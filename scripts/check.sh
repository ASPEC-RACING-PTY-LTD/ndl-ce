#!/usr/bin/env bash
# Full local check, run before every commit and push. GitHub CI only runs a
# quick subset (formatting, vet, types, lint, policy, migrations); the Go and
# UI test suites run here, where the Go test cache and vitest keep them fast.
#
#   bash scripts/check.sh          everything
#   bash scripts/check.sh --quick  skip the Go and UI test suites
set -uo pipefail

cd "$(dirname "$0")/.." || exit 1
quick=false
[ "${1:-}" = "--quick" ] && quick=true

logs="$(mktemp -d)"
trap 'rm -rf "$logs"' EXIT
failed=()

# run NAME CMD... starts a check in the background, logging to its own file.
pids=()
names=()
run() {
  local name=$1
  shift
  ("$@") >"$logs/$name.log" 2>&1 &
  pids+=("$!")
  names+=("$name")
}

policy() {
  node scripts/ci/check-em-dashes.mjs &&
    node scripts/ci/check-bootstrap-size.mjs &&
    node scripts/ci/check-package-structure.mjs &&
    node scripts/ci/check-ci-policy.mjs &&
    node --test scripts/release-version.test.mjs &&
    node scripts/ci/check-openapi-routes.mjs &&
    node scripts/ci/check-ui-copy.mjs
}

go_static() {
  local unformatted
  unformatted="$(gofmt -l cmd internal gen 2>/dev/null)"
  if [ -n "$unformatted" ]; then
    echo "gofmt needed:"
    echo "$unformatted"
    return 1
  fi
  go vet ./...
}

ui_static() {
  cd ui && pnpm -s lint && pnpm -s typecheck
}

run policy policy
run go-vet go_static
run ui-lint ui_static
if ! $quick; then
  run go-test go test ./...
  run ui-test sh -c 'cd ui && pnpm -s test'
fi

for i in "${!pids[@]}"; do
  if wait "${pids[$i]}"; then
    echo "ok    ${names[$i]}"
  else
    echo "FAIL  ${names[$i]}"
    failed+=("${names[$i]}")
  fi
done

for name in "${failed[@]}"; do
  echo
  echo "---- $name ----"
  grep -vE '^(ok|\?) ' "$logs/$name.log" | tail -60
done

[ "${#failed[@]}" -eq 0 ]
