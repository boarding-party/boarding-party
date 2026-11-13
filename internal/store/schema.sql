CREATE TABLE IF NOT EXISTS project (
  id   INTEGER PRIMARY KEY CHECK (id = 1),
  body TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS persona_state (
  persona TEXT PRIMARY KEY,
  state   TEXT NOT NULL
);
-- A persona's browser cookies and site storage (Playwright storage state),
-- restored at the start of its next run. Kept apart from persona_state: it
-- holds session cookies and is never shown on the board.
CREATE TABLE IF NOT EXISTS persona_profile (
  persona TEXT PRIMARY KEY,
  state   TEXT NOT NULL,
  saved   INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS runs (
  id       TEXT PRIMARY KEY,
  token    TEXT NOT NULL UNIQUE,
  persona  TEXT NOT NULL DEFAULT '',
  role     TEXT NOT NULL DEFAULT '',
  story    TEXT NOT NULL DEFAULT '',
  state    TEXT NOT NULL,
  outcome  TEXT NOT NULL DEFAULT '',
  session  TEXT NOT NULL DEFAULT '',
  started  INTEGER NOT NULL DEFAULT 0,
  finished INTEGER NOT NULL DEFAULT 0,
  chain  TEXT NOT NULL DEFAULT '',
  follows  TEXT NOT NULL DEFAULT '',
  via      TEXT NOT NULL DEFAULT '',
  context  TEXT NOT NULL DEFAULT '',
  followed INTEGER NOT NULL DEFAULT 0,
  kind       TEXT NOT NULL DEFAULT '',
  journey_id TEXT NOT NULL DEFAULT '',
  revision   INTEGER NOT NULL DEFAULT 0,
  seed       INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS runs_started ON runs(started);
CREATE TABLE IF NOT EXISTS bugs (
  id       TEXT PRIMARY KEY,
  run_id   TEXT NOT NULL,
  summary  TEXT NOT NULL DEFAULT '',
  step     TEXT NOT NULL DEFAULT '',
  evidence TEXT NOT NULL DEFAULT '',
  severity TEXT NOT NULL DEFAULT '',
  note     TEXT NOT NULL DEFAULT '',
  created  INTEGER NOT NULL DEFAULT 0,
  issue_id TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS bugs_run ON bugs(run_id);
-- An issue is one problem on the site; each bugs row (a report) belongs to
-- one. check_chain is the chain of the fix check under way, if any.
CREATE TABLE IF NOT EXISTS issues (
  id            TEXT PRIMARY KEY,
  summary       TEXT NOT NULL DEFAULT '',
  status        TEXT NOT NULL,
  reason        TEXT NOT NULL DEFAULT '',
  created       INTEGER NOT NULL DEFAULT 0,
  updated       INTEGER NOT NULL DEFAULT 0,
  reopened      INTEGER NOT NULL DEFAULT 0,
  check_chain TEXT NOT NULL DEFAULT '',
  check_note    TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS issues_updated ON issues(updated);
CREATE TABLE IF NOT EXISTS messages (
  id          TEXT PRIMARY KEY,
  uidvalidity INTEGER NOT NULL DEFAULT 0,
  uid         INTEGER NOT NULL DEFAULT 0,
  persona     TEXT NOT NULL DEFAULT '',
  sender      TEXT NOT NULL DEFAULT '',
  recipients  TEXT NOT NULL DEFAULT '[]',
  subject     TEXT NOT NULL DEFAULT '',
  body        TEXT NOT NULL DEFAULT '',
  links       TEXT NOT NULL DEFAULT '[]',
  received    INTEGER NOT NULL DEFAULT 0,
  stored      INTEGER NOT NULL DEFAULT 0,
  filtered    TEXT NOT NULL DEFAULT '',
  mailbox     TEXT NOT NULL DEFAULT '',
  UNIQUE(mailbox, uidvalidity, uid)
);
CREATE INDEX IF NOT EXISTS messages_persona_received ON messages(persona, received);
CREATE TABLE IF NOT EXISTS mail_cursor (
  id       INTEGER PRIMARY KEY CHECK (id = 1),
  validity INTEGER NOT NULL,
  last     INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS queue (
  id      TEXT PRIMARY KEY,
  persona TEXT NOT NULL DEFAULT '',
  role    TEXT NOT NULL DEFAULT '',
  story   TEXT NOT NULL DEFAULT '',
  chain TEXT NOT NULL DEFAULT '',
  follows TEXT NOT NULL DEFAULT '',
  via     TEXT NOT NULL DEFAULT '',
  context TEXT NOT NULL DEFAULT '',
  reason  TEXT NOT NULL DEFAULT '',
  created INTEGER NOT NULL DEFAULT 0,
  kind       TEXT NOT NULL DEFAULT '',
  journey_id TEXT NOT NULL DEFAULT '',
  revision   INTEGER NOT NULL DEFAULT 0,
  seed       INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS waits (
  run_id   TEXT PRIMARY KEY,
  kind     TEXT NOT NULL DEFAULT '',
  sender   TEXT NOT NULL DEFAULT '',
  subject  TEXT NOT NULL DEFAULT '',
  role     TEXT NOT NULL DEFAULT '',
  story    TEXT NOT NULL DEFAULT '',
  at       INTEGER NOT NULL DEFAULT 0,
  began    INTEGER NOT NULL DEFAULT 0,
  deadline INTEGER NOT NULL DEFAULT 0,
  status   TEXT NOT NULL,
  detail   TEXT NOT NULL DEFAULT '',
  ended    INTEGER NOT NULL DEFAULT 0,
  after_id TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS waits_status ON waits(status);
CREATE INDEX IF NOT EXISTS runs_persona_story ON runs(persona, story, started);
CREATE TABLE IF NOT EXISTS persona_due (
  persona    TEXT PRIMARY KEY,
  activity   TEXT NOT NULL,
  due        INTEGER NOT NULL,
  next_story INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS message_fired (
  message_id TEXT PRIMARY KEY,
  story      TEXT NOT NULL DEFAULT '',
  at         INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS events (
  persona     TEXT NOT NULL,
  uid         TEXT NOT NULL,
  title       TEXT NOT NULL DEFAULT '',
  start       INTEGER NOT NULL DEFAULT 0,
  "end"       INTEGER NOT NULL DEFAULT 0,
  location    TEXT NOT NULL DEFAULT '',
  url         TEXT NOT NULL DEFAULT '',
  message_id  TEXT NOT NULL DEFAULT '',
  fired_start INTEGER NOT NULL DEFAULT 0,
  fired_story TEXT NOT NULL DEFAULT '',
  PRIMARY KEY(persona, uid)
);
CREATE INDEX IF NOT EXISTS events_start ON events(start);
-- A child is a persona a lead made: body is {persona, stories}. Children are
-- merged into the project the director runs, never into the user's file.
CREATE TABLE IF NOT EXISTS children (
  name    TEXT PRIMARY KEY,
  lead    TEXT NOT NULL,
  body    TEXT NOT NULL,
  reason  TEXT NOT NULL DEFAULT '',
  created INTEGER NOT NULL,
  stopped INTEGER NOT NULL DEFAULT 0
);
-- A lead's decision log: one row per write a lead made, with its reason.
CREATE TABLE IF NOT EXISTS lead_log (
  id      INTEGER PRIMARY KEY AUTOINCREMENT,
  lead    TEXT NOT NULL,
  run_id  TEXT NOT NULL DEFAULT '',
  action  TEXT NOT NULL,
  subject TEXT NOT NULL DEFAULT '',
  reason  TEXT NOT NULL DEFAULT '',
  created INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS lead_log_lead ON lead_log(lead, created);
-- What a run's browser logged: console errors and warnings, uncaught
-- exceptions and failed requests, as the worker saw them over CDP.
CREATE TABLE IF NOT EXISTS console (
  run_id TEXT NOT NULL,
  seq    INTEGER PRIMARY KEY AUTOINCREMENT,
  at     INTEGER NOT NULL,
  level  TEXT NOT NULL,
  kind   TEXT NOT NULL,
  text   TEXT NOT NULL,
  url    TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS console_run ON console(run_id, seq);
-- Who did what from the board: the actor is the cloud user's email
-- (X-BP-Actor) or "local" for the CLI. Never holds a secret.
CREATE TABLE IF NOT EXISTS audit (
  id      INTEGER PRIMARY KEY AUTOINCREMENT,
  at      INTEGER NOT NULL,
  actor   TEXT NOT NULL,
  action  TEXT NOT NULL,
  subject TEXT NOT NULL DEFAULT ''
);
-- A journey is what a persona does for a story, recorded by an AI run as
-- Playwright code and replayed without a model. One per persona and story.
CREATE TABLE IF NOT EXISTS journeys (
  id            TEXT PRIMARY KEY,
  persona       TEXT NOT NULL,
  story         TEXT NOT NULL,
  state         TEXT NOT NULL,
  live_rev      INTEGER NOT NULL DEFAULT 0,
  created       INTEGER NOT NULL,
  passed        INTEGER NOT NULL DEFAULT 0,
  failed        INTEGER NOT NULL DEFAULT 0,
  last_replay   INTEGER NOT NULL DEFAULT 0,
  last_explored INTEGER NOT NULL DEFAULT 0,
  UNIQUE(persona, story)
);
-- Every version of a journey's code. Numbers only grow; a bug revision
-- reproduces one bug and never goes live. The code is agent-written and
-- untrusted: shown only as text.
CREATE TABLE IF NOT EXISTS journey_revisions (
  journey_id TEXT NOT NULL,
  n          INTEGER NOT NULL,
  kind       TEXT NOT NULL,
  code       TEXT NOT NULL,
  reason     TEXT NOT NULL DEFAULT '',
  run_id     TEXT NOT NULL DEFAULT '',
  parent     INTEGER NOT NULL DEFAULT 0,
  bug_id     TEXT NOT NULL DEFAULT '',
  bug_step   INTEGER NOT NULL DEFAULT 0,
  created    INTEGER NOT NULL,
  PRIMARY KEY (journey_id, n)
);
-- Why anything about a journey changed or did not: one row per decision,
-- the model's and people's alike, kept for good.
CREATE TABLE IF NOT EXISTS journey_decisions (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  journey_id TEXT NOT NULL,
  run_id     TEXT NOT NULL DEFAULT '',
  kind       TEXT NOT NULL,
  reason     TEXT NOT NULL DEFAULT '',
  actor      TEXT NOT NULL,
  evidence   TEXT NOT NULL DEFAULT '[]',
  rev_before INTEGER NOT NULL DEFAULT 0,
  rev_after  INTEGER NOT NULL DEFAULT 0,
  created    INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS journey_decisions_journey ON journey_decisions(journey_id, created);
