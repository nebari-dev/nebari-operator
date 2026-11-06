#!/usr/bin/env bash
# Fails when govulncheck reports a reachable ("called") vulnerability that has a
# fixed version available. Advisories with no fix are printed but do not fail,
# so the gate stays green on findings nobody can act on yet and goes red as soon
# as a fix ships. Same rule as the govulncheck gate in nebari-infrastructure-core.
#
# GOVULNCHECK selects the binary (make vuln passes the pinned one in bin/).
set -euo pipefail

govulncheck="${GOVULNCHECK:-govulncheck}"
if ! command -v "$govulncheck" >/dev/null 2>&1; then
  echo "govulncheck not found; run: make vuln" >&2
  exit 1
fi
if ! command -v jq >/dev/null 2>&1; then
  echo "jq not found in PATH" >&2
  exit 1
fi

tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT

# -format json exits 0 whatever it finds, so the decision is made from the
# parsed findings below.
"$govulncheck" -format json ./... >"$tmp"

# A finding is reachable when its most specific trace frame names a function.
# Group by advisory; a group fails when it is reachable and has a fix.
group='
  [ .[] | select(.finding) | .finding ]
  | group_by(.osv)
  | map({
      osv:    .[0].osv,
      module: .[0].trace[0].module,
      called: (any(.[]; .trace[0].function != null)),
      fixed:  (map(.fixed_version // empty) | first)
    })
'

echo "govulncheck findings (grouped by advisory):"
jq -s -r "$group | .[] | \"  \(.osv)  \(.module)  called=\(.called)  fixed=\(.fixed // \"N/A\")\"" "$tmp"

fixable="$(jq -s "$group | map(select(.called and .fixed))" "$tmp")"
count="$(jq 'length' <<<"$fixable")"

if [[ "$count" -gt 0 ]]; then
  echo "" >&2
  echo "FAIL: ${count} reachable vulnerability(ies) have a fix available; bump these:" >&2
  jq -r '.[] | "  \(.osv)  \(.module) -> \(.fixed)"' <<<"$fixable" >&2
  exit 1
fi

echo ""
echo "OK: no reachable vulnerability with a fix available."
