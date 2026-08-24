// Package scheduler decides what Mario should train next and when it is
// reasonable to say so.
//
// This is the part the original design treated as a cron job, which it is not.
// "Tell me to train legs" requires knowing what was trained, what has
// recovered, what is overdue, and whether the person is currently asleep after
// a night shift. That is a rotation state machine with a quiet-hours model on
// top, and it earns its own package.
package scheduler

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// MuscleGroup is a recovery unit. The scheduler tracks recovery per group
// rather than per session type, so an unplanned extra chest day still delays
// the next push session.
type MuscleGroup string

const (
	Chest      MuscleGroup = "chest"
	Back       MuscleGroup = "back"
	Shoulders  MuscleGroup = "shoulders"
	Biceps     MuscleGroup = "biceps"
	Triceps    MuscleGroup = "triceps"
	Quads      MuscleGroup = "quads"
	Hamstrings MuscleGroup = "hamstrings"
	Glutes     MuscleGroup = "glutes"
	Calves     MuscleGroup = "calves"
	Forearms   MuscleGroup = "forearms"
	Core       MuscleGroup = "core"
)

// recoveryHours is how long a group needs before it is worth training hard
// again. Large groups take longer; forearms and calves take almost nothing,
// which is why they can appear in consecutive sessions without complaint.
var recoveryHours = map[MuscleGroup]float64{
	Chest: 48, Back: 48, Shoulders: 48,
	Quads: 72, Hamstrings: 72, Glutes: 72,
	Biceps: 36, Triceps: 36,
	Calves: 24, Forearms: 24, Core: 24,
}

// SessionTemplate is one slot in the rotation.
type SessionTemplate struct {
	Name   string        `json:"name"`
	Groups []MuscleGroup `json:"groups"`
}

// Split is the rotation. It is stored in settings as JSON so it can be changed
// from the app without a redeploy.
type Split struct {
	Templates []SessionTemplate `json:"templates"`
	// MaxConsecutiveDays is how many training days in a row before the
	// scheduler starts recommending a rest day instead of a session.
	MaxConsecutiveDays int `json:"max_consecutive_days"`
	// TargetPerWeek is the weekly session count the nudges aim at.
	TargetPerWeek int `json:"target_per_week"`
}

// DefaultSplit is a four-day upper/lower rotation that matches the session
// types in Mario's own logs (back+biceps, legs+shoulders, chest+triceps).
func DefaultSplit() Split {
	return Split{
		MaxConsecutiveDays: 3,
		TargetPerWeek:      4,
		Templates: []SessionTemplate{
			{Name: "chest+triceps", Groups: []MuscleGroup{Chest, Triceps, Shoulders}},
			{Name: "back+biceps", Groups: []MuscleGroup{Back, Biceps, Forearms}},
			{Name: "legs+shoulders", Groups: []MuscleGroup{Quads, Hamstrings, Glutes, Shoulders, Calves}},
			{Name: "arms+core", Groups: []MuscleGroup{Biceps, Triceps, Forearms, Core}},
		},
	}
}

func ParseSplit(s string) (Split, error) {
	if strings.TrimSpace(s) == "" {
		return DefaultSplit(), nil
	}
	var sp Split
	if err := json.Unmarshal([]byte(s), &sp); err != nil {
		return DefaultSplit(), fmt.Errorf("parse split: %w", err)
	}
	if len(sp.Templates) == 0 {
		return DefaultSplit(), nil
	}
	if sp.MaxConsecutiveDays == 0 {
		sp.MaxConsecutiveDays = 3
	}
	if sp.TargetPerWeek == 0 {
		sp.TargetPerWeek = 4
	}
	return sp, nil
}

func (s Split) JSON() string {
	b, _ := json.Marshal(s)
	return string(b)
}

// Readiness scores how ready a template is to be trained, given when each of
// its groups was last hit. 1.0 means fully recovered; below 1.0 means at least
// one group is still inside its window.
func (t SessionTemplate) Readiness(now time.Time, lastHit map[MuscleGroup]time.Time) float64 {
	if len(t.Groups) == 0 {
		return 1
	}
	worst := 1.0
	for _, g := range t.Groups {
		last, ok := lastHit[g]
		if !ok {
			continue // never trained, fully ready
		}
		need := recoveryHours[g]
		if need == 0 {
			need = 48
		}
		elapsed := now.Sub(last).Hours()
		r := elapsed / need
		if r > 1 {
			r = 1
		}
		if r < worst {
			worst = r
		}
	}
	return worst
}

// Staleness is how overdue a template is, in days since it was last trained.
// Combined with readiness it produces the rotation: recovered and long-unhit
// wins over recovered and recent.
func (t SessionTemplate) Staleness(now time.Time, lastSession map[string]time.Time) float64 {
	last, ok := lastSession[t.Name]
	if !ok {
		return 14 // never trained, treat as very overdue but not infinite
	}
	return now.Sub(last).Hours() / 24
}
