# Design notes

Why the code is shaped the way it is, mostly recording decisions that look
arbitrary until the thing they prevent happens.

## The model does no arithmetic

The original sketch passed raw numbers to the coach and then asked the prompt to
evaluate them: "flag zero rest days in the last 6 logged days", "flag a load
jump over 15 percent". That is six threshold comparisons handed to a model, in a
system whose stated rule was that the app does the maths.

Now every condition is evaluated in Go and arrives pre-decided:

```json
"flags": [
  {"type": "pain", "detail": "left elbow on skull crushers", "priority": 1},
  {"type": "failure_overuse", "detail": "3 sets taken to failure", "priority": 4}
]
```

The coach prompt collapses to "address `flags[0]`". This also fixes the length
problem: when two conditions fire at once, six lines is not enough to cover both
well, and priority ordering decides which one wins instead of the model guessing.

## set_type, or why volume was meaningless

There was no warmup/working distinction, so `volume_today_kg` included warmups.
Do more warmup sets because you slept badly and the coach congratulates you on
increased volume. Every set now carries a `set_type`, and only working, backoff
and drop sets count.

The same field fixes the top set: without it, a heavy warmup single sorts above
the real working set.

## LiftKey, or why PRs were fake

Keying history on the exercise name alone means a 30 kg per-side dumbbell row
(60 kg of load) and a 30 kg total machine row compare as the same lift. Whoever
does the machine version second gets a PR that never happened.

History is keyed on `(name, equipment, load_basis)`. Units are normalized to kg
at insert rather than at query time, because a kg-versus-lb comparison is the
same bug wearing a different hat.

## What counts as a rep PR

First attempt: more reps than the best previous set at this weight *or heavier*.
That awards a rep PR to 70 kg x 12 after 100 kg x 5, which is a deload wearing a
medal. It was caught by a test, not by reading.

The rule now needs three things: the exact weight has been handled before, the
reps beat the best at that weight, and the weight is within 10 percent of the
all-time best. Adding reps to a light backoff set is not progress.

## Comparisons run on the session timeline, not the date

Cutting history at `local_date < today` meant a second session logged the same
day could not see the first, so an evening entry after a morning one reported
every lift as a fresh baseline. Queries now exclude the current session by id
and compare on `logged_at`.

## Staleness is frequency-aware

"Same top weight for 5 sessions" means five weeks for a weekly lift and twelve
days for one trained three times a week. The first is a real stall; the second
is a normal fortnight. A lift is stale only when it clears both a session count
and an elapsed-days threshold.

## Validation is not optional

The pipeline was documented as parse -> validate -> persist and implemented as
parse -> persist. The difference matters more than it looks: one bad row does
not just produce one wrong entry, it becomes a permanent ceiling. A 900 kg bench
from a misheard word is never beaten, so that lift never gets another PR and the
coach goes quiet about it forever.

Fatal values and unresolved references both park in `pending_parses` rather than
being written. Repairable problems — a swapped weight/reps pair, an out-of-range
RPE, an unknown enum — are fixed and reported.

## Prompt rules that can be enforced are enforced

Open models leak emoji no matter how many capitals the prompt uses. `StripEmoji`
is three lines and guarantees it. The banned-metaphor list triggers exactly one
regeneration and then ships anyway, because a slightly off-voice reply beats no
reply and the session is already safely logged.

## The retry that would not have worked

"Retry once with the error appended" is correct, but only because appending the
error changes the input. At temperature 0, an identical request produces an
identical failure — a retry that changes nothing is just a second bill.

## Rank

Two halves, both chosen for what they cannot be gamed into saying.

**Consistency** counts qualifying training days over 28 days, where a day needs
at least six working sets to qualify. Credit per week is capped at five days, so
grinding seven days never outscores a sane schedule. A gap over four days costs
points; the penalty is capped so a long layoff floors the score instead of
driving it absurdly negative.

**Strength** measures bodyweight multiples against conventional standards, using
the best estimate in a 90-day window so a deload costs nothing. It is scaled by
coverage: one strong lift is not a strong lifter, so full credit needs at least
three tracked lifts.

Seven tiers with three divisions each gives 21 steps, which is granular enough
that progress is visible weekly rather than yearly.

## The scheduler is a state machine, not a cron job

"Tell me to train legs" needs to know what was trained, what has recovered, what
is overdue, and whether the person is asleep after a night shift. Recovery is
tracked per muscle group rather than per session type, so an unplanned extra
chest day correctly delays the next push session.

Availability is the part that decides whether the app survives contact with a
real schedule. A reminder at 04:00 on a Saturday, mid-shift, is not a reminder.

Dedup keys are date-scoped, so a service restart cannot re-nag and a new day
naturally allows one fresh reminder.

## Known gaps

- **Editing a persisted session.** The confirm path handles a bad parse before
  it is written, but there is no endpoint to fix a session logged three days ago.
- **Session-type inference** falls back to keyword matching when a freestyle
  session does not match a split template. It is adequate, not clever.
- **Bodyweight is a single setting**, so historical bodyweight-lift volume is
  computed against today's bodyweight rather than the bodyweight at the time.
- **The coach is single-turn by design**, so it cannot answer "what do you mean".
  A conversational mode would need its own prompt and its own history handling.

## The Berserk rank system, and what the specs did not settle

The rank engine implements the Berserk Rank System v1.3, which arrived as three
layers: v1.0 supplies the mathematics, v1.2 patches the ladder and the gates and
restores v1.0's formulas verbatim, and v1.3 is errata over v1.2. Where the
layers disagree the later one wins, and each such site is marked in the code
with its erratum number.

Five things were underdetermined. Each is marked `CONSTRUCTED` at its site;
one of the five has since been resolved and is struck through below.

**The two machine coefficients.** v1.0 §4.2 lists machine chest press and hack
squat with a confidence but no conversion coefficient. They are given 1.00 and
the machine cap of 85, on the same reasoning that caps leg press: without a
per-machine calibration the load number is unanchored, so it may support a
mid-range score and no more.

**Weekly hard sets.** ~~v1.0 §6.3 wants a per-muscle-group median, and there is
no exercise-to-muscle-group map.~~ **Resolved.** `internal/muscle` now supplies
the mapping, and `V_volume` takes the real per-group median. See "The muscle
map" below.

**Session density.** Sessions carry no duration, so working-sets-per-hour uses
the `avg_session_minutes` setting rather than measured elapsed time.

**Technical quality.** v1.0 §6.5 wants the ROM- or tempo-verified share of sets.
The schema has no such flag; `clean_reps` is the closest honest signal it does
carry, so a set counts as verified when `clean_reps` is present and accounts for
every rep. Sets where the user reported partials correctly fail to count.

**The milestone list.** v1.2 Patch 7 prices a milestone at 60 Blood and gives
two examples without enumerating the set. The list is built from numbers the
specs already treat as landmarks: the §6.1 DOMINION targets, the §6.2 FFMI
100-point mark, and pattern scores at each multiple of ten above 60.

### Four places the specs contradict their own arithmetic

These are resolved in favour of the arithmetic, and each has a test.

**The Blood threshold table.** `Blood_required(n) = 1200 × (n−1)^1.4` reproduces
the published tiers II–V exactly (1200, 3167, 5587, 8357) and then diverges from
the two illustrative rows at the bottom: it gives ~18,282 for VIII against a
printed 18,600, and ~34,437 for XII against a printed 38,900. The formula is the
generative rule both documents state, so it wins. If the printed numbers were
meant to be authoritative the exponent is nearer 1.44.

**The height correction.** v1.0 §2.2 says the leverage term moves references by
"at most about ±6%". With p = 0.50 on the presses the true extremes across
155–205 cm are +7.2% and −6.8%. The prose is a round number rather than a bound;
the magnitude is what matters and the test holds it at 7.5%.

**The BF_mod floor.** Erratum 2 floors the body-fat modifier at 0.50, but with
v1.0's coefficients the steepest branch only reaches 0.50 at 70.6% body fat, and
a reported value is already bounded to 60%. The clamp is therefore a guard
against a future retune, not a behaviour any user will meet. FRAME does still
reach zero, but through `S_ffmi` rather than the modifier.

**Rank on day one.** v1.2 Patch 3 promotes only after ten consecutive qualifying
days; v1.1 §21 says rank is current capability granted *immediately* from real
numbers. On day one these cannot both hold, and holding a lifter who walks in
benching 110 kg at COMMONER for ten days is precisely the insult §21 exists to
prevent. The first grant is exempt; the hold applies to every change after it.

### Why the Berserk boundary shows gates instead of a score

Erratum 1 deletes the RS condition from the Berserk requirement, because the six
attribute gates already guarantee RS ≥ 79.86 and the condition therefore carried
no information beyond a 0.14 sliver of artifact. The consequence that matters is
in the UI. Below the boundary the composite is the honest readout: "RS 61 / 62 →
APOSTLE". At the boundary it is actively misleading, because a lifter can sit
above the old threshold and still be one point of MASTERY from the rank. So the
top two ranks render the gate table, with the binding gate carrying a computed
instruction rather than encouragement.

The same erratum makes every gate hard. Compensation between attributes is fine
on ranks 1–13, where the RS band *is* the definition — it is not fine at Berserk,
because Berserk is specifically a claim about completeness. A 95 MIGHT does not
purchase a 65 VIGOR.

### Why the pattern floor is not the target

Erratum 6: a perfectly balanced lifter at exactly the 78 pattern floor fails the
MIGHT ≥ 85 gate by seven points, because MIGHT equals the pattern score when all
six are equal. The floor exists to permit *imbalance* — one pattern at 78 carried
by others above 85 — not to define the target, and the minimum balanced pattern
score for MIGHT 85 is 85. A user staring at a passing floor and a failing MIGHT
gate would otherwise assume a bug, so the readout says this explicitly, and the
suggestion is computed by solving the MIGHT formula for the weakest pattern.

That solve is numerical rather than closed-form: once the weakest pattern rises
past the second lowest, a different pair forms the `LOW` term, so the algebra has
a kink in it. MIGHT is monotonic in any single pattern score, so bisection is
exact to the displayed precision and cannot pick the wrong root.

## The muscle map, and where its data comes from

`internal/muscle` answers one question the sets table cannot: which muscles did
this exercise train. Two things needed it — VIGOR's `V_volume`, which v1.0 §6.3
defines as a per-muscle-group median, and the muscle map screen, which shades a
body diagram by how much work each group got.

### Provenance, and what was deliberately not taken

The exercise library is derived from `hasaneyldrm/exercises-dataset`, by way of
openGym, which had already slimmed and normalized it. That dataset is **not**
under openGym's licence: openGym's own `NOTICE.md` records it as Creative
Commons under the upstream terms, and it is used here on that basis. Only four
fields are kept — name, equipment, primary target, secondary muscles. No
instructional text, image or animation is reproduced.

**Nothing else from openGym is used.** Its application code is AGPL-3.0, and
copying any of it would relicense this repository. The body diagram in
`MuscleMap.kt` is our own Compose drawing, not openGym's `body-paths.js`.

### Name resolution is the hard part

The parser emits what a lifter says — "bench press", "rdl", "squat". The dataset
names things formally and equipment first — "barbell bench press", "barbell
romanian deadlift", "barbell full squat". Of the thirty-seven lifts the rank
system scores, **five** match by name. A bare join finds almost nothing.

So resolution runs four passes, most confident first: a hand-written alias
table, exact match, an equipment-prefixed retry ("bench press" + barbell →
"barbell bench press"), and finally token containment where every word of the
logged name must appear in the candidate. Ties break toward the shortest name,
which prefers the plain movement over an exotic variant.

The alias table is asserted by test, because an alias pointing at a name the
dataset does not contain fails *silently* — the lookup falls through to the
fuzzy pass and either finds something wrong or finds nothing. Thirteen of the
first draft's aliases were wrong exactly this way.

### The dataset is glutes-biased, and it had to be corrected

The upstream data labels **144** upper-leg exercises glutes-primary, against 44
for quads and 27 for hamstrings. That includes the back squat, the front squat,
the hack squat, the leg press and the Romanian deadlift. Taken literally, a
lifter squatting three times a week reads as barely training quads, which
distorts both the body diagram and the median behind `V_volume`.

`primaryOverride` corrects eleven entries. The corrections are deliberately
narrow — only compound squat and hinge patterns, and only where the dataset's
*own* secondary list already names the muscle being promoted. The demoted group
is not dropped: it falls back to a secondary and keeps half credit, so an
override moves emphasis rather than deleting work the lift genuinely does. The
conventional deadlift is left alone at glutes-primary, because it is a hip hinge
and there is no case for promoting something else.

### Why a median, and why only the major groups

A set counts in full for its primary target and at half for each secondary, so a
bench press registers as triceps work without reading as a triceps session.

`V_volume` then takes the **median** across groups, not the mean: one muscle
trained into the ground must not read as high overall work capacity, which is
precisely what the term exists to not reward. The test for this logs 45 sets of
curls in a week and gets a median of 0.0, against 12.0 for a balanced week.

The median runs over eight *major* groups, excluding calves, forearms and core.
Their volume norms are different enough that including them drags the median
down for everyone who trains sensibly, which would turn VIGOR into a measure of
whether someone does direct calf work.

### Unmapped exercises are surfaced, never swallowed

An exercise that resolves to nothing is reported by name, in the API payload and
on screen. Volume credited to no group is invisible in the figures, and the only
way a mapping gap gets noticed is if the app admits to it. For the same reason
an unknown lift is never forced into a group: volume attributed to the wrong
muscle is worse than volume left uncounted, because the second is visible as a
gap and the first is not.
