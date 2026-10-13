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
# Script: watch-bastions-port.sh
# Description: Continuously watch bastion VMs on PowerVC/OpenStack, printing a
#              line whenever a bastion's addresses or ports change, and when one
#              disappears.  Bastions are discovered dynamically on every pass —
#              no hardcoded server list required.
#
#              Each pass makes one "server list" call plus one "port list" call
#              per bastion; the port lookups run in parallel.  A pass where an
#              OpenStack call fails is skipped or reported as a lookup failure
#              rather than being misread as a deletion or an empty port list.
#
# Usage: ./watch-bastions-port.sh [OPTIONS]
#
# Options:
#   -c, --cloud <cloud>     OpenStack cloud name (default: ocp-ci)
#   -i, --interval <sec>    Seconds between passes (default: 30)
#   -r, --regex <pattern>   Regex pattern to match bastion names (default: p-uuid)
#   -h, --help              Show this help and exit
#
# Output format (one line per change; the bastion name and address columns are
# padded with spaces so the "|" separators line up on every line of a pass):
#   <date> <time> <bastion>: <network>=<ip>[,<ip>][; ...] | <port-id>,<port-id>,
#   <date> <time> <bastion>: <deleted>
#   <date> <time> <bastion>: <port lookup failed>
#
# Environment Variables:
#   CLOUD       - OpenStack cloud name from clouds.yaml (default: ocp-ci)
#   INTERVAL    - Seconds between passes, positive integer (default: 30)
#   BASTION_RE  - Regex pattern to match bastion names
#                 (default: '^p-[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$')
#
# Prerequisites:
#   - openstack CLI must be in PATH
#   - jq must be in PATH
#
# Exit Codes:
#   0   - Normal exit (never reached unless interrupted)
#   1   - Missing dependency or invalid configuration
#   130 - Interrupted (SIGINT)
#   143 - Terminated (SIGTERM)
#==============================================================================

# No -e: a transient openstack failure must not kill a long-running watcher.
set -uo pipefail

function log_error() { echo "[ERROR] $*" >&2; }

function usage() {
  cat <<EOF
Usage: ${0##*/} [OPTIONS]

Continuously watch bastion VMs on PowerVC/OpenStack.

OPTIONS:
  -c, --cloud <cloud>     OpenStack cloud name (default: \${CLOUD:-ocp-ci})
  -i, --interval <sec>    Seconds between passes (default: \${INTERVAL:-30})
  -r, --regex <pattern>   Regex pattern to match bastion names (default: p-uuid)
  -h, --help              Show this help and exit

ENVIRONMENT VARIABLES:
  CLOUD       OpenStack cloud name from clouds.yaml (default: ocp-ci)
  INTERVAL    Seconds between passes, positive integer (default: 30)
  BASTION_RE  Regex pattern to match bastion names
              (default: '^p-[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$')
EOF
  exit 0
}

# Default bastion regex matches p-<uuid> exactly. Cluster nodes have an extra
# -<cluster>-(master|worker|bootstrap)[N] suffix, so this anchored pattern
# excludes them.
DEFAULT_BASTION_RE='^p-[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$'

CLOUD="${CLOUD:-ocp-ci}"
INTERVAL="${INTERVAL:-30}"
BASTION_RE="${BASTION_RE:-${BASTION_REGEX:-${DEFAULT_BASTION_RE}}}"

while [[ $# -gt 0 ]]; do
  case "$1" in
    -c|--cloud)
      [[ $# -lt 2 ]] && { log_error "$1 requires an argument"; exit 1; }
      CLOUD="$2"; shift 2 ;;
    -i|--interval)
      [[ $# -lt 2 ]] && { log_error "$1 requires an argument"; exit 1; }
      INTERVAL="$2"; shift 2 ;;
    -r|--regex)
      [[ $# -lt 2 ]] && { log_error "$1 requires an argument"; exit 1; }
      BASTION_RE="$2"; shift 2 ;;
    -h|--help)
      usage ;;
    *)
      log_error "Unknown option: $1"
      exit 1 ;;
  esac
done

readonly CLOUD
readonly INTERVAL
readonly BASTION_RE

#------------------------------------------------------------------------------
# Sleep for INTERVAL seconds in a way that lets INT/TERM traps fire immediately.
# A plain foreground "sleep" would delay trap handling until it finished.
#------------------------------------------------------------------------------
function interruptible_sleep() {
  sleep "${INTERVAL}" &
  SLEEP_PID=$!
  wait "${SLEEP_PID}"
}

#------------------------------------------------------------------------------
# Print one timestamped, column-aligned line.
# Reads: name_w (name column width), addr_w (address column width)
# $1 - bastion name
# $2 - either "<addresses><TAB><ports>" or a bare status such as "<deleted>"
#------------------------------------------------------------------------------
function print_line() {
  local name="$1" value="$2" addr ports
  if [[ "${value}" == *$'\t'* ]]; then
    addr="${value%%$'\t'*}"
    ports="${value#*$'\t'}"
    printf '%s %-*s %-*s | %s\n' "$(date '+%F %T')" "$((name_w + 1))" "${name}:" "${addr_w}" "${addr}" "${ports}"
  else
    printf '%s %-*s %s\n' "$(date '+%F %T')" "$((name_w + 1))" "${name}:" "${value}"
  fi
}

#------------------------------------------------------------------------------
# Fail fast on missing dependencies or bad configuration.
#------------------------------------------------------------------------------
for cmd in openstack jq; do
  if ! command -v "${cmd}" >/dev/null 2>&1; then
    log_error "Missing required program: ${cmd}"
    exit 1
  fi
done
if [[ ! "${INTERVAL}" =~ ^[1-9][0-9]*$ ]]; then
  log_error "INTERVAL must be a positive integer, got: ${INTERVAL}"
  exit 1
fi
if [[ -z "${BASTION_RE}" ]]; then
  log_error "BASTION_RE cannot be empty"
  exit 1
fi
re_rc=0
[[ "" =~ ${BASTION_RE} ]] 2>/dev/null || re_rc=$?
if [[ ${re_rc} -ge 2 ]]; then
  log_error "Invalid regular expression for BASTION_RE: ${BASTION_RE}"
  exit 1
fi

# Per-pass scratch space for the parallel port lookups.
TMP_DIR=$(mktemp -d) || { log_error "mktemp failed"; exit 1; }
readonly TMP_DIR

# Cleanup runs on every exit path; INT/TERM just convert to a normal exit with
# the conventional status so the EXIT trap runs exactly once.
trap 'kill "${SLEEP_PID:-}" 2>/dev/null; rm -rf "${TMP_DIR}"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

declare -A last   # last["<name>"]="<addresses><TAB><ports>" or a <status> marker
name_w=0          # width of the name column for the current pass
addr_w=0          # width of the address column for the current pass

while true; do
  # One API call returns every server's name, id and addresses.  If it fails,
  # skip this pass entirely: treating it as an empty list would report every
  # bastion as deleted.
  if ! server_json=$(openstack --os-cloud="${CLOUD}" server list -f json 2>/dev/null); then
    interruptible_sleep
    continue
  fi

  declare -A bastion_id=()    # name -> server UUID
  declare -A bastion_addr=()  # name -> "net=ip[,ip]; net=ip"

  # jq failure (e.g. non-JSON output) also skips the pass, for the same reason.
  if ! parsed=$(printf '%s' "${server_json}" | jq -r '
        .[]
        | [ .Name, .ID,
            ( (.Networks // {})
              | if type == "object"
                then to_entries | map("\(.key)=\(.value | join(","))") | join("; ")
                else tostring end ) ]
        | @tsv' 2>/dev/null); then
    interruptible_sleep
    continue
  fi

  while IFS=$'\t' read -r name id addr; do
    [[ "${name}" =~ ${BASTION_RE} ]] || continue
    bastion_id["${name}"]="${id}"
    bastion_addr["${name}"]="${addr}"
  done <<< "${parsed}"

  # Sorted so output order is stable between passes.
  mapfile -t bastions < <(printf '%s\n' "${!bastion_id[@]}" | sort)
  [[ ${#bastions[@]} -eq 1 && -z "${bastions[0]}" ]] && bastions=()

  # Column widths for aligned output: the widest bastion name (including any
  # about to be reported as deleted) and the widest address string this pass.
  name_w=0
  for B in "${bastions[@]}" "${!last[@]}"; do
    [[ ${#B} -gt ${name_w} ]] && name_w=${#B}
  done

  # Report bastions that disappeared since the last pass, then forget them so a
  # recreated bastion with the same name is reported afresh.
  for B in "${!last[@]}"; do
    if [[ -z "${bastion_id[$B]+x}" ]]; then
      print_line "${B}" "<deleted>"
      unset "last[$B]"
    fi
  done

  # Port lookups in parallel; each job writes its result to its own file only
  # on success, so a failed lookup is distinguishable from "no ports".
  # The server UUID is used rather than the name, which need not be unique.
  rm -f "${TMP_DIR:?}"/*
  for B in "${bastions[@]}"; do
    (
      if ports=$(openstack --os-cloud="${CLOUD}" port list --server "${bastion_id[$B]}" -f value -c ID 2>/dev/null); then
        ports=$(printf '%s\n' "${ports}" | sort | tr '\n' ',')
        # Tab separates the address and port fields so they can be padded later.
        printf '%s\t%s' "${bastion_addr[$B]}" "${ports}" > "${TMP_DIR}/${B}"
      fi
    ) &
  done
  wait

  addr_w=0
  for B in "${bastions[@]}"; do
    [[ ${#bastion_addr[$B]} -gt ${addr_w} ]] && addr_w=${#bastion_addr[$B]}
  done

  for B in "${bastions[@]}"; do
    if [[ -f "${TMP_DIR}/${B}" ]]; then
      NOW=$(<"${TMP_DIR}/${B}")
    else
      NOW="<port lookup failed>"
    fi
    if [[ "${last[$B]-}" != "${NOW}" ]]; then
      print_line "${B}" "${NOW}"
      last[$B]="${NOW}"
    fi
  done

  interruptible_sleep
done
