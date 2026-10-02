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
intuizi auth login     # exchanges credentials for an API token and stores it
intuizi auth status    # shows the token in use and the account it belongs to
intuizi auth logout    # forgets the stored token
```

The token goes to the OS credential store when there is one, otherwise to
the config file; see [Where the token is stored](#where-the-token-is-stored).
It can also be supplied per-call with the `INTUIZI_API_TOKEN`
environment variable, which wins over the stored one. `auth logout` forgets the
token locally; it does not revoke it on the server.

Unattended, pass the email as a flag and pipe the password:

```bash
echo "$PASSWORD" | intuizi auth login --email you@example.com
```

A token is bound to the console that minted it, and the config file stores the
base URL and the account email alongside it. `auth login` reuses a stored token
that still works for the same console and the same account, since accounts are
capped at 10 active tokens, and says on stderr which account is logged in.
Without `--email` the stored token is kept. An `--email` that differs from the
stored account's, compared ignoring case, mints a token for that account and
replaces the stored one; the replaced token stays valid on the server until it
expires or is revoked at My Account > API Tokens. A token stored before the
account was recorded (v0.1.3 and earlier) cannot be matched, so the first
`auth login --email` after upgrading mints a new token and records the account,
and stderr says it replaced the stored token and whether that token was still
valid; `auth login` without `--email` keeps it.

Logging in with a different `--base-url` mints a token for that console and
makes it the one in use, with its account. In the config file that replaces
the previous console's token. In the OS credential store the previous
console's token stays stored under that console: a plain `auth logout` does
not remove it, `auth status` does not show it, and logging back in to that
console mints another rather than reusing it. Remove it with
`intuizi --base-url <previous console> auth logout`, as
[Where the token is stored](#where-the-token-is-stored) shows. Either way it
stays valid on the server until it expires or is revoked at My Account > API
Tokens.

The file and its directory are checked before anything is minted, so a
corrupt file or an unwritable directory fails without spending a token slot,
and `auth login`, `auth status` and `auth logout` all name the file when it
cannot be read. When the credential store
does not answer within two seconds and the config file names a console but
does not hold the token itself, `auth login` fails before it mints anything;
unlock the store and try again. A login that mints while the store is not
answering, such as the first login on a machine, saves the new token to the
config file instead and says so on stderr. `auth status --verify` makes one
request to confirm the token is still accepted. Login and logout print commentary on
stderr; only `auth status` writes to stdout. Ctrl-C at a prompt exits 130 and
leaves the terminal as it found it.

`auth status` prints an `Account:` line. It names the stored token's account,
or says it is unknown for a token stored before accounts were recorded, and
that `auth login --email <address>` mints a token that records it. When
`INTUIZI_API_TOKEN` is in effect the account is reported unknown: that token
was not minted by `auth login`, so nothing local says whose it is, and the
`--verify` request does not return the user either.

```
$ intuizi auth status
Base URL: https://console.intuizi.com
Token:    present (in the OS credential store)
Account:  you@example.com
Expires:  2027-09-26 (in 364 days)
```

Every command refuses a stored token for a `--base-url` other than the
console that minted it, rather than send the credential to another host.
`auth status` does the same: with another console's `--base-url` it prints
`Token:    none for this console (the stored one belongs to <url>)`, no
account or expiry, sends nothing even with `--verify`, and exits 1.

`auth logout` forgets the account email with the token, and stays local: it
never calls the server.

## Global flags

| Flag | Meaning |
| --- | --- |
| `--json` | Print the raw response envelope instead of a table |
| `--quiet` | Print only ids on stdout, one per line, for shell scripts. Not with `--json` |
| `--base-url` | Console base URL (default: the stored value, then production) |
| `--idempotency-key` | Reuse one Idempotency-Key to retry a create whose outcome is unknown |
| `--debug` | Print every HTTP request and response on stderr, with credentials redacted |

### `--debug`

Each request and response on stderr, one item per line: the method and URL,
then one line per request header; the status and how long it took, then one
line per response header, then how many bytes came back. Stdout is untouched,
so `--debug` composes with `--json` and `--quiet`.

```
> GET https://console.intuizi.com/api/v2/analyses/reference/poi/brands?search=star
>   Accept: application/json
>   Authorization: <redacted>
>   User-Agent: intuizi-cli/v0.1.4
< 200 OK in 212ms
<   Content-Type: application/json
<   ...
<   1843 bytes read
```

Credentials are redacted: the `Authorization` and `Idempotency-Key` headers,
cookies, every `x-amz-*` header, any userinfo in a URL, and the signature
parameters in a presigned upload URL, which carries no `Authorization` header
because the signature in its query string is the credential.

**Bodies are never printed.** `--json` already shows the response envelope,
including on a failure, and `--dry-run` shows the body a create would send, so
a body dump here would be a third route to the same information carrying risk
the other two do not: the auth route sends your password and receives a token
good for a year, and a reservation response carries a presigned URL. Printing
no body also means `--debug` never reads one, so it cannot change what a
command does.

A transcript is still safe to read before sending it on: it names the host, the
path and any search terms you passed.

### Rate limits

On a `429` the CLI waits the `Retry-After` the response names and tries again,
at most twice, and says so on stderr each time
(`rate limited (429): retrying in 3s (retry 1 of 2)`). A create keeps its
Idempotency-Key across those retries. A `429` that asks for more than 60
seconds, such as the organization build budget's, is not waited out: the
command exits `1` at once, and the error ends with the wait the server asked
for, as in `(429, Retry-After: 1800s)`. Run it again after that.

A create that claims an upload reference (`cohorts create --upload-reference`,
`poi submissions create --upload-reference`, or `upload_reference` in a
`--file` body) is retried only after the per-minute rate limiter's `429`. The
build budget can refuse a cohort create after the reference is claimed, so a
retry would fail with `The upload reference has already been used.` and hide
the refusal. That `429` is returned as it came, and the error says to upload
the file again for a new reference.

### Where the token is stored

`auth login` puts the token in the OS credential store when there is one: the
macOS Keychain, Windows Credential Manager, or the Linux secret service. The
config file keeps the base URL, the expiry and the account email either way,
none of them secret, so `auth status` can still tell you when the token runs
out and whose it is.

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
the config file. Any non-empty value switches the store off except one that
reads as false: `0` and `false` leave it on, as leaving it unset does. Set
it before `auth login` and keep it set: while it is set, a token already in the
credential store is not read, so commands report that you are not logged in,
and `auth login` mints a new token, which counts toward the account's 10.
`INTUIZI_API_TOKEN` still takes precedence over both.

The config file is refused if its permissions are looser than 0600: it holds a
bearer token, and `auth login` always writes it owner-only, so a wider mode
means something else changed it.

## Commands

| Command | What it does |
| --- | --- |
| `audiences` | list, show, create, delete, `lookalike create \| cancel`, and `estimate create \| show` to size one without creating it |
| `activations` | Deliver a completed audience to an endpoint connection, and `preview` the devices a frequency filter would export |
| `cohorts` | Build from a cloud file, an upload, or an audience |
| `poi` | First-party locations, brands, categories and submissions |
| `reference <group> <catalog>` | Read-only catalogs; see below |
| `projects` | Group analyses under a project |
| `schedules` | Recurring rebuilds, with activate and deactivate |
| `uploads` | `reserve` a presigned slot, or `put` to upload in one step |
| `webhooks list` | Inspect webhook endpoints registered in the Console |
| `usage` | Data-scan usage and limits for a calendar month (`--month YYYY-MM`, default the current month) |
| `completion <shell>` | Shell completion script |

Deletes prompt for confirmation unless `--yes`.

## Building creates from flags

Every create takes flags. `--file payload.json` (or `-` for stdin) stays for the
nested cases named under each command, and giving both is rejected rather than
resolved silently.

Two habits make this safe:

- **`--dry-run`** prints the body the flags produce and creates nothing.
  Redirect it to a file for a starting point in the `--file` form. On
  `audiences create` and `audiences estimate create` it still reads the
  catalogs to resolve names, so it needs a token and counts against the read
  budget; the other creates send nothing and need no token. It cannot be
  combined with `--wait`.
- **Names resolve, ids do not.** A name is looked up and must match exactly one
  entry. The lookup matches substrings, so when several come back and exactly
  one is labelled with the name itself, ignoring case, that one is taken:
  `Example Coffee` resolves even though `Example Coffee Reserve` also matches.
  Otherwise zero or several is an error listing what was found, with ids to
  copy from, and so is an exact label on a result too long to arrive in one
  page. A numeric value is taken as the id and passed through unchecked, so a
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
the audience will build against ids that account does not have. The dates are
examples too: a `WebDomain` start date must fall within the last 45 days, and
a schedule's start must be in the future.

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
| `--country` | ISO-3 code, repeatable; the API requires it for every type except Cohorts, AffinityTransactions and ProfileAttributes, and the CLI checks it before sending only on Origin | `reference common countries` |
| `--state` | state code, repeatable | `reference common states --countries USA` |
| `--city` | city name, repeatable | `reference common cities --states CA` |
| `--zipcode` | zip code, repeatable | `reference common zipcodes --cities "San Francisco"` |
| `--filter` | POI quality filter, repeatable: `anomalous_devices`, `anomalous_pois` or `strict_gps`; POI only, none by default | - |
| `--frequency` | boolean: run the type's frequency analysis; POI, Apps and WebDomain only | - |
| `--dry-run` `--wait` `--timeout` | - | - |

`--type` is case-insensitive in and canonical out: `poi` sends `POI`. The
flags build POI, Apps, WebDomain, CTV, AffinityTransactions, Deidentified and
Origin. Cohorts, Demographics and ProfileAttributes are refused before
anything is sent, exit 2, and go through `--file`: each requires a field no
flag writes (a `cohort_id`, at least one demographic filter, or
`profile_attributes` rows or `profile_attribute_groups`), and Demographics
also rejects the dates and signal providers the flags always send.

`--category` resolves against a different catalog for each type, and the
resolved ids go into a different payload field:

| `--type` | Catalog | Payload field |
| --- | --- | --- |
| POI | `reference poi categories` | `categories` |
| Apps | `reference apps categories` | `categories` |
| AffinityTransactions | `reference transactions categories` | `categories` |
| WebDomain | `reference web iab-categories` | `iab_category_codes` |

An IAB category row carries two identifiers: the IAB code (`IAB2`) as `value`,
which `reference web domains --category-codes` takes, and the catalog `id`
(`2`), which is what `iab_category_codes` takes. A WebDomain `--category` name
resolves to the `id`, and a number passed instead must be that `id`, not the
code. Its label reads `IAB2 - Automotive`, so the exact-match rule takes the
row whose code (`IAB1`) or name (`Automotive`) is the one given: `IAB1`
resolves to IAB1 rather than failing because IAB10 to IAB19 also contain it.

`--brand` is POI only. AffinityTransactions has brands too, in its own
`brands` field, which no flag writes yet; that filter needs `--file`.

`--frequency` runs the dataset type's frequency analysis as the audience
builds, the toggle Audience Manager calls Visitation Frequency (POI), Apps
Frequency (Apps) or Web Frequency (WebDomain). It sends the matching key in
the `analyses` block - `frequency`, `apps_frequency` or `web_frequency` - and
is refused on any other type before anything is sent, exit 2. The analysis
counts the distinct days each device was seen, which is what
[`activations preview`](#activations-preview) sums and what an activation's
`--freq-min` and `--freq-max` filter on. It cannot be added after the build,
and it requires additional permissions which need to be approved by your
Account Manager: a 403 means they are not enabled for the account. The
day-part variant (`frequency_day_part`) needs `--file`, and cannot be
previewed. This is not `schedules create --frequency`, which sets how often a
schedule runs.

`--filter` sends the POI quality filters, the POI filter list in Audience
Manager, in the dataset's `filters` field. None apply unless given.
`anomalous_devices` drops devices on Intuizi's list of known bad devices,
`anomalous_pois` drops every visit to locations flagged as anomalous, and
`strict_gps` (GPS Strict Filtering) drops every visit at a spot where more than
500 devices from one signal provider were seen at that location in the same
month: city-center defaults and rounded coordinates reported for many devices
at once. Use `strict_gps` for address-level lists such as offices and small
businesses. Leave it off for stores, malls and venues, where real crowds
gather on one spot and it removes them too. Audience Manager turns on
`anomalous_devices` and `anomalous_pois` by default, so pass both to match an
audience built there. Values are case-insensitive and a repeat is sent once.
An unknown value, or `--filter` on any type but POI, is refused before
anything is sent, exit 2.

```bash
intuizi audiences create --type poi --brand "Example Offices" --country USA \
  --start-date 2026-06-01 --end-date 2026-08-31 \
  --filter anomalous_devices --filter anomalous_pois --filter strict_gps \
  --name "Example Offices - summer" --dry-run
```

`--type origin` targets devices by their home location rather than the places
they visited, so its only filters are geographic. `--country` is required,
because the API requires it: `reference common countries --dataset-type Origin`
lists the countries with Origin data, and the API rejects a country outside
that set when the console has Origin coverage configured. `--state`, `--city`
and `--zipcode` narrow it further; DMAs, which Origin also accepts, need
`--file`. `--brand`, `--brand-all` and `--category` are refused for Origin
before anything is sent, exit 2. Signal providers apply as for any other type:
omitting `--provider` sends every Origin provider.

Origin data is weekly, so the API widens the window to the whole
Monday-to-Sunday weeks it touches. The body keeps the dates as given, and when
they are not already whole weeks stderr names the window that is actually
scanned:

```bash
intuizi audiences create --type origin --country USA --state CA \
  --start-date 2026-09-02 --end-date 2026-09-09 --name "CA residents" --dry-run
```

```
Origin data is weekly: the API widens 2026-09-02..2026-09-09 to the whole weeks 2026-08-31..2026-09-13
```

`--brand-all` takes a search and selects every match, where `--brand` insists
on exactly one. It reports what it selected to stderr, so an expansion is
visible without polluting a piped payload:

```bash
intuizi audiences create --type poi --brand-all coffee --country USA \
  --start-date 2026-09-02 --end-date 2026-09-09 --name "Coffee - 1 week" --dry-run
```

```
--brand-all "coffee" selected 9 brands
```

It combines with `--brand`, so an exact brand plus a whole search is one
command. A search matching nothing is an error.

Omitting `--provider` includes every provider for the dataset type, which is
almost always what you want. Provider sets differ per type, and a `--provider`
outside the type's catalog is rejected before anything is created (the CLI
reads that catalog first, so the check needs a token): the API would accept it
and build an audience that completes with zero devices. A `--file`
body lists its own `signal_providers`.

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
      "start_date": "2026-09-13",
      "end_date": "2026-09-19",
      "signal_providers": ["ef3d56f1305f0f8f28bdf35eb524d729"],
      "categories": [1],
      "location": { "countries": ["USA"] }
    },
    {
      "type": "WebDomain",
      "start_date": "2026-09-13",
      "end_date": "2026-09-19",
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

Move the dates to a recent week before sending. A `WebDomain` dataset's
`start_date` must fall within the last 45 days, because older web data is
archived: the earliest date accepted is today minus 46 days, in UTC, and an
earlier one is rejected with a 422 that names it. The dates here, and in
`examples/audience-two-datasets.json`, are illustrations that go stale.

`operator` is `AND`, `OR` or `NOTIN`, from `reference common operators`.
`NOTIN` is order-sensitive: it subtracts the second dataset from the first.

Note that the selector field differs per type. Apps uses `categories`,
WebDomain uses `iab_category_codes`, and POI uses `analysisdata` for brands.

Needs `--file`: two datasets with an operator; the nested `refine`,
`crossvisitation` and `crosspurchase` blocks; the Cohorts, Demographics and
ProfileAttributes types; and any other field no flag writes, such as
`project_id` to file the audience under a project, POI `locations`, DMAs,
`analyses` beyond the one key `--frequency` writes (the day-part variant, or
a frequency analysis for each dataset of a two-dataset body), and
`datastreams`. An audience built without a frequency analysis cannot have it
added after the build, and `activations preview` needs it, so put it in the
body when you plan to preview a `--file` audience. `datastreams` asks for data
stream visualizations, which the audience draws as status `109` before it
completes: `reference common datastream-visualizations --dataset-type <type>`
lists the ids.

```bash
intuizi audiences create --file examples/audience-two-datasets.json
intuizi audiences create --file examples/audience-refine-crosspurchase.json
```

### audiences estimate create

Takes everything `audiences create` takes - the same flags, resolved the same
way, the same `--file` body, `--dry-run` - and sends it to Estimate Audience
Size instead, which answers how many devices the audience would hold without
creating it. Nothing appears in Audience Manager, and no export, cohort,
schedule or activation follows. Swapping `estimate create` for `create` in the
same command line builds the audience that was estimated. `--frequency` is
accepted for that reason, and checked as on create, but an estimate never runs
an analysis, so it changes neither the figures nor `recipe_hash`. `--filter` is
part of the recipe: the filters change both, so estimate with the ones the
audience will be built with.

```bash
intuizi audiences estimate create \
  --type poi --brand starbucks \
  --country USA --state CA --city "San Francisco" \
  --start-date 2026-09-02 --end-date 2026-09-09 \
  --name "Starbucks visitors - SF - 1 week" --wait

intuizi audiences estimate create --file examples/audience-two-datasets.json --wait
```

An estimate runs the same build a create would, so it takes about as long,
scans the same data, and counts toward the monthly data-scan limit (as the
`estimate` operation in `usage`) and the organization's build budget. It
comes back `pending` with no figures. Its status is a word, not an id:
`pending`, then `processing`, then one of `completed`, `blocked` or `failed`.
`--wait` follows it there as it follows a build (see
[The async model](#the-async-model)), and `audiences estimate show <id>`
reads it, with `--wait` to resume a wait that timed out.

`completed` carries the figures, which lead the output: `uniques`, the
approximate distinct device count, with a standard error of about 2.3%;
`visits`, `signals` and `unique_eips` where the dataset types produce them;
then `as_of`, `method`, `providers` and the rest. `blocked` means the request
cannot be answered as sent - most often dates outside the dataset's data
coverage - and shows its code and reason as `blocked`; `failed` explains
itself in `status_message`. Both are final and end a wait with exit 1.

`recipe_hash` is computed from the `operator` and `datasets` only, so renaming
does not change it. An audience created from the same body carries the same
`recipe_hash` on `audiences show <id> --json`, which is the proof it is the
audience that was estimated. The request carries an Idempotency-Key like a
create: a retry after a 429 reuses it, and when an estimate gets no response
at all, rerunning it with the `--idempotency-key` stderr printed returns the
estimate the first attempt started, if it did, rather than scanning twice.

```bash
$ intuizi audiences estimate show 12
id                  12
name                Starbucks visitors - SF - 1 week
status              completed
uniques             482311
visits              1203440
...
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
| `--contrast-audience-id` | Completed, non-lookalike audience other than the seed | `audiences list` |
| `--notify` | boolean, default true, always sent | - |
| `--file` | the whole payload as JSON, or `-` for stdin | for fields the flags do not model |
| `--dry-run` | - | - |

The seed for `--source-audience-id` must be Completed, must not itself be a
lookalike, and must hold at least 1,000 devices.

Checked before anything is sent: `--name` is not blank, the two audience ids
are positive and differ, `--target-size` is between 1 and 4,000,000, and no
`--signal`, `--country` or `--state` is blank. A repeated `--signal` is sent
once.
`--file` takes the whole body instead, forwarded untouched, for anything the
flags do not model.

`--signal` takes `poi`, `apps`, `demographics`, `transactions` or
`profile_attributes`. `web` and `ctv` were withdrawn and are rejected.

When the run completes, the API emails the user who created it. `--notify` is
on by default and always sent as `notification`, so `--notify=false` is what
turns the email off.

The Lookalike commands, `lookalike create` and `lookalike cancel` alike,
require additional permissions which need to be approved by your Account
Manager; a 403 means they are not enabled for the account.

Training shows as status `108` Modeling, which is not terminal;
`audiences show <id> --wait` follows the new audience through it to Completed.

`audiences lookalike cancel <id>` takes the id `lookalike create` returned, not
the seed's. The run stops at its next checkpoint, and until then the audience
reads `108` Modeling. Once the run stops it ends at `400` Error, which is
final: it never reaches Completed. `show <id> --wait` follows a cancelled run
to that status and exits `1`, as it does for any failed build, and a webhook
receiver gets `audience.failed`. The API returns only `400` Error, and
Audience Manager in the Intuizi console shows the status as
`Cancelled on request.` A cancel that arrives once the result is already being
published is ignored, and the run completes. A run that has already finished
cannot be cancelled. A run that stopped on a cancel before cancelled runs
ended at `400` still reads `108` Modeling, so a wait on it ends only at
`--timeout`. Remove a cancelled run with `audiences delete <id>`.

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
| `--datastream` | datastream id, repeatable or comma-separated | `reference common datastreams` |
| `--description` | any string | - |
| `--project-id` | project id | `projects list` |
| `--freq-min` `--freq-max` | a range of distinct days, both inclusive, both or neither | `activations preview` |
| `--filter-hash` | the `filter_hash` a preview printed for that range | `activations preview` |
| `--dry-run` | - | - |
| `--wait` `--timeout` | - | - |

An activation costs money, so `--dry-run` is worth a habit here: it prints the
body the flags produce, sends nothing, and needs no token. Not with `--wait` or
`--file`.

Pricing models are per partner: `reference common pricing-models --partner-id
<id>`. Endpoint partners come from `reference common endpoint-partners`, their
datastreams from `reference common datastreams --partner-id <id>`. The partner
of a connection is its `partner.id` in `reference common endpoint-connections
--json`.

A datastream is one of the partner's delivery outputs, and only an enabled one
uploads anything. Each `--datastream` is sent as `{"id": <id>, "status": true}`
in `datastreams`; a repeated id is sent once, and a zero or negative one is
refused, exit 2. Without `--datastream` the activation still reaches Completed
but delivers nothing, so the flag form prints a one-line warning on stderr,
`--dry-run` included. A `--file` body is sent as written, without the warning.
Per-stream `inputs`, `compression` or `service_account` need `--file`.

With `--wait`, a Completed activation whose record lists no datastreams exits
0 but notes on stderr that nothing was delivered.

`--freq-min` and `--freq-max` export only the devices seen on that range of
distinct days, and need an audience built with a frequency analysis
(`audiences create --frequency`). They are sent with `"freq_limit": true`,
without which the API refuses them. Preview the range first with
[`activations preview`](#activations-preview) and pass the `filter_hash` it
printed as `--filter-hash`: the API recomputes it from the audience as stored
at create time, so an export whose range differs from the preview, or whose
audience was rebuilt since, is refused with a 422 rather than exported. A
lone bound, a hash without its range, and an upside-down range are refused
before anything is sent, exit 2. The activation read echoes both as `filters`
and `filter_hash`.

```bash
intuizi activations create --audience-id 88 \
  --endpoint-connection-id 4 --pricing-model-id 3 --datastream 7 --wait

# only the devices seen on 2 to 5 days, exactly as previewed
intuizi activations create --audience-id 88 \
  --endpoint-connection-id 4 --pricing-model-id 3 --datastream 7 \
  --freq-min 2 --freq-max 5 --filter-hash sha256:4f9d... --wait

# or the whole body, for fields these flags do not model
intuizi activations create --file examples/activation.json --wait
```

### activations preview

| Flag | Takes | Where the value comes from |
| --- | --- | --- |
| `--audience-id` | Completed audience built with a frequency analysis | `audiences list` |
| `--freq-min` | fewest distinct days, inclusive | the `frequency_bounds` of an earlier preview |
| `--freq-max` | most distinct days, inclusive; the upper bound for an open-ended range | the same |

All three are required, and a negative or upside-down range is refused before
anything is sent, exit 2. A read-only dry run of the frequency filter:
nothing is created, exported or billed, so preview as many ranges as needed.
`filtered_count` is the exact number of devices seen on `--freq-min` to
`--freq-max` distinct days in the audience's date window - what Audience
Manager shows as Limit Audience for the same Freq. Range - and the output
leads with it after the audience's name: then the range, the
`frequency_bounds` a range must fall inside, the audience's totals, and the
`filter_hash` to pass to `activations create`.
Under that is the whole histogram: devices per number of distinct days, the
buckets a range sums. `source_count` is the audience total, an approximate
count, and `histogram_total` the exact sum of the histogram, so the two can
differ slightly. `--json` adds `limitations`, which say how each count is
made. `--quiet` does not apply: a preview returns a count, not an id.

The audience must be Completed and built with exactly one frequency analysis:
`audiences create --frequency`, or the matching `analyses` key in a `--file`
body. Any other audience, a day-part analysis, a Lookalike Model or a cohort
included, is refused with a 422. A range outside `frequency_bounds` is a 422
naming the bounds.

```bash
$ intuizi activations preview --audience-id 88 --freq-min 2 --freq-max 5
audience               Coffee Buyers NYC
filtered_count         2000
freq_range             2-5
frequency_bounds       1-5
source_count           5100
histogram_total        5000
filter_hash            sha256:4f9d0c7e1b2a8d3f6e5c4b3a2918f7e6d5c4b3a291807f6e5d4c3b2a19180706
as_of                  2026-08-01 06:12:44
analysis_type          frequency
dataset_type           POI
is_activation_allowed  true
method                 histogram_sum
recency                POI 06/01/2026..07/31/2026

days  devices
1     3000
2     1200
3     500
5     300
```

### cohorts create

Exactly one source: `--file-uri`, `--upload-reference` or `--audience-id`.

| Flag | Takes | Where the value comes from |
| --- | --- | --- |
| `--name` | any string; rejected for an audience source | - |
| `--file-uri` | `s3://bucket/path` or `gs://bucket/path` | - |
| `--upload-reference` | reference from an upload with `--purpose cohort` | `uploads put`, or `uploads reserve` |
| `--audience-id` | Completed audience | `audiences list` |
| `--file-format` | `csv`, `gzip` or `parquet` | - |
| `--identifier-type` | one of nine, below | - |
| `--identifier-column` | column name: letters, digits, `_` and `-` | `cohorts preview` |
| `--metadata-columns` | column name, repeatable | `cohorts preview` |
| `--ip-enrichment` | boolean: also add devices seen on the same IP addresses as the cohort's devices (Enrich by Household) | - |
| `--device-limit` | device cap | - |
| `--project-id` | project id, for a file or upload source; rejected for an audience source, which takes the audience's project | `projects list` |
| `--dry-run` | - | - |

`--identifier-type` takes `eid`, `eid_md5`, `maid`, `ip`, `hem_plaintext`,
`scid`, `hem_md5`, `hem_sha1` or `hem_sha256`.

A `file_uri` ending `.csv`, `.gz` or `.parquet` is read as a single file;
anything else is read as a folder, and whitespace anywhere in it is refused.
The match is case-sensitive, so `Q3.CSV` imports as a folder. The same rule
decides an `--upload-reference` import, by the name the file was uploaded
under: the `uploads put` file name, or `--filename` on `uploads reserve`. The
API keeps the first 100 characters of that name, so a longer one loses its
suffix. Name the file `.csv`, `.gz` or `.parquet` before uploading it:
`cohorts preview` reads an upload whatever its name, so a clean preview does
not show which way it will import.

A regular audience makes at most one cohort, and a Lookalike Model audience
can make several. An audience cohort takes the audience's own name and
project, so `--name` and `--project-id` are rejected alongside `--audience-id`
rather than sent to be ignored.

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

With `--upload-reference`, the organization build budget's `429` and the
monthly data-scan limit's `422` both refuse the create after it has claimed
the reference, so a create refused by either has used the reference up, and
the CLI does not retry that `429`. Upload the file again for a new reference
once there is room. See [Rate limits](#rate-limits).

A create returns as soon as the import is queued; follow it with
`cohorts show <id>` as [The async model](#the-async-model) describes.

Needs `--file`: capping an audience cohort by visit frequency (`freq_limit`
with `freq_min`/`freq_max`), by distance (`distance_limit` with `distance` in
meters) or, for a Lookalike Model audience, by score range (`score_limit` with
`min_score`/`max_score`) instead of a device count, and Match Strictness
(`max_devices_per_ip`, 1 to 5) on an SCID file import.

```bash
cat > freq-capped.json <<'EOF'
{
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
| `--upload-reference` | reference from an upload with `--purpose cohort` | `uploads put`, or `uploads reserve` |
| `--file-format` | `csv` (the default) or `gzip`; the format is not detected, so pass `gzip` for a compressed file | - |

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

Schedules require additional permissions which need to be approved by your
Account Manager. Every `schedules` command needs them, `list` and `show`
included; a 403 means they are not enabled for the account.

| Flag | Takes | Where the value comes from |
| --- | --- | --- |
| `--name` | letters, digits, spaces, `_` and `-`, up to 255 characters | - |
| `--audience-id` | audience to rebuild each cycle | `audiences list` |
| `--project-id` | project id | `projects list` |
| `--start` | `"YYYY-MM-DD HH:MM:SS"`, read in `--timezone`, must be in the future | - |
| `--timezone` | `UTC` or a region-based IANA name such as `America/New_York`; not a legacy or `Etc/` name | - |
| `--frequency` | one of four, case-insensitive | `reference common schedule-frequencies` |
| `--window` | data window id, `1` to `8` | `reference common schedule-windows` |
| `--window-days` | `1` to `365`, `--window 3` Custom only | - |
| `--ending` | `1`, `2` or `3`; default `1` | `reference common schedule-endings` |
| `--after-recurrences` | run count of `1` or more, `--ending 2` only | - |
| `--end-date` | `YYYY-MM-DD`, on or after the `--start` date, `--ending 3` only | - |
| `--dry-run` | - | - |

`--frequency` takes `daily`, `weekly`, `bi-weekly` or `monthly`. `--ending` is
`1` Never, `2` Recurrences or `3` Custom Date.

Each ending rule carries its own field, and a mismatch is rejected before
anything is sent; the API rejects it as well, with a 422. `--start` and
`--timezone` are checked before anything is sent: the zone must be `UTC` or a
region-based IANA name, the start must parse in the layout above, and it must
still be in the future in that zone. The API accepts only current
region-based names and `UTC`, so `US/Eastern`, `GMT` or `Etc/UTC` is refused
before anything is sent. A legacy name filed under a region, such as
`Asia/Calcutta` for `Asia/Kolkata`, passes that check, `--dry-run` included,
and the API rejects it with a 422.

An `--end-date` before the `--start` date, read in `--timezone`, is rejected
before anything is sent, and so is a `--name` over 255 characters. The API
rejects that end date as well, with a 422, so a `--file` body cannot carry
one either.

```bash
intuizi schedules create --name "Weekly coffee refresh" --audience-id 88 \
  --start "2027-09-15 06:00:00" --timezone America/New_York \
  --frequency weekly --window 4
```

Needs `--file`: the nested activation block that re-exports every cycle.

```bash
intuizi schedules create --file examples/schedule.json
```

`schedules deactivate <id>` pauses a schedule, and `schedules activate <id>`
resumes it at the next scheduled time after now. A run the pause spanned is
not backfilled, but it is not used up either: a Recurrences or Custom Date
schedule still makes every run it counted at creation, which carries a Custom
Date schedule past its `--end-date` by about as long as it was paused.
Activating a Fulfilled schedule, one whose ending was met, sets it Active, but
it does not run again; stderr says so when the schedule returned shows every
counted run made. Create a new schedule instead.

### poi submissions create

| Flag | Takes | Where the value comes from |
| --- | --- | --- |
| `--name` | any string | - |
| `--brand-id` | a first-party brand id | `poi brands list` |
| `--file` | a `.csv` or `.txt` of locations | - |
| `--list` | path to a JSON file holding `locations[]` (and `name` and `brand_id` unless the flags give them), or `-` for stdin | - |
| `--upload-reference` | reference from an upload with `--purpose poi_submission` | `uploads put`, or `uploads reserve` |
| `--key` | how each listed location is matched to the brand's existing POIs, below | - |
| `--update` | update the brand's existing POIs that a listed location matches; needs `--key` | - |
| `--remove` | archive the brand's existing POIs that no listed location matches, keeping the matched ones, once over the whole submission; not applied when no location has a value for `--key`; needs `--key` | - |

Exactly one of `--file`, `--list` and `--upload-reference`. `--file` and
`--upload-reference` need `--name` and `--brand-id`; `--list` takes both from
the body unless the flags override them, and `--key`, `--update` and
`--remove` replace the body's `key`, `update` and `remove` the same way, so
`--remove=false` turns off a `"remove": true` in a reused file. A flag that
replaces a different value in the body says so on stderr, as in
`note: --brand-id 12 replaces brand_id 9 from the --list body` or
`note: --remove=false replaces remove true from the --list body`. Only `--list`
reads stdin: `--file -` is refused and points at `--list -`.

`--key` takes `location-id`, `gps-coordinates`, `store-id`, `master-id` or
`external-id`, and the API matches on `gps-coordinates` when it is left out,
or null in a `--list` body. A listed location that matches an existing POI is
never added a second time: `--list ... --update --key store-id` changes the
POIs it matches by store id, and without `--update` they are left as they
are. A location that matches nothing is added as a new POI.

`--remove` keeps the POIs the listed locations match and archives every other
POI the brand had. It is applied once, after the whole submission has been
matched, however many locations it has, so list every location the brand
should keep, in one submission, not the ones to drop. A `--remove` submission
whose locations carry values for `--key` but match no POI archives every POI
the brand had. One that holds no location at all, or in which no location has
a value for `--key` (a `location_id`, `store_id`, `master_id`, or
`external_id`, a blank value counting as none), archives nothing.
`--update` and `--remove` take effect when Intuizi approves the submission.
If an approval is interrupted part way, its locations are still imported but
`--remove` is not applied. Send the submission again to apply it.

With `--key location-id`, a location matches the brand's POI whose id is its
`location_id`, the `id` column of `poi locations list`. A `location_id` must
be the id of one of your POIs, as a whole number (`101` or `101.0`), or the
API rejects the submission. The API checks every row of a `--file` or
`--list` submission, but only the first 64 KB of an `--upload-reference`
file. A location with no `location_id`, or one that is not the id of one of
the brand's POIs, is added as a new POI.

Only the `--upload-reference` form sends an `Idempotency-Key`. The API reads
none on the `--file` and `--list` forms, so there `--idempotency-key` has no
effect and stderr says so.

Submission CSVs need `latitude`, `longitude`, and a country column headed
`country|alpha_2` or `country|alpha_3`. The taxonomy nests segments >
categories > brands. Segments already exist and cannot be created: pick one
with `poi segments list`, create categories under it, and brands under those.

#### The rest of poi

| Command | Flags | Notes |
| --- | --- | --- |
| `poi segments list` | `--search` | the available segments; ids come back as `value` |
| `poi categories list` | `--search` | |
| `poi categories create` | `--name`, `--segment-id` | parent id from `poi segments list` |
| `poi brands list` | `--search` | |
| `poi brands create` | `--name`, `--category-id` | parent id from `poi categories list` |
| `poi locations list` | `--search`, `--brands`, `--countries`, `--geometry`, `--page`, `--per-page` | `--brands` takes your own brand ids, repeatable or comma-separated; `--countries` takes ISO-3 codes such as `USA`, repeatable (locations are stored as ISO-3 even when submitted as alpha-2, so `US` matches nothing); `--geometry` is `polygon` or `coordinates` |
| `poi locations show <id>` | | |
| `poi submissions list` | `--search`, `--sort-by`, `--order` | not paginated; `--sort-by` is `name`, `status`, `created_at` or `updated_at`; `--order` is `asc` or `desc` |
| `poi submissions show <id>` | | where an asynchronous submission is followed |
| `poi submissions delete <id>` | `--yes` | only a `Waiting` submission can be deleted; a new one is `Importing` until its locations have been read |

`--search` on `locations list` matches name, address, city, state, zip, DMA,
external id and placekey, not the store id; on the taxonomy lists and `submissions list` it
matches the name. A value outside a flag's set - `--key`, `--geometry`,
`--sort-by`, `--order`, an empty `--name` or a zero parent id on the creates -
is refused before anything is sent, exit 2.

### uploads

| Command | Flag | Takes |
| --- | --- | --- |
| `reserve` | `--purpose` | `poi_submission` or `cohort` |
| | `--filename` | original filename, which names the stored object; default `upload.csv` |
| | `--content-length` | exact byte size of the PUT |
| | `--content-type` | MIME type, default `text/csv` |
| `put <file>` | `--purpose` | `poi_submission` or `cohort` |
| | `--content-type` | MIME type, default `text/csv` |

`reserve` returns a presigned URL for the caller to PUT to. `put` does both
steps and prints the `upload_reference` alone on stdout; with `--json` it prints
the reservation envelope instead, once the PUT has succeeded, so
`.data[0].upload_reference` is the same value either way.

A reservation expires at its `expires_at`, 15 minutes after it is made. The
PUT and the create that claims the reference both have to happen before then:
a create after it is refused even when the PUT succeeded. A reference is
claimed once, so a create refused after claiming it has used it up; upload the
file again for a new one.

The filename matters twice, and `put` sends the file's own name. A
`poi_submission` name must end `.csv` or `.txt`, or the reservation is
refused. A `cohort` name decides how `cohorts create` imports the file: one
ending `.csv`, `.gz` or `.parquet`, case-sensitive and within its first 100
characters, is read as one file, and anything else as a folder. Rename the
file before uploading it.

```bash
ref=$(intuizi uploads put customers.csv --purpose cohort)
intuizi uploads put customers.csv --purpose cohort --json | jq -r '.data[0].upload_reference'
```

Every header the reservation lists is sent on the PUT; `--content-type`
replaces `Content-Type` alone. An empty file is
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

Most catalogs match `--search` on the label. The demographics `genders`,
`marital-statuses` and `incomes` match the code in the `value` column instead,
not the text label (`--search F`, not `female`, for genders, and `M`, not
`married`, for marital statuses), `apps bundle-ids` matches the app name, and
`poi locations` matches the name or the address.

- **common** - dataset-types, countries, states, cities, dmas, zipcodes,
  operators, languages, signal-providers, endpoint-partners,
  endpoint-connections, pricing-models, datastreams,
  datastream-visualizations, schedule-frequencies, schedule-windows,
  schedule-endings
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

Several catalogs cascade: a child read takes values from the level above to
narrow its list.

Geography requires the parent. `states` needs `--countries`, `dmas` needs
`--countries`, `cities` needs `--states`, and `zipcodes` needs `--cities`.
Their other flags only narrow the list: `dmas` takes `--states` and
`--cities`, `cities` takes `--dmas`, and `zipcodes` takes `--states`, `--dmas`
and `--countries`.

In every other cascade the parent is optional, and leaving it out returns the
catalog unfiltered:

- **poi** - `segments`, then `categories` (`--segments`), then `brands`
  (`--categories`), then `locations` (`--brands`).
- **transactions** - `categories`, then `subcategories` (`--categories`), then
  `brands` (`--categories`, `--subcategories`).
- **profile-attributes** - `categories`, then `keys` (`--category-ids`), then
  `values` (`--category-ids`, `--key`).
- **web** - `iab-categories`, then `iab-subcategories` (`--category-ids`,
  which takes ids or IAB codes, one kind per call: the API reads every value
  as the kind of the first and drops the rest, so a mix is refused before it
  is sent). `domains` narrows by either level (`--category-codes`,
  `--subcategory-codes`). An `iab-categories` row's `value` is the IAB code,
  which `--quiet` prints and `domains --category-codes` takes; a WebDomain
  audience's `iab_category_codes` takes the number in its `id` column instead.

`datastreams` and `datastream-visualizations` are different catalogs.
`datastreams --partner-id <id>` lists a partner's delivery outputs, which an
activation's `--datastream` enables. `datastream-visualizations` lists the
charts an audience can draw as it builds, for the `datastreams` array of an
audience `--file` body; `--dataset-type POI` narrows it to the streams a
dataset of that type accepts.

Table cells show a list of plain values inline, such as a datastream's
`dataset_types`; a list of objects shows as a count, and `--json` has it in
full.

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

Both are global and apply to reads, creates and deletes alike, with these
exceptions:

- `auth`, `version` and `completion` print text and ignore either flag.
- `usage`, `cohorts preview`, `activations preview` and `reference
  profile-attributes recency-limits` return no ids, so they refuse `--quiet`;
  use `--json`.
- `uploads put` prints the bare upload reference, which is also what
  `--quiet` prints; `--json` prints the reservation envelope.
- Deletes, `audiences lookalike cancel`, and `schedules activate` and
  `deactivate` print their confirmation on stderr and nothing on stdout,
  `--quiet` or not; `--json` prints the response envelope.
- `--dry-run` prints the request body as JSON whichever flag is set: `--json`
  adds no envelope, and `--quiet` prints no ids.

`--quiet` prints one scalar per record, so it cannot reach a nested field.
Anything deeper needs `--json` and `jq`.

```bash
id=$(intuizi audiences create --type poi --brand starbucks \
       --country USA --state CA --city "San Francisco" \
       --start-date 2026-09-02 --end-date 2026-09-09 \
       --name "Starbucks - SF" --wait --quiet)

intuizi audiences show "$id" --json | jq '.data[0].normalized_payload'
```

Commentary always goes to stderr: status changes while `--wait` polls, the
"page 1 of N" footer, "no results". A pipe therefore sees only data.

On a failure `--json` still prints what the server sent: the error envelope
goes to stdout, the one-line error goes to stderr as usual, and the exit code
is 1. A script can therefore read a 422's field errors from the same place it
reads a success. The CLI refuses a blank name itself, so this example sends a
name longer than the API's 255 characters to get a 422 back:

```bash
long=$(printf 'x%.0s' {1..256})
out=$(intuizi projects create --name "$long" --json) || jq '.data.errors' <<<"$out"
```

Stdout stays empty, and only the stderr line explains the failure, in four
cases: a body that is not JSON, such as an HTML error page from a proxy; a
failure before anything is sent, such as a usage error (exit 2), a missing
token or an unreadable `--file`; a failed catalog read while
`audiences create` resolves a name or the default providers; and a failed PUT
to the storage host on `uploads put`.

A `--wait` that fails, times out or gives up is different: the record it
prints was read successfully, so stdout carries the success envelope that
record came in, while the exit code is 1. A `--wait` that ends on a `401`,
`403` or `404` prints that refusal's error envelope, like any other request.
See [The async model](#the-async-model).

### Envelope shapes

Every response shares the `status`, `code`, `message`, `data` envelope, but what
sits inside `data` differs by the kind of read. This is the usual reason a `jq`
path returns nothing.

| Read | Path to the records |
| --- | --- |
| `show <id>` | `.data[0]` - an array of one, so the index is required |
| `cohorts show <id>`, `projects show <id>` | `.data` - the record itself, with no array around it |
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

A `--file` body is forwarded as written, and the API validates the fields it
defines. On an audience, a key the dataset type does not support inside a
dataset block or its `location`, or an unknown key inside the top-level
`analyses` block, is rejected with a 422 that names the key, so a mistyped
filter fails the create rather than building something unintended. Top-level
keys are not checked by name: a misspelt top-level block such as
`crosspurchse` is ignored, and the audience builds without it.

`normalized_payload` on an audience is the canonical copy of the `name`,
`operator` and `datasets` it was created from. The audience's `recipe_hash`
(`.data[0].recipe_hash`, beside `normalized_payload` rather than inside it) is
computed from the `operator` and `datasets` only, so the name does not change
it. `normalized_payload` echoes what was sent, so a per-dataset filter, such as the brand or the
country, that is missing from it was not sent:

```bash
intuizi audiences show <id> --json | jq '.data[0].normalized_payload.datasets[0].location'
```

Top-level blocks such as `crossvisitation`, `crosspurchase` and `analyses` are
never part of it, even when they were applied. An implausible `results_count`
is the symptom to watch: a signal provider id outside the account's catalog is
not rejected, and the audience completes with zero devices.

Three things to know about the field:

- It is spelled `normalized_payload`, with underscores.
- It is the request in canonical form, not a guide to what is valid to send.
  A single-dataset audience reads back with `"operator": "Single"`, but
  `Single` is not a value the operators catalog accepts on the way in, so do
  not copy it into a new payload.
- Only an API create sets it. It is `null` for audiences built in Audience
  Manager, for Lookalike Model results, for the audiences a schedule builds
  each cycle, and for audiences created before the field existed.

Building creates from flags avoids most of this: names are resolved against the
catalogs before anything is sent, and the payload is assembled from typed fields
rather than free-form JSON.

## The async model

Creates return immediately and the resource completes in the background.
`audiences create`, `audiences show`, `audiences estimate create`,
`audiences estimate show`, `activations create` and `activations show` take
`--wait` with an optional `--timeout` (default 60m).
Waiting polls every 15 seconds, prints status changes to stderr and the record
the wait ended on to stdout, and exits non-zero on failure.

The record is printed whether the wait succeeded, failed, or was abandoned,
and the exit code alone says which: 0 for Completed, 1 for a failed state, a
failed datastream, a timeout, or three failed reads in a row. When the wait
is abandoned - timed out, or given up after three failed reads - the record is
the last one read, so it shows where the job stood; the job keeps running
server-side, and the error names the `show <id> --wait` command that resumes
the wait. The exit code is the one to branch on.

With `--json` stdout is always the server's envelope, so read the job's status
as `.data[0].status.id` whatever the outcome. A failed wait exits 1 with a
`"status": "success"` envelope on stdout, whose `.status` says nothing about
the job. A Completed or failed record is re-read once the wait ends, tried up
to three times after a success and once after a failure; if that re-read
fails, the envelope the record was last polled in is printed instead, and
stderr says `printing the envelope as last polled: re-reading it failed
(...)`. An abandoned wait makes no request once it ends, so a timed-out
command exits at `--timeout`, and stdout carries the envelope last polled,
which holds the record the error names. With `--quiet` only the id is printed,
whatever the outcome.

No record is printed when the wait ends with none to show: a timeout or three
failed reads before any status was read, a 401, 403 or 404 while polling, or
Ctrl-C. With `--json` a wait that ends on a failed read still prints that
read's error envelope, as any other command does: the 401, 403 or 404, or the
last of the three failed reads.

Audience and activation status ids share one scale, where `104` Completed is
the only success. A wait polls on through `100` Initiating, `101` Processing,
`102` Analyzing, `103` Decryption Requested, `105` DataStreaming, `108`
Modeling and `109` Visualizing data streams. `105` comes before `104`, not
after it. `108` is a Lookalike Model training. `109` is an audience drawing
the data stream visualizations it opted into, before `105` and `104`. Any
other id ends the wait with exit 1: `106` Expired, `107` Additional Info, the
`4xx` errors (a cancelled Lookalike Model ends at `400`), and any id the CLI
does not know.

An estimate's status is a word instead: `pending` and `processing` keep the
wait going, `completed` is the one success, and `blocked` and `failed` end it
with exit 1, naming the blocked code and reason or the `status_message`. Any
other word ends it too. With `--json` read it as `.data[0].status`, and the
figures as `.data[0].estimate`.

`107` Additional Info means the worker could not run the request as given, so
it stopped and nothing follows. The error says the build (or, for an
activation, the export) stopped with Additional Info, and names no
`show <id> --wait` to resume it: waiting again stops on the same status at
once. The API does not return the reason, but Audience Manager in the
Intuizi console shows it on the audience or activation. For an audience it is
most often a date range outside the dataset's data coverage, or a filter the
dataset needs. Fix the request and create it again.

Cohort status ids are their own scale: `1` Uploading, `2` Initiating,
`3` Processing, `4` Completed, `5` Not Available. `4` and `5` are final: `5`
means the import failed, and sends `cohort.failed` to a webhook receiver. No
cohort command takes `--wait`, so re-run `cohorts show <id>` until the status
is `4` or `5`. A cohort that failed before failures were reported as `5` can
still read the error code it failed with, a status outside `1` to `5` that the
table shows as `Unknown`: it has failed too. With `--json` the id is
`.data.status.id`: a cohort read returns the record itself, not an array of
one. After a failed import, fix the cause and create the cohort again. An
`--upload-reference` is used up by the failed create, so upload the file again
for a new one. A regular audience keeps its failed cohort and makes at most
one, so run `cohorts delete <id>` before creating from it again.

Everything else is followed with `show <id>`, or by a webhook registered in the
Console. A build or export that stops on `107` Additional Info or ends in a
`4xx` error sends `audience.failed` or `activation.failed`, the `400` a
cancelled Lookalike Model ends at included, and an import that ends at `5` Not
Available sends `cohort.failed`. An activation that reaches `104` with a
datastream that failed to deliver still sends `activation.completed`: check its
`datastreams[]` on `show <id>`. A webhook is a
notification, not a source of truth: keep a low-frequency `show <id>` poll as
the fallback for a delivery that exhausts its retries.

## Shell completion

A Homebrew install, on macOS or Linux, includes the completion scripts for
bash, zsh and fish. After a binary or npm install, load the script the CLI
generates for your shell:

```bash
# zsh: add to ~/.zshrc
autoload -U compinit && compinit
source <(intuizi completion zsh)

# bash, with the bash-completion package: add to ~/.bashrc
source <(intuizi completion bash)

# fish: run once
mkdir -p ~/.config/fish/completions
intuizi completion fish > ~/.config/fish/completions/intuizi.fish
```

For PowerShell, add this line to your profile (`$PROFILE`):

```powershell
intuizi completion powershell | Out-String | Invoke-Expression
```

Zsh loads no completion, the Homebrew one included, until its completion
system is switched on, which is what the `compinit` line does: if
`intuizi aud<TAB>` does nothing, that line is missing from `~/.zshrc`. To
install a script file instead of loading it at startup,
`intuizi completion <shell> --help` gives the Linux and macOS (Homebrew)
locations.

Completion covers commands, flag names, and the values of `--type`,
`--signal`, `--file-format`, `--identifier-type`, `schedules create
--frequency` and `--purpose`. Other flags that take a fixed set, such as `poi submissions list
--sort-by` and `--order`, `poi locations list --geometry` and `poi submissions
create --key`, complete no values; their help lists them.

## Typical workflows

```bash
# Audience -> activation, ids carried between steps
id=$(intuizi audiences create --type poi --brand starbucks \
       --country USA --start-date 2026-09-02 --end-date 2026-09-09 \
       --name "Starbucks visitors - 1 week" --wait --quiet)

intuizi activations create --audience-id "$id" \
  --endpoint-connection-id 4 --pricing-model-id 3 --datastream 7 --wait

# Size it, build it with the frequency analysis, preview a range, export it
intuizi audiences estimate create --type poi --brand starbucks \
  --country USA --start-date 2026-09-02 --end-date 2026-09-09 \
  --name "Starbucks visitors - 1 week" --wait
id=$(intuizi audiences create --type poi --brand starbucks \
       --country USA --start-date 2026-09-02 --end-date 2026-09-09 \
       --name "Starbucks visitors - 1 week" --frequency --wait --quiet)
hash=$(intuizi activations preview --audience-id "$id" --freq-min 2 --freq-max 5 \
         --json | jq -r '.data[0].filter_hash')
intuizi activations create --audience-id "$id" \
  --endpoint-connection-id 4 --pricing-model-id 3 --datastream 7 \
  --freq-min 2 --freq-max 5 --filter-hash "$hash" --wait

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
