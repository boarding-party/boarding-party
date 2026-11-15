# Daily digest email

Status: approved 2026-11-15.

## Why

Today you learn what happened only by opening each board. The digest puts
the day's news in one email each morning, so people check the board when
something needs them and not otherwise. It's the cheapest way to bring
people back, and it's the cloud's first feature that the open-source board
alone doesn't have.

## What the reader gets

One email per person per morning, covering every project in their accounts.
Each line links to the thing on the project's board.

```
Subject: boardingparty: 2 new bugs, 1 journey broke (Shop staging)

Shop staging (shop.p.bpboards.com)
  New bugs
    - VAT dropdown cuts off "20% (standard)"     https://shop.p.bpboards.com/#/bugs/9f2c
    - Invoice PDF total ignores discount         https://shop.p.bpboards.com/#/bugs/a41e
  Fix checks
    - passed: Login loops after password reset   https://shop.p.bpboards.com/#/bugs/77d0
  Journeys
    - broke: Sam Taylor / keyboard invoice       https://shop.p.bpboards.com/#/journeys/e358
  Runs: 6 finished, 1 stuck                      https://shop.p.bpboards.com/#/runs/f15a
  Spent yesterday: $1.20 of $5.00

Blog (blog.p.bpboards.com)
  Couldn't reach the board since 03:10. Check the cluster "hetzner-1".

Stop these emails: https://bpboards.com/digest/off?t=...
```

What goes in, per project, for the last day:

- **New bugs:** issues first found in the window.
- **Fix checks:** passed (the bug stays fixed) or failed (the bug is open
  again).
- **Journeys:** ones that broke (a replay failed) and ones that got repaired
  or rolled back.
- **Runs:** how many finished, plus each run that got stuck or failed.
- **Spend:** yesterday's spend against the daily budget. It's flagged when
  over budget or when the board is paused.
- **Unreachable board:** a project whose board couldn't be read gets one
  line saying since when and which cluster.

## Goals

- One plain-text email per user per day, at 07:30 Europe/London.
- No email on a day when none of a person's projects had news. Quiet days
  stay quiet.
- One-click opt-out: a link in every email, a `List-Unsubscribe` header,
  and a toggle on the account page.
- Lists are capped at 10 lines per section, followed by "and 14 more" with a
  link to the list on the board.

## Non-goals

- Per-project schedules, time zones per person, or a choice of frequency.
- Slack, webhooks or real-time alerts.
- An HTML design. The mailer sends plain text today; HTML can come later.
- A digest for open-source installs without the cloud. They have the board
  and no mailer.

## Anti-goals

- An email that's noise: a daily message full of zeros teaches people to
  filter it out.
- Run content in email: no screenshots, page text, persona mailboxes, keys
  or site passwords. Titles and names only.
- Run data stored in the cloud. The cloud spec says run data stays in the
  namespace. The cloud asks each board in the morning and stores only that it
  sent the email.

## How it works

### Director: `GET /api/digest?since=<RFC 3339>`

A read-only admin API endpoint. The director decides what counts as news,
so the cloud never infers it from raw lists. The response:

```json
{
  "since": "...", "until": "...",
  "newBugs":   {"items": [{"id": "", "title": "", "severity": ""}], "more": 0},
  "fixChecks": {"items": [{"issueId": "", "title": "", "passed": true}], "more": 0},
  "journeys":  {"broke":    {"items": [{"id": "", "persona": "", "story": ""}], "more": 0},
                "repaired": {"items": [...], "more": 0}},
  "runs":      {"finished": 6, "stuck": {"items": [{"id": "", "persona": "", "story": "", "state": ""}], "more": 0}},
  "spent": 1.20, "budget": 5.00, "overBudget": false, "paused": false
}
```

Where each part comes from in the director's store:

- **New bugs:** issues whose first report is in the window (`FirstFinds`).
- **Fix checks:** decisions of kind `fix check`, with the verdict the
  check settled. The store keeps no status history, so "reopened" means a
  failed fix check. A bug reopened by hand doesn't show.
- **Journeys broke:** journeys whose replay failed in the window (the
  `replay_failed` run context, which moves the journey to `repairing`).
- **Journeys repaired:** copilot decisions `site changed` or
  `journey weakness` (each carries a new proven revision), and `rolled back`.
  A copilot `bug` decision is already counted under new bugs.
- **Runs:** runs finished in the window. Stuck or failed runs are listed.
- **Spend:** from the daily spend the budget already uses.

`since` is capped at 7 days back.

### Cloud: the morning job

1. At 07:30 Europe/London, one cloud replica takes a Postgres advisory lock
   and runs the job. Other replicas skip.
2. For each linked project, it calls `GET /api/digest` once over the
   existing on-demand forward, with `X-BP-Actor: digest`. `since` is the
   time of the last digest sent for that project, or 24 hours ago. A board
   that can't be read becomes the "couldn't reach" line, reusing the
   project-card summary states.
3. For each user with the digest on, it collects the projects in their
   accounts and drops projects with no news. If nothing is left, it sends
   nothing. Otherwise it renders the email and sends it through the existing
   `auth.Sender`.
4. It records `(user, day)` in `digest_sends`, so a restarted job doesn't
   send twice.

Schema additions:

- `users.digest boolean not null default true`
- `digest_sends(user_id, day date, sent_at timestamptz, primary key (user_id, day))`

The opt-out link carries a signed token (user id plus purpose) and works
without signing in.

## Testing

- **Director:** handler tests against a seeded store. Each section has its
  own test, and there's one for an empty window. A test checks that no
  section carries report bodies or screenshots.
- **Cloud:**
  - A golden-file test of the rendered email.
  - A test that a user with all-quiet projects gets nothing.
  - A test that a second run of the job on the same day sends nothing.
  - A test that the opt-out link turns the digest off and a forged token is
    refused.
- **Live:** point the cloud's SMTP at hello@tinysystems.io, link
  `boardingparty-test`, run the job by hand once, and read the email.

## Order of work

1. Director endpoint in the open-source repo, then tag a release.
2. Cloud: bump the pin to that release, then add the job, schema, renderer
   and opt-out.
3. Live check as above.

## My calls (change any of them)

- **Recipients:** every member gets the digest, not only owners, with
  opt-out. Members are the ones triaging bugs.
- **Time:** 07:30 Europe/London for everyone. Per-person time zones wait for
  someone to ask.
- **Window:** since the last digest. If one is missed, the next covers the
  gap, up to 7 days.
- **Currency:** dollars, as the board shows spend.
- **Format:** plain text, matching the sign-in mails.
