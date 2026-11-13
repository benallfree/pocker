#!/usr/bin/env bash
# Prints the PocketBase core changes in a commit range: every path outside what PocketOne owns.
#
#   .github/core-audit/core-diff.sh <base> <head>     # e.g. origin/main HEAD, or <sha>^ <sha>
#   .github/core-audit/core-diff.sh --files <base> <head>
#
# CE-owned (not core): ext/, examples/ce/, docs/, README.md, CLAUDE.md, .github/, .claude/.
# Everything else is PocketBase's own code and needs a core audit (see CLAUDE.md).
set -euo pipefail
files_only=0
if [ "${1:-}" = "--files" ]; then files_only=1; shift; fi
base=${1:?base commit}
head=${2:?head commit}
pathspec=(. ':!ext' ':!examples/ce' ':!docs' ':!README.md' ':!CLAUDE.md' ':!.github' ':!.claude')
if [ "$files_only" = 1 ]; then
  git diff --name-only "$base" "$head" -- "${pathspec[@]}"
else
  git diff "$base" "$head" -- "${pathspec[@]}"
fi
