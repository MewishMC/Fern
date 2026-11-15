#!/usr/bin/env bash
# Reads one version's entry from CHANGELOG.md.
#
#   changelog.sh name  2.1   -> the release name from "## v2.1 - Name" (may be empty)
#   changelog.sh notes 2.1   -> the lines under that heading, up to the next "## "
#
# Exits 2 when CHANGELOG.md has no entry for the version.
set -euo pipefail
mode=${1:?usage: changelog.sh name|notes VERSION}
version=${2:?usage: changelog.sh name|notes VERSION}
file=${CHANGELOG:-CHANGELOG.md}

awk -v ver="${version#v}" -v mode="$mode" '
{ sub(/\r$/, "") }  # CRLF checkouts
/^## / {
    if (found) { done = 1; exit }
    line = $0
    sub(/^## +/, "", line)
    v = line
    sub(/[ \t].*$/, "", v)
    sub(/^v/, "", v)
    if (v == ver) {
        found = 1
        name = line
        sub(/^[^ \t]+[ \t]*/, "", name)
        sub(/^(-|—|–|:)[ \t]*/, "", name)
    }
    next
}
found { lines[++n] = $0 }
END {
    if (!found) exit 2
    if (mode == "name") { print name; exit 0 }
    first = 1; last = n
    while (first <= n && lines[first] ~ /^[ \t]*$/) first++
    while (last >= first && lines[last] ~ /^[ \t]*$/) last--
    for (i = first; i <= last; i++) print lines[i]
}' "$file"
