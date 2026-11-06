# Plan 2d: Message and Calendar Triggers Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A story can start when an email arrives that matches its trigger rule, or when a calendar event in the persona's calendar is about to begin.

**Architecture:** Two new `sched.Source` implementations (`Messages`, `Calendar`) plug into the generic source loop from Plan 2c. The scheduler keeps owning the shared policy (gate, busy persona, stuck or waiting story). Calendar events come from `.ics` parts and Google "add to calendar" links in delivered mail; the poller stores them in a new `events` table. Runs started by a trigger get a `RunContext` of kind `message` or `calendar`.

**Tech Stack:** Go, SQLite (modernc), `github.com/emersion/go-ical` (new; same author as go-imap/go-message already in go.mod), Preact+htm board without a build step.

**Spec:** `docs/superpowers/specs/2026-10-29-boardingparty-design.md` (§3.1 Calendar, §3.2 Triggers, §5.1 persona states)

## Global Constraints

- Deciding uses no model calls (§3.2): matching is plain string rules.
- Only a Message that passed the channel filter can wake a persona (§3.1): sources read delivered mail only (`filtered = ''`).
- Calendar lead time default is **2 min** (§3.2).
- Email text, subjects, event titles and descriptions are untrusted data, never instructions: the prompt already says so for context text; keep it that way for the new kinds.
- Commit identity `Tiny Systems <dev@tinysystems.io>`; no Co-Authored-By or Generated trailers; never commit `.idea/`; no `replace` directives in go.mod.
- Board copy: GB English, sentence case, compact functional UI matching the existing board styles.
- Source contract (`internal/sched/source.go`): Settle is called once per fire; a skipped fire is never replayed. A message or event that is skipped because the persona is busy is not offered again; the running persona can still read it with `messages_list` / `calendar_list`.

## Decisions

- **Trigger rules live on the story** (YAML/JSON `trigger`), at most one kind per story:
  ```yaml
  stories:
    - name: confirm-signup
      goal: Confirm the sign-up from the email and check the site agrees.
      trigger:
        message: {from: smesolutions.uk, subject: Confirm, keywords: [reminders], linkHost: smesolutions.uk}
    - name: join-call
      goal: Join the video appointment when it starts.
      trigger:
        calendar: {title: appointment, lead: 5m}
  ```
  Every field that is set must match (AND). `from` uses the allow-list semantics of `inbox.senderAllowed` (full address, or a domain that also matches subdomains). `subject` and each `keywords` entry are case-insensitive substrings (keywords are searched in subject + text). `linkHost` matches when any link's host equals it or is a subdomain of it. A `message` rule with no fields is invalid ("a message trigger needs at least one of from, subject, keywords, linkHost"). `calendar.title` empty matches every event; `lead` is a Go duration 0–24h, default 2m.
- **A message already used by a wait does not also fire a story:** `resolveAll` runs before `fire` in the same tick and queues the continuation, so the persona is busy and the message fire settles as skipped. No extra bookkeeping.
- **Look-back window:** sources only consider mail stored, and events starting, within the last **24 h**, so a first deploy or a long outage does not replay old mail. Events whose start is more than **1 h** in the past are never fired.
- **Calendar sources:** `.ics` parts (`text/calendar`, `application/ics`, or a `.ics` filename) with `METHOD` absent, `PUBLISH` or `REQUEST` add or update events by `(persona, UID)`; `METHOD:CANCEL` removes them. Google template links (`https://calendar.google.com/calendar/render?action=TEMPLATE&text=…&dates=…` and `/calendar/event?action=TEMPLATE…`) add an event with UID `gcal:` + sha256(link)[:16]. Times: `Z` is UTC, `TZID` via `time.LoadLocation` (fall back to the project time zone if unknown), floating times and all-day `DATE` values use the project time zone; all-day events start at 09:00 local.
- **Run context:** `store.RunContext` gains kinds `message` and `calendar` with `MessageID`, `Arrived` ("Email from X: subject") reused for message, and a new `Event *ContextEvent{Title, Start, Location, URL}` for calendar.

## Review Focus

1. A burst of two matching emails for one persona in one tick: the first fires, the second is settled as skipped, never both queued (test in Task 3).
2. An email that resolves an open email wait must not also start a triggered story (test in Task 3).
3. An invite re-sent with a new time (same UID) moves the event and lets it fire again at the new time; a CANCEL removes it before it fires (test in Task 4).
4. An `.ics` with a `TZID` the container lacks, or a floating time, lands in the project time zone, not UTC (test in Task 4).
5. A story with a trigger also stays eligible for schedule and manual starts; a stuck or waiting story is not started by a trigger (test in Task 3).

---

### Task 1: Story trigger rules

**Files:**
- Modify: `internal/project/project.go` (Story, validate)
- Create: `internal/project/trigger.go`, `internal/project/trigger_test.go`
- Modify: `internal/inbox/route.go` (export the sender check)

**Interfaces:**
- Produces:
  - `type Trigger struct { Message *MessageRule \`json:"message,omitempty"\`; Calendar *CalendarRule \`json:"calendar,omitempty"\` }`
  - `type MessageRule struct { From string; Subject string; Keywords []string; LinkHost string }` (json: `from`, `subject`, `keywords`, `linkHost`, all omitempty)
  - `type CalendarRule struct { Title string; Lead string }` (json `title`, `lead`, omitempty)
  - `Story.Trigger *Trigger \`json:"trigger,omitempty"\``
  - `func (r MessageRule) Matches(from, subject, text string, links []string) bool`
  - `func (r CalendarRule) Matches(title string) bool`
  - `func (r CalendarRule) LeadTime() time.Duration` (2m when empty)
  - `func SenderMatches(from string, allow []string) bool` in package project (moved from `inbox.senderAllowed`, which now calls it), so project does not import inbox.

- [ ] **Step 1: Write failing tests** in `trigger_test.go`:
  - `TestMessageRuleMatches`: table with `{From:"smesolutions.uk"}` matches `hello@smesolutions.uk` and `a@mail.smesolutions.uk`, not `x@evil-smesolutions.uk`; `{Subject:"confirm"}` matches "Confirm Martyn's Law reminders"; `{Keywords:["reminders","hall"]}` needs both, searched in subject+text; `{LinkHost:"smesolutions.uk"}` matches link `https://www.smesolutions.uk/r?x` and not `https://smesolutions.uk.evil.test/`; combined rule fails if any one field fails.
  - `TestCalendarRule`: empty title matches anything; `{Title:"Appointment"}` matches "Video appointment with Dr Lee"; `LeadTime()` is 2m when empty and 5m for `"5m"`.
  - `TestTriggerValidation` (via `project.Load` on YAML): rejects both kinds set ("a story trigger has either message or calendar, not both"), an empty message rule, a lead of `-1m`, `25h` or `soon`, a linkHost containing `/` or `:`.
- [ ] **Step 2:** `go test ./internal/project/ -run 'Trigger|MessageRule|CalendarRule'` — FAIL (undefined).
- [ ] **Step 3:** Implement the types, matchers and validation (validation runs inside `Project.validate` for every story; error text prefixed with `role "<r>" story "<s>": `). Move `senderAllowed` to `project.SenderMatches` and update `inbox/route.go`.
- [ ] **Step 4:** `go test ./internal/project/ ./internal/inbox/` — PASS.
- [ ] **Step 5:** Commit `feat(project): story trigger rules for messages and calendar events`.

### Task 2: Store support for fired messages, events and run context

**Files:**
- Modify: `internal/store/schema.sql`, `internal/store/store.go` (RunContext)
- Create: `internal/store/events.go`, `internal/store/events_test.go`, `internal/store/fired_test.go`
- Modify: `internal/store/messages.go`

**Interfaces:**
- Produces:
  - Table `message_fired(message_id TEXT PRIMARY KEY, story TEXT NOT NULL DEFAULT '', at INTEGER NOT NULL)`.
  - `func (s *Store) UnfiredMessages(since time.Time, limit int) ([]Message, error)` — delivered (`filtered = ''`), `persona != ''`, `stored >= since`, not in `message_fired`, oldest first (stored, rowid).
  - `func (s *Store) MarkMessageFired(id, story string, at time.Time) error` (insert or ignore).
  - `func (s *Store) MessageFired(ids []string) (map[string]string, error)` — id → story for the board.
  - Table `events(persona TEXT, uid TEXT, title TEXT, start INTEGER, end INTEGER, location TEXT, url TEXT, message_id TEXT, fired_start INTEGER NOT NULL DEFAULT 0, fired_story TEXT NOT NULL DEFAULT '', PRIMARY KEY(persona, uid))`.
  - `type Event struct { Persona, UID, Title string; Start, End time.Time; Location, URL, MessageID string; FiredStart time.Time; FiredStory string }`
  - `func (s *Store) UpsertEvent(e Event) error` — on conflict updates fields; `fired_start` is kept, so an event fires again only when its start changed (`fired_start != start`).
  - `func (s *Store) DeleteEvent(persona, uid string) error`
  - `func (s *Store) Events(persona string, from, to time.Time) ([]Event, error)` — persona "" = all; ordered by start.
  - `func (s *Store) DueEvents(now time.Time, maxLead time.Duration) ([]Event, error)` — `start <= now+maxLead`, `start >= now-1h`, `fired_start != start`.
  - `func (s *Store) MarkEventFired(persona, uid string, start time.Time, story string) error`
  - `RunContext` gains `Event *ContextEvent \`json:"event,omitempty"\``; `type ContextEvent struct { Title string; Start time.Time; Location, URL string }` (json lower-camel). Kind comment becomes `"continue" | "handoff" | "message" | "calendar"`.

- [ ] **Step 1: Write failing tests:**
  - `TestUnfiredMessages`: three messages (one filtered, one with no persona, one delivered); only the delivered one is returned; after `MarkMessageFired` it is gone; `since` excludes older ones; order is oldest first.
  - `TestEventsUpsertRefires`: upsert event start 10:00, `DueEvents(09:59, 2m)` returns it; `MarkEventFired(…, 10:00, "s")`; `DueEvents` now empty; upsert same UID with start 11:00; `DueEvents(10:59, 2m)` returns it again; `DeleteEvent` removes it.
  - `TestDueEventsSkipsStale`: event started 2h ago is not due.
- [ ] **Step 2:** `go test ./internal/store/ -run 'Unfired|Events|DueEvents'` — FAIL.
- [ ] **Step 3:** Implement (schema uses `CREATE TABLE IF NOT EXISTS`, like the rest; timestamps via the existing `ts`/`fromTS` helpers).
- [ ] **Step 4:** `go test ./internal/store/` — PASS.
- [ ] **Step 5:** Commit `feat(store): fired-message log, persona calendar events, trigger run context`.

### Task 3: Message trigger source

**Files:**
- Create: `internal/sched/messages.go`, `internal/sched/messages_test.go`
- Modify: `cmd/director/main.go` (Sources), `internal/launcher/prompt.go` (context kinds)

**Interfaces:**
- Consumes: Task 1 `Story.Trigger`, `MessageRule.Matches`; Task 2 `UnfiredMessages`, `MarkMessageFired`, `RunContext`.
- Produces: `type Messages struct { Store *store.Store }` implementing `Source` with `Name() == "message"`. `const LookBack = 24 * time.Hour`.

Due: load `UnfiredMessages(now-LookBack, 200)`. For each message, find the persona's role; collect, in role order, the stories whose `Trigger.Message` matches. No match: `MarkMessageFired(id, "", now)` inside Due and emit nothing. Match: emit `Fire{Source:"message", Persona, Role, Stories: matches, Reason: "Email: " + clip(subject, 80), Context: json(RunContext{Kind:"message", MessageID: id, Arrived: "Email from <from>: <subject>"})}`. Keep the fire→message id map for Settle (like `Schedule` caches its state). Settle: `MarkMessageFired(id, story, now)`.

- [ ] **Step 1: Write failing tests** (use the existing `newFix` fixture with a project YAML that has a mailbox persona and a `trigger.message` story):
  - `TestMessageFiresMatchingStory`: a delivered message from `hello@smesolutions.uk` subject "Confirm your sign-up" queues the story with `Reason` "Email: Confirm your sign-up" and a context whose kind is `message` and `messageId` is the message id; a second tick queues nothing more.
  - `TestMessageNoMatchIsMarked`: a non-matching message queues nothing and is not offered again (`UnfiredMessages` empty).
  - `TestMessageBurstFiresOnce`: two matching messages for one persona in one tick: one queue item, both marked fired (second with story "").
  - `TestMessageResolvingWaitDoesNotTrigger`: a run waiting on email with that subject; after one `Tick`, the queue holds exactly one item and it is the continuation (`Via == "continue"`).
  - `TestMessageRespectsStuckAndPaused`: last run of the story stuck → nothing queued; paused gate → nothing queued and the message is marked fired.
- [ ] **Step 2:** `go test ./internal/sched/ -run Message` — FAIL.
- [ ] **Step 3:** Implement `Messages`; add it to `sch.Sources` in `cmd/director/main.go`; in `prompt.go` extend the context sentence: "…or picks up a hand-off from another persona (from says who and what they did), or starts because an email arrived (messageId: read it with message_read first) or a calendar event is about to start (event)." Update any prompt golden test.
- [ ] **Step 4:** `go test ./...` — PASS; `go vet ./...` clean.
- [ ] **Step 5:** Commit `feat(sched): start stories from matching email`.

### Task 4: Calendar events from mail

**Files:**
- Create: `internal/inbox/calendar.go`, `internal/inbox/calendar_test.go`, `internal/inbox/testdata/invite-request.eml`, `testdata/invite-cancel.eml`, `testdata/gcal-link.eml`
- Modify: `internal/inbox/message.go` (Message gains `Events []Event`), `internal/inbox/parse.go` (collect calendar parts), `internal/inbox/poller.go` (store events for delivered mail), `go.mod`/`go.sum` (`github.com/emersion/go-ical`)

**Interfaces:**
- Consumes: Task 2 `UpsertEvent`, `DeleteEvent`.
- Produces:
  - `type Event struct { UID, Title string; Start, End time.Time; Location, URL string; Cancel bool }` in package inbox.
  - `func ParseICS(b []byte, loc *time.Location) ([]Event, error)`
  - `func GoogleLinkEvent(link string, loc *time.Location) (Event, bool)`
  - `Parse(raw)` keeps its signature; it stores raw calendar parts on the Message (unexported field) and the poller resolves them with the project location once the persona is known: `func (m Message) CalendarEvents(loc *time.Location) []Event` (ics parts first, then Google links found in `Links`; at most **20** events per message; bad parts skipped).

- [ ] **Step 1: Write failing tests:**
  - `TestParseICSRequest`: fixture with `METHOD:REQUEST`, `DTSTART;TZID=Europe/London:20261110T143000` → Start `2026-11-10T14:30:00Z`, Title from SUMMARY, Location, URL, UID.
  - `TestParseICSCancel`: `METHOD:CANCEL` → one event with `Cancel: true`.
  - `TestParseICSUnknownTZIDAndFloating`: `TZID=Made/Up` and a floating `20261110T090000` both land at 09:00 in the passed location (`Europe/London`).
  - `TestParseICSAllDay`: `DTSTART;VALUE=DATE:20261110` → 09:00 local.
  - `TestGoogleLinkEvent`: `https://calendar.google.com/calendar/render?action=TEMPLATE&text=Video+appointment&dates=20261110T143000Z/20261110T150000Z&location=Online` → title, start, end, location, UID prefix `gcal:`; a non-template link → false.
  - `TestPollerStoresEvents` (poller_test style with the fake IMAP fixture): delivered invite → `store.Events` has it for the persona; the same invite **filtered** stores nothing; a later CANCEL deletes it.
- [ ] **Step 2:** `go test ./internal/inbox/ -run 'ICS|Google|PollerStoresEvents'` — FAIL.
- [ ] **Step 3:** `go get github.com/emersion/go-ical@latest`; implement. Calendar parts are read with the same `maxRead` cap as body parts.
- [ ] **Step 4:** `go test ./internal/inbox/ ./internal/store/` — PASS.
- [ ] **Step 5:** Commit `feat(inbox): persona calendar from .ics invites and add-to-calendar links`.

### Task 5: Calendar trigger source, calendar_list tool, status

**Files:**
- Create: `internal/sched/calendar.go`, `internal/sched/calendar_test.go`
- Modify: `cmd/director/main.go`, `internal/mcpserver/server.go` (+test), `internal/runs/cost.go` (Status due), `internal/runs/status_test.go` or nearest existing test

**Interfaces:**
- Consumes: Task 1 `CalendarRule`, Task 2 `DueEvents`, `MarkEventFired`, `Events`, `ContextEvent`.
- Produces:
  - `type Calendar struct { Store *store.Store }`, `Name() == "calendar"`.
  - MCP tool `calendar_list` (no args): the persona's events from now-1h to now+14 days, fields `title, start, end, location, url` (start/end RFC 3339 in UTC plus a `local` string in the project time zone, `Mon 10 Nov, 14:30`).
  - `DueView` gains `Reason string \`json:"reason,omitempty"\``; Status lists calendar events of the next 24 h whose persona has a story with a calendar trigger, at `start - lead`, reason `"Calendar: <title>"`, merged with schedule dues, soonest first.

Due: `maxLead` = the largest `LeadTime()` over all calendar-trigger stories (none → return nothing). For each `DueEvents(now, maxLead)` event, collect the persona's stories whose rule matches the title and whose `start - lead <= now`. None (rule not matched or lead not reached): if no story could ever match → `MarkEventFired(…, "")`; if one matches but its lead is not reached, leave it. Emit `Fire{Source:"calendar", Persona, Role, Stories, Reason: "Calendar: " + clip(title, 80), Context: RunContext{Kind:"calendar", Event:{…}}}`. Settle: `MarkEventFired(persona, uid, start, story)`.

- [ ] **Step 1: Write failing tests:**
  - `TestCalendarFiresAtLead`: event at 10:00, story lead 5m: tick at 09:54 queues nothing; at 09:55 queues the story with reason "Calendar: Video appointment" and context kind `calendar`; at 09:56 nothing more.
  - `TestCalendarRescheduledFiresAgain`: after firing, upsert same UID at 11:00; tick 10:58 queues again.
  - `TestCalendarUnmatchedTitleMarked`: rule `title: appointment`, event "Team lunch" → nothing queued, `DueEvents` no longer returns it.
  - `TestCalendarListTool` (mcpserver): returns only the calling persona's events, in order.
  - `TestStatusIncludesCalendarDue`: status `due` holds the event at start-lead with its reason, after an earlier schedule due.
- [ ] **Step 2:** `go test ./internal/sched/ ./internal/mcpserver/ ./internal/runs/ -run 'Calendar|StatusIncludesCalendar'` — FAIL.
- [ ] **Step 3:** Implement; register `&sched.Calendar{Store: st}` in main after `Messages`.
- [ ] **Step 4:** `go test ./... && go vet ./...` — PASS.
- [ ] **Step 5:** Commit `feat(sched): start stories from calendar events; calendar_list tool`.

### Task 6: Board

**Files:**
- Modify: `internal/board/ui/project.js` (story editor), `internal/board/ui/app.js` (Later list reason, Inbox "Started" note), `internal/board/ui/styles.css`
- Modify: `internal/api/inbox.go` (messages list includes `started` story from `MessageFired`) + test

**Interfaces:**
- Consumes: Task 1 JSON shape, Task 2 `MessageFired`, Task 5 `DueView.Reason`.

- [ ] **Step 1: Write failing API test** `TestMessagesListShowsStartedStory`: after `MarkMessageFired(id, "confirm-signup", t)`, `GET /api/messages` returns that message with `"started":"confirm-signup"`; a message marked with "" has no `started` field.
- [ ] **Step 2:** `go test ./internal/api/ -run StartedStory` — FAIL; then implement; PASS.
- [ ] **Step 3: Story editor.** Under each story, a "Starts on its own" select: `Only by schedule or by hand` (no trigger) / `When an email arrives` / `When a calendar event starts`. Email shows four inputs in the existing `.fields` grid: From (placeholder `smesolutions.uk`), Subject contains, Words in the email (Tags, `Add word`), Link to (placeholder `smesolutions.uk`), with hint "Every filled box must match. Only mail that passed Accept mail from counts." Calendar shows Title contains (placeholder `appointment`) and "Start this many minutes early" (number, default 2, 0–1440, stored as `"<n>m"`). Switching kind clears the other rule. Validation errors from the API show as today.
- [ ] **Step 4: Later list and Inbox.** The Later list shows `d.reason` when present (else today's text). The Inbox message row shows a small `Started <story>` tag when `started` is set.
- [ ] **Step 5: Check in a browser** with `go run ./cmd/boardingparty board --dev-director --no-open --port 3399`: set a message trigger, save, reload, the fields round-trip; set a calendar trigger with 5 minutes, round-trips as `5m`; phone width (390 px) has no horizontal scroll.
- [ ] **Step 6:** `go test ./...` — PASS. Commit `feat(board): story triggers editor; trigger reasons on the board`.

### Task 7: Deploy and live check

- [ ] **Step 1:** Push main, wait for CI (test, worker-smoke, images) green on the commit.
- [ ] **Step 2:** `go build -o $S/bp ./cmd/boardingparty && CLOUDSDK_ACTIVE_CONFIG_NAME=tinyplatform $S/bp setup -n boardingparty-test --context gke_tinyplatform_europe-west2-a_cluster-1 --image-tag sha-<short>`.
- [ ] **Step 3: Message trigger live.** Back up the project. Split Megan's story into `martyns-signup` (make the pack and sign up, nothing else) and `martyns-confirm` with `trigger.message: {from: hello@tinysystems.io, subject: "Confirm Martyn's Law"}` and goal "Confirm the reminders from the email, check the site says they are on, then use the stop link in the welcome email and check the site says you are unsubscribed." Start `martyns-signup` by hand. Pass: a second run of `martyns-confirm` starts on its own with reason "Email: Confirm Martyn's Law reminders…", its context kind is `message`, it ends done, and the Inbox shows `Started martyns-confirm` on that email.
- [ ] **Step 4: Calendar trigger live.** Needs an invite only a human can send: ask the user to send a Google Calendar invite from `hello@tinysystems.io` to `info+megan@smesolutions.uk`, titled "Hall safety walk-round", starting about 15 minutes ahead. Give Megan a story `walk-round` with `trigger.calendar: {title: walk-round, lead: 2m}` and a short goal on smesolutions.uk. Pass: the event appears in the Later list as "Calendar: Hall safety walk-round" at start-2m, the run starts then with context kind `calendar`, and ends done.
- [ ] **Step 5:** Restore the project from the backup; report.
