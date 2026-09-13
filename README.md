# Intuizi CLI

The official command-line interface for the [Intuizi](https://intuizi.com) data
signal platform. A single-binary client for the Intuizi API v2: manage
audiences, activations, cohorts, and POI data from your terminal, scripts, or
CI.

> **Status: pre-release.** Under active development. The release pipeline
> (GitHub Releases, Homebrew, npm) is in place; the first public version will
> be v0.1.0, when this repository and the Homebrew tap go public.

## Why a CLI

Intuizi already has three surfaces: the web Console for browser users, the
REST API v2 for integrations, and an MCP server for AI agents. The CLI
completes the set for humans working in terminals and for automation in
scripts and CI pipelines.

## Installation

```bash
# Homebrew (macOS / Linux)
brew install intuizi/intuizi-cli/intuizi

# npm (macOS / Linux / Windows, needs Node 18+)
npm install -g @intuizi/cli

# Or download an archive from GitHub Releases and put `intuizi` on your PATH
```

No runtime dependencies: the CLI ships as a single static Go binary for
macOS, Linux and Windows, amd64 and arm64. The npm package is a small launcher
that pulls in the binary for your platform as an optional dependency; nothing
is downloaded at install time beyond the packages themselves.

Until v0.1.0 the repositories are private, so Homebrew and npm cannot fetch
anything yet. Build from source in the meantime:

```bash
go install github.com/intuizi/intuizi-cli@latest   # or: make build
```

## Usage

Every command runs from flags. Names are resolved against the reference
catalogs, so you pass "starbucks" rather than an id you had to look up first,
and a name matching nothing or several things is an error that lists what it
found.

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
| `2` | Usage error - an unknown flag or flag value, an unknown command or subcommand, an invalid id argument, or flags that are missing or conflict. Caught before anything is sent |
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

`scripts/live-test.sh <base-url>` is the exception: it runs the CLI against a
real test console with the token you are logged in with, exercising every
command group end to end, creating and then deleting a project, audiences,
cohorts, an upload, a schedule and POI submissions, and keeping activations to
`--dry-run`. It writes `results.md` with every command's exit code and output.
Point it only at a test environment.

### CI

`.github/workflows/ci.yml` runs `go vet`, golangci-lint and `go test -race` on
every push to master and every pull request, across ubuntu-latest and
macos-latest. Lint runs on Ubuntu only, since results do not vary by OS.

A separate `package` job runs a goreleaser snapshot on every pull request,
checks that the rendered Homebrew formula parses, builds the npm packages from
the snapshot, installs the launcher from the tarballs and runs it. Packaging
breaks on the PR that causes them, not on the release tag.

The `ci` job aggregates the others and is the required status check for merging
to master. Require that one, not an individual matrix leg, because a leg's name
changes whenever the matrix does.

### Releases

`.github/workflows/release.yml` fires on a `v*` tag and runs three jobs in
order:

1. **test**: `go vet` and `go test -race`. A tag that fails here publishes
   nothing.
2. **release**: goreleaser cross-compiles six binaries (darwin, linux and
   windows, each amd64 and arm64), packages them with a checksums file,
   publishes a GitHub Release, and commits the Homebrew formula to
   [`intuizi/homebrew-intuizi-cli`](https://github.com/intuizi/homebrew-intuizi-cli).
   The push uses an SSH deploy key stored as the `HOMEBREW_TAP_DEPLOY_KEY`
   secret; it can write to that one repository and nothing else. A prerelease
   tag such as `v0.2.0-rc1` gets a GitHub Release but leaves the formula alone.
3. **npm**: `npm/build.mjs` turns the release binaries into `@intuizi/cli` plus
   six `@intuizi/cli-<os>-<cpu>` platform packages and publishes them, platform
   packages first. It needs an npm automation token in the `NPM_TOKEN` secret;
   without one it builds the packages and stops with a warning. A prerelease
   version is published under the `next` dist-tag, never `latest`. The job is
   idempotent, so re-running it after a partial publish finishes the set.

Configuration is in `.goreleaser.yaml` and `npm/`. To cut a release:

```bash
git tag -a v0.1.0 -m "v0.1.0" && git push origin v0.1.0
```

Then run the manual `brew-smoke` workflow, which installs the formula on clean
macOS and Linux runners and checks the reported version.

Test the packaging locally without publishing anything:

```bash
goreleaser release --snapshot --clean      # binaries, archives, dist/homebrew/Formula/intuizi.rb
node npm/build.mjs --dist dist --out dist/npm
```

`goreleaser check` reports the `brews` stanza as deprecated: goreleaser now
prefers Homebrew casks, which are macOS-only and need the binary signed and
notarised or a quarantine workaround. The formula works on Linux too and needs
neither, so it stays until Apple signing is set up. It keeps working across
goreleaser v2, which the workflow pins.

#### Before the first public release

- Make this repository and `intuizi/homebrew-intuizi-cli` public. Homebrew
  downloads release archives anonymously, so a private repository breaks
  `brew install` for everyone.
- Create the `intuizi` organisation on npmjs.com, mint a granular automation
  token with publish rights and store it as the `NPM_TOKEN` repository secret.
- Have counsel read `LICENSE` once; it is proprietary and was drafted in-house.

## License

Proprietary. The source is published so you can read what the tool does, not
as open source: you may install and run unmodified copies to access Intuizi
services through your own account, and nothing more. Using the CLI requires an
Intuizi account and is subject to the Intuizi Terms of Service. See
[LICENSE](LICENSE); the open-source libraries compiled into the binary keep
their own licenses, reproduced in [THIRD_PARTY_NOTICES](THIRD_PARTY_NOTICES).

## Related

- Intuizi API v2 reference - the source of truth for every endpoint this CLI
  wraps (see the developer docs published from the `intuizi-console` repo).
- Intuizi MCP server - the AI-agent surface built on the same API.
