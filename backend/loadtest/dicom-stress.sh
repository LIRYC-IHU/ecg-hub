#!/bin/bash
# ECG Hub — DICOM C-STORE Stress Test
# Sends all .dcm files from the database directory via DICOM C-STORE
# to stress the DICOM SCP ingestion pipeline.
#
# Usage:
#   ./loadtest/dicom-stress.sh
#   ./loadtest/dicom-stress.sh /path/to/files 4242 ECGHUB
#
# Prereqs: storescu (from dcmtk: brew install dcmtk)

DIR="${1:-/Users/jonathan.milhas/database}"
PORT="${2:-4242}"
HOST="${3:-localhost}"
CALLED_AE="${4:-ECG-HUB}"
CALLING_AE="${5:-LOADTEST}"

count=0
errors=0
start=$(date +%s)

echo "=== ECG Hub DICOM C-STORE Stress Test ==="
echo "Source: $DIR"
echo "Target: $HOST:$PORT (AE: $CALLED_AE)"
echo "Calling AE: $CALLING_AE"
echo ""

# First verify connectivity with C-ECHO
echo "Verifying connection (C-ECHO)..."
echoscu -v -aec "$CALLED_AE" -aet "$CALLING_AE" "$HOST" "$PORT" 2>/dev/null
if [ $? -ne 0 ]; then
  echo "ERROR: C-ECHO failed — is the DICOM server running on $HOST:$PORT?"
  exit 1
fi
echo "C-ECHO OK"
echo ""

# Send all .dcm files
while IFS= read -r file; do
  storescu -aec "$CALLED_AE" -aet "$CALLING_AE" "$HOST" "$PORT" "$file" 2>/dev/null
  status=$?
  count=$((count + 1))
  if [ $status -ne 0 ]; then
    errors=$((errors + 1))
    echo "  FAIL: $(basename "$file")"
  else
    printf "\r  Sent: %d files..." "$count"
  fi
done < <(find "$DIR" -type f \( -name "*.dcm" -o -name "*.dicom" \) 2>/dev/null)

end=$(date +%s)
duration=$((end - start))

echo ""
echo ""
echo "=== Results ==="
echo "Files sent:  $count"
echo "Errors:      $errors"
echo "Duration:    ${duration}s"
if [ $duration -gt 0 ]; then
  echo "Throughput:  $((count / duration)) files/s"
fi
echo ""
echo "Check Grafana: dicom_scp_files_received_total, dicom_scp_bytes_received_total"
