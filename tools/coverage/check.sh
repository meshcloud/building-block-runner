#!/bin/sh
# Coverage threshold checker (PLAN_DETAIL_00_guardrails.md §5.4).
#
# Usage: check.sh <module-dir> <profile-file>
#   <module-dir>   the Go module whose profile this is (e.g. "." when already cd'd into
#                   it, or "tf-block-runner" from the repo root) — used to resolve source
#                   files for `go tool cover -func` and to match thresholds against this
#                   module's own import path.
#   <profile-file> a `go test -coverprofile` profile for that module.
#
# Reads (resolved relative to this script's own location, not the caller's cwd, so it
# works whether invoked from the repo root or from inside a module dir):
#   exclusions.txt  — lines `<import-path>/<file.go>  # justification`; every statement in
#                      a listed file is dropped from the profile before reporting/gating.
#   thresholds.txt  — lines `<import-path-prefix> <min-percent>`; only lines whose prefix
#                      is this module's path (or a subpackage of it) apply here.
#
# Ships with both list files empty, so today this is report-only plumbing (D6): a run
# never fails on thresholds because there are none yet. Phase 1 turns the gate on by
# adding a threshold line — no further change to this script or to CI.
#
# Output: the `go tool cover -func` report on stdout (and appended to
# $GITHUB_STEP_SUMMARY when set, so CI's job summary shows it); exit 0 if every applicable
# threshold is met, 1 otherwise.

set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
EXCLUSIONS="$SCRIPT_DIR/exclusions.txt"
THRESHOLDS="$SCRIPT_DIR/thresholds.txt"

MODULE_DIR=${1:?"usage: check.sh <module-dir> <profile-file>"}
PROFILE=${2:?"usage: check.sh <module-dir> <profile-file>"}

if [ ! -f "$PROFILE" ]; then
  echo "check.sh: coverage profile not found: $PROFILE" >&2
  exit 1
fi
if [ ! -f "$MODULE_DIR/go.mod" ]; then
  echo "check.sh: no go.mod under module dir: $MODULE_DIR" >&2
  exit 1
fi

MODULE_PATH=$(awk '/^module[ \t]/ { print $2; exit }' "$MODULE_DIR/go.mod")

FILTERED=$(mktemp)
trap 'rm -f "$FILTERED"' EXIT

# Drop every statement line whose source file is on the exclusion list (D6 adapter
# exclusions); the `mode:` header line is passed through untouched.
awk -v exfile="$EXCLUSIONS" '
  BEGIN {
    while ((getline line < exfile) > 0) {
      sub(/#.*/, "", line)
      gsub(/[ \t]+$/, "", line)
      if (line != "") excluded[line] = 1
    }
    close(exfile)
  }
  NR == 1 { print; next }
  {
    file = $1
    sub(/:.*/, "", file)
    if (!(file in excluded)) print
  }
' "$PROFILE" > "$FILTERED"

REPORT=$(cd "$MODULE_DIR" && go tool cover -func="$FILTERED")
echo "$REPORT"

if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
  {
    echo "### Coverage — $MODULE_PATH"
    echo '```'
    echo "$REPORT"
    echo '```'
  } >> "$GITHUB_STEP_SUMMARY"
fi

# Weighted statement coverage for one <import-path-prefix>: sum the statement counts
# (go tool cover profile field 2) of every line under that prefix, weighted by whether
# the line was actually hit (field 3 > 0) — NOT an average of per-function percentages,
# which would over-weight small functions.
coverage_for_prefix() {
  awk -v prefix="$1" '
    NR == 1 { next }
    {
      file = $1
      sub(/:.*/, "", file)
      if (file == prefix || index(file, prefix "/") == 1) {
        total += $2
        if ($3 + 0 > 0) covered += $2
      }
    }
    END {
      if (total == 0) { print "NOMATCH"; exit }
      printf "%.1f", (covered / total) * 100
    }
  ' "$FILTERED"
}

STATUS=0
while IFS=' ' read -r prefix min || [ -n "$prefix" ]; do
  case "$prefix" in
    '' | '#'*) continue ;;
  esac
  # Only thresholds that target this module (its own path or one of its subpackages)
  # apply to this invocation — a per-module CI step only holds that module's profile.
  case "$prefix" in
    "$MODULE_PATH" | "$MODULE_PATH"/*) ;;
    *) continue ;;
  esac

  actual=$(coverage_for_prefix "$prefix")
  if [ "$actual" = "NOMATCH" ]; then
    echo "check.sh: FAIL $prefix — threshold set ($min%) but no matching statements in profile (stale path?)" >&2
    STATUS=1
    continue
  fi

  if awk -v a="$actual" -v m="$min" 'BEGIN { exit !(a + 0 < m + 0) }'; then
    echo "check.sh: FAIL $prefix — ${actual}% < ${min}% threshold" >&2
    STATUS=1
  else
    echo "check.sh: PASS $prefix — ${actual}% >= ${min}% threshold"
  fi
done < "$THRESHOLDS"

exit "$STATUS"
