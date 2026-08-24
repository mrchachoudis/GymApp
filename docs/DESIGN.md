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
