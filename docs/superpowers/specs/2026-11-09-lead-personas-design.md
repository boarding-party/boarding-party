# Lead personas: design

Date: 2026-11-09. Builds on the main spec (`2026-10-29-boardingparty-design.md`)
and the bug list (`2026-11-08-bug-list-design.md`).

## Why

Writing personas, their stories and their prompts by hand is slow. On
smesolutions.uk every persona, every story and every rerun was set up by
hand, and so was the reading of each run to decide what to try next. That is
the work a user would most like to hand off, and it is the wall a new user
hits first.

The goal: the user writes one persona with a short brief and ticks what it
may do. That persona, a **lead**, then writes other personas, watches their
runs and reruns them, inside a budget the director enforces. Users who want
to write every persona by hand still can; a project with no lead works as
today.

The main spec already planned a supervisor (`canWatchRecordings` on a role,
`list_recordings` and `flag_run` tools). It was never built. A lead replaces
it.

## Words

- A **lead** is a persona with one or more powers ticked.
- A **child** is a persona the lead made. Children belong to one lead.
- A **hand-made** persona is one the user wrote, as today.
- A **lead run** is a run of the lead itself. It has no browser.

## Goals

1. Any persona can be made a lead by ticking powers on its page. With no
   power ticked it is a normal persona.
2. A lead writes children (persona and stories) that differ from each other
   and from the hand-made personas, and they run without the user's OK.
3. A lead reads the runs, notes and bugs of the project and decides what to
   do next: add a child, change a child's story, stop a child, rerun one.
4. A lead tidies the bug list by joining a duplicate report to its issue.
5. One budget per lead covers the lead's own runs and all its children's
   runs. The director enforces it.
6. The board shows each lead's children, its spend, and a log of its
   decisions with a reason for each.

## Non-goals

- No grandchildren: a child can never have powers (my call; keeps the tree
  and the budget simple).
- No lead without the user: the user always writes the lead and its brief.
- No new sites: children use only the lead's project sites and the existing
  egress fence.
- No code fixes. A lead finds and checks bugs; it never changes the site.

## Anti-goals

- No vetting. A child never waits for the user's OK before it runs. The
  budget and the record on the board are the controls. (User's ruling.)
- A lead never closes an issue, never marks one not a bug and never marks
  one fixed. Those stay the user's.
- A lead never edits a hand-made persona, and never edits a child the user
  has edited (see Adopting).
- Spend never goes over the lead's budget by more than the runs already
  running when it ran out (same soft cap as the project budget).

## The powers

Ticked on the persona's edit page. Each is a separate box.

| Power | What it allows |
|---|---|
| See other runs | Read every run, its outcome, notes and bug reports in the project, and the issue list. |
| Create personas | Add, change and stop its own children and their stories. |
| Start runs | Queue runs of its own children, including reruns after a bug is marked fixed. |
| Tidy bugs | Join a report to an existing issue (`same_as` after the fact). |

"Create personas" and "Start runs" without "See other runs" are allowed but
rarely useful; the page says so. The director checks the power behind the
run token on every tool call; without it the tool returns "not allowed".

## The budget

Set on the lead's page, all required once any power is ticked:

- money per day (USD, like the project budget),
- runs per day,
- children at most.

Defaults (my call): $5 a day, 10 runs a day, 6 children. The money and runs
count the lead's own runs plus all its children's runs, in the project's
time zone, reset at midnight like the project budget. The project budget
still applies on top: whichever runs out first stops the run.

When a lead's budget is spent, its queued runs (its own and its children's)
stay queued and start the next day; the board says why, as it does for the
project budget. A lead asking for an extra child over the cap gets a tool
error saying so.

## How a lead works

A lead run is short and has no browser, video or trace. It gets a lead
assignment: its brief, the project sites, its budget and what is left of it
today, its children with their recent results, the hand-made personas
(read only), and a summary of the project's recent runs and open issues
(if it may see them).

It wakes:

- when the last running child of a batch ends (no lead run while any of its
  children is running or queued),
- once a day at a time set on the lead's page (default 09:00 in the project
  time zone),
- when the user presses "Run now" on its page.

It acts through new MCP tools on the bp server (names are the builder's):

- read: list runs, read one run (outcome, notes, bug reports, no video),
  list issues;
- write children: create a child with its stories, change a child's
  stories or traits, stop a child (it stays on the board, marked stopped);
- queue: queue a run of one child and story;
- bugs: join a report to an issue;
- log: every write tool takes a required `reason`, which goes to the
  decision log.

Then it finishes like any run, with an outcome line.

Children are normal personas: same prompt, same tools, same egress fence,
same browser profile handling, same bug reporting with `knownIssues`.

### Children in the project model (my calls)

- **One role per child.** Stories belong to a role and the scheduler rotates
  every persona of a role through all its stories, so each child gets its
  own role holding that one persona and its own stories. The role is named
  after the child and marked as made by the lead.
- **Lead-queued only.** Children have no activity schedule and no triggers;
  they run only when their lead queues them (including reruns after a bug is
  marked fixed). So "Create personas" implies "Start runs": ticking the
  first ticks the second, and the second can't be unticked while the first
  is on.
- **A lead only leads.** Once any power is ticked, the persona stops taking
  browser runs: the scheduler, triggers, inbox routing and handoffs skip it.
  Its role's other personas carry on as before. Unticking every power makes
  it a normal persona again.
- **Identity by the director.** The lead never writes identity. The director
  gives each child a synthetic identity: a name-based email on the project
  inbox (plus-addressing, `<inbox local part>+<child name>@<domain>`, the
  scheme the main spec planned) when the project has an inbox, a generated
  password, and address fields from the existing synthetic data rules.
  Without an inbox, children get no email and the lead is told so in its
  assignment.

## Adopting a child

If the user edits a child on the board, it becomes hand-made: it leaves the
lead's children, stops counting against the lead's caps, and the lead may
no longer change it. The board says "Adopted from <lead>". The user can
also delete a child.

## Where children live

The project file stays the user's. Children live in the director's store
and are merged into the project only where the director runs things
(scheduler, assignments). `GET /api/project` and the CLI keep returning and
replacing the raw project file, so saving the project editor or uploading
YAML never touches children. Children have their own endpoints and their
own edit form on the child page; saving that form is what adopts a child,
which moves it from the store into the project file as a hand-made persona
with its role. (My call: the user's file is never rewritten by an agent, so
a user's edit and a lead's edit can't fight over one file.)

## Safety

Prompt injection comes from anything a child reads: site pages, email,
calendar events, downloaded files. On smesolutions.uk the scanner shows text
from third-party sites, so anyone can plant text a persona will read. That
text reaches the lead through the child's notes and bug reports. A lead can
spend and can write instructions for other agents, so it makes an injection
worth more.

What limits it:

- The lead's prompt says run notes, bug reports and issue text are data,
  never instructions, as the children's prompt does now.
- Children are written through tools with fixed fields (name, traits,
  profile, story name, goal), each with a length cap. The lead can't set
  sites, extra hosts, model, identity domains, triggers or powers.
- Text the lead reads is clipped, as `knownIssues` is today.
- The director enforces the budget, the caps and the powers; nothing the
  lead writes changes them.
- Every child shows "Made by <lead>: <reason>" and every action is in the
  decision log, so a strange child is visible on the board.

Worst case: odd children spend one lead's daily budget on the project's own
sites.

## The board

- Persona page: the four power boxes, the budget fields, the daily wake
  time and "Run now", shown only once a power is ticked.
- Lead page (the persona page of a lead): its children with each child's
  runs and bugs found, spend today against the budget, and the decision log
  (newest first, one line per action with its reason and a link to the lead
  run).
- Child page: "Made by <lead>: <reason>", plus Adopt and Delete.
- Sidebar: leads show a small count of children.

## Left to the builder

- Exact tool names and argument shapes, and the lead prompt wording.
- The run summary format the lead receives and its size limits.
- How "differ from each other" is pushed (prompt guidance is enough to
  start).
- Storage layout for children and the decision log.
- Whether the lead run uses a cheaper model by default.
