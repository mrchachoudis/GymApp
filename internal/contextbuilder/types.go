// Package contextbuilder turns training history into the CONTEXT block handed
// to the coach model.
//
// The governing rule: the model performs no arithmetic and evaluates no
// thresholds. Every comparison, percentage and boolean is decided here in Go
// against real rows. The model's only job is to phrase what this package
// already concluded.
//
// That is a change from the original design, which passed raw numbers like
// days_since_last_rest and then asked the prompt to check whether they crossed
// a line. Models are unreliable at threshold arithmetic, and a coach that
// occasionally invents a PR is worse than no coach.
package contextbuilder

// Flag is a pre-evaluated criticism. The coach addresses flags[0] and may
// mention flags[1]; it is forbidden from inventing its own.
type Flag struct {
	Type     string `json:"type"`
	Detail   string `json:"detail"`
	Priority int    `json:"priority"` // lower sorts first
}

// Flag types, in priority order.
const (
	FlagPain           = "pain"            // any joint or pain complaint
	FlagUnsafeSetup    = "unsafe_setup"    // heavy compound with no rack or spotter
	FlagLoadJump       = "load_jump"       // load rose faster than tissue adapts
	FlagFailureOveruse = "failure_overuse" // too many sets to failure in one session
	FlagNoRest         = "no_rest"         // no rest day in the recent window
	FlagNightShift     = "night_shift"     // heavy top set on no sleep
	FlagStale          = "stale"           // a lift has not moved in a long time
)

const (
	prioPain = iota + 1
	prioUnsafe
	prioLoadJump
	prioFailure
	prioNoRest
	prioNightShift
	prioStale
)

// LiftEntry is one lift's story for today, with today and the prior session
// already compared.
type LiftEntry struct {
	Name        string `json:"name"`
	Display     string `json:"display"`
	Equipment   string `json:"equipment"`
	LoadBasis   string `json:"load_basis"`
	TopSetToday string `json:"top_set_today"`

	IsBaseline     bool   `json:"is_baseline"`
	PreviousTopSet string `json:"previous_top_set,omitempty"`
	PreviousDate   string `json:"previous_date,omitempty"`
	IsWeightPR     bool   `json:"is_weight_pr"`
	IsRepPR        bool   `json:"is_rep_pr"`

	// e1RM is omitted above 8 reps, where Epley stops meaning anything.
	Est1RMToday    *float64 `json:"est_1rm_today,omitempty"`
	Est1RMPrevious *float64 `json:"est_1rm_previous,omitempty"`

	// Volume counts working sets only. Warmups vary with sleep and mood, so
	// including them makes week-over-week comparison noise.
	VolumeTodayKg    float64  `json:"volume_today_kg"`
	VolumePreviousKg *float64 `json:"volume_previous_kg,omitempty"`

	LoadJumpPct *float64 `json:"load_jump_pct,omitempty"`
}

// StaleLift is a lift whose top working weight has not moved. Staleness is
// measured in days as well as appearances: five sessions of a lift trained
// weekly is a five-week stall, five sessions of one trained three times a week
// is under a fortnight and completely normal.
type StaleLift struct {
	Name        string  `json:"name"`
	Display     string  `json:"display"`
	TopWeightKg float64 `json:"top_weight_kg"`
	Sessions    int     `json:"sessions"`
	Days        int     `json:"days"`
}

// Context is the JSON block sent as the coach's user message alongside the
// logged session.
type Context struct {
	Date              string `json:"date"`
	DaysSinceLastRest int    `json:"days_since_last_rest"`
	SessionsThisWeek  int    `json:"sessions_this_week"`
	PostNightShift    bool   `json:"post_night_shift"`

	Flags []Flag `json:"flags"`

	LiftHistory []LiftEntry `json:"lift_history"`
	StaleLifts  []StaleLift `json:"stale_lifts"`
	Streaks     []string    `json:"streaks,omitempty"`

	// Suggestion is the progression line, computed here so the coach cannot
	// invent a number. Empty means say nothing about progression.
	Suggestion string `json:"suggestion,omitempty"`

	RankDelta string `json:"rank_delta,omitempty"`
}
