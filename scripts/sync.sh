#!/usr/bin/env bash
# Copies one rapidoc release (the bundle, its license file, integration.json) into assets/, and the
# conformance fixtures of @rapidoc/embed into testdata/conformance/. Both are committed: `go get` runs no
# build step, so everything the package needs must be in git.
#
#   scripts/sync.sh                  from npm: rapidoc at RAPIDOC_VERSION, @rapidoc/embed latest
#   scripts/sync.sh 12.3.0           from npm, another rapidoc version (also rewrites RAPIDOC_VERSION)
#   scripts/sync.sh --from DIR       from a checkout of the RapiDoc monorepo (after npm run build:rapidoc)
#
# The three asset files must come from the same release: integration.json carries the bundle's SRI hash.
set -euo pipefail

project="$(cd "$(dirname "$0")/.." && pwd)"
files=(rapidoc-min.js rapidoc-min.js.LICENSE.txt integration.json)
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

if [ "${1:-}" = "--from" ]; then
  monorepo="${2:?--from needs the folder of a RapiDoc checkout}"
  dist="$monorepo/packages/rapidoc/dist"
  conformance="$monorepo/packages/rapidoc-embed/conformance"
else
  version="${1:-$(tr -d '[:space:]' < "$project/RAPIDOC_VERSION")}"
  fetch() { # fetch <tarball url> <folder>
    mkdir -p "$2"
    curl --fail --silent --show-error --location "$1" | tar -xz -C "$2"
  }
  fetch "https://registry.npmjs.org/rapidoc/-/rapidoc-$version.tgz" "$work/rapidoc"
  embed="$(curl --fail --silent --show-error https://registry.npmjs.org/@rapidoc/embed/latest \
    | sed -n 's/.*"tarball":"\([^"]*\)".*/\1/p')"
  fetch "$embed" "$work/embed"
  dist="$work/rapidoc/package/dist"
  conformance="$work/embed/package/conformance"
fi

for file in "${files[@]}"; do
  if [ ! -f "$dist/$file" ]; then
    echo "$dist has no $file" >&2
    exit 1
  fi
  cp "$dist/$file" "$project/assets/$file"
done

rm -rf "$project/testdata/conformance"
mkdir -p "$project/testdata/conformance"
cp -R "$conformance/fixtures" "$project/testdata/conformance/fixtures"
cp "$conformance/README.md" "$project/testdata/conformance/README.md"

synced="$(sed -n 's/.*"version": *"\([^"]*\)".*/\1/p' "$project/assets/integration.json" | head -1)"
echo "$synced" > "$project/RAPIDOC_VERSION"
echo "rapidoc $synced in assets/, $(ls "$project/testdata/conformance/fixtures" | wc -l | tr -d ' ') fixtures in testdata/conformance/"
