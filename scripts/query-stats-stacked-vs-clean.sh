#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat >&2 <<'EOF'
usage: scripts/query-stats-stacked-vs-clean.sh -repo WORKTREE -stacked-index INDEX_DIR -clean-index INDEX_DIR -git-index ZOEKT_GIT_INDEX -zoekt ZOEKT -base COMMIT -max-k N -query QUERY [-out TSV]

Builds a forced stacked-delta index from BASE through BASE~N. At each k it also
builds a clean full index at BASE~k, runs `zoekt -v` on both indexes with QUERY,
and writes side-by-side query stats to TSV.

The worktree should be disposable.
EOF
}

repo=""
stacked_index=""
clean_index=""
git_index=""
zoekt_bin=""
base=""
max_k=""
query=""
out_path=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    -repo) repo="$2"; shift 2 ;;
    -stacked-index) stacked_index="$2"; shift 2 ;;
    -clean-index) clean_index="$2"; shift 2 ;;
    -git-index) git_index="$2"; shift 2 ;;
    -zoekt) zoekt_bin="$2"; shift 2 ;;
    -base) base="$2"; shift 2 ;;
    -max-k) max_k="$2"; shift 2 ;;
    -query) query="$2"; shift 2 ;;
    -out) out_path="$2"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) usage; echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done

if [[ -z "$repo" || -z "$stacked_index" || -z "$clean_index" || -z "$git_index" || -z "$zoekt_bin" || -z "$base" || -z "$max_k" || -z "$query" ]]; then
  usage
  exit 2
fi

if [[ -z "$out_path" ]]; then
  out_path="/tmp/zoekt-query-stacked-vs-clean.tsv"
fi

if [[ ! -x "$git_index" ]]; then
  echo "git index binary is not executable: $git_index" >&2
  exit 2
fi
if [[ ! -x "$zoekt_bin" ]]; then
  echo "zoekt binary is not executable: $zoekt_bin" >&2
  exit 2
fi

stat_field() {
  local file="$1"
  local field="$2"
  sed -nE "s/.*${field}:([0-9]+).*/\\1/p" "$file" | tail -1
}

run_query_stats() {
  local index_dir="$1"
  local prefix="$2"
  local stderr_file="/tmp/zoekt-query-${prefix}.stderr"
  local stdout_file="/tmp/zoekt-query-${prefix}.stdout"
  local time_file="/tmp/zoekt-query-${prefix}.time"

  /usr/bin/time -p "$zoekt_bin" -v -index_dir "$index_dir" "$query" >"$stdout_file" 2>"$stderr_file.time" || true
  # /usr/bin/time -p and zoekt -v share stderr. Split the final time lines from
  # zoekt logs by pattern.
  awk '/^(real|user|sys) / {print > "'"$time_file"'"; next} {print > "'"$stderr_file"'"}' "$stderr_file.time"

  local real user sys
  real="$(awk '/^real / {print $2}' "$time_file")"
  user="$(awk '/^user / {print $2}' "$time_file")"
  sys="$(awk '/^sys / {print $2}' "$time_file")"

  printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s' \
    "$real" \
    "$user" \
    "$sys" \
    "$(stat_field "$stderr_file" ContentBytesLoaded)" \
    "$(stat_field "$stderr_file" IndexBytesLoaded)" \
    "$(stat_field "$stderr_file" FileCount)" \
    "$(stat_field "$stderr_file" FilesConsidered)" \
    "$(stat_field "$stderr_file" FilesLoaded)" \
    "$(stat_field "$stderr_file" FilesSkipped)" \
    "$(stat_field "$stderr_file" ShardsScanned)" \
    "$(stat_field "$stderr_file" ShardsSkippedFilter)" \
    "$(stat_field "$stderr_file" MatchCount)" \
    "$(stat_field "$stderr_file" NgramMatches)" \
    "$(stat_field "$stderr_file" NgramLookups)" \
    "$(stat_field "$stderr_file" MatchTreeConstruction)" \
    "$(stat_field "$stderr_file" MatchTreeSearch)"
}

index_full() {
  local index_dir="$1"
  "$git_index" \
    -index "$index_dir" \
    -branches HEAD \
    -submodules=false \
    -disable_ctags \
    "$repo" >/tmp/zoekt-query-index.stdout 2>/tmp/zoekt-query-index.stderr
}

index_delta() {
  "$git_index" \
    -index "$stacked_index" \
    -branches HEAD \
    -submodules=false \
    -disable_ctags \
    -delta \
    "$repo" >/tmp/zoekt-query-delta.stdout 2>/tmp/zoekt-query-delta.stderr
}

mkdir -p "$(dirname "$out_path")"
rm -rf "$stacked_index" "$clean_index"
mkdir -p "$stacked_index" "$clean_index"

git -C "$repo" checkout --detach "$base" >/dev/null 2>&1
git -C "$repo" reset --hard "$base" >/dev/null
index_full "$stacked_index"

printf 'k\ttarget_short\tstacked_shards\tclean_shards\tstacked_index_bytes\tclean_index_bytes\tstacked_query_real_s\tstacked_query_user_s\tstacked_query_sys_s\tstacked_ContentBytesLoaded\tstacked_IndexBytesLoaded\tstacked_FileCount\tstacked_FilesConsidered\tstacked_FilesLoaded\tstacked_FilesSkipped\tstacked_ShardsScanned\tstacked_ShardsSkippedFilter\tstacked_MatchCount\tstacked_NgramMatches\tstacked_NgramLookups\tstacked_MatchTreeConstruction\tstacked_MatchTreeSearch\tclean_query_real_s\tclean_query_user_s\tclean_query_sys_s\tclean_ContentBytesLoaded\tclean_IndexBytesLoaded\tclean_FileCount\tclean_FilesConsidered\tclean_FilesLoaded\tclean_FilesSkipped\tclean_ShardsScanned\tclean_ShardsSkippedFilter\tclean_MatchCount\tclean_NgramMatches\tclean_NgramLookups\tclean_MatchTreeConstruction\tclean_MatchTreeSearch\n' >"$out_path"

for k in $(seq 1 "$max_k"); do
  target="${base}~${k}"
  if ! target_commit="$(git -C "$repo" rev-parse "$target" 2>/dev/null)"; then
    echo "stopping: cannot resolve $target" >&2
    break
  fi

  git -C "$repo" checkout --detach "$target_commit" >/dev/null 2>&1
  git -C "$repo" reset --hard "$target_commit" >/dev/null

  index_delta

  rm -rf "$clean_index"
  mkdir -p "$clean_index"
  index_full "$clean_index"

  stacked_shards="$(find "$stacked_index" -name '*.zoekt' -type f | wc -l | tr -d ' ')"
  clean_shards="$(find "$clean_index" -name '*.zoekt' -type f | wc -l | tr -d ' ')"
  stacked_bytes="$(du -sk "$stacked_index" | awk '{print $1 * 1024}')"
  clean_bytes="$(du -sk "$clean_index" | awk '{print $1 * 1024}')"

  stacked_stats="$(run_query_stats "$stacked_index" stacked)"
  clean_stats="$(run_query_stats "$clean_index" clean)"

  printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' \
    "$k" \
    "${target_commit:0:12}" \
    "$stacked_shards" \
    "$clean_shards" \
    "$stacked_bytes" \
    "$clean_bytes" \
    "$stacked_stats" \
    "$clean_stats" | tee -a "$out_path"
done

printf 'wrote %s\n' "$out_path" >&2
