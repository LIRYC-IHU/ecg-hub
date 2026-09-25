#!/usr/bin/env bash
#
# Send an HL7 message to the inbound ADT listener and show the acknowledgement.
#
#   tools/adt-send.sh a08                  # built-in sample
#   tools/adt-send.sh a40
#   tools/adt-send.sh unknown              # a trigger we do not handle
#   tools/adt-send.sh malformed            # not routable at all
#   tools/adt-send.sh file message.hl7     # your own, segments separated by \r or \n
#   HOST=10.0.0.5 PORT=2576 tools/adt-send.sh a08
#
# Exists because the framing is the part that goes wrong by hand: MLLP wraps the
# message in 0x0B ... 0x1C 0x0D, the segment separator is a carriage return and
# not a newline, and an editor will silently give you the wrong one. It also
# prints the reply, which a bare `nc` leaves you waiting for — the listener keeps
# the connection open for the next message, so nothing closes it for you.
set -euo pipefail

HOST=${HOST:-localhost}
PORT=${PORT:-2576}
FACILITY=${FACILITY:-TESTFACILITY}
PATIENT=${PATIENT:-BS1215}

now=$(date +%Y%m%d%H%M%S)
stamp=$(date +%Y%m%d%H%M%S%z)

msh() { # msh <trigger> <structure> <control-id>
  printf 'MSH|^~\\&|TESTAPP|%s|ECG-HUB|LIRYC|%s||ADT^%s^%s|%s|P|2.5.1' \
    "$FACILITY" "$stamp" "$1" "$2" "$3"
}

case "${1:-a08}" in
  a08)
    # Demographics update. Everything the mappings will read lives in PID.
    msg="$(msh A08 ADT_A01 "TEST-A08-$now")\rEVN|A08|$stamp\r"
    msg+="PID|1||$PATIENT^^^$FACILITY^MR||DUPONT^JEAN^^^^L||19850115|M|||12 RUE DE TEST^^BORDEAUX^^33000^FR||0556000000|||S||NDA-$now\r"
    msg+='PV1|1|O\r'
    ;;
  a40)
    # Merge. PID-3 is the surviving identifier, MRG-1 the one to stop using —
    # the direction is the whole point, and it is easy to read backwards.
    msg="$(msh A40 ADT_A39 "TEST-A40-$now")\rEVN|A40|$stamp\r"
    msg+="PID|1||$PATIENT^^^$FACILITY^MR||DUPONT^JEAN^^^^L||19850115|M\r"
    msg+="MRG|${MERGE_FROM:-MRN-$PATIENT}^^^$FACILITY^MR|||||DUPONT^JEAN\r"
    msg+='PV1|1|O\r'
    ;;
  unknown)
    # A trigger RAD-12 does not define. It must still be acknowledged.
    msg="$(msh A99 ADT_A01 "TEST-A99-$now")\rEVN|A99|$stamp\r"
    msg+="PID|1||$PATIENT\r"
    ;;
  malformed)
    # No routable message type at all — the reply is what matters here.
    msg='MSH|^~\\&|TESTAPP|BROKEN\rPID|1\r'
    ;;
  file)
    [ $# -ge 2 ] || { echo "usage: $0 file <path>" >&2; exit 1; }
    # Accept a file written with either separator: HL7 wants carriage returns,
    # every editor produces newlines.
    msg=$(tr '\n' '\r' < "$2")
    ;;
  *)
    echo "unknown sample: $1 (try a08, a40, unknown, malformed, file <path>)" >&2
    exit 1
    ;;
esac

command -v nc >/dev/null 2>&1 || { echo "nc is not installed" >&2; exit 1; }

echo "→ $HOST:$PORT"
printf '%b' "$msg" | sed 's/\r/\n  /g' | sed '1s/^/  /'
echo

# -w bounds the wait: the listener holds the connection open for the next
# message, so without it nc sits until the read timeout.
reply=$(printf '\x0B%b\x1C\x0D' "$msg" | nc -w "${WAIT:-3}" "$HOST" "$PORT" | tr -d '\013\034' || true)

if [ -z "$reply" ]; then
  echo "← no acknowledgement (is the listener enabled and the port published?)"
  exit 1
fi

echo "←"
printf '%s' "$reply" | tr '\r' '\n' | sed 's/^/  /'

code=$(printf '%s' "$reply" | tr '\r' '\n' | awk -F'|' '/^MSA/{print $2; exit}')
case "$code" in
  AA) echo; echo "AA — accepted" ;;
  AE) echo; echo "AE — understood, not applied" ;;
  AR) echo; echo "AR — refused" ;;
  *)  echo; echo "no MSA code in the reply" ;;
esac
