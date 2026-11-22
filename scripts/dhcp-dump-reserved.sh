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
# Script: dhcp-dump-reserved.sh
# Description: Run on the cluster controller.  Reads the current Kea DHCPv4
#              configuration, collects every reserved hw-address, and runs
#              tcpdump showing only DHCP packets whose client hardware address
#              (BOOTP chaddr) is one of those MACs.
#
#              Reservations come and go as hosts are created and deleted, so
#              the config is re-read every --refresh seconds.  When the set of
#              reserved MACs changes, the change is reported and tcpdump is
#              restarted with a new filter.  If a re-read fails (for example
#              while the file is being rewritten) the current capture keeps
#              running with the previous MAC list.
#
#              Filtering on chaddr rather than the Ethernet header matches both
#              directions: client DISCOVER/REQUEST (src MAC = client) and server
#              OFFER/ACK/NAK (which are often sent to ff:ff:ff:ff:ff:ff).
#              Requests from MACs without a reservation are ignored.
#
#              Output: tcpdump's packet lines (with Ethernet headers, -e) go to
#              stdout; this script's own status lines go to stderr.  Redirect
#              both (2>&1) if you want a single, ordered log.  A restart on a
#              reservation change loses any packet that arrives in the instant
#              between the old capture stopping and the new one starting.
#
# Limitations:
#   - Only reservations identified by "hw-address" in the config file are
#     matched (colon-separated, e.g. fa:16:3e:61:78:5e).  Reservations keyed by
#     client-id, duid or circuit-id, and reservations stored in a host database
#     rather than the config file, are not seen.
#   - Only IPv4 (DHCPv4, UDP ports 67/68) is captured.
#
# Usage: sudo ./dhcp-dump-reserved.sh [OPTIONS] [-- <extra tcpdump args>]
#
# Options:
#   -c, --config <file>      Kea config file (default: /etc/kea/kea-dhcp4.conf)
#   -i, --interface <ifname> Interface to capture on (default: first interface
#                            in the Kea "interfaces-config")
#   -r, --refresh <sec>      Seconds between config re-reads (default: 10)
#   -n, --dry-run            Print the tcpdump command once and exit
#   -h, --help               Show this help and exit
#
# Examples:
#   sudo ./dhcp-dump-reserved.sh
#   sudo ./dhcp-dump-reserved.sh -r 5 -- -vv
#   sudo ./dhcp-dump-reserved.sh -n
#
# Environment Variables:
#   KEA_CONFIG  - Kea config file (default: /etc/kea/kea-dhcp4.conf)
#   REFRESH     - Seconds between config re-reads (default: 10)
#
# Prerequisites:
#   - Must run on the host where kea-dhcp4 is running (it reads the local
#     config and captures on the local interface); --dry-run is exempt
#   - jq and tcpdump must be in PATH
#   - Root privileges (reading the Kea config and capturing packets)
#   - The Kea config must be strict JSON (no comments)
#
# Exit Codes:
#   0   - Normal exit (dry run)
#   1   - Missing dependency, bad configuration, no reservations at startup,
#         or tcpdump exited unexpectedly
#   130 - Interrupted (SIGINT)
#   143 - Terminated (SIGTERM)
#==============================================================================

set -uo pipefail

function log_error() { echo "[ERROR] $*" >&2; }
function log_info() { echo "$(date '+%F %T') $*" >&2; }

function usage() {
  sed -n '/^# Usage:/,/^# Prerequisites:/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//; $d'
  exit 0
}

CONFIG="${KEA_CONFIG:-/etc/kea/kea-dhcp4.conf}"
REFRESH="${REFRESH:-10}"
IFACE=""
DRY_RUN=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    -c|--config)
      [[ $# -lt 2 ]] && { log_error "$1 requires an argument"; exit 1; }
      CONFIG="$2"; shift 2 ;;
    -i|--interface)
      [[ $# -lt 2 ]] && { log_error "$1 requires an argument"; exit 1; }
      IFACE="$2"; shift 2 ;;
    -r|--refresh)
      [[ $# -lt 2 ]] && { log_error "$1 requires an argument"; exit 1; }
      REFRESH="$2"; shift 2 ;;
    -n|--dry-run)
      DRY_RUN=1; shift ;;
    -h|--help)
      usage ;;
    --)
      shift; break ;;
    *)
      log_error "Unknown option: $1"
      exit 1 ;;
  esac
done
# Anything left over is passed straight to tcpdump.
EXTRA_ARGS=("$@")

for cmd in jq tcpdump; do
  if ! command -v "${cmd}" >/dev/null 2>&1; then
    log_error "Missing required program: ${cmd}"
    exit 1
  fi
done

if [[ ! "${REFRESH}" =~ ^[1-9][0-9]*$ ]]; then
  log_error "REFRESH must be a positive integer, got: ${REFRESH}"
  exit 1
fi

# This script must run on the Kea server itself (local config, local capture).
# A dry run is allowed anywhere so a copied config can be inspected.
if [[ ${DRY_RUN} -eq 0 ]] && ! pgrep -x kea-dhcp4 >/dev/null 2>&1; then
  log_error "kea-dhcp4 is not running on this host; run this on the Kea server"
  exit 1
fi

if [[ ! -r "${CONFIG}" ]]; then
  log_error "Cannot read ${CONFIG} (run as root?)"
  exit 1
fi

if [[ -z "${IFACE}" ]]; then
  IFACE=$(jq -r '.Dhcp4["interfaces-config"].interfaces[0] // empty' "${CONFIG}") || {
    log_error "Failed to parse ${CONFIG} as JSON"
    exit 1
  }
fi
if [[ ! "${IFACE}" =~ ^[A-Za-z0-9_.:-]{1,15}$ ]]; then
  log_error "Missing or invalid interface name: '${IFACE}' (use --interface)"
  exit 1
fi

#------------------------------------------------------------------------------
# Read the reserved MACs from the Kea config into the global array MACS
# (lowercased, de-duplicated, sorted).  Reservations may be defined per subnet,
# per subnet inside a shared network, or at the global level.  Returns
# non-zero, leaving MACS untouched, if the config cannot be read or parsed.
# Invalid MACs are skipped and reported only while WARN_INVALID=1 (the initial
# read), so a bad entry does not repeat on every refresh.
#------------------------------------------------------------------------------
function read_macs() {
  local out mac
  local -a found=()
  out=$(jq -r '
      [ (.Dhcp4.subnet4[]?.reservations[]?),
        (.Dhcp4["shared-networks"][]?.subnet4[]?.reservations[]?),
        (.Dhcp4.reservations[]?) ]
      | map(select(has("hw-address")) | .["hw-address"] | ascii_downcase)
      | unique | .[]' "${CONFIG}" 2>/dev/null) || return 1
  while IFS= read -r mac; do
    [[ -z "${mac}" ]] && continue
    if [[ ! "${mac}" =~ ^([0-9a-f]{2}:){5}[0-9a-f]{2}$ ]]; then
      [[ ${WARN_INVALID} -eq 1 ]] && log_error "Skipping invalid MAC in config: ${mac}"
      continue
    fi
    found+=("${mac}")
  done <<< "${out}"
  MACS=("${found[@]}")
}

#------------------------------------------------------------------------------
# Build the tcpdump BPF filter for the MACs in MACS into the global FILTER.
# BOOTP chaddr starts at UDP payload offset 28; the UDP header is 8 bytes, so
# the MAC is at udp[36:4] (first four octets) and udp[40:2] (last two octets).
#------------------------------------------------------------------------------
function build_filter() {
  local mac a b c d e f sep=""
  FILTER="udp and (port 67 or port 68) and ("
  for mac in "${MACS[@]}"; do
    IFS=: read -r a b c d e f <<< "${mac}"
    FILTER+="${sep}(udp[36:4] = 0x${a}${b}${c}${d} and udp[40:2] = 0x${e}${f})"
    sep=" or "
  done
  FILTER+=")"
}

#------------------------------------------------------------------------------
# Report MACs added to / removed from the reservations.
# $1 - previous MACs (newline separated), $2 - current MACs (newline separated)
#------------------------------------------------------------------------------
function report_changes() {
  local mac
  while IFS= read -r mac; do
    [[ -n "${mac}" ]] && log_info "reservation added:   ${mac}"
  done < <(comm -13 <(printf '%s\n' "$1" | sort) <(printf '%s\n' "$2" | sort))
  while IFS= read -r mac; do
    [[ -n "${mac}" ]] && log_info "reservation removed: ${mac}"
  done < <(comm -23 <(printf '%s\n' "$1" | sort) <(printf '%s\n' "$2" | sort))
}

TCPDUMP_PID=""
SLEEP_PID=""

function stop_tcpdump() {
  if [[ -n "${TCPDUMP_PID}" ]]; then
    kill "${TCPDUMP_PID}" 2>/dev/null
    wait "${TCPDUMP_PID}" 2>/dev/null
    TCPDUMP_PID=""
  fi
}

function start_tcpdump() {
  build_filter
  log_info "capturing on ${IFACE} for ${#MACS[@]} MAC(s): ${MACS[*]}"
  tcpdump -n -e -l -i "${IFACE}" "${EXTRA_ARGS[@]}" "${FILTER}" &
  TCPDUMP_PID=$!
}

# Sleep in the background so INT/TERM traps fire immediately.
function interruptible_sleep() {
  sleep "${REFRESH}" &
  SLEEP_PID=$!
  wait "${SLEEP_PID}" 2>/dev/null
}

MACS=()
WARN_INVALID=1
if ! read_macs; then
  log_error "Failed to parse ${CONFIG} as JSON"
  exit 1
fi
if [[ ${#MACS[@]} -eq 0 ]]; then
  log_error "No hw-address reservations found in ${CONFIG}"
  exit 1
fi

WARN_INVALID=0

if [[ ${DRY_RUN} -eq 1 ]]; then
  build_filter
  printf '%q ' tcpdump -n -e -l -i "${IFACE}" "${EXTRA_ARGS[@]}" "${FILTER}"
  echo
  exit 0
fi

trap 'stop_tcpdump; kill "${SLEEP_PID:-}" 2>/dev/null' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

echo "Config    : ${CONFIG} (re-read every ${REFRESH}s)" >&2
start_tcpdump

while true; do
  interruptible_sleep

  if [[ -n "${TCPDUMP_PID}" ]] && ! kill -0 "${TCPDUMP_PID}" 2>/dev/null; then
    log_error "tcpdump exited unexpectedly"
    TCPDUMP_PID=""
    exit 1
  fi

  PREV=("${MACS[@]}")
  # A failed read (e.g. file mid-rewrite) keeps the current capture running.
  if ! read_macs; then
    log_info "failed to re-read ${CONFIG}; keeping previous MAC list"
    continue
  fi

  PREV_TXT=$(printf '%s\n' "${PREV[@]}")
  CUR_TXT=$(printf '%s\n' "${MACS[@]}")
  [[ "${PREV_TXT}" == "${CUR_TXT}" ]] && continue

  report_changes "${PREV_TXT}" "${CUR_TXT}"
  stop_tcpdump
  if [[ ${#MACS[@]} -eq 0 ]]; then
    log_info "no reservations left; waiting"
  else
    start_tcpdump
  fi
done
