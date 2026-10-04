#!/usr/bin/env bash
# Puts one release on npm: the packages npm/build.mjs wrote, the six platform
# packages first and then @intuizi/cli, the launcher that depends on them.
#
#   npm/publish.sh dist/npm
#
# There is no npm token. Each package has a trusted publisher entry on
# npmjs.com naming this repository's release workflow, and the registry
# exchanges the workflow run's OIDC token for the right to publish that one
# package. So this only works inside that workflow, in a job that holds the
# id-token permission.
#
# All seven or nothing: the registry is asked about every package before any is
# uploaded, and a single refusal fails the run with nothing published.
#
# Safe to re-run: a version that is already on the registry is left alone, so a
# run that failed half way finishes the set.
set -euo pipefail

out=${1:?usage: npm/publish.sh <the directory npm/build.mjs wrote, e.g. dist/npm>}

# One field of the package.json in the directory $2.
field() {
  node -p "require(process.argv[1]).$1" "$(realpath "$2")/package.json"
}

version=$(field version "$out/@intuizi/cli")

# Nothing below 0.1.0 reaches the registry. The 0.0.x tags exist to exercise
# the release pipeline, and npm never lets a version number be used twice.
case "$version" in
  0.0.*)
    echo "::warning::$version is below 0.1.0, so the npm packages were built but not published"
    exit 0
    ;;
esac

# A prerelease must not become what a plain `npm install` resolves to.
tag=latest
case "$version" in *-*) tag=next ;; esac

# Whether the registry accepts this run as a trusted publisher of the package
# in the directory $1. npm attempts the OIDC exchange before every publish, a
# dry one included, and a dry run signs and uploads nothing. How the exchange
# went shows only at the verbose log level; the last thing npm said about it is
# left in $exchange, which for a refusal is the registry's reason.
trusted() {
  local log
  log=$(cd "$1" && npm publish --dry-run --tag "$tag" --loglevel verbose 2>&1) || true
  exchange=$(sed -n 's/^npm verb[a-z]* oidc //p' <<<"$log" | tail -n 1)
  grep -q 'oidc Successfully retrieved and set token' <<<"$log"
}

# Ask first. What is already on the registry needs no permission; everything
# else has to be accepted before anything is uploaded. Platform packages come
# first, so the launcher's optionalDependencies resolve the moment
# @intuizi/cli itself appears.
accepted=()
refused=0
for dir in "$out"/@intuizi/cli-* "$out/@intuizi/cli"; do
  name=$(field name "$dir")
  if [ -n "$(npm view "$name@$version" version 2>/dev/null)" ]; then
    echo "$name@$version is already on the registry"
  elif trusted "$dir"; then
    echo "$name@$version: the registry accepts this run as a trusted publisher"
    accepted+=("$dir")
  else
    echo "::error title=npm refused this run as a trusted publisher::$name: ${exchange:-npm attempted no OIDC exchange}"
    refused=$((refused + 1))
  fi
done

if [ "$refused" -gt 0 ]; then
  echo "Nothing was uploaded: $refused of this release's packages cannot be published by this run."
  echo "README.md, \"How npm publishing is authorised\", says what each one needs. Fix it and re-run this job."
  exit 1
fi

[ ${#accepted[@]} -gt 0 ] || exit 0

# npm exchanges the token again for the upload itself. --provenance is what it
# would turn on by itself after the exchange; saying so makes it a requirement.
for dir in "${accepted[@]}"; do
  (cd "$dir" && npm publish --access public --tag "$tag" --provenance)
done
