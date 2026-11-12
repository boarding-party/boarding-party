# Journeys: AI writes the test, Playwright runs it

12 November 2026. Design agreed in conversation; calls marked "(my call)"
were made by the author and are open to correction.

## Why

Today every scheduled run is Claude driving a browser through Playwright
MCP. Each run costs tokens, and two runs of the same story can go
differently depending on the model's "mood". A failing scheduled run should
mean the site changed, not that the model wandered.

The AI moves to three jobs:

1. **Discovery and writing.** A persona explores a story as it does now,
   and records what it did as a Playwright journey: a script with
   expressions and generated data in place of fixed values.
2. **Replay.** Scheduled reruns run the journey with no model. Cheap and
   the same every time apart from the generated data.
3. **Copilot.** When a replay fails, the AI is called in with the journey,
   the failed step, the inputs and the evidence. It decides: real bug
   (report it as today) or the journey needs changing (write a new
   revision).

Judgement bugs (confusing wording, unreadable print, misleading messages)
still come from AI runs only: discovery, weekly fresh exploration and
copilot runs. A replay cannot see them.

## Goals

- Scheduled runs are replays by default once a story has a journey.
- Every AI run ends with a journey revision of what it did, or a recorded
  reason why the existing revision still stands.
- A failed replay brings in the AI automatically; a repaired journey is
  live again without anyone acting.
- A full audit trail of every AI decision about a journey, with its reason
  and evidence. This is a project-wide rule: all data is kept so the
  product can evolve on it.
- Replays use generated data that fits the persona, with edge cases mixed
  in, and every replay can be repeated exactly from its seed.

## Non-goals

- Users writing or editing journey code on the board. They can read it,
  pause, replay, roll back and delete.
- Replacing AI exploration. Fresh AI runs stay, weekly by default.
- Browsers other than the Chromium the worker uses today.
- One journey spanning several personas (a handoff chains journeys).

## Anti-goals (a failure even if it works)

- Flaky replays that wake the AI on most runs: tokens and noise come back.
- The copilot repairing a journey over a real bug, so the bug is hidden.
  When unsure, it reports the bug and keeps the revision.
- Token spend the user did not expect: pausing a journey never falls back
  to AI runs.
- Any AI decision about a journey without a recorded reason.

## Words

- **Journey**: the saved Playwright script for one persona doing one
  story. The user-facing word; "script" is not used in the UI because it
  sounds like the user must write code.
- **Revision**: one version of a journey. Journeys keep every revision.
- **Replay**: a run that executes a journey revision with no model. It is
  still a run on the Runs page.
- **AI run**: a run where Claude drives the browser, as today.

The code already uses "journey" for a chain of runs (`Run.Journey`,
`Queued.Journey`, `issues.check_journey`, JSON key `journey`); the UI
calls that a "Chain". Rename the existing concept to **chain** in Go, SQL
and JSON so "journey" means one thing (my call). Breaking the JSON key is
fine: the board and the cloud are the only consumers.

## The journey entity

One journey per (persona, story) (my call: the data and the account belong
to the persona). Fields: persona, story, state, created, the live revision,
and counters (replays passed and failed, last replay, last fresh AI run of
the story).

States: **live** (scheduled replays use it), **paused** (nothing runs for
that persona and story; no AI fallback), **repairing** (a copilot run is
under way; scheduled replays wait for it).

Revisions: number, the code, parent revision, the run that wrote it, the
reason ("first recording", "Save button renamed: step 4 updated",
"date was fixed to 12 Nov; now computed from today"), the data generators
it uses, created. Never deleted while the journey exists. Deleting the
journey deletes its revisions and keeps its decisions in the audit trail.

A journey's replays use the newest revision by default. A user can replay
any older revision and roll back (roll back makes a new revision copying
the old code, reason "rolled back to revision N by <actor>", so the history
only grows).

## How an AI run records a revision

The agent drives the browser as now. Playwright MCP already reports the
code of each action it runs; that is the raw material. Before `finish_run`
the agent:

1. Writes the journey module from what it did: steps named after the
   story, fixed values replaced by data expressions, checks for what it
   saw (see "Journey format").
2. Replays it once inside its own pod against a fresh browser context and
   a new seed (`bp-replay`, shipped in the worker image). It fixes and
   repeats until it passes, or gives up and says why.
3. Saves it with a new MCP tool `save_journey` (code, reason, generators,
   the self-replay result). The director stores a revision only if the
   self-replay passed (my call: an unproven revision would make the first
   scheduled replay fail and wake the copilot).

Cases:

- The run finished the story: a full revision.
- The run stopped at a bug: a **bug revision** up to the step where the
  bug shows, ending in a declared check that fails while the bug is there.
  Its self-replay passes when it fails exactly at that declared step and
  check. A bug revision never becomes the live revision (my call): the
  journey keeps replaying its full live revision, and the bug revision
  belongs to the bug report, for fix checks (below). If the journey has no
  live revision yet, scheduled runs of that story stay AI runs.
- The run did the same as the live revision: no new revision; a
  "confirmed" decision with a short reason.
- The agent could not produce a passing journey: no revision; a
  "not recorded" decision with the reason.

Every one of these is a decision row (see "Audit trail").

## Journey format

An ES module run by `bp-replay` with Playwright (the library already in
the worker image, not the MCP server):

```js
export default async function journey({ page, data, bp, expect }) {
  const biz = data.business();             // generated, fits the persona
  await bp.step('Open the invoice form', async () => { ... });
  await bp.step('Fill the business', async () => {
    await page.getByLabel('Business name').fill(biz.name);
  });
  const lines = data.lineItems({ min: 1, max: 6 });
  await bp.step('Check the total', async () => {
    await expect(page.getByTestId('total')).toHaveText(bp.money(bp.totalWithVat(lines)));
  });
  const mail = await bp.waitFor({ email: { subject: /Confirm/ }, within: '2h' });
  await bp.step('Confirm from the email', async () => { await page.goto(mail.link(/confirm/)); });
}
```

- `bp.step(name, fn)`: named steps; a failure is reported as "failed at
  step N: <name>" with a screenshot.
- `data.*`: generators (below). Every value drawn is logged with the seed.
- `expect`: Playwright's. Checks computed from inputs, never fixed strings
  copied from one run.
- `bp.waitFor(...)`: the same waits the director has today (email,
  handoff, time). A replay stops there as "waiting"; the director parks it
  exactly like an AI run's wait; the continuation is a replay of the same
  revision that resumes after that `waitFor`, with the arrived message
  available. How resumption is implemented (re-running from the top with
  completed parts skipped, or splitting the module into parts at save
  time) is the builder's choice.
- Waits use "until visible" style; no sleeps.
- Browser state: the replay starts from the persona's saved profile like
  an AI run, and saves it back at the end (my call).

## Generated data

**Fixed vs generated.** The persona's identity is fixed: name, the
business they run, their account, their email, from the persona and its
saved profile. Generated per replay: the details of each transaction (an
invoice's customer and line items, dates, a venue's capacity, a booking's
time). Stories that create an account or a record use a fresh
plus-address on the persona's inbox per replay (`info+megan-<seed>@…`)
(my call); a story can opt out of replays with `replay: false` in the
project when the site must not collect such records.

Generators per field, seeded: business name, person name, UK address and
postcode, phone, VAT number, company number, line items, prices,
quantities, dates relative to today. Library choice is the builder's
(a pinned faker with en_GB locale is fine).

- **Fits the persona**: generators take the persona's profile (a
  bookkeeper gets long company names and many line items; a sole trader
  one item and a round price). The persona's notes and profile fields are
  the input.
- **Edge cases**: a fixed list (O'Brien, Zoë Ågren, a 120-character
  company name, £0.01, £999,999.99, quantity 0, 29 February, a due date
  before the invoice date, leading spaces, pasted line breaks). Each replay
  mixes in one or two (my call: 30% of replays, so most replays stay on
  the happy path).
- **Seed**: every replay records its seed and the values drawn; the board
  shows them on a failed replay and "Replay with these inputs" repeats it.
- **Limits**: emails only as plus-addresses on the persona's inbox; data
  goes only into the story's own forms. Same egress fence as AI runs.
- A rejected edge-case input is not automatically a bug (the site may be
  right to refuse quantity 0). It goes to the copilot like any failure.
  When the copilot decides the site was right, it writes a new revision
  that excludes that case for that field (decision kind **input refused
  correctly**, my call), so the same case never wakes it again.

## Replays

A replay is a run with `kind: "replay"`, the journey and revision it ran,
the seed. It reports passed, failed at step N (error, screenshot, trace,
console, inputs), or waiting. It has no model usage, so its cost is $0 in
the spend panel (compute is not counted). Replays still run when the
day's budget is spent; pause stops them like everything else (my call).
They type and move at human pace like AI runs: the same `humanise.mjs`
patch applies to the Playwright core `bp-replay` uses (my call).

Replays run with the same isolation as AI runs: the egress fence, resource
limits, node pool, live screen and artifact upload. Decided: tiny gains a
"command" session agent (a session that runs a given command in the image,
no model, no Claude Code injected) with the same proxy, network policy,
security context, pool and limits as agent sessions. Released in tiny
first, then boardingparty bumps the dependency and launches replays with
it. tiny (tiny-systems/tiny) is ours and in scope.

Flake guard (my call): a failed replay is run once more with the same
seed before the copilot is called. Both fail: copilot. Second passes:
recorded as "flaky" on the journey; three flaky replays in a row call the
copilot to harden the journey.

## The copilot

A failed replay (after the flake guard) queues an AI run of the same
persona and story with a new context kind `replay_failed`: journey,
revision, failed step, error, screenshot, trace, inputs and seed, and the
journey's recent history. The journey is "repairing" until it settles.

The copilot reproduces the failure in the browser and decides, through a
new MCP tool `decide_journey`:

- **bug**: the site misbehaves. It reports the bug as today (linked to the
  revision and step) and the journey stays as it is. If the bug blocks the
  rest of the story, the journey keeps failing at that step until the bug
  is fixed; repeated failures at the same step against an open issue do
  not call the copilot again (my call).
- **site changed**: the page changed legitimately. It writes a new revision
  (same self-replay rule) with the reason.
- **journey weakness**: the journey was not prepared for something that
  changes with time (fixed dates, data left from earlier runs, ordering,
  time zones). New revision with the reason. The AI may also make this
  decision on any later AI run of the story when it notices a weakness.
- **flaky**: the site and the journey are fine; the failure was timing.
  It hardens the waits in a new revision.
- **input refused correctly**: see "Generated data".

Each decision states its reason and evidence (screenshots, the element
that changed, what it compared). "Site changed" requires saying why this
is not a bug. When unsure: bug.

## Scheduling

When a persona is due and the scheduler picks a story:

- story has a live journey and the story's last fresh AI run is within the
  role's exploration interval: **replay**;
- no journey, or the interval has passed: **AI run** (it records or
  confirms a revision);
- journey paused: nothing runs for that persona and story.

Exploration interval: a new role field (my call: `explore`, default
weekly, parsed like `activity`). Copilot runs do not reset the interval;
fresh AI runs do.

Start run on the board gains a choice: AI run, or replay (newest revision
or a chosen one).

## Fix checks

When an issue is marked fixed and its bug report links to a journey
revision and step, the fix check is a replay of that revision: passing the
step means "Gone", failing the same check means "Still there", any other
failure goes to the copilot. Bugs without a journey link (wording,
judgement) keep today's AI fix check.

## Audit trail

Every AI decision about a journey is a stored row: journey, run, kind
(recorded, bug revision recorded, confirmed, not recorded, bug, site
changed, journey weakness, flaky, input refused correctly, rolled back), reason, evidence references, revision
before and after, actor (model or user), time. Users' actions (pause,
resume, roll back, delete, replay) are rows too, with the actor from
`X-BP-Actor` when the cloud forwards it. Nothing about journeys is
decided without a row. Rows are kept when a journey is deleted.

## Board

- **Journeys** page in the sidebar: one row per journey: persona, story,
  state, revision, replay record (e.g. 41 passed, 2 failed), last replay.
- **Journey** page: the live revision's code (read only), revisions with
  reasons and a diff to the previous one, the decision timeline, recent
  replays. Actions: Pause/Resume, Replay (choose revision), Roll back,
  Delete.
- **Runs**: replays are runs, labelled "Replay" with the revision; the
  outcome reads "Passed" or "Failed at step 4: Fill the business"; cost
  shows $0. A failed replay links to its copilot run and its decision.
- **Bug pages**: a report linked to a journey shows the revision and step.
- Wording follows the existing board style and GB English.

## Constraints

- Go director, Preact/htm board, SQLite store; worker image already has
  Playwright and Node.
- Breaking changes are fine (single consumer: our board and cloud).
- Commits as Tiny Systems; push to main; tests with `-race`.

## Left to the builder

- The tiny "command" agent's API shape (within the decision above).
- Resuming a journey after a wait.
- The generator library and the exact `bp` helper API, within the shape
  above.
- How the agent turns the MCP action log into the module (prompt text and
  any helper that extracts "Ran Playwright code" blocks from the
  transcript).
- Storage layout for revisions (SQLite rows vs artifacts).
- Plan order. Suggested: tiny command agent and release; rename chain; journey entity and revisions; record
  on AI runs with self-replay; replay runs; scheduler switch; copilot;
  generated data and edge cases; fix checks by replay; board pages.

## Open questions

None at sign-off.
