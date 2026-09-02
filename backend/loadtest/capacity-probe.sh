#!/bin/bash
# ECG Hub — capacity probe
#
# Answers one question: what does a single ECG cost in CPU seconds and RAM?
# Everything else -- cores for 5000/day, headroom at peak -- is arithmetic on
# top of that, and arithmetic carries to another machine where a throughput
# number measured on this one does not.
#
# Reads the backend's own /metrics endpoint. No Prometheus, no Grafana, no
# exporters: the Go client already publishes process_cpu_seconds_total and
# process_resident_memory_bytes, which is exactly what sizing needs.
#
# Usage:
#   METRICS=http://host:9091/metrics ./loadtest/capacity-probe.sh
#   METRICS=... ./loadtest/capacity-probe.sh 'JOBS=8 ./loadtest/ftp-stress.sh /data 2121 host'
#
# Measures the backend process only. Postgres runs in its own container and is
# not counted; on an ingestion-heavy workload it is the smaller half, but size
# the database from its own numbers, not from these.
#
# Run it against an otherwise idle deployment: every CPU second in the window
# is attributed to the ECGs ingested in that window.

set -u
METRICS="${METRICS:-http://localhost:9091/metrics}"
LOAD_CMD="${1:-./loadtest/ftp-stress.sh}"
SAMPLE_FILE=$(mktemp)

scrape() { curl -sS -m 10 "$METRICS" 2>/dev/null; }

# Pull one metric out of a scrape. Sums across label sets, so a metric split by
# volume or vendor totals instead of silently reporting its first series.
value_of() {
  python3 -c "
import re,sys
name = sys.argv[1]
total, seen = 0.0, False
for line in sys.stdin:
    if line.startswith('#'):
        continue
    m = re.match(rf'{re.escape(name)}(\{{[^}}]*\}})? +(\S+)$', line.strip())
    if m:
        total += float(m.group(2)); seen = True
print(f'{total:.6f}' if seen else '')
" "$1"
}

snap=$(scrape)
if [ -z "$snap" ]; then
  echo "ERROR: no response from $METRICS" >&2
  echo "       Check METRICS_ENABLED=true and that the port is reachable." >&2
  exit 1
fi

cpu0=$(printf '%s' "$snap" | value_of process_cpu_seconds_total)
files0=$(printf '%s' "$snap" | value_of storage_files_total)
rss0=$(printf '%s' "$snap" | value_of process_resident_memory_bytes)
cores=$(printf '%s' "$snap" | value_of go_sched_gomaxprocs_threads)

if [ -z "$cpu0" ] || [ -z "$files0" ]; then
  echo "ERROR: $METRICS answered, but without the metrics this needs." >&2
  echo "       Wanted process_cpu_seconds_total and storage_files_total." >&2
  exit 1
fi

echo "=== ECG Hub capacity probe ==="
echo "Metrics:  $METRICS"
echo "Load:     $LOAD_CMD"
echo "Backend:  ${cores%.*} cores visible, $(( ${rss0%.*} / 1048576 )) MB RSS, ${files0%.*} files stored"
echo ""

# Sample RSS through the run: the value left after the queue drains is not the
# value that sized the machine.
( while :; do
    scrape | value_of process_resident_memory_bytes >> "$SAMPLE_FILE"
    sleep 2
  done ) &
sampler=$!
trap 'kill $sampler 2>/dev/null; rm -f "$SAMPLE_FILE"' EXIT

t0=$(date +%s)
eval "$LOAD_CMD"
echo ""
echo "Waiting 30s for the pipeline to drain..."
sleep 30
t1=$(date +%s)

kill $sampler 2>/dev/null; wait $sampler 2>/dev/null

snap=$(scrape)
cpu1=$(printf '%s' "$snap" | value_of process_cpu_seconds_total)
files1=$(printf '%s' "$snap" | value_of storage_files_total)

python3 - "$cpu0" "$cpu1" "$files0" "$files1" "$rss0" "$((t1-t0))" "$SAMPLE_FILE" <<'PY'
import sys
cpu0, cpu1, f0, f1, rss0, window = (float(x) for x in sys.argv[1:7])
samples = [float(l) for l in open(sys.argv[7]) if l.strip()]
cpu, ecgs = cpu1 - cpu0, f1 - f0
peak = max(samples) if samples else rss0

print("=== Results ===")
print(f"Window:          {window:.0f}s")
print(f"Files stored:    +{ecgs:.0f}")
print(f"CPU consumed:    {cpu:.2f} core-seconds")
print(f"Peak RSS:        {peak/1048576:.0f} MB (idle {rss0/1048576:.0f} MB)")

if ecgs < 1:
    print("\nNothing was stored, so there is nothing to extrapolate from.")
    print("Files already ingested are rejected on their content hash -- a second")
    print("run over the same directory measures deduplication, not ingestion.")
    sys.exit(1)

per = cpu / ecgs
print(f"\nCost per ECG:    {per*1000:.0f} ms CPU")
print(f"Rate achieved:   {ecgs/window:.2f} ECG/s")

print("\n=== Extrapolated ===")
print("Clinical load lands in the working day, so an 8h divisor is the honest")
print("average, and x3 covers the burst inside it (morning intake, rounds).")
for daily in (3000, 5000):
    avg = per * daily / (8 * 3600)
    print(f"  {daily}/day:  {per*daily/60:5.1f} core-min/day   {avg:.3f} cores avg   {avg*3:.2f} cores at peak")
print(f"\nRAM: size on the peak above. It is per-process and excludes Postgres,")
print(f"whose working set grows with the table, not with the daily rate.")
PY
