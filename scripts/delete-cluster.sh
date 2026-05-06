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
# Script: delete-cluster.sh
# Description: Comprehensive OpenShift cluster deletion script for PowerVC
#              environments. Handles metadata cleanup, cluster destruction,
#              and optional directory removal.
#
# Usage: ./delete-cluster.sh
#
# Environment Variables:
#   CLOUD          - OpenStack cloud name from clouds.yaml (prompted if unset)
#   CLUSTER_DIR    - Directory containing cluster installation files (prompted if unset)
#   CONTROLLER_IP  - IP address of the PowerVC controller (prompted if unset)
#
# Prerequisites:
#   - ocp-ipi-powervc-linux-{arch} tool must be in PATH
#   - openshift-install must be in PATH
#   - jq must be in PATH
#   - openstack CLI must be in PATH
#   - Valid OpenStack credentials configured in clouds.yaml
#   - Cluster directory must contain metadata.json
#
# Exit Codes:
#   0 - Success
#   1 - Error (missing dependencies, invalid configuration, operation failure)
#
# Examples:
#   # Interactive mode (prompts for all required information)
#   ./delete-cluster.sh
#
#   # Non-interactive mode with environment variables
#   CLOUD="mycloud" CLUSTER_DIR="test" CONTROLLER_IP="192.168.1.100" ./delete-cluster.sh
#==============================================================================

set -euo pipefail

#==============================================================================
# Global Variables
#==============================================================================
readonly SCRIPT_NAME="$(basename "${BASH_SOURCE[0]}")"
readonly SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# ANSI color codes for enhanced terminal output
readonly COLOR_RED='\033[0;31m'      # Error messages
readonly COLOR_GREEN='\033[0;32m'    # Success messages
readonly COLOR_YELLOW='\033[1;33m'   # Warning messages
readonly COLOR_BLUE='\033[0;34m'     # Info messages
readonly COLOR_RESET='\033[0m'       # Reset to default

# Temporary directory for metadata backup (initialized in display_cluster_info)
TEMP_DIR=""

#==============================================================================
# Utility Functions
#==============================================================================

#------------------------------------------------------------------------------
# Function: log_info
# Description: Print informational messages in blue color
# Arguments:
#   $* - Message text to display
# Output: Formatted message to stdout
#------------------------------------------------------------------------------
function log_info() {
	echo -e "${COLOR_BLUE}[INFO]${COLOR_RESET} $*"
}

#------------------------------------------------------------------------------
# Function: log_success
# Description: Print success messages in green color
# Arguments:
#   $* - Message text to display
# Output: Formatted message to stdout
#------------------------------------------------------------------------------
function log_success() {
	echo -e "${COLOR_GREEN}[SUCCESS]${COLOR_RESET} $*"
}

#------------------------------------------------------------------------------
# Function: log_warning
# Description: Print warning messages in yellow color
# Arguments:
#   $* - Message text to display
# Output: Formatted message to stdout
#------------------------------------------------------------------------------
function log_warning() {
	echo -e "${COLOR_YELLOW}[WARNING]${COLOR_RESET} $*"
}

#------------------------------------------------------------------------------
# Function: log_error
# Description: Print error messages in red color to stderr
# Arguments:
#   $* - Error message text to display
# Output: Formatted error message to stderr
#------------------------------------------------------------------------------
function log_error() {
	echo -e "${COLOR_RED}[ERROR]${COLOR_RESET} $*" >&2
}

#------------------------------------------------------------------------------
# Function: die
# Description: Print error message and exit script with failure status
# Arguments:
#   $* - Error message to display before exiting
# Exit Code: 1 (failure)
#------------------------------------------------------------------------------
function die() {
	log_error "$*"
	exit 1
}

#------------------------------------------------------------------------------
# Function: command_exists
# Description: Check if a command is available in the system PATH
# Arguments:
#   $1 - Command name to check
# Returns:
#   0 - Command exists
#   1 - Command not found
#------------------------------------------------------------------------------
function command_exists() {
	command -v "$1" >/dev/null 2>&1
}

#------------------------------------------------------------------------------
# Function: validate_non_empty
# Description: Validate that a variable is set and contains a non-empty value
# Arguments:
#   $1 - Variable name to validate (not the value itself)
# Exit: Terminates script if variable is empty or unset
#------------------------------------------------------------------------------
function validate_non_empty() {
	local var_name="$1"
	local var_value="${!var_name:-}"

	if [[ -z "${var_value}" ]]; then
		die "${var_name} must be set and non-empty"
	fi
}

#------------------------------------------------------------------------------
# Function: prompt_input
# Description: Prompt user for input with optional default value and validation
# Arguments:
#   $1 - Prompt text to display to user
#   $2 - Variable name to store the input value
#   $3 - Default value (optional, empty string if not provided)
#   $4 - Allow empty input flag (optional, "false" by default)
# Side Effects:
#   - Sets and exports the variable specified in $2
#   - Exits script if input is empty and allow_empty is false
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
		read -rp "${prompt_text} []: " input_value
	fi

	if [[ -z "${input_value}" ]] && [[ "${allow_empty}" != "true" ]]; then
		die "You must enter a value for ${var_name}"
	fi

	printf -v "${var_name}" '%s' "${input_value}"
	export "${var_name}"
}

#------------------------------------------------------------------------------
# Function: execute_with_check
# Description: Execute a command with error handling and status reporting
# Arguments:
#   $1 - Description of the operation being performed
#   $@ - Command and arguments to execute (shift removes first arg)
# Exit: Terminates script if command fails
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

#------------------------------------------------------------------------------
# Function: confirm_action
# Description: Prompt user for yes/no confirmation before proceeding
# Arguments:
#   $1 - Confirmation prompt text
# Returns:
#   0 - User confirmed (yes/y/YES/Y)
#   1 - User declined or provided other input
#------------------------------------------------------------------------------
function confirm_action() {
	local prompt_text="$1"
	local response

	read -rp "${prompt_text} (yes/no): " response

	case "${response}" in
		yes|YES|y|Y)
			return 0
			;;
		*)
			log_info "Operation cancelled by user"
			return 1
			;;
	esac
}

#==============================================================================
# Main Functions
#==============================================================================

#------------------------------------------------------------------------------
# Function: initialize_powervc_tool
# Description: Determine the architecture-specific PowerVC tool name based on
#              the system architecture. Sets the POWERVC_TOOL global variable.
# Side Effects:
#   - Sets and exports POWERVC_TOOL as readonly
# Exit: Terminates script if architecture is unsupported
# Supported Architectures: x86_64 (amd64), ppc64le, aarch64
#------------------------------------------------------------------------------
function initialize_powervc_tool() {
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

#------------------------------------------------------------------------------
# Function: check_required_programs
# Description: Verify that all required programs are installed and available
#              in the system PATH. Checks for PowerVC tool, openshift-install,
#              and jq.
# Exit: Terminates script if any required program is missing
#------------------------------------------------------------------------------
function check_required_programs() {
	local -a required_programs=("${POWERVC_TOOL}" "openshift-install" "jq" "openstack")
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
# Function: collect_cloud_name
# Description: Collect the OpenStack cloud name from environment or user input.
#              The cloud name must match an entry in clouds.yaml.
# Side Effects:
#   - Sets and exports CLOUD variable if not already set
# Exit: Terminates script if CLOUD is empty after collection
#------------------------------------------------------------------------------
function collect_cloud_name() {
	log_info "Collecting OpenStack cloud name..."

	if [[ ! -v CLOUD ]]; then
		prompt_input "What is the OpenStack cloud name (from clouds.yaml)" "CLOUD"
	fi

	validate_non_empty "CLOUD"

	log_success "Cloud name: ${CLOUD}"
}

#------------------------------------------------------------------------------
# Function: collect_cluster_directory
# Description: Collect the cluster installation directory path from environment
#              or user input. Validates that the directory exists.
# Side Effects:
#   - Sets and exports CLUSTER_DIR variable if not already set
# Exit: Terminates script if directory doesn't exist or CLUSTER_DIR is empty
#------------------------------------------------------------------------------
function collect_cluster_directory() {
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

#------------------------------------------------------------------------------
# Function: verify_cluster_directory
# Description: Verify that the cluster directory contains expected files,
#              particularly metadata.json. Prompts user to continue if files
#              are missing.
# Exit: Terminates script (status 0) if user declines to continue with missing files
#------------------------------------------------------------------------------
function verify_cluster_directory() {
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
		die "Cannot proceed without required files: ${missing_files[*]}"
	else
		log_success "Cluster directory contains expected files"
	fi
}

#------------------------------------------------------------------------------
# Function: collect_controller_ip
# Description: Collect the PowerVC controller IP address from environment or
#              user input. This IP is used for metadata operations.
# Side Effects:
#   - Sets and exports CONTROLLER_IP variable if not already set
# Exit: Terminates script if CONTROLLER_IP is empty after collection
#------------------------------------------------------------------------------
function collect_controller_ip() {
	log_info "Collecting controller IP information..."

	if [[ ! -v CONTROLLER_IP ]]; then
		prompt_input "What is the ${POWERVC_TOOL} master controller IP" "CONTROLLER_IP"
	fi

	validate_non_empty "CONTROLLER_IP"

	log_success "Controller IP: ${CONTROLLER_IP}"
}

#------------------------------------------------------------------------------
# Function: verify_controller
# Description: Verify network connectivity to the PowerVC controller using ping.
#              Sends a single ping with 5-second timeout.
# Exit: Terminates script if controller is not reachable
#------------------------------------------------------------------------------
function verify_controller() {
	log_info "Verifying controller connectivity: ${CONTROLLER_IP}"

	if ! ping -c1 -W5 "${CONTROLLER_IP}" >/dev/null 2>&1; then
		die "Cannot ping controller at ${CONTROLLER_IP}"
	fi

	log_success "Controller is reachable"
}

#------------------------------------------------------------------------------
# Function: delete_metadata
# Description: Delete cluster metadata from the PowerVC controller using the
#              send-metadata command. Skips if metadata.json is not found.
# Exit: Terminates script if metadata deletion fails
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
# Function: destroy_cluster
# Description: Destroy the OpenShift cluster using openshift-install. Prompts
#              for confirmation before proceeding. Performs container cleanup
#              before cluster destruction.
# Side Effects:
#   - Calls hack_cleanup_containers to remove OpenStack containers
#   - Deletes all cluster resources via openshift-install
# Exit: Terminates script if user declines or if destruction fails
#------------------------------------------------------------------------------
function destroy_cluster() {
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

#------------------------------------------------------------------------------
# Function: display_cluster_info
# Description: Display cluster information extracted from metadata.json before
#              deletion. Shows cluster name, infrastructure ID, directory, and
#              controller IP.
# Side Effects:
#   - Sets and exports INFRAID variable from metadata.json (REQUIRED by destroy_cluster)
# Exit: Terminates script if metadata.json is missing or infrastructure ID is unknown
#------------------------------------------------------------------------------
function display_cluster_info() {
	log_info "Cluster deletion information:"
	echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

	if [[ -f "${CLUSTER_DIR}/metadata.json" ]]; then
		local cluster_name
		local infra_id

		cluster_name=$(jq -r '.clusterName // "unknown"' "${CLUSTER_DIR}/metadata.json" 2>/dev/null || echo "unknown")
		infra_id=$(jq -r '.infraID // "unknown"' "${CLUSTER_DIR}/metadata.json" 2>/dev/null || echo "unknown")

		echo "  Cluster Name:	  ${cluster_name}"
		echo "  Infrastructure ID: ${infra_id}"

		if [[ "${infra_id}" == "unknown" ]]; then
			die "Infrastructure ID is unknown"
		fi

		export INFRAID="${infra_id}"

		TEMP_DIR=$(mktemp -d)
		/bin/cp "${CLUSTER_DIR}/metadata.json" "${TEMP_DIR}"
	else
		die "metadata.json not found, cannot determine infrastructure ID"
	fi

	echo "  Cluster Directory: ${CLUSTER_DIR}"
	echo "  Controller IP:	 ${CONTROLLER_IP}"
	echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
}

#------------------------------------------------------------------------------
# Function: hack_cleanup_containers
# Description: Cleanup OpenStack containers and objects one at a time. This is
#              a workaround for bulk deletion failures in OpenStack. Filters
#              containers by infrastructure ID and deletes all objects before
#              removing the container itself.
# Arguments:
#   $1 - Cloud name (from clouds.yaml)
#   $2 - Infrastructure ID to filter containers
# Returns:
#   0 - Success (all containers processed or none found)
#   1 - Failure (missing required parameters)
# Note: Individual object/container deletion failures are logged as warnings
#       but don't cause the function to fail
#------------------------------------------------------------------------------
function hack_cleanup_containers() {
	local cloud="${1:-}"
	local infra_id="${2:-}"

	if [[ -z "${cloud}" ]] || [[ -z "${infra_id}" ]]; then
		log_error "hack_cleanup_containers requires cloud and infra_id parameters"
		return 1
	fi

	log_info "Cleaning up OpenStack containers for infrastructure: ${infra_id}"

	# List all containers
	local container_list_output
	if ! container_list_output=$(openstack --os-cloud="${cloud}" container list --format csv 2>&1); then
		log_warning "Failed to list containers: ${container_list_output}"
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

			if ! openstack --os-cloud="${cloud}" object delete "${container}" "${object}"; then
				log_warning "Failed to delete object: ${object}"
			fi
		done < <(openstack --os-cloud="${cloud}" object list "${container}" --format value -c Name 2>/dev/null)

		# Delete the container itself
		log_info "Deleting container: ${container}"
		if ! openstack --os-cloud="${cloud}" container delete "${container}"; then
			log_warning "Failed to delete container: ${container}"
		fi
	done < <(openstack --os-cloud="${cloud}" container list --format value -c Name 2>/dev/null | grep -F -- "${infra_id}" || true)

	log_info "Container cleanup complete. Processed ${container_count} containers and ${object_count} objects"
	return 0
}

#------------------------------------------------------------------------------
# Function: cleanup_cluster_directory
# Description: Optionally remove the cluster directory after successful deletion.
#              Prompts user for confirmation before removing the directory.
# Note: Failure to remove directory is logged but doesn't cause script failure
#------------------------------------------------------------------------------
function cleanup_cluster_directory() {
	log_info "Cluster directory cleanup..."

	# Check if directory still exists (may have been removed by openshift-install)
	if [[ ! -d "${CLUSTER_DIR}" ]]; then
		log_info "Cluster directory already removed: ${CLUSTER_DIR}"
		return 0
	fi

	# Safety check: prevent deletion of critical directories
	local resolved_dir
	resolved_dir="$(cd "${CLUSTER_DIR}" && pwd)"
	case "${resolved_dir}" in
		/|/home|/root|/etc|/var|/usr|/tmp|/bin|/sbin|/lib|/opt|/boot)
			log_error "Refusing to delete critical directory: ${resolved_dir}"
			return 1
		;;
	esac

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

################################################################################
# cleanup_on_exit: Trap handler for script exit
# Automatically cleans up resources on script failure
# Registered via: trap cleanup_on_exit EXIT
# Cleans up:
#   - Temporary cluster metadata
################################################################################
function cleanup_on_exit() {
	local exit_code=$?

	if [[ ${exit_code} -ne 0 ]]; then
		log_error "Script failed with exit code ${exit_code}"
	fi

	# If we have previously save the metadata
	if [[ -n "${TEMP_DIR}" ]] && [[ -d "${TEMP_DIR}" ]]
	then
		# And the metadata is missing
		if [[ ! -d "${CLUSTER_DIR}" ]]; then
			# Then copy it back
			mkdir -p "${CLUSTER_DIR}"
			/bin/cp "${TEMP_DIR}/metadata.json" "${CLUSTER_DIR}"
		fi

		/bin/rm -rf "${TEMP_DIR}"
	fi
}

trap cleanup_on_exit EXIT

#==============================================================================
# Main Execution
#==============================================================================

#------------------------------------------------------------------------------
# Function: main
# Description: Main entry point for the cluster deletion script. Orchestrates
#              the complete deletion workflow including:
#              1. Initialization and prerequisite checks
#              2. Information collection (directory, controller, cloud)
#              3. Connectivity verification
#              4. Cluster information display
#              5. Cluster destruction
#              6. Metadata cleanup
#              7. Optional directory removal
# Arguments:
#   $@ - Command line arguments (currently unused, reserved for future use)
# Exit Codes:
#   0 - Successful deletion
#   1 - Error during any phase (via die() calls in subfunctions)
# Workflow:
#   - Validates all prerequisites before making any changes
#   - Prompts for user confirmation before destructive operations
#   - Provides detailed logging throughout the process
#   - Handles errors gracefully with informative messages
#------------------------------------------------------------------------------
function main() {
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
	collect_cloud_name
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

	# Delete saved metadata from controller
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
