#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat >&2 <<'EOF'
usage: scripts/delta-admission-sequential.sh -repo WORKTREE -index INDEX_DIR -binary ZOEKT_GIT_INDEX -base COMMIT -max-k N [-log JSONL] [-out TSV]

Runs a sequential delta-admission calibration:
  1. checkout WORKTREE to BASE
  2. delete and recreate INDEX_DIR
  3. full-index BASE with stats-v1
  4. for k=1..N:
       checkout WORKTREE to BASE~k
       run delta stats-v1 against the existing index stack
       append one JSONL admission decision
       append one TSV summary row

The worktree should be disposable.
EOF
}

repo=""
index_dir=""
binary=""
base=""
max_k=""
log_path=""
out_path=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    -repo)
      repo="$2"
      shift 2
      ;;
    -index)
      index_dir="$2"
      shift 2
      ;;
    -binary)
      binary="$2"
      shift 2
      ;;
    -base)
      base="$2"
      shift 2
      ;;
    -max-k)
      max_k="$2"
      shift 2
      ;;
    -log)
      log_path="$2"
      shift 2
      ;;
    -out)
      out_path="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      usage
      echo "unknown argument: $1" >&2
      exit 2
      ;;
  esac
done

if [[ -z "$repo" || -z "$index_dir" || -z "$binary" || -z "$base" || -z "$max_k" ]]; then
  usage
  exit 2
fi

if [[ -z "$log_path" ]]; then
  log_path="$index_dir/delta-admission-sequential.jsonl"
fi
if [[ -z "$out_path" ]]; then
  out_path="$index_dir/delta-admission-sequential.tsv"
fi

if [[ ! -x "$binary" ]]; then
  echo "binary is not executable: $binary" >&2
  exit 2
fi

timed_run() {
  local time_file="$1"
  local stdout_file="$2"
  local stderr_file="$3"
  shift 3

  /usr/bin/time -l bash -c '
    stdout_file="$1"
    stderr_file="$2"
    shift 2
    "$@" >"$stdout_file" 2>"$stderr_file"
  ' bash "$stdout_file" "$stderr_file" "$@" 2>"$time_file"
}

time_first_line_stat() {
  local file="$1"
  local position="$2"
  awk -v position="$position" 'NR == 1 {print $position}' "$file"
}

time_l_stat() {
  local file="$1"
  local label="$2"
  awk -v label="$label" '
    $0 ~ "^[[:space:]]*[0-9]+[[:space:]]+" label "$" {print $1; found=1}
    END {if (!found) print ""}
  ' "$file"
}

git -C "$repo" checkout --detach "$base" >/dev/null 2>&1
git -C "$repo" reset --hard "$base" >/dev/null
rm -rf "$index_dir"
mkdir -p "$index_dir"
mkdir -p "$(dirname "$log_path")" "$(dirname "$out_path")"
rm -f "$log_path" "$out_path"

full_time_file="$(mktemp /tmp/zoekt-seq-full-time.XXXXXX)"
delta_time_file="$(mktemp /tmp/zoekt-seq-delta-time.XXXXXX)"
trap 'rm -f "$full_time_file" "$delta_time_file"' EXIT

timed_run "$full_time_file" /tmp/zoekt-seq-full.stdout /tmp/zoekt-seq-full.stderr \
  "$binary" \
    -index "$index_dir" \
    -branches HEAD \
    -submodules=false \
    -disable_ctags \
    -delta_admission_mode stats-v1 \
    "$repo"

full_real="$(time_first_line_stat "$full_time_file" 1)"
full_user="$(time_first_line_stat "$full_time_file" 3)"
full_sys="$(time_first_line_stat "$full_time_file" 5)"
full_max_rss_bytes="$(time_l_stat "$full_time_file" "maximum resident set size")"

printf 'k\ttarget_short\taccepted\treason\tfull_real_s\tfull_user_s\tfull_sys_s\tfull_max_rss_bytes\tdelta_real_s\tdelta_user_s\tdelta_sys_s\tdelta_cpu_pct\tdelta_max_rss_bytes\tdelta_peak_footprint_bytes\tdelta_page_reclaims\tdelta_page_faults\tdelta_swaps\tdelta_block_inputs\tdelta_block_outputs\tdelta_voluntary_context_switches\tdelta_involuntary_context_switches\tdelta_instructions\tdelta_cycles\twrite_bytes_ratio\tphysical_live_ratio\ttombstone_path_ratio\tnext_delta_layer_count\tshard_fanout_ratio\tcandidate_indexed_bytes\tcandidate_document_count\tchanged_or_deleted_paths\tshard_count\tindex_bytes\n' > "$out_path"

for k in $(seq 1 "$max_k"); do
  target="${base}~${k}"
  if ! target_commit="$(git -C "$repo" rev-parse "$target" 2>/dev/null)"; then
    echo "stopping: cannot resolve $target" >&2
    break
  fi

  git -C "$repo" checkout --detach "$target_commit" >/dev/null 2>&1
  git -C "$repo" reset --hard "$target_commit" >/dev/null

  : > "$delta_time_file"
  timed_run "$delta_time_file" /tmp/zoekt-seq-delta.stdout /tmp/zoekt-seq-delta.stderr \
    "$binary" \
      -index "$index_dir" \
      -branches HEAD \
      -submodules=false \
      -disable_ctags \
      -delta \
      -delta_admission_mode stats-v1 \
      -delta_admission_log_json "$log_path" \
      "$repo"

  delta_real="$(time_first_line_stat "$delta_time_file" 1)"
  delta_user="$(time_first_line_stat "$delta_time_file" 3)"
  delta_sys="$(time_first_line_stat "$delta_time_file" 5)"
  delta_max_rss_bytes="$(time_l_stat "$delta_time_file" "maximum resident set size")"
  delta_peak_footprint_bytes="$(time_l_stat "$delta_time_file" "peak memory footprint")"
  delta_page_reclaims="$(time_l_stat "$delta_time_file" "page reclaims")"
  delta_page_faults="$(time_l_stat "$delta_time_file" "page faults")"
  delta_swaps="$(time_l_stat "$delta_time_file" "swaps")"
  delta_block_inputs="$(time_l_stat "$delta_time_file" "block input operations")"
  delta_block_outputs="$(time_l_stat "$delta_time_file" "block output operations")"
  delta_voluntary_context_switches="$(time_l_stat "$delta_time_file" "voluntary context switches")"
  delta_involuntary_context_switches="$(time_l_stat "$delta_time_file" "involuntary context switches")"
  delta_instructions="$(time_l_stat "$delta_time_file" "instructions retired")"
  delta_cycles="$(time_l_stat "$delta_time_file" "cycles elapsed")"
  delta_cpu_pct="$(awk -v user="$delta_user" -v sys="$delta_sys" -v real="$delta_real" 'BEGIN {
    if (real > 0) {
      printf "%.2f", 100 * (user + sys) / real
    }
  }')"

  shards="$(find "$index_dir" -name '*.zoekt' -type f | wc -l | tr -d ' ')"
  index_bytes="$(du -sk "$index_dir" | awk '{print $1 * 1024}')"
  decision="$(tail -n 1 "$log_path")"

  row="$(jq -r \
    --arg target "$target_commit" \
    --arg full_real "$full_real" \
    --arg full_user "$full_user" \
    --arg full_sys "$full_sys" \
    --arg full_max_rss_bytes "$full_max_rss_bytes" \
    --arg delta_real "$delta_real" \
    --arg delta_user "$delta_user" \
    --arg delta_sys "$delta_sys" \
    --arg delta_cpu_pct "$delta_cpu_pct" \
    --arg delta_max_rss_bytes "$delta_max_rss_bytes" \
    --arg delta_peak_footprint_bytes "$delta_peak_footprint_bytes" \
    --arg delta_page_reclaims "$delta_page_reclaims" \
    --arg delta_page_faults "$delta_page_faults" \
    --arg delta_swaps "$delta_swaps" \
    --arg delta_block_inputs "$delta_block_inputs" \
    --arg delta_block_outputs "$delta_block_outputs" \
    --arg delta_voluntary_context_switches "$delta_voluntary_context_switches" \
    --arg delta_involuntary_context_switches "$delta_involuntary_context_switches" \
    --arg delta_instructions "$delta_instructions" \
    --arg delta_cycles "$delta_cycles" \
    --arg shards "$shards" \
    --arg index_bytes "$index_bytes" \
    '
    [
      $target[0:12],
      (.accepted // null),
      (.reason // ""),
      $full_real,
      $full_user,
      $full_sys,
      $full_max_rss_bytes,
      $delta_real,
      $delta_user,
      $delta_sys,
      $delta_cpu_pct,
      $delta_max_rss_bytes,
      $delta_peak_footprint_bytes,
      $delta_page_reclaims,
      $delta_page_faults,
      $delta_swaps,
      $delta_block_inputs,
      $delta_block_outputs,
      $delta_voluntary_context_switches,
      $delta_involuntary_context_switches,
      $delta_instructions,
      $delta_cycles,
      (.write_bytes_ratio // null),
      (.physical_live_ratio // null),
      (.tombstone_path_ratio // null),
      (.next_delta_layer_count // null),
      (.shard_fanout_ratio // null),
      (.candidate_indexed_bytes // null),
      (.candidate_document_count // null),
      (.changed_or_deleted_paths // null),
      $shards,
      $index_bytes
    ] | @tsv
    ' <<<"$decision")"

  printf '%s\t%s\n' "$k" "$row" | tee -a "$out_path"
done

printf 'wrote %s and %s\n' "$out_path" "$log_path" >&2
