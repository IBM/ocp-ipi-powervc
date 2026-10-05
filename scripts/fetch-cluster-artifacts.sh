#!/usr/bin/env bash

# Copyright 2026 IBM Corp
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

################################################################################
# Script: fetch-cluster-artifacts.sh
# Description: Fetch cluster artifacts from a CI pod and controller host
#
# This script automates the retrieval of cluster artifacts from an OpenShift
# CI installation pod, including:
#   - Extracting the INFRA ID from the pod logs
#   - Fetching metadata.json and kubeconfig from the pod via oc rsh (Running pods)
#   - Falling back to SCP from the controller for metadata.json when the pod has
#     already finished (kubeconfig is only accessible while the pod is Running)
#
# The script replicates the following manual workflow:
#   oc logs -c <container> pod/<pod-name> | grep INFRAID=
#   INFRAID=<value>
#   scp -r -i ~/.ssh/id_powervc cloud-user@<controller-ip>:/home/cloud-user/${INFRAID}/ .
#   (export CLUSTER_DIR="${INFRAID}"; mkdir -p "${CLUSTER_DIR}/auth/"; \
#    oc rsh -c <container> pod/<pod-name> /bin/bash -c \
#    "cat /tmp/installer/auth/kubeconfig" > "${CLUSTER_DIR}/auth/kubeconfig")
#
# Environment Variables:
#   POD_NAME        - Name of the CI pod (e.g., ocp-e2e-ovn-powervc-multi-p-p-ipi-install-powervc-install)
#   CONTAINER_NAME  - Container name within the pod (default: test)
#   CONTROLLER_IP   - IP address of the controller host; sourced automatically
#                     from an environment file (e.g. environmentCI) or set explicitly
#   SSH_KEY         - Path to the SSH private key for controller access (falls back to BASTION_RSA,
#                     then ~/.ssh/id_powervc)
#   BASTION_RSA     - SSH private key path from a sourced environment file (e.g. environmentCI);
#                     used as SSH_KEY when SSH_KEY is not explicitly set
#   CONTROLLER_USER - Username on the controller host (default: cloud-user)
#   INFRAID         - Infra ID override; if set, skips log extraction
#   POLL_INTERVAL   - Seconds between polls for pod detection and log scanning (default: 10)
#   POLL_TIMEOUT    - Maximum seconds to wait for each polling operation (default: 300)
#
# Dependencies:
#   - oc: OpenShift CLI client
#   - scp: Secure copy program
#
# Exit Codes:
#   0 - Artifacts fetched successfully
#   1 - Validation failure or fetch error
#
# Usage Examples:
#   # Minimal — prompts for pod name and controller IP
#   ./fetch-cluster-artifacts.sh
#
#   # With environment variables
#   export POD_NAME=ocp-e2e-ovn-powervc-multi-p-p-ipi-install-powervc-install
#   export CONTROLLER_IP=10.130.41.245
#   ./fetch-cluster-artifacts.sh
#
#   # Override container name and SSH key
#   export CONTAINER_NAME=installer
#   export SSH_KEY=~/.ssh/id_rsa
#   ./fetch-cluster-artifacts.sh
#
#   # Skip log extraction by providing INFRAID directly
#   export INFRAID=p-892f089c-91a5-4d18-p8khs
#   export CONTROLLER_IP=10.130.41.245
#   ./fetch-cluster-artifacts.sh
#
# Process Flow:
#   1. Validate required programs
#   2. Collect inputs: poll for running install pod, prompt for CONTROLLER_IP
#      if not already set, validate SSH key
#   3. Extract INFRA ID from pod logs (skipped if INFRAID is already set)
#   4. Fetch metadata.json and kubeconfig from the pod via oc rsh (Running pod);
#      if the pod has already finished, warns and skips the kubeconfig, then
#      falls back to SCP from the controller for metadata.json
#   5. SCP artifact directory from controller host only when metadata.json was
#      not already retrieved from the pod
#
################################################################################

set -euo pipefail

#==============================================================================
# Global Variables
#==============================================================================
readonly SCRIPT_NAME="$(basename "${BASH_SOURCE[0]}")"

# ANSI color codes for enhanced terminal output
readonly COLOR_RED='\033[0;31m'      # Error messages
readonly COLOR_GREEN='\033[0;32m'    # Success messages
readonly COLOR_YELLOW='\033[1;33m'   # Warning messages
readonly COLOR_BLUE='\033[0;34m'     # Info messages
readonly COLOR_RESET='\033[0m'       # Reset to default

# Debug mode
DEBUG="${DEBUG:-false}"
# Normalize DEBUG to true/false
[[ "${DEBUG}" == "true" ]] && DEBUG=true || DEBUG=false

#==============================================================================
# Utility Functions
#==============================================================================

#------------------------------------------------------------------------------
# log_info - Print informational message with blue color
#
# Outputs an informational message to stdout with [INFO] prefix in blue color.
#
# Arguments:
#   $* - Message text to display
#
# Returns:
#   0 - Always succeeds
#
# Example:
#   log_info "Starting process..."
#------------------------------------------------------------------------------
function log_info() {
	echo -e "${COLOR_BLUE}[INFO]${COLOR_RESET} $*"
}

#------------------------------------------------------------------------------
# log_success - Print success message with green color
#
# Outputs a success message to stdout with [SUCCESS] prefix in green color.
#
# Arguments:
#   $* - Message text to display
#
# Returns:
#   0 - Always succeeds
#
# Example:
#   log_success "Operation completed successfully"
#------------------------------------------------------------------------------
function log_success() {
	echo -e "${COLOR_GREEN}[SUCCESS]${COLOR_RESET} $*"
}

#------------------------------------------------------------------------------
# log_warning - Print warning message with yellow color
#
# Outputs a warning message to stdout with [WARNING] prefix in yellow color.
#
# Arguments:
#   $* - Message text to display
#
# Returns:
#   0 - Always succeeds
#
# Example:
#   log_warning "Configuration file not found, using defaults"
#------------------------------------------------------------------------------
function log_warning() {
	echo -e "${COLOR_YELLOW}[WARNING]${COLOR_RESET} $*"
}

#------------------------------------------------------------------------------
# log_error - Print error message with red color
#
# Outputs an error message to stderr with [ERROR] prefix in red color.
#
# Arguments:
#   $* - Error message text to display
#
# Returns:
#   0 - Always succeeds (does not exit)
#
# Example:
#   log_error "Failed to connect to server"
#------------------------------------------------------------------------------
function log_error() {
	echo -e "${COLOR_RED}[ERROR]${COLOR_RESET} $*" >&2
}

#------------------------------------------------------------------------------
# log_debug - Print debug message when DEBUG mode is enabled
#
# Outputs a debug message to stdout with [DEBUG] prefix in yellow color.
# Only displays output when the global DEBUG variable is set to true.
#
# Arguments:
#   $* - Debug message text to display
#
# Returns:
#   0 - Always succeeds
#
# Globals:
#   DEBUG - Controls whether debug messages are displayed
#
# Example:
#   log_debug "Variable value: ${my_var}"
#------------------------------------------------------------------------------
function log_debug() {
	if ${DEBUG}; then
		echo -e "${COLOR_YELLOW}[DEBUG]${COLOR_RESET} $*"
	fi
}

#------------------------------------------------------------------------------
# die - Print error message and exit with failure status
#
# Logs an error message to stderr and terminates the script with exit code 1.
# This function should be used for fatal errors that prevent script continuation.
#
# Arguments:
#   $* - Error message text to display before exiting
#
# Returns:
#   Never returns (exits with code 1)
#
# Example:
#   die "Required file not found: ${config_file}"
#------------------------------------------------------------------------------
function die() {
	log_error "$*"
	exit 1
}

#------------------------------------------------------------------------------
# command_exists - Check if a command is available in PATH
#
# Tests whether a given command exists and is executable in the current PATH.
#
# Arguments:
#   $1 - Command name to check
#
# Returns:
#   0 - Command exists and is executable
#   1 - Command not found
#
# Example:
#   if command_exists "oc"; then
#       echo "oc is installed"
#   fi
#------------------------------------------------------------------------------
function command_exists() {
	command -v "$1" >/dev/null 2>&1
}

#------------------------------------------------------------------------------
# validate_non_empty - Validate that a variable is set and non-empty
#
# Checks if a named variable is set and contains a non-empty value.
# Terminates the script with an error if the variable is empty or unset.
#
# Arguments:
#   $1 - Name of the variable to validate (not the value)
#
# Returns:
#   0 - Variable is set and non-empty
#   Never returns if validation fails (calls die)
#
# Example:
#   validate_non_empty "CONTROLLER_IP"
#------------------------------------------------------------------------------
function validate_non_empty() {
	local var_name="$1"
	local var_value="${!var_name:-}"

	if [[ -z "${var_value}" ]]; then
		die "${var_name} must be set and non-empty"
	fi
}

#------------------------------------------------------------------------------
# prompt_input - Prompt user for input with optional default and validation
#
# Displays a prompt to the user and reads input from stdin. Supports default
# values and optional empty input validation. The input value is stored in
# the specified variable and exported to the environment.
#
# Arguments:
#   $1 - Prompt text to display to the user
#   $2 - Name of variable to store the input value
#   $3 - Default value (optional, shown in brackets if provided)
#   $4 - Allow empty input: "true" or "false" (optional, default: "false")
#
# Returns:
#   0 - Input successfully collected and stored
#   Never returns if validation fails and allow_empty is false (calls die)
#
# Globals:
#   Sets and exports the variable named in $2
#
# Example:
#   prompt_input "Enter pod name" "POD_NAME"
#   prompt_input "Enter container name" "CONTAINER_NAME" "test"
#------------------------------------------------------------------------------
function prompt_input() {
	local prompt_text="$1"
	local var_name="$2"
	local default_value="${3:-}"
	local allow_empty="${4:-false}"

	local input_value

	if [[ -n "${default_value}" ]]; then
		read -rp "${prompt_text} [${default_value}]: " input_value
		input_value="${input_value:-${default_value}}"
	else
		read -rp "${prompt_text}: " input_value
	fi

	if [[ -z "${input_value}" ]] && [[ "${allow_empty}" != "true" ]]; then
		die "You must enter a value for ${var_name}"
	fi

	printf -v "${var_name}" '%s' "${input_value}"
	export "${var_name}"
}

#==============================================================================
# Main Functions
#==============================================================================

#------------------------------------------------------------------------------
# show_usage - Print command help and usage examples
#
# Displays command syntax, supported options, relevant environment variables,
# and common invocation examples. This function writes help text to stdout.
#
# Arguments:
#   None
#
# Returns:
#   0 - Help text written successfully
#
# Example:
#   show_usage
#------------------------------------------------------------------------------
function show_usage() {
	cat <<EOF
Usage: ${SCRIPT_NAME} [--help]

Fetch cluster artifacts from a CI pod and controller host.

Options:
  --help, -h    Show this help message

Environment Variables:
  POD_NAME         Name of the CI pod containing the installer logs
  CONTAINER_NAME   Container name within the pod (default: test)
  CONTROLLER_IP    IP address of the controller host
  SSH_KEY          Path to SSH private key for controller access (falls back to BASTION_RSA, then ~/.ssh/id_powervc)
  BASTION_RSA      SSH private key path from a sourced environment file (e.g. environmentCI)
  CONTROLLER_USER  Username on the controller host (default: cloud-user)
  INFRAID          Infra ID override; if set, skips log extraction
  POLL_INTERVAL    Seconds between polls for pod detection and log scanning (default: 10)
  POLL_TIMEOUT     Maximum seconds to wait for each polling operation (default: 300)
  DEBUG            Enable debug output (default: false)

Examples:
  # Source an environment file then run (CONTROLLER_IP set automatically)
  source environmentCI && ${SCRIPT_NAME}

  # With explicit environment variables
  export POD_NAME=ocp-e2e-ovn-powervc-multi-p-p-ipi-install-powervc-install
  export CONTROLLER_IP=10.130.41.245
  ${SCRIPT_NAME}

  # Override container name and SSH key
  export CONTAINER_NAME=installer
  export SSH_KEY=~/.ssh/id_rsa
  export CONTROLLER_IP=10.130.41.245
  ${SCRIPT_NAME}

  # Skip log extraction by providing INFRAID directly
  export INFRAID=p-892f089c-91a5-4d18-p8khs
  export CONTROLLER_IP=10.130.41.245
  ${SCRIPT_NAME}
EOF
}

#------------------------------------------------------------------------------
# parse_arguments - Parse CLI options
#
# Processes command-line arguments, handles the help flag, and warns about
# any unrecognised options.
#
# Arguments:
#   $@ - Command-line arguments passed to the script
#
# Returns:
#   0 - Arguments parsed successfully
#   Never returns on --help (exits 0) or unknown option (calls die)
#
# Example:
#   parse_arguments "$@"
#------------------------------------------------------------------------------
function parse_arguments() {
	while [[ $# -gt 0 ]]; do
		case "$1" in
			--help|-h)
				show_usage
				exit 0
				;;
			-*)
				die "Unknown option: $1"
				;;
			*)
				log_warning "Ignoring unexpected argument: $1"
				;;
		esac
		shift
	done
}

#------------------------------------------------------------------------------
# check_required_programs - Verify external runtime dependencies are available
#
# Ensures that all required commands used by this script are present in PATH
# before any remote operations are attempted. Missing commands are accumulated
# and reported together to make remediation easier.
#
# Arguments:
#   None
#
# Returns:
#   0 - All required programs are available
#   Never returns if one or more programs are missing (calls die)
#
# Example:
#   check_required_programs
#------------------------------------------------------------------------------
function check_required_programs() {
	local -a required_programs=("oc" "scp")
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
# detect_install_pod - Detect running or completed install pod(s) via oc get pods
#
# Queries the current namespace for pods whose name ends in "-install" and
# whose status is "Running", "Completed", or "Succeeded". If exactly one such
# pod is found it is used automatically. If multiple matches are found, the
# user is prompted to choose. If none are found the function returns a non-zero
# exit code so the caller can retry or fall back to a manual prompt.
#
# Arguments:
#   None
#
# Returns:
#   0 - POD_NAME set to the detected pod name
#   1 - No running or completed install pod found
#   Never returns if an invalid pod selection is made (calls die)
#
# Globals Set:
#   POD_NAME - Name of the detected install pod
#
# Example:
#   detect_install_pod
#------------------------------------------------------------------------------
function detect_install_pod() {
	local -a candidates
	mapfile -t candidates < <(
		oc get pods --no-headers 2>/dev/null \
		| awk '$3 ~ /^(Running|Completed|Succeeded)$/ && $1 ~ /-install$/ { print $1 }'
	) || true

	if [[ ${#candidates[@]} -eq 0 ]]; then
		return 1
	fi

	if [[ ${#candidates[@]} -eq 1 ]]; then
		POD_NAME="${candidates[0]}"
		export POD_NAME
		log_success "Auto-detected install pod: ${POD_NAME}"
		return 0
	fi

	# Multiple matches — let the user pick
	log_warning "Multiple running install pods found:"
	local i
	for i in "${!candidates[@]}"; do
		echo "  $((i + 1))) ${candidates[${i}]}"
	done

	local choice
	read -rp "Select pod number [1]: " choice
	choice="${choice:-1}"

	if ! [[ "${choice}" =~ ^[0-9]+$ ]] || \
	   [[ "${choice}" -lt 1 ]] || \
	   [[ "${choice}" -gt ${#candidates[@]} ]]; then
		die "Invalid selection: ${choice}"
	fi

	POD_NAME="${candidates[$((choice - 1))]}"
	export POD_NAME
	log_success "Selected install pod: ${POD_NAME}"
	return 0
}

#------------------------------------------------------------------------------
# wait_for_install_pod - Poll until a running install pod appears
#
# Repeatedly calls detect_install_pod every POLL_INTERVAL seconds until a
# running pod whose name ends in "-install" is found or POLL_TIMEOUT seconds
# have elapsed. Prints a progress dot each interval while waiting.
#
# Arguments:
#   None
#
# Returns:
#   0 - POD_NAME set to the detected pod name
#   Never returns if the timeout is reached (calls die)
#
# Globals Read:
#   POLL_INTERVAL - Seconds between polls; resolved and exported by collect_inputs
#   POLL_TIMEOUT  - Maximum seconds to wait; resolved and exported by collect_inputs
#
# Globals Set:
#   POD_NAME - Name of the detected install pod
#
# Example:
#   wait_for_install_pod
#------------------------------------------------------------------------------
function wait_for_install_pod() {
	local interval="${POLL_INTERVAL}"
	local timeout="${POLL_TIMEOUT}"
	local elapsed=0
	local dots_printed=false

	log_info "Waiting for a running or completed install pod (timeout: ${timeout}s, interval: ${interval}s)..."

	while [[ "${elapsed}" -lt "${timeout}" ]]; do
		if detect_install_pod; then
			${dots_printed} && echo ""
			return 0
		fi
		printf '.'
		dots_printed=true
		sleep "${interval}"
		elapsed=$(( elapsed + interval ))
	done

	echo ""
	die "Timed out after ${timeout}s waiting for a running or completed install pod"
}

#------------------------------------------------------------------------------
# collect_inputs - Gather and validate all required inputs
#
# Prompts for any inputs not already set via environment variables, then
# applies defaults for optional parameters. Validates that mandatory values
# are non-empty before proceeding. When POD_NAME is not set, polls for a
# running pod whose name ends in "-install" via oc get pods; times out via
# die if no pod appears within POLL_TIMEOUT seconds.
#
# Arguments:
#   None
#
# Returns:
#   0 - All inputs collected and validated
#   Never returns if a required value is missing (calls die)
#
# Globals Set/Read:
#   POD_NAME        - Name of the CI pod (polled or prompted if unset)
#   CONTAINER_NAME  - Container within the pod (default: test)
#   CONTROLLER_IP   - IP address of the controller host (prompted if unset)
#   SSH_KEY         - Path to SSH private key (falls back to BASTION_RSA, then ~/.ssh/id_powervc)
#   BASTION_RSA     - Sourced from an environment file; used as SSH_KEY fallback
#   CONTROLLER_USER - Username on the controller host (default: cloud-user)
#   INFRAID         - Infra ID (optional override; skips log extraction when set)
#   POLL_INTERVAL   - Seconds between polls for pod detection and log scanning
#                     (default: 10; validated as positive integer and exported here)
#   POLL_TIMEOUT    - Maximum seconds to wait for each polling operation
#                     (default: 300; validated as positive integer and exported here)
#
# Example:
#   collect_inputs
#------------------------------------------------------------------------------
function collect_inputs() {
	log_info "Collecting inputs..."

	# POLL_INTERVAL / POLL_TIMEOUT — validate once here before any polling begins,
	# then export the resolved values so all downstream functions use the same integers
	POLL_INTERVAL="${POLL_INTERVAL:-10}"
	POLL_TIMEOUT="${POLL_TIMEOUT:-300}"
	if ! [[ "${POLL_INTERVAL}" =~ ^[1-9][0-9]*$ ]]; then
		die "POLL_INTERVAL must be a positive integer, got: ${POLL_INTERVAL}"
	fi
	if ! [[ "${POLL_TIMEOUT}" =~ ^[1-9][0-9]*$ ]]; then
		die "POLL_TIMEOUT must be a positive integer, got: ${POLL_TIMEOUT}"
	fi
	export POLL_INTERVAL POLL_TIMEOUT

	# POD_NAME — poll until a running install pod appears or the timeout is reached
	if [[ -z "${POD_NAME:-}" ]]; then
		wait_for_install_pod
	fi

	# CONTAINER_NAME — optional, default: test
	CONTAINER_NAME="${CONTAINER_NAME:-test}"
	log_debug "CONTAINER_NAME=${CONTAINER_NAME}"

	# CONTROLLER_IP — required for SCP; sourced from environment file or prompted
	if [[ -z "${CONTROLLER_IP:-}" ]]; then
		prompt_input "Enter the controller IP address" "CONTROLLER_IP"
	fi
	validate_non_empty "CONTROLLER_IP"

	# SSH_KEY — falls back to BASTION_RSA from a sourced environment file, then ~/.ssh/id_powervc
	SSH_KEY="${SSH_KEY:-${BASTION_RSA:-${HOME}/.ssh/id_powervc}}"
	# Expand a leading tilde so paths like ~/... work when set via export
	SSH_KEY="${SSH_KEY/#\~/$HOME}"
	log_debug "SSH_KEY=${SSH_KEY}"

	if [[ ! -f "${SSH_KEY}" ]]; then
		die "SSH key not found: ${SSH_KEY}"
	fi

	# Warn if key permissions are broader than 600/400 — scp will refuse to use it
	local key_perms
	key_perms=$(stat -c '%a' "${SSH_KEY}" 2>/dev/null || stat -f '%Lp' "${SSH_KEY}" 2>/dev/null || true)
	if [[ -n "${key_perms}" && "${key_perms}" != "600" && "${key_perms}" != "400" ]]; then
		log_warning "SSH key has insecure permissions (${key_perms}): ${SSH_KEY}"
		log_warning "Consider running: chmod 600 ${SSH_KEY}"
	fi

	# CONTROLLER_USER — optional, default: cloud-user
	# Note: the controller VM and bastion VM are distinct hosts but currently share
	# the same CentOS default username; override when the environment separates them
	CONTROLLER_USER="${CONTROLLER_USER:-cloud-user}"
	log_debug "CONTROLLER_USER=${CONTROLLER_USER}"

	log_success "Inputs collected"
	log_debug "POD_NAME=${POD_NAME}"
	log_debug "CONTROLLER_IP=${CONTROLLER_IP}"
}

#------------------------------------------------------------------------------
# extract_infraid - Poll pod logs until INFRA ID appears
#
# Polls the pod logs every POLL_INTERVAL seconds until a line containing
# INFRAID= is found or POLL_TIMEOUT seconds have elapsed. The value is
# extracted and stored in the global INFRAID variable. If INFRAID is already
# set in the environment, this step is skipped entirely.
#
# oc logs failures (e.g. pod not yet ready) are suppressed with || true so
# the retry loop continues; grep's non-zero exit on no-match is likewise
# suppressed. The loop only aborts if the timeout is reached.
#
# Arguments:
#   None
#
# Returns:
#   0 - INFRAID extracted or already set
#   Never returns if the timeout is reached (calls die)
#
# Globals Read:
#   CONTAINER_NAME - Container name within the pod
#   POD_NAME       - Name of the CI pod
#   POLL_INTERVAL  - Seconds between polls; resolved and exported by collect_inputs
#   POLL_TIMEOUT   - Maximum seconds to wait; resolved and exported by collect_inputs
#
# Globals Set:
#   INFRAID - Infrastructure ID parsed from the pod log (exported)
#
# Example:
#   extract_infraid
#------------------------------------------------------------------------------
function extract_infraid() {
	if [[ -n "${INFRAID:-}" ]]; then
		log_info "Using provided INFRAID: ${INFRAID}"
		return 0
	fi

	local interval="${POLL_INTERVAL}"
	local timeout="${POLL_TIMEOUT}"
	local elapsed=0

	log_info "Waiting for INFRAID= in pod/${POD_NAME} logs (timeout: ${timeout}s, interval: ${interval}s)..."

	local dots_printed=false

	while [[ "${elapsed}" -lt "${timeout}" ]]; do
		local pod_logs
		pod_logs=$(oc logs -c "${CONTAINER_NAME}" "pod/${POD_NAME}" 2>/dev/null) || true

		local log_line
		log_line=$(grep 'INFRAID=' <<< "${pod_logs}" | tail -1 || true)

		if [[ -n "${log_line}" ]]; then
			log_debug "log_line=${log_line}"

			# Extract the value after INFRAID= and strip any trailing whitespace
			# ([[:space:]] includes \r so a separate CR-trim is not needed)
			INFRAID="${log_line##*INFRAID=}"
			INFRAID="${INFRAID%%[[:space:]]*}"

			if [[ -z "${INFRAID}" ]]; then
				die "Parsed INFRAID is empty from line: ${log_line}"
			fi
			export INFRAID

			${dots_printed} && echo ""
			log_success "INFRAID=${INFRAID}"
			return 0
		fi

		printf '.'
		dots_printed=true
		sleep "${interval}"
		elapsed=$(( elapsed + interval ))
	done

	echo ""
	die "Timed out after ${timeout}s waiting for INFRAID= in logs of pod/${POD_NAME} (container: ${CONTAINER_NAME})"
}

#------------------------------------------------------------------------------
# rsh_file - Stream a single file from a pod container via oc rsh into a local path
#
# Runs oc rsh to cat a remote file into a local temporary file, then moves
# it atomically to the destination only if the content is non-empty.
# On any failure the temporary file is removed and the function returns 1
# so callers can decide whether to die or fall back.
#
# Arguments:
#   $1 - Remote path inside the container (e.g. /tmp/installer/auth/kubeconfig)
#   $2 - Local destination path
#
# Returns:
#   0 - File written successfully
#   1 - oc rsh failed or returned empty content
#
# Globals Read:
#   CONTAINER_NAME - Container name within the pod
#   POD_NAME       - Name of the CI pod
#
# Example:
#   rsh_file /tmp/installer/metadata.json "${INFRAID}/metadata.json"
#------------------------------------------------------------------------------
function rsh_file() {
	local remote_path="$1"
	local dest_path="$2"

	local tmp
	tmp=$(mktemp)

	if ! oc rsh -c "${CONTAINER_NAME}" "pod/${POD_NAME}" \
		/bin/bash -c "cat ${remote_path}" \
		> "${tmp}" 2>/dev/null; then
		rm -f "${tmp}"
		return 1
	fi

	if [[ ! -s "${tmp}" ]]; then
		rm -f "${tmp}"
		return 1
	fi

	mkdir -p "$(dirname "${dest_path}")"
	mv "${tmp}" "${dest_path}"
	return 0
}

#------------------------------------------------------------------------------
# fetch_pod_artifacts - Retrieve metadata.json and kubeconfig from the pod
#
# Checks the pod phase first. If the pod is Running, uses oc rsh to fetch
# both /tmp/installer/metadata.json and /tmp/installer/auth/kubeconfig.
#
# If the pod has already finished, only the kubeconfig is inaccessible (it
# lives only inside the container); a warning is emitted for it and the
# function returns 1 so main() knows to fall back to SCP for metadata.json.
# A finished pod also returns 1 so the SCP fallback is triggered.
#
# Arguments:
#   None
#
# Returns:
#   0 - Both files fetched from the pod successfully; SCP fallback not needed
#   1 - Pod is not Running; SCP fallback required for metadata.json
#
# Globals Read:
#   INFRAID        - Destination directory name
#   CONTAINER_NAME - Container name within the pod
#   POD_NAME       - Name of the CI pod
#
# Example:
#   fetch_pod_artifacts
#------------------------------------------------------------------------------
function fetch_pod_artifacts() {
	local pod_phase
	pod_phase=$(oc get "pod/${POD_NAME}" --no-headers -o jsonpath='{.status.phase}' 2>/dev/null || true)
	log_debug "pod_phase=${pod_phase}"

	if [[ "${pod_phase}" != "Running" ]]; then
		log_warning "Pod phase is '${pod_phase}' — kubeconfig is only accessible while the pod is Running"
		log_warning "Skipping kubeconfig retrieval; set KUBECONFIG manually if needed"
		return 1
	fi

	log_info "Pod is Running — fetching artifacts via oc rsh (container: ${CONTAINER_NAME})..."

	local metadata_ok=false
	local kubeconfig_ok=false

	if rsh_file "/tmp/installer/metadata.json" "${INFRAID}/metadata.json"; then
		log_success "metadata.json written to ${INFRAID}/metadata.json"
		metadata_ok=true
	else
		log_warning "Could not fetch metadata.json via oc rsh — will fall back to SCP"
	fi

	if rsh_file "/tmp/installer/auth/kubeconfig" "${INFRAID}/auth/kubeconfig"; then
		log_success "kubeconfig written to ${INFRAID}/auth/kubeconfig"
		kubeconfig_ok=true
	else
		log_warning "Could not fetch kubeconfig via oc rsh"
	fi

	log_debug "metadata_ok=${metadata_ok} kubeconfig_ok=${kubeconfig_ok}"

	# Return 0 only when metadata.json was obtained; kubeconfig is best-effort.
	${metadata_ok} && return 0 || return 1
}

#------------------------------------------------------------------------------
# scp_artifacts - Copy cluster artifact directory from the controller
#
# Uses SCP to recursively copy the INFRAID directory from the controller's
# home directory to the current working directory. StrictHostKeyChecking is
# disabled to avoid interactive prompts in CI environments.
#
# If the destination directory (./<INFRAID>/) already exists, the user is
# warned that files may be overwritten or merged and prompted to confirm
# before proceeding. Answering anything other than y/Y aborts the script.
#
# Arguments:
#   None
#
# Returns:
#   0 - Directory copied successfully
#   Never returns if the user aborts or SCP fails (calls die)
#
# Globals Read:
#   SSH_KEY         - Path to SSH private key
#   CONTROLLER_USER - Username on the controller host
#   CONTROLLER_IP   - IP address of the controller host
#   INFRAID         - Infra ID; determines both the source and destination directory name
#
# Example:
#   scp_artifacts
#------------------------------------------------------------------------------
function scp_artifacts() {
	local remote_path="${CONTROLLER_USER}@${CONTROLLER_IP}:/home/${CONTROLLER_USER}/${INFRAID}/"

	if [[ -d "${INFRAID}" ]]; then
		log_warning "Destination directory ./${INFRAID}/ already exists — existing files may be overwritten or merged"
		local confirm
		read -rp "Continue anyway? [y/N]: " confirm
		if [[ "${confirm}" != "y" && "${confirm}" != "Y" ]]; then
			die "Aborted by user"
		fi
	fi

	log_info "Copying artifacts from ${remote_path} ..."

	if ! scp -r -i "${SSH_KEY}" \
		-o StrictHostKeyChecking=no \
		-o BatchMode=yes \
		"${remote_path}" .; then
		die "SCP failed: could not copy ${remote_path} to current directory"
	fi

	log_success "Artifacts copied to ./${INFRAID}/"
}

#==============================================================================
# Main Execution
#==============================================================================

#------------------------------------------------------------------------------
# main - Main execution function orchestrating the artifact fetch workflow
#
# Coordinates all steps required to fetch cluster artifacts from a CI pod:
# 1. Parse command-line arguments
# 2. Check required programs
# 3. Collect inputs
# 4. Extract INFRA ID from pod logs
# 5. Fetch metadata.json + kubeconfig from the pod via oc rsh (Running pod);
#    fall back to SCP from the controller for metadata.json if pod is finished
#
# Arguments:
#   $@ - All command-line arguments (passed to parse_arguments)
#
# Returns:
#   0 - Script completed successfully
#   Non-zero - Error occurred (via die)
#
# Example:
#   main "$@"
#------------------------------------------------------------------------------
function main() {
	log_info "Starting fetch-cluster-artifacts script"
	log_info "Script: ${SCRIPT_NAME}"
	echo ""

	# Parse arguments
	parse_arguments "$@"

	# Check requirements
	check_required_programs
	echo ""

	# Collect inputs
	collect_inputs
	echo ""

	# Extract INFRA ID
	extract_infraid
	echo ""

	# Try to fetch metadata.json + kubeconfig directly from the pod.
	# Fall back to SCP from the controller if the pod is no longer Running.
	mkdir -p "${INFRAID}"
	if ! fetch_pod_artifacts; then
		scp_artifacts
	fi
	echo ""

	log_success "All artifacts fetched successfully"
	log_info "Cluster directory: ./${INFRAID}/"
	if [[ -s "${INFRAID}/auth/kubeconfig" ]]; then
		log_info "Kubeconfig:        ./${INFRAID}/auth/kubeconfig"
	fi
}

# Run main function
main "$@"

# Made with Bob
