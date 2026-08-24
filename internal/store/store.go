// Package store owns the SQLite database.
//
// It uses modernc.org/sqlite, a pure-Go implementation, so the binary
// cross-compiles to the mini PC without a C toolchain and without CGO_ENABLED
// juggling on the build host.
package store

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"time"

	_ "modernc.org/sqlite"

	"github.com/mrcha/gymlogger/internal/model"
)

//go:embed schema.sql
var schemaSQL string

type Store struct {
	db *sql.DB
	// loc is the user's timezone. Every "what day is it" decision in the app
	// routes through here, because a session logged at 01:00 after a night
	// shift must not land on the wrong calendar day.
	loc *time.Location
}

func Open(path string, loc *time.Location) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// SQLite tolerates exactly one writer. Capping the pool avoids spurious
	// SQLITE_BUSY under concurrent HTTP handlers.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &Store{db: db, loc: loc}, nil
}

func (s *Store) Close() error             { return s.db.Close() }
func (s *Store) DB() *sql.DB              { return s.db }
func (s *Store) Location() *time.Location { return s.loc }

// LocalDate renders a timestamp as the calendar date in the user's timezone.
func (s *Store) LocalDate(t time.Time) string {
	return t.In(s.loc).Format("2006-01-02")
}

// ---------- settings ----------

func (s *Store) Setting(ctx context.Context, key, def string) string {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if err != nil {
		return def
	}
	return v
}

func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO settings(key, value) VALUES(?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

func (s *Store) BodyweightKg(ctx context.Context) float64 {
	var v float64
	if err := s.db.QueryRowContext(ctx,
		`SELECT CAST(value AS REAL) FROM settings WHERE key = 'bodyweight_kg'`).Scan(&v); err != nil || v <= 0 {
		return 80 // a placeholder, only used to scale bodyweight-lift volume
	}
	return v
}

// ---------- persistence ----------

// Persist writes a validated session and returns its id. Everything happens in
// one transaction: a session with half its exercises written is worse than no
// session at all, because the PR queries would treat the gap as real.
func (s *Store) Persist(ctx context.Context, p *model.ParsedSession) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	loggedAt := p.LoggedAt
	if loggedAt.IsZero() {
		loggedAt = time.Now()
	}
	now := time.Now().UTC().Format(time.RFC3339)

	isRest := 0
	if p.SessionType != nil && (*p.SessionType == "rest") {
		isRest = 1
	}
	postShift := 0
	if p.Subjective.SleepOrShift != nil && *p.Subjective.SleepOrShift != "" {
		postShift = 1
	}

	res, err := tx.ExecContext(ctx, `
		INSERT INTO sessions
		  (logged_at, local_date, session_type, raw_text, energy, pain,
		   sleep_or_shift, notes, is_rest_day, post_nightshift, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		loggedAt.UTC().Format(time.RFC3339),
		s.LocalDate(loggedAt),
		p.SessionType, p.RawText,
		p.Subjective.Energy, p.Subjective.Pain,
		p.Subjective.SleepOrShift, p.Subjective.Notes,
		isRest, postShift, now,
	)
	if err != nil {
		return 0, fmt.Errorf("insert session: %w", err)
	}
	sessionID, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}

	for i, ex := range p.Exercises {
		exRes, err := tx.ExecContext(ctx,
			`INSERT INTO exercises(session_id, position, name, raw_name, equipment)
			 VALUES (?,?,?,?,?)`,
			sessionID, i, ex.Name, ex.RawName, string(ex.Equipment))
		if err != nil {
			return 0, fmt.Errorf("insert exercise %q: %w", ex.Name, err)
		}
		exID, err := exRes.LastInsertId()
		if err != nil {
			return 0, err
		}
		for j, st := range ex.Sets {
			var unit *string
			if st.Unit != nil {
				u := string(*st.Unit)
				unit = &u
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO sets
				  (exercise_id, position, set_type, weight_kg, orig_weight, orig_unit,
				   load_basis, reps, reps_uncertain, reps_low, reps_high,
				   to_failure, clean_reps, rpe, notes)
				VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
				exID, j, string(st.SetType), st.WeightKg(), st.Weight, unit,
				string(st.LoadBasis), st.Reps, boolInt(st.RepsUncertain),
				st.RepsLow, st.RepsHigh, boolInt(st.ToFailure),
				st.CleanReps, st.RPE, st.Notes,
			); err != nil {
				return 0, fmt.Errorf("insert set %d of %q: %w", j, ex.Name, err)
			}
		}
	}

	for _, c := range p.Cardio {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO cardio(session_id, modality, minutes, speed, incline, notes)
			 VALUES (?,?,?,?,?,?)`,
			sessionID, c.Modality, c.Minutes, c.Speed, c.Incline, c.Notes); err != nil {
			return 0, fmt.Errorf("insert cardio: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return sessionID, nil
}

func (s *Store) SaveCoachReply(ctx context.Context, sessionID int64, reply string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET coach_reply = ? WHERE id = ?`, reply, sessionID)
	return err
}

// StashPending parks a parse that could not be trusted, so the user can fix it
// instead of the app silently writing a wrong number into the PR history.
func (s *Store) StashPending(ctx context.Context, raw string, p *model.ParsedSession, reason string) (int64, error) {
	blob, err := json.Marshal(p)
	if err != nil {
		return 0, err
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO pending_parses(raw_text, parsed_json, reason, created_at)
		 VALUES (?,?,?,?)`,
		raw, string(blob), reason, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) Pending(ctx context.Context, id int64) (*model.ParsedSession, string, error) {
	var blob, raw string
	err := s.db.QueryRowContext(ctx,
		`SELECT parsed_json, raw_text FROM pending_parses WHERE id = ? AND resolved_at IS NULL`,
		id).Scan(&blob, &raw)
	if err != nil {
		return nil, "", err
	}
	var p model.ParsedSession
	if err := json.Unmarshal([]byte(blob), &p); err != nil {
		return nil, "", err
	}
	return &p, raw, nil
}

func (s *Store) ResolvePending(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE pending_parses SET resolved_at = ? WHERE id = ?`,
		time.Now().UTC().Format(time.RFC3339), id)
	return err
}

// ---------- device tokens ----------

func (s *Store) RegisterDevice(ctx context.Context, token, platform string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO device_tokens(token, platform, created_at, last_seen)
		VALUES (?,?,?,?)
		ON CONFLICT(token) DO UPDATE SET last_seen = excluded.last_seen`,
		token, platform, now, now)
	return err
}

func (s *Store) DeviceTokens(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT token FROM device_tokens`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) DeleteDevice(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM device_tokens WHERE token = ?`, token)
	return err
}

// ---------- notification dedup ----------

// MarkNotified records a send and reports whether this is the first time.
// The INSERT is the lock: if the key already exists the send is a duplicate,
// which is what stops a scheduler restart from re-nagging.
func (s *Store) MarkNotified(ctx context.Context, dedupKey, kind, body string) (bool, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO notifications_sent(dedup_key, kind, body, sent_at)
		VALUES (?,?,?,?)
		ON CONFLICT(dedup_key) DO NOTHING`,
		dedupKey, kind, body, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
