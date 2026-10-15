-- Sessions, events, and the index of raw-log segment files.
-- Times are unix nanoseconds. Raw log text lives in segment files, not here:
-- events.message is truncated; the full redacted event is in the segment.

CREATE TABLE sessions (
    id           TEXT PRIMARY KEY,
    mode         TEXT NOT NULL,
    project_root TEXT NOT NULL,
    status       TEXT NOT NULL CHECK (status IN ('active', 'closed', 'interrupted')),
    kestrel_pid  INTEGER NOT NULL,
    started_ns   INTEGER NOT NULL,
    ended_ns     INTEGER,
    services     TEXT NOT NULL DEFAULT '[]',   -- JSON array of names
    final_status TEXT NOT NULL DEFAULT '[]',   -- JSON array of ServiceStatus
    events       INTEGER NOT NULL DEFAULT 0,
    errors       INTEGER NOT NULL DEFAULT 0,
    dropped      INTEGER NOT NULL DEFAULT 0,
    bytes        INTEGER NOT NULL DEFAULT 0,   -- segment bytes on disk + row estimate
    write_errors INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX idx_sessions_project ON sessions (project_root, started_ns);
CREATE INDEX idx_sessions_status ON sessions (status, ended_ns);

CREATE TABLE segments (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    service    TEXT NOT NULL,
    path       TEXT NOT NULL,                  -- relative to the sessions directory
    first_seq  INTEGER NOT NULL,
    last_seq   INTEGER NOT NULL,
    bytes      INTEGER NOT NULL DEFAULT 0,     -- uncompressed content size
    disk_bytes INTEGER NOT NULL DEFAULT 0,     -- size on disk (compressed once closed)
    compressed INTEGER NOT NULL DEFAULT 0,
    closed     INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX idx_segments_session ON segments (session_id, service, first_seq);

CREATE TABLE events (
    session_id  TEXT NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    seq         INTEGER NOT NULL,
    id          TEXT NOT NULL,
    time_ns     INTEGER NOT NULL,
    service     TEXT NOT NULL,
    pid         INTEGER NOT NULL DEFAULT 0,
    type        TEXT NOT NULL,
    severity    TEXT NOT NULL,
    source      TEXT NOT NULL,
    message     TEXT NOT NULL,                 -- truncated; full text in the segment
    attrs       TEXT NOT NULL DEFAULT '{}',    -- JSON
    fingerprint TEXT NOT NULL DEFAULT '',
    segment_id  INTEGER REFERENCES segments (id) ON DELETE SET NULL,
    seg_offset  INTEGER,
    PRIMARY KEY (session_id, seq)
) WITHOUT ROWID;

CREATE INDEX idx_events_service_time ON events (session_id, service, time_ns);
CREATE INDEX idx_events_type ON events (session_id, type);
CREATE UNIQUE INDEX idx_events_id ON events (id);
