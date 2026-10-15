-- The service layout a session ran with (names, depends_on, probe ports), as
-- JSON. Diagnosis builds its dependency graph from it.
ALTER TABLE sessions ADD COLUMN topology TEXT NOT NULL DEFAULT '{"services":[]}';
