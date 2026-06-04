#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat >&2 <<'EOF'
usage: scripts/analyze-query-stats-stacked-vs-clean.sh TSV

Analyzes output from scripts/query-stats-stacked-vs-clean.sh. It derives
stacked/clean ratios and highlights signals that may be useful for deciding
delta vs. full rebuild thresholds.
EOF
}

if [[ $# -ne 1 || "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit $([[ $# -eq 1 ]] && 0 || 2)
fi

input="$1"
if [[ ! -f "$input" ]]; then
  echo "input does not exist: $input" >&2
  exit 2
fi

tmp="$(mktemp /tmp/zoekt-query-analysis.XXXXXX)"
trap 'rm -f "$tmp"' EXIT

awk -F '\t' '
NR == 1 { next }
{
  k=$1
  target=$2
  stacked_shards=$3+0
  clean_shards=$4+0
  stacked_bytes=$5+0
  clean_bytes=$6+0
  stacked_real=$7+0
  clean_real=$23+0
  stacked_content=$10+0
  clean_content=$26+0
  stacked_index_loaded=$11+0
  clean_index_loaded=$27+0
  stacked_files_considered=$13+0
  clean_files_considered=$29+0
  stacked_files_loaded=$14+0
  clean_files_loaded=$30+0
  stacked_shards_scanned=$16+0
  clean_shards_scanned=$32+0
  stacked_shards_skipped_filter=$17+0
  clean_shards_skipped_filter=$33+0
  stacked_matches=$18+0
  clean_matches=$34+0
  stacked_ngram_matches=$19+0
  clean_ngram_matches=$35+0
  stacked_ngram_lookups=$20+0
  clean_ngram_lookups=$36+0
  stacked_construction=$21+0
  clean_construction=$37+0
  stacked_search=$22+0
  clean_search=$38+0

  size_ratio = ratio(stacked_bytes, clean_bytes)
  real_ratio = ratio(stacked_real, clean_real)
  index_loaded_ratio = ratio(stacked_index_loaded, clean_index_loaded)
  files_considered_ratio = ratio(stacked_files_considered, clean_files_considered)
  files_loaded_ratio = ratio(stacked_files_loaded, clean_files_loaded)
  shards_scanned_ratio = ratio(stacked_shards_scanned, clean_shards_scanned)
  ngram_matches_ratio = ratio(stacked_ngram_matches, clean_ngram_matches)
  ngram_lookups_ratio = ratio(stacked_ngram_lookups, clean_ngram_lookups)
  construction_ratio = ratio(stacked_construction, clean_construction)
  search_ratio = ratio(stacked_search, clean_search)
  stale_shard_fraction = stacked_shards > 0 ? stacked_shards_skipped_filter / stacked_shards : 0
  scanned_shard_fraction = stacked_shards > 0 ? stacked_shards_scanned / stacked_shards : 0
  skipped_per_scanned = stacked_shards_scanned > 0 ? stacked_shards_skipped_filter / stacked_shards_scanned : 0

  print k, target, stacked_shards, clean_shards, stacked_bytes, clean_bytes, \
    size_ratio, real_ratio, index_loaded_ratio, files_considered_ratio, \
    files_loaded_ratio, shards_scanned_ratio, ngram_matches_ratio, \
    ngram_lookups_ratio, construction_ratio, search_ratio, \
    stale_shard_fraction, scanned_shard_fraction, skipped_per_scanned, \
    stacked_ngram_lookups, clean_ngram_lookups, stacked_shards_scanned, \
    clean_shards_scanned, stacked_files_considered, clean_files_considered, \
    stacked_content, clean_content, stacked_matches, clean_matches
}
function ratio(a, b) {
  if (b == 0) {
    return a == 0 ? 0 : 999999999
  }
  return a / b
}
' "$input" > "$tmp"

echo "== Summary =="
awk '
{
  n++
  sum_size += $7
  sum_real += $8
  sum_index_loaded += $9
  sum_files_considered += $10
  sum_shards_scanned += $12
  sum_ngram_matches += $13
  sum_ngram_lookups += $14
  sum_construction += $15
  sum_search += $16
  sum_stale_fraction += $17
  if (n == 1 || $7 > max_size) { max_size=$7; max_size_k=$1 }
  if (n == 1 || $8 > max_real) { max_real=$8; max_real_k=$1 }
  if (n == 1 || $14 > max_ngram_lookups) { max_ngram_lookups=$14; max_ngram_lookups_k=$1 }
  if (n == 1 || $15 > max_construction) { max_construction=$15; max_construction_k=$1 }
}
END {
  printf "rows: %d\n", n
  printf "avg_size_ratio: %.3f\n", sum_size/n
  printf "avg_real_ratio: %.3f\n", sum_real/n
  printf "avg_index_loaded_ratio: %.3f\n", sum_index_loaded/n
  printf "avg_files_considered_ratio: %.3f\n", sum_files_considered/n
  printf "avg_shards_scanned_ratio: %.3f\n", sum_shards_scanned/n
  printf "avg_ngram_matches_ratio: %.3f\n", sum_ngram_matches/n
  printf "avg_ngram_lookups_ratio: %.3f\n", sum_ngram_lookups/n
  printf "avg_matchtree_construction_ratio: %.3f\n", sum_construction/n
  printf "avg_matchtree_search_ratio: %.3f\n", sum_search/n
  printf "avg_stale_shard_fraction: %.3f\n", sum_stale_fraction/n
  printf "max_size_ratio: %.3f at k=%s\n", max_size, max_size_k
  printf "max_real_ratio: %.3f at k=%s\n", max_real, max_real_k
  printf "max_ngram_lookup_ratio: %.3f at k=%s\n", max_ngram_lookups, max_ngram_lookups_k
  printf "max_matchtree_construction_ratio: %.3f at k=%s\n", max_construction, max_construction_k
}
' "$tmp"

echo
echo "== Correlations With Query Work Ratios =="
awk '
{
  add("size_ratio", $7, $14)
  add("stacked_shards", $3, $14)
  add("stale_shard_fraction", $17, $14)
  add("scanned_shard_fraction", $18, $14)
  add("skipped_per_scanned", $19, $14)
  add("size_ratio_vs_real", $7, $8)
  add("stacked_shards_vs_real", $3, $8)
}
END {
  print "x_metric\ty_metric\tpearson"
  for (name in n) {
    split(name, parts, SUBSEP)
    x=parts[1]; y=parts[2]
    num=sumxy[name] - sumx[name]*sumy[name]/n[name]
    denx=sumx2[name] - sumx[name]*sumx[name]/n[name]
    deny=sumy2[name] - sumy[name]*sumy[name]/n[name]
    corr=(denx > 0 && deny > 0) ? num / sqrt(denx * deny) : 0
    printf "%s\t%s\t%.3f\n", x, y, corr
  }
}
function add(xname, x, y) {
  key=xname SUBSEP "ngram_lookup_ratio"
  if (xname ~ /_vs_real$/) {
    sub(/_vs_real$/, "", xname)
    key=xname SUBSEP "real_ratio"
  }
  n[key]++
  sumx[key]+=x
  sumy[key]+=y
  sumx2[key]+=x*x
  sumy2[key]+=y*y
  sumxy[key]+=x*y
}
' "$tmp" | sort | column -t -s $'\t'

echo
echo "== Rows With Largest Ngram Lookup Inflation =="
sort -k14,14nr "$tmp" | head -10 | awk '
BEGIN {
  print "k\tsize_ratio\tstacked_shards\tstale_shard_fraction\tshards_scanned\tfiles_considered\tngram_lookup_ratio\treal_ratio"
}
{
  printf "%s\t%.3f\t%s\t%.3f\t%s\t%s\t%.3f\t%.3f\n", $1, $7, $3, $17, $22, $24, $14, $8
}
' | column -t -s $'\t'

echo
echo "== Rows Where Stacked Query Did Extra Candidate Work But Not Extra Content IO =="
awk '$24 > $25 && $26 == $27 {
  printf "%s\t%.3f\t%s\t%s\t%s\t%s\t%.3f\t%.3f\n", $1, $7, $3, $22, $24, $26, $14, $8
}' "$tmp" | {
  echo "k	size_ratio	stacked_shards	shards_scanned	files_considered	content_bytes	ngram_lookup_ratio	real_ratio"
  cat
} | head -15 | column -t -s $'\t'

echo
echo "== Possible Threshold Hints =="
awk '
{
  # These are query-work observations, not proposed defaults.
  if ($14 >= 10 && first_ngram10 == "") first_ngram10=$1
  if ($14 >= 20 && first_ngram20 == "") first_ngram20=$1
  if ($7 >= 1.5 && first_size15 == "") first_size15=$1
  if ($7 >= 2.0 && first_size20 == "") first_size20=$1
  if ($3 >= 20 && first_shards20 == "") first_shards20=$1
}
END {
  printf "first k with ngram_lookup_ratio >= 10x: %s\n", first_ngram10
  printf "first k with ngram_lookup_ratio >= 20x: %s\n", first_ngram20
  printf "first k with size_ratio >= 1.5x: %s\n", first_size15
  printf "first k with size_ratio >= 2.0x: %s\n", first_size20
  printf "first k with stacked_shards >= 20: %s\n", first_shards20
}
' "$tmp"
