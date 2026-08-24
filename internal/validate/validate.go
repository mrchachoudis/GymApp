// Package validate is the gate between a language model's output and the
// database.
//
// This is the step the original design named in a code comment and then never
// wrote. It matters more than it looks: a single bad parse does not just
// produce one wrong row, it poisons every PR comparison that lift is ever
// involved in afterwards. A 1000 kg bench from a misheard word becomes a
// permanent ceiling nothing will ever beat, and the coach goes quiet about
// that lift forever.
package validate

import (
	"fmt"
	"strings"

	"github.com/mrcha/gymlogger/internal/model"
)

type Severity int

const (
	// Warn means the value was implausible but repairable. The session is
	// persisted with the repair applied and the fix is reported to the user.
	Warn Severity = iota
	// Fatal means the parse cannot be trusted. The session goes to
	// pending_parses for confirmation instead of into the history.
	Fatal
)

type Issue struct {
	Severity Severity
	Path     string
	Message  string
	Repaired string // non-empty when the validator changed a value
}

func (i Issue) String() string {
	s := i.Path + ": " + i.Message
	if i.Repaired != "" {
		s += " (" + i.Repaired + ")"
	}
	return s
}

type Result struct {
	Issues []Issue
}

func (r *Result) Fatal() bool {
	for _, i := range r.Issues {
		if i.Severity == Fatal {
			return true
		}
	}
	return false
}

func (r *Result) Reason() string {
	var parts []string
	for _, i := range r.Issues {
		if i.Severity == Fatal {
			parts = append(parts, i.String())
		}
	}
	return strings.Join(parts, "; ")
}

func (r *Result) Repairs() []string {
	var parts []string
	for _, i := range r.Issues {
		if i.Repaired != "" {
			parts = append(parts, i.String())
		}
	}
	return parts
}

// bounds are plausible per-set loads in kilograms, expressed in the units the
// load_basis implies. They are deliberately generous: the job is catching a
// field swap or a hallucinated digit, not policing whether Mario is strong.
type bounds struct{ minKg, maxKg float64 }

var perBasisBounds = map[model.LoadBasis]bounds{
	model.BasisTotal:   {0.5, 500},
	model.BasisPerSide: {0.5, 120},
	model.BasisPerLimb: {0.5, 200},
	model.BasisBWPlus:  {0.5, 150},
}

const (
	maxRepsPerSet   = 200
	maxSetsPerLift  = 30
	maxLiftsPerDay  = 25
	suspectRepsHigh = 60 // above this a "rep" count is probably a weight
)

// Session checks a parsed session and repairs what it safely can.
// The ParsedSession is mutated in place for Warn-level repairs.
func Session(p *model.ParsedSession) *Result {
	res := &Result{}

	if len(p.Exercises) > maxLiftsPerDay {
		res.Issues = append(res.Issues, Issue{
			Severity: Fatal, Path: "exercises",
			Message: fmt.Sprintf("%d exercises in one session is not a real workout", len(p.Exercises)),
		})
		return res
	}

	for i := range p.Exercises {
		ex := &p.Exercises[i]
		path := fmt.Sprintf("exercises[%d]", i)

		if strings.TrimSpace(ex.Name) == "" {
			res.Issues = append(res.Issues, Issue{
				Severity: Fatal, Path: path, Message: "exercise has no name",
			})
			continue
		}
		ex.Name = strings.ToLower(strings.TrimSpace(ex.Name))
		if ex.RawName == "" {
			ex.RawName = ex.Name
		}

		if !ex.Equipment.Valid() {
			res.Issues = append(res.Issues, Issue{
				Severity: Warn, Path: path,
				Message:  fmt.Sprintf("unknown equipment %q", ex.Equipment),
				Repaired: "set to other",
			})
			ex.Equipment = model.EquipOther
		}

		if len(ex.Sets) == 0 {
			res.Issues = append(res.Issues, Issue{
				Severity: Warn, Path: path,
				Message: "no sets recorded", Repaired: "exercise dropped",
			})
			continue
		}
		if len(ex.Sets) > maxSetsPerLift {
			res.Issues = append(res.Issues, Issue{
				Severity: Fatal, Path: path,
				Message: fmt.Sprintf("%d sets on one lift", len(ex.Sets)),
			})
			continue
		}

		for j := range ex.Sets {
			validateSet(res, ex, &ex.Sets[j], fmt.Sprintf("%s.sets[%d]", path, j))
		}
	}

	// Drop exercises that lost all their sets, so they do not create empty
	// rows that later look like a session where the lift was skipped.
	kept := p.Exercises[:0]
	for _, ex := range p.Exercises {
		if len(ex.Sets) > 0 && strings.TrimSpace(ex.Name) != "" {
			kept = append(kept, ex)
		}
	}
	p.Exercises = kept

	return res
}

func validateSet(res *Result, ex *model.Exercise, s *model.Set, path string) {
	if !s.SetType.Valid() {
		res.Issues = append(res.Issues, Issue{
			Severity: Warn, Path: path,
			Message:  fmt.Sprintf("unknown set_type %q", s.SetType),
			Repaired: "set to working",
		})
		s.SetType = model.SetWorking
	}

	if !s.LoadBasis.Valid() {
		repaired := model.BasisTotal
		if ex.Equipment == model.EquipDumbbell {
			repaired = model.BasisPerSide
		} else if ex.Equipment == model.EquipBodyweight {
			repaired = model.BasisBW
		}
		res.Issues = append(res.Issues, Issue{
			Severity: Warn, Path: path,
			Message:  fmt.Sprintf("unknown load_basis %q", s.LoadBasis),
			Repaired: "set to " + string(repaired),
		})
		s.LoadBasis = repaired
	}

	if s.Unit != nil && *s.Unit != model.UnitKg && *s.Unit != model.UnitLb {
		kg := model.UnitKg
		res.Issues = append(res.Issues, Issue{
			Severity: Warn, Path: path,
			Message:  fmt.Sprintf("unknown unit %q", *s.Unit),
			Repaired: "assumed kg",
		})
		s.Unit = &kg
	}
	if s.Weight != nil && s.Unit == nil {
		kg := model.UnitKg
		s.Unit = &kg
	}

	// A bodyweight set carrying a load is a contradiction; almost always the
	// model meant bodyweight_plus.
	if s.LoadBasis == model.BasisBW && s.Weight != nil && *s.Weight > 0 {
		res.Issues = append(res.Issues, Issue{
			Severity: Warn, Path: path,
			Message:  "bodyweight set has a load",
			Repaired: "changed to bodyweight_plus",
		})
		s.LoadBasis = model.BasisBWPlus
	}

	if s.Weight != nil {
		if *s.Weight < 0 {
			res.Issues = append(res.Issues, Issue{
				Severity: Fatal, Path: path,
				Message: fmt.Sprintf("negative weight %.1f", *s.Weight),
			})
			return
		}
		if kg := s.WeightKg(); kg != nil {
			if b, ok := perBasisBounds[s.LoadBasis]; ok && *kg > b.maxKg {
				res.Issues = append(res.Issues, Issue{
					Severity: Fatal, Path: path,
					Message: fmt.Sprintf("%.1f kg %s is outside plausible range (max %.0f)",
						*kg, s.LoadBasis, b.maxKg),
				})
				return
			}
		}
	}

	reps := s.Reps
	if reps != nil {
		switch {
		case *reps < 0:
			res.Issues = append(res.Issues, Issue{
				Severity: Fatal, Path: path,
				Message: fmt.Sprintf("negative reps %d", *reps),
			})
			return
		case *reps > maxRepsPerSet:
			res.Issues = append(res.Issues, Issue{
				Severity: Fatal, Path: path,
				Message: fmt.Sprintf("%d reps in one set", *reps),
			})
			return
		case *reps > suspectRepsHigh && s.Weight != nil && *s.Weight <= 20:
			// Classic field swap: "100 x 5" came back as weight 5, reps 100.
			res.Issues = append(res.Issues, Issue{
				Severity: Warn, Path: path,
				Message:  fmt.Sprintf("reps %d with weight %.1f looks like a swapped pair", *reps, *s.Weight),
				Repaired: "weight and reps exchanged",
			})
			w := float64(*reps)
			r := int(*s.Weight)
			s.Weight, s.Reps = &w, &r
		}
	}

	if s.RepsUncertain {
		if s.RepsLow != nil && s.RepsHigh != nil && *s.RepsLow > *s.RepsHigh {
			s.RepsLow, s.RepsHigh = s.RepsHigh, s.RepsLow
			res.Issues = append(res.Issues, Issue{
				Severity: Warn, Path: path,
				Message: "rep range inverted", Repaired: "bounds swapped",
			})
		}
	}

	if s.CleanReps != nil {
		eff := s.EffectiveReps()
		if *s.CleanReps < 0 || (eff > 0 && *s.CleanReps > eff) {
			res.Issues = append(res.Issues, Issue{
				Severity: Warn, Path: path,
				Message:  fmt.Sprintf("clean_reps %d exceeds reps %d", *s.CleanReps, eff),
				Repaired: "clamped to reps",
			})
			s.CleanReps = &eff
		}
	}

	if s.RPE != nil && (*s.RPE < 1 || *s.RPE > 10) {
		res.Issues = append(res.Issues, Issue{
			Severity: Warn, Path: path,
			Message: fmt.Sprintf("rpe %.1f out of range", *s.RPE), Repaired: "dropped",
		})
		s.RPE = nil
	}

	// A set with neither a load nor a rep count carries no information.
	if s.Weight == nil && s.Reps == nil && !s.RepsUncertain && s.LoadBasis != model.BasisBW {
		res.Issues = append(res.Issues, Issue{
			Severity: Warn, Path: path,
			Message: "set has no weight and no reps", Repaired: "kept as a placeholder",
		})
	}
}
