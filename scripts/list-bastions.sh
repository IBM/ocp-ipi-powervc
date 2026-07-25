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
# Script: list-bastions.sh
# Description: List bastion (standalone) VMs on PowerVC/OpenStack.
#              Any server whose name does not match the cluster-node pattern
#              (p-<hash>-<cluster>-(master|worker|bootstrap)[N]) is treated as
#              a bastion / standalone VM.
#
# Usage: ./list-bastions.sh [--cloud <cloud>] [--bastionRSA <path>]
#
# Options:
#   --cloud       <cloud>  OpenStack cloud name (skip interactive prompt)
#   --bastionRSA  <path>   Path to SSH private key for bastion access (optional)
#   -h, --help             Show this help and exit
#
# Environment Variables:
#   CLOUD        - OpenStack cloud name from clouds.yaml (skips prompt if set)
#   BASTION_RSA  - Path to SSH private key for bastion access (skips prompt if set)
#
# Prerequisites:
#   - openstack CLI must be in PATH
#   - ssh must be in PATH (required only when --bastionRSA is provided)
#
# Exit Codes:
#   0 - Success
#   1 - Error (missing dependencies, invalid configuration, operation failure)
#==============================================================================

set -euo pipefail

#==============================================================================
# Global Variables
#==============================================================================
readonly SCRIPT_NAME="$(basename "${BASH_SOURCE[0]}")"

# ANSI color codes
readonly COLOR_RED='\033[0;31m'
readonly COLOR_GREEN='\033[0;32m'
readonly COLOR_YELLOW='\033[1;33m'
readonly COLOR_BLUE='\033[0;34m'
readonly COLOR_RESET='\033[0m'

#==============================================================================
# Utility Functions
#==============================================================================

function log_info()    { echo -e "${COLOR_BLUE}[INFO]${COLOR_RESET} $*"; }
function log_success() { echo -e "${COLOR_GREEN}[SUCCESS]${COLOR_RESET} $*"; }
function log_warning() { echo -e "${COLOR_YELLOW}[WARNING]${COLOR_RESET} $*"; }
function log_error()   { echo -e "${COLOR_RED}[ERROR]${COLOR_RESET} $*" >&2; }
function die()         { log_error "$*"; exit 1; }

#------------------------------------------------------------------------------
# Return 0 if the given command is available in PATH, 1 otherwise.
# $1 - command name to test
#------------------------------------------------------------------------------
function command_exists() {
	command -v "$1" >/dev/null 2>&1
}

#------------------------------------------------------------------------------
# Prompt the user for a value, storing it in the named variable.
# $1 - prompt text; $2 - variable name
#------------------------------------------------------------------------------
function prompt_input() {
	local prompt_text="$1"
	local var_name="$2"

	local input_value
	read -rp "${prompt_text} []: " input_value

	if [[ -z "${input_value}" ]]; then
		die "You must enter a value for ${var_name}"
	fi

	printf -v "${var_name}" '%s' "${input_value}"
	export "${var_name}"
}

#------------------------------------------------------------------------------
# Collect the OpenStack cloud name from the environment or by prompting.
#------------------------------------------------------------------------------
function collect_cloud_name() {
	log_info "Collecting OpenStack cloud name..."

	if [[ ! -v CLOUD ]] || [[ -z "${CLOUD}" ]]; then
		prompt_input "What is the OpenStack cloud name (from clouds.yaml)" "CLOUD"
	fi

	export CLOUD
	log_success "Cloud name: ${CLOUD}"
}

#------------------------------------------------------------------------------
# Collect the bastion RSA key path from the environment or by prompting.
# The key is optional — if not provided the column is omitted from output.
#------------------------------------------------------------------------------
function collect_bastion_rsa() {
	if [[ ! -v BASTION_RSA ]] || [[ -z "${BASTION_RSA}" ]]; then
		return 0
	fi

	if [[ ! -f "${BASTION_RSA}" ]]; then
		die "SSH key file does not exist: ${BASTION_RSA}"
	fi

	export BASTION_RSA
	log_success "Bastion RSA key: ${BASTION_RSA}"
}

#------------------------------------------------------------------------------
# Print usage information to stderr and exit 0.
#------------------------------------------------------------------------------
function usage() {
	echo "Usage: ${SCRIPT_NAME} [--cloud <cloud>] [--bastionRSA <path>]" >&2
	echo ""                                                               >&2
	echo "  --cloud       <cloud>  OpenStack cloud name (skips interactive prompt)" >&2
	echo "  --bastionRSA  <path>   Path to SSH private key for bastion access"      >&2
	echo "  -h, --help             Show this help and exit"                         >&2
	exit 0
}

#------------------------------------------------------------------------------
# Test whether the bastion at the given IP is reachable via SSH.
# Tries the usernames "cloud-user" and "core" in order; returns 0 as soon as
# one succeeds (writing the matched username into the variable named by $2),
# or 1 if both fail.
# Requires BASTION_RSA to be set.
# $1 - IP address of the bastion to test
# $2 - name of the variable to receive the matched username (on success)
#------------------------------------------------------------------------------
function bastionIsAlive() {
	local ip="$1"
	local -n _matched_user="$2"

	if [[ -z "${BASTION_RSA:-}" ]]; then die "bastionIsAlive: BASTION_RSA is not set"; fi

	local user
	for user in cloud-user core; do
		if ssh \
			-o "IdentitiesOnly=yes" \
			-o "BatchMode=yes" \
			-o "ConnectTimeout=30" \
			-o "StrictHostKeyChecking=no" \
			-i "${BASTION_RSA}" \
			"${user}@${ip}" \
			"echo bastion-is-alive" \
			>/dev/null 2>&1; then
			_matched_user="${user}"
			return 0
		fi
	done
	return 1
}

#------------------------------------------------------------------------------
# Fetch the server list from OpenStack and populate the global _csv_lines array.
# Exits with a warning if the cloud has no servers.
# Reads:  CLOUD
# Writes: _csv_lines (global array, one CSV row per element, header at index 0)
#------------------------------------------------------------------------------
function fetch_server_list() {
	log_info "Fetching server list from cloud: ${CLOUD} ..."

	# Separate the call from the assignment so the || die path is reachable under
	# set -e (a failed command substitution inside $() does not trigger errexit on
	# the assignment line itself).
	# Redirect stderr to a temp file so openstack warnings reach the terminal on
	# success while still being captured for the error message on failure.
	local err_tmp
	err_tmp=$(mktemp)
	local raw_csv
	if ! raw_csv=$(openstack --os-cloud="${CLOUD}" server list --format=csv 2>"${err_tmp}"); then
		local err_msg
		err_msg=$(cat "${err_tmp}"); rm -f "${err_tmp}"
		die "openstack server list failed: ${err_msg}"
	fi
	rm -f "${err_tmp}"

	# Strip a trailing newline (if any) before splitting so mapfile does not
	# produce a spurious empty final element.
	mapfile -t _csv_lines < <(printf '%s' "${raw_csv}")
	# Treat header-only (1 line) or empty as no results.
	if [[ ${#_csv_lines[@]} -le 1 ]]; then
		log_warning "No servers found."
		exit 0
	fi
}

#------------------------------------------------------------------------------
# Parse _csv_lines and populate the bastion output arrays and column widths.
# Any server whose name does not match the cluster-node pattern is a bastion.
# Exits with a warning if no bastion VMs are found.
# Reads:  _csv_lines
# Writes: bastion_names, bastion_ips, bastion_statuses, bastion_images,
#         bastion_alive_users, _col_name, _col_ip, _col_status, _col_image
#------------------------------------------------------------------------------
function collect_bastions() {
	# Any server whose name does NOT match the cluster-node pattern is a bastion.
	bastion_names=()
	bastion_ips=()
	bastion_statuses=()
	bastion_images=()
	bastion_alive_users=()

	# Column widths — initialised to header string lengths as minimums.
	_col_name=4    # len("Name")
	_col_ip=10     # len("IP Address")
	_col_status=6  # len("Status")
	_col_image=5   # len("Image")

	local _id name status networks image _rest ip
	while IFS=',' read -r _id name status networks image _rest; do
		# Guard against any blank lines in the input.
		[[ -z "${name//\"/}" ]] && continue
		name="${name//\"/}"
		status="${status//\"/}"
		image="${image//\"/}"
		# Extract the first IPv4 address from the networks field using pure bash —
		# avoids a grep subprocess per row and removes the grep dependency entirely.
		ip=""
		if [[ "${networks}" =~ ([0-9]+\.[0-9]+\.[0-9]+\.[0-9]+) ]]; then
			ip="${BASH_REMATCH[1]}"
		fi

		# Skip cluster nodes — they follow p-<hash>-<cluster>-(master|worker|bootstrap)[N]
		if [[ "${name}" =~ ^p-[a-f0-9-]+-[a-z0-9]+-(master|worker|bootstrap) ]]; then
			continue
		fi

		bastion_names+=("${name}")
		bastion_ips+=("${ip}")
		bastion_statuses+=("${status}")
		bastion_images+=("${image}")

		# Update column widths as we go — avoids a second pass.
		[[ ${#name}   -gt ${_col_name}   ]] && _col_name=${#name}
		[[ ${#ip}     -gt ${_col_ip}     ]] && _col_ip=${#ip}
		[[ ${#status} -gt ${_col_status} ]] && _col_status=${#status}
		[[ ${#image}  -gt ${_col_image}  ]] && _col_image=${#image}
	done < <(printf '%s\n' "${_csv_lines[@]:1}")
	unset _csv_lines

	if [[ ${#bastion_names[@]} -eq 0 ]]; then
		log_warning "No bastion VMs found."
		exit 0
	fi
}

#------------------------------------------------------------------------------
# Probe each collected bastion via SSH and remove any that do not respond.
# The username that succeeded is stored in bastion_alive_users at the same index.
# No-op if BASTION_RSA is not set.
# Reads/Writes: bastion_names, bastion_ips, bastion_statuses, bastion_images,
#               bastion_alive_users, _col_name, _col_ip, _col_status, _col_image
#------------------------------------------------------------------------------
function filter_alive_bastions() {
	if [[ -z "${BASTION_RSA:-}" ]]; then
		return 0
	fi

	log_info "Probing bastions for SSH reachability..."

	local -a alive_names=() alive_ips=() alive_statuses=() alive_images=() alive_users=()

	# Reset column widths — they will be recomputed from surviving entries only.
	_col_name=4
	_col_ip=10
	_col_status=6
	_col_image=5
	_col_user=8   # len("Username")

	local i
	for i in "${!bastion_names[@]}"; do
		local ip="${bastion_ips[$i]}"
		if [[ -z "${ip}" ]]; then
			log_warning "Skipping ${bastion_names[$i]}: no IP address"
			continue
		fi

		local matched_user=""
		if bastionIsAlive "${ip}" matched_user; then
			log_success "${bastion_names[$i]} (${ip}) is reachable as ${matched_user}"
			alive_names+=("${bastion_names[$i]}")
			alive_ips+=("${ip}")
			alive_statuses+=("${bastion_statuses[$i]}")
			alive_images+=("${bastion_images[$i]}")
			alive_users+=("${matched_user}")

			[[ ${#bastion_names[$i]}    -gt ${_col_name}   ]] && _col_name=${#bastion_names[$i]}
			[[ ${#ip}                   -gt ${_col_ip}     ]] && _col_ip=${#ip}
			[[ ${#bastion_statuses[$i]} -gt ${_col_status} ]] && _col_status=${#bastion_statuses[$i]}
			[[ ${#bastion_images[$i]}   -gt ${_col_image}  ]] && _col_image=${#bastion_images[$i]}
			[[ ${#matched_user}         -gt ${_col_user}   ]] && _col_user=${#matched_user}
		else
			log_warning "${bastion_names[$i]} (${ip}) did not respond — excluded"
		fi
	done

	bastion_names=("${alive_names[@]+"${alive_names[@]}"}")
	bastion_ips=("${alive_ips[@]+"${alive_ips[@]}"}")
	bastion_statuses=("${alive_statuses[@]+"${alive_statuses[@]}"}")
	bastion_images=("${alive_images[@]+"${alive_images[@]}"}")
	bastion_alive_users=("${alive_users[@]+"${alive_users[@]}"}")

	if [[ ${#bastion_names[@]} -eq 0 ]]; then
		log_warning "No reachable bastion VMs found."
		exit 0
	fi

	log_success "${#bastion_names[@]} reachable bastion VM(s) after liveness check"
}

#------------------------------------------------------------------------------
# Render the bastion table to stdout.
# A Username column is included when bastion_alive_users is populated (i.e.
# after filter_alive_bastions has run). RSA Key column is included when
# BASTION_RSA is set.
# Reads: bastion_names, bastion_ips, bastion_statuses, bastion_images,
#        bastion_alive_users (optional), _col_name, _col_ip, _col_status,
#        _col_image, _col_user (optional), BASTION_RSA (optional)
#------------------------------------------------------------------------------
function display_bastions() {
	local _hdr="  %-${_col_name}s  %-${_col_ip}s  %-${_col_status}s  %-${_col_image}s"
	local _sep
	_sep="  $(printf '%*s' "${_col_name}"   '' | tr ' ' '-')"
	_sep+="  $(printf '%*s' "${_col_ip}"     '' | tr ' ' '-')"
	_sep+="  $(printf '%*s' "${_col_status}" '' | tr ' ' '-')"
	_sep+="  $(printf '%*s' "${_col_image}"  '' | tr ' ' '-')"
	local _row="  %-${_col_name}s  %-${_col_ip}s  %-${_col_status}s  %-${_col_image}s"

	local -a _hdr_args=("Name" "IP Address" "Status" "Image")

	# Username column — present only when liveness probing has run.
	local _show_user=false
	if [[ ${#bastion_alive_users[@]} -gt 0 ]]; then
		_show_user=true
		_hdr+="  %-${_col_user}s"
		_sep+="  $(printf '%*s' "${_col_user}" '' | tr ' ' '-')"
		_row+="  %-${_col_user}s"
		_hdr_args+=("Username")
	fi

	if [[ -v BASTION_RSA ]] && [[ -n "${BASTION_RSA}" ]]; then
		_hdr+="  %s"
		_sep+="  -------"
		_row+="  %s"
		_hdr_args+=("RSA Key")
	fi

	echo ""
	echo "Bastion / Standalone VMs (${#bastion_names[@]}):"
	echo ""
	# shellcheck disable=SC2059
	printf "${_hdr}\n" "${_hdr_args[@]}"
	echo "${_sep}"
	local i
	local -a _row_args
	for i in "${!bastion_names[@]}"; do
		_row_args=("${bastion_names[$i]}" "${bastion_ips[$i]}" "${bastion_statuses[$i]}" "${bastion_images[$i]}")
		[[ "${_show_user}" == "true"  ]]                       && _row_args+=("${bastion_alive_users[$i]}")
		[[ -v BASTION_RSA ]] && [[ -n "${BASTION_RSA}" ]]      && _row_args+=("${BASTION_RSA}")
		# shellcheck disable=SC2059
		printf "${_row}\n" "${_row_args[@]}"
	done
	echo ""

	log_success "Found ${#bastion_names[@]} bastion VM(s)"
}

#------------------------------------------------------------------------------
# Parse script arguments and export CLOUD, BASTION_RSA.
# Called before any setup so -h/--help exits immediately without side-effects.
# $@ - all script arguments
#------------------------------------------------------------------------------
function parse_arguments() {
	while [[ $# -gt 0 ]]; do
		case "$1" in
			--cloud)
				[[ $# -lt 2 ]] && { echo "Error: $1 requires an argument." >&2; exit 1; }
				CLOUD="$2"; shift 2 ;;
			--bastionRSA)
				[[ $# -lt 2 ]] && { echo "Error: $1 requires an argument." >&2; exit 1; }
				BASTION_RSA="$2"; shift 2 ;;
			-h|--help)
				usage ;;
			*)
				echo "Error: unknown option $1." >&2; exit 1 ;;
		esac
	done
}

#==============================================================================
# Argument Parsing
#==============================================================================

parse_arguments "$@"

#==============================================================================
# Initialise
#==============================================================================

# Check dependencies first — fail fast before any prompts.
if ! command_exists openstack; then
	die "Missing required program: openstack"
fi
if [[ -v BASTION_RSA ]] && ! command_exists ssh; then
	die "Missing required program: ssh"
fi

collect_cloud_name
collect_bastion_rsa

#==============================================================================
# Fetch Server List
#==============================================================================

fetch_server_list

#==============================================================================
# Collect Bastion / Standalone VMs
#==============================================================================

collect_bastions

#==============================================================================
# Filter Alive Bastions
#==============================================================================

filter_alive_bastions

#==============================================================================
# Display Bastion List
#==============================================================================

display_bastions
