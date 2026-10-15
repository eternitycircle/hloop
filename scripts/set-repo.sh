#!/bin/sh
# Point the install one-liners at your GitHub repo.  Usage: scripts/set-repo.sh <github-user>/<repo>
set -eu
[ $# -eq 1 ] || { echo "usage: $0 <github-user>/<repo>" >&2; exit 1; }
case "$1" in */*) ;; *) echo "expected <github-user>/<repo>" >&2; exit 1;; esac
cd "$(dirname "$0")/.."
for f in README.md docs/*.md scripts/install.sh scripts/install.ps1; do
  [ -f "$f" ] || continue
  tmp="$(mktemp)"; sed "s|OWNER/harness-loop|$1|g; s|OWNER/REPO|$1|g" "$f" > "$tmp" && cat "$tmp" > "$f"; rm -f "$tmp"
done
echo "Updated install scripts and docs to use $1"
