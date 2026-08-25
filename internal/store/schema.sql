PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS sessions (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    logged_at      TEXT    NOT NULL,          -- RFC3339 UTC
    local_date     TEXT    NOT NULL,          -- YYYY-MM-DD in the user's timezone
    session_type   TEXT,
    raw_text       TEXT    NOT NULL,
    energy         TEXT,
    pain           TEXT,
    sleep_or_shift TEXT,
    notes          TEXT,
    is_rest_day    INTEGER NOT NULL DEFAULT 0,
    post_nightshift INTEGER NOT NULL DEFAULT 0,
    coach_reply    TEXT,
    created_at     TEXT    NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_sessions_date ON sessions(local_date DESC);

CREATE TABLE IF NOT EXISTS exercises (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id  INTEGER NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    position    INTEGER NOT NULL,
    name        TEXT    NOT NULL,
    raw_name    TEXT    NOT NULL,
    equipment   TEXT    NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_exercises_session ON exercises(session_id);
CREATE INDEX IF NOT EXISTS idx_exercises_name ON exercises(name);

-- weight_kg is the canonical stored load, always kilograms, always in the
-- units the load_basis implies (per_side stays per_side). orig_weight and
-- orig_unit exist only so the UI can echo back what the user actually said.
CREATE TABLE IF NOT EXISTS sets (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    exercise_id    INTEGER NOT NULL REFERENCES exercises(id) ON DELETE CASCADE,
    position       INTEGER NOT NULL,
    set_type       TEXT    NOT NULL,
    weight_kg      REAL,
    orig_weight    REAL,
    orig_unit      TEXT,
    load_basis     TEXT    NOT NULL,
    reps           INTEGER,
    reps_uncertain INTEGER NOT NULL DEFAULT 0,
    reps_low       INTEGER,
    reps_high      INTEGER,
    to_failure     INTEGER NOT NULL DEFAULT 0,
    clean_reps     INTEGER,
    rpe            REAL,
    notes          TEXT
);
CREATE INDEX IF NOT EXISTS idx_sets_exercise ON sets(exercise_id);

-- Denormalized view used by every history and PR query. Joining three tables
-- in each of a dozen context queries was unreadable; this keeps the SQL flat.
CREATE VIEW IF NOT EXISTS v_sets AS
SELECT
    s.id            AS session_id,
    s.local_date    AS local_date,
    s.logged_at     AS logged_at,
    e.id            AS exercise_id,
    e.name          AS name,
    e.equipment     AS equipment,
    st.id           AS set_id,
    st.position     AS position,
    st.set_type     AS set_type,
    st.weight_kg    AS weight_kg,
    st.load_basis   AS load_basis,
    st.reps         AS reps,
    st.reps_low     AS reps_low,
    st.reps_uncertain AS reps_uncertain,
    st.to_failure   AS to_failure,
    st.clean_reps   AS clean_reps
FROM sets st
JOIN exercises e ON e.id = st.exercise_id
JOIN sessions  s ON s.id = e.session_id;

CREATE TABLE IF NOT EXISTS cardio (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id INTEGER NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    modality   TEXT    NOT NULL,
    minutes    REAL,
    speed      REAL,
    incline    REAL,
    notes      TEXT
);
CREATE INDEX IF NOT EXISTS idx_cardio_session ON cardio(session_id);

-- Parses that needed a human before they could be trusted. Rows here are not
-- yet in sessions; the app surfaces them, the user confirms or corrects, and
-- only then do they get persisted.
CREATE TABLE IF NOT EXISTS pending_parses (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    raw_text    TEXT    NOT NULL,
    parsed_json TEXT    NOT NULL,
    reason      TEXT    NOT NULL,
    created_at  TEXT    NOT NULL,
    resolved_at TEXT
);

CREATE TABLE IF NOT EXISTS device_tokens (
    token      TEXT PRIMARY KEY,
    platform   TEXT NOT NULL,
    created_at TEXT NOT NULL,
    last_seen  TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

-- Superseded by berserk_snapshots. Retained rather than dropped so the old
-- Iron-to-Mythic history stays readable after the v1.3 changeover.
CREATE TABLE IF NOT EXISTS rank_snapshots (
    local_date       TEXT PRIMARY KEY,
    score            REAL    NOT NULL,
    tier             TEXT    NOT NULL,
    tier_index       INTEGER NOT NULL,
    consistency      REAL    NOT NULL,
    strength         REAL    NOT NULL,
    detail_json      TEXT    NOT NULL,
    created_at       TEXT    NOT NULL
);

-- Dedup ledger for push. Without it a restart of the scheduler re-sends every
-- reminder it already sent today.
CREATE TABLE IF NOT EXISTS notifications_sent (
    dedup_key  TEXT PRIMARY KEY,
    kind       TEXT NOT NULL,
    body       TEXT NOT NULL,
    sent_at    TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_notifications_sent_at ON notifications_sent(sent_at DESC);

-- ===================================================================
-- BERSERK RANK SYSTEM v1.3
-- ===================================================================

-- Body composition history. The rank engine needs a *series*, not a single
-- reading: v1.0 §2.1 smooths LBM over eight weeks because raw scale numbers
-- swing 2 kg on water alone, and §9 freezes reference recalculation when
-- reported BF% jumps more than 4 points in 30 days. Neither is expressible
-- against a single settings row.
CREATE TABLE IF NOT EXISTS body_metrics (
    local_date    TEXT PRIMARY KEY,
    bodyweight_kg REAL NOT NULL,
    bodyfat_pct   REAL,               -- 0..100, NULL when unmeasured
    bf_source     TEXT NOT NULL DEFAULT 'estimate', -- dexa|caliper|navy|estimate
    created_at    TEXT NOT NULL
);

-- Onboarding self-reports (v1.2 Patch 5). These score at 0.93 confidence and
-- are PROVISIONAL: they carry a user as far as DREADBORN and no further, which
-- is what stops a text field from minting a day-one BERSERK.
CREATE TABLE IF NOT EXISTS pattern_claims (
    pattern     TEXT PRIMARY KEY,     -- h_press|v_press|squat|hinge|v_pull|h_pull
    e1rm_kg     REAL NOT NULL,
    lift        TEXT NOT NULL DEFAULT '',
    claimed_at  TEXT NOT NULL
);

-- The twelve skill unlocks of v1.0 §6.5. Binary and load-free, so they cannot
-- be inferred from set rows; the user asserts them and MASTERY reads them.
CREATE TABLE IF NOT EXISTS skill_unlocks (
    skill       TEXT PRIMARY KEY,
    unlocked_at TEXT NOT NULL
);

-- Nightly rank snapshots. Hysteresis (v1.2 Patch 3: promote on ten consecutive
-- qualifying days, demote three points below the band floor) is a statement
-- about history, so the history has to exist.
CREATE TABLE IF NOT EXISTS berserk_snapshots (
    local_date     TEXT PRIMARY KEY,
    rs             REAL    NOT NULL,
    rank_index     INTEGER NOT NULL,  -- 1..14
    rank_name      TEXT    NOT NULL,
    eligible_index INTEGER NOT NULL,  -- what RS alone would grant, pre-hysteresis
    might          REAL    NOT NULL,
    dominion       REAL    NOT NULL,
    frame          REAL    NOT NULL,
    vigor          REAL    NOT NULL,
    discipline     REAL    NOT NULL,
    mastery        REAL    NOT NULL,
    berserk        INTEGER NOT NULL DEFAULT 0,
    threat_level   REAL    NOT NULL DEFAULT 100,
    confidence     REAL    NOT NULL DEFAULT 0,
    blood_total    REAL    NOT NULL DEFAULT 0,
    detail_json    TEXT    NOT NULL,
    system_version TEXT    NOT NULL,
    created_at     TEXT    NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_berserk_snapshots_date ON berserk_snapshots(local_date DESC);

-- Blood ledger (v1.2 Patch 7 as corrected by v1.3 Erratum 5). Append-only, and
-- dedup_key is the whole design: weekly Blood is awarded per rolling Monday
-- boundary, so the key is the boundary date and a nightly recompute cannot pay
-- the same week twice.
CREATE TABLE IF NOT EXISTS blood_ledger (
    dedup_key   TEXT PRIMARY KEY,
    source      TEXT NOT NULL,        -- week|gates_month|strength_pr|rep_pr|...
    amount      REAL NOT NULL,
    awarded_on  TEXT NOT NULL,        -- local_date the award is attributed to
    detail      TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_blood_ledger_date ON blood_ledger(awarded_on DESC);
