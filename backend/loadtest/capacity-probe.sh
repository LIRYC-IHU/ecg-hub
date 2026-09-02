#!/bin/bash
# ECG Hub — capacity probe
#
# Answers one question: what does a single ECG cost in CPU seconds and RAM?
# Everything else (how many cores for 5000/day, how much headroom at peak) is
# arithmetic on top of that, and arithmetic scales where a throughput number
# measured on one machine does not.
#
# It brackets a load run with two Prometheus reads, so it needs the metrics
# overlay up:
#   docker compose -f docker-compose.yml -f docker-compose.metrics.yml up -d
#
# Usage:
#   ./loadtest/capacity-probe.sh                       # runs ftp-stress.sh
#   ./loadtest/capacity-probe.sh 'JOBS=8 ./loadtest/ftp-stress.sh /data 2121 host'
#   PROM=http://localhost:9090 ./loadtest/capacity-probe.sh
#
# Run it on an otherwise idle deployment: it attributes every CPU second burned
# during the window to the ECGs ingested in that window, so a backup or a
# reindex running alongside inflates the per-ECG cost.

set -u
PROM="${PROM:-http://localhost:9090}"
LOAD_CMD="${1:-./loadtest/ftp-stress.sh}"

q() {
  # Instant query, scalar out. Empty (not zero) when the metric is absent, so a
  # missing exporter is visible instead of silently reading as no load.
  curl -sS --get --data-urlencode "query=$1" "$PROM/api/v1/query" 2>/dev/null \
    | python3 -c "
import json,sys
try:
    r = json.load(sys.stdin)['data']['result']
    print(r[0]['value'][1] if r else '')
except Exception:
    print('')
"
}

need() {
  local v; v=$(q "$1")
  if [ -z "$v" ]; then
    echo "ERROR: metric unavailable at $PROM — is the overlay up and scraping?" >&2
    echo "       query: $1" >&2
    exit 1
  fi
  echo "$v"
}

echo "=== ECG Hub capacity probe ==="
echo "Prometheus: $PROM"
echo "Load:       $LOAD_CMD"
echo ""

CPU_Q='sum(container_cpu_usage_seconds_total{container_label_com_docker_compose_service=~"backend|db"})'
ECG_Q='sum(ingest_files_received_total)'
RSS_Q='sum(container_memory_rss{container_label_com_docker_compose_service=~"backend|db"})'

cpu0=$(need "$CPU_Q"); ecg0=$(need "$ECG_Q"); rss0=$(need "$RSS_Q")
t0=$(date +%s)
echo "Baseline: ${ecg0%.*} files ingested, RSS $(( ${rss0%.*} / 1048576 )) MB"
echo ""

eval "$LOAD_CMD"
rc=$?
echo ""
echo "Load command exited $rc. Waiting 45s for the pipeline to drain and for"
echo "Prometheus to scrape the tail of the run..."
sleep 45

cpu1=$(need "$CPU_Q"); ecg1=$(need "$ECG_Q")
t1=$(date +%s)
# Peak, not final: RSS after the queue drains is not the RSS that sized the box.
rss_peak=$(q "max_over_time(sum(container_memory_rss{container_label_com_docker_compose_service=~\"backend|db\"})[$((t1-t0))s:15s])")

python3 - "$cpu0" "$cpu1" "$ecg0" "$ecg1" "$rss0" "${rss_peak:-0}" "$((t1-t0))" <<'PY'
import sys
cpu0, cpu1, ecg0, ecg1, rss0, rsspeak, window = (float(x) for x in sys.argv[1:8])
cpu, ecgs = cpu1 - cpu0, ecg1 - ecg0

print("=== Results ===")
print(f"Window:            {window:.0f}s")
print(f"ECGs ingested:     {ecgs:.0f}")
print(f"CPU consumed:      {cpu:.1f} core-seconds")
print(f"Peak RSS:          {rsspeak/1048576:.0f} MB (baseline {rss0/1048576:.0f} MB)")

if ecgs < 1:
    print("\nNo ECGs ingested — nothing to extrapolate from.")
    print("Files already stored are rejected as duplicates on their content hash;")
    print("a second run over the same directory measures deduplication, not ingestion.")
    sys.exit(1)

per = cpu / ecgs
print(f"\nCost per ECG:      {per*1000:.0f} ms CPU")
print(f"Sustained rate:    {ecgs/window:.2f} ECG/s over the window")

print("\n=== Extrapolated ===")
for label, daily in (("3000/day", 3000), ("5000/day", 5000)):
    core_s = per * daily
    # Hospital ECGs cluster in the working day; 8h is the honest divisor, and
    # the x3 covers the burst inside it (ward rounds, morning intake).
    avg  = core_s / (8 * 3600)
    print(f"{label:10} {core_s/60:6.1f} core-min/day   avg {avg:.3f} cores   peak x3 {avg*3:.2f} cores")
print(f"\nRAM: size on the peak above, not the average. Add the DB working set,")
print(f"which grows with the table, not with the daily rate.")
PY
