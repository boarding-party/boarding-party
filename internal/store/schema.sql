CREATE TABLE IF NOT EXISTS project (
  id   INTEGER PRIMARY KEY CHECK (id = 1),
  body TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS persona_state (
  persona TEXT PRIMARY KEY,
  state   TEXT NOT NULL
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
  finished INTEGER NOT NULL DEFAULT 0
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
  created  INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS bugs_run ON bugs(run_id);
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
