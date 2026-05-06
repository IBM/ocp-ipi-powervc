#!/usr/bin/env bash

# Copyright 2026 IBM Corp
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# cleanup-containers.sh - Clean up OpenStack containers and objects
#
# This script removes all containers and their objects from an OpenStack cloud.
# It processes containers one at a time, deleting all objects before removing
# the container itself. Optionally filters by infrastructure ID.
#
# USAGE:
#   ./cleanup-containers.sh [INFRA_ID]
#
# REQUIRED ENVIRONMENT VARIABLES:
#   CLOUD - OpenStack cloud name from clouds.yaml
#
# OPTIONAL ARGUMENTS:
#   INFRA_ID - Filter containers by infrastructure ID (e.g., cluster-abc123)
#
# FEATURES:
#   - Validates required environment variables
#   - Checks for required programs (openstack)
#   - Processes containers and objects sequentially for reliability
#   - Optional filtering by infrastructure ID
#   - Detailed logging with statistics
#   - Error handling with continue-on-error for individual operations
#
# EXIT CODES:
#   0 - Success (all containers processed)
#   1 - Missing required environment variable
#   2 - Missing required program
#
# EXAMPLES:
#   # Clean up all containers
#   CLOUD=powervc ./cleanup-containers.sh
#
#   # Clean up containers for specific cluster
#   CLOUD=powervc ./cleanup-containers.sh cluster-abc123

set -euo pipefail

#==============================================================================
# Global Variables
#==============================================================================

readonly SCRIPT_NAME="$(basename "${BASH_SOURCE[0]}")"
readonly SCRIPT_VERSION="2.0"

# Exit codes
readonly EXIT_SUCCESS=0
readonly EXIT_MISSING_ENV_VAR=1
readonly EXIT_MISSING_PROGRAM=2

# ANSI color codes for enhanced terminal output
readonly COLOR_RED='\033[0;31m'      # Error messages
readonly COLOR_GREEN='\033[0;32m'    # Success messages
readonly COLOR_YELLOW='\033[1;33m'   # Warning messages
readonly COLOR_BLUE='\033[0;34m'     # Info messages
readonly COLOR_RESET='\033[0m'       # Reset to default

#==============================================================================
# Utility Functions
#==============================================================================

#------------------------------------------------------------------------------
# log_info - Print informational message
#
# Arguments:
#   $@ - Message to log
#------------------------------------------------------------------------------
function log_info() {
	echo -e "${COLOR_BLUE}[INFO]${COLOR_RESET} $*"
}

#------------------------------------------------------------------------------
# log_success - Print success message
#
# Arguments:
#   $@ - Message to log
#------------------------------------------------------------------------------
function log_success() {
	echo -e "${COLOR_GREEN}[SUCCESS]${COLOR_RESET} $*"
}

#------------------------------------------------------------------------------
# log_warning - Print warning message
#
# Arguments:
#   $@ - Message to log
#------------------------------------------------------------------------------
function log_warning() {
	echo -e "${COLOR_YELLOW}[WARNING]${COLOR_RESET} $*"
}

#------------------------------------------------------------------------------
# log_error - Print error message to stderr
#
# Arguments:
#   $@ - Message to log
#------------------------------------------------------------------------------
function log_error() {
	echo -e "${COLOR_RED}[ERROR]${COLOR_RESET} $*" >&2
}

#------------------------------------------------------------------------------
# die - Exit with error message
#
# Arguments:
#   $@ - Error message
#------------------------------------------------------------------------------
function die() {
	log_error "$*"
	exit 1
}

#------------------------------------------------------------------------------
# command_exists - Check if a command exists
#
# Arguments:
#   $1 - Command name
#
# Returns:
#   0 - Command exists
#   1 - Command not found
#------------------------------------------------------------------------------
function command_exists() {
	command -v "$1" >/dev/null 2>&1
}

#==============================================================================
# Validation Functions
#==============================================================================

#------------------------------------------------------------------------------
# validate_environment_variables - Check required environment variables
#
# Validates that CLOUD environment variable is set and non-empty.
#
# Returns:
#   0 - All required variables are set
#   1 - One or more variables are missing or empty
#------------------------------------------------------------------------------
function validate_environment_variables() {
	log_info "Validating required environment variables"

	if [[ ! -v CLOUD ]]; then
		log_error "Required environment variable CLOUD is not set"
		log_error "Please set CLOUD to your OpenStack cloud name from clouds.yaml"
		return 1
	fi

	if [[ -z "${CLOUD}" ]]; then
		log_error "Required environment variable CLOUD is set but empty"
		return 1
	fi

	log_success "Environment variable CLOUD is set: ${CLOUD}"
	return 0
}

#------------------------------------------------------------------------------
# validate_programs - Check required programs are available
#
# Validates that openstack CLI is installed and accessible.
#
# Returns:
#   0 - All required programs are available
#   1 - One or more programs are missing
#------------------------------------------------------------------------------
function validate_programs() {
	log_info "Validating required programs"

	if ! command_exists "openstack"; then
		log_error "Required program 'openstack' is not installed or not in PATH"
		log_error "Please install python-openstackclient"
		return 1
	fi

	log_success "All required programs are available"
	return 0
}

#==============================================================================
# Cleanup Functions
#==============================================================================

#------------------------------------------------------------------------------
# cleanup_containers - Remove all containers and their objects
#
# Processes each container matching the optional filter, deleting all objects
# before removing the container itself. Continues on individual failures to
# process as many containers as possible.
#
# Arguments:
#   $1 - (optional) Infrastructure ID filter
#
# Returns:
#   0 - Always returns success after processing all containers
#------------------------------------------------------------------------------
function cleanup_containers() {
	local infra_id_filter="${1:-}"
	local container_count=0
	local object_count=0
	local failed_objects=0
	local failed_containers=0

	log_info "Starting container cleanup for cloud: ${CLOUD}"
	if [[ -n "${infra_id_filter}" ]]; then
		log_info "Filtering containers by infrastructure ID: ${infra_id_filter}"
	fi

	# Check if we can connect to the cloud
	if ! openstack --os-cloud="${CLOUD}" container list --format csv >/dev/null; then
		log_warning "Failed to list containers - check cloud connectivity and credentials"
		return 0
	fi

	# Process each container
	while IFS= read -r container; do
		[[ -z "${container}" ]] && continue

		container_count=$((container_count + 1))
		log_info "Processing container #${container_count}: ${container}"

		# Delete all objects in the container
		local container_objects=0
		while IFS= read -r object; do
			[[ -z "${object}" ]] && continue

			object_count=$((object_count + 1))
			container_objects=$((container_objects + 1))
			log_info "  Deleting object ${container_objects}: ${object}"

			if ! openstack --os-cloud="${CLOUD}" object delete "${container}" "${object}" 2>/dev/null; then
				log_warning "  Failed to delete object: ${object}"
				failed_objects=$((failed_objects + 1))
			fi
		done < <(

			if [[ -n "${infra_id_filter}" ]]; then
				openstack --os-cloud="${CLOUD}" object list "${container}" --format value -c Name 2>/dev/null | \
					grep -F -- "${infra_id_filter}" || true
			else
				openstack --os-cloud="${CLOUD}" object list "${container}" --format value -c Name 2>/dev/null
			fi
		)

		log_info "  Processed ${container_objects} objects from container: ${container}"

		# Delete the container itself
		log_info "  Deleting container: ${container}"
		if ! openstack --os-cloud="${CLOUD}" container delete "${container}" 2>/dev/null; then
			log_warning "  Failed to delete container: ${container}"
			failed_containers=$((failed_containers + 1))
		else
			log_success "  Container deleted: ${container}"
		fi
	done < <(
		if [[ -n "${infra_id_filter}" ]]; then
			openstack --os-cloud="${CLOUD}" container list --format value -c Name 2>/dev/null | \
				grep -F -- "${infra_id_filter}" || true
		else
			openstack --os-cloud="${CLOUD}" container list --format value -c Name 2>/dev/null
		fi
	)

	# Print summary
	echo ""
	log_info "Cleanup Summary:"
	log_info "  Containers processed: ${container_count}"
	log_info "  Objects processed: ${object_count}"
	if [[ ${failed_objects} -gt 0 ]] || [[ ${failed_containers} -gt 0 ]]; then
		log_warning "  Failed object deletions: ${failed_objects}"
		log_warning "  Failed container deletions: ${failed_containers}"
	else
		log_success "  All operations completed successfully"
	fi

	return 0
}

#==============================================================================
# Main Function
#==============================================================================

#------------------------------------------------------------------------------
# main - Main entry point for the script
#
# Orchestrates the complete workflow:
#   1. Validates environment variables
#   2. Validates required programs
#   3. Cleans up containers and objects
#
# Arguments:
#   $1 - (optional) Infrastructure ID filter
#
# Exit Codes:
#   EXIT_SUCCESS (0)           - Normal exit
#   EXIT_MISSING_ENV_VAR (1)   - Missing environment variable
#   EXIT_MISSING_PROGRAM (2)   - Missing required program
#------------------------------------------------------------------------------
function main() {
	local infra_id_filter="${1:-}"

	log_info "=========================================="
	log_info "${SCRIPT_NAME} v${SCRIPT_VERSION} starting"
	log_info "=========================================="
	echo ""

	# Validate environment
	if ! validate_environment_variables; then
		log_error "Environment validation failed"
		exit ${EXIT_MISSING_ENV_VAR}
	fi
	echo ""

	# Validate programs
	if ! validate_programs; then
		log_error "Program validation failed"
		exit ${EXIT_MISSING_PROGRAM}
	fi
	echo ""

	# Cleanup containers
	cleanup_containers "${infra_id_filter}"
	echo ""

	log_success "Container cleanup completed"
	exit ${EXIT_SUCCESS}
}

# Execute main function
main "$@"

# Made with Bob
