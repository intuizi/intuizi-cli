# Intuizi CLI documentation

A single-binary Go client for the Intuizi API v2. Manage audiences,
activations, cohorts, schedules, and first-party POI data from a terminal,
script, or CI pipeline.

## Install

```bash
brew install intuizi/intuizi-cli/intuizi   # Homebrew, macOS or Linux
npm install -g @intuizi/cli                # npm, any platform with Node 18+
go build -o ~/go/bin/intuizi .             # from source
```

Archives for every platform are on the GitHub Releases page. Homebrew and npm
serve versions from v0.1.0, the first public release.

## Authenticate

```bash
intuizi auth login     # exchanges credentials for an API token, stored in ~/.config/intuizi/
intuizi auth status    # shows the logged-in account
intuizi auth logout    # forgets the stored token
```

The token can also be supplied per-call with the `INTUIZI_API_TOKEN`
environment variable, which wins over the stored one. `auth logout` forgets the
token locally; it does not revoke it on the server.

Unattended, pass the email as a flag and pipe the password:

```bash
echo "$PASSWORD" | intuizi auth login --email you@example.com
```

A token is bound to the console that minted it, and the config file stores the
base URL alongside it. `auth login` reuses a stored token that still works for
the same console, since accounts are capped at 10 active tokens; logging in
with a different `--base-url` mints a new token there and replaces the stored
base URL and token together. The file and its directory are checked before
anything is minted, so a corrupt file or an unwritable directory fails without
spending a token slot, and `auth login`, `auth status` and `auth logout` all
name the file when it cannot be read. `auth status --verify` makes one request
to confirm the token is still accepted. Login and logout print commentary on
stderr; only `auth status` writes to stdout. Ctrl-C at a prompt exits 130 and
leaves the terminal as it found it.

## Global flags

| Flag | Meaning |
| --- | --- |
| `--json` | Print the raw response envelope instead of a table |
| `--quiet` | Print only ids on stdout, one per line, for shell scripts. Not with `--json` |
| `--base-url` | Console base URL (default: the stored value, then production) |
| `--idempotency-key` | Reuse one Idempotency-Key to retry a create whose outcome is unknown |

### Where the token is stored

`auth login` puts the token in the OS credential store when there is one: the
macOS Keychain, Windows Credential Manager, or the Linux secret service. The
config file keeps the base URL and the expiry either way, so `auth status` can
still tell you when the token runs out.

Containers, CI runners and SSH sessions have no credential store. There the
token falls back to the config file, written owner-only. `auth status` says
which of the two is in use.

Entries are keyed by console, so a token minted against one base URL is never
sent to another, and `auth logout` clears the entry for the console you are
logged in to. To clear another, log out against it:

```bash
intuizi --base-url https://other-console auth logout
```

Set `INTUIZI_NO_KEYRING=1` to skip the credential store and keep the token in
the config file. `INTUIZI_API_TOKEN` still takes precedence over both.

The config file is refused if its permissions are looser than 0600: it holds a
bearer token, and `auth login` always writes it owner-only, so a wider mode
means something else changed it.

## Commands

| Command | What it does |
| --- | --- |
| `audiences` | list, show, create, delete, and `lookalike create \| cancel` |
| `activations` | Deliver a completed audience to an endpoint connection |
| `cohorts` | Build from a cloud file, an upload, or an audience |
| `poi` | First-party locations, brands, categories and submissions |
| `reference <group> <catalog>` | Read-only catalogs; see below |
| `projects` | Group analyses under a project |
| `schedules` | Recurring rebuilds, with activate and deactivate |
| `uploads` | `reserve` a presigned slot, or `put` to upload in one step |
| `webhooks list` | Inspect webhook endpoints registered in the Console |
| `usage` | This month's data-scan usage and limits |
| `completion <shell>` | Shell completion script |

Deletes prompt for confirmation unless `--yes`.

## Building creates from flags

Every create takes flags. `--file payload.json` (or `-` for stdin) stays for the
nested cases named under each command, and giving both is rejected rather than
resolved silently.

Two habits make this safe:

- **`--dry-run`** prints the body the flags produce and creates nothing.
  Redirect it to a file for a starting point in the `--file` form. It still
  reads the catalogs to resolve names, so it needs a token and counts against
  the read budget; it cannot be combined with `--wait`.
- **Names resolve, ids do not.** A name is looked up and must match exactly one
  entry; zero or several is an error listing what was found, with ids to copy
  from. A numeric value is taken as the id and passed through unchecked, so a
  wrong number is not caught.

The global output flags work on creates as well as reads. `--json` prints the
full record the API returned rather than the table, and `--quiet` prints only
the new id, which is what chains one create into the next:

```bash
intuizi audiences create --type poi --brand starbucks ... --json | jq '.data[0].status'
id=$(intuizi audiences create --type poi --brand starbucks ... --wait --quiet)
```

`--dry-run` and `--json` do different things: one shows the request about to
be sent, the other the response that came back.

### Using a payload file

`--file` takes the whole body as JSON and forwards it untouched, so a field
this CLI has never heard of still reaches the API. Use it for the nested cases
named under each command below.

```bash
intuizi audiences create --file examples/audience-poi.json --wait
intuizi audiences create --file examples/audience-two-datasets.json
intuizi cohorts create --file examples/cohort.json
intuizi schedules create --file examples/schedule.json
```

`-` reads the body from stdin, so a payload can be edited in the pipeline
rather than written to disk first:

```bash
jq '.name = "Q3 rerun"' base.json | intuizi audiences create --file - --wait
```

That composes with the catalogs. `--quiet` gives bare values, and `jq` can
splice them into a template, which is the file-form equivalent of the
resolution the flags perform automatically:

```bash
providers=$(intuizi reference common signal-providers --data-type POI --quiet \
  | jq -Rsc 'split("\n") | map(select(length > 0))')

jq --argjson p "$providers" '.datasets[0].signal_providers = $p' base.json \
  | intuizi audiences create --file - --wait
```

Ready-made payloads for every command live in `examples/`. Every id in them is
a placeholder: replace each one with a value read from the account in use, or
the audience will build against ids that account does not have.

`--file` cannot be combined with a flag that builds a body, or with
`--dry-run`, since the file already carries one. `--wait`, `--timeout`,
`--json` and `--quiet` all work with it.

Because the body is forwarded as written, nothing validates it before it is
sent, which is the trade for being able to send fields the CLI does not model.
See [Checking what the API accepted](#checking-what-the-api-accepted).

### audiences create

| Flag | Takes | Where the value comes from |
| --- | --- | --- |
| `--type` | one dataset type, case-insensitive | `reference common dataset-types` |
| `--name` | any string | - |
| `--start-date` `--end-date` | `YYYY-MM-DD` | - |
| `--brand` | name or id, POI only, repeatable | `reference poi brands --search <name>` |
| `--brand-all` | a search, taking every match; POI only | `reference poi brands` |
| `--category` | name or id, repeatable | one catalog per type, below |
| `--provider` | id, repeatable | `reference common signal-providers --data-type <type>` |
| `--country` | ISO-3 code, repeatable | `reference common countries` |
| `--state` | state code, repeatable | `reference common states --countries USA` |
| `--city` | city name, repeatable | `reference common cities --states CA` |
| `--zipcode` | zip code, repeatable | `reference common zipcodes --cities "San Francisco"` |
| `--dry-run` `--wait` `--timeout` | - | - |

`--type` is case-insensitive in and canonical out: `poi` sends `POI`. Accepted
types are POI, Apps, WebDomain, CTV, Cohorts, AffinityTransactions,
Demographics, Deidentified and ProfileAttributes.

`--category` resolves against a different catalog for each type, and the
resolved ids go into a different payload field:

| `--type` | Catalog | Payload field |
| --- | --- | --- |
| POI | `reference poi categories` | `categories` |
| Apps | `reference apps categories` | `categories` |
| AffinityTransactions | `reference transactions categories` | `categories` |
| WebDomain | `reference web iab-categories` | `iab_category_codes` |

`--brand` is POI only. AffinityTransactions has brands too, in its own
`brands` field, which no flag writes yet; that filter needs `--file`.

`--brand-all` takes a search and selects every match, where `--brand` insists
on exactly one. It reports what it selected to stderr, so an expansion is
visible without polluting a piped payload:

```bash
intuizi audiences create --type poi --brand-all coffee \
  --start-date 2026-09-02 --end-date 2026-09-09 --name "Coffee - 1 week" --dry-run
```

```
--brand-all "coffee" selected 9 brands
```

It combines with `--brand`, so an exact brand plus a whole search is one
command. A search matching nothing is an error.

Omitting `--provider` includes every provider for the dataset type, which is
almost always right: a provider left out builds an audience that completes with
zero devices and no error. Provider sets differ per type, and a `--provider`
outside the type's catalog is rejected before anything is sent: the API would
accept it and build that same empty audience.

Blank values are rejected before anything is sent, as is a zero or negative
`--brand` or `--category` id. Beyond that a numeric id is passed through as
given, since no catalog filters by id.

```bash
intuizi audiences create \
  --type poi --brand starbucks \
  --country USA --state CA --city "San Francisco" \
  --start-date 2026-09-02 --end-date 2026-09-09 \
  --name "Starbucks visitors - SF - 1 week" --wait
```

Geography beyond `--country` is worth confirming on first use: see
[Checking what the API accepted](#checking-what-the-api-accepted).

#### The same audience as a file

`--dry-run` prints the body those flags produce, so redirect it to write the
file. **This is the command that creates the file:**

```bash
intuizi audiences create \
  --type poi --brand starbucks \
  --country USA --state CA --city "San Francisco" \
  --start-date 2026-09-02 --end-date 2026-09-09 \
  --name "Starbucks visitors - SF - 1 week" \
  --dry-run > starbucks-sf.json
```

**`starbucks-sf.json` then contains:**

```json
{
  "name": "Starbucks visitors - SF - 1 week",
  "datasets": [
    {
      "type": "POI",
      "start_date": "2026-09-02",
      "end_date": "2026-09-09",
      "analysisdata": [208],
      "signal_providers": [
        "80791f138dc009b67aabd14f3add93b3",
        "ef3d56f1305f0f8f28bdf35eb524d729",
        "c342914ea6c9214e27ed5772557ea115",
        "f13befc8485804e590cfe246cd20296e"
      ],
      "location": {
        "countries": ["USA"],
        "states": ["CA"],
        "cities": ["San Francisco"]
      }
    }
  ]
}
```

**And this is the command that sends it**, creating the identical audience:

```bash
intuizi audiences create --file starbucks-sf.json --wait
```

To write the file by hand instead, a heredoc takes the JSON literally. The
quotes around `'EOF'` matter: without them the shell expands `$` and backticks
inside the payload.

```bash
cat > starbucks-sf.json <<'EOF'
{
  "name": "Starbucks visitors - SF - 1 week",
  "datasets": [ ... ]
}
EOF
```

Either way, check it parses before sending. `jq` names the line on a trailing
comma or a missing brace:

```bash
jq . starbucks-sf.json
```

Note what the flags resolved. `208` is the Starbucks brand id, taken from
the name. The four signal providers are every POI provider on the account,
included because `--provider` was omitted. Writing this by hand means looking
both up first:

```bash
intuizi reference poi brands --search starbucks --quiet                 # 208
intuizi reference common signal-providers --data-type POI --quiet       # the four
```

There is no `operator` field, because one dataset does not take one.

#### Two datasets, which flags cannot express

An operator combines two dataset blocks, and each block carries its own
selectors, so flag names have nowhere to put them. This is the case `--file`
exists for:

**`two-datasets.json`:**

```json
{
  "name": "Apps OR Web - US - 1 week",
  "operator": "OR",
  "datasets": [
    {
      "type": "Apps",
      "start_date": "2026-08-09",
      "end_date": "2026-08-15",
      "signal_providers": ["ef3d56f1305f0f8f28bdf35eb524d729"],
      "categories": [1],
      "location": { "countries": ["USA"] }
    },
    {
      "type": "WebDomain",
      "start_date": "2026-08-09",
      "end_date": "2026-08-15",
      "signal_providers": ["80791f138dc009b67aabd14f3add93b3"],
      "iab_category_codes": [1],
      "location": { "countries": ["USA"] }
    }
  ]
}
```

**Send it with:**

```bash
intuizi audiences create --file two-datasets.json --wait
```

`operator` is `AND`, `OR` or `NOTIN`, from `reference common operators`.
`NOTIN` is order-sensitive: it subtracts the second dataset from the first.

Note that the selector field differs per type. Apps uses `categories`,
WebDomain uses `iab_category_codes`, and POI uses `analysisdata` for brands.

Needs `--file`: two datasets with an operator, and the nested `refine`,
`crossvisitation` and `crosspurchase` blocks.

```bash
intuizi audiences create --file examples/audience-two-datasets.json
intuizi audiences create --file examples/audience-refine-crosspurchase.json
```

### audiences lookalike create

| Flag | Takes | Where the value comes from |
| --- | --- | --- |
| `--name` | any string | - |
| `--source-audience-id` | the seed; see below | `audiences list` |
| `--target-size` | device count, capped at 4,000,000 | - |
| `--signal` | data family, repeatable | fixed list, below |
| `--country` | ISO-3 code, repeatable, required | `reference common countries` |
| `--state` | state code, repeatable | `reference common states --countries USA` |
| `--exclude-seed-devices` `--expand-eids` | booleans, default false, always sent | - |
| `--contrast-audience-id` | Completed, non-lookalike audience | `audiences list` |
| `--notify` | boolean | - |
| `--file` | the whole payload as JSON, or `-` for stdin | for fields the flags do not model |
| `--dry-run` | - | - |

The seed for `--source-audience-id` must be Completed, must not itself be a
lookalike, and must hold at least 1,000 devices.

Checked before anything is sent: `--name` is not blank, the two audience ids
are positive, `--target-size` is between 1 and 4,000,000, and no `--signal`,
`--country` or `--state` is blank. A repeated `--signal` is sent once.
`--file` takes the whole body instead, forwarded untouched, for anything the
flags do not model.

`--signal` takes `poi`, `apps`, `demographics`, `transactions` or
`profile_attributes`. `web` and `ctv` were withdrawn and are rejected.

Requires the Lookalike capability; a 403 means it is not enabled.

```bash
intuizi audiences lookalike create \
  --name "Starbucks lookalike" --source-audience-id 1381 \
  --target-size 500000 --signal poi --signal apps --country USA
```

### activations create

| Flag | Takes | Where the value comes from |
| --- | --- | --- |
| `--audience-id` | Completed audience | `audiences list` |
| `--endpoint-connection-id` | destination connection | `reference common endpoint-connections` |
| `--pricing-model-id` | pricing model for the export | `reference common pricing-models` |
| `--description` | any string | - |
| `--project-id` | project id | `projects list` |
| `--dry-run` | - | - |
| `--wait` `--timeout` | - | - |

An activation costs money, so `--dry-run` is worth a habit here: it prints the
body the flags produce, sends nothing, and needs no token. Not with `--wait` or
`--file`.

Pricing models are per partner: `reference common pricing-models --partner-id
<id>`. Endpoint partners come from `reference common endpoint-partners`, their
datastreams from `reference common datastreams --partner-id <id>`.

```bash
intuizi activations create --audience-id 88 \
  --endpoint-connection-id 4 --pricing-model-id 3 --wait

# or the whole body, for fields these flags do not model
intuizi activations create --file examples/activation.json --wait
```

### cohorts create

Exactly one source: `--file-uri`, `--upload-reference` or `--audience-id`.

| Flag | Takes | Where the value comes from |
| --- | --- | --- |
| `--name` | any string; rejected for an audience source | - |
| `--file-uri` | `s3://bucket/path` or `gs://bucket/path` | - |
| `--upload-reference` | reference from a reserved upload | `uploads reserve`, or `uploads put` |
| `--audience-id` | Completed audience | `audiences list` |
| `--file-format` | `csv`, `gzip` or `parquet` | - |
| `--identifier-type` | one of nine, below | - |
| `--identifier-column` | column name | `cohorts preview` |
| `--metadata-columns` | column name, repeatable | `cohorts preview` |
| `--ip-enrichment` | boolean | - |
| `--device-limit` | device cap | - |
| `--project-id` | project id | `projects list` |
| `--dry-run` | - | - |

`--identifier-type` takes `eid`, `eid_md5`, `maid`, `ip`, `hem_plaintext`,
`scid`, `hem_md5`, `hem_sha1` or `hem_sha256`.

A `file_uri` ending `.csv`, `.gz` or `.parquet` is read as a single file;
anything else is read as a folder, and whitespace anywhere in it is refused.
An audience makes at most one live cohort, and the cohort takes the audience's
own name, so `--name` is rejected alongside `--audience-id` rather than sent
to be ignored.

```bash
# from a cloud file
intuizi cohorts create --name "Q3 customers" \
  --file-uri s3://example-bucket/cohorts/q3.csv --file-format csv \
  --identifier-type hem_sha256 --identifier-column email_sha256

# from an upload
intuizi cohorts create --name "Q3 customers" \
  --upload-reference upl_abc123 --file-format csv \
  --identifier-type hem_sha256 --identifier-column email_sha256

# from a completed audience
intuizi cohorts create --audience-id 88 --device-limit 1000
```

Needs `--file`: capping an audience cohort by visit frequency (`freq_limit`
with `freq_min`/`freq_max`) or by distance (`distance_limit` with `distance` in
meters) instead of a device count.

```bash
cat > freq-capped.json <<'EOF'
{
  "name": "Frequent visitors",
  "source": "audience",
  "audience_id": 88,
  "freq_limit": true,
  "freq_min": 3,
  "freq_max": 10
}
EOF

intuizi cohorts create --file freq-capped.json
```

`examples/cohort-from-audience.json` uses `device_limit`, which the flags now
express directly as `--audience-id 88 --device-limit 1000`.

### cohorts preview

| Flag | Takes | Where the value comes from |
| --- | --- | --- |
| `--file-uri` | `s3://bucket/path` or `gs://bucket/path` | - |
| `--upload-reference` | reference from a reserved upload | `uploads reserve`, or `uploads put` |
| `--file-format` | `csv` or `gzip`; sniffed if omitted | - |

Exactly one of `--file-uri` or `--upload-reference`. Parquet cannot be
previewed. `--quiet` does not apply: a preview returns sample rows, not an id.

Run this before `cohorts create` to confirm `--identifier-column`. The output
is the file's column names as a header row with the sample rows under it, and
the row count on stderr; `--json` has the same `columns`, `samples` and
`sample_rows` fields in the raw envelope.

```bash
$ intuizi cohorts preview --file-uri s3://example-bucket/cohorts/q3.csv
email_sha256  city         zip
ab12          New York     10001
cd34          Los Angeles  90001
2 sample rows
```

### schedules create

| Flag | Takes | Where the value comes from |
| --- | --- | --- |
| `--name` | any string | - |
| `--audience-id` | audience to rebuild each cycle | `audiences list` |
| `--project-id` | project id | `projects list` |
| `--start` | `"YYYY-MM-DD HH:MM:SS"`, read in `--timezone`, must be in the future | - |
| `--timezone` | IANA name, e.g. `America/New_York` | - |
| `--frequency` | one of four, case-insensitive | `reference common schedule-frequencies` |
| `--window` | data window id, `1` to `8` | `reference common schedule-windows` |
| `--window-days` | `1` to `365`, `--window 3` Custom only | - |
| `--ending` | `1`, `2` or `3`; default `1` | `reference common schedule-endings` |
| `--after-recurrences` | run count of `1` or more, `--ending 2` only | - |
| `--end-date` | `YYYY-MM-DD`, `--ending 3` only | - |
| `--dry-run` | - | - |

`--frequency` takes `daily`, `weekly`, `bi-weekly` or `monthly`. `--ending` is
`1` Never, `2` Recurrences or `3` Custom Date.

Each ending rule carries its own field, and a mismatch is rejected here because
the API ignores the wrong one rather than refusing it. `--start` and
`--timezone` are checked before anything is sent: the zone must be a real IANA
name, the start must parse in the layout above, and it must still be in the
future in that zone.

```bash
intuizi schedules create --name "Weekly coffee refresh" --audience-id 88 \
  --start "2027-09-15 06:00:00" --timezone America/New_York \
  --frequency weekly --window 2
```

Needs `--file`: the nested activation block that re-exports every cycle.

```bash
intuizi schedules create --file examples/schedule.json
```

### poi submissions create

| Flag | Takes | Where the value comes from |
| --- | --- | --- |
| `--name` | any string | - |
| `--brand-id` | a first-party brand id | `poi brands list` |
| `--file` | a `.csv` or `.txt` of locations | - |
| `--list` | JSON body carrying `locations[]`, or `-` for stdin | - |
| `--upload-reference` | reference from a reserved upload | `uploads put` |
| `--key` | how to match existing POIs, below | - |
| `--update` `--remove` | booleans; both need `--key` | - |

Exactly one of `--file`, `--list` and `--upload-reference`. `--file` and
`--upload-reference` need `--name` and `--brand-id`; `--list` takes both from
the body unless the flags override them. Only `--list` reads stdin: `--file -`
is refused and points at `--list -`.

`--key` takes `location-id`, `gps-coordinates`, `store-id`, `master-id` or
`external-id`. It travels on every route, so `--list ... --update --key
store-id` updates the matched POIs rather than inserting duplicates.

Submission CSVs need `latitude`, `longitude`, and a country column headed
`country|alpha_2` or `country|alpha_3`. The taxonomy nests segments >
categories > brands; create the parent first.

#### The rest of poi

| Command | Flags | Notes |
| --- | --- | --- |
| `poi segments list` | `--search` | ids come back as `value` |
| `poi categories list` | `--search` | |
| `poi categories create` | `--name`, `--segment-id` | parent id from `poi segments list` |
| `poi brands list` | `--search` | |
| `poi brands create` | `--name`, `--category-id` | parent id from `poi categories list` |
| `poi locations list` | `--search`, `--brands`, `--countries`, `--geometry`, `--page`, `--per-page` | `--brands` takes your own brand ids, repeatable or comma-separated; `--countries` repeats; `--geometry` is `polygon` or `coordinates` |
| `poi locations show <id>` | | |
| `poi submissions list` | `--search`, `--sort-by`, `--order` | not paginated; `--sort-by` is `name`, `status`, `created_at` or `updated_at`; `--order` is `asc` or `desc` |
| `poi submissions show <id>` | | where an asynchronous submission is followed |
| `poi submissions delete <id>` | `--yes` | only a waiting submission can be deleted |

`--search` on `locations list` matches name, address, city, state, zip, DMA,
external id and placekey; on the taxonomy lists and `submissions list` it
matches the name. A value outside a flag's set - `--key`, `--geometry`,
`--sort-by`, `--order`, an empty `--name` or a zero parent id on the creates -
is refused before anything is sent, exit 2.

### uploads

| Command | Flag | Takes |
| --- | --- | --- |
| `reserve` | `--purpose` | `poi_submission` or `cohort` |
| | `--filename` | original filename |
| | `--content-length` | exact byte size of the PUT |
| | `--content-type` | MIME type, default `text/csv` |
| `put <file>` | `--purpose` | `poi_submission` or `cohort` |
| | `--content-type` | MIME type, default `text/csv` |

`reserve` returns a presigned URL for the caller to PUT to. `put` does both
steps and prints the `upload_reference` alone on stdout; with `--json` it prints
the reservation envelope instead, once the PUT has succeeded, so
`.data[0].upload_reference` is the same value either way.

```bash
ref=$(intuizi uploads put customers.csv --purpose cohort)
intuizi uploads put customers.csv --purpose cohort --json | jq -r '.data[0].upload_reference'
```

Every header the reservation lists is sent on the PUT, since all of them are
signed; `--content-type` replaces `Content-Type` alone. An empty file is
refused before a slot is reserved, a `--purpose` outside the two values before
anything is sent, and a redirect from the storage host is reported as an error
rather than followed.

## Reference catalogs

Every id above has a lookup. All reads are GETs with no side effects, take
`--search` for a case-insensitive contains match, and honour both output flags:
`--quiet` for bare values one per line, `--json` for the full envelope when the
label is needed as well as the value. The one exception is
`profile-attributes recency-limits`: it takes no parameters, and its rows are
`{start_limit, end_limit}` with nothing `--quiet` could print, so `--quiet` is
refused there (exit 2) - read it with `--json`.

- **common** - dataset-types, countries, states, cities, dmas, zipcodes,
  operators, languages, signal-providers, endpoint-partners,
  endpoint-connections, pricing-models, datastreams, schedule-frequencies,
  schedule-windows, schedule-endings
- **poi** - segments, categories, brands, locations
- **apps** - categories, tags, os, bundle-ids, taxonomies
- **web** - iab-categories, iab-subcategories, domains, ref-domains, browsers,
  device-types, device-makes, device-oses
- **ctv** - vendors, content-types, content-genres, channel-names,
  device-types, device-makes, device-oses, connection-types, isps, series
- **transactions** - categories, subcategories, brands, incomes, ages, genders,
  ethnicities
- **demographics** - genders, ages, marital-statuses, incomes
- **profile-attributes** - categories, keys, values, recency-limits
- **deidentified** - fields
- **cohorts** - list

The geography catalogs cascade: countries, then states, then cities, then
zipcodes. Each level takes the level above it. The POI taxonomy cascades the
same way: segments, then categories, then brands, then locations.

Paginated reads return one page per call; ask for more with `--page` and
`--per-page`. Both must be 1 or more: the API rejects 0 rather than treating it
as unset, so the flag refuses it before anything is sent.

```bash
intuizi reference common states --countries USA --search california
intuizi reference common cities --states CA --search "san francisco"
intuizi reference poi brands --search starbucks --quiet    # 208
intuizi reference poi brands --search starbucks --json | jq -c '.data[0]'
```

## Reading responses

### --quiet or --json

They do different jobs and cannot be combined; asking for both is an error.

| Flag | Prints | Use it for |
| --- | --- | --- |
| neither | a table, or key/value lines for one record | reading at a terminal |
| `--quiet` | bare ids on stdout, one per line | capturing a value, or feeding a loop |
| `--json` | the raw response envelope | reaching any field the table does not show |

Both are global, so they apply to every command: reads, creates and deletes
alike. `--quiet` prints one scalar per record, so it cannot reach a nested
field. Anything deeper needs `--json` and `jq`.

```bash
id=$(intuizi audiences create --type poi --brand starbucks \
       --start-date 2026-09-02 --end-date 2026-09-09 \
       --name "Starbucks - SF" --wait --quiet)

intuizi audiences show "$id" --json | jq '.data[0].normalized_payload'
```

Commentary always goes to stderr: status changes while `--wait` polls, the
"page 1 of N" footer, "no results". A pipe therefore sees only data.

On a failure `--json` still prints what the server sent: the error envelope
goes to stdout, the one-line error goes to stderr as usual, and the exit code
is 1. A script can therefore read a 422's field errors from the same place it
reads a success:

```bash
out=$(intuizi projects create --name "" --json) || jq '.data.errors' <<<"$out"
```

A failure whose body is not JSON, such as an HTML error page from a proxy,
prints nothing on stdout; only the stderr line explains it.

### Envelope shapes

Every response shares the `status`, `code`, `message`, `data` envelope, but what
sits inside `data` differs by the kind of read. This is the usual reason a `jq`
path returns nothing.

| Read | Path to the records |
| --- | --- |
| `show <id>` | `.data[0]` - an array of one, so the index is required |
| `list` | `.data.items[]`, with `.data.pagination` alongside |
| `reference`, unpaged catalog | `.data[]` - a bare array |
| `reference`, paged catalog | `.data.items[]` |

A script consuming both reference shapes must switch on the type.
`.data.items // .data` does not work, because indexing an array with a string
raises rather than falling through:

```bash
items='(if (.data|type) == "object" then .data.items else .data end)'
intuizi reference apps categories --search food --json | jq "$items[0].value"
```

Or avoid it entirely, since `--quiet` handles both shapes:

```bash
intuizi reference apps categories --search "Food & Drink" --quiet    # 23
```

### Checking what the API accepted

A `--file` body is forwarded as written. A field the API does not recognise is
ignored rather than rejected, so a mistyped key or an unsupported filter
produces a Completed resource that quietly does not mean what was intended.

`normalized_payload` on an audience is the API's own record of what it kept.
Reading it back is the only way to confirm a filter applied:

```bash
intuizi audiences show <id> --json | jq '.data[0].normalized_payload.datasets[0].location'
```

A filter missing from the response was dropped. An implausible `results_count`
is the other symptom: a signal provider id outside the account's catalog is not
rejected, and the audience completes with zero devices.

Three things to know about the field:

- It is spelled `normalized_payload`, with underscores.
- It reflects what was stored, not what is valid to send. A single-dataset
  audience reads back with `"operator": "Single"`, but `Single` is not a value
  the operators catalog accepts on the way in, so do not copy it into a new
  payload.
- It is only populated on recently created records; older audiences return null.

Building creates from flags avoids most of this: names are resolved against the
catalogs before anything is sent, and the payload is assembled from typed fields
rather than free-form JSON.

## The async model

Creates return immediately and the resource completes in the background.
`audiences create`, `audiences show`, `activations create` and
`activations show` take `--wait` with an optional `--timeout` (default 60m).
Waiting polls every 15 seconds, prints status changes to stderr and the final
record to stdout, and exits non-zero on failure.

Audience and activation status ids share one scale, where `104` is Completed.
`108` Modeling, seen during a lookalike run, is not terminal.

Cohort status ids are their own scale: `1` Uploading, `2` Initiating,
`3` Processing, `4` Completed, `5` Not Available.

Everything else is followed with `show <id>`, or by a webhook registered in the
Console.

## Shell completion

Cobra generates a completion script for each shell:

```bash
intuizi completion zsh > "${fpath[1]}/_intuizi"
exec zsh
```

Zsh needs its completion system switched on for this, or any other, completion
to load. If `intuizi aud<TAB>` does nothing, add to `~/.zshrc`:

```bash
autoload -U compinit && compinit
```

Completion covers commands and flag names. Flag values do not complete yet.

## Typical workflows

```bash
# Audience -> activation, ids carried between steps
id=$(intuizi audiences create --type poi --brand starbucks \
       --country USA --start-date 2026-09-02 --end-date 2026-09-09 \
       --name "Starbucks visitors - 1 week" --wait --quiet)

intuizi activations create --audience-id "$id" \
  --endpoint-connection-id 4 --pricing-model-id 3 --wait

# Cohort from a cloud file, columns confirmed first
intuizi cohorts preview --file-uri s3://example-bucket/cohorts/q3.csv
intuizi cohorts create --name "Q3 customers" \
  --file-uri s3://example-bucket/cohorts/q3.csv --file-format csv \
  --identifier-type hem_sha256 --identifier-column email_sha256

# First-party POIs: find the brand id, submit a CSV, follow it
intuizi poi brands list --search example
intuizi poi submissions create --name my-locations --brand-id 123 --file locations.csv
intuizi poi submissions show <id>
```

## Development

```bash
go build ./... && go vet ./... && go test -race ./... && golangci-lint run
```

Command logic lives in `cmd/`, the HTTP client and envelope decoding in
`internal/api/`, token and config storage in `internal/config/`, and table and
JSON rendering in `internal/output/`. Create payload examples are in
`examples/`; every id in them is a placeholder.
