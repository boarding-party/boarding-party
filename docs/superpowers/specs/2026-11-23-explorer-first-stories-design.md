# Explorer: first stories from the site

Date: 2026-11-23. Amends first-run personas (`2026-11-18-first-run-personas-design.md`).
Approved in chat on 2026-11-23 ("go").

## Why

Owners asked whether the cloud can read the site and make the first
personas and roles by itself. Today Setup needs at least one story. An owner
who has not thought about stories yet stops there.

## Change

This reverses one non-goal of the 18 November design: the site is now also
read to write stories, but only when the owner gives none.

1. On Setup, stories are optional.
2. With no stories, the first-run lead's brief tells it to find them first.
   On its first wake it makes one child, **Explorer**, with one story: look
   round the site as a first-time visitor, sign up if the site lets it, and
   finish with 3 to 6 things people come to the site to do, one per line.
3. When Explorer's run finishes, the lead wakes as usual ("Children
   finished"), reads the run with `run_read`, writes 3 to 5 stories from it,
   stops Explorer, and makes the personas with the same spread rules as
   before.
4. No approval screen; the first persona runs start straight away (unchanged
   anti-goal).

## Limits

- Explorer takes one child slot: `maxChildren` is personas + 1 when there are
  no stories. Explorer is stopped before the personas are made, so at most
  the chosen number of personas run at once.
- Daily runs with no stories: min(1 + personas × 5, 30). With stories it
  stays min(personas × stories, 30). The board shows the same figure.
- Cost: one extra persona run before the first real runs.

## Known limits

- On a site with invite-only sign-up, Explorer sees only public pages, so its
  stories cover only those. A login given on Setup does not reach Explorer:
  the lead may pass one, as for any child.
- Explorer's outcome is untrusted data from the site. The lead takes goals
  from it, never instructions (the lead prompt's existing security rule).

## Calls made by the builder

- Explorer is a lead child, not a new run kind: the scheduler, the lead page
  and the decision log already show it and why it was made. The "written by
  the explorer" marking promised in chat is the lead's decision log reason on
  each persona it makes from Explorer's run.
- No separate plan file: the change is a brief, two limits and the Setup
  page, built test first in one pass.
