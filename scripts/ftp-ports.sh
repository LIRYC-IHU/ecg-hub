#!/usr/bin/env bash
#
# ECG Hub — make the FTP ports reachable on a host-networking deployment.
#
# Two jobs, both of them things docker-compose used to do and stopped doing the
# day the stack moved to network_mode: host.
#
#   1. Redirect the privileged port 21 to the port the backend actually binds.
#      With a bridge network, dockerd performed that privileged bind itself
#      (the "21:2121" mapping). In the host namespace nothing does: the backend
#      runs as a non-root user under cap_drop: ALL, so it binds 2121 and only
#      2121, and a device dialling 21 reaches nothing.
#
#   2. Open the FTP passive port range in the host firewall, when there is one.
#
# What this script cannot do, and what will still bite you: the passive range
# has to be open on every router and firewall between the device and this host,
# not only here. And it cannot be opened on demand — with TLS on the control
# channel a NAT helper cannot read the "227 Entering Passive Mode" reply, so
# nothing upstream will open the port for you. See docs/deploy-prod.md.
#
# Safe to run twice: it converges on exactly one redirect rule whatever state
# it starts from. That matters — a rule applied twice is the normal outcome of
# doing this by hand, and duplicates are invisible until someone reads the
# chain.
#
#   sudo ./scripts/ftp-ports.sh --check      # report, change nothing
#   sudo ./scripts/ftp-ports.sh              # apply and persist
#   sudo ./scripts/ftp-ports.sh --remove     # undo
#
set -euo pipefail

PUBLIC_PORT=21
TARGET_PORT=2121
PASSIVE_RANGE=30000:30100
ACTION=apply
INSTALL_PERSISTENCE=0

usage() {
	sed -n '2,/^set -euo/p' "$0" | sed 's/^# \{0,1\}//;$d'
	cat <<'USAGE'
Options:
  --check                 Report the current state and exit. Changes nothing.
  --remove                Remove the redirect instead of adding it.
  --public-port <n>       Port devices dial (default 21).
  --target-port <n>       Port the backend binds (default 2121; must match
                          Admin > Modules > FTP).
  --passive-range <a:b>   Passive range to open in the firewall
                          (default 30000:30100; must match the module config).
  --install-persistence   Install iptables-persistent when it is missing.
                          Off by default: installing a package on a running
                          server is not something a script should do quietly.
USAGE
}

while [ $# -gt 0 ]; do
	case "$1" in
	--check) ACTION=check ;;
	--remove) ACTION=remove ;;
	--public-port) PUBLIC_PORT="$2"; shift ;;
	--target-port) TARGET_PORT="$2"; shift ;;
	--passive-range) PASSIVE_RANGE="$2"; shift ;;
	--install-persistence) INSTALL_PERSISTENCE=1 ;;
	-h | --help) usage; exit 0 ;;
	*) echo "unknown option: $1" >&2; usage >&2; exit 2 ;;
	esac
	shift
done

die() { printf '\033[31m✗ %s\033[0m\n' "$*" >&2; exit 1; }

# "Status: inactive" contains "active" as a substring, so this compares the
# whole line rather than searching it.
ufw_active() {
	command -v ufw >/dev/null || return 1
	[ "$(ufw status 2>/dev/null | head -1)" = "Status: active" ]
}

# saved_rule_present asks the file the rules are restored from at boot, not the
# live table — the live table is what we just changed, and it says nothing
# about surviving a reboot.
saved_rule_present() {
	local f
	for f in /etc/iptables/rules.v4 /etc/sysconfig/iptables; do
		[ -f "$f" ] || continue
		grep -q -- "--dport ${PUBLIC_PORT} -j REDIRECT --to-ports ${TARGET_PORT}" "$f" && return 0
	done
	return 1
}
say() { printf '\033[36m▶ %s\033[0m\n' "$*"; }
ok() { printf '\033[32m✓ %s\033[0m\n' "$*"; }
warn() { printf '\033[33m! %s\033[0m\n' "$*"; }

[ "$(uname -s)" = "Linux" ] || die "Linux only — this rewrites netfilter rules on the host running the stack."
[ "$(id -u)" -eq 0 ] || die "run me as root (sudo $0 $*)"
command -v iptables >/dev/null || die "iptables not found"

RULE=(-p tcp --dport "$PUBLIC_PORT" -j REDIRECT --to-ports "$TARGET_PORT")
# The printed form iptables -S uses, which is what we count.
RULE_RE="^-A PREROUTING -p tcp -m tcp --dport ${PUBLIC_PORT} -j REDIRECT --to-ports ${TARGET_PORT}$"

count_rules() {
	iptables -t nat -S PREROUTING 2>/dev/null | grep -cE "$RULE_RE" || true
}

report() {
	local n
	n=$(count_rules)
	say "Redirect ${PUBLIC_PORT} → ${TARGET_PORT}: ${n} rule(s) in nat PREROUTING"
	case "$n" in
	0) warn "  nothing listening on ${PUBLIC_PORT} — a device dialling it reaches nothing" ;;
	1) ok "  exactly one, as it should be" ;;
	*) warn "  duplicated — harmless, but a sign it was applied by hand more than once" ;;
	esac

	if ss -tlnH "sport = :${TARGET_PORT}" 2>/dev/null | grep -q .; then
		ok "Backend is listening on ${TARGET_PORT}"
	else
		warn "Nothing is listening on ${TARGET_PORT} — is the FTP module started?"
	fi

	if ufw_active; then
		if ufw status 2>/dev/null | grep -q "${PASSIVE_RANGE%:*}:${PASSIVE_RANGE#*:}/tcp"; then
			ok "Passive range ${PASSIVE_RANGE} allowed in ufw"
		else
			warn "Passive range ${PASSIVE_RANGE} not allowed in ufw"
		fi
	else
		say "ufw inactive or absent — nothing to open on this host"
	fi

	if saved_rule_present; then
		ok "Redirect is saved to /etc/iptables/rules.v4 — it survives a reboot"
	else
		warn "Redirect is NOT persisted — it disappears at the next reboot, and"
		warn "  nothing will say so: the backend keeps listening on ${TARGET_PORT}"
	fi

	warn "Upstream is not checked and cannot be: every router and firewall between"
	warn "  the devices and this host must forward ${PUBLIC_PORT} and ${PASSIVE_RANGE}."
}

persist() {
	if command -v netfilter-persistent >/dev/null; then
		netfilter-persistent save >/dev/null
		ok "Saved with netfilter-persistent"
		return
	fi
	if [ "$INSTALL_PERSISTENCE" -eq 1 ] && command -v apt-get >/dev/null; then
		say "Installing iptables-persistent"
		# Preseed both prompts: the package asks whether to save current rules,
		# and an unattended install hangs on that dialog otherwise.
		echo "iptables-persistent iptables-persistent/autosave_v4 boolean true" | debconf-set-selections
		echo "iptables-persistent iptables-persistent/autosave_v6 boolean true" | debconf-set-selections
		DEBIAN_FRONTEND=noninteractive apt-get install -y -qq iptables-persistent >/dev/null
		netfilter-persistent save >/dev/null
		ok "Installed and saved with netfilter-persistent"
		return
	fi
	if [ -d /etc/iptables ]; then
		iptables-save >/etc/iptables/rules.v4
		ok "Saved to /etc/iptables/rules.v4"
		return
	fi
	warn "No persistence mechanism found — the rule is live but will not survive a reboot."
	warn "  Re-run with --install-persistence, or save the rules however this host does it."
}

case "$ACTION" in
check)
	report
	;;

remove)
	n=$(count_rules)
	while [ "$n" -gt 0 ]; do
		iptables -t nat -D PREROUTING "${RULE[@]}"
		n=$((n - 1))
	done
	ok "Removed the ${PUBLIC_PORT} → ${TARGET_PORT} redirect"
	persist
	;;

apply)
	# Delete every copy, then add exactly one. Converging beats checking: the
	# starting state may already hold two, and adding "only if absent" would
	# leave them.
	n=$(count_rules)
	if [ "$n" -gt 1 ]; then
		warn "Found ${n} copies of the redirect — collapsing to one"
	fi
	while [ "$n" -gt 0 ]; do
		iptables -t nat -D PREROUTING "${RULE[@]}"
		n=$((n - 1))
	done
	iptables -t nat -A PREROUTING "${RULE[@]}"
	ok "Redirect ${PUBLIC_PORT} → ${TARGET_PORT} in place"

	if ufw_active; then
		ufw allow "${PASSIVE_RANGE}/tcp" >/dev/null
		ok "Passive range ${PASSIVE_RANGE} allowed in ufw"
	fi

	persist
	echo
	report
	;;
esac
