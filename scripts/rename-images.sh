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
# Script: rename-images.sh
# Description: Renames RHCOS images in OpenStack by adding a prefix
#
# Purpose:
#   Bulk rename OpenStack images belonging to a specific project by adding
#   a prefix to their names. Useful for organizing images or preparing them
#   for specific environments.
#
# Usage:
#   # Interactive mode (prompts for all inputs)
#   ./rename-images.sh
#
#   # Non-interactive mode with environment variables
#   CLOUD=mycloud PREFIX=prod- PROJECT=abc123 ./rename-images.sh
#
#   # Dry-run mode (preview changes without executing)
#   DRY_RUN=true CLOUD=mycloud PREFIX=test- PROJECT=abc123 ./rename-images.sh
#
#   # Custom image pattern
#   IMAGE_PATTERN="rhcos-4.15*" CLOUD=mycloud PREFIX=ocp- PROJECT=abc123 ./rename-images.sh
#
# Environment Variables:
#   CLOUD          - OpenStack cloud name from clouds.yaml (required)
#   PREFIX         - Prefix to add to image names (required)
#   PROJECT        - Project ID to filter images by ownership (required)
#   IMAGE_PATTERN  - Shell glob pattern for image names (optional, default: rhcos-*)
#   DRY_RUN        - Set to 'true' to preview changes (optional, default: false)
#
# Examples:
#   # Add 'prod-' prefix to all RHCOS images in project abc123
#   CLOUD=powervc PREFIX=prod- PROJECT=abc123 ./rename-images.sh
#
#   # Preview renaming with 'test-' prefix
#   DRY_RUN=true CLOUD=powervc PREFIX=test- PROJECT=abc123 ./rename-images.sh
#
#   # Rename only RHCOS 4.15 images
#   IMAGE_PATTERN="rhcos-4.15*" CLOUD=powervc PREFIX=ocp415- PROJECT=abc123 ./rename-images.sh
################################################################################

set -euo pipefail

#==============================================================================
# Global Variables
# Defines script-wide constants and configuration values
#==============================================================================
readonly SCRIPT_NAME="$(basename "${BASH_SOURCE[0]}")"
readonly SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# ANSI color codes for enhanced terminal output
readonly COLOR_RED='\033[0;31m'      # Error messages
readonly COLOR_GREEN='\033[0;32m'    # Success messages
readonly COLOR_YELLOW='\033[1;33m'   # Warning messages
readonly COLOR_BLUE='\033[0;34m'     # Info messages
readonly COLOR_RESET='\033[0m'       # Reset to default

#==============================================================================
# Utility Functions
# Reusable helper functions for logging, validation, and common operations
#==============================================================================

################################################################################
# Logging Functions
# Provide colored, categorized output for better user experience
################################################################################

################################################################################
# log_info: Display informational messages in blue
# Usage: log_info "message text"
################################################################################
function log_info() {
	echo -e "${COLOR_BLUE}[INFO]${COLOR_RESET} $*"
}

################################################################################
# log_success: Display success messages in green
# Usage: log_success "operation completed"
################################################################################
function log_success() {
	echo -e "${COLOR_GREEN}[SUCCESS]${COLOR_RESET} $*"
}

################################################################################
# log_warning: Display warning messages in yellow
# Usage: log_warning "potential issue detected"
################################################################################
function log_warning() {
	echo -e "${COLOR_YELLOW}[WARNING]${COLOR_RESET} $*"
}

################################################################################
# log_error: Display error messages in red to stderr
# Usage: log_error "operation failed"
################################################################################
function log_error() {
	echo -e "${COLOR_RED}[ERROR]${COLOR_RESET} $*" >&2
}

################################################################################
# die: Exit script with error message
# Logs error and terminates with exit code 1
# Usage: die "fatal error occurred"
################################################################################
function die() {
	log_error "$*"
	exit 1
}

################################################################################
# command_exists: Check if a command is available in PATH
# Returns: 0 if command exists, 1 otherwise
# Usage: if command_exists "jq"; then ...; fi
################################################################################
function command_exists() {
	command -v "$1" >/dev/null 2>&1
}

################################################################################
# is_var_set: Check if an environment variable is set and not empty
# Parameters:
#   $1 - Variable name to check
# Returns: 0 (true) if variable is set and non-empty, 1 (false) otherwise
# Usage: if is_var_set "CLUSTER_NAME"; then ...; fi
################################################################################
function is_var_set() {
	local var_name="$1"
	local var_value="${!var_name:-}"

	[[ -n "${var_value}" ]]
}

################################################################################
# validate_non_empty: Ensure environment variable is set and non-empty
# Parameters:
#   $1 - Variable name to validate
# Exits: With error if variable is empty or unset
# Usage: validate_non_empty "CLUSTER_NAME"
################################################################################
function validate_non_empty() {
	local var_name="$1"

	if ! is_var_set "${var_name}"; then
		die "${var_name} must be set and non-empty"
	fi
}

################################################################################
# prompt_input: Interactive prompt with validation and default values
# Parameters:
#   $1 - Prompt text to display
#   $2 - Variable name to store result
#   $3 - Default value (optional)
#   $4 - Allow empty input (default: false)
#   $5 - Hide input for secrets (default: false)
# Sets: Named variable with user input and exports it
# Usage: prompt_input "Enter name" "CLUSTER_NAME" "default" false false
################################################################################
function prompt_input() {
	local prompt_text="$1"
	local var_name="$2"
	local default_value="${3:-}"
	local allow_empty="${4:-false}"
	local is_secret="${5:-false}"

	local input_value

	if [[ "${is_secret}" == "true" ]]; then
		read -rsp "${prompt_text}: " input_value
		echo  # Add newline after hidden input
	else
		if [[ -n "${default_value}" ]]; then
			read -rp "${prompt_text} [${default_value}]: " input_value
			input_value="${input_value:-${default_value}}"
		else
			read -rp "${prompt_text} []: " input_value
		fi
	fi

	if [[ -z "${input_value}" ]] && [[ "${allow_empty}" != "true" ]]; then
		die "You must enter a value for ${var_name}"
	fi

	printf -v "${var_name}" '%s' "${input_value}"
	export "${var_name}"
}

################################################################################
# verify_openstack_resource: Verify OpenStack resource exists
# Parameters:
#   $1 - Resource type (image, flavor, network, keypair, etc.)
#   $2 - Resource name or ID
#   $3 - Cloud name (optional, defaults to $CLOUD)
# Exits: With error if resource not found
# Usage: verify_openstack_resource "image" "rhel-8" "mycloud"
################################################################################
function verify_openstack_resource() {
	local resource_type="$1"
	local resource_name="$2"
	local cloud="${3:-${CLOUD}}"

	log_info "Verifying ${resource_type}: ${resource_name}"

	if ! openstack --os-cloud="${cloud}" "${resource_type}" show "${resource_name}" >/dev/null 2>&1; then
		die "Cannot find ${resource_type} '${resource_name}'. Please verify OpenStack configuration."
	fi

	log_success "Found ${resource_type}: ${resource_name}"
}

################################################################################
# execute_with_check: Execute command with automatic error handling
# Parameters:
#   $1 - Description of operation (for logging)
#   $@ - Command and arguments to execute
# Exits: With error if command fails
# Usage: execute_with_check "Install packages" yum install -y package
################################################################################
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
# Main Functions
#==============================================================================

################################################################################
# check_required_programs: Verify all dependencies are installed
# Checks for required command-line tools in PATH
# Required programs:
#   - ocp-ipi-powervc-linux-{arch}: PowerVC automation tool
#   - openshift-install: OpenShift installer
#   - openstack: OpenStack CLI client
#   - jq: JSON processor
#   - getent: DNS resolution utility
# Exits: If any required program is missing
################################################################################
function check_required_programs() {
	local -a required_programs=("openstack")
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

################################################################################
# verify_openstack_connectivity: Test OpenStack API connectivity
# Performs a simple API call to verify authentication and connectivity
# Uses: $CLOUD environment variable for cloud selection
# Exits: If OpenStack is unreachable or authentication fails
################################################################################
function verify_openstack_connectivity() {
	log_info "Verifying OpenStack connectivity..."

	if ! openstack --os-cloud="${CLOUD}" image list >/dev/null 2>&1; then
		die "Cannot connect to OpenStack. Please verify clouds.yaml configuration."
	fi

	log_success "OpenStack connectivity verified"
}

################################################################################
# collect_environment_variables: Gather all required configuration
# Prompts user for any missing environment variables
# Handles both interactive and automated (pre-set variables) modes
# Sets and exports all required variables
# Environment variables:
#   CLOUD - OpenStack cloud name from clouds.yaml (required)
#   PREFIX - Prefix to add to image names (required)
#   PROJECT - Project ID to filter images (required)
#   IMAGE_PATTERN - Pattern to match image names (optional, default: rhcos-*)
#   DRY_RUN - Preview changes without executing (optional, default: false)
################################################################################
function collect_environment_variables() {
	log_info "Collecting environment variables..."

	# Cloud name from clouds.yaml
	if [[ ! -v CLOUD ]]; then
		prompt_input "What is the cloud name in ~/.config/openstack/clouds.yaml" "CLOUD"
	fi

	if [[ ! -v PREFIX ]]; then
		prompt_input "What prefix do you want to add to image names" "PREFIX"
	fi

	if [[ ! -v PROJECT ]]; then
		prompt_input "What project ID should we filter for" "PROJECT"
	fi
	
	# Optional: Image pattern (defaults to rhcos-* if not set)
	if [[ ! -v IMAGE_PATTERN ]]; then
		prompt_input "What image name pattern to match" "IMAGE_PATTERN" "rhcos-*" true
	fi
	
	# Optional: Dry-run mode
	if [[ ! -v DRY_RUN ]]; then
		DRY_RUN="false"
		export DRY_RUN
	fi

	log_success "All environment variables collected"
}

################################################################################
# validate_environment_variables: Ensure all required variables are set
# Validates that critical environment variables are non-empty
# Should be called after collect_environment_variables
# Exits: If any required variable is missing or empty
################################################################################
function validate_environment_variables() {
	log_info "Validating environment variables..."

	local -a required_vars=(
		"CLOUD"
		"PREFIX"
		"PROJECT"
	)

	for var in "${required_vars[@]}"; do
		validate_non_empty "${var}"
	done

	log_success "All environment variables validated"
}

################################################################################
# show_usage: Display script usage information
# Shows command-line options and examples
################################################################################
function show_usage() {
	cat <<-EOF
		Usage: ${SCRIPT_NAME} [OPTIONS]
		
		Rename OpenStack images by adding a prefix to their names.
		
		OPTIONS:
		    -h, --help          Show this help message
		    -d, --dry-run       Preview changes without executing
		    -c, --cloud NAME    OpenStack cloud name from clouds.yaml
		    -p, --prefix TEXT   Prefix to add to image names
		    -j, --project ID    Project ID to filter images
		    -i, --pattern GLOB  Image name pattern (default: rhcos-*)
		
		ENVIRONMENT VARIABLES:
		    CLOUD          - OpenStack cloud name (required)
		    PREFIX         - Prefix to add (required)
		    PROJECT        - Project ID (required)
		    IMAGE_PATTERN  - Image name pattern (optional)
		    DRY_RUN        - Set to 'true' for dry-run mode (optional)
		
		EXAMPLES:
		    # Interactive mode
		    ${SCRIPT_NAME}
		    
		    # Non-interactive with environment variables
		    CLOUD=powervc PREFIX=prod- PROJECT=abc123 ${SCRIPT_NAME}
		    
		    # Dry-run mode
		    ${SCRIPT_NAME} --dry-run --cloud powervc --prefix test- --project abc123
		    
		    # Custom pattern
		    IMAGE_PATTERN="rhcos-4.15*" CLOUD=powervc PREFIX=ocp- PROJECT=abc123 ${SCRIPT_NAME}
	EOF
}

################################################################################
# parse_arguments: Parse command-line arguments
# Parameters: All script arguments ($@)
# Sets global variables based on provided options
################################################################################
function parse_arguments() {
	while [[ $# -gt 0 ]]; do
		case "$1" in
			-h|--help)
				show_usage
				exit 0
				;;
			-d|--dry-run)
				DRY_RUN="true"
				export DRY_RUN
				shift
				;;
			-c|--cloud)
				CLOUD="$2"
				export CLOUD
				shift 2
				;;
			-p|--prefix)
				PREFIX="$2"
				export PREFIX
				shift 2
				;;
			-j|--project)
				PROJECT="$2"
				export PROJECT
				shift 2
				;;
			-i|--pattern)
				IMAGE_PATTERN="$2"
				export IMAGE_PATTERN
				shift 2
				;;
			*)
				log_error "Unknown option: $1"
				show_usage
				exit 1
				;;
		esac
	done
}

################################################################################
# cleanup_on_exit: Trap handler for script exit
# Automatically cleans up resources on script exit
# Registered via: trap cleanup_on_exit EXIT
# Cleans up:
#   - Temporary image list file
################################################################################
function cleanup_on_exit() {
	local exit_code=$?

	# Always clean up temporary files
	[[ -f "${FILE}" ]] && /bin/rm -f "${FILE}"

	if [[ ${exit_code} -ne 0 ]]; then
		log_error "Script failed with exit code ${exit_code}"
	fi
}

trap cleanup_on_exit EXIT

#==============================================================================
# Main Execution
#==============================================================================

FILE=$(mktemp)

# Dry-run mode flag (set to true to preview changes without executing)
DRY_RUN="${DRY_RUN:-false}"

################################################################################
# get_image_owner: Safely retrieve image owner with error handling
# Parameters:
#   $1 - Image UUID
#   $2 - Cloud name
# Returns: Owner ID via stdout, or empty string on error
# Usage: owner=$(get_image_owner "$uuid" "$cloud")
################################################################################
function get_image_owner() {
	local uuid="$1"
	local cloud="$2"
	local owner=""
	
	if ! owner=$(openstack --os-cloud="${cloud}" image show "${uuid}" \
		--format=value --column=owner 2>/dev/null); then
		log_warning "Failed to retrieve owner for image ${uuid}"
		return 1
	fi
	
	echo "${owner}"
}

################################################################################
# filter_images: Filter images based on pattern and ownership
# Parameters:
#   $1 - Image name
#   $2 - Image owner
#   $3 - Image pattern (optional, defaults to rhcos-*)
# Returns: 0 if image should be processed, 1 otherwise
################################################################################
function filter_images() {
	local name="$1"
	local owner="$2"
	local pattern="${3:-rhcos-*}"
	
	# Check if owner matches project
	if [[ "${owner}" != "${PROJECT}" ]]; then
		return 1
	fi
	
	# Check if name matches pattern
	if [[ ! "${name}" == ${pattern} ]]; then
		return 1
	fi
	
	# Skip if already has prefix
	if [[ "${name}" == ${PREFIX}* ]]; then
		log_info "Skipping '${name}' - already has prefix"
		return 1
	fi
	
	return 0
}

################################################################################
# rename_image: Rename a single image with error handling
# Parameters:
#   $1 - Image UUID
#   $2 - Current name
#   $3 - New name
#   $4 - Cloud name
# Returns: 0 on success, 1 on failure
################################################################################
function rename_image() {
	local uuid="$1"
	local current_name="$2"
	local new_name="$3"
	local cloud="$4"
	
	log_info "Renaming: '${current_name}' -> '${new_name}'"
	
	if [[ "${DRY_RUN}" == "true" ]]; then
		log_warning "[DRY-RUN] Would rename image ${uuid}"
		return 0
	fi
	
	if ! openstack --os-cloud="${cloud}" image set --name "${new_name}" "${uuid}" 2>&1; then
		log_error "Failed to rename image ${uuid}"
		return 1
	fi
	
	log_success "Successfully renamed image ${uuid}"
	return 0
}

################################################################################
# process_images: Main image processing loop
# Reads image list, filters, and renames matching images
# Uses global variables: CLOUD, PROJECT, PREFIX, FILE
################################################################################
function process_images() {
	local image_pattern="${IMAGE_PATTERN:-rhcos-*}"
	local total_images=0
	local processed_images=0
	local failed_images=0
	local skipped_images=0
	
	log_info "Processing images with pattern: ${image_pattern}"
	log_info "Target project: ${PROJECT}"
	log_info "Prefix to add: ${PREFIX}"
	
	if [[ "${DRY_RUN}" == "true" ]]; then
		log_warning "DRY-RUN MODE: No changes will be made"
	fi
	
	# Get image list
	if ! openstack --os-cloud="${CLOUD}" image list \
		--format=value --column=ID --column=Name --column=Status > "${FILE}" 2>&1; then
		die "Failed to retrieve image list from OpenStack"
	fi
	
	# Process each image
	while IFS=$' ' read -r uuid name status; do
		# Skip empty lines
		[[ -z "${uuid}" ]] && continue
		
		total_images=$((total_images + 1))
		
		# Get image owner
		local owner
		if ! owner=$(get_image_owner "${uuid}" "${CLOUD}"); then
			failed_images=$((failed_images + 1))
			continue
		fi
		
		# Filter images
		if ! filter_images "${name}" "${owner}" "${image_pattern}"; then
			skipped_images=$((skipped_images + 1))
			continue
		fi
		
		# Construct new name
		local new_name="${PREFIX}${name}"
		
		# Rename image
		if rename_image "${uuid}" "${name}" "${new_name}" "${CLOUD}"; then
			processed_images=$((processed_images + 1))
		else
			failed_images=$((failed_images + 1))
		fi
	#done < "${FILE}"
        done < <(grep "${IMAGE_PATTERN}" "${FILE}")
	
	# Display summary
	log_info "=========================================="
	log_info "Image Processing Summary"
	log_info "=========================================="
	log_info "Total images examined: ${total_images}"
	log_info "Images processed: ${processed_images}"
	log_info "Images skipped: ${skipped_images}"
	log_info "Images failed: ${failed_images}"
	log_info "=========================================="
	
	if [[ ${failed_images} -gt 0 ]]; then
		log_warning "Some images failed to process"
		return 1
	fi
	
	return 0
}

################################################################################
# confirm_operation: Ask user to confirm before proceeding
# Returns: 0 if user confirms, 1 otherwise
################################################################################
function confirm_operation() {
	if [[ "${DRY_RUN}" == "true" ]]; then
		return 0
	fi
	
	log_warning "This operation will rename images in project: ${PROJECT}"
	log_warning "Prefix to add: ${PREFIX}"
	log_warning "Cloud: ${CLOUD}"
	
	local response
	read -rp "Do you want to continue? (yes/no): " response
	
	if [[ "${response}" != "yes" ]]; then
		log_info "Operation cancelled by user"
		return 1
	fi
	
	return 0
}

################################################################################
# main: Primary entry point for image renaming
# Executes the complete workflow in logical phases:
#   Phase 0: Parse command-line arguments
#   Phase 1: Initialization and validation
#   Phase 2: Environment collection and verification
#   Phase 3: Image processing
# Each phase must complete successfully before proceeding to the next
################################################################################
function main() {
	log_info "Starting RHCOS image renaming script"
	log_info "Script: ${SCRIPT_NAME}"
	log_info "Working directory: $(pwd)"

	# Phase 0: Parse command-line arguments
	parse_arguments "$@"

	# Phase 1: Initialize and validate prerequisites
	check_required_programs

	# Phase 2: Collect and validate inputs
	collect_environment_variables
	verify_openstack_connectivity
	validate_environment_variables
	
	# Display configuration
	log_info "Configuration:"
	log_info "  Cloud: ${CLOUD}"
	log_info "  Project: ${PROJECT}"
	log_info "  Prefix: ${PREFIX}"
	log_info "  Pattern: ${IMAGE_PATTERN:-rhcos-*}"
	log_info "  Dry-run: ${DRY_RUN}"
	
	# Phase 3: Confirm and process images
	if ! confirm_operation; then
		log_info "Operation cancelled"
		exit 0
	fi
	
	if ! process_images; then
		die "Image processing completed with errors"
	fi

	# Success!
	if [[ "${DRY_RUN}" == "true" ]]; then
		log_success "Dry-run completed successfully! No changes were made."
	else
		log_success "Image renaming completed successfully!"
	fi
}

# Run main function
main "$@"

# Made with Bob
