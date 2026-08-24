// Package api is the HTTP surface the phone talks to.
//
// This service sits behind a Cloudflare Tunnel, which means it has a public
// hostname. Anyone who finds that hostname can reach it, so every route except
// the health check requires a bearer token. A tunnel is not a firewall.
package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mrcha/gymlogger/internal/app"
	"github.com/mrcha/gymlogger/internal/model"
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
