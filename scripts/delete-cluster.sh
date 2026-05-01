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

set -euo pipefail

#==============================================================================
# Global Variables
#==============================================================================
readonly SCRIPT_NAME="$(basename "${BASH_SOURCE[0]}")"
readonly SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Color codes for output
readonly COLOR_RED='\033[0;31m'
readonly COLOR_GREEN='\033[0;32m'
readonly COLOR_YELLOW='\033[1;33m'
readonly COLOR_BLUE='\033[0;34m'
readonly COLOR_RESET='\033[0m'

#==============================================================================
# Utility Functions
#==============================================================================

# Print colored messages
log_info() {
	echo -e "${COLOR_BLUE}[INFO]${COLOR_RESET} $*"
}

log_success() {
	echo -e "${COLOR_GREEN}[SUCCESS]${COLOR_RESET} $*"
}

log_warning() {
	echo -e "${COLOR_YELLOW}[WARNING]${COLOR_RESET} $*"
}

log_error() {
	echo -e "${COLOR_RED}[ERROR]${COLOR_RESET} $*" >&2
}

# Exit with error message
die() {
	log_error "$*"
	exit 1
}

# Check if a command exists
command_exists() {
	command -v "$1" >/dev/null 2>&1
}

# Validate non-empty variable
validate_non_empty() {
	local var_name="$1"
	local var_value="${!var_name:-}"

	if [[ -z "${var_value}" ]]; then
		die "${var_name} must be set and non-empty"
	fi
}

# Prompt for input with validation
prompt_input() {
	local prompt_text="$1"
	local var_name="$2"
	local default_value="${3:-}"
	local allow_empty="${4:-false}"

	local input_value

	if [[ -n "${default_value}" ]]; then
		read -rp "${prompt_text} [${default_value}]: " input_value
		input_value="${input_value:-${default_value}}"
	else
		read -rp "${prompt_text} []: " input_value
	fi

	if [[ -z "${input_value}" ]] && [[ "${allow_empty}" != "true" ]]; then
		die "You must enter a value for ${var_name}"
	fi

	eval "${var_name}='${input_value}'"
	export "${var_name}"
}

# Execute command with error handling
execute_with_check() {
	local description="$1"
	shift

	log_info "Executing: ${description}"

	if ! "$@"; then
		local rc=$?
		die "${description} failed with exit code ${rc}"
	fi

	log_success "${description} completed successfully"
}

# Prompt for confirmation
confirm_action() {
	local prompt_text="$1"
	local response

	read -rp "${prompt_text} (yes/no): " response

	case "${response}" in
		yes|YES|y|Y)
			return 0
			;;
		*)
			log_info "Operation cancelled by user"
			exit 0
			;;
	esac
}

#==============================================================================
# Main Functions
#==============================================================================

# Initialize architecture-specific tool name
initialize_powervc_tool() {
	local arch
	arch="$(uname -m)"

	case "${arch}" in
		x86_64)
			arch="amd64"
			;;
		ppc64le|aarch64)
			# Keep as-is
			;;
		*)
			die "Unsupported architecture: ${arch}"
			;;
	esac

	POWERVC_TOOL="ocp-ipi-powervc-linux-${arch}"
	readonly POWERVC_TOOL

	log_info "Using PowerVC tool: ${POWERVC_TOOL}"
}

# Check all required programs are installed
check_required_programs() {
	local -a required_programs=("${POWERVC_TOOL}" "openshift-install" "jq")
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

# Collect cluster directory
collect_cluster_directory() {
	log_info "Collecting cluster directory information..."

	if [[ ! -v CLUSTER_DIR ]]; then
		prompt_input "What directory was used for the installation" "CLUSTER_DIR" "test"
	fi

	validate_non_empty "CLUSTER_DIR"

	if [[ ! -d "${CLUSTER_DIR}" ]]; then
		die "Directory ${CLUSTER_DIR} does not exist!"
	fi

	log_success "Cluster directory: ${CLUSTER_DIR}"
}

# Verify cluster directory contents
verify_cluster_directory() {
	log_info "Verifying cluster directory contents..."

	local -a required_files=("metadata.json")
	local missing_files=()

	for file in "${required_files[@]}"; do
		local file_path="${CLUSTER_DIR}/${file}"
		if [[ ! -f "${file_path}" ]]; then
			missing_files+=("${file}")
			log_warning "Missing file: ${file_path}"
		fi
	done

	if [[ ${#missing_files[@]} -gt 0 ]]; then
		log_warning "Some expected files are missing: ${missing_files[*]}"
		log_warning "This may indicate the cluster was not fully created or already deleted"

		if ! confirm_action "Do you want to continue anyway?"; then
			exit 0
		fi
	else
		log_success "Cluster directory contains expected files"
	fi
}

# Collect controller IP
collect_controller_ip() {
	log_info "Collecting controller IP information..."

	if [[ ! -v CONTROLLER_IP ]]; then
		prompt_input "What is the ${POWERVC_TOOL} master controller IP" "CONTROLLER_IP"
	fi

	validate_non_empty "CONTROLLER_IP"

	log_success "Controller IP: ${CONTROLLER_IP}"
}

# Verify controller connectivity
verify_controller() {
	log_info "Verifying controller connectivity: ${CONTROLLER_IP}"

	if ! ping -c1 -W5 "${CONTROLLER_IP}" >/dev/null 2>&1; then
		die "Cannot ping controller at ${CONTROLLER_IP}"
	fi

	log_success "Controller is reachable"
}

# Delete metadata from controller
delete_metadata() {
	local metadata_file="${CLUSTER_DIR}/metadata.json"

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

# Destroy OpenShift cluster
destroy_cluster() {
	log_info "Destroying OpenShift cluster..."
	log_warning "This operation will delete all cluster resources"

	if ! confirm_action "Are you sure you want to destroy the cluster?"; then
		exit 0
	fi

	# Cleanup containers before destroying cluster
	if ! hack_cleanup_containers "${CLOUD}" "${INFRAID}"; then
		log_warning "Container cleanup encountered issues, continuing with cluster destroy"
	fi

	execute_with_check "Cluster destruction" \
		openshift-install destroy cluster \
		--dir="${CLUSTER_DIR}" \
		--log-level=debug
}

# Display cluster information before deletion
display_cluster_info() {
	log_info "Cluster deletion information:"
	echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

	if [[ -f "${CLUSTER_DIR}/metadata.json" ]]; then
		local cluster_name

		cluster_name=$(jq -r '.clusterName // "unknown"' "${CLUSTER_DIR}/metadata.json" 2>/dev/null || echo "unknown")
		infra_id=$(jq -r '.infraID // "unknown"' "${CLUSTER_DIR}/metadata.json" 2>/dev/null || echo "unknown")

		echo "  Cluster Name:	  ${cluster_name}"
		echo "  Infrastructure ID: ${infra_id}"

	if [[ "${infra_id}" == "unknown" ]]; then
		die "Infrastructure ID is unknown"
	fi

	export INFRAID=${infra_id}
	fi

	echo "  Cluster Directory: ${CLUSTER_DIR}"
	echo "  Controller IP:	 ${CONTROLLER_IP}"
	echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
}

# Cleanup OpenStack containers and objects one at a time
# This is a workaround for bulk deletion failures
# Args:
#   $1: Cloud name
#   $2: Infrastructure ID to filter containers
# Returns: 0 on success, 1 on failure
hack_cleanup_containers() {
	if [[ -z "${CLOUD}" ]] || [[ -z "${INFRAID}" ]]; then
		log_error "hack_cleanup_containers requires cloud and infra_id parameters"
		return 1
	fi

	log_info "Cleaning up OpenStack containers for infrastructure: ${INFRAID}"

	# List all containers
	if ! openstack --os-cloud="${CLOUD}" container list --format csv &>/dev/null; then
		log_warning "Failed to list containers or no containers found"
		return 0
	fi

	local container_count=0
	local object_count=0

	# Process each container matching the infrastructure ID
	while IFS= read -r container; do
		[[ -z "${container}" ]] && continue

		container_count=$((container_count + 1))
		log_info "Processing container: ${container}"

		# Delete all objects in the container
		while IFS= read -r object; do
			[[ -z "${object}" ]] && continue

			object_count=$((object_count + 1))
			log_info "Deleting object: ${object} from container: ${container}"

			if ! openstack --os-cloud="${CLOUD}" object delete "${container}" "${object}"; then
				log_warning "Failed to delete object: ${object}"
			fi
		done < <(openstack --os-cloud="${CLOUD}" object list "${container}" --format csv 2>/dev/null | sed -e '/\(Name\)/d' -e 's,",,g')

		# Delete the container itself
		log_info "Deleting container: ${container}"
		if ! openstack --os-cloud="${CLOUD}" container delete "${container}"; then
			log_warning "Failed to delete container: ${container}"
		fi
	done < <(openstack --os-cloud="${CLOUD}" container list --format csv 2>/dev/null | sed -e '/\(Name\|container_name\)/d' -e 's,",,g' | grep -F -- "${INFRAID}" || true)

	log_info "Container cleanup complete. Processed ${container_count} containers and ${object_count} objects"
	return 0
}

# Cleanup cluster directory (optional)
cleanup_cluster_directory() {
	log_info "Cluster directory cleanup..."

	if confirm_action "Do you want to remove the cluster directory (${CLUSTER_DIR})?"; then
		log_info "Removing cluster directory: ${CLUSTER_DIR}"

		if rm -rf "${CLUSTER_DIR}"; then
			log_success "Cluster directory removed"
		else
			log_error "Failed to remove cluster directory"
			log_info "You may need to manually remove: ${CLUSTER_DIR}"
		fi
	else
		log_info "Cluster directory preserved: ${CLUSTER_DIR}"
	fi
}

#==============================================================================
# Main Execution
#==============================================================================

main() {
	log_info "Starting OpenShift cluster deletion script"
	log_info "Script: ${SCRIPT_NAME}"
	log_info "Working directory: $(pwd)"
	echo ""

	# Initialize
	initialize_powervc_tool
	check_required_programs
	echo ""

	# Collect information
	collect_cluster_directory
	verify_cluster_directory
	collect_controller_ip
	echo ""

	# Verify connectivity
	verify_controller
	echo ""

	# Display cluster information
	display_cluster_info
	echo ""

	# Destroy cluster
	destroy_cluster
	echo ""

	# Delete metadata from controller
	delete_metadata
	echo ""

	# Optional cleanup
	cleanup_cluster_directory
	echo ""

	log_success "Cluster deletion completed successfully!"
	log_info "All cluster resources have been removed"
}

# Run main function
main "$@"

# Made with Bob
