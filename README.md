# Intuizi CLI

The official command-line interface for the [Intuizi](https://intuizi.com) data
signal platform. A single-binary client for the Intuizi API v2: manage
audiences, activations, cohorts, and POI data from your terminal, scripts, or
CI.

It talks to your Intuizi account, so you need one to use it. The source is
published so you can read what the tool does before you run it. It is not open
source; see [License](#license).

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

Or build from source, which needs the Go toolchain:

```bash
go install github.com/intuizi/intuizi-cli@latest   # or: make build
```

## Quickstart

```bash
intuizi auth login                  # stores a token in ~/.config/intuizi/
intuizi reference poi brands --search starbucks
intuizi audiences list
```

`auth login` asks for the email and password of your Intuizi account. In CI,
set `INTUIZI_API_TOKEN` to a token minted in the console under My Account then
API Tokens, and skip the login.

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
| `2` | Usage error - an unknown flag or flag value, an unknown command or subcommand, an invalid id argument, or flags that are missing or conflict. Caught before anything is sent |
| `130` | Interrupted with Ctrl-C (SIGINT) |
| `143` | Terminated (SIGTERM) |

The signal codes follow the shell's `128 + signal` convention, so a script can
tell a cancelled run from a failed one.

## Support, issues and contributions

Bug reports and feature requests are welcome as
[issues](https://github.com/intuizi/intuizi-cli/issues). Include the command
you ran, what you expected, and what happened. For anything account-specific,
contact Intuizi support rather than opening a public issue.

Pull requests from outside Intuizi are not accepted. The license does not grant
the right to create derivative works, and any contribution would assign its
rights to Intuizi, so an issue is the useful way to get something changed.

Security problems go to security@intuizi.com, not to the issue tracker. See
[SECURITY.md](SECURITY.md).

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

`.github/workflows/codeql.yml` runs CodeQL over the Go code on every pull
request and weekly. Every action in every workflow is pinned to a commit rather
than a tag, so a compromised or re-pointed tag cannot change what runs here;
Dependabot proposes the updates.

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
   attaches signed build provenance to every archive (verify a download with
   `gh attestation verify <file> --repo intuizi/intuizi-cli`), publishes a
   GitHub Release, and commits the Homebrew formula to
   [`intuizi/homebrew-intuizi-cli`](https://github.com/intuizi/homebrew-intuizi-cli).
   The push uses an SSH deploy key stored as the `HOMEBREW_TAP_DEPLOY_KEY`
   secret; it can write to that one repository and nothing else. A prerelease
   tag such as `v0.2.0-rc1` gets a GitHub Release but leaves the formula alone.
3. **npm**: `npm/build.mjs` turns the release binaries into `@intuizi/cli` plus
   six `@intuizi/cli-<os>-<cpu>` platform packages and publishes them, platform
   packages first. A version below 0.1.0 is built but not published, so the
   0.0.x tags that exercise this pipeline cannot put a package on the registry
   before the repositories are public. It needs an npm automation token in the `NPM_TOKEN` secret;
   without one it builds the packages and stops with a warning. A prerelease
   version is published under the `next` dist-tag, never `latest`. The job is
   idempotent, so re-running it after a partial publish finishes the set.

#### Before tagging

Run the live test against a test console. It is a manual gate: an unattended
job would need a credential in CI, and a test console carries whatever branch
is deployed to it, so a schedule would fail on other people's half-finished
work rather than on real API drift.

```bash
make build
./bin/intuizi --base-url https://TEST-CONSOLE auth login
scripts/live-test.sh https://TEST-CONSOLE
```

It exercises every command group end to end and cleans up after itself, writing
a pass/fail summary to `live-results/results.md` (gitignored — real API
responses). Activations stay at `--dry-run`, so `activations show`/`delete` are
not covered. A non-zero exit means do not tag. You need an account on the
environment you test against; each has its own database.

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

#### How npm publishing is authorised

Today, with a granular access token in the `NPM_TOKEN` secret, scoped to the
`@intuizi` packages and nothing else. Every publish also carries provenance, so
a tarball on the registry can be traced back to the commit and the run that
built it.

That token is meant to be temporary. All seven packages are already registered
to trust this repository's release workflow, which would remove the secret
entirely, but the exchange fails for this repository: GitHub mints OIDC tokens
with an immutable subject claim for repositories created after 15 July 2026,
and the npm registry cannot yet validate that format, so it answers "package
not found" ([npm/cli#9969](https://github.com/npm/cli/issues/9969)). The claim
cannot be disabled. When npm fixes it, drop `registry-url` and `NODE_AUTH_TOKEN`
from the release workflow, then revoke the token and delete the secret.

## License

Proprietary, copyright Intuizi Inc. The source is published so you can read what
the tool does, not as open source: you may install and run unmodified copies to access Intuizi
services through your own account, and nothing more. Using the CLI requires an
Intuizi account and is subject to the Intuizi Terms of Service. See
[LICENSE](LICENSE); the open-source libraries compiled into the binary keep
their own licenses, reproduced in [THIRD_PARTY_NOTICES](THIRD_PARTY_NOTICES).

## Related

- [DOCS.md](DOCS.md) - every flag, the catalog behind each value, and the
  response shapes a script has to parse.
- [examples/](examples) - ready-made payloads for the creates that take
  `--file`.
- The Intuizi API v2 reference is the source of truth for every endpoint this
  CLI wraps, and the Intuizi MCP server is the agent-facing surface on the same
  API. Both are reachable from the console.
