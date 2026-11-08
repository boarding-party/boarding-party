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
  journey  TEXT NOT NULL DEFAULT '',
  follows  TEXT NOT NULL DEFAULT '',
  via      TEXT NOT NULL DEFAULT '',
  context  TEXT NOT NULL DEFAULT '',
  followed INTEGER NOT NULL DEFAULT 0
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
-- one. check_journey is the journey of the fix check under way, if any.
CREATE TABLE IF NOT EXISTS issues (
  id            TEXT PRIMARY KEY,
  summary       TEXT NOT NULL DEFAULT '',
  status        TEXT NOT NULL,
  reason        TEXT NOT NULL DEFAULT '',
  created       INTEGER NOT NULL DEFAULT 0,
  updated       INTEGER NOT NULL DEFAULT 0,
  reopened      INTEGER NOT NULL DEFAULT 0,
  check_journey TEXT NOT NULL DEFAULT '',
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
  journey TEXT NOT NULL DEFAULT '',
  follows TEXT NOT NULL DEFAULT '',
  via     TEXT NOT NULL DEFAULT '',
  context TEXT NOT NULL DEFAULT '',
  reason  TEXT NOT NULL DEFAULT '',
  created INTEGER NOT NULL DEFAULT 0
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
