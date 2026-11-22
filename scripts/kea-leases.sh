#!/usr/bin/env bash

# Copyright 2025 IBM Corp
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#	http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

#==============================================================================
# Script: kea-leases.sh
# Description: Run on the cluster controller.  Lists the DHCPv4 leases that the
#              running Kea server currently holds in memory, together with the
#              server name from the matching host reservation.
#
#              ocp-ipi-powervc configures Kea with a non-persistent memfile
#              lease database ("persist": false), so there is no lease file to
#              read and Kea has no control socket to ask.  Instead the lease
#              table is rebuilt from Kea's own log:
#
#                1. Everything before the last "DHCPSRV_MEMFILE_DB ...
#                   persist=false" line is ignored; Kea logs that line each time
#                   it starts, and the lease table starts out empty then.
#                2. DHCP4_LEASE_ALLOC / DHCP4_LEASE_REUSE lines add or refresh a
#                   lease; the expiry is the log time plus the lease time Kea
#                   logged.
#                3. DHCP4_RELEASE, DHCP4_RELEASE_DELETED and DHCP4_DECLINE_LEASE
#                   lines remove a lease, and a client that is given a new
#                   address loses its previous one.
#                4. Leases whose expiry has passed are dropped.
#
#              The log message formats match Kea 3.0.3 (the version installed
#              on the controller); a Kea that rewords them needs this script
#              updated.
#
#              The server name is the "hostname" of the reservation whose
#              hw-address matches the lease (ocp-ipi-powervc sets it to the
#              OpenStack server name).
#
# Usage: sudo ./kea-leases.sh [OPTIONS]
#
# Options:
#   -c, --config <file>  Kea config file (default: /etc/kea/kea-dhcp4.conf)
#   -l, --log <file>     Kea log file (default: the file logger output in the
#                        config, else /var/log/kea/kea-dhcp4.log); the rotated
#                        <file>.1 is read first when it exists
#   -a, --all            Also list reservations that have no current lease
#   -w, --watch <sec>    Keep running: redraw the table every <sec> seconds
#                        (like watch(1)) until interrupted.  The config and log
#                        are re-read on every pass, so new VMs show up as their
#                        leases are granted.  Without this option, or with 0,
#                        the table is printed once.
#   -h, --help           Show this help and exit
#
# Examples:
#   sudo ./kea-leases.sh
#   sudo ./kea-leases.sh -a -w 5
#
# Output columns:
#   IP  MAC  SERVER  LAST-ACK  EXPIRES
#
# Environment Variables:
#   KEA_CONFIG  - Kea config file (default: /etc/kea/kea-dhcp4.conf)
#   KEA_LOG     - Kea log file
#
# Prerequisites:
#   - gawk and jq must be in PATH
#   - Root privileges (the Kea log is only readable by the kea user)
#   - The Kea config must be strict JSON (no comments)
#   - Kea must log at INFO or finer (the generated config uses DEBUG)
#
# Limitations:
#   - Only as accurate as the log: if the log was deleted or rotated past the
#     last Kea start, leases allocated before that point are missing and a
#     warning is printed.
#   - Time stamps are in the host's local time zone, as logged by Kea.
#
# Exit Codes:
#   0   - Success
#   1   - Missing dependency, unreadable file or bad configuration
#   130 - Interrupted (SIGINT) while watching
#   143 - Terminated (SIGTERM) while watching
#==============================================================================

set -uo pipefail

function log_error() { echo "[ERROR] $*" >&2; }

function usage() {
  sed -n '/^# Usage:/,/^# Prerequisites:/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//; $d'
  exit 0
}

CONFIG="${KEA_CONFIG:-/etc/kea/kea-dhcp4.conf}"
LOG_FILE="${KEA_LOG:-}"
SHOW_ALL=0
WATCH=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    -c|--config)
      [[ $# -lt 2 ]] && { log_error "$1 requires an argument"; exit 1; }
      CONFIG="$2"; shift 2 ;;
    -l|--log)
      [[ $# -lt 2 ]] && { log_error "$1 requires an argument"; exit 1; }
      LOG_FILE="$2"; shift 2 ;;
    -a|--all)
      SHOW_ALL=1; shift ;;
    -w|--watch)
      [[ $# -lt 2 ]] && { log_error "$1 requires an argument"; exit 1; }
      WATCH="$2"; shift 2 ;;
    -h|--help)
      usage ;;
    *)
      log_error "Unknown option: $1"
      exit 1 ;;
  esac
done

if [[ ! "${WATCH}" =~ ^(0|[1-9][0-9]*)$ ]]; then
  log_error "--watch must be a number of seconds, got: ${WATCH}"
  exit 1
fi

for cmd in gawk jq; do
  if ! command -v "${cmd}" >/dev/null 2>&1; then
    log_error "Missing required program: ${cmd}"
    exit 1
  fi
done

if [[ ! -r "${CONFIG}" ]]; then
  log_error "Cannot read ${CONFIG} (run as root?)"
  exit 1
fi

# Default log file: the first output of the "kea-dhcp4" logger.  Kea resolves a
# relative output name against its log directory.
if [[ -z "${LOG_FILE}" ]]; then
  LOG_FILE=$(jq -r '
      [ .Dhcp4.loggers[]? | select(.name == "kea-dhcp4")
        | .["output-options"][]?.output ] | first // empty' "${CONFIG}" 2>/dev/null) || {
    log_error "Failed to parse ${CONFIG} as JSON"
    exit 1
  }
  [[ -z "${LOG_FILE}" ]] && LOG_FILE="kea-dhcp4.log"
  [[ "${LOG_FILE}" != /* ]] && LOG_FILE="/var/log/kea/${LOG_FILE}"
fi

if [[ ! -r "${LOG_FILE}" ]]; then
  log_error "Cannot read ${LOG_FILE} (run as root?)"
  exit 1
fi

# Print the lease table once.  The reservations and the log file list are read
# on every call so that --watch sees changes.
function print_leases() {
  local reservations
  local -a log_files=()

  # The lease table is rebuilt in order, so the rotated log must come first.
  [[ -r "${LOG_FILE}.1" ]] && log_files+=("${LOG_FILE}.1")
  log_files+=("${LOG_FILE}")

  # Reservations as "mac<TAB>ip<TAB>hostname", from every place Kea allows them.
  reservations=$(jq -r '
      [ (.Dhcp4.subnet4[]?.reservations[]?),
        (.Dhcp4["shared-networks"][]?.subnet4[]?.reservations[]?),
        (.Dhcp4.reservations[]?) ]
      | map(select(has("hw-address")))
      | .[] | [ (.["hw-address"] | ascii_downcase),
                (.["ip-address"] // "-"),
                (.hostname // "-") ] | @tsv' "${CONFIG}") || {
    log_error "Failed to parse ${CONFIG} as JSON"
    return 1
  }

  {
    printf 'IP\tMAC\tSERVER\tLAST-ACK\tEXPIRES\n'
    gawk -v show_all="${SHOW_ALL}" '
      BEGIN { FS = "\t"; now = systime() }

      function epoch(ts) {
        return mktime(substr(ts, 1, 4) " " substr(ts, 6, 2) " " substr(ts, 9, 2) " " \
                      substr(ts, 12, 2) " " substr(ts, 15, 2) " " substr(ts, 18, 2))
      }
      function drop(ip) {
        if (ip in lmac && ipof[lmac[ip]] == ip) delete ipof[lmac[ip]]
        delete lmac[ip]; delete lack[ip]; delete lexp[ip]
      }

      # File 1: reservations.
      ARGIND == 1 { rip[$1] = $2; rhost[$1] = $3; next }

      # File 2: the Kea log(s), oldest first.
      /DHCPSRV_MEMFILE_DB .*opening memory file lease database/ {
        delete lmac; delete lack; delete lexp; delete ipof
        marker = 1
        persistent = ($0 ~ /persist=true/)
        next
      }
      /DHCP4_LEASE_(ALLOC|REUSE) / {
        if (match($0, /\[hwtype=1 ([0-9a-f:]+)\]/, m) &&
            match($0, /lease ([0-9.]+) has been (allocated|reused) for ([0-9]+) seconds/, x)) {
          ts = substr($0, 1, 19)
          # A client holds one lease: a new address replaces its previous one.
          if ((m[1] in ipof) && ipof[m[1]] != x[1]) drop(ipof[m[1]])
          ipof[m[1]] = x[1]
          lmac[x[1]] = m[1]
          lack[x[1]] = ts
          lexp[x[1]] = epoch(ts) + x[3]
        }
        next
      }
      /DHCP4_RELEASE(_DELETED)? / {
        if (match($0, /address ([0-9.]+) was (released|deleted on release)/, r)) drop(r[1])
        next
      }
      /DHCP4_DECLINE_LEASE/ {
        if (match($0, /addr(ess)? ([0-9.]+)/, d)) drop(d[2])
        next
      }

      END {
        if (!marker)
          print "[WARN] no Kea start marker in the log; leases from before the log begins are missing" > "/dev/stderr"
        if (persistent)
          print "[WARN] Kea last started with persist=true; leases are also stored on disk" > "/dev/stderr"

        for (ip in lmac) {
          if (lexp[ip] <= now) continue
          mac = lmac[ip]
          have[mac] = 1
          printf "%s\t%s\t%s\t%s\t%s\n", ip, mac, (mac in rhost ? rhost[mac] : "(no reservation)"), \
                 lack[ip], strftime("%Y-%m-%d %H:%M:%S", lexp[ip])
        }
        if (show_all)
          for (mac in rip)
            if (!(mac in have))
              printf "%s\t%s\t%s\t-\t-\n", rip[mac], mac, rhost[mac]
      }
    ' <(printf '%s\n' "${reservations}") <(cat "${log_files[@]}") \
      | sort -t "$(printf '\t')" -V -k1,1
  } | column -t -s "$(printf '\t')"
}

if [[ "${WATCH}" -eq 0 ]]; then
  print_leases
  exit $?
fi

SLEEP_PID=""
trap 'kill "${SLEEP_PID:-}" 2>/dev/null' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

while true; do
  # Build the whole screen first so a slow pass does not leave it blank.
  screen=$(print_leases 2>&1)
  [[ -t 1 ]] && printf '\033[H\033[2J'
  printf 'Every %ss: %s    %s\n\n%s\n' "${WATCH}" "${0##*/}" "$(date '+%F %T')" "${screen}"
  sleep "${WATCH}" &
  SLEEP_PID=$!
  wait "${SLEEP_PID}" 2>/dev/null
done
