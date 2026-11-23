# Custom API Integrations Setup Guide

Track Custom API Integrations usage in onWatch with local JSONL ingestion.

Ingest local JSONL files to monitor Custom API Integrations usage in onWatch. This is not for subscription or quota tracking, but for logging token usage in custom scripts and programs that make API calls. Wrap your integrations with telemetry to record per-call token usage, cost, and latency data, and track everything in onWatch.

## Prerequisites

- onWatch with the Custom API Integrations backend enabled
- A script or automation that already calls a supported provider API
- Ability to write a JSONL file locally

Any lowercase provider slug is accepted (`a-z`, then `a-z0-9._-`), for example:

- `anthropic`
- `openai`
- `zai`
- `deepseek`
- `openrouter`
- `gemini`

Use the same slug as the account's provider in **Accounts & Costs** so events attach to that account.

## How It Works

1. Your script calls the provider API.
2. Your script reads the usage fields from the API response.
3. Your script appends one normalised JSON object per line to a file in `~/.onwatch/api-integrations/`.
4. onWatch tails `*.jsonl` files in that directory and stores the events in SQLite.

The source files are just the ingest input. The canonical persisted data lives in `~/.onwatch/data/onwatch.db`.

## Default Paths

- API Integrations directory: `~/.onwatch/api-integrations`
- Database: `~/.onwatch/data/onwatch.db`
- Log file: `~/.onwatch/data/.onwatch.log`

In containers, the default API Integrations directory is `/data/api-integrations`.

## Configuration

Custom API Integrations ingestion is enabled by default.

Optional environment variables:

```env
ONWATCH_API_INTEGRATIONS_ENABLED=true
ONWATCH_API_INTEGRATIONS_DIR=~/.onwatch/api-integrations
ONWATCH_API_INTEGRATIONS_RETENTION=1440h
```

If you change `ONWATCH_API_INTEGRATIONS_DIR`, point your scripts and onWatch at the same directory.

Retention notes:

- `ONWATCH_API_INTEGRATIONS_RETENTION` controls how long ingested API Integrations events are kept in SQLite
- default retention is `1440h` which is 60 days
- set `ONWATCH_API_INTEGRATIONS_RETENTION=0` to disable database pruning
- pruning applies only to the SQLite table, not to the source `.jsonl` files

## Event Format

Write one JSON object per line.

Required fields:

```json
{
  "ts": "2026-04-03T12:00:00Z",
  "integration": "notes-organiser",
  "provider": "anthropic",
  "model": "claude-3-7-sonnet",
  "prompt_tokens": 1200,
  "completion_tokens": 300
}
```

Optional fields:

- `total_tokens`
- `cache_read_tokens` - cached prompt tokens, a subset of `prompt_tokens`
- `cache_write_tokens` - prompt tokens written to the cache, a disjoint subset of `prompt_tokens`
- `cost_usd`
- `latency_ms`
- `account` - account name (used when `account_id` is omitted)
- `account_id` - stable account ID from **Accounts & Costs**; survives renames
- `omp_provider` - omp provider name that made the call; binds the event to the account onWatch discovered from that omp entry (written by the [omp extension](#omp))
- `request_id`
- `metadata`

Full example:

```json
{
  "ts": "2026-04-03T12:00:00Z",
  "integration": "notes-organiser",
  "provider": "anthropic",
  "account": "personal",
  "model": "claude-3-7-sonnet",
  "request_id": "req_123",
  "prompt_tokens": 1200,
  "completion_tokens": 300,
  "total_tokens": 1500,
  "cost_usd": 0.0123,
  "latency_ms": 1840,
  "metadata": {
    "task": "weekly-meeting-notes"
  }
}
```

Notes:

- `ts` must be RFC3339 in UTC, for example `2026-04-03T12:00:00Z`
- `provider` must be a lowercase slug: a letter, then letters, digits, `.`, `_` or `-`
- `metadata` must be a JSON object if present
- If `account` is omitted, onWatch stores it as `default`
- If `total_tokens` is omitted, onWatch computes `prompt_tokens + completion_tokens`
- If `account_id` is set it must belong to an active account with the same `provider`; otherwise the event is bound by exact `provider` + `account` name, and pre-existing events are bound when that account is created or renamed
- `cache_read_tokens` + `cache_write_tokens` must not exceed `prompt_tokens`

## Cost Without `cost_usd`

When an event has no `cost_usd`, onWatch estimates it with its own built-in pricing snapshot. No omp installation, statistics database, or network access is involved. Estimates:

- match the exact `provider` + `model` only (no prefix guessing); an unknown model stays **unknown**, never `$0`
- charge cache reads/writes at the model's cache rates, and never charge cached tokens twice
- exclude tools, storage, taxes, batch/priority tiers, and negotiated discounts

Add or override rates with `ONWATCH_PRICING_FILE`, a JSON array (max 1 MiB) of exact rates in USD per million tokens:

```json
[
  {"provider": "zai", "model": "glm-4.6", "input_per_million": 0.6, "output_per_million": 2.2, "cache_read_per_million": 0.11}
]
```

Omitted cache rates charge the full input rate. An invalid file stops startup rather than silently pricing calls wrongly.

## Python Examples

Python-first examples are included here:

- `examples/api_integrations/python/onwatch_api_integrations.py`
- `examples/api_integrations/python/anthropic_example.py`
- `examples/api_integrations/python/openai_example.py`
- `examples/api_integrations/python/mistral_example.py`
- `examples/api_integrations/python/openrouter_example.py`
- `examples/api_integrations/python/gemini_example.py`

The helper in `examples/api_integrations/python/onwatch_api_integrations.py` appends normalised JSONL events to the API Integrations directory.

These are initial examples to show the general use-case, but the logic can be expanded to any API-driven custom integration you want.

Included example utilities currently include:

- `examples/api_integrations/python/generate_practice_dataset.py`

You can also build your own wrapper around the helper and write any number of integration-specific JSONL files, as long as they end in `.jsonl`.

## omp

[`examples/api_integrations/omp/onwatch.ts`](../examples/api_integrations/omp/onwatch.ts) is an omp extension that reports every model call omp makes: main-agent replies, subagent replies, and side calls such as titles and compaction. Install it with:

```bash
scripts/install-omp-extension.sh            # copy into ~/.omp/agent/extensions
scripts/install-omp-extension.sh --link     # or symlink it, so `git pull` keeps it current
```

Without a checkout, `curl -fsSL https://raw.githubusercontent.com/TTN-ATTN/onWatch/main/scripts/install-omp-extension.sh | bash` downloads and installs it. `--profile NAME` targets an `omp --profile` agent directory, and `--uninstall` removes it. The script never overwrites an unrelated `extensions/onwatch.ts` unless you pass `--force`. Restart running omp sessions to load it.

New omp sessions then append one event per call to `omp-YYYY-MM.jsonl` in the API Integrations directory. Each event carries `omp_provider`, so onWatch attaches it to the matching `omp / <provider>` account on **Accounts & Costs** and prices it with its own rates (omp's cost figures are not used).

- Only the provider, model, token counts, and timing are written: no prompts, replies, or keys.
- Calls that failed and used no tokens are skipped, and replays are dropped by `request_id`.
- An omp provider holding several logins (for example two Anthropic subscriptions) cannot tell onWatch which login made a call, so those calls stay unattached and appear only on the API Integrations tab.
- Models onWatch has no price for show as **Unknown**; add them with `ONWATCH_PRICING_FILE`.
- Set `ONWATCH_OMP_USAGE=false` in omp's environment to pause reporting without removing the extension.

## Dashboard and API

Once events are being ingested, open the `API Integrations` tab in the dashboard.

The dashboard shows:

- per-integration cards with request counts, token totals, providers, and optional cost
- all-time and recent usage insight panels
- a shared usage chart with metric modes for tokens per call, API calls, accumulated tokens, and cost
- ingest health, tailed files, and recent alerts

Per-account cost analysis lives at **Accounts & Costs** (`/accounts`): daily and per-model cost, token, and cache breakdowns for one account. When the provider exposes a billing API, its reported spend is shown with the provider's own scope (organization or API key) and period. Observed-call tables are kept separate because they may not reconcile with an organization-wide bill.

API Integrations can also be queried through the read-only backend API:

- `GET /api/api-integrations/current`
- `GET /api/api-integrations/history?range=6h`
- `GET /api/api-integrations/health`

Dashboard visibility is controlled through the normal settings API via `api_integrations_visibility`, but ingestion itself is controlled by `ONWATCH_API_INTEGRATIONS_ENABLED`.

## Start onWatch

Foreground mode is easiest for first-time verification:

```bash
onwatch --debug
```

You should see a log line showing that the API integrations ingester started.

## Verify File Output

Check that your script is writing JSONL events:

```bash
ls -la ~/.onwatch/api-integrations
tail -n 20 ~/.onwatch/api-integrations/*.jsonl
```

Each API call should append one valid JSON line.

## Verify Database Ingestion

Check recently ingested API integrations usage events:

```bash
sqlite3 ~/.onwatch/data/onwatch.db "select integration_name, provider, account_name, model, prompt_tokens, completion_tokens, total_tokens, captured_at from api_integration_usage_events order by id desc limit 20;"
```

Check ingest cursor state:

```bash
sqlite3 ~/.onwatch/data/onwatch.db "select source_path, offset_bytes, file_size, partial_line from api_integration_ingest_state;"
```

Expected result:

- `api_integration_usage_events` contains one row per ingested event
- `api_integration_ingest_state` contains one row per tailed file
- `offset_bytes` increases as the file grows

## Troubleshooting

### No rows appear in `api_integration_usage_events`

Check:

- onWatch is running
- `ONWATCH_API_INTEGRATIONS_ENABLED` is not set to `false`
- your script writes into the same directory that onWatch is tailing
- the file name ends with `.jsonl`
- each line is valid JSON

Run:

```bash
tail -f ~/.onwatch/data/.onwatch.log
```

### Invalid lines are skipped

onWatch skips malformed or schema-invalid lines and creates a system alert instead of stopping ingestion.

Check recent alerts:

```bash
sqlite3 ~/.onwatch/data/onwatch.db "select provider, alert_type, title, message, created_at from system_alerts where provider = 'api_integrations' order by id desc limit 20;"
```

### Duplicate rows

onWatch deduplicates ingested events using a derived fingerprint based on the source path and stable event fields.

This protects against:

- daemon restart
- file reread after truncation
- repeated scans of the same already-ingested lines

If you intentionally want two events, they must differ in at least one meaningful field such as timestamp or request id.

### Rotating source files

If you want to start a fresh source log for new events, move or rename the active `.jsonl` file and let your wrapper create a new one.

Notes:

- onWatch will treat the new file as a new ingest source
- previously ingested history remains in SQLite until you clear or replace the stored database
- rotating the source file changes future ingestion, but it does not erase existing chart history by itself

## Backend Storage

Custom API Integrations data is stored in separate SQLite tables from the existing subscription/quota tracking tables:

- `api_integration_usage_events`
- `api_integration_ingest_state`

This means Custom API Integrations telemetry is identifiable and queryable independently from provider quota snapshots and reset cycles.

Database retention behavior:

- onWatch automatically prunes old rows from `api_integration_usage_events`
- the pruning cutoff is controlled by `ONWATCH_API_INTEGRATIONS_RETENTION`
- the default is 60 days
- source `.jsonl` files are not pruned or compacted by onWatch
- if you want smaller source logs, rotate or remove the JSONL files manually
