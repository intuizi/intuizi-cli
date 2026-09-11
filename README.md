# Intuizi CLI

The official command-line interface for the [Intuizi](https://intuizi.com) data
signal platform. A single-binary client for the Intuizi API v2: manage
audiences, activations, cohorts, and POI data from your terminal, scripts, or
CI.

> **Status: pre-release.** Under active development and not yet published. The
> release pipeline is in place and verified, but no public version exists; the
> first will be v0.1.0.

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

## Usage

Every command runs from flags. Names are resolved against the reference
catalogs, so "starbucks" works in place of an id looked up beforehand, and a
name matching nothing or several things is an error listing what it found.
`--brand-all` takes every match for a search instead, for the deliberate "all
the coffee brands" case.

```bash
# Authenticate once; stores a bearer token in ~/.config/intuizi/
intuizi auth login

# Build an audience and block until it has built
intuizi audiences create \
  --type poi --brand starbucks \
  --country USA --state CA --city "San Francisco" \
  --start-date 2026-09-02 --end-date 2026-09-09 \
  --name "Starbucks visitors - SF - 1 week" --wait

# Export it, following the delivery to Completed
intuizi activations create --audience-id 88 \
  --endpoint-connection-id 12 --pricing-model-id 3 --wait

# Import your own identifiers as a cohort from a cloud file
intuizi cohorts create --name "Loyalty members" \
  --file-uri s3://example-bucket/exports/loyalty.csv.gz --file-format gzip \
  --identifier-type hem_sha256 --identifier-column email_sha256

# Or upload the file first and build from the reference it returns
ref=$(intuizi uploads put customers.csv --purpose cohort)
intuizi cohorts create --name "Customers" --upload-reference "$ref" \
  --file-format csv --identifier-type hem_sha256 --identifier-column email

# Or turn a completed audience into a cohort
intuizi cohorts create --audience-id 88 --device-limit 1000

# Rebuild an audience every week
intuizi schedules create --name "Weekly refresh" --audience-id 88 \
  --start "2026-09-15 06:00:00" --timezone America/New_York \
  --frequency weekly --window 2
```

`--dry-run` prints the request body those flags produce and sends nothing, so
you can check a payload before it costs anything, or redirect it to a file as a
starting point.

```bash
intuizi audiences create --type poi --brand starbucks ... --dry-run > audience.json
```

`--file payload.json` (or `-` for stdin) stays for the genuinely nested cases:
two datasets combined with an operator, refine and crosspurchase blocks, a
schedule's auto-export, and cohort caps by visit frequency or distance.
Ready-made payloads live in [`examples/`](examples).

```bash
# Everything supports --json for scripting, and --quiet for ids alone
intuizi reference poi brands --search starbucks --quiet       # 208
id=$(intuizi audiences create --type poi ... --wait --quiet)
intuizi audiences show "$id" --json | jq '.data[0].normalized_payload'
```

Full flag reference, including the lookup command behind every value, is in
[DOCS.md](DOCS.md).

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
| `completion` | shell completion for bash, zsh, fish, powershell |

In CI, set `INTUIZI_API_TOKEN` instead of running `auth login`.

## Design

- **Pure API v2 client.** Every command maps to a documented endpoint of the
  Intuizi API v2 (`https://console.intuizi.com/api/v2`). No server-side
  logic lives here.
- **Full v2 parity.** Auth, audiences (including refine and crosspurchase),
  activations, cohorts, POI, and reference reads.
- **Human first, script friendly.** Readable tables by default, `--json` for
  raw responses, `--quiet` for ids alone, and exit codes scripts can branch on.
- **Wrong input fails before it is sent.** Names are resolved against the
  catalogs, dataset types and date ranges are validated locally, and payloads
  are built from typed fields rather than free-form JSON.

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

Requires Go 1.25+, matching the `go` directive in `go.mod`. Lint with
golangci-lint 2.13.2; CI pins that exact patch, so another version will report
findings CI does not, or miss ones it catches.

```bash
make build   # build ./bin/intuizi
make test    # go test ./...
make lint    # golangci-lint
```

### Tests

Tests never contact the real API. Each one starts a `net/http/httptest` server,
hands it a canned response envelope and points the client's base URL at it, so
the whole suite runs offline and needs no token.

Realistic multi-field envelopes live as files in `cmd/testdata/responses/`; see
the README there for what is captured and how to refresh it. Short bodies, and
any body whose exact values a test asserts on, stay inline in the test that
uses them.

### CI

`.github/workflows/ci.yml` runs `go vet`, golangci-lint and `go test -race` on
every push to master and every pull request, across ubuntu-latest and
macos-latest. Lint runs on Ubuntu only, since results do not vary by OS.

The `ci` job aggregates the matrix and is the required status check for merging
to master. Require that one, not an individual matrix leg, because a leg's name
changes whenever the matrix does.

### Releases

`.github/workflows/release.yml` fires on a `v*` tag. It runs the test suite,
then goreleaser cross-compiles six binaries (darwin, linux and windows, each
amd64 and arm64), packages them with a checksums file, and publishes a GitHub
Release. Configuration is in `.goreleaser.yaml`.

Test the packaging locally without publishing anything:

```bash
goreleaser release --snapshot --clean
```

## Related

- Intuizi API v2 reference - the source of truth for every endpoint this CLI
  wraps (see the developer docs published from the `intuizi-console` repo).
- Intuizi MCP server - the AI-agent surface built on the same API.
