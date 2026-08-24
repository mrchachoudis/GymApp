// Package model defines the wire and storage types for a logged session.
//
// ParsedSession mirrors the parser prompt's JSON schema exactly. It is the
// untrusted output of a language model: nothing in it is safe to persist
// until it has been through validate.Session.
package model

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// ---------- enums ----------

type SetType string

const (
	SetWarmup  SetType = "warmup"
	SetWorking SetType = "working"
	SetBackoff SetType = "backoff"
	SetDrop    SetType = "drop"
)

// CountsTowardVolume reports whether a set belongs in working-volume totals.
// Warmups are excluded: their count varies with sleep, temperature and mood,
// so including them makes week-over-week volume comparisons meaningless.
func (s SetType) CountsTowardVolume() bool {
	return s == SetWorking || s == SetBackoff || s == SetDrop
}

// CountsTowardPR reports whether a set may set a personal record. Only true
// working sets qualify; a drop set at a lighter weight is not a rep PR.
func (s SetType) CountsTowardPR() bool { return s == SetWorking }

func (s SetType) Valid() bool {
	switch s {
	case SetWarmup, SetWorking, SetBackoff, SetDrop:
		return true
	}
	return false
}

type LoadBasis string

const (
	BasisTotal   LoadBasis = "total"
	BasisPerSide LoadBasis = "per_side"
	BasisPerLimb LoadBasis = "per_limb"
	BasisBW      LoadBasis = "bodyweight"
	BasisBWPlus  LoadBasis = "bodyweight_plus"
)

func (b LoadBasis) Valid() bool {
	switch b {
	case BasisTotal, BasisPerSide, BasisPerLimb, BasisBW, BasisBWPlus:
		return true
	}
	return false
}

type Equipment string

const (
	EquipBarbell    Equipment = "barbell"
	EquipDumbbell   Equipment = "dumbbell"
	EquipCable      Equipment = "cable"
	EquipMachine    Equipment = "machine"
	EquipBodyweight Equipment = "bodyweight"
	EquipOther      Equipment = "other"
)

func (e Equipment) Valid() bool {
	switch e {
	case EquipBarbell, EquipDumbbell, EquipCable, EquipMachine, EquipBodyweight, EquipOther:
		return true
	}
	return false
}

type Unit string

const (
	UnitKg Unit = "kg"
	UnitLb Unit = "lb"
)

const lbToKg = 0.45359237

// ---------- parsed (untrusted) types ----------

type Set struct {
	SetType       SetType   `json:"set_type"`
	Weight        *float64  `json:"weight"`
	Unit          *Unit     `json:"unit"`
	LoadBasis     LoadBasis `json:"load_basis"`
	Reps          *int      `json:"reps"`
	RepsUncertain bool      `json:"reps_uncertain"`
	RepsLow       *int      `json:"reps_low"`
	RepsHigh      *int      `json:"reps_high"`
	ToFailure     bool      `json:"to_failure"`
	CleanReps     *int      `json:"clean_reps"`
	RPE           *float64  `json:"rpe"`
	Notes         *string   `json:"notes"`
}

type Exercise struct {
	Name      string    `json:"name"`
	RawName   string    `json:"raw_name"`
	Equipment Equipment `json:"equipment"`
	Sets      []Set     `json:"sets"`
}

type Cardio struct {
	Modality string   `json:"modality"`
	Minutes  *float64 `json:"minutes"`
	Speed    *float64 `json:"speed"`
	Incline  *float64 `json:"incline"`
	Notes    *string  `json:"notes"`
}

type Subjective struct {
	Energy       *string `json:"energy"`
	Pain         *string `json:"pain"`
	SleepOrShift *string `json:"sleep_or_shift"`
	Notes        *string `json:"notes"`
}

type Assumption struct {
	Path   string `json:"path"`
	Field  string `json:"field"`
	Value  string `json:"value"`
	Reason string `json:"reason"`
}

type UnresolvedRef struct {
	Path string `json:"path"`
	Text string `json:"text"`
}

// ParsedSession is exactly what the parser model returns.
type ParsedSession struct {
	SessionType          *string         `json:"session_type"`
	Exercises            []Exercise      `json:"exercises"`
	Cardio               []Cardio        `json:"cardio"`
	Subjective           Subjective      `json:"subjective"`
	Assumptions          []Assumption    `json:"assumptions"`
	UnresolvedReferences []UnresolvedRef `json:"unresolved_references"`
	Unparsed             *string         `json:"unparsed"`

	// Populated by the server, never by the model.
	RawText  string    `json:"raw_text,omitempty"`
	LoggedAt time.Time `json:"logged_at,omitempty"`
}

// IsEmpty reports whether the parse found no trainable content.
func (p *ParsedSession) IsEmpty() bool {
	return len(p.Exercises) == 0 && len(p.Cardio) == 0
}

// NeedsConfirmation reports whether the app must ask before trusting the
// parse. Unresolved references are the blocking case: the model was pointed
// at information it does not have, so some field is a hole rather than a guess.
func (p *ParsedSession) NeedsConfirmation() bool {
	return len(p.UnresolvedReferences) > 0
}

// ---------- normalization ----------

// WeightKg returns the set's load converted to kilograms, leaving the
// load_basis semantics untouched. A per_side 20 lb dumbbell becomes a
// per_side 9.07 kg dumbbell, not a 18.14 kg total.
//
// Storing one canonical unit means PR queries never compare kg against lb,
// which is the failure mode that silently produces fake records.
func (s Set) WeightKg() *float64 {
	if s.Weight == nil {
		return nil
	}
	w := *s.Weight
	if s.Unit != nil && *s.Unit == UnitLb {
		w *= lbToKg
	}
	w = math.Round(w*100) / 100
	return &w
}

// EffectiveLoadKg returns the total system load moved in one rep, used only
// for volume. bodyweight rows need the user's bodyweight to mean anything,
// which is why it is a parameter rather than an assumption.
func (s Set) EffectiveLoadKg(bodyweightKg float64) float64 {
	w := 0.0
	if kg := s.WeightKg(); kg != nil {
		w = *kg
	}
	switch s.LoadBasis {
	case BasisPerSide, BasisPerLimb:
		return w * 2
	case BasisBW:
		return bodyweightKg
	case BasisBWPlus:
		return bodyweightKg + w
	default:
		return w
	}
}

// EffectiveReps resolves an uncertain rep count to a single number for math.
// The low end is used deliberately: when Mario is not sure whether it was 10
// or 12, crediting him with 12 inflates every downstream comparison.
func (s Set) EffectiveReps() int {
	if s.Reps != nil {
		return *s.Reps
	}
	if s.RepsUncertain && s.RepsLow != nil {
		return *s.RepsLow
	}
	return 0
}

// LiftKey identifies a comparable lift. Name alone is not enough: a 30 kg
// per-side dumbbell row and a 30 kg total machine row share a canonical name
// but are different movements with different loads. Comparing them produces
// PRs that never happened.
type LiftKey struct {
	Name      string
	Equipment Equipment
	LoadBasis LoadBasis
}

func (k LiftKey) String() string {
	return fmt.Sprintf("%s|%s|%s", k.Name, k.Equipment, k.LoadBasis)
}

// Display renders a lift key for human-facing text.
func (k LiftKey) Display() string {
	switch k.LoadBasis {
	case BasisPerSide:
		return k.Name + " (per side)"
	case BasisPerLimb:
		return k.Name + " (per leg)"
	case BasisBWPlus:
		return k.Name + " (weighted)"
	default:
		return k.Name
	}
}

func KeyFor(ex Exercise, s Set) LiftKey {
	return LiftKey{
		Name:      strings.ToLower(strings.TrimSpace(ex.Name)),
		Equipment: ex.Equipment,
		LoadBasis: s.LoadBasis,
	}
}

// Epley1RM estimates a one-rep max. It is only meaningful under about 8 reps;
// above that the formula drifts badly and the caller should not display it.
func Epley1RM(weightKg float64, reps int) (float64, bool) {
	if reps <= 0 || reps > 8 || weightKg <= 0 {
		return 0, false
	}
	v := weightKg * (1 + float64(reps)/30.0)
	return math.Round(v*10) / 10, true
}
