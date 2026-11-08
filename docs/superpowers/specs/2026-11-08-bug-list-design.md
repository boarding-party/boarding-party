# Bug list with fix checks: design

Date: 2026-11-08. Builds on the main spec (`2026-10-29-boardingparty-design.md`).

## Why

Today a bug lives only inside the run that reported it. Live runs on
smesolutions.uk showed what that costs:

- The same problem gets reported again by every run that meets it ("Run a
  free scan" link, twice).
- False positives come back too. Personas reported the invoice number in a
  new browser as a bug twice, but it is the site's default value.
- Nobody can tell whether a fix worked without starting a run by hand and
  reading its outcome.

The goal is one list of problems across all runs, which the user triages,
and which checks fixes by itself: report, fix, verify.

## Words

- A **report** is what one run says through `report_bug`. These are the
  existing `bugs` rows; they stay as they are.
- An **issue** is one problem on the site. Each report belongs to exactly
  one issue, and an issue has one or more reports.

## Goals

1. Each report is either attached to an existing issue or opens a new one.
2. The user sets an issue's status on the board: confirmed, not a bug, or
   fixed.
3. When an issue is marked fixed, boardingparty re-runs the story that found
   it and closes the issue if the problem is gone, or reopens it if not.
4. Personas know the known issues, so they stop re-reporting what is already
   known and stop reporting what the user said is not a bug.

## Non-goals

- No export to GitHub, Linear or Jira (later; the list is the source).
- No merging or splitting of issues by hand in the first version (my call:
  the agent's matching plus "not a bug" covers the noise seen so far).
- No severity editing by the user (the agent's guess stays on each report).

## Anti-goals

- No extra model call just to match reports. The persona already has the
  context, so it does the matching while it runs.
- A "not a bug" verdict must never hide a real regression for good: if a
  persona strongly believes the problem is real, it can still report it as
  new, and the board shows the dismissed issue it resembles.

## How it works

### Matching (at report time, by the persona)

`next_assignment` gains `knownIssues`: the open, confirmed, fixed and
not-a-bug issues, each with id, status, summary and (for not a bug) the
user's reason. At most 50, newest activity first (my call).

`report_bug` gains an optional `same_as` (issue id). The prompt says: before
reporting, check knownIssues; if it is the same problem, pass `same_as`; if
it matches a not-a-bug issue, do not report it unless something clearly
differs, and then say what. A report without `same_as` (or with an unknown
id) opens a new issue.

A report attached to a not-a-bug issue keeps that issue as not a bug and only
adds the report; the board shows the count, so a growing pile is visible.

### Statuses

`open` (new, untriaged) → the user picks `confirmed`, `not a bug` (with an
optional reason) or `fixed`. `fixed` leads to `verifying`, then `closed`
(the check passed) or back to `open` with a "reopened" mark (the check found
it again). A new report attached to a `closed` issue also reopens it.

### Fix check

Marking an issue fixed queues one run: the persona and story of the issue's
latest report, with a new run context kind `verify` that carries the issue
id, summary, step and evidence. The prompt tells the persona to retrace the
story to that step and look for the problem specifically.

When that run finishes:

- `done` with no report attached to the issue: the issue is **closed**.
- Any report with `same_as` = the issue: the issue is **reopened**.
- `stuck` or `failed`, or the run was stopped: the issue stays `fixed` with
  "could not check". The board offers "Check again".

The verify run goes through the normal queue and gates (pause, budget, busy
persona). It does not get a "first visit" exemption: freshBrowser stories
stay fresh.

### Board

A new **Bugs** page in the main navigation:

- A list of issues, filtered by status (default: open and reopened first,
  then confirmed, fixed and verifying; closed and not a bug behind a
  filter). Each row: summary, status, number of reports, last seen,
  personas that hit it.
- An issue page: all reports with their screenshots and links to the runs;
  buttons Confirm, Not a bug (asks for a short reason), Mark fixed, Check
  again (when a check could not run).
- The run page keeps its bug list. Each report links to its issue.

### Existing data

Each existing report becomes its own `open` issue (no attempt to merge
history; my call). The user can mark the old duplicates as not a bug.

## Constraints

- Bug text is written by agents and is untrusted; the board renders it as
  text, as today.
- The scheduler's deciding still makes no model calls.
- GB English in all copy.

## Decisions from the builder check (my calls)

1. **Explicit verdict.** A verify run cannot close an issue just by ending
   `done`: the persona may have taken another path. `finish_run` on a run
   with a verify context needs `verify: {reached, still_there, note}`.
   `reached: false` means "could not check". `still_there: true` reopens the
   issue, `still_there: false` closes it. Without the field, a `done` on a
   verify run is a tool error that asks for it.
2. **Waiting.** A verify run may wait (email, handoff, time) like any run.
   Its continuation keeps the verify context, and the verdict comes from the
   run in that chain that finishes without waiting. A chain that ends stuck,
   failed or stopped means "could not check".
3. **Reports during a verify run.** The verdict decides the issue, not the
   reports. Other problems the persona meets are new issues as usual, and a
   report with `same_as` = the verified issue is just attached.
4. **Who re-runs.** The latest report whose persona and story still exist
   in the project. If none do, the issue shows "could not check: the story
   or persona is gone" and nothing is queued. The context is stored in the
   run like a handoff context and handed back by `next_assignment`.
5. **One check at a time.** An issue has at most one queued or running
   verify. Mark fixed while verifying does nothing. If the user changes the
   status during verifying, the verdict of that run is ignored (it only
   applies while the issue is still verifying). A `same_as` report from an
   ordinary run attaches without changing a verifying issue, and reopens a
   closed one.
6. **knownIssues.** All not-a-bug issues come first (they are what stops
   repeat false positives), then open, confirmed, fixed, verifying and
   closed-in-the-last-30-days issues by latest activity, 100 in total.
   Closed ones are there so a returning problem can be matched and reopened.
7. **Reopened** is a timestamp on the issue, shown as a mark; the report
   count, last seen and personas are worked out from the reports.
8. Reports attached to a not-a-bug issue count towards its last seen and
   personas like any other.

## Left to the builder

Table layout, API shapes, the exact prompt wording, and where on the board
the counts appear, as long as the behaviour above holds.
