#!/bin/bash
# ECG Hub — FTP/FTPS Ingestion Stress Test
#
# Sends every ECG file found under a directory to the FTP module, stressing the
# whole path: FTP → IngestQueue → Router → Module → Persist (→ object storage).
#
# Usage:
#   ./loadtest/ftp-stress.sh                                  # localhost, TLS on
#   ./loadtest/ftp-stress.sh /path/to/files 2121 ftp.example.org
#   FTP_TLS=0 ./loadtest/ftp-stress.sh                        # cleartext server
#   FTP_INSECURE=1 ./loadtest/ftp-stress.sh                   # self-signed cert (dev)
#   FTP_USER=cex FTP_PASS=cex ./loadtest/ftp-stress.sh
#   JOBS=8 ./loadtest/ftp-stress.sh                           # 8 concurrent uploads
#
# The host defaults to localhost on purpose: a stress test that defaults to a
# production hostname is one forgotten argument away from flooding a hospital.
#
# Prereqs: curl (any build with FTP support — the system one on macOS and Linux
# qualifies). openssl is optional and only used for the certificate preflight.

DIR="${1:-/Users/jonathan.milhas/database}"
PORT="${2:-2121}"
HOST="${3:-localhost}"
FTP_USER="${FTP_USER:-CEX}"
FTP_PASS="${FTP_PASS:-CEX}"

# TLS on by default: the module enforces it in production (MandatoryEncryption),
# so a cleartext run there fails at the banner with "421 TLS is required".
FTP_TLS="${FTP_TLS:-1}"
# Accept a certificate that does not validate. For a self-signed dev pair only —
# leaving it off is what makes a run against a real deployment prove the chain.
FTP_INSECURE="${FTP_INSECURE:-0}"

# Concurrent uploads. One at a time measures the round-trip latency of a single
# transfer, which is not what a capacity test is after: the server never gets
# more than one file to work on. Raise it until throughput stops climbing.
JOBS="${JOBS:-1}"

EXTENSIONS="xml dat dcm DAT"

curl_opts=(-sS --connect-timeout 10 --max-time 300)
scheme_label="FTP (cleartext)"
if [ "$FTP_TLS" = "1" ]; then
  # --ssl-reqd, never --ssl: the latter falls back to cleartext without a word,
  # which turns a failed TLS test into a green one.
  curl_opts+=(--ssl-reqd)
  scheme_label="FTPS (explicit, AUTH TLS)"
  [ "$FTP_INSECURE" = "1" ] && curl_opts+=(--insecure) && scheme_label="$scheme_label, certificate NOT verified"
fi

file_size() {
  # macOS and GNU stat disagree on flags; ask both.
  stat -f%z "$1" 2>/dev/null || stat -c%s "$1" 2>/dev/null || echo 0
}

echo "=== ECG Hub FTP Stress Test ==="
echo "Source:     $DIR"
echo "Target:     $HOST:$PORT (user: $FTP_USER)"
echo "Transport:  $scheme_label"
echo "Extensions: $EXTENSIONS"
echo "Concurrency: $JOBS"
echo ""

# Certificate preflight — fail here, once, rather than after N uploads.
if [ "$FTP_TLS" = "1" ] && command -v openssl >/dev/null 2>&1; then
  cert=$(openssl s_client -connect "$HOST:$PORT" -starttls ftp -servername "$HOST" </dev/null 2>/dev/null \
         | openssl x509 -noout -subject -enddate 2>/dev/null)
  if [ -n "$cert" ]; then
    echo "Certificate presented by the server:"
    echo "$cert" | sed 's/^/  /'
    echo ""
  else
    echo "WARNING: could not negotiate AUTH TLS on $HOST:$PORT."
    echo "         Either the module has TLS off (use FTP_TLS=0), or it is on"
    echo "         without a usable certificate — check the backend logs."
    echo ""
  fi
fi

count=0
errors=0
bytes=0
start=$(date +%s)

# Uploads run in subshells under xargs, so counters cannot live in variables --
# each child would increment its own copy. One result line per file into a temp
# file, tallied once at the end.
results=$(mktemp)
export FTP_USER FTP_PASS HOST PORT
export CURL_OPTS="${curl_opts[*]}"

upload_one() {
  local file="$1" name status
  name=$(basename "$file")
  # shellcheck disable=SC2086 -- CURL_OPTS is a list of flags, word splitting wanted
  if err=$(curl $CURL_OPTS -u "$FTP_USER:$FTP_PASS" -T "$file" \
             "ftp://$HOST:$PORT/$name" 2>&1 >/dev/null); then
    echo "OK $(stat -f%z "$file" 2>/dev/null || stat -c%s "$file" 2>/dev/null || echo 0)"
  else
    status=$?
    # curl's exit code is the useful part: 67 bad credentials, 9 access denied,
    # 28 timeout (a passive port that never opened), 35 TLS handshake, 60
    # certificate not trusted.
    echo "FAIL $status $name ${err//$'\n'/ }"
  fi
}
export -f upload_one

for ext in $EXTENSIONS; do
  find "$DIR" -type f -name "*.$ext" 2>/dev/null
done | xargs -P "$JOBS" -I{} bash -c 'upload_one "$@"' _ {} >> "$results"

# awk for all three: `grep -c` exits 1 on no match, which turns a legitimate
# zero into a shell error, and the usual `|| echo 0` guard then prints twice.
count=$(awk '/^OK/ {n++} END {print n+0}' "$results")
errors=$(awk '/^FAIL/ {n++} END {print n+0}' "$results")
bytes=$(awk '/^OK/ {s+=$2} END {print s+0}' "$results")
awk '/^FAIL/ {code=$2; name=$3; $1=$2=$3=""; sub(/^ +/,""); printf "  FAIL (curl %s) %s - %s\n", code, name, $0}' "$results" | head -20
rm -f "$results"

end=$(date +%s)
duration=$((end - start))

echo ""
echo ""
echo "=== Results ==="
echo "Files sent:  $count"
echo "Errors:      $errors"
echo "Volume:      $((bytes / 1048576)) MB"
echo "Duration:    ${duration}s"
if [ "$duration" -gt 0 ]; then
  echo "Throughput:  $((count / duration)) files/s, $((bytes / 1048576 / duration)) MB/s"
fi
echo ""
echo "Check Grafana: ingest_files_received_total, ingest_pipeline_duration_seconds"
echo "With object storage on, also: storage_spool_files (should return to 0)."
