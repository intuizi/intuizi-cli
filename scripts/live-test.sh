#!/usr/bin/env bash
# Live end-to-end test of the CLI against a real console.
#
#   scripts/live-test.sh <base-url> [results-dir]
#
# Uses the token the CLI already holds (auth login, or INTUIZI_API_TOKEN) and
# the binary in $INTUIZI_BIN (default ./bin/intuizi). Every step logs the
# command, its exit code and the first lines of both streams to results.md in
# the results directory; the script never stops on a failure and exits 1 at the
# end if any step failed, so it can gate a scheduled workflow.
#
# It creates and then deletes: a project, two POI audiences (one built from a
# piped --dry-run body), two cohorts, an upload, a schedule and two POI
# submissions, all named cli-live-*. Cleanup runs from an EXIT trap, so an
# interrupted run still removes what it made. Activations are only ever
# --dry-run: a real one delivers to an endpoint and costs money.
#
# Point it at a test console. The audience build is the slow step; set
# LIVE_WAIT_TIMEOUT (default 20m) if the console is slower than that.
set -u

BASE=${1:?usage: live-test.sh <base-url> [results-dir]}
OUT=${2:-live-results}
B=${INTUIZI_BIN:-./bin/intuizi}
WAIT_TIMEOUT=${LIVE_WAIT_TIMEOUT:-20m}

[ -x "$B" ] || { echo "no binary at $B (set INTUIZI_BIN or run make build)" >&2; exit 2; }
command -v jq >/dev/null || { echo "jq is required" >&2; exit 2; }

mkdir -p "$OUT"; R="$OUT/results.md"; : > "$R"
pass=0; fail=0; n=0
declare -a PROJECTS=() AUDIENCES=() COHORTS=() SCHEDULES=() SUBMISSIONS=()

I() { "$B" --base-url "$BASE" "$@"; }

# step <expected-exit|any> <label> <cmd...>; stdout lands in $OUT/<n>.out.
step() {
  local want=$1 label=$2; shift 2; n=$((n+1))
  local so="$OUT/$n.out" se="$OUT/$n.err"
  "$@" >"$so" 2>"$se"; local got=$?
  local mark=PASS; [ "$want" = any ] || [ "$got" = "$want" ] || mark=FAIL
  [ $mark = PASS ] && pass=$((pass+1)) || fail=$((fail+1))
  {
    echo "### $n. $label  [$mark] exit=$got want=$want"
    echo '```'; printf '%q ' "$@"; echo; echo '```'
    [ -s "$so" ] && { echo "stdout:"; echo '```'; head -c 1500 "$so" | head -25; echo '```'; }
    [ -s "$se" ] && { echo "stderr:"; echo '```'; head -c 800 "$se" | head -12; echo '```'; }
    echo
  } >> "$R"
  echo "$mark [$got] $label"
  LAST_OUT=$so; LAST_EXIT=$got
}
first() { head -1 "$LAST_OUT"; }

cleanup() {
  local id
  for id in "${SUBMISSIONS[@]}"; do step 0 "cleanup poi submissions delete $id" I poi submissions delete "$id" --yes; done
  for id in "${SCHEDULES[@]}";   do step 0 "cleanup schedules delete $id"       I schedules delete "$id" --yes; done
  for id in "${COHORTS[@]}";     do step 0 "cleanup cohorts delete $id"         I cohorts delete "$id" --yes; done
  for id in "${AUDIENCES[@]}";   do step 0 "cleanup audiences delete $id"       I audiences delete "$id" --yes; done
  for id in "${PROJECTS[@]}";    do step 0 "cleanup projects delete $id"        I projects delete "$id" --yes; done
  { echo; echo "## Summary: $pass passed, $fail failed, $n steps"; } >> "$R"
  echo "== $pass passed, $fail failed, $n steps; report: $R"
  [ "$fail" -eq 0 ]
}
trap cleanup EXIT

echo "# Live test: $BASE, $(date -u +%FT%TZ), binary $($B version)" >> "$R"; echo >> "$R"

# ------------------------------------------------------------------ auth
step 0 "auth status --verify" I auth status --verify

# ------------------------------------------------------------------ reads
for c in "audiences list" "audiences list --per-page 3" "activations list" "cohorts list" "schedules list" \
         "projects list" "webhooks list" "usage" "poi segments list" "poi categories list" "poi brands list" \
         "poi locations list --per-page 5" "poi submissions list"; do
  # shellcheck disable=SC2086
  step 0 "$c" I $c
  # shellcheck disable=SC2086
  step 0 "$c --json" I $c --json
done
step 0 "audiences list --quiet" I audiences list --quiet
step 0 "usage --month" I usage --month "$(date -u +%Y-%m)"

# Ids the catalogs below need, read from this console rather than assumed.
step 0 "reference poi brands --search starbucks --quiet" I reference poi brands --search starbucks --quiet
SBUX=$(first); SBUX=${SBUX:-1}
step 0 "reference common endpoint-partners --quiet" I reference common endpoint-partners --quiet
PARTNER=$(first); PARTNER=${PARTNER:-1}

# Every reference catalog, --json and --quiet. Lines are eval'd so quoted
# values survive. recency-limits refuses --quiet by design (rows have no id).
while IFS= read -r line; do
  [ -z "$line" ] && continue
  eval "set -- $line"
  step 0 "reference $*  --json" I reference "$@" --json
  want=0; case "$1 $2" in "profile-attributes recency-limits") want=2;; esac
  step $want "reference $*  --quiet" I reference "$@" --quiet
done <<CATS
common dataset-types
common countries
common states --countries USA
common cities --states CA
common dmas --countries USA
common zipcodes --cities "San Francisco"
common operators
common languages
common signal-providers --data-type POI
common endpoint-partners
common endpoint-connections
common pricing-models --partner-id $PARTNER
common datastreams --partner-id $PARTNER
common schedule-frequencies
common schedule-windows
common schedule-endings
poi segments
poi categories
poi brands --search starbucks
poi locations --brands $SBUX --per-page 5
apps categories
apps tags
apps os
apps bundle-ids --per-page 5
apps taxonomies
web iab-categories
web iab-subcategories
web domains --search google --per-page 5
web ref-domains --search google --per-page 5
web browsers
web device-types
web device-makes
web device-oses
ctv vendors
ctv content-types
ctv content-genres
ctv channel-names --search cnn
ctv device-types
ctv device-makes
ctv device-oses
ctv connection-types
ctv isps --per-page 5
ctv series --per-page 5
transactions categories
transactions subcategories
transactions brands --per-page 5
transactions incomes
transactions ages
transactions genders
transactions ethnicities
demographics genders
demographics ages
demographics marital-statuses
demographics incomes
profile-attributes categories
profile-attributes keys
profile-attributes values --per-page 5
profile-attributes recency-limits
deidentified fields
cohorts list
CATS

# ------------------------------------------------------------------ error paths
step 1 "404 -> exit 1" I audiences show 999999999
step 1 "404 --json prints the envelope, exit 1" I audiences show 999999999 --json
step 1 "bad token -> 401 exit 1" env INTUIZI_API_TOKEN=not-a-real-token "$B" --base-url "$BASE" audiences list
step 2 "unknown flag -> exit 2" I audiences list --nope
step 2 "unknown subcommand -> exit 2" I reference common state
step 2 "missing required flag -> exit 2" I reference common states

# ------------------------------------------------------------------ create / delete
STAMP=$(date -u +%Y%m%d-%H%M%S)
step 0 "projects create" I projects create --name "cli-live-test $STAMP" --quiet
PROJECT=$(first); [ -n "$PROJECT" ] && PROJECTS+=("$PROJECT")
step 0 "projects show" I projects show "$PROJECT"

# One brand, one city, one day: a small scan.
START=$(date -u -v-10d +%F 2>/dev/null || date -u -d '10 days ago' +%F)
END=$(date -u -v-9d +%F 2>/dev/null || date -u -d '9 days ago' +%F)
[ -n "$START" ] && [ -n "$END" ] || { echo "date arithmetic failed" >&2; exit 2; }
AUDFLAGS=(--type poi --brand "$SBUX" --country USA --state CA --city "San Francisco" --start-date "$START" --end-date "$END")
step 0 "audiences create --dry-run" I audiences create "${AUDFLAGS[@]}" --name "cli-live-test $STAMP" --dry-run
step 0 "audiences create --wait --quiet" I audiences create "${AUDFLAGS[@]}" --name "cli-live-test $STAMP" --wait --timeout "$WAIT_TIMEOUT" --quiet
AUD=$(first); [ -n "$AUD" ] && AUDIENCES+=("$AUD")
step 0 "audiences show" I audiences show "$AUD"
step 0 "audiences show --json" I audiences show "$AUD" --json
step 0 "audiences create --file - (body from --dry-run)" bash -c "'$B' --base-url '$BASE' audiences create --type poi --brand $SBUX --country USA --start-date $START --end-date $END --name 'cli-live-test file $STAMP' --dry-run | '$B' --base-url '$BASE' audiences create --file - --quiet"
AUD2=$(first); [ -n "$AUD2" ] && AUDIENCES+=("$AUD2")
step any "audiences lookalike create --dry-run (capability may be off)" I audiences lookalike create --name "cli-live-lal $STAMP" --source-audience-id "$AUD" --target-size 10000 --signal poi --country USA --dry-run

step 0 "cohorts create --audience-id" I cohorts create --audience-id "$AUD" --device-limit 100 --quiet
COH=$(first); [ -n "$COH" ] && COHORTS+=("$COH")
step 0 "cohorts show" I cohorts show "$COH"

CSV="$OUT/members.csv"
printf 'email_sha256,tier\n%s,gold\n%s,silver\n' "$(printf a@example.com | sha256sum | cut -d' ' -f1)" "$(printf b@example.com | sha256sum | cut -d' ' -f1)" > "$CSV"
step 0 "uploads reserve" I uploads reserve --purpose cohort --filename members.csv --content-length "$(wc -c < "$CSV" | tr -d ' ')" --json
step 0 "uploads put" I uploads put "$CSV" --purpose cohort
REF=$(first)
step 0 "cohorts preview --upload-reference" I cohorts preview --upload-reference "$REF"
step 0 "cohorts preview --json" I cohorts preview --upload-reference "$REF" --json
step 0 "cohorts create --upload-reference" I cohorts create --name "cli-live-upload $STAMP" --upload-reference "$REF" --file-format csv --identifier-type hem_sha256 --identifier-column email_sha256 --metadata-columns tier --quiet
COH2=$(first); [ -n "$COH2" ] && COHORTS+=("$COH2")

SCHED_START=$(date -u -v+2d '+%F 06:00:00' 2>/dev/null || date -u -d '+2 days' '+%F 06:00:00')
step 0 "schedules create --dry-run" I schedules create --name "cli-live-sched $STAMP" --audience-id "$AUD" --start "$SCHED_START" --timezone America/New_York --frequency weekly --window 2 --project-id "$PROJECT" --dry-run
step 0 "schedules create" I schedules create --name "cli-live-sched $STAMP" --audience-id "$AUD" --start "$SCHED_START" --timezone America/New_York --frequency weekly --window 2 --project-id "$PROJECT" --quiet
SCH=$(first); [ -n "$SCH" ] && SCHEDULES+=("$SCH")
step 0 "schedules show" I schedules show "$SCH"
step 0 "schedules deactivate" I schedules deactivate "$SCH"
step 0 "schedules activate" I schedules activate "$SCH"

# Activations: --dry-run only, with ids that exist on this console.
step 0 "reference common endpoint-connections --quiet" I reference common endpoint-connections --quiet
EC=$(first)
step 0 "reference common pricing-models --quiet" I reference common pricing-models --partner-id "$PARTNER" --quiet
PM=$(first)
if [ -n "$EC" ] && [ -n "$PM" ]; then
  step 0 "activations create --dry-run (never sent)" I activations create --audience-id "$AUD" --endpoint-connection-id "$EC" --pricing-model-id "$PM" --project-id "$PROJECT" --dry-run
  step 2 "activations create --dry-run --wait is refused" I activations create --audience-id "$AUD" --endpoint-connection-id "$EC" --pricing-model-id "$PM" --dry-run --wait
fi

# POI submissions against the first first-party brand, by file and by upload.
step 0 "poi brands list --quiet" I poi brands list --quiet
BRAND=$(first)
if [ -n "$BRAND" ]; then
  LOC="$OUT/locations.csv"
  printf 'name,latitude,longitude,country|alpha_3\ncli-live-test %s,37.7749,-122.4194,USA\n' "$STAMP" > "$LOC"
  step 0 "poi submissions create --file" I poi submissions create --name "cli-live-poi $STAMP" --brand-id "$BRAND" --file "$LOC" --quiet
  SUB=$(first); [ -n "$SUB" ] && SUBMISSIONS+=("$SUB")
  step 0 "poi submissions show" I poi submissions show "$SUB"
  step 0 "uploads put --purpose poi_submission" I uploads put "$LOC" --purpose poi_submission
  PREF=$(first)
  step 0 "poi submissions create --upload-reference" I poi submissions create --name "cli-live-poi-upl $STAMP" --brand-id "$BRAND" --upload-reference "$PREF" --quiet
  SUB2=$(first); [ -n "$SUB2" ] && SUBMISSIONS+=("$SUB2")
  # The console can answer 422 for a few seconds after an upload-created
  # submission appears; give it a moment before the cleanup deletes it.
  sleep 10
fi
