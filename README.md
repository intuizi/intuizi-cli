# Intuizi CLI

[![Release](https://img.shields.io/github/v/release/intuizi/intuizi-cli)](https://github.com/intuizi/intuizi-cli/releases/latest)
[![npm](https://img.shields.io/npm/v/@intuizi/cli)](https://www.npmjs.com/package/@intuizi/cli)
[![CI](https://github.com/intuizi/intuizi-cli/actions/workflows/ci.yml/badge.svg)](https://github.com/intuizi/intuizi-cli/actions/workflows/ci.yml)

**Build, size, and deliver audiences from your terminal.**

The Intuizi CLI is one small program, `intuizi`, for the Large Behavioral
Model, [Intuizi](https://intuizi.com)'s flagship large quantitative model
(LQM), trained on de-identified, real-world behavioral signals. What you do in
the Intuizi console or with API calls, you can do with a command: type it
yourself, or put it in a script or a CI job.

**Documentation:** [console.intuizi.com/developers/cli](https://console.intuizi.com/developers/cli)
(sign in with your Intuizi account), and [DOCS.md](DOCS.md) in this
repository.

You need an Intuizi account to use it. The source is published so you can
read what the tool does before you run it. It is not open source; see
[License](#license).

## What you can do

- **Build audiences** from real-world behavior: places visited, apps, web
  and CTV activity, purchases, and more, using brand and category names
  instead of ids.
- **Size before you build:** estimate how many devices an audience would
  hold, without creating it.
- **Deliver** an audience to your destinations: all of it, or only the
  devices seen on several days.
- **Bring your own data:** import your customer lists as cohorts, and submit
  your own locations.
- **Keep it fresh:** rebuild an audience every day, week, or month on a
  schedule.
- **Script it:** JSON output, ids alone, and clear exit codes, for scripts
  and CI pipelines.

The CLI, the [REST API](https://console.intuizi.com/developers/api/v2) and the
[MCP server](https://console.intuizi.com/developers/mcp) reach the same
platform with the same rules and limits: the CLI is for people at a terminal
and for scripts and CI, the API for your own code, and the MCP server for AI
agents.

## Installation

```bash
# Homebrew (macOS / Linux)
brew install intuizi/intuizi-cli/intuizi

# npm (macOS / Linux / Windows, needs Node 18+)
npm install -g @intuizi/cli

# Or download an archive from GitHub Releases and put `intuizi` on your PATH
# (macOS: xattr -d com.apple.quarantine intuizi, or use brew/npm instead)
```

No runtime dependencies: the CLI ships as a single static Go binary for
macOS, Linux and Windows, amd64 and arm64. The npm package is a small launcher
that pulls in the binary for your platform as an optional dependency; nothing
is downloaded at install time beyond the packages themselves.

Or build from source, which needs the Go toolchain. Name the binary
`intuizi` and put it on your PATH, since every command below uses that name:

```bash
git clone https://github.com/intuizi/intuizi-cli && cd intuizi-cli
go build -o "$(go env GOPATH)/bin/intuizi" .   # or: make build, which writes ./bin/intuizi
```

A source build reports its version as `dev`.

## Quickstart

```bash
# Log in once, with the email and password you use for the Intuizi console
intuizi auth login

# Find the brand you want to target
intuizi reference poi brands --search starbucks

# See how many devices the audience would hold, without creating it
intuizi audiences estimate create --type poi --brand starbucks --country USA \
  --start-date 2026-09-02 --end-date 2026-09-09 --name "Starbucks - 1 week" --wait

# Build it, and wait until it is ready
intuizi audiences create --type poi --brand starbucks --country USA \
  --start-date 2026-09-02 --end-date 2026-09-09 --name "Starbucks - 1 week" --wait
```

`auth login` asks for the email and password of your Intuizi account and
keeps the token it mints in the OS credential store, or in
`~/.config/intuizi/config.json` where there is none; `auth status` shows where
it is held and which account it belongs to. To switch accounts, log in again
with the other account's `--email`. In CI, set `INTUIZI_API_TOKEN` to a token
minted in the console under My Account then API Tokens, and skip the login.

## Usage

Every command runs from flags. Names are resolved against the reference
catalogs, so "starbucks" works in place of an id looked up beforehand. A name
that matches several things resolves when exactly one of them carries that
exact name; otherwise nothing or several is an error listing what it found.
`--brand-all` takes every match for a search instead, for the deliberate "all
the coffee brands" case.

```bash
# Authenticate once; stores a bearer token for later commands
intuizi auth login

# Size an audience without creating it: the same flags as create
intuizi audiences estimate create \
  --type poi --brand starbucks \
  --country USA --state CA --city "San Francisco" \
  --start-date 2026-09-02 --end-date 2026-09-09 \
  --name "Starbucks visitors - SF - 1 week" --wait

# Build it, with the frequency analysis a preview needs, and block until it
# has built
intuizi audiences create \
  --type poi --brand starbucks \
  --country USA --state CA --city "San Francisco" \
  --start-date 2026-09-02 --end-date 2026-09-09 \
  --name "Starbucks visitors - SF - 1 week" --frequency --wait

# Count the devices seen on 2 to 5 distinct days
intuizi activations preview --audience-id 88 --freq-min 2 --freq-max 5

# Export exactly that range through one of the partner's datastreams,
# following the delivery to Completed
intuizi activations create --audience-id 88 \
  --endpoint-connection-id 12 --pricing-model-id 3 --datastream 7 \
  --freq-min 2 --freq-max 5 --filter-hash sha256:4f9d... --wait

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

# Rebuild an audience every week; --start must be in the future in --timezone
intuizi schedules create --name "Weekly refresh" --audience-id 88 \
  --start "2027-09-15 06:00:00" --timezone America/New_York \
  --frequency weekly --window 4
```

`--dry-run` prints the request body those flags produce and creates nothing,
so you can check a payload before it costs anything, or redirect it to a file
as a starting point. On `audiences create` it still reads the catalogs to
resolve names, so it needs a token; the other creates send nothing.

```bash
intuizi audiences create --type poi --brand starbucks ... --dry-run > audience.json
```

`--file payload.json` (or `-` for stdin) stays for what the flags do not
model: two datasets combined with an operator, the refine, crossvisitation and
crosspurchase blocks, the Cohorts, Demographics and ProfileAttributes audience
types, any other audience field no flag writes (project_id, POI locations,
DMAs, the day-part frequency analysis, datastreams), an
activation's partner and per-stream inputs, a schedule's auto-export, cohort
limits by visit frequency, distance or Lookalike Model score range, and Match
Strictness on an SCID cohort import.
Ready-made payloads live in [`examples/`](examples).

```bash
# Reads and creates take --json for scripting, and --quiet for ids alone
intuizi reference poi brands --search starbucks --quiet       # 208
id=$(intuizi audiences create --type poi ... --wait --quiet)
intuizi audiences show "$id" --json | jq '.data[0].normalized_payload'
```

How to build each create, including the lookup command behind every value, is
in [DOCS.md](DOCS.md); `--help` on any command lists all of its flags.

## Commands

| Command | What it covers |
| --- | --- |
| `auth` | `login`, `status`, `logout` |
| `audiences` | `list`, `show`, `create`, `delete`, `lookalike create\|cancel`, `estimate create\|show` |
| `activations` | `list`, `show`, `create`, `preview`, `delete` |
| `cohorts` | `list`, `show`, `create`, `preview`, `delete` |
| `schedules` | `list`, `show`, `create`, `activate`, `deactivate`, `delete` |
| `projects` | `list`, `show`, `create`, `delete` |
| `poi` | `segments`, `categories`, `brands`, `locations`, `submissions` |
| `uploads` | `reserve`, `put` |
| `reference` | 61 read-only catalogs across 10 groups |
| `usage` | monthly data-scan usage |
| `webhooks` | `list` |
| `completion` | shell completion for bash, zsh, fish, powershell |

In CI, set `INTUIZI_API_TOKEN` instead of running `auth login`.

## Design

- **Pure API v2 client.** Every command that calls the API uses documented
  endpoints of the Intuizi API v2 (`https://console.intuizi.com/api/v2`).
  Some make more than one call (catalog lookups for names, `--wait` polling,
  and the presigned storage PUT in `uploads put`), and `version`,
  `completion`, `auth logout` and `auth status` without `--verify` call none.
  No server-side logic lives here.
- **Most of the v2 surface.** Auth, audiences (including refine,
  crossvisitation and crosspurchase, and size estimates), activations
  (including the frequency preview), cohorts, schedules, projects, POI,
  uploads, usage and reference reads, the data stream visualization catalog
  among them.
- **Human first, script friendly.** Readable tables by default, `--json` for
  raw responses, `--quiet` for ids alone, and exit codes scripts can branch on.
- **Wrong input fails before it is sent.** Names are resolved against the
  catalogs, dataset types and date ranges are validated locally, and payloads
  built from flags come from typed fields rather than free-form JSON. A
  `--file` body is forwarded as written.

## Exit codes

| Code | Meaning |
| --- | --- |
| `0` | Success |
| `1` | A failure once the command line parsed: an API error, a `--wait` that failed, timed out or gave up, or a local failure such as an unreadable `--file`, a missing token or a declined confirmation |
| `2` | Usage error - an unknown command or flag, a bad value or id, missing or conflicting flags, a delete without `--yes` where stdin is not a terminal, or a name that matches no catalog entry or several. Nothing is created, though a name lookup or the `--provider` check may already have read a catalog |
| `130` | Interrupted with Ctrl-C (SIGINT) |
| `143` | Terminated (SIGTERM) |

The signal codes follow the shell's `128 + signal` convention, so a script can
tell a cancelled run from a failed one.

With the npm package, `intuizi` is a Node launcher that runs the binary and
passes its exit code through. A SIGTERM or SIGHUP sent to the launcher alone,
as Docker, systemd or a CI runner sends one, is passed on to the binary, so
the command stops and exits as it would without the launcher: `143` for a
SIGTERM. Ctrl-C reaches the binary through the terminal.

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
`--dry-run`. It writes `results.md` with every command's exit code and output,
and exits 1 if any step failed. Ctrl-C stops it after the step in flight,
cleans up and exits 130. SIGTERM stops it at once, logs the step it cut short
as failed, cleans up and exits 143. A Ctrl-C during cleanup fails only the
delete in flight and cleanup goes on; a second one, counting any that stopped
the run, abandons cleanup. Point it only at a test environment.

### CI

`.github/workflows/ci.yml` runs `go vet`, golangci-lint and `go test -race` on
every push to master and every pull request, across ubuntu-latest and
macos-latest. Lint runs on Ubuntu only, since results do not vary by OS.

A separate `package` job runs a goreleaser snapshot on every pull request,
checks that the rendered Homebrew formula parses, builds the npm packages from
the snapshot, installs the launcher from the tarballs and runs it. It also
runs the launcher's own tests (`node --test npm/cli/test/launcher.test.mjs`),
which check that exit codes and SIGTERM pass through it, and the tests of the
release's npm step (`node --test npm/test/publish.test.mjs`), which run
`npm/publish.sh` against a fake npm. Packaging breaks on the PR that causes
them, not on the release tag.

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
   `gh attestation verify <file> --repo intuizi/intuizi-cli`) and an SPDX
   SBOM, `<archive>.sbom.json`, publishes a GitHub Release, and commits the
   Homebrew formula to
   [`intuizi/homebrew-intuizi-cli`](https://github.com/intuizi/homebrew-intuizi-cli).
   The push uses an SSH deploy key stored as the `HOMEBREW_TAP_DEPLOY_KEY`
   secret; it can write to that one repository and nothing else. A prerelease
   tag such as `v0.2.0-rc1` gets a GitHub Release but leaves the formula alone.
3. **npm**: `npm/build.mjs` turns the release binaries into `@intuizi/cli` plus
   six `@intuizi/cli-<os>-<cpu>` platform packages, and `npm/publish.sh`
   publishes them by trusted publishing, platform packages first. It is all
   seven or nothing: the script asks the registry about every package before
   it uploads any (see
   [How npm publishing is authorised](#how-npm-publishing-is-authorised)). A
   version below 0.1.0 is built but not published, so the 0.0.x tags that
   exercise this pipeline cannot put a package on the registry. A prerelease
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
a pass/fail summary to `live-results/results.md` (gitignored - real API
responses). Activations stay at `--dry-run`, so `activations show`/`delete` are
not covered. A non-zero exit means do not tag. You need an account on the
environment you test against; each has its own database.

Configuration is in `.goreleaser.yaml` and `npm/`. To cut a release:

```bash
git tag -a v0.1.0 -m "v0.1.0" && git push origin v0.1.0
```

Then run the manual `brew-smoke` workflow, which installs the formula on clean
macOS and Linux runners and checks the reported version, and `npm-smoke`, which
does the same for `npm install -g @intuizi/cli` on Windows, macOS and Linux.

Test the packaging locally without publishing anything. Needs syft on PATH,
which the snapshot shells out to for the SBOMs (`brew install syft`):

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

By trusted publishing: there is no npm token. Each of the seven packages has a
trusted publisher entry on npmjs.com naming this repository and `release.yml`,
and the registry exchanges the release run's OIDC token for the right to
publish that one package. Nothing can leak, expire or need rotating. Every
upload also carries provenance, so a tarball on the registry can be traced back
to the commit and the run that built it.

`npm/publish.sh` asks the registry about every package before it uploads any:
npm attempts the exchange before every publish, a dry one included, and a dry
run uploads nothing. If the registry refuses one, the job fails with nothing
uploaded and names the package with the registry's reason. "package not found"
means the package has no entry that matches this workflow. Add one under the
package's Settings > Trusted Publisher on npmjs.com:

- Publisher: GitHub Actions
- Organization or user: `intuizi`
- Repository: `intuizi-cli`
- Workflow filename: `release.yml`
- Environment: none

or from a terminal, with npm 11.5.1 or later:

```bash
npm trust github @intuizi/cli-linux-x64 --file release.yml --repo intuizi/intuizi-cli --allow-publish
```

Either way npm asks for 2FA. Then re-run the job: it leaves alone whatever is
already on the registry.

To see how a version was published, ask the registry who published it. A
trusted publisher shows as GitHub Actions, anything else as a person:

```bash
npm view @intuizi/cli _npmUser    # GitHub Actions <npm-oidc-no-reply@github.com>
```

npm also prints a notice on every publish: "npm tokens that bypass 2FA are
being restricted for account changes and direct publishing". It is harmless
here. The registry attaches it even to requests that carry no token at all, so
it says nothing about how a publish was authorised; the publisher above does.

Two things follow from the entries being per package and bound to the
workflow's filename. Renaming `release.yml` breaks publishing until every entry
is recreated. And npm only lets a package that already exists have an entry, so
a new platform package has to be published once by hand before the workflow can
publish it.

## License

Proprietary, copyright Intuizi Inc. The source is published so you can read what
the tool does, not as open source: you may install and run unmodified copies to access Intuizi
services through your own account, and nothing more. Using the CLI requires an
Intuizi account and is subject to the Intuizi Terms of Service. See
[LICENSE](LICENSE); the open-source libraries compiled into the binary keep
their own licenses, reproduced in [THIRD_PARTY_NOTICES](THIRD_PARTY_NOTICES).

## Related

- [DOCS.md](DOCS.md) - how to build each create, the catalog behind each
  value, and the response shapes a script has to parse. `intuizi <command>
  --help` lists every flag.
- [examples/](examples) - ready-made payloads for the creates that take
  `--file`.
- The [Intuizi developer docs](https://console.intuizi.com/developers/)
  (sign in with your Intuizi account): the CLI pages, the API v2 reference,
  which is the source of truth for every endpoint this CLI wraps, and the MCP
  server, the agent-facing surface on the same API.
