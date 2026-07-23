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
# Script: list-clusters-and-delete.sh
# Description: List running OpenShift clusters on PowerVC/OpenStack, prompt the
#              user to select one, and delete it.
#
# Usage: ./list-clusters-and-delete.sh [-c <cloud>] [-l]
#
# Options:
#   -c <cloud>   OpenStack cloud name (skip interactive prompt)
#   -l           List clusters only; do not prompt for deletion
#   -h           Show this help and exit
#
# Environment Variables:
#   CLOUD          - OpenStack cloud name from clouds.yaml (skips prompt if set)
#   CONTROLLER_IP  - IP of the PowerVC controller (prompted if unset, deletion only)
#
# Prerequisites:
#   - ocp-ipi-powervc-linux-{arch} tool must be in PATH  (deletion only)
#   - openshift-install must be in PATH                  (deletion only)
#   - openstack CLI must be in PATH
#   - jq must be in PATH
#
# Exit Codes:
#   0 - Success or user cancelled
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

# Set by write-metadata section; must survive until after openshift-install destroy.
CLUSTER_DIR=""
# Backup copy of metadata.json taken just before destruction.
# Used by delete_metadata and cleanup_on_exit to restore on failure.
TEMP_DIR=""

# List-only mode flag (set by -l)
LIST_ONLY=false

#==============================================================================
# Utility Functions  (kept in sync with delete-cluster.sh)
#==============================================================================

function log_info()    { echo -e "${COLOR_BLUE}[INFO]${COLOR_RESET} $*"; }
function log_success() { echo -e "${COLOR_GREEN}[SUCCESS]${COLOR_RESET} $*"; }
function log_warning() { echo -e "${COLOR_YELLOW}[WARNING]${COLOR_RESET} $*"; }
function log_error()   { echo -e "${COLOR_RED}[ERROR]${COLOR_RESET} $*" >&2; }
function die()         { log_error "$*"; exit 1; }

function command_exists() {
	command -v "$1" >/dev/null 2>&1
}

#------------------------------------------------------------------------------
# Validate that a named variable is set and non-empty.
#------------------------------------------------------------------------------
function validate_non_empty() {
	local var_name="$1"
	local var_value="${!var_name:-}"

	if [[ -z "${var_value}" ]]; then
		die "${var_name} must be set and non-empty"
	fi
}

#------------------------------------------------------------------------------
# Prompt the user for a value, storing it in the named variable.
# $1 - prompt text; $2 - variable name; $3 - default (optional)
#------------------------------------------------------------------------------
function prompt_input() {
	local prompt_text="$1"
	local var_name="$2"
	local default_value="${3:-}"

	local input_value

	if [[ -n "${default_value}" ]]; then
		read -rp "${prompt_text} [${default_value}]: " input_value
		input_value="${input_value:-${default_value}}"
	else
		read -rp "${prompt_text} []: " input_value
	fi

	if [[ -z "${input_value}" ]]; then
		die "You must enter a value for ${var_name}"
	fi

	printf -v "${var_name}" '%s' "${input_value}"
	export "${var_name}"
}

#------------------------------------------------------------------------------
# Collect the OpenStack cloud name from the environment or by prompting.
# Kept in sync with delete-cluster.sh.
#------------------------------------------------------------------------------
function collect_cloud_name() {
	log_info "Collecting OpenStack cloud name..."

	if [[ ! -v CLOUD ]] || [[ -z "${CLOUD}" ]]; then
		prompt_input "What is the OpenStack cloud name (from clouds.yaml)" "CLOUD"
	fi

	validate_non_empty "CLOUD"
	export CLOUD

	log_success "Cloud name: ${CLOUD}"
}

#------------------------------------------------------------------------------
# Run a command, logging its description. Die on failure.
#------------------------------------------------------------------------------
function execute_with_check() {
	local description="$1"
	shift

	log_info "Executing: ${description}"

	local rc=0
	"$@" || rc=$?
	if [[ ${rc} -ne 0 ]]; then
		die "${description} failed with exit code ${rc}"
	fi

	log_success "${description} completed successfully"
}

#==============================================================================
# Deletion Functions  (kept in sync with delete-cluster.sh)
#==============================================================================

#------------------------------------------------------------------------------
# Resolve the architecture-specific PowerVC tool name into POWERVC_TOOL.
#------------------------------------------------------------------------------
function initialize_powervc_tool() {
	local arch
	arch="$(uname -m)"

	case "${arch}" in
		x86_64|amd64|ppc64le|aarch64) ;;
		*) die "Unsupported architecture: ${arch}" ;;
	esac

	POWERVC_TOOL="ocp-ipi-powervc-linux-${arch}"
	readonly POWERVC_TOOL

	log_info "Using PowerVC tool: ${POWERVC_TOOL}"
}

#------------------------------------------------------------------------------
# Verify all required programs are present.
# In list-only mode, only openstack and jq are required.
#------------------------------------------------------------------------------
function check_required_programs() {
	local -a required_programs=("openstack" "jq")

	if [[ "${LIST_ONLY}" == "false" ]]; then
		required_programs+=("${POWERVC_TOOL}" "openshift-install")
	fi

	local missing_programs=()

	log_info "Checking required programs..."

	for program in "${required_programs[@]}"; do
		if ! command_exists "${program}"; then
			missing_programs+=("${program}")
			log_error "Missing required program: ${program}"
		fi
	done

	if [[ ${#missing_programs[@]} -gt 0 ]]; then
		die "Missing required programs: ${missing_programs[*]}"
	fi

	log_success "All required programs are available"
}

#------------------------------------------------------------------------------
# Prompt for the PowerVC controller IP if not already set.
#------------------------------------------------------------------------------
function collect_controller_ip() {
	log_info "Collecting controller IP information..."

	local tool_label="${POWERVC_TOOL:-ocp-ipi-powervc}"

	if [[ ! -v CONTROLLER_IP ]] || [[ -z "${CONTROLLER_IP}" ]]; then
		read -rp "What is the ${tool_label} master controller IP []: " CONTROLLER_IP
		[[ -z "${CONTROLLER_IP}" ]] && die "You must enter a value for CONTROLLER_IP"
		export CONTROLLER_IP
	fi

	log_success "Controller IP: ${CONTROLLER_IP}"
}

#------------------------------------------------------------------------------
# Ping the controller to confirm reachability.
#------------------------------------------------------------------------------
function verify_controller() {
	log_info "Verifying controller connectivity: ${CONTROLLER_IP}"

	if ! ping -c1 -W5 "${CONTROLLER_IP}" >/dev/null 2>&1; then
		die "Cannot ping controller at ${CONTROLLER_IP}"
	fi

	log_success "Controller is reachable"
}

#------------------------------------------------------------------------------
# Delete object-storage containers belonging to this cluster before destroying.
#------------------------------------------------------------------------------
function hack_cleanup_containers() {
	local cloud="${1:-}"
	local infra_id="${2:-}"

	if [[ -z "${cloud}" ]] || [[ -z "${infra_id}" ]]; then
		log_error "hack_cleanup_containers requires cloud and infra_id parameters"
		return 1
	fi

	log_info "Cleaning up OpenStack containers for infrastructure: ${infra_id}"

	# Fetch the container list once; reuse it for both the error check and iteration.
	local container_list_output
	if ! container_list_output=$(openstack --os-cloud="${cloud}" container list --format value -c Name 2>&1); then
		log_warning "Failed to list containers: ${container_list_output}"
		return 0
	fi

	if [[ -z "${container_list_output}" ]]; then
		log_info "No containers found for infrastructure: ${infra_id}"
		return 0
	fi

	local container_count=0
	local object_count=0

	while IFS= read -r container; do
		[[ -z "${container}" ]] && continue
		# Skip containers that don't belong to this cluster.
		[[ "${container}" != *"${infra_id}"* ]] && continue

		container_count=$(( container_count + 1 ))
		log_info "Processing container: ${container}"

		while IFS= read -r object; do
			[[ -z "${object}" ]] && continue

			object_count=$(( object_count + 1 ))
			log_info "Deleting object: ${object} from container: ${container}"

			if ! openstack --os-cloud="${cloud}" object delete "${container}" "${object}"; then
				log_warning "Failed to delete object: ${object}"
			fi
		done < <(openstack --os-cloud="${cloud}" object list "${container}" --format value -c Name 2>/dev/null)

		log_info "Deleting container: ${container}"
		if ! openstack --os-cloud="${cloud}" container delete "${container}"; then
			log_warning "Failed to delete container: ${container}"
		fi
	done <<< "${container_list_output}"

	log_info "Container cleanup complete. Processed ${container_count} containers and ${object_count} objects"
	return 0
}

#------------------------------------------------------------------------------
# Send the delete-metadata command to the PowerVC controller.
#------------------------------------------------------------------------------
function delete_metadata() {
	local metadata_file="${TEMP_DIR}/metadata.json"

	if [[ ! -f "${metadata_file}" ]]; then
		log_warning "Metadata file not found: ${metadata_file}"
		log_info "Skipping metadata deletion step"
		return 0
	fi

	log_info "Deleting metadata from controller..."

	execute_with_check "Metadata deletion" \
		"${POWERVC_TOOL}" \
		send-metadata \
		--deleteMetadata "${metadata_file}" \
		--serverIP "${CONTROLLER_IP}" \
		--shouldDebug true
}

#------------------------------------------------------------------------------
# Require the user to type the cluster name as confirmation, then clean up
# object-storage containers and destroy the cluster.
# Ctrl-C during the prompt is caught by the SIGINT trap.
#------------------------------------------------------------------------------
function destroy_cluster() {
	log_info "Destroying OpenShift cluster..."
	log_warning "This operation will permanently delete all cluster resources"
	echo ""

	local confirm_name=""
	while [[ "${confirm_name}" != "${SELECTED_CLUSTER}" ]]; do
		read -rp "Type the cluster name to confirm deletion (or Ctrl-C to abort): " confirm_name
		if [[ "${confirm_name}" != "${SELECTED_CLUSTER}" ]]; then
			log_warning "Name does not match — expected: ${SELECTED_CLUSTER}"
		fi
	done

	if ! hack_cleanup_containers "${CLOUD}" "${INFRAID}"; then
		log_warning "Container cleanup encountered issues, continuing with cluster destroy"
	fi

	execute_with_check "Cluster destruction" \
		openshift-install destroy cluster \
		--dir="${CLUSTER_DIR}" \
		--log-level=debug
}

#------------------------------------------------------------------------------
# SIGINT trap: log a clear interruption message then let the EXIT trap run.
#------------------------------------------------------------------------------
function handle_interrupt() {
	echo "" >&2
	log_warning "Interrupted by user (Ctrl-C) — cleaning up ..."
	exit 130
}

#------------------------------------------------------------------------------
# EXIT trap: on failure, restore metadata.json so re-runs are possible.
# Always cleans up TEMP_DIR. CLUSTER_DIR is intentionally left for the user
# to inspect if something went wrong.
#------------------------------------------------------------------------------
function cleanup_on_exit() {
	local exit_code=$?

	# Exit 130 = SIGINT (Ctrl-C): a deliberate user interruption, not a failure.
	if [[ ${exit_code} -ne 0 ]] && [[ ${exit_code} -ne 130 ]]; then
		log_error "Script failed with exit code ${exit_code}"
	fi

	# Restore metadata.json on non-zero exit so re-runs are possible.
	# Guard CLUSTER_DIR non-empty to avoid accidentally writing to "/" or ".".
	if [[ -n "${TEMP_DIR}" ]] && [[ -d "${TEMP_DIR}" ]]; then
		if [[ ${exit_code} -gt 0 ]] \
			&& [[ -n "${CLUSTER_DIR}" ]] \
			&& [[ ! -f "${CLUSTER_DIR}/metadata.json" ]]; then
			log_info "Restoring metadata.json to ${CLUSTER_DIR}/ ..."
			mkdir -p "${CLUSTER_DIR}"
			/bin/cp "${TEMP_DIR}/metadata.json" "${CLUSTER_DIR}/"
		fi
		/bin/rm -rf "${TEMP_DIR}"
	fi

	# Only remove CLUSTER_DIR on clean exit; leave it in place on failure for
	# post-mortem inspection.
	if [[ ${exit_code} -eq 0 ]] && [[ -n "${CLUSTER_DIR}" ]] && [[ -d "${CLUSTER_DIR}" ]]; then
		/bin/rm -rf "${CLUSTER_DIR}"
	fi
}

#==============================================================================
# Argument Parsing  —  parsed before trap so -h exits without triggering cleanup
#==============================================================================

usage() {
	echo "Usage: ${SCRIPT_NAME} [-c <cloud>] [-l]" >&2
	echo ""                                          >&2
	echo "  -c <cloud>   OpenStack cloud name (skips interactive prompt)" >&2
	echo "  -l           List clusters only; skip deletion prompt"               >&2
	echo "  -h           Show this help and exit"                                >&2
	exit 0
}

# Parse arguments before registering the EXIT trap so that -h exits cleanly
# without running cleanup_on_exit.
while getopts ":c:lh" opt; do
	case $opt in
		c) CLOUD="$OPTARG" ;;
		l) LIST_ONLY=true ;;
		h) usage ;;
		:) echo "Error: -${OPTARG} requires an argument." >&2; exit 1 ;;
		\?) echo "Error: unknown option -${OPTARG}." >&2; exit 1 ;;
	esac
done

# Register traps now that argument parsing (including -h) is complete.
trap cleanup_on_exit EXIT
trap handle_interrupt INT

#==============================================================================
# Initialise
#==============================================================================

# collect_cloud_name prompts if CLOUD is still unset/empty after getopts.
collect_cloud_name

if [[ "${LIST_ONLY}" == "false" ]]; then
	initialize_powervc_tool
fi
check_required_programs
echo ""

#==============================================================================
# Fetch Server List
#==============================================================================

log_info "Fetching server list from cloud: ${CLOUD} ..."

# Separate the call from the assignment so the || die path is reachable under
# set -e (a failed command substitution inside $() does not trigger errexit on
# the assignment line itself).
# Redirect stderr to a temp file so openstack warnings reach the terminal on
# success while still being captured for the error message on failure.
_err_tmp=$(mktemp)
if ! raw_csv=$(openstack --os-cloud="${CLOUD}" server list --format=csv 2>"${_err_tmp}"); then
	die "openstack server list failed: $(cat "${_err_tmp}")"
fi
rm -f "${_err_tmp}"; unset _err_tmp

# Read all CSV lines into an array once — used for the empty check and the
# parse loop below, avoiding repeated forks.
mapfile -t _csv_lines <<< "${raw_csv}"
unset raw_csv   # fully consumed into _csv_lines; free the memory
if [[ ${#_csv_lines[@]} -le 1 ]]; then
	log_warning "No servers found."
	exit 0
fi

#==============================================================================
# Deduplicate Cluster Names and Count Nodes
#==============================================================================

# Server names follow the pattern: p-<hash>-<cluster_name>-(master|worker|bootstrap)[N]
declare -A seen             # cluster_name -> 1
declare -a clusters         # ordered list of unique cluster names
declare -A master_count     # cluster_name -> integer
declare -A worker_count     # cluster_name -> integer
declare -A bootstrap_count  # cluster_name -> integer

# Iterate from index 1 to skip the CSV header — no subshell or tail fork needed.
for (( _i=1; _i<${#_csv_lines[@]}; _i++ )); do
	IFS=',' read -r _id name _rest <<< "${_csv_lines[$_i]}"
	name="${name//\"/}"
	if [[ "$name" =~ ^(p-[a-f0-9-]+-[a-z0-9]+)-(master|worker|bootstrap)([0-9]*) ]]; then
		cluster_name="${BASH_REMATCH[1]}"
		role="${BASH_REMATCH[2]}"

		if [[ -z "${seen[$cluster_name]+set}" ]]; then
			seen[$cluster_name]=1
			clusters+=("$cluster_name")
			master_count[$cluster_name]=0
			worker_count[$cluster_name]=0
			bootstrap_count[$cluster_name]=0
		fi

		case "${role}" in
			master)    master_count[$cluster_name]=$(( master_count[$cluster_name] + 1 ))       ;;
			worker)    worker_count[$cluster_name]=$(( worker_count[$cluster_name] + 1 ))       ;;
			bootstrap) bootstrap_count[$cluster_name]=$(( bootstrap_count[$cluster_name] + 1 )) ;;
		esac
	fi
done
unset _csv_lines _i

if [[ ${#clusters[@]} -eq 0 ]]; then
	log_warning "No OpenShift clusters found."
	exit 0
fi

#==============================================================================
# Display Cluster List
#==============================================================================

# Compute column width dynamically from the longest cluster name.
_col_w=7  # minimum — length of "Cluster" header
for cname in "${clusters[@]}"; do
	[[ ${#cname} -gt ${_col_w} ]] && _col_w=${#cname}
done
unset cname

echo ""
echo "Available OpenShift clusters:"
echo ""
printf "  %-4s  %-${_col_w}s  %s\n" "No." "Cluster" "Nodes (m/w/b)"
printf "  %-4s  %-${_col_w}s  %s\n" "----" "$(printf '%*s' "${_col_w}" '' | tr ' ' '-')" "-------------"
for i in "${!clusters[@]}"; do
	cname="${clusters[$i]}"
	node_summary="${master_count[$cname]}m / ${worker_count[$cname]}w / ${bootstrap_count[$cname]}b"
	printf "  %-4s  %-${_col_w}s  %s\n" "$(( i + 1 )))" "${cname}" "${node_summary}"
done
echo ""

if [[ "${LIST_ONLY}" == "true" ]]; then
	exit 0
fi

#==============================================================================
# Numbered Prompt
#==============================================================================

SELECTED_CLUSTER=""
while [[ -z "${SELECTED_CLUSTER}" ]]; do
	read -rp "Enter cluster number [1-${#clusters[@]}]: " choice
	if [[ "${choice}" =~ ^[0-9]+$ ]] && (( choice >= 1 && choice <= ${#clusters[@]} )); then
		SELECTED_CLUSTER="${clusters[$(( choice - 1 ))]}"
	else
		log_warning "Invalid selection — enter a number between 1 and ${#clusters[@]}."
	fi
done

export SELECTED_CLUSTER
echo ""
log_success "Selected cluster: ${SELECTED_CLUSTER}"

#==============================================================================
# Write Metadata
#==============================================================================

CLUSTER_DIR=$(mktemp -d)
export CLUSTER_DIR

jq -n \
	--arg name  "${SELECTED_CLUSTER}" \
	--arg cloud "${CLOUD}" \
	'{
		clusterName: $name,
		clusterID:   $name,
		infraID:     $name,
		powervc: {
			cloud: $cloud,
			identifier: { openshiftClusterID: $name }
		},
		featureSet:       "",
		customFeatureSet: null
	}' > "${CLUSTER_DIR}/metadata.json"

# Read INFRAID back from the written file — single source of truth.
INFRAID=$(jq -r '.infraID' "${CLUSTER_DIR}/metadata.json")
export INFRAID

log_info "Cluster metadata written to: ${CLUSTER_DIR}/metadata.json"
log_info "Metadata contents:"
# Capture jq output first so pipeline errors are detectable under set -eo pipefail.
_metadata_pretty=$(jq . "${CLUSTER_DIR}/metadata.json")
while IFS= read -r line; do log_info "  ${line}"; done <<< "${_metadata_pretty}"
unset _metadata_pretty
echo ""

#==============================================================================
# Collect Controller IP and Verify Connectivity
#==============================================================================

collect_controller_ip
verify_controller
echo ""

#==============================================================================
# Confirm and Delete Cluster
#==============================================================================

log_info "Cluster deletion summary:"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  Cluster Name:       ${SELECTED_CLUSTER}"
echo "  Infrastructure ID:  ${INFRAID}"
echo "  Masters / Workers / Bootstrap:  ${master_count[$SELECTED_CLUSTER]}m / ${worker_count[$SELECTED_CLUSTER]}w / ${bootstrap_count[$SELECTED_CLUSTER]}b"
echo "  Cluster Directory:  ${CLUSTER_DIR}"
echo "  Controller IP:      ${CONTROLLER_IP}"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""

# Back up metadata before destruction so delete_metadata and cleanup_on_exit
# can use it after openshift-install has finished with CLUSTER_DIR.
TEMP_DIR=$(mktemp -d)
/bin/cp "${CLUSTER_DIR}/metadata.json" "${TEMP_DIR}/"

destroy_cluster
echo ""

delete_metadata
echo ""

log_success "Cluster deletion completed successfully!"
log_info "All cluster resources have been removed"
