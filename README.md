# Gym Logger

Talk at it between sets. It logs the session, does the maths itself, and says
one useful thing about it.

Two model calls per log: a cheap parser at temperature 0 that turns freeform
text into JSON, and a better coach at 0.8 that reacts to the result. Every
number the coach states was computed in Go against real rows first — the model
never calculates a PR, never compares to last week, and never decides whether
something crossed a threshold.

## Shape

```
phone (Kotlin/Compose)
  |  HTTPS + bearer token
  v
Cloudflare Tunnel  ->  gymd on the mini PC  ->  SQLite
                            |
                            +-> OpenCode (parser + coach)
                            +-> FCM (reminders)
```

The mini PC holds the API keys. The APK holds nothing but a server URL and a
shared token, because anything shipped inside an APK can be pulled back out of
it in about thirty seconds.

## Pipeline

```
raw text
  -> parse        (LLM, temp 0, JSON mode, one retry with the decode error appended)
  -> validate     (bounds, field-swap detection, enum repair)
  -> park or persist
  -> build context (PRs, flags, stale lifts, suggestion — all SQL)
  -> rank
  -> coach        (LLM, temp 0.8, emoji stripped, banned metaphors regenerated once)
```

Two things stop a write: values that cannot be real (a 900 kg bench), and
references the parser could not resolve ("same as last time" — it has no
history). Both go to `pending_parses` for confirmation. A bad row is
permanent: it becomes a ceiling nothing beats, and the coach goes quiet about
that lift forever.

## Packages

| Package | Job |
|---|---|
| `internal/prompt` | The two system prompts, embedded from `.txt` |
| `internal/model` | Wire types, unit normalization, `LiftKey`, Epley |
| `internal/store` | SQLite schema and persistence |
| `internal/validate` | The gate between the model and the database |
| `internal/contextbuilder` | PR maths, flags, staleness, progression suggestion |
| `internal/berserk` | Berserk Rank System v1.3: six attributes, six movement patterns, the fourteen-rank ladder, and the Blood economy |
| `internal/scheduler` | Split rotation, recovery, quiet hours, notifications |
| `internal/llm` | OpenCode client, offline stub, output sanitizers |
| `internal/push` | FCM v1 sender |
| `internal/api` | HTTP surface |
| `internal/app` | Wires the pipeline together |

## Running it

No API key required — the service falls back to a deterministic offline parser
and coach, so the whole thing is usable before any account exists.

```bash
go build -o gymd ./cmd/gymd

# one-shot from the terminal
./gymd -db gym.db -log "bench press 100 x 5, 5, 4; dips bw x 12, 10"

# what should I train
./gymd -db gym.db -next

# current rank
./gymd -db gym.db -rank

# the service
GYM_AUTH_TOKEN=$(openssl rand -hex 32) ./gymd -db gym.db -addr 127.0.0.1:8080
```

Environment:

| Variable | Meaning |
|---|---|
| `GYM_AUTH_TOKEN` | Required. Shared secret the phone sends. |
| `OPENCODE_API_KEY` | Unset means the offline stub. |
| `OPENCODE_ENDPOINT` | Override the default endpoint. |
| `FCM_CREDENTIALS` | Path to the Firebase service-account JSON. Unset means notifications are logged, not sent. |
| `GYM_DB`, `GYM_ADDR`, `GYM_TZ` | Same as the flags. |

## Settings

Stored in the `settings` table, changeable at runtime via `POST /v1/settings`:

| Key | Default | Meaning |
|---|---|---|
| `bodyweight_kg` | 80 | Scales bodyweight-lift volume and the strength standards |
| `no_rack` | false | Set true to flag heavy compounds done with no rack or spotter |
| `split_json` | four-day rotation | The session rotation |
| `shift_days` | friday,saturday,sunday | Night-shift days |
| `shift_start_hour` / `shift_end_hour` | 23 / 7 | Shift window |
| `post_shift_sleep_hours` | 8 | How long after a shift he is asleep |
| `quiet_start_hour` / `quiet_end_hour` | 22 / 9 | Ordinary quiet hours |
| `reminder_hour` | 17 | Earliest a session reminder fires |

## Docs

- [`docs/DEPLOY.md`](docs/DEPLOY.md) — systemd, Cloudflare Tunnel, cross-compiling
- [`docs/FIREBASE.md`](docs/FIREBASE.md) — Firebase project and the Android app
- [`docs/DESIGN.md`](docs/DESIGN.md) — why the maths lives in SQL and not the prompt

## Tests

```bash
go test ./...
```

The suite covers the parts that fail quietly rather than loudly: PR detection
across `load_basis` boundaries, warmups excluded from volume, rep PRs not being
awarded to deloads, staleness needing elapsed days as well as sessions, and the
coach sanitizers.
