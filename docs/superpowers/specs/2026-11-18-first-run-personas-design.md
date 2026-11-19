# First-run personas: design

Date: 2026-11-18. Step 5 of the cloud plan (`2026-11-10-cloud-design.md`).
Builds on lead personas (`2026-11-09-lead-personas-design.md`). Supersedes
the "First-run personas" paragraph of the cloud design.

## Why

Setting up smesolutions.uk by hand, the boring part was the roles and the
personas. The site description and the stories were easy: the owner knows
what the site is and what has to work. Roles came from the stories and from
knowing the business, not from reading the site's forms or pages. Personas
were filler: names, ages, traits, invented only because a run needs them.

So a new project should ask only for what the owner knows (the site and the
stories) and make the roles and personas itself.

The cloud design planned a lead that reads the site from the staging URL
and invents personas. That gets the order wrong: the stories should come
from the owner, and the personas should come from the stories.

## Goals

1. On a new project the owner gives the staging URL and writes the stories
   in plain words, one per line, for example "a small business owner signs
   up and creates their first invoice".
2. The board turns that into a first set of personas without further
   questions: it works out which kind of user each story needs, then
   creates personas spread across popular segments.
3. The first runs start straight away. The owner fixes personas afterwards,
   with no approval step before the run (same rule as leads: no vetting).
4. Default 4 personas, never more than 6 at once.

## Non-goals

- Existing projects. This is for new projects only for now. A project that
  already has roles or personas never sees it (owner's ruling).
- Reading the site to invent roles or stories. The site is used only to
  run the stories.
- The board writing stories the owner didn't ask for.

## Anti-goals

- An approval screen between "Start" and the first run.
- More than 6 personas at once, or a cap the owner can't see.
- Personas that are all the same person with different names. A set where
  every persona is a desktop user in their thirties has failed even if it
  runs.

## Personas: the segment spread

A persona is one intersection of popular segments plus the kind of user its
story needs. The segments are the existing profile fields
(`internal/project/profile.go`):

- age (the five bands in code),
- device,
- tech confidence,
- at most one of reading, assistive technology or eyesight, where it fits.

Across the set, young and old, phone and desktop, and low and high
confidence each appear at least once where the count allows. Every kind of
user a story needs gets at least one persona. With 4 personas and stories
needing 2 kinds of user, that is 2 personas per kind, each with a different
age, device and confidence. Names and traits are the board's choice
(owner's ruling).

If the stories need more kinds of user than the count, the count wins: a
persona takes the stories of more than one kind, and the lead says so in its
decision log (my call).

The kind of user ("paying customer", "admin") is a trait on the child. Each
child keeps its own role named after it, as the lead design has it (my call:
the lead model stays unchanged; the owner said roles don't matter to them).

## How it works (my calls)

It reuses the lead. "Start" on the setup screen saves, in one project write
merged into whatever project file exists:

1. the site (`sites`),
2. a role "First run" holding one hand-made lead persona, "First-run lead",
   with the powers See other runs, Create personas and Start runs; children
   at most = the chosen count; money per day from the screen (default $5);
   runs per day = count × stories, at most 30,
3. the lead's brief: the owner's stories plus the segment rules above,
   within the existing 2000-character brief cap,
4. if the money per day is above the project budget, the project budget
   raised to match, so the project budget never stops the first runs first,
5. the lead's identity email from the saved mailbox, and `inbox.allowFrom`
   set to the site's domain if the project has no inbox settings yet.

The scheduler's existing "first wake" then starts the lead; Start does not
wake it again.

The lead creates its children with the existing tools: one child per
persona, each with the owner's stories its kind of user needs, written as
stories with a name and a goal (at most 5 per child, the existing cap). A
child has at most one queued run at a time (existing rule, kept), so each
child's stories run one after another across the lead's "children finished"
wakes. Those wakes are short lead runs and count against the same budget.
From then on it is an ordinary lead.

The owner fixes personas the way leads already allow: edit a child (which
adopts it as hand-made), delete it, or change the lead's brief and press
Run now.

## Email

Most first stories involve sign-up, so the mailbox matters.

- The setup screen shows the existing mailbox step when no mailbox is
  saved. It can be skipped.
- Skipped: the screen says sign-up and password-reset stories will stop at
  the email step until a mailbox is added. "No mailbox" means the mailbox
  Secret is not saved.
- Added later: the director fills in the lead's email and gives every child
  that has no email its plus-address. The director owns identity, so the
  lead still never writes it (my call).

## Logins for users who can't sign up

Some kinds of user need an account made beforehand: an admin, a customer
who has had an account for years. The board can't make those. Both ways
work (owner's ruling):

**Up front, optional.** The setup screen has an optional "Logins" list:
kind of user, username or email, password. Stored the way persona
identities are stored today.

**When a run gets stuck.** A persona that reaches a login it has no account
for finishes `waiting` with a new wait kind, `login`, naming the kind of
user and a short note ("needs an admin account to open Settings"). The
board shows "Waiting for a login: admin" on the run, the lead's page and
the sidebar's attention count, with a form for username and password.
Saving it stores the login under that kind and resumes the run, the same
way a run resumes when an awaited email arrives. The wait has the usual
deadline; when it passes, the run ends stuck as today.

How a login reaches a child (my calls):

- Logins belong to the project, by kind of user. The lead's assignment
  lists the kinds that have a login, never the values.
- `child_create` takes an optional `login` field naming one of those
  kinds. The director then gives the child that login as its identity
  instead of a generated one. The lead still never writes or reads
  identity values.
- A login added while a run waits goes to the waiting child and to the
  project's logins for later children of that kind.
- Several personas may share one login. Runs that use the same login can
  log each other out; the lead is told which children share one.

## The setup screen

Shown when `api/project` is not found or the project has no roles. One
page, in this order:

1. Site: the staging URL, pre-filled from the cloud when it has one. The
   board checks it with its usual site rules.
2. Agent key, if none is saved: the existing Agent key card.
3. Mailbox, if none is saved: the existing mailbox step, skippable (see
   Email).
4. Stories: one per line, 1 to 5 lines, each at most 200 characters, with
   the limits shown.
5. Personas: a number, 4 by default, 1 to 6.
6. Logins, optional: kind of user, username or email, password; add a
   row per kind.
7. Money per day: default $5, with a line saying runs past it wait until
   the next day.
8. "Start". Then the board opens the lead's page, where the children and
   their runs appear.

"Set up by hand instead" links to the full project editor, which works as
today.

## The cloud

The cloud already asks for name and staging URL when it creates a project.
It adds the staging URL to the `bp-project` ConfigMap (merged with the
existing keys, not replacing them), and the setup screen pre-fills Site from
it. The cloud stores nothing new and starts nothing: the owner presses Start
on the board. Without the cloud (CLI users), Site starts empty.

## Left to the builder

- Lead brief wording and how the segment spread is pushed (prompt guidance
  first; a check in the child-create tool only if guidance is not enough).
- Setup screen layout, within the board's existing look.
- ConfigMap key name.
- Whether Start keeps the owner's stories anywhere besides the brief.
