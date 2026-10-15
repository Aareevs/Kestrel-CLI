-- Results of `kestrel verify`, kept with the session they verified (and
-- removed with it). report is the full redacted VerificationReport as JSON.
CREATE TABLE verifications (
    id         TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    started_ns INTEGER NOT NULL,
    ended_ns   INTEGER NOT NULL,
    verdict    TEXT NOT NULL,
    incident   INTEGER NOT NULL DEFAULT 0,
    window_len TEXT NOT NULL,
    report     TEXT NOT NULL
);

CREATE INDEX idx_verifications_session ON verifications (session_id, started_ns);
