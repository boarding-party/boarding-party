# Bug grouping: one issue, read once

Approved direction from the user 2026-11-18 (mockups agreed in the visual
companion: bugs list "A with big screenshots", issue page "one bug, read
once"). Board UI and director only; the cloud just frames it.

## Intent

Personas report the same problem again on every run that meets it. The
grouping into issues mostly works (a report names `same_as`), but the
pages make every report look like a new bug: the issue page shows each
report as a full card with its own reworded title and screenshot, and the
bugs list shows no severity and no picture. The user cannot tell which
report to read, what is new, or whether anything differs.

The user asked for "the Sentry approach": an issue is the thing you look
at; reports are occurrences counted under it; only an occurrence that
differs asks for attention.

## Goals

- The bugs list says, per issue, how bad it is, what it looks like, where
  it happens, how often and how recently, and whether it needs the user
  (new, or back after a fix).
- The issue page shows the bug once, in full, from one main report, and
  every report as a short row marked "same as before" or "different:
  <what>".
- A persona reporting a known problem says whether anything differs, in
  one line, so the board can mark the row.

## Non-goals

- No automatic splitting or merging by the board. The owner splits; the
  persona and the lead join (as today).
- No deterministic fingerprint (hash of page + step + error). Reports are
  free text written by a model; a fingerprint on that text would split
  rewordings and join different bugs on one page. Grouping stays the
  persona's judgement, given better inputs (below). Revisit if duplicates
  still pile up after this ships.
- No per-user "seen" state ("new since your last visit").
- No change to triage (Confirm, Not a bug, Mark fixed), fix checks, the
  daily digest, or the cloud's project cards.

## Anti-goals

- Hiding a report that differs. A "different" row is always visible and
  tinted, never folded away.
- The issue's main text changing on its own every run. The main report
  is stable until the owner picks another.

## Bugs list (agreed mockup: dense row, big screenshot)

One row per issue, grouped as today (Open, …; closed and not a bug behind
the toggle):

- Left edge: a severity colour bar (high red, medium amber, low grey).
- Screenshot: about 168 × 105 px, from the main report's evidence image.
  No image: a plain placeholder of the same size, so rows line up.
- Top line: severity tag, then at most one attention tag, in this order
  of precedence: "Seen again after fix" (the issue was reopened; today's
  `reopened`), "New today" (first report within the last 24 hours),
  "Something different" (a report marked different within the last 24
  hours; my call, so a differing report shows on the list without a
  per-user seen state).
- Title: the issue summary, up to two lines.
- Two muted lines: page path and step ("/invoice · step "…""); personas
  and stories ("Priya Shah, John Smith · stories first-invoice,
  check-invoice").
- Right column: report count (large), "reports", first seen, last seen.
- Order inside a group stays as today (reopened first in Open, then by
  latest activity).

Severity of an issue = the highest severity among its reports.

## Issue page (agreed mockup)

- Header: title, severity tag, status tag (with "seen again after fix"
  when reopened), and one line: "N reports by M personas · first <when> ·
  last <when>".
- Main report card: persona, story and time; where (page and step); what
  happens (evidence); expected; links to the run, the video at the
  report's moment and the page view (as today's report links); the big
  screenshot on the right.
- Reports list: one short row per report, newest first: persona, story,
  "same as before" / "Different: <one line>" / "first report", time.
  The main report's row carries a "Main" tag. Different rows are tinted
  and show the difference text in full.
- Clicking a row opens that report in full under it (today's report card
  body). An open report has "Make main" and "Split into new issue".
- The triage panel stays as it is.

## What a report must carry

`report_bug` gains two optional fields:

- `page`: the page path where it happened (the persona knows its URL).
  Stored on the bug; shown as the page line. Query strings dropped.
- `difference`: only with `same_as`. Empty means the same as the known
  issue; otherwise one line saying what differs (another page, value,
  step, device, or a new symptom). Stored on the bug; drives the row
  marking.

Old reports have neither: their rows show no marking and the list shows
no page line for them.

The known issues a persona is given gain each issue's page and step (from
its main report) and its severity, so matching has more than a summary to
go on. The prompt tells the persona to fill `difference` honestly and to
keep it to one line.

## Main report, split

- An issue has a main report. New issue: its first report. "Make main"
  sets another (owner or member, as triage is). Joins (lead
  `report_join`) and new reports never change it.
- The issue title is the main report's summary: "Make main" and split
  set `issues.summary` from it (my call). The title still changes only
  when someone acts, so the anti-goal holds.
- A report the lead joins shows as "same as before (joined by the
  lead)"; `report_join` gets no difference field (my call). When a join
  or split takes an issue's main report away and reports remain, the
  next-oldest becomes main.
- "Split into new issue" moves one report into a new open issue whose
  main report and summary are that report's. The old issue keeps the
  rest; if the split report was its main, its next-oldest report becomes
  main. Same permission as triage. Recorded in the audit log like other
  triage.
- Split details (my calls): offered only on an issue with two or more
  reports, in any status; the old issue's status, reopened mark and
  updated time stay; the new issue is open, created and updated now;
  journey links stay with the moved bug.

## Row marking

- Each report row is one of: "first report" (the earliest report by
  time), "same as before" (`same_as` with an empty `difference`),
  "Different: <difference>", "same as before (joined by the lead)", or
  no marking (old reports from before this change, and reports without
  `same_as` that started no issue). Storing "same" apart from "unknown"
  is up to the builder.

## Screenshot and page

- The screenshot is the main report's evidence image; if it has none,
  the newest report's that has one; else the placeholder (my call).
- The persona reads `page` from its browser (Playwright's snapshots
  carry the page URL) and passes the path. Empty is allowed.

## Left to the builder

- Storage shape (columns on bugs/issues, migration of existing rows: main
  report = earliest report).
- How the screenshot is picked from a report's evidence (the issue page
  already finds evidence images) and how thumbnails are served.
- Exact copy beyond what is quoted here, GB English, the board's existing
  tokens and type.
- Mobile layout of the row (screenshot may go above or shrink).
- Whether "New today" uses the board's local day or a rolling 24 hours
  (my call: rolling 24 hours).
