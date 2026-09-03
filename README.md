# Intuizi CLI

The official command-line interface for the [Intuizi](https://intuizi.com) data
signal platform. A single-binary client for the Intuizi API v2: manage
audiences, activations, cohorts, and POI data from your terminal, scripts, or
CI.

> **Status: pre-alpha.** This repository was just created and the CLI is under
> active development. Nothing is released yet - the interface below describes
> the planned v0.1.0 surface and may change.

## Why a CLI

Intuizi already has three surfaces: the web Console for browser users, the
REST API v2 for integrations, and an MCP server for AI agents. The CLI
completes the set for humans working in terminals and for automation in
scripts and CI pipelines.

## Planned installation

```bash
# Homebrew (macOS / Linux)
brew install intuizi/tap/intuizi

# npm (any platform with Node)
npm install -g @intuizi/cli

# Or download a binary from GitHub Releases
```

No runtime dependencies - the CLI ships as a single static Go binary for
macOS, Linux, and Windows (amd64 and arm64).

## Planned usage

```bash
# Authenticate once; stores a bearer token in ~/.config/intuizi/
intuizi auth login

# Look up reference data for building audiences
intuizi reference common dataset-types
intuizi reference common states --countries USA
intuizi reference apps categories --search fitness

# Create an audience from a payload file and block until it has built
intuizi audiences create --file examples/audience-poi.json --wait

# Export it to a destination and follow it to Completed (--timeout defaults to 60m)
intuizi activations create --audience-id 88 \
  --endpoint-connection-id 12 --pricing-model-id 3 --wait

# Import your own identifiers as a cohort from a cloud file
intuizi cohorts create --name "Loyalty members" \
  --file-uri s3://my-bucket/exports/loyalty.csv.gz --file-format gzip \
  --identifier-type hem_sha256 --identifier-column email_sha256

# Or upload the file first and reference it from the payload
ref=$(intuizi uploads put customers.csv --purpose cohort)
jq --arg r "$ref" '.upload_reference = $r | del(.file_uri)' examples/cohort.json \
  | intuizi cohorts create --file -

# Or turn a completed audience into a cohort
intuizi cohorts create --file examples/cohort-from-audience.json

# Everything supports --json for scripting, and --quiet for ids alone
intuizi audiences list --json | jq '.data.items[0].id'
id=$(intuizi audiences create --file examples/audience-poi.json --wait --quiet)
```

Ready-made payloads for every `--file` command live in [`examples/`](examples).

## Commands

| Command | What it covers |
| --- | --- |
| `auth` | `login`, `status`, `logout` |
| `audiences` | `list`, `show`, `create`, `delete`, `lookalike create\|cancel` |
| `activations` | `list`, `show`, `create`, `delete` |
| `cohorts` | `list`, `show`, `create`, `preview`, `delete` |
| `schedules` | `list`, `show`, `create`, `activate`, `deactivate`, `delete` |
| `projects` | `list`, `show`, `create`, `delete` |
| `poi` | `segments`, `categories`, `brands`, `locations`, `submissions` |
| `uploads` | `reserve`, `put` |
| `reference` | 60 read-only catalogs across 10 groups |
| `usage` | monthly data-scan usage |
| `webhooks` | `list` |

In CI, set `INTUIZI_API_TOKEN` instead of running `auth login`.

## Design

- **Pure API v2 client.** Every command maps to a documented endpoint of the
  Intuizi API v2 (`https://console.intuizi.com/api/v2`). No server-side
  logic lives here.
- **Full v2 parity.** Auth, audiences (including refine and crosspurchase),
  activations, cohorts, POI, and reference reads.
- **Human first, script friendly.** Readable tables by default, `--json` for
  raw responses, `--quiet` for ids alone, and exit codes scripts can branch on.

## Exit codes

| Code | Meaning |
| --- | --- |
| `0` | Success |
| `1` | API error, or a `--wait` that ended in a failed or timed-out state |
| `2` | Usage error - an unknown flag, or an invocation the CLI rejects before sending anything (missing or conflicting flags) |
| `130` | Interrupted with Ctrl-C (SIGINT) |
| `143` | Terminated (SIGTERM) |

The signal codes follow the shell's `128 + signal` convention, so a script can
tell a cancelled run from a failed one.

## Development

Requires Go 1.25+.

```bash
make build   # build ./bin/intuizi
make test    # go test ./...
make lint    # golangci-lint
```

## Related

- Intuizi API v2 reference - the source of truth for every endpoint this CLI
  wraps (see the developer docs published from the `intuizi-console` repo).
- Intuizi MCP server - the AI-agent surface built on the same API.
