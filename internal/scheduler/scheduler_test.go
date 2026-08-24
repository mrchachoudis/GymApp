package scheduler

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/mrcha/gymlogger/internal/model"
	"github.com/mrcha/gymlogger/internal/store"
)

func f(v float64) *float64        { return &v }
func ip(v int) *int               { return &v }
func up(v model.Unit) *model.Unit { return &v }

func newStore(t *testing.T) *store.Store {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Bucharest")
	if err != nil {
		loc = time.UTC
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "sched.db"), loc)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func logTyped(t *testing.T, st *store.Store, day, sessionType string) {
	t.Helper()
	d, _ := time.Parse("2006-01-02", day)
	when := time.Date(d.Year(), d.Month(), d.Day(), 18, 0, 0, 0, st.Location())
	p := &model.ParsedSession{
		SessionType: &sessionType,
		RawText:     "test",
		LoggedAt:    when,
		Exercises: []model.Exercise{{
			Name: "bench press", RawName: "bench", Equipment: model.EquipBarbell,
			Sets: []model.Set{{
				SetType: model.SetWorking, LoadBasis: model.BasisTotal,
				Weight: f(100), Unit: up(model.UnitKg), Reps: ip(5),
			}},
		}},
	}
	if _, err := st.Persist(context.Background(), p); err != nil {
		t.Fatalf("persist: %v", err)
	}
}

// The whole point of the availability model: a reminder at 04:00 on a Saturday,
// mid night-shift, is not a reminder.
func TestNotAvailableDuringNightShift(t *testing.T) {
	st := newStore(t)
	s := New(st)
	loc := st.Location()

	// Saturday 2026-08-22 at 04:00, inside the Friday-night shift.
	during := time.Date(2026, 8, 22, 4, 0, 0, 0, loc)
	if s.Available(context.Background(), during) {
		t.Fatal("must not notify in the middle of a night shift")
	}

	// Saturday 10:00, still inside the post-shift sleep window.
	sleeping := time.Date(2026, 8, 22, 10, 0, 0, 0, loc)
	if s.Available(context.Background(), sleeping) {
		t.Fatal("must not notify while he is asleep after a shift")
	}

	// Saturday 18:00, awake and free.
	awake := time.Date(2026, 8, 22, 18, 0, 0, 0, loc)
	if !s.Available(context.Background(), awake) {
		t.Fatal("saturday evening should be available")
	}
}

func TestQuietHoursOnANormalNight(t *testing.T) {
	st := newStore(t)
	s := New(st)
	loc := st.Location()

	// Tuesday 03:00, no shift, just the small hours.
	if s.Available(context.Background(), time.Date(2026, 8, 18, 3, 0, 0, 0, loc)) {
		t.Fatal("3am is not a reasonable time to buzz anyone")
	}
	if !s.Available(context.Background(), time.Date(2026, 8, 18, 17, 0, 0, 0, loc)) {
		t.Fatal("tuesday afternoon should be available")
	}
}

// The rotation should move on rather than recommending the same session again.
func TestRotationAdvances(t *testing.T) {
	st := newStore(t)
	s := New(st)

	logTyped(t, st, "2026-08-17", "chest+triceps")
	rec, err := s.Recommend(context.Background(), time.Date(2026, 8, 19, 17, 0, 0, 0, st.Location()))
	if err != nil {
		t.Fatalf("recommend: %v", err)
	}
	if rec.Kind != "train" {
		t.Fatalf("expected a training recommendation, got %s (%s)", rec.Kind, rec.Reason)
	}
	if rec.SessionName == "chest+triceps" {
		t.Fatal("should not recommend the session trained two days ago when others are stale")
	}
}

// Recovery gates the rotation. Legs trained yesterday are not ready today even
// if legs are the most overdue slot on paper.
func TestUnrecoveredSessionIsNotRecommended(t *testing.T) {
	st := newStore(t)
	s := New(st)

	now := time.Date(2026, 8, 19, 17, 0, 0, 0, st.Location())
	logTyped(t, st, "2026-08-18", "legs+shoulders")

	rec, err := s.Recommend(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Kind == "train" && rec.SessionName == "legs+shoulders" {
		t.Fatal("legs trained yesterday must not be recommended today")
	}
}

func TestRestAfterConsecutiveDays(t *testing.T) {
	st := newStore(t)
	s := New(st)

	for _, d := range []string{"2026-08-17", "2026-08-18", "2026-08-19"} {
		logTyped(t, st, d, "chest+triceps")
	}
	rec, err := s.Recommend(context.Background(), time.Date(2026, 8, 19, 20, 0, 0, 0, st.Location()))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Kind != "rest" {
		t.Fatalf("three days straight should produce a rest recommendation, got %s", rec.Kind)
	}
}

// A restart must not re-send a reminder that already went out today.
func TestNotificationDedup(t *testing.T) {
	st := newStore(t)
	s := New(st)

	// Wednesday evening, nothing logged for days.
	now := time.Date(2026, 8, 19, 18, 0, 0, 0, st.Location())
	logTyped(t, st, "2026-08-14", "chest+triceps")

	first, err := s.Tick(context.Background(), now)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(first) == 0 {
		t.Fatal("expected at least one notification on the first tick")
	}

	second, err := s.Tick(context.Background(), now.Add(10*time.Minute))
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(second) != 0 {
		t.Fatalf("the same reminders must not fire twice in one day, got %+v", second)
	}
}

// Logging a session should silence the day's reminders.
func TestNoRemindersAfterTrainingToday(t *testing.T) {
	st := newStore(t)
	s := New(st)

	now := time.Date(2026, 8, 19, 18, 0, 0, 0, st.Location())
	logTyped(t, st, "2026-08-19", "chest+triceps")

	notifs, err := s.Tick(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(notifs) != 0 {
		t.Fatalf("no nagging after the session is already logged, got %+v", notifs)
	}
}

func TestInferGroupsHandlesFreestyleSessionTypes(t *testing.T) {
	groups := inferGroups("legs and some shoulders")
	has := map[MuscleGroup]bool{}
	for _, g := range groups {
		has[g] = true
	}
	for _, want := range []MuscleGroup{Quads, Hamstrings, Glutes, Shoulders} {
		if !has[want] {
			t.Fatalf("expected %s in inferred groups, got %v", want, groups)
		}
	}
}
