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
# Script:  sriov-worker-watch.sh
#
# Purpose:
#   Provisions N SR-IOV-enabled worker nodes one at a time.  For each worker
#   the script scales the MachineSet to 0, then to 1, waits for the new
#   Machine object to appear, pre-creates its OpenStack port configured for
#   SR-IOV (vnic-type "direct") with the binding-profile values required by
#   PowerVC, then repeats until N ports have been created.
#
# Usage:
#   CLOUD=<cloud> CLUSTER_DIR=<dir> NETWORK_NAME=<net> \
#       ./sriov-worker-watch.sh [NUM_WORKERS]
#
#   NUM_WORKERS may be passed as the first positional argument, set as an
#   environment variable, or entered interactively.  The command-line argument
#   takes precedence over the environment variable.
#   CLOUD, CLUSTER_DIR, and NETWORK_NAME can also be supplied interactively.
#
# Environment variables:
#   CLOUD                        Name of the cloud entry in ~/.config/openstack/clouds.yaml (required)
#   CLUSTER_DIR                  Path to the existing OpenShift installation directory (required)
#   NETWORK_NAME                 Name of the OpenStack network to attach the SR-IOV port to (required)
#   NUM_WORKERS                  Number of SR-IOV worker ports to create (required, positive integer;
#                                overridden by the first positional argument if provided)
#   WATCH_POLL_INTERVAL_SECONDS  Seconds between machine-list polls (default: 2)
#   WATCH_HEARTBEAT_SECONDS      Seconds between progress log lines in the watch loop (default: 30)
#   OC_AUTH_RETRY_SECONDS        Total seconds to retry oc whoami before giving up (default: 360)
#
# Workflow:
#   1. Validate required CLI tools are present.
#   2. Collect and validate environment variables.
#   3. Verify OpenStack connectivity and OpenShift RBAC.
#   4. Resolve NETWORK_NAME to a subnet ID.
#   5. Read the infrastructure ID from CLUSTER_DIR/metadata.json.
#   6. Scale MachineSet to 0.
#      For each of the N workers:
#        a. Scale MachineSet to (created+1): 1, 2, 3, … N.
#        b. Poll until a new worker Machine appears.
#        c. Create its SR-IOV port and record it as known.
#      Exit 0 when all N ports have been created.  Exit 1 on any failure.
#
# Dependencies:
#   openstack  OpenStack CLI (python-openstackclient)
#   oc         OpenShift CLI
#   jq         JSON processor
################################################################################

set -euo pipefail

#==============================================================================
# Global constants
#==============================================================================

readonly SCRIPT_NAME="$(basename "${BASH_SOURCE[0]}")"

# Seconds to sleep between consecutive machine-list polls.
# Can be overridden by exporting WATCH_POLL_INTERVAL_SECONDS before invocation.
readonly WATCH_POLL_INTERVAL_SECONDS="${WATCH_POLL_INTERVAL_SECONDS:-2}"

# Seconds between progress heartbeat log lines during the watch loop.
# Can be overridden by exporting WATCH_HEARTBEAT_SECONDS before invocation.
readonly WATCH_HEARTBEAT_SECONDS="${WATCH_HEARTBEAT_SECONDS:-30}"

readonly OC_AUTH_RETRY_SECONDS="${OC_AUTH_RETRY_SECONDS:-360}"

# ANSI escape codes used by the log_* functions below.
readonly COLOR_RED='\033[0;31m'      # Errors
readonly COLOR_GREEN='\033[0;32m'    # Success confirmations
readonly COLOR_YELLOW='\033[1;33m'   # Warnings
readonly COLOR_BLUE='\033[0;34m'     # Informational messages
readonly COLOR_RESET='\033[0m'       # Reset to terminal default

#==============================================================================
# Logging helpers
# Each function writes a prefixed, coloured line.  log_error writes to stderr;
# all others write to stdout.  printf '%b\n' is used instead of echo -e for
# consistent escape-sequence handling across environments.
#==============================================================================

################################################################################
# log_info: Informational message (blue).
# Usage: log_info "text"
################################################################################
function log_info() {
	printf '%b\n' "${COLOR_BLUE}[INFO]${COLOR_RESET} $*"
}

################################################################################
# log_success: Success confirmation (green).
# Usage: log_success "text"
################################################################################
function log_success() {
	printf '%b\n' "${COLOR_GREEN}[SUCCESS]${COLOR_RESET} $*"
}

################################################################################
# log_warning: Non-fatal warning (yellow).
# Usage: log_warning "text"
################################################################################
function log_warning() {
	printf '%b\n' "${COLOR_YELLOW}[WARNING]${COLOR_RESET} $*"
}

################################################################################
# log_error: Error message written to stderr (red).
# Usage: log_error "text"
################################################################################
function log_error() {
	printf '%b\n' "${COLOR_RED}[ERROR]${COLOR_RESET} $*" >&2
}

#==============================================================================
# Core utility functions
#==============================================================================

################################################################################
# die
# Log an error message and terminate the script with exit code 1.
# Usage: die "fatal error message"
################################################################################
function die() {
	log_error "$*"
	exit 1
}

################################################################################
# prompt_input
# Display an interactive prompt, read user input, and export the result into a
# named variable.  Supports an optional default value.
#
# Parameters:
#   $1  prompt_text    Text displayed to the user before the input cursor.
#   $2  var_name       Name of the variable that will receive the input value.
#   $3  default_value  Value used when the user presses Enter with no input
#                      (optional; displayed in brackets next to the prompt).
#
# Side effects:
#   Sets and exports the variable named by $2.
# Exits: 1 if input is empty and no default was provided.
# Usage: prompt_input "Cloud name" "CLOUD"
################################################################################
function prompt_input() {
	local prompt_text="$1"
	local var_name="$2"
	local default_value="${3:-}"

	local input_value

	if [[ -n "${default_value}" ]]; then
		read -rp "${prompt_text} [${default_value}]: " input_value
		input_value="${input_value:-${default_value}}"
	else
		read -rp "${prompt_text}: " input_value
	fi

	if [[ -z "${input_value}" ]]; then
		die "You must enter a value for ${var_name}"
	fi

	printf -v "${var_name}" '%s' "${input_value}"
	export "${var_name}"
}

#==============================================================================
# Environment and prerequisite functions
#==============================================================================

################################################################################
# validate_environment_variables
# Confirm that every variable required by the script is set and non-empty,
# and that CLUSTER_DIR exists on disk.  Must be called after
# collect_environment_variables so that interactive prompting has already
# filled any missing values.
# All missing-variable errors are accumulated and reported together before the
# script exits, so the user sees every problem in a single run.
# The filesystem check runs last, after all variable checks.
# Validates: CLOUD, CLUSTER_DIR (non-empty + exists), NETWORK_NAME, NUM_WORKERS
# Exits: 1 if any required variable is missing, empty, or CLUSTER_DIR is absent.
################################################################################
function validate_environment_variables() {
	log_info "Validating environment variables..."

	local -a required_vars=(
		"CLOUD"
		"CLUSTER_DIR"
		"NETWORK_NAME"
		"NUM_WORKERS"
	)

	# Accumulate all missing-variable errors before exiting so the user sees
	# every problem at once rather than one per run.
	local -a missing_vars=()
	for var in "${required_vars[@]}"; do
		if [[ -z "${!var:-}" ]]; then
			missing_vars+=("${var}")
			log_error "${var} must be set and non-empty"
		fi
	done

	if [[ ${#missing_vars[@]} -gt 0 ]]; then
		die "Missing required variables: ${missing_vars[*]}"
	fi

	# Validate NUM_WORKERS is a positive integer now that we know it is non-empty.
	if ! [[ "${NUM_WORKERS}" =~ ^[1-9][0-9]*$ ]]; then
		die "NUM_WORKERS must be a positive integer, got: '${NUM_WORKERS}'"
	fi

	# Filesystem check runs after all variable checks so the user sees every
	# missing-variable error before any directory-existence error.
	if [[ ! -d "${CLUSTER_DIR}" ]]; then
		die "Cluster directory not found: ${CLUSTER_DIR}. Ensure the installation directory exists."
	fi

	# Derive KUBECONFIG from CLUSTER_DIR — it is always at this path for an
	# OpenShift IPI installation.
	export KUBECONFIG="${CLUSTER_DIR}/auth/kubeconfig"
	if [[ ! -f "${KUBECONFIG}" ]]; then
		die "kubeconfig not found: ${KUBECONFIG}. Ensure the cluster installation has completed."
	fi

	log_success "All environment variables validated"
}

################################################################################
# collect_environment_variables
# Gather the four required configuration values, prompting the user
# interactively for any that are not already present in the environment.
# Pre-setting a variable before invocation skips its prompt entirely, enabling
# non-interactive (CI/scripted) use.
#
# Variables collected:
#   CLOUD        - Name of the cloud entry in ~/.config/openstack/clouds.yaml
#   CLUSTER_DIR  - Path to the existing OpenShift installation directory
#   NETWORK_NAME - Name of the OpenStack network for SR-IOV port attachment
#   NUM_WORKERS  - Number of SR-IOV workers to provision
#
# Side effects: Sets and exports all four variables.
################################################################################
function collect_environment_variables() {
	log_info "Collecting environment variables..."

	# Cloud name from clouds.yaml
	if [[ ! -v CLOUD ]]; then
		prompt_input "What is the cloud name in ~/.config/openstack/clouds.yaml" "CLOUD"
	fi

	# Cluster installation directory
	if [[ ! -v CLUSTER_DIR ]]; then
		prompt_input "What is the path to the existing cluster installation directory" "CLUSTER_DIR"
	fi

	# OpenStack network to attach SR-IOV ports to
	if [[ ! -v NETWORK_NAME ]]; then
		prompt_input "What is the OpenStack network" "NETWORK_NAME"
	fi

	# Number of SR-IOV workers to provision
	if [[ ! -v NUM_WORKERS ]]; then
		prompt_input "How many SR-IOV workers to create" "NUM_WORKERS"
	fi

	log_success "All environment variables collected"
}

################################################################################
# cleanup_on_exit
# EXIT trap handler — logs a failure message whenever the script terminates
# with a non-zero exit code, giving a consistent last line of output for
# operators and CI log parsers.
# Registered in main via: trap cleanup_on_exit EXIT
################################################################################
function cleanup_on_exit() {
	local exit_code=$?

	if [[ ${exit_code} -ne 0 ]]; then
		log_error "Script failed with exit code ${exit_code}"
	fi
}

################################################################################
# check_required_programs
# Verify that every external CLI tool the script depends on is present on PATH.
# All missing tools are reported before the script exits so the user can
# install everything in a single pass rather than discovering absences one by one.
#
# Required tools:
#   openstack  OpenStack CLI (python-openstackclient)
#   oc         OpenShift CLI
#   jq         JSON processor (used to parse metadata.json)
#
# Exits: 1 if one or more required programs are missing from PATH.
################################################################################
function check_required_programs() {
	local -a required_programs=("openstack" "oc" "jq")
	local -a missing_programs=()

	log_info "Checking required programs..."

	for program in "${required_programs[@]}"; do
		if ! command -v "${program}" >/dev/null 2>&1; then
			missing_programs+=("${program}")
			log_error "Missing required program: ${program}"
		fi
	done

	if [[ ${#missing_programs[@]} -gt 0 ]]; then
		die "Missing required programs: ${missing_programs[*]}"
	fi

	log_success "All required programs are available"
}

################################################################################
# verify_openstack_connectivity
# Confirm that the OpenStack endpoint is reachable and that the credentials in
# clouds.yaml are valid by issuing a token — the lightest possible API call
# that exercises authentication without depending on any specific service.
# Uses: $CLOUD
# Exits: 1 if the token request fails for any reason (network, auth, config).
################################################################################
function verify_openstack_connectivity() {
	log_info "Verifying OpenStack connectivity..."

	if ! openstack --os-cloud="${CLOUD}" token issue >/dev/null 2>&1; then
		die "Cannot connect to OpenStack. Please verify clouds.yaml configuration."
	fi

	log_success "OpenStack connectivity verified"
}

################################################################################
# verify_openshift_rbac
# Confirm that KUBECONFIG is valid and the oc session has permission to list
# Machine objects in the openshift-machine-api namespace.  A clear preflight
# failure here is far more actionable than a cryptic error deep inside the
# watch loop.
# Also checks for machineset list permission and warns if absent — the
# MachineSet scale hint will fall back to the infra_id-derived name.
# Retries the oc whoami check for up to OC_AUTH_RETRY_SECONDS (default 360)
# before giving up, to tolerate a briefly unavailable API server.
# Exits: 1 if KUBECONFIG is invalid or the oc session cannot list Machine objects.
################################################################################
function verify_openshift_rbac() {
	log_info "Verifying OpenShift connectivity and RBAC permissions..."

	local start_time
	printf -v start_time '%(%s)T' -1
	local now

	while ! oc whoami >/dev/null 2>&1; do
		printf -v now '%(%s)T' -1
		if (( now - start_time >= OC_AUTH_RETRY_SECONDS )); then
			die "Cannot authenticate with OpenShift after ${OC_AUTH_RETRY_SECONDS}s. Ensure KUBECONFIG is set and the API server is reachable (currently: ${KUBECONFIG})."
		fi
		log_info "OpenShift API server not yet reachable — retrying in ${WATCH_POLL_INTERVAL_SECONDS}s (${KUBECONFIG})..."
		sleep "${WATCH_POLL_INTERVAL_SECONDS}"
	done
	log_info "Authenticated as: $(oc whoami)"

	if ! oc auth can-i list machines.machine.openshift.io \
		-n openshift-machine-api >/dev/null 2>&1; then
		die "Insufficient permissions: cannot list Machine objects in openshift-machine-api. Check your kubeconfig and RBAC."
	fi

	if ! oc auth can-i list machinesets.machine.openshift.io \
		-n openshift-machine-api >/dev/null 2>&1; then
		log_warning "Cannot list MachineSet objects — scale hint will use the infra ID fallback."
	fi

	log_success "OpenShift RBAC check passed"
}

#==============================================================================
# Network and cluster metadata functions
#==============================================================================

################################################################################
# resolve_subnet_id
# Resolve NETWORK_NAME to its OpenStack subnet UUID, log the result to stderr,
# and print the UUID to stdout so callers can capture it via $().
# Exits with an error if the network has zero or more than one subnet, since
# the SR-IOV port must be attached to an unambiguous single subnet.
#
# The OpenStack CLI is invoked with "--format shell" which emits key=value
# lines.  sed extracts the value between the first pair of single-quotes in
# the subnets= line:
#   subnets='a1b2c3d4-5678-...'  →  a1b2c3d4-5678-...
#
# stderr from the OpenStack CLI is left intact so that auth failures and
# endpoint errors surface directly rather than being swallowed.
# Log output goes to stderr so it is not captured when called via $().
#
# Uses:   $CLOUD, $NETWORK_NAME
# Prints: subnet UUID to stdout
# Exits:  1 if the network cannot be queried, has multiple subnets, or the
#         UUID cannot be parsed.
################################################################################
function resolve_subnet_id() {
	log_info "Resolving subnet ID for network: ${NETWORK_NAME}" >&2

	local subnet_output
	subnet_output=$(openstack --os-cloud="${CLOUD}" network show "${NETWORK_NAME}" --format shell | grep '^subnets=' || true)

	if [[ -z "${subnet_output}" ]]; then
		die "Failed to retrieve subnet information for network: ${NETWORK_NAME}"
	fi

	# Extract the value between the first pair of single-quotes.
	local raw_subnets
	raw_subnets=$(sed -e "s,^[^']*',," -e "s,'.*$,," <<< "${subnet_output}")

	if [[ -z "${raw_subnets}" ]]; then
		die "No subnets found for network: ${NETWORK_NAME}"
	fi

	# Reject networks with multiple subnets — the port must target exactly one.
	if [[ "${raw_subnets}" == *","* ]]; then
		die "Network ${NETWORK_NAME} has multiple subnets (${raw_subnets}). Attach the SR-IOV port to a network with exactly one subnet."
	fi

	local subnet_id="${raw_subnets// /}"   # strip any surrounding whitespace
	log_success "Subnet ID: ${subnet_id}" >&2
	printf '%s' "${subnet_id}"
}

#==============================================================================
# Scaling and watcher functions
#==============================================================================

################################################################################
# scale_workers
# Scale the worker MachineSet(s) to the specified number of replicas.
#
# Parameters:
#   $1  num_replicas  Target number of worker replicas (non-negative integer).
#   $2  infra_id      Cluster infrastructure ID (optional, used as fallback).
#
# Returns: 0 on success, 1 on failure.
# Usage: scale_workers 3 "${infra_id}"
################################################################################
function scale_workers() {
	local num_replicas="${1:-}"
	local infra_id="${2:-}"

	if [[ -z "${num_replicas}" ]]; then
		log_error "scale_workers requires a replica count"
		return 1
	fi

	if ! [[ "${num_replicas}" =~ ^[0-9]+$ ]]; then
		log_error "Invalid replica count: '${num_replicas}'. Must be a non-negative integer."
		return 1
	fi

	local machinesets
	machinesets=$(oc get machinesets -n openshift-machine-api --no-headers 2>/dev/null | awk '{print $1}') || true

	if [[ -z "${machinesets}" ]]; then
		if [[ -n "${infra_id}" ]]; then
			machinesets="${infra_id}-worker-0"
		else
			log_error "No MachineSets found to scale"
			return 1
		fi
	fi

	local ms
	while IFS= read -r ms; do
		[[ -z "${ms}" ]] && continue
		log_info "Scaling MachineSet ${ms} to ${num_replicas} replica(s)..."
		if oc scale machineset "${ms}" -n openshift-machine-api --replicas="${num_replicas}"; then
			log_success "Scaled MachineSet ${ms} to ${num_replicas}"
		else
			log_error "Failed to scale MachineSet ${ms}"
			return 1
		fi
	done <<< "${machinesets}"

	return 0
}

################################################################################
# create_n_workers
# Provision N SR-IOV worker nodes one at a time by incrementally scaling the
# MachineSet.  Before the loop the MachineSet is scaled to 0.  Each iteration
# then scales to (created+1) — i.e. 0→1, 1→2, … — so exactly one new Machine
# appears per iteration.  For every iteration:
#   1. Scale MachineSet(s) to (created+1).
#   2. Poll until a new worker Machine object appears (one not in the known set).
#   3. Create the SR-IOV port for that Machine, then record it as known.
# Repeats until N ports have been created successfully.
#
# Parameters:
#   $1  num_workers  Total number of SR-IOV workers to create (positive integer).
#   $2  infra_id     Cluster infrastructure ID (from metadata.json).
#   $3  subnet_id    OpenStack subnet UUID resolved from NETWORK_NAME.
#
# Uses:    $CLOUD, $NETWORK_NAME, $WATCH_POLL_INTERVAL_SECONDS,
#          $WATCH_HEARTBEAT_SECONDS
# Exits:   0 when all N ports have been created
#          1 on any scale or port creation failure
################################################################################
function create_n_workers() {
	local num_workers="$1"
	local infra_id="$2"
	local subnet_id="$3"

	log_info "Provisioning ${num_workers} SR-IOV worker(s)..."

	# Tracks all Machine names seen across iterations so we never re-create a
	# port for a machine that appeared in an earlier round.
	local known_machines
	known_machines=$(oc get machines -n openshift-machine-api \
		-l machine.openshift.io/cluster-api-machine-role=worker \
		--no-headers 2>/dev/null | awk '{print $1}') || true

	if [[ -n "${known_machines}" ]]; then
		log_info "Worker Machines already present at startup (will not create ports for these):"
		while IFS= read -r m; do
			log_info "  ${m}"
		done <<< "${known_machines}"
	fi

	# Declare loop-scoped variables once here; new_machine and port_name are
	# reset each iteration rather than re-declared with local inside the loop.
	local new_machine=""
	local port_name
	local current_machines
	local now
	local last_progress_log
	local m
	local created=0

	# Scale to 0 once before the loop so the cumulative scale-up starts clean.
	log_info "Scaling MachineSet(s) to 0 before provisioning..."
	if ! scale_workers 0 "${infra_id}"; then
		die "Failed to scale MachineSet(s) to 0"
	fi

	while (( created < num_workers )); do
		log_info "--- Worker $((created + 1)) of ${num_workers} ---"

		# Scale up by one to trigger creation of exactly one new Machine.
		# Each iteration increments the replica count: 0→1, 1→2, 2→3, …
		log_info "Scaling MachineSet(s) to $((created + 1))..."
		if ! scale_workers $(( created + 1 )) "${infra_id}"; then
			die "Failed to scale MachineSet(s) to $((created + 1)) for worker $((created + 1))"
		fi

		# Step 3: Poll until a Machine name appears that is not in known_machines.
		log_info "Waiting for new worker Machine object (polls every ${WATCH_POLL_INTERVAL_SECONDS}s)..."
		new_machine=""
		printf -v last_progress_log '%(%s)T' -1

		while [[ -z "${new_machine}" ]]; do
			sleep "${WATCH_POLL_INTERVAL_SECONDS}" || true

			printf -v now '%(%s)T' -1
			if (( now - last_progress_log >= WATCH_HEARTBEAT_SECONDS )); then
				log_info "Still waiting for new Machine (worker $((created + 1)) of ${num_workers})..."
				last_progress_log=${now}
			fi

			current_machines=$(oc get machines -n openshift-machine-api \
				-l machine.openshift.io/cluster-api-machine-role=worker \
				--no-headers 2>/dev/null | awk '{print $1}') || true

			for m in ${current_machines}; do
				if [[ $'\n'"${known_machines}"$'\n' != *$'\n'"${m}"$'\n'* ]]; then
					new_machine="${m}"
					break
				fi
			done
		done

		log_info "New Machine detected: ${new_machine}"

		# Step 4: Create the SR-IOV port for the new Machine.
		port_name="${new_machine}-nodes"
		log_info "Creating SR-IOV port: ${port_name}"

		if openstack --os-cloud="${CLOUD}" port create \
			--network "${NETWORK_NAME}" \
			--fixed-ip subnet="${subnet_id}" \
			--vnic-type direct \
			--binding-profile capacity=0.02 \
			--binding-profile delete_with_instance=1 \
			--binding-profile vnic_required_vfs=2 \
			--disable-port-security \
			"${port_name}" >/dev/null; then
			log_success "Port created: ${port_name}"
		else
			die "Port creation failed for ${port_name}"
		fi

		# Record the machine so subsequent iterations don't mistake it for new.
		known_machines="${known_machines}"$'\n'"${new_machine}"
		created=$(( created + 1 ))
	done

	log_success "All ${num_workers} SR-IOV worker port(s) created successfully"
}

#==============================================================================
# Entry point
#==============================================================================

################################################################################
# main
# Orchestrates the full workflow in six sequential phases.  Each phase must
# complete without error before the next begins.
#
# Phases:
#   1. Prerequisite check  — verify required CLI tools are on PATH
#   2. Configuration       — collect + validate env vars, verify OpenStack auth
#   3. RBAC check          — confirm oc can list Machine objects
#   4. Subnet resolution   — resolve NETWORK_NAME to a subnet ID
#   5. Cluster metadata    — read infraID from CLUSTER_DIR/metadata.json
#   6. Worker loop         — provision NUM_WORKERS SR-IOV workers one at a time
#
# Usage: main "$@"   (called at the bottom of this script)
# Returns: 0 on success, 1 on any failure.
################################################################################
function main() {
	trap cleanup_on_exit EXIT
	# Allow Ctrl-C and SIGTERM to exit cleanly without triggering the
	# "Script failed" error message from cleanup_on_exit.
	trap 'log_info "Interrupted. Exiting."; exit 0' INT TERM

	# If NUM_WORKERS was passed as a positional argument, it takes precedence
	# over any environment variable of the same name.
	if [[ $# -ge 1 ]]; then
		NUM_WORKERS="$1"
		export NUM_WORKERS
		log_info "NUM_WORKERS=${NUM_WORKERS} (from command-line argument)"
	fi

	log_info "Starting OpenShift SR-IOV worker watch script"
	log_info "Script: ${SCRIPT_NAME}"
	log_info "Working directory: $(pwd)"

	# Phase 1: Verify all required CLI tools are present
	check_required_programs

	# Phase 2: Collect and validate configuration, verify OpenStack connectivity
	collect_environment_variables
	validate_environment_variables
	verify_openstack_connectivity

	# Phase 3: Confirm oc has permission to list Machine objects
	verify_openshift_rbac

	# Phase 4: Resolve NETWORK_NAME to a subnet ID
	local subnet_id
	subnet_id=$(resolve_subnet_id)

	# Phase 5: Read the infrastructure ID from the cluster metadata
	local metadata_file="${CLUSTER_DIR}/metadata.json"
	if [[ ! -f "${metadata_file}" ]]; then
		die "Cluster metadata not found: ${metadata_file}"
	fi

	local infra_id
	infra_id=$(jq -r .infraID "${metadata_file}")
	if [[ -z "${infra_id}" || "${infra_id}" == "null" ]]; then
		die "infraID is missing or null in ${metadata_file}"
	fi
	log_info "Infrastructure ID: ${infra_id}"

	# Phase 6: Provision NUM_WORKERS SR-IOV workers one at a time
	create_n_workers "${NUM_WORKERS}" "${infra_id}" "${subnet_id}"
}

main "$@"
