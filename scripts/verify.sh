#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
export GOTOOLCHAIN=local
if [[ -d vendor ]]; then
  export GOFLAGS="${GOFLAGS:-} -mod=vendor"
else
  export GOFLAGS="${GOFLAGS:-} -mod=readonly"
  go mod verify
fi
[[ -z "$(gofmt -l cmd internal integration)" ]] || { echo 'Go formatting check failed; run make fmt' >&2; exit 1; }
for script in scripts/*.sh; do bash -n "$script"; done
python3 scripts/test_workflow.py
go test ./...
go vet ./...
