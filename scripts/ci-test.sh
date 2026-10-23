#!/usr/bin/env bash
set -euo pipefail

# Dependency acquisition belongs to setup steps; tests use the populated cache.
export GOTOOLCHAIN=local GOPROXY=off GOFLAGS=-mod=readonly
go version
[[ "$(go env GOVERSION)" == go1.27.3 ]] || { echo 'Go 1.27.3 is required' >&2; exit 2; }
export pnpm_config_verify_deps_before_run=error
pnpm --dir web run lint
python3 -B scripts/release_contract.py frontend --version "${VERSION:?VERSION is required}" --commit "${COMMIT:?COMMIT is required}"
go test ./...
go test -tags=jsoniter ./...
go vet ./...
go vet -tags=jsoniter ./...
CGO_ENABLED=1 go test -race ./internal/conf ./internal/db/... ./internal/op ./internal/update ./internal/server/handlers
for script in scripts/build.sh scripts/ci-test.sh scripts/release-images.sh; do
    bash -n "$script"
done
python3 -B -m unittest discover -s scripts/tests -p 'test_release_*.py' -v
