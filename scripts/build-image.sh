#!/usr/bin/env bash
# Run on the separate build system. This script never pushes an image.
set -euo pipefail
[[ $# -eq 2 ]] || { echo "Usage: $0 IMAGE_REPOSITORY VERSION_TAG" >&2; exit 2; }
repository=$1
version=$2
[[ "$repository" != -* && "$repository" != *[[:space:]]* && -n "$repository" ]] || { echo 'Invalid image repository' >&2; exit 2; }
[[ "$version" =~ ^[a-zA-Z0-9_][a-zA-Z0-9_.-]{0,127}$ ]] || { echo 'Invalid version/tag' >&2; exit 2; }
cd "$(dirname "${BASH_SOURCE[0]}")/.."
git_sha=$(git rev-parse --verify HEAD)
[[ -z "$(git status --porcelain --untracked-files=normal)" ]] || { echo 'Build requires a clean committed checkout (including vendor/ if used)' >&2; exit 1; }
# Commit time is the deterministic default. An explicit CI build time is permitted,
# but changes binary metadata and prevents byte-identical rebuilds across times.
build_timestamp=${BUILD_TIMESTAMP:-$(git show -s --format=%cI HEAD)}
[[ "$build_timestamp" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(Z|[+-][0-9]{2}:[0-9]{2})$ ]] || { echo 'BUILD_TIMESTAMP must be an RFC3339 timestamp without fractional seconds' >&2; exit 2; }
source_date_epoch=$(git show -s --format=%ct HEAD)
image="$repository:$version"
args=(build --build-arg "VERSION=$version" --build-arg "GIT_SHA=$git_sha"
  --build-arg "BUILD_TIMESTAMP=$build_timestamp" --build-arg "SOURCE_DATE_EPOCH=$source_date_epoch" --tag "$image")
if [[ -n ${PLATFORM:-} ]]; then args+=(--platform "$PLATFORM"); fi
docker "${args[@]}" .
printf 'Built %s\nGit SHA: %s\nBuild timestamp: %s\nNo image was pushed.\n' "$image" "$git_sha" "$build_timestamp"
