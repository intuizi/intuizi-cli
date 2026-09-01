# Intuizi CLI documentation

A single-binary Go client for the Intuizi API v2. Manage audiences,
activations, cohorts, schedules, and your own POI data from a terminal,
script, or CI pipeline.

## Install

```bash
go build -o ~/go/bin/intuizi .
```

Homebrew, npm, and GitHub Releases are planned for v0.1.0.

## Authenticate

```bash
intuizi auth login     # exchanges credentials for an API token, stored in ~/.config/intuizi/
intuizi auth status    # shows who you are logged in as
intuizi auth logout    # forgets the stored token
```

The token can also be supplied per-call with the INTUIZI_API_TOKEN
environment variable, which wins over the stored one.

## Global flags

| Flag | Meaning |
| --- | --- |
| `--json` | Print the raw response envelope instead of a table |
| `--base-url` | Console base URL (default: the stored value, then production) |
| `--idempotency-key` | Reuse one Idempotency-Key to retry a create whose outcome is unknown |

## Commands

| Command | What it does |
| --- | --- |
| `audiences list \| show \| create \| delete` | Manage audiences; `lookalike create \| cancel` for Lookalike Models |
| `activations list \| show \| create \| delete` | Deliver a completed audience to an endpoint connection |
| `cohorts list \| show \| create \| preview \| delete` | Build cohorts from a cloud file, an upload, or an audience |
| `poi locations \| brands \| categories \| segments \| submissions` | Your company's own POI data (see below) |
| `reference <dataset> <catalog>` | Read-only catalogs: apps, poi, web, ctv, demographics, transactions, … |
| `projects list \| create` | Group analyses under a project |
| `schedules list \| show \| create \| activate \| deactivate \| delete` | Recurring runs |
| `uploads reserve \| put` | Reserve a presigned slot; `put` uploads and prints the `upload_reference` |
| `webhooks list` | Inspect webhook endpoints registered in the Console |
| `usage` | This month's data-scan usage and limits |

Creates take either flags or `--file payload.json` (`-` for stdin). Deletes
prompt for confirmation unless `--yes`.

## The async model

Creates return immediately; the resource completes in the background.
`audiences`/`activations` `create` and `show` take `--wait [--timeout 60m]`,
which polls every 15 s, prints status changes to stderr and the final record
to stdout, and exits non-zero on failure. Everything else is followed with
`show <id>`, or by a webhook registered in the Console.

## Typical workflows

```bash
# Audience -> activation
intuizi audiences create --file audience.json --wait
intuizi activations create --audience-id 88 --endpoint-connection-id 4 \
  --pricing-model-id 3 --wait

# Cohort from a cloud file
intuizi cohorts create --name "Q3 customers" --file-uri s3://example-bucket/q3.csv \
  --file-format csv --identifier-type hem_sha256 --identifier-column email_sha256

# Your own POIs: find the brand id, submit a CSV, follow it
intuizi poi brands list --search example
intuizi poi submissions create --name my-locations --brand-id 123 --file locations.csv
intuizi poi submissions show <id>
```

POI submission CSVs need `latitude`, `longitude`, and a country column headed
`country|alpha_2` or `country|alpha_3`. The taxonomy nests segments >
categories > brands; create the parent first.

## Development

```bash
go build ./... && go vet ./... && go test ./...
```

Command logic lives in `cmd/`, the HTTP client and envelope decoding in
`internal/api/`, token/config storage in `internal/config/`, and table/JSON
rendering in `internal/output/`. Examples of create payloads are in
`examples/`.
