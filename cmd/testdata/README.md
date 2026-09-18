# Test fixtures

`responses/` holds whole API v2 response envelopes. The tests serve them from
`net/http/httptest` servers, so nothing here ever reaches a real API. Go ignores
`testdata` directories when building, and `go test` runs with the package
directory as its working directory, which is why `fixture()` in
`fixture_test.go` can read them by plain relative path.

Only realistic, multi-field envelopes earn a file. Short bodies stay inline in
the test that uses them, where they can be read alongside the assertion.

## What is in here

| file | source record |
| --- | --- |
| `responses/audience-completed.json` | audience 15572, a completed build |
| `responses/activation-completed.json` | activation 5621, two delivered datastreams |
| `responses/activation-failed-datastreams.json` | activation 5561, Completed but every datastream failed |

Captured 2026-09-03 by reading those ids through the API v2 read endpoints on a
live account. They are not from the gamma staging console, and the ids do not
resolve there, so refreshing them needs an account where they do.

`activation-failed-datastreams.json` has no test reading it yet. It is the
shape `failedDatastreams` in `cmd/wait.go` parses, and it is here because a
plain `show` on such an activation looks perfectly healthy: the failure only
surfaces under `--wait`.

## These files are re-capturable, and must stay that way

No test asserts on a value in these files. Assertions name the columns a table
renders, or the key labels a detail view prints, never an id, a count or a
name. That is deliberate: a refreshed capture changes ids and totals, and no
test should notice.

A response body whose exact values a test does read is a **golden input**, not
a sample, and belongs inline. `audienceList` in `runner_test.go` is the
example. It carries a tab inside an audience name and pagination totals that
three assertions depend on, and no real response has either. Moving it to a
file would invite someone to re-capture over it and break the suite.

## Scrubbing is not optional

Every real response carries an internal address under `created_by`, and the
activations carry customer and partner names and a real delivery bucket. This
repository must become public at launch for the Homebrew and GitHub Releases
installs, so none of that can enter its history.

Replace, every time:

- personal and company names, with `Example User`, `Example Partner`,
  `Example Cloud`
- email addresses, with `user@example.com`
- storage URIs, with `s3://example-bucket/...`

Keep as captured: ids, status codes and their names, field names and nesting,
counts, totals, timestamps, and datastream error text. Those are the parts the
tests exercise.

`TestFixturesAreWellFormedEnvelopes` guards this, and
`TestRepositoryNamesNothingInternal` in `cmd/repo_scrub_test.go` guards the
same rule across every file in the repository, fixtures or not. The first
real leak was a bucket path inside an inline test body, which the fixture
guard could never have seen. It checks every file in
`responses/` is valid JSON, is a whole envelope carrying `status`, `code` and
`data`, and contains no bearer token or internal address. It reads each file
through `fixture()`, so it covers the helper as well.

## Two shapes worth knowing

A single read returns `data` as a **one-element array**, not an object.
`first[T]` in `internal/api/decode.go` accepts either.

A datastream's `results` is **polymorphic**: an object holding a `uri` when
delivery produced a file, and an empty array when it did not. A failed stream
also carries an `error` string. Both variants appear across the two activation
fixtures.

## Adding a fixture

Put response envelopes in `responses/`. The guard test assumes everything there
is a response, so a request body for a `--file` test does not belong in it;
create a sibling directory instead. Reusable create payloads already live in
`examples/` at the repository root.
