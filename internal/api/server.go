// Package api is the HTTP surface the phone talks to.
//
// This service sits behind a Cloudflare Tunnel, which means it has a public
// hostname. Anyone who finds that hostname can reach it, so every route except
// the health check requires a bearer token. A tunnel is not a firewall.
package api

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mrcha/gymlogger/internal/app"
	"github.com/mrcha/gymlogger/internal/berserk"
	"github.com/mrcha/gymlogger/internal/exercises"
	"github.com/mrcha/gymlogger/internal/lifts"
	"github.com/mrcha/gymlogger/internal/model"
	"github.com/mrcha/gymlogger/internal/muscle"
	"github.com/mrcha/gymlogger/internal/push"
	"github.com/mrcha/gymlogger/internal/scheduler"
	"github.com/mrcha/gymlogger/internal/store"
)

type Server struct {
	App    *app.App
	Store  *store.Store
	Sched  *scheduler.Scheduler
	Push   push.Sender
	Logger *slog.Logger

	// AuthToken is the shared secret the phone sends. Required.
	AuthToken string

	// MediaDir holds the exercise demo images and animations. Empty disables
	// the media route; the library still works, without demos.
	MediaDir string
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
	})

	mux.Handle("POST /v1/log", s.auth(http.HandlerFunc(s.handleLog)))
	mux.Handle("POST /v1/pending/{id}/confirm", s.auth(http.HandlerFunc(s.handleConfirm)))
	mux.Handle("GET /v1/rank", s.auth(http.HandlerFunc(s.handleRank)))
	mux.Handle("GET /v1/next", s.auth(http.HandlerFunc(s.handleNext)))
	mux.Handle("GET /v1/history", s.auth(http.HandlerFunc(s.handleHistory)))
	mux.Handle("GET /v1/session/{id}", s.auth(http.HandlerFunc(s.handleSession)))
	mux.Handle("POST /v1/device", s.auth(http.HandlerFunc(s.handleDevice)))
	mux.Handle("GET /v1/settings", s.auth(http.HandlerFunc(s.handleGetSettings)))
	mux.Handle("POST /v1/settings", s.auth(http.HandlerFunc(s.handleSetSetting)))
	mux.Handle("POST /v1/test-push", s.auth(http.HandlerFunc(s.handleTestPush)))

	// Berserk rank system inputs. The engine reads body composition, onboarding
	// self-reports and skill unlocks, and none of them can be derived from set
	// rows, so each needs a way in.
	mux.Handle("GET /v1/profile", s.auth(http.HandlerFunc(s.handleGetProfile)))
	mux.Handle("POST /v1/profile", s.auth(http.HandlerFunc(s.handleSetProfile)))
	mux.Handle("POST /v1/body", s.auth(http.HandlerFunc(s.handleBody)))
	mux.Handle("POST /v1/claims", s.auth(http.HandlerFunc(s.handleClaims)))
	mux.Handle("POST /v1/skills", s.auth(http.HandlerFunc(s.handleSkills)))
	mux.Handle("GET /v1/blood", s.auth(http.HandlerFunc(s.handleBlood)))
	mux.Handle("GET /v1/muscles", s.auth(http.HandlerFunc(s.handleMuscles)))
	mux.Handle("GET /v1/lifts", s.auth(http.HandlerFunc(s.handleLifts)))
	mux.Handle("GET /v1/suggest", s.auth(http.HandlerFunc(s.handleSuggest)))
	mux.Handle("GET /v1/lifts/{key}", s.auth(http.HandlerFunc(s.handleLift)))

	// Exercise library.
	mux.Handle("GET /v1/exercises", s.auth(http.HandlerFunc(s.handleExercises)))
	mux.Handle("GET /v1/exercises/facets", s.auth(http.HandlerFunc(s.handleFacets)))
	mux.Handle("GET /v1/exercises/{id}", s.auth(http.HandlerFunc(s.handleExercise)))
	mux.Handle("GET /v1/media/{kind}/{file}", s.auth(http.HandlerFunc(s.handleMedia)))

	return logging(s.Logger, mux)
}

// auth compares the bearer token in constant time. A plain == leaks the token
// one byte at a time to anyone willing to measure, and this endpoint is on the
// public internet.
func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if s.AuthToken == "" || subtle.ConstantTimeCompare([]byte(got), []byte(s.AuthToken)) != 1 {
			writeErr(w, http.StatusUnauthorized, errors.New("unauthorized"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func logging(l *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		l.Info("http", "method", r.Method, "path", r.URL.Path, "dur", time.Since(start))
	})
}

// ---------- handlers ----------

type logRequest struct {
	Text string `json:"text"`
	// At lets the phone log a session after the fact. Optional.
	At string `json:"at,omitempty"`
}

func (s *Server) handleLog(w http.ResponseWriter, r *http.Request) {
	var req logRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("decode body: %w", err))
		return
	}
	if strings.TrimSpace(req.Text) == "" {
		writeErr(w, http.StatusBadRequest, errors.New("text is required"))
		return
	}

	at := time.Now()
	if req.At != "" {
		parsed, err := time.Parse(time.RFC3339, req.At)
		if err != nil {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("parse at: %w", err))
			return
		}
		at = parsed
	}

	// The LLM calls can be slow; give them room without letting a hung
	// provider pin a connection forever.
	ctx, cancel := contextWithTimeout(r, 90*time.Second)
	defer cancel()

	res, err := s.App.Log(ctx, req.Text, at)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

type confirmRequest struct {
	Parsed *model.ParsedSession `json:"parsed"`
}

func (s *Server) handleConfirm(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("bad pending id"))
		return
	}
	var req confirmRequest
	if r.ContentLength > 0 {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("decode body: %w", err))
			return
		}
	}

	ctx, cancel := contextWithTimeout(r, 90*time.Second)
	defer cancel()

	res, err := s.App.ConfirmPending(ctx, id, req.Parsed)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleRank(w http.ResponseWriter, r *http.Request) {
	rk, err := s.App.Rank.Compute(r.Context(), time.Now())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, rk)
}

// profileResponse is everything the profile screen renders: what the user
// typed, what the engine derived from it, and what evidence exists.
//
// The derived half matters as much as the raw half. Lean body mass sets every
// strength reference in the system, so a user needs to see the number their
// rank is actually built on -- and whether it came from a measurement or a
// guess -- rather than only the inputs they supplied.
type profileResponse struct {
	HeightCm       float64 `json:"height_cm"`
	BodyweightKg   float64 `json:"bodyweight_kg"`
	BodyfatPct     float64 `json:"bodyfat_pct"`
	BFSource       string  `json:"bf_source"`
	Sex            string  `json:"sex"`
	TrainingMonths float64 `json:"training_months"`
	VO2maxEst      float64 `json:"vo2max_est"`
	SessionMinutes float64 `json:"avg_session_minutes"`
	GoalProfile    string  `json:"goal_profile"`

	// Derived.
	LBMKg     float64 `json:"lbm_kg"`
	FFMIAdj   float64 `json:"ffmi_adj"`
	Estimated bool    `json:"estimated"`
	Frozen    bool    `json:"frozen"`

	// Missing names the inputs that are costing the user score right now, so
	// the screen can say which field to fill rather than listing all of them
	// with equal weight.
	Missing []string      `json:"missing,omitempty"`
	Claims  []claimOut    `json:"claims"`
	Skills  []skillOut    `json:"skills"`
	Refs    []referenceKg `json:"references"`
}

type claimOut struct {
	Pattern string  `json:"pattern"`
	Name    string  `json:"name"`
	E1RMKg  float64 `json:"e1rm_kg"`
	Lift    string  `json:"lift"`
	// Anchor and Hint exist so the profile screen can say which lift a pattern
	// means and what number to type. "Horizontal Press" is the spec's language,
	// not a lifter's, and a field labelled only that is a guess waiting to
	// happen.
	Anchor string `json:"anchor"`
	Hint   string `json:"hint"`
}

type skillOut struct {
	Skill    string `json:"skill"`
	Unlocked bool   `json:"unlocked"`
}

// referenceKg is the load that scores 100 on each pattern for this body. It is
// the most concrete thing the profile can show: change your weight or body fat
// and these move, which makes the LBM/bodyweight split visible instead of
// theoretical.
type referenceKg struct {
	Pattern string  `json:"pattern"`
	Name    string  `json:"name"`
	RefKg   float64 `json:"ref_kg"`
}

func (s *Server) handleGetProfile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, err := berserk.LoadProfile(ctx, s.Store, time.Now())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	out := profileResponse{
		HeightCm:       p.HeightCm,
		BodyweightKg:   round1(p.BodyweightKg),
		BodyfatPct:     round1(p.BodyfatPct),
		BFSource:       p.BFSource,
		Sex:            s.Store.Setting(ctx, "sex", "male"),
		TrainingMonths: p.TrainingMonths,
		VO2maxEst:      p.VO2maxEst,
		SessionMinutes: p.SessionMinutes,
		GoalProfile:    p.GoalProfile,
		LBMKg:          round1(p.LBMKg),
		FFMIAdj:        round1(p.FFMIAdj),
		Estimated:      p.Estimated,
		Frozen:         p.Frozen,
	}

	// Each of these is a term reading zero or a guess purely because nothing
	// was entered, which is different from a term reading low because of how
	// someone trains.
	if s.Store.Setting(ctx, "bodyweight_kg", "") == "" {
		out.Missing = append(out.Missing, "bodyweight_kg")
	}
	if p.Estimated {
		out.Missing = append(out.Missing, "bodyfat_pct")
	}
	if s.Store.Setting(ctx, "height_cm", "") == "" {
		out.Missing = append(out.Missing, "height_cm")
	}
	if p.TrainingMonths <= 0 {
		out.Missing = append(out.Missing, "training_months")
	}
	if p.VO2maxEst <= 0 {
		out.Missing = append(out.Missing, "vo2max_est")
	}

	for _, pat := range berserk.Patterns {
		out.Refs = append(out.Refs, referenceKg{
			Pattern: string(pat), Name: pat.Display(),
			RefKg: round1(berserk.Ref(p, pat)),
		})
	}

	// Each cursor is drained and closed before the next query opens. The store
	// allows one connection, so overlapping cursors are a deadlock waiting for
	// a loop that exits early.
	claims := map[string]claimOut{}
	if rows, err := s.Store.DB().QueryContext(ctx, `SELECT pattern, e1rm_kg, lift FROM pattern_claims`); err == nil {
		for rows.Next() {
			var c claimOut
			if err := rows.Scan(&c.Pattern, &c.E1RMKg, &c.Lift); err == nil {
				claims[c.Pattern] = c
			}
		}
		rows.Close()
	}
	for _, pat := range berserk.Patterns {
		c := claims[string(pat)]
		c.Pattern, c.Name = string(pat), pat.Display()
		c.Anchor = berserk.AnchorLift(pat)
		if berserk.ScoredAgainstBodyweight(pat) {
			c.Hint = "total load: bodyweight + anything added"
		} else {
			c.Hint = "bar weight in kg"
		}
		out.Claims = append(out.Claims, c)
	}

	unlocked := map[string]bool{}
	if srows, err := s.Store.DB().QueryContext(ctx, `SELECT skill FROM skill_unlocks`); err == nil {
		for srows.Next() {
			var k string
			if err := srows.Scan(&k); err == nil {
				unlocked[k] = true
			}
		}
		srows.Close()
	}
	for _, sk := range berserk.Skills {
		out.Skills = append(out.Skills, skillOut{Skill: sk, Unlocked: unlocked[sk]})
	}

	writeJSON(w, http.StatusOK, out)
}

type setProfileRequest struct {
	HeightCm       *float64 `json:"height_cm,omitempty"`
	Sex            *string  `json:"sex,omitempty"`
	TrainingMonths *float64 `json:"training_months,omitempty"`
	VO2maxEst      *float64 `json:"vo2max_est,omitempty"`
	SessionMinutes *float64 `json:"avg_session_minutes,omitempty"`
	GoalProfile    *string  `json:"goal_profile,omitempty"`
}

func (s *Server) handleSetProfile(w http.ResponseWriter, r *http.Request) {
	var req setProfileRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	ctx := r.Context()

	// Bounds are rejections, not clamps. Silently correcting a typo hides it,
	// and every one of these moves a strength reference or an attribute.
	set := func(key string, v *float64, lo, hi float64) error {
		if v == nil {
			return nil
		}
		if *v < lo || *v > hi {
			return fmt.Errorf("%s must be between %g and %g", key, lo, hi)
		}
		return s.Store.SetSetting(ctx, key, strconv.FormatFloat(*v, 'f', -1, 64))
	}

	if err := set("height_cm", req.HeightCm, 120, 230); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := set("training_months", req.TrainingMonths, 0, 900); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := set("vo2max_est", req.VO2maxEst, 15, 90); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := set("avg_session_minutes", req.SessionMinutes, 15, 240); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if req.Sex != nil {
		if *req.Sex != "male" && *req.Sex != "female" {
			writeErr(w, http.StatusBadRequest, errors.New(`sex must be "male" or "female"`))
			return
		}
		if err := s.Store.SetSetting(ctx, "sex", *req.Sex); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
	}
	if req.GoalProfile != nil {
		switch *req.GoalProfile {
		case "balanced", "power", "physique":
		default:
			writeErr(w, http.StatusBadRequest, errors.New("goal_profile must be balanced, power or physique"))
			return
		}
		if err := s.Store.SetSetting(ctx, "goal_profile", *req.GoalProfile); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }

func settingFloat(ctx context.Context, st *store.Store, key string, def float64) float64 {
	v := st.Setting(ctx, key, "")
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return def
	}
	return f
}

type bodyRequest struct {
	Date         string   `json:"date,omitempty"` // YYYY-MM-DD, defaults to today
	BodyweightKg float64  `json:"bodyweight_kg"`
	BodyfatPct   *float64 `json:"bodyfat_pct,omitempty"`
	Source       string   `json:"bf_source,omitempty"` // dexa|caliper|navy|estimate

	// Tape measurements, centimetres. When bodyfat_pct is absent and these are
	// present the server computes it. Doing the arithmetic here rather than on
	// the phone keeps one implementation of the formula, and it is the formula
	// most likely to be got wrong: it takes a logarithm of (waist - neck).
	NeckCm  float64 `json:"neck_cm,omitempty"`
	WaistCm float64 `json:"waist_cm,omitempty"`
	HipCm   float64 `json:"hip_cm,omitempty"`
}

func (s *Server) handleBody(w http.ResponseWriter, r *http.Request) {
	var req bodyRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	// Bounds, not preferences: a value outside these is a typo or a unit
	// mix-up, and it would move every strength reference in the system.
	if req.BodyweightKg < 30 || req.BodyweightKg > 300 {
		writeErr(w, http.StatusBadRequest, errors.New("bodyweight_kg must be between 30 and 300"))
		return
	}
	if req.BodyfatPct != nil && (*req.BodyfatPct < 3 || *req.BodyfatPct > 60) {
		writeErr(w, http.StatusBadRequest, errors.New("bodyfat_pct must be between 3 and 60"))
		return
	}

	// Tape method, when no direct measurement was given. It needs height, which
	// comes from the profile rather than being asked for twice.
	if req.BodyfatPct == nil && req.WaistCm > 0 && req.NeckCm > 0 {
		h := settingFloat(r.Context(), s.Store, "height_cm", 0)
		if h <= 0 {
			writeErr(w, http.StatusBadRequest,
				errors.New("set your height in the profile before using the tape method"))
			return
		}
		sex := berserk.Sex(s.Store.Setting(r.Context(), "sex", "male"))
		bf, err := berserk.NavyBodyFat(sex, h, req.NeckCm, req.WaistCm, req.HipCm)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		req.BodyfatPct = &bf
		req.Source = "navy"
	}
	date := req.Date
	if date == "" {
		date = s.Store.LocalDate(time.Now())
	} else if _, err := time.Parse("2006-01-02", date); err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("date must be YYYY-MM-DD"))
		return
	}
	if err := s.Store.RecordBodyMetrics(r.Context(), date, req.BodyweightKg, req.BodyfatPct, req.Source); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	// Echo the computed body fat so a tape entry shows the user the number it
	// derived rather than making them refetch to find out.
	out := map[string]any{"status": "recorded", "date": date}
	if req.BodyfatPct != nil {
		out["bodyfat_pct"] = *req.BodyfatPct
		out["bf_source"] = req.Source
	}
	writeJSON(w, http.StatusOK, out)
}

type claimsRequest struct {
	Claims []struct {
		Pattern string  `json:"pattern"`
		E1RMKg  float64 `json:"e1rm_kg"`
		Lift    string  `json:"lift,omitempty"`
	} `json:"claims"`
}

func (s *Server) handleClaims(w http.ResponseWriter, r *http.Request) {
	var req claimsRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	valid := map[string]bool{}
	for _, p := range berserk.Patterns {
		valid[string(p)] = true
	}
	for _, c := range req.Claims {
		if !valid[c.Pattern] {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("unknown pattern %q", c.Pattern))
			return
		}
		if c.E1RMKg <= 0 || c.E1RMKg > 500 {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("implausible e1rm for %s", c.Pattern))
			return
		}
		if err := s.Store.SetPatternClaim(r.Context(), c.Pattern, c.E1RMKg, c.Lift); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "saved", "count": len(req.Claims)})
}

type skillsRequest struct {
	Skills map[string]bool `json:"skills"`
}

func (s *Server) handleSkills(w http.ResponseWriter, r *http.Request) {
	var req skillsRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	known := map[string]bool{}
	for _, s := range berserk.Skills {
		known[s] = true
	}
	for name, unlocked := range req.Skills {
		if !known[name] {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("unknown skill %q", name))
			return
		}
		if err := s.Store.SetSkill(r.Context(), name, unlocked); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "saved", "known": berserk.Skills})
}

// handleBlood returns the ledger tail, so the user can see what earned what
// rather than only a running total.
func (s *Server) handleBlood(w http.ResponseWriter, r *http.Request) {
	total, err := s.App.Rank.Ledger.Total(r.Context(), time.Now())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	rows, err := s.Store.DB().QueryContext(r.Context(), `
		SELECT source, amount, awarded_on, detail FROM blood_ledger
		ORDER BY awarded_on DESC, rowid DESC LIMIT 50`)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()

	type entry struct {
		Source string  `json:"source"`
		Amount float64 `json:"amount"`
		On     string  `json:"awarded_on"`
		Detail string  `json:"detail"`
	}
	out := []entry{}
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.Source, &e.Amount, &e.On, &e.Detail); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		out = append(out, e)
	}
	writeJSON(w, http.StatusOK, map[string]any{"blood": total, "recent": out})
}

// handleMuscles reports per-group volume over a trailing window, which is what
// the muscle map screen renders.
func (s *Server) handleMuscles(w http.ResponseWriter, r *http.Request) {
	days := 7
	if v := r.URL.Query().Get("days"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 3650 {
			days = n
		}
	}
	to := time.Now()
	from := to.AddDate(0, 0, -days+1)

	rep, err := muscle.Window(r.Context(), s.Store, from, to)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func (s *Server) handleExercises(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	query := exercises.Query{
		Text:      q.Get("q"),
		BodyPart:  q.Get("body_part"),
		Equipment: q.Get("equipment"),
		Target:    q.Get("target"),
	}
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			query.Limit = n
		}
	}
	if v := q.Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			query.Offset = n
		}
	}
	writeJSON(w, http.StatusOK, exercises.Search(query))
}

func (s *Server) handleFacets(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, exercises.AllFacets())
}

func (s *Server) handleExercise(w http.ResponseWriter, r *http.Request) {
	ex, ok := exercises.ByID(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, errors.New("no such exercise"))
		return
	}
	writeJSON(w, http.StatusOK, ex)
}

// handleMedia serves a demo image or animation from MediaDir.
//
// The files are not in this repository: they are 139 MB and the upstream
// dataset asks that anyone redistributing them review its licence first, so the
// operator populates a directory instead (scripts/fetch-media.sh). With no
// directory configured the route reports 404 and the phone simply shows no
// demo, which is a degraded library rather than a broken one.
func (s *Server) handleMedia(w http.ResponseWriter, r *http.Request) {
	if s.MediaDir == "" {
		writeErr(w, http.StatusNotFound, errors.New("media is not configured on this server"))
		return
	}
	kind := r.PathValue("kind")
	if kind != "gif" && kind != "img" {
		writeErr(w, http.StatusNotFound, errors.New("unknown media kind"))
		return
	}

	// The filename is taken from the library, never from user input, but this
	// route is on the public internet and a path-traversal bug here would serve
	// arbitrary files off the mini PC. Reject anything that is not a bare name.
	file := r.PathValue("file")
	if file == "" || file != filepath.Base(file) || strings.HasPrefix(file, ".") {
		writeErr(w, http.StatusBadRequest, errors.New("bad media name"))
		return
	}
	switch strings.ToLower(filepath.Ext(file)) {
	case ".gif", ".jpg", ".jpeg", ".png":
	default:
		writeErr(w, http.StatusBadRequest, errors.New("unsupported media type"))
		return
	}

	full := filepath.Join(s.MediaDir, kind, file)
	// Demo media never changes once fetched, so it is worth caching hard: these
	// are 96 KB animations and the phone would otherwise refetch them on every
	// scroll.
	w.Header().Set("Cache-Control", "public, max-age=604800, immutable")
	http.ServeFile(w, r, full)
}

func (s *Server) handleLifts(w http.ResponseWriter, r *http.Request) {
	out, err := lifts.List(r.Context(), s.Store, time.Now())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"lifts": out})
}

// handleSuggest powers the log box's autocomplete.
func (s *Server) handleSuggest(w http.ResponseWriter, r *http.Request) {
	limit := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	out, err := lifts.Suggest(r.Context(), s.Store, time.Now(), r.URL.Query().Get("q"), limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"suggestions": out})
}

func (s *Server) handleLift(w http.ResponseWriter, r *http.Request) {
	name, equipment, basis, ok := lifts.ParseKey(r.PathValue("key"))
	if !ok {
		writeErr(w, http.StatusBadRequest, errors.New("bad lift key"))
		return
	}
	d, err := lifts.Get(r.Context(), s.Store, time.Now(), name, equipment, basis)
	if err == sql.ErrNoRows {
		writeErr(w, http.StatusNotFound, errors.New("no working sets for that lift"))
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) handleNext(w http.ResponseWriter, r *http.Request) {
	rec, err := s.Sched.Recommend(r.Context(), time.Now())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

type historyRow struct {
	SessionID   int64  `json:"session_id"`
	LocalDate   string `json:"local_date"`
	SessionType string `json:"session_type"`
	RawText     string `json:"raw_text"`
	CoachReply  string `json:"coach_reply"`
	IsRestDay   bool   `json:"is_rest_day"`
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	limit := 30
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 365 {
			limit = n
		}
	}

	rows, err := s.Store.DB().QueryContext(r.Context(), `
		SELECT id, local_date, COALESCE(session_type, ''), raw_text,
		       COALESCE(coach_reply, ''), is_rest_day
		FROM sessions ORDER BY local_date DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()

	out := []historyRow{}
	for rows.Next() {
		var h historyRow
		var rest int
		if err := rows.Scan(&h.SessionID, &h.LocalDate, &h.SessionType, &h.RawText, &h.CoachReply, &rest); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		h.IsRestDay = rest == 1
		out = append(out, h)
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": out})
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("bad session id"))
		return
	}

	rows, err := s.Store.DB().QueryContext(r.Context(), `
		SELECT name, equipment, load_basis, set_type,
		       COALESCE(weight_kg, 0), COALESCE(reps, reps_low, 0), to_failure
		FROM v_sets WHERE session_id = ? ORDER BY exercise_id, position`, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()

	type setRow struct {
		Name      string  `json:"name"`
		Equipment string  `json:"equipment"`
		LoadBasis string  `json:"load_basis"`
		SetType   string  `json:"set_type"`
		WeightKg  float64 `json:"weight_kg"`
		Reps      int     `json:"reps"`
		ToFailure bool    `json:"to_failure"`
	}
	out := []setRow{}
	for rows.Next() {
		var sr setRow
		var fail int
		if err := rows.Scan(&sr.Name, &sr.Equipment, &sr.LoadBasis, &sr.SetType,
			&sr.WeightKg, &sr.Reps, &fail); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		sr.ToFailure = fail == 1
		out = append(out, sr)
	}
	writeJSON(w, http.StatusOK, map[string]any{"session_id": id, "sets": out})
}

type deviceRequest struct {
	Token    string `json:"token"`
	Platform string `json:"platform"`
}

func (s *Server) handleDevice(w http.ResponseWriter, r *http.Request) {
	var req deviceRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if req.Token == "" {
		writeErr(w, http.StatusBadRequest, errors.New("token is required"))
		return
	}
	if req.Platform == "" {
		req.Platform = "android"
	}
	if err := s.Store.RegisterDevice(r.Context(), req.Token, req.Platform); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "registered"})
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Store.DB().QueryContext(r.Context(), `SELECT key, value FROM settings`)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		out[k] = v
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleSetSetting(w http.ResponseWriter, r *http.Request) {
	var req map[string]string
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	for k, v := range req {
		if err := s.Store.SetSetting(r.Context(), k, v); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}

func (s *Server) handleTestPush(w http.ResponseWriter, r *http.Request) {
	tokens, err := s.Store.DeviceTokens(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	res, err := s.Push.Send(r.Context(), tokens, push.Message{
		Title: "Gym logger",
		Body:  "Push is wired up correctly.",
		Data:  map[string]string{"kind": "test"},
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	for _, stale := range res.Stale {
		_ = s.Store.DeleteDevice(r.Context(), stale)
	}
	writeJSON(w, http.StatusOK, map[string]any{"sent": res.Sent, "dropped": len(res.Stale)})
}

// ---------- helpers ----------

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}
