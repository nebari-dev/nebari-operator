#!/usr/bin/env bash
#
# Regenerate the machine-owned blocks in AGENTS.md from their source of truth,
# so the doc cannot silently drift from the code it describes.
#
# Currently generates: the make-targets tables, derived from the Makefile's
# `##@ Section` headers and `target: ... ## description` help annotations --
# the same data `make help` prints. Everything OUTSIDE the markers is
# hand-authored and left untouched.
#
# Run via `make agents`. CI runs it and fails if AGENTS.md is out of date,
# the same drift-gate used for `make manifests` / `make docs`.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
AGENTS="${ROOT}/AGENTS.md"
MAKEFILE="${ROOT}/Makefile"

MARKER_BEGIN='<!-- BEGIN GENERATED: make-targets'
MARKER_END='<!-- END GENERATED: make-targets -->'

# Makefile `##@` sections to omit from the doc: the tool-install targets are
# an implementation detail (deps auto-install on first use) and only add noise.
SKIP_SECTIONS="Dependencies"

gen_block() {
  printf '%s (source: Makefile `##@`/`## ` help text -- run `make agents` to refresh) -->\n' "$MARKER_BEGIN"
  printf '\n_Generated from the Makefile; do not edit by hand. Change a target'"'"'s `## ` help comment and run `make agents`._\n'
  awk -v skip="$SKIP_SECTIONS" '
    BEGIN { FS = ":.*## "; show = 0 }
    /^##@/ {
      sect = substr($0, 5); gsub(/[ \t]+$/, "", sect)
      show = 1
      n = split(skip, arr, " ")
      for (i = 1; i <= n; i++) if (arr[i] == sect) show = 0
      if (show) printf "\n### %s\n\n| Target | Description |\n| --- | --- |\n", sect
      next
    }
    /^[a-zA-Z0-9_-]+:.*## / {
      if (show) printf "| `make %s` | %s |\n", $1, $2
    }
  ' "$MAKEFILE"
  printf '\n%s\n' "$MARKER_END"
}

BLOCK="$(gen_block)" python3 - "$AGENTS" "$MARKER_BEGIN" "$MARKER_END" <<'PY'
import os, re, sys

path, begin, end = sys.argv[1], sys.argv[2], sys.argv[3]
src = open(path).read()
block = os.environ["BLOCK"].rstrip("\n")
pat = re.compile(re.escape(begin) + r".*?" + re.escape(end), re.DOTALL)
if not pat.search(src):
    sys.exit(f"gen-agents: markers not found in {path}")
open(path, "w").write(pat.sub(lambda _: block, src))
PY

echo "gen-agents: regenerated make-targets block in ${AGENTS#"$ROOT"/}"
