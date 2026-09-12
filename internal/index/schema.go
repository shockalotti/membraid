// Package index is the hot store: a SQLite working set that makes one shared
// vault feel like one brain (SPEC §5).
//
// It is a derived cache. Everything here can be rebuilt from the wire log plus
// the vault, which is why the schema may change freely and the wire-log format
// may not.
package index

const schemaVersion = 1

const schemaSQL = `
CREATE TABLE IF NOT EXISTS memories (
  id             TEXT PRIMARY KEY,
  kind           TEXT NOT NULL,
  key            TEXT,
  content        TEXT NOT NULL,
  scope          TEXT NOT NULL DEFAULT 'shared',
  source         TEXT NOT NULL,
  valid_from     TEXT NOT NULL,
  valid_to       TEXT,
  supersedes     TEXT,
  superseded_by  TEXT,
  source_concept TEXT,
  confidence     REAL,
  session_ref    TEXT,
  created_at     TEXT NOT NULL,
  last_retrieved TEXT
);

CREATE INDEX IF NOT EXISTS idx_memories_scope_current ON memories(scope, valid_to);

-- At most one CURRENT row per subject, enforced by the schema rather than by
-- careful coding. This is what stops a shared brain holding five live opinions
-- about the same thing. source is deliberately absent: a fact learned in one
-- harness is current in all of them.
CREATE UNIQUE INDEX IF NOT EXISTS idx_memories_key_current
  ON memories(scope, kind, key)
  WHERE key IS NOT NULL AND valid_to IS NULL;

-- A subject's full history, closed rows included. The partial index above
-- deliberately excludes exactly the rows the history lives in.
CREATE INDEX IF NOT EXISTS idx_memories_key_all
  ON memories(scope, kind, key) WHERE key IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_memories_superseded_by
  ON memories(superseded_by) WHERE superseded_by IS NOT NULL;

CREATE VIRTUAL TABLE IF NOT EXISTS memories_fts USING fts5(
  content, id UNINDEXED, tokenize='porter unicode61'
);

-- How far into each wire-log file this index has read. Files are append-only,
-- so import resumes where it stopped instead of rereading years of history on
-- every command. "pos" rather than "offset": OFFSET is an SQL keyword.
CREATE TABLE IF NOT EXISTS log_offsets (
  file TEXT PRIMARY KEY,
  pos  INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS scopes (
  scope      TEXT PRIMARY KEY,
  name       TEXT NOT NULL,
  path       TEXT,
  first_seen TEXT NOT NULL,
  last_seen  TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS concepts (
  path           TEXT PRIMARY KEY,
  scope          TEXT NOT NULL DEFAULT 'shared',
  type           TEXT NOT NULL,
  key            TEXT,
  title          TEXT,
  summary        TEXT,
  status         TEXT NOT NULL DEFAULT 'draft',
  author         TEXT,
  tags           TEXT,
  created        TEXT,
  updated        TEXT,
  stale_after    TEXT,
  excerpt        TEXT,
  last_retrieved TEXT
);

CREATE INDEX IF NOT EXISTS idx_concepts_scope_status ON concepts(scope, status);
CREATE INDEX IF NOT EXISTS idx_concepts_key ON concepts(scope, type, key) WHERE key IS NOT NULL;

CREATE VIRTUAL TABLE IF NOT EXISTS concepts_fts USING fts5(
  title, summary, excerpt, path UNINDEXED, tokenize='porter unicode61'
);
`
