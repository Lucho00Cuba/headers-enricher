#!/usr/bin/env bash
# Generates CHANGELOG.md from git log grouped by tags and commit type.
#
# Usage:
#   ./scripts/changelog.sh              # writes to CHANGELOG.md
#   ./scripts/changelog.sh --dry-run   # prints to stdout only
#   ./scripts/changelog.sh --output=path/to/file.md
#   ./scripts/changelog.sh --extract=v0.1.0  # print only that version's notes
#
# Conventional commit prefixes are grouped into sections:
#   feat                    -> Added
#   fix, perf               -> Fixed
#   refactor                -> Changed
#   security                -> Security
#   docs                    -> Documentation
#   chore, ci, test, build  -> Maintenance
#   anything else           -> Other

set -euo pipefail

REPO="https://github.com/Lucho00Cuba/headers-enricher"
OUTPUT="CHANGELOG.md"
DRY_RUN=false

EXTRACT_TAG=""

for arg in "$@"; do
  case "$arg" in
    --dry-run)    DRY_RUN=true ;;
    --output=*)   OUTPUT="${arg#*=}" ;;
    --extract=*)  EXTRACT_TAG="${arg#*=}" ;;
  esac
done

TMPDIR_WORK="$(mktemp -d)"
trap 'rm -rf "$TMPDIR_WORK"' EXIT

# --------------------------------------------------------------------------- #
# helpers                                                                      #
# --------------------------------------------------------------------------- #

# Map a commit subject to a section name.
section_for() {
  local prefix
  prefix=$(printf '%s' "$1" | sed 's/^\([a-zA-Z]*\).*/\1/')
  case "$prefix" in
    feat)                 printf 'Added' ;;
    fix|perf)             printf 'Fixed' ;;
    refactor)             printf 'Changed' ;;
    security)             printf 'Security' ;;
    docs)                 printf 'Documentation' ;;
    chore|ci|test|build)  printf 'Maintenance' ;;
    *)                    printf 'Other' ;;
  esac
}

# Strip conventional-commit prefix for display.
strip_prefix() {
  printf '%s' "$1" | sed 's/^[a-zA-Z]*([^)]*): //' | sed 's/^[a-zA-Z]*: //'
}

# Write one formatted commit line to a section temp file.
# Uses plain files instead of process substitution to stay bash 3.2 safe.
bucket_commits() {
  local from="$1" to="$2" dir="$3"
  local range
  if [ -z "$from" ]; then range="$to"; else range="${from}..${to}"; fi

  local tmp_log="$dir/log.txt"
  git log --pretty=tformat:"%H %s" "$range" > "$tmp_log" 2>/dev/null || true

  local short hash subject section display link
  while IFS= read -r line; do
    [ -z "$line" ] && continue
    hash="${line%% *}"
    subject="${line#* }"
    short="${hash:0:7}"
    section=$(section_for "$subject")
    display=$(strip_prefix "$subject")
    link="[\`${short}\`](${REPO}/commit/${hash})"
    printf -- '- %s (%s)\n' "$display" "$link" >> "$dir/${section}.txt"
  done < "$tmp_log"
}

# Print all non-empty section files in canonical order.
flush_sections() {
  local dir="$1"
  local order="Added Fixed Changed Security Documentation Maintenance Other"
  local printed=false

  for section in $order; do
    local f="$dir/${section}.txt"
    if [ -f "$f" ] && [ -s "$f" ]; then
      printf '### %s\n\n' "$section"
      cat "$f"
      printf '\n'
      printed=true
    fi
  done

  $printed || printf '_No commits found._\n\n'
}

# --------------------------------------------------------------------------- #
# main                                                                         #
# --------------------------------------------------------------------------- #

generate() {
  printf '# Changelog\n\n'
  printf 'All notable changes to this project will be documented in this file.\n\n'
  printf 'The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),\n'
  printf 'and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).\n\n'

  local tags_raw
  tags_raw=$(git tag --sort=-version:refname 2>/dev/null || true)

  # ── No tags yet ────────────────────────────────────────────────────────── #
  if [ -z "$tags_raw" ]; then
    printf '## [Unreleased]\n\n'
    local d="$TMPDIR_WORK/unreleased"
    mkdir -p "$d"
    bucket_commits "" "HEAD" "$d"
    flush_sections "$d"
    return
  fi

  # newest-first for display, oldest-first for bucketing ranges
  local tags_desc tags_asc latest oldest
  tags_desc=$(printf '%s' "$tags_raw")
  tags_asc=$(printf '%s\n' "$tags_desc" | tail -r 2>/dev/null || printf '%s\n' "$tags_desc" | awk '{lines[NR]=$0} END{for(i=NR;i>=1;i--) print lines[i]}')
  latest=$(printf '%s\n' "$tags_desc" | head -1)
  oldest=$(printf '%s\n' "$tags_asc"  | head -1)

  # ── Unreleased section ─────────────────────────────────────────────────── #
  local unreleased_count
  unreleased_count=$(git log --oneline "${latest}..HEAD" 2>/dev/null | wc -l | tr -d ' ')
  if [ "$unreleased_count" -gt 0 ]; then
    printf '## [Unreleased]\n\n'
    local d="$TMPDIR_WORK/unreleased"
    mkdir -p "$d"
    bucket_commits "$latest" "HEAD" "$d"
    flush_sections "$d"
  fi

  # ── Bucket commits oldest→newest, then print newest→oldest ─────────────── #
  # Pass 1: bucket each tag's commits using correct [older..newer] ranges.
  local prev_tag=""
  while IFS= read -r tag; do
    [ -z "$tag" ] && continue
    local d="$TMPDIR_WORK/$tag"
    mkdir -p "$d"
    bucket_commits "$prev_tag" "$tag" "$d"
    prev_tag="$tag"
  done <<< "$tags_asc"

  # Pass 2: print sections newest→oldest.
  while IFS= read -r tag; do
    [ -z "$tag" ] && continue
    local date
    date=$(git log -1 --format=%as "$tag")
    printf '## [%s] - %s\n\n' "$tag" "$date"
    local d="$TMPDIR_WORK/$tag"
    flush_sections "$d"
  done <<< "$tags_desc"

  # ── Comparison links footer ─────────────────────────────────────────────── #
  printf -- '---\n\n'
  printf '[Unreleased]: %s/compare/%s...HEAD\n' "$REPO" "$latest"

  local prev=""
  while IFS= read -r tag; do
    [ -z "$tag" ] && continue
    if [ -n "$prev" ]; then
      printf '[%s]: %s/compare/%s...%s\n' "$prev" "$REPO" "$tag" "$prev"
    fi
    prev="$tag"
  done <<< "$tags_desc"
  printf '[%s]: %s/releases/tag/%s\n' "$oldest" "$REPO" "$oldest"
}

if [ -n "$EXTRACT_TAG" ]; then
  local_full="$TMPDIR_WORK/full.md"
  generate > "$local_full"
  awk -v tag="$EXTRACT_TAG" '
    /^## \[/ { if (found) exit; if (index($0, "[" tag "]") > 0) { found=1; next } }
    /^---/   { if (found) exit }
    found    { print }
  ' "$local_full"
elif $DRY_RUN; then
  generate
else
  generate > "$OUTPUT"
  printf '✓ %s generated\n' "$OUTPUT"
fi
