// Command gymd is the gym logger service.
//
// It runs on the mini PC: HTTP API for the phone, a scheduler loop for
// reminders, and SQLite on local disk. Nothing here is multi-user; there is
// one lifter and one database.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/mrcha/gymlogger/internal/api"
	"github.com/mrcha/gymlogger/internal/app"
	"github.com/mrcha/gymlogger/internal/berserk"
	"github.com/mrcha/gymlogger/internal/llm"
	"github.com/mrcha/gymlogger/internal/push"
	"github.com/mrcha/gymlogger/internal/scheduler"
	"github.com/mrcha/gymlogger/internal/store"
)

func main() {
	var (
		dbPath   = flag.String("db", env("GYM_DB", "gym.db"), "path to the SQLite database")
		addr     = flag.String("addr", env("GYM_ADDR", "127.0.0.1:8080"), "listen address")
		tz       = flag.String("tz", env("GYM_TZ", "Europe/Bucharest"), "user timezone")
		logText  = flag.String("log", "", "log a session from the command line and exit")
		showNext = flag.Bool("next", false, "print the next recommended session and exit")
		showRank = flag.Bool("rank", false, "print the current rank and exit")
	)
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	loc, err := time.LoadLocation(*tz)
	if err != nil {
		logger.Error("bad timezone", "tz", *tz, "err", err)
		os.Exit(1)
	}

	st, err := store.Open(*dbPath, loc)
	if err != nil {
		logger.Error("open store", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	// Bind to 127.0.0.1 by default. The Cloudflare Tunnel connects outbound
	// from this machine, so the service never needs to listen on the LAN, let
	// alone the internet.
	client := buildLLMClient(logger)
	application := app.New(st, client, logger)
	sched := scheduler.New(st)
	sender := buildPushSender(logger)

	switch {
	case *logText != "":
		runOneShotLog(application, *logText, logger)
		return
	case *showNext:
		runShowNext(sched, logger)
		return
	case *showRank:
		runShowRank(application, logger)
		return
	}

	authToken := os.Getenv("GYM_AUTH_TOKEN")
	if authToken == "" {
		logger.Error("GYM_AUTH_TOKEN is required; this service is reachable from the internet through the tunnel")
		os.Exit(1)
	}

	// Demo media is optional and lives outside the repository; see
	// scripts/fetch-media.sh. Without it the exercise library still works, just
	// without animations.
	mediaDir := env("GYM_MEDIA_DIR", "")
	if mediaDir != "" {
		if _, err := os.Stat(mediaDir); err != nil {
			logger.Warn("GYM_MEDIA_DIR is set but unreadable, serving the library without demos",
				"dir", mediaDir, "err", err)
			mediaDir = ""
		} else {
			logger.Info("serving exercise media", "dir", mediaDir)
		}
	}

	srv := &api.Server{
		App:       application,
		Store:     st,
		Sched:     sched,
		Push:      sender,
		Logger:    logger,
		AuthToken: authToken,
		MediaDir:  mediaDir,
	}

	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
		// No WriteTimeout: a log request legitimately waits on two LLM calls,
		// and the per-request context already bounds it.
		IdleTimeout: 120 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go runScheduler(ctx, sched, st, sender, logger)

	go func() {
		logger.Info("listening", "addr", *addr, "db", *dbPath, "tz", *tz)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdownCtx)
}

// buildLLMClient falls back to the deterministic stub when no API key is set,
// so the service is fully usable before an OpenCode key exists.
func buildLLMClient(logger *slog.Logger) llm.Client {
	key := os.Getenv("OPENCODE_API_KEY")
	if key == "" {
		logger.Warn("OPENCODE_API_KEY not set, using the offline stub parser and coach")
		return llm.Stub{}
	}
	return llm.NewHTTPClient(key, env("OPENCODE_ENDPOINT", llm.DefaultEndpoint))
}

func buildPushSender(logger *slog.Logger) push.Sender {
	path := os.Getenv("FCM_CREDENTIALS")
	if path == "" {
		logger.Warn("FCM_CREDENTIALS not set, notifications will be logged instead of sent")
		return &push.Noop{}
	}
	sender, err := push.NewFCM(context.Background(), path)
	if err != nil {
		logger.Error("fcm setup failed, falling back to no-op", "err", err)
		return &push.Noop{}
	}
	logger.Info("fcm ready", "project", sender.ProjectID)
	return sender
}

// runScheduler ticks every few minutes rather than on a cron expression.
// Reminders depend on state that changes between fixed times (a session logged
// at 16:50 should cancel the 17:00 reminder), and the dedup ledger makes
// frequent ticks harmless.
func runScheduler(ctx context.Context, sched *scheduler.Scheduler, st *store.Store,
	sender push.Sender, logger *slog.Logger) {

	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	tick := func() {
		notifs, err := sched.Tick(ctx, time.Now())
		if err != nil {
			logger.Error("scheduler tick", "err", err)
			return
		}
		if len(notifs) == 0 {
			return
		}
		tokens, err := st.DeviceTokens(ctx)
		if err != nil {
			logger.Error("device tokens", "err", err)
			return
		}
		for _, n := range notifs {
			res, err := sender.Send(ctx, tokens, push.Message{
				Title: n.Title,
				Body:  n.Body,
				Data:  map[string]string{"kind": n.Kind},
			})
			if err != nil {
				logger.Error("push send", "kind", n.Kind, "err", err)
				continue
			}
			for _, stale := range res.Stale {
				_ = st.DeleteDevice(ctx, stale)
			}
			logger.Info("notification sent", "kind", n.Kind, "title", n.Title, "devices", res.Sent)
		}
	}

	tick()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tick()
		}
	}
}

// ---------- one-shot CLI modes ----------

func runOneShotLog(a *app.App, text string, logger *slog.Logger) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	res, err := a.Log(ctx, text, time.Now())
	if err != nil {
		logger.Error("log failed", "err", err)
		os.Exit(1)
	}

	if res.Needs != "" {
		fmt.Printf("Needs confirmation: %s\n", res.Needs)
		if res.PendingID != 0 {
			fmt.Printf("Pending id: %d\n", res.PendingID)
		}
	}
	for _, r := range res.Repairs {
		fmt.Printf("Repaired: %s\n", r)
	}
	if res.SessionID != 0 {
		fmt.Printf("Session %d logged.\n\n", res.SessionID)
	}
	if res.Reply != "" {
		fmt.Println(res.Reply)
		fmt.Println()
	}
	if res.Rank != nil {
		printRank(res.Rank)
	}
}

// printRank renders the one-screen summary.
//
// Erratum 1 governs the shape: below the Berserk boundary a composite is the
// honest readout, and at the boundary it is actively misleading, because a
// lifter can sit above the old RS threshold and still be one attribute short.
// So the top two ranks get the gate table instead of a number.
func printRank(r *berserk.Rank) {
	fmt.Printf("\n%s   RS %.1f\n", r.Rank, r.RS)

	if r.ShowGates {
		for _, g := range r.Berserk.Gates {
			mark := "x"
			if g.Pass {
				mark = "+"
			}
			fmt.Printf("  %-11s %5.1f / %-3.0f %s", g.Name, g.Value, g.Threshold, mark)
			if g.Fix != "" {
				fmt.Printf("   %s", g.Fix)
			}
			fmt.Println()
		}
		fmt.Printf("  %-11s %d / 6 verified\n", "PATTERNS", r.Berserk.PatternsVerified)
		fmt.Printf("  %-11s %5.1f / %-3.0f\n", "MIN PATTERN", r.Berserk.MinPattern,
			berserk.RungByIndex(14).PatternFloor)
		fmt.Printf("\n%s\n", r.Berserk.Summary)
	} else if r.NextRank != "" {
		fmt.Printf("RS %.0f / %.0f -> %s\n", r.RS, r.RS+r.ToNext, r.NextRank)
	}

	fmt.Printf("\nMIGHT %.0f  DOMINION %.0f  FRAME %.0f  VIGOR %.0f  DISCIPLINE %.0f  MASTERY %.0f\n",
		r.Attributes.Might, r.Attributes.Dominion, r.Attributes.Frame,
		r.Attributes.Vigor, r.Attributes.Discipline, r.Attributes.Mastery)

	for _, p := range r.Patterns {
		note := string(p.Status)
		if p.Imputed {
			note = "UNTESTED (imputed)"
		}
		fmt.Printf("  %-8s %5.1f  %s\n", p.Pattern.Short(), p.Score, note)
	}

	if r.Blood.Total > 0 {
		fmt.Printf("\n%s   %.0f Blood (+%.0f last 30d), %.0f to %s\n",
			r.Blood.TierName, r.Blood.Total, r.Blood.Last30Day, r.Blood.ToNext, r.Blood.NextTier)
	}
	fmt.Printf("Threat level %.0f   confidence %.0f%%\n", r.ThreatLevel, r.Confidence*100)

	if r.WeakLink != "" {
		fmt.Printf("\n%s\n", r.WeakLink)
	}
	for _, n := range r.Notes {
		fmt.Printf("  note: %s\n", n)
	}
}

func runShowNext(sched *scheduler.Scheduler, logger *slog.Logger) {
	rec, err := sched.Recommend(context.Background(), time.Now())
	if err != nil {
		logger.Error("recommend failed", "err", err)
		os.Exit(1)
	}
	b, _ := json.MarshalIndent(rec, "", "  ")
	fmt.Println(string(b))
}

func runShowRank(a *app.App, logger *slog.Logger) {
	r, err := a.Rank.Compute(context.Background(), time.Now())
	if err != nil {
		logger.Error("rank failed", "err", err)
		os.Exit(1)
	}
	printRank(r)
}

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}
