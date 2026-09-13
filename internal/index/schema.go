// Package index is the hot store: a SQLite working set that makes one shared
// vault feel like one brain (SPEC §5).
//
// It is a derived cache. Everything here can be rebuilt from the wire log plus
// the vault, which is why the schema may change freely and the wire-log format
// may not.
package index

const schemaVersion = 3

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

-- Small per-index facts, such as when this index last wrote a retrieval
-- checkpoint. Local state: rebuilt indexes start empty and simply checkpoint
-- again.
CREATE TABLE IF NOT EXISTS meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
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

-- Embeddings for optional vector search (docs/SEARCH-EVALUATION.md). Derived,
-- per-machine state: never written to the wire log, rebuilt by re-embedding.
-- model names the embedder, so vectors from different models are never mixed.
-- full is float32 little-endian; bits holds one sign bit per dimension, for a
-- cheap first pass before exact rescoring.
CREATE TABLE IF NOT EXISTS vectors (
  id    TEXT NOT NULL,
  model TEXT NOT NULL,
  full  BLOB NOT NULL,
  bits  BLOB NOT NULL,
  PRIMARY KEY (id, model)
);
CREATE INDEX IF NOT EXISTS idx_vectors_model_bits ON vectors(model, id, bits);

-- vector_gen moves whenever what vector search can see changes: a memory
-- written, closed or rescoped, or a vector stored or removed. A long-lived
-- process compares it before searching and reloads its in-memory copy only when
-- it moved. last_retrieved is deliberately not watched: every search touches it.
INSERT OR IGNORE INTO meta (key, value) VALUES ('vector_gen', '0');
CREATE TRIGGER IF NOT EXISTS vector_gen_memory_insert AFTER INSERT ON memories BEGIN
  UPDATE meta SET value = CAST(value AS INTEGER) + 1 WHERE key = 'vector_gen';
END;
CREATE TRIGGER IF NOT EXISTS vector_gen_memory_update AFTER UPDATE OF valid_to, scope ON memories BEGIN
  UPDATE meta SET value = CAST(value AS INTEGER) + 1 WHERE key = 'vector_gen';
END;
CREATE TRIGGER IF NOT EXISTS vector_gen_vector_insert AFTER INSERT ON vectors BEGIN
  UPDATE meta SET value = CAST(value AS INTEGER) + 1 WHERE key = 'vector_gen';
END;
CREATE TRIGGER IF NOT EXISTS vector_gen_vector_update AFTER UPDATE ON vectors BEGIN
  UPDATE meta SET value = CAST(value AS INTEGER) + 1 WHERE key = 'vector_gen';
END;
CREATE TRIGGER IF NOT EXISTS vector_gen_vector_delete AFTER DELETE ON vectors BEGIN
  UPDATE meta SET value = CAST(value AS INTEGER) + 1 WHERE key = 'vector_gen';
END;
`
