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

#==============================================================================
# Script: print-stream-json.sh
# Description: Download CoreOS JSON metadata and verify RHCOS images exist in OpenStack
#
# Features:
#   - Support for multiple release versions via --release parameter
#   - Multiple output formats: text, JSON, CSV
#   - Verbose mode for detailed debugging
#   - Dry-run mode for testing without actual verification
#   - Automatic fallback to multiple CoreOS JSON sources (rhcos.json, rhel-9, rhel-10)
#   - RHEL version preference (rhel9 or rhel10)
#   - Optional project name prefix for image names
#   - Comprehensive error handling and reporting
#   - Execution time tracking and summary statistics
#
# Usage: See --help for detailed usage information
#==============================================================================

set -euo pipefail

#==============================================================================
# Global Variables
#==============================================================================
readonly SCRIPT_NAME="$(basename "${BASH_SOURCE[0]}")"
readonly SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Array to store multiple release versions
declare -a RELEASES=()

# Options
VERBOSE=false
DRY_RUN=false
QUIET=false
OUTPUT_FORMAT="text"  # text, json, or csv
RHEL_VERSION=""       # rhel9, rhel10, or empty (auto-detect)

# ANSI color codes for enhanced terminal output
readonly COLOR_RED='\033[0;31m'      # Error messages
readonly COLOR_GREEN='\033[0;32m'    # Success messages
readonly COLOR_YELLOW='\033[1;33m'   # Warning messages
readonly COLOR_BLUE='\033[0;34m'     # Info messages
readonly COLOR_CYAN='\033[0;36m'     # Debug messages
readonly COLOR_RESET='\033[0m'       # Reset to default

#==============================================================================
# Utility Functions
#==============================================================================

#------------------------------------------------------------------------------
# Function: log_info
# Description: Print informational message in blue color (suppressed in quiet mode)
# Arguments:
#   $* - Message to display
# Returns: None
# Global Variables:
#   QUIET - If true, suppresses output
# Example: log_info "Processing release 4.21"
#------------------------------------------------------------------------------
function log_info() {
	if [[ "${QUIET}" != "true" ]]; then
		echo -e "${COLOR_BLUE}[INFO]${COLOR_RESET} $*"
	fi
}

#------------------------------------------------------------------------------
# Function: log_success
# Description: Print success message in green color (suppressed in quiet mode)
# Arguments:
#   $* - Message to display
# Returns: None
# Global Variables:
#   QUIET - If true, suppresses output
# Example: log_success "All releases processed successfully"
#------------------------------------------------------------------------------
function log_success() {
	if [[ "${QUIET}" != "true" ]]; then
		echo -e "${COLOR_GREEN}[SUCCESS]${COLOR_RESET} $*"
	fi
}

#------------------------------------------------------------------------------
# Function: log_warning
# Description: Print warning message in yellow color (suppressed in quiet mode)
# Arguments:
#   $* - Message to display
# Returns: None
# Global Variables:
#   QUIET - If true, suppresses output
# Example: log_warning "No release specified, using default"
#------------------------------------------------------------------------------
function log_warning() {
	if [[ "${QUIET}" != "true" ]]; then
		echo -e "${COLOR_YELLOW}[WARNING]${COLOR_RESET} $*"
	fi
}

#------------------------------------------------------------------------------
# Function: log_error
# Description: Print error message in red color to stderr unless quiet mode is enabled
# Arguments:
#   $* - Error message to display
# Returns: None
# Note: Errors are suppressed in QUIET mode; use die() for critical errors
# Example: log_error "Failed to download file"
#------------------------------------------------------------------------------
function log_error() {
	if [[ "${QUIET}" != "true" ]]; then
		echo -e "${COLOR_RED}[ERROR]${COLOR_RESET} $*" >&2
	fi
}

#------------------------------------------------------------------------------
# Function: log_debug
# Description: Print debug message in cyan color (only when VERBOSE=true)
# Arguments:
#   $* - Debug message to display
# Returns: None
# Global Variables:
#   VERBOSE - Controls whether debug messages are shown
# Example: log_debug "HTTP status code: 200"
#------------------------------------------------------------------------------
function log_debug() {
	if [[ "${VERBOSE}" == "true" ]]; then
		echo -e "${COLOR_CYAN}[DEBUG]${COLOR_RESET} $*"
	fi
}

#------------------------------------------------------------------------------
# Function: die
# Description: Print error message and exit with status 1
# Arguments:
#   $* - Error message to display before exiting
# Returns: Never returns (exits script)
# Example: die "Cannot connect to OpenStack"
#------------------------------------------------------------------------------
function die() {
	local save_quiet=${QUIET}
	QUIET=false
	log_error "$*"
	QUIET=${save_quiet}
	exit 1
}

#------------------------------------------------------------------------------
# Function: command_exists
# Description: Check if a command/program is available in PATH
# Arguments:
#   $1 - Command name to check
# Returns:
#   0 - Command exists
#   1 - Command not found
# Example: command_exists "curl" && echo "curl is available"
#------------------------------------------------------------------------------
function command_exists() {
	command -v "$1" >/dev/null 2>&1
}

#------------------------------------------------------------------------------
# Function: check_required_programs
# Description: Verify all required programs are installed and available
# Arguments: None
# Returns:
#   0 - All required programs found
#   1 - One or more programs missing (exits via die)
# Required Programs:
#   - curl: For downloading files
#   - jq: For JSON parsing
# Example: check_required_programs
#------------------------------------------------------------------------------
function check_required_programs() {
	local -a required_programs=("curl" "jq")
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
# Function: check_openstack_cli
# Description: Verify OpenStack CLI is available (skipped in dry-run mode)
# Arguments: None
# Returns:
#   0 - OpenStack CLI found or dry-run mode enabled
#   1 - OpenStack CLI not found (exits via die)
# Global Variables:
#   DRY_RUN - If true, skips the check
# Example: check_openstack_cli
#------------------------------------------------------------------------------
function check_openstack_cli() {
	if [[ "${DRY_RUN}" != "true" ]]; then
		if ! command_exists "openstack"; then
			die "Missing required program: openstack (required for verification, use --dry-run to skip)"
		fi
	fi
}

#------------------------------------------------------------------------------
# is_var_set: Check if an environment variable is set and not empty
# Parameters:
#   $1 - Variable name to check
# Returns: 0 (true) if variable is set and non-empty, 1 (false) otherwise
# Usage: if is_var_set "CLUSTER_NAME"; then ...; fi
#------------------------------------------------------------------------------
function is_var_set() {
	local var_name="$1"
	local var_value="${!var_name:-}"

	[[ -n "${var_value}" ]]
}

#------------------------------------------------------------------------------
# Function: validate_non_empty
# Description: Validate that a variable is set and non-empty
# Arguments:
#   $1 - Variable name to validate (not the value, the name)
# Returns:
#   0 - Variable is set and non-empty
#   1 - Variable is empty or unset (exits via die)
# Example: validate_non_empty "CLOUD"
#------------------------------------------------------------------------------
function validate_non_empty() {
	local var_name="$1"

	if ! is_var_set "${var_name}"; then
		die "${var_name} must be set and non-empty"
	fi
}

#------------------------------------------------------------------------------
# Function: validate_environment_variables
# Description: Validate all required environment variables are set
# Arguments: None
# Returns:
#   0 - All required variables validated
#   1 - One or more variables missing (exits via die)
# Required Variables:
#   CLOUD - OpenStack cloud name from clouds.yaml
# Example: validate_environment_variables
#------------------------------------------------------------------------------
function validate_environment_variables() {
	log_info "Validating environment variables..."

	local -a required_vars=(
		"CLOUD"
	)

	for var in "${required_vars[@]}"; do
		validate_non_empty "${var}"
	done

	log_success "All environment variables validated"
}

#------------------------------------------------------------------------------
# Function: verify_openstack_connectivity
# Description: Test connection to OpenStack using configured cloud
# Arguments: None
# Returns:
#   0 - Successfully connected to OpenStack
#   1 - Cannot connect (exits via die)
# Global Variables:
#   CLOUD - OpenStack cloud name to use
# Example: verify_openstack_connectivity
#------------------------------------------------------------------------------
function verify_openstack_connectivity() {
	log_info "Verifying OpenStack connectivity..."

	if ! openstack --os-cloud="${CLOUD}" image list >/dev/null 2>&1; then
		die "Cannot connect to OpenStack. Please verify clouds.yaml configuration."
	fi

	log_success "OpenStack connectivity verified"
}

#------------------------------------------------------------------------------
# Function: verify_openstack_resource
# Description: Verify that a specific OpenStack resource exists
# Arguments:
#   $1 - Resource type (e.g., "image", "network", "flavor")
#   $2 - Resource name to verify
#   $3 - Cloud name (optional, defaults to $CLOUD)
# Returns:
#   0 - Resource found
#   1 - Resource not found
# Global Variables:
#   CLOUD - Default OpenStack cloud name
#   DRY_RUN - If true, skips actual verification
# Example: verify_openstack_resource "image" "rhcos-4.21.0"
#------------------------------------------------------------------------------
function verify_openstack_resource() {
	local resource_type="$1"
	local resource_name="$2"
	local cloud="${3:-${CLOUD}}"

	if [[ "${DRY_RUN}" == "true" ]]; then
		log_info "[DRY RUN] Would verify ${resource_type}: ${resource_name}"
		return 0
	fi

	log_info "Verifying ${resource_type}: ${resource_name}"

	if ! openstack --os-cloud="${cloud}" "${resource_type}" show "${resource_name}" >/dev/null 2>&1; then
		log_error "Cannot find ${resource_type} '${resource_name}'"
		return 1
	fi

	log_success "Found ${resource_type}: ${resource_name}"
	return 0
}

#------------------------------------------------------------------------------
# Function: parse_arguments
# Description: Parse and validate command-line arguments
# Arguments:
#   $@ - All command-line arguments passed to script
# Returns:
#   0 - Arguments parsed successfully
#   1 - Invalid arguments (exits via die)
# Global Variables Modified:
#   RELEASES - Array of release versions to process
#   VERBOSE - Enable verbose/debug output
#   DRY_RUN - Enable dry-run mode
#   QUIET - Suppress log output (except errors)
#   OUTPUT_FORMAT - Output format (text, json, csv)
#   RHEL_VERSION - RHEL version preference (rhel9, rhel10, or empty)
#   CLOUD - OpenStack cloud name (optional, can be set via environment)
#   PROJECT - Project name prefix for image names (optional)
# Supported Options:
#   --release <version> - Specify release (can be used multiple times)
#   -v, --verbose - Enable verbose output
#   -q, --quiet - Suppress log output (except errors)
#   --dry-run - Simulate operations
#   --format <type> - Output format (text, json, csv)
#   --rhel <version> - RHEL version preference (rhel9 or rhel10)
#   --cloud <name> - OpenStack cloud name (overrides CLOUD env var)
#   --project <name> - Project name prefix for image names
#   -h, --help - Show usage information
# Example: parse_arguments "$@"
#------------------------------------------------------------------------------
function parse_arguments() {
	while [[ $# -gt 0 ]]; do
		case "$1" in
			--release)
				if [[ -z "${2:-}" ]]; then
					die "Error: --release requires a value"
				fi
				RELEASES+=("$2")
				shift 2
				;;
			-v|--verbose)
				VERBOSE=true
				shift
				;;
			-q|--quiet)
				QUIET=true
				shift
				;;
			--dry-run)
				DRY_RUN=true
				shift
				;;
			--format)
				if [[ -z "${2:-}" ]]; then
					die "Error: --format requires a value (text, json, or csv)"
				fi
				case "$2" in
					text|json|csv)
						OUTPUT_FORMAT="$2"
						;;
					*)
						die "Error: Invalid format '$2'. Must be text, json, or csv"
						;;
				esac
				shift 2
				;;
			--rhel)
				if [[ -z "${2:-}" ]]; then
					die "Error: --rhel requires a value (rhel9 or rhel10)"
				fi
				case "$2" in
					rhel9|rhel10)
						RHEL_VERSION="$2"
						;;
					*)
						die "Error: Invalid RHEL version '$2'. Must be rhel9 or rhel10"
						;;
				esac
				shift 2
				;;
			--cloud)
				if [[ -z "${2:-}" ]]; then
					die "Error: --cloud requires a value"
				fi
				CLOUD="${2}"
				shift 2
				;;
			--project)
				if [[ -z "${2:-}" ]]; then
					die "Error: --project requires a value"
				fi
				PROJECT="${2}"
				shift 2
				;;
			-h|--help)
				show_usage
				exit 0
				;;
			*)
				die "Unknown option: $1. Use --help for usage information."
				;;
		esac
	done

	# If no releases specified, use default
	if [[ ${#RELEASES[@]} -eq 0 ]]; then
		RELEASES=("release-4.21")
		log_warning "No release specified, using default: release-4.21"
	fi

	# Verbose and quiet are mutually exclusive
	if [[ "${VERBOSE}" == "true" && "${QUIET}" == "true" ]]; then
		die "Error: --verbose and --quiet cannot be used together"
	fi

	# Auto-enable quiet mode for structured output formats
	if [[ "${OUTPUT_FORMAT}" != "text" && "${QUIET}" != "true" ]]; then
		log_info "Enabling quiet mode for structured output format: ${OUTPUT_FORMAT}"
		QUIET=true
	fi

	log_debug "Parsed arguments: RELEASES=(${RELEASES[*]}), VERBOSE=${VERBOSE}, QUIET=${QUIET}, DRY_RUN=${DRY_RUN}, OUTPUT_FORMAT=${OUTPUT_FORMAT}, RHEL_VERSION=${RHEL_VERSION}"
}

#------------------------------------------------------------------------------
# Function: show_usage
# Description: Display comprehensive usage information and examples
# Arguments: None
# Returns: None
# Output: Prints usage documentation to stdout
# Example: show_usage
#------------------------------------------------------------------------------
function show_usage() {
	cat <<EOF
Usage: ${SCRIPT_NAME} [OPTIONS]

Download CoreOS JSON metadata and verify RHCOS images exist in OpenStack.

OPTIONS:
    --release <version>    Specify a release version (can be used multiple times)
                          Example: --release release-4.21 --release release-4.22
    --rhel <version>      Prefer specific RHEL version: rhel9 or rhel10
                          If not specified, tries all available versions
    --cloud <name>        OpenStack cloud name (overrides CLOUD env var)
    --project <name>      Project name prefix to prepend to image names
    -v, --verbose         Enable verbose output with debug information
    -q, --quiet           Suppress log output (errors still shown)
                          Cannot be used with --verbose
    --dry-run             Simulate operations without making actual changes
    --format <type>       Output format: text (default), json, or csv
    -h, --help            Show this help message

ENVIRONMENT VARIABLES:
    CLOUD                 OpenStack cloud name from clouds.yaml (required unless --cloud used)
    PROJECT               Project name prefix for image names (optional unless --project used)
    DEBUG                 Enable debug mode (optional, default: false)

EXAMPLES:
    # Single release
    ${SCRIPT_NAME} --release release-4.21

    # Multiple releases
    ${SCRIPT_NAME} --release release-4.21 --release release-4.22

    # Specify RHEL 9 version
    ${SCRIPT_NAME} --release release-4.21 --rhel rhel9

    # Specify RHEL 10 version
    ${SCRIPT_NAME} --release release-4.22 --rhel rhel10

    # Multiple releases with verbose output
    ${SCRIPT_NAME} --release release-4.21 --release release-4.22 --verbose

    # Quiet mode (only errors and structured output)
    ${SCRIPT_NAME} --release release-4.21 --quiet

    # Quiet mode with JSON output
    ${SCRIPT_NAME} --release release-4.21 --quiet --format json

    # Dry run to test without verification
    ${SCRIPT_NAME} --release release-4.21 --dry-run

    # Output in JSON format
    ${SCRIPT_NAME} --release release-4.21 --format json

    # Use default release
    ${SCRIPT_NAME}

EOF
}

#------------------------------------------------------------------------------
# Function: can_curl
# Description: Check if a URL is accessible and returns HTTP 200
# Arguments:
#   $1 - URL to check
# Returns:
#   0 - URL is accessible (HTTP 200)
#   1 - URL is not accessible or returns non-200 status
# Note: curl doesn't return error for 404, so we check HTTP status code
# Example: can_curl "https://example.com/file.json" && echo "URL is valid"
#------------------------------------------------------------------------------
function can_curl() {
	local url="$1"
	local http_code

	log_debug "Checking URL: ${url}"
	http_code=$(curl --silent --location --max-time 30 --connect-timeout 10 --output /dev/null --write-out "%{http_code}" "${url}")
	log_debug "HTTP status code: ${http_code}"

	if [[ "${http_code}" -ne 200 ]]; then
		return 1
	fi
	return 0
}

#------------------------------------------------------------------------------
# Function: download_coreos_json
# Description: Download CoreOS JSON from multiple possible URL locations
# Arguments:
#   $1 - Release version (e.g., "release-4.21")
# Returns:
#   0 - Successfully downloaded from one of the URLs
#   1 - Failed to download from all URLs
# Global Variables:
#   FILE1 - Temporary file path where JSON will be saved
#   RHEL_VERSION - If set, prioritizes specific RHEL version
# URL Priority:
#   If RHEL_VERSION is set: tries that version first, then others
#   If not set: tries rhcos.json, then rhel-9, then rhel-10
# Example: download_coreos_json "release-4.21"
#------------------------------------------------------------------------------
function download_coreos_json() {
	local release="$1"
	local -a urls=()

	# Build URL list based on RHEL_VERSION preference
	if [[ "${RHEL_VERSION}" == "rhel9" ]]; then
		urls=(
			"https://raw.githubusercontent.com/openshift/installer/refs/heads/${release}/data/data/coreos/coreos-rhel-9.json"
			"https://raw.githubusercontent.com/openshift/installer/refs/heads/${release}/data/data/coreos/rhcos.json"
			"https://raw.githubusercontent.com/openshift/installer/refs/heads/${release}/data/data/coreos/coreos-rhel-10.json"
		)
		log_debug "Prioritizing RHEL 9 CoreOS JSON"
	elif [[ "${RHEL_VERSION}" == "rhel10" ]]; then
		urls=(
			"https://raw.githubusercontent.com/openshift/installer/refs/heads/${release}/data/data/coreos/coreos-rhel-10.json"
			"https://raw.githubusercontent.com/openshift/installer/refs/heads/${release}/data/data/coreos/rhcos.json"
			"https://raw.githubusercontent.com/openshift/installer/refs/heads/${release}/data/data/coreos/coreos-rhel-9.json"
		)
		log_debug "Prioritizing RHEL 10 CoreOS JSON"
	else
		urls=(
			"https://raw.githubusercontent.com/openshift/installer/refs/heads/${release}/data/data/coreos/rhcos.json"
			"https://raw.githubusercontent.com/openshift/installer/refs/heads/${release}/data/data/coreos/coreos-rhel-9.json"
			"https://raw.githubusercontent.com/openshift/installer/refs/heads/${release}/data/data/coreos/coreos-rhel-10.json"
		)
		log_debug "Trying all CoreOS JSON variants in default order"
	fi

	for url in "${urls[@]}"; do
		log_debug "Trying URL: ${url}"
		if can_curl "${url}"; then
			if curl --silent --location --output "${FILE1}" "${url}"; then
				log_info "Downloaded ${url}"
				return 0
			else
				log_warning "Failed to download from ${url}"
			fi
		else
			log_debug "URL not available: ${url}"
		fi
	done

	log_error "Could not download CoreOS JSON from any known location for release ${release}"
	return 1
}

#------------------------------------------------------------------------------
# Function: extract_image_info
# Description: Extract image metadata from downloaded CoreOS JSON
# Arguments:
#   $1 - Name of associative array to populate (passed by reference)
# Returns:
#   0 - Successfully extracted all information
#   1 - Failed to extract required information
# Global Variables:
#   FILE1 - Input JSON file path
#   FILE2 - Temporary file for intermediate processing
# Extracted Fields:
#   download_url - URL to download the image
#   filename - Image filename (without .qcow2.gz extension)
#   sha256 - SHA256 checksum of the image
# Example:
#   declare -A image_info
#   extract_image_info image_info
#------------------------------------------------------------------------------
function extract_image_info() {
	local -n result=$1

	if ! jq -r '.architectures.ppc64le.artifacts.openstack' "${FILE1}" > "${FILE2}" 2>/dev/null; then
		log_error "Failed to extract OpenStack artifacts from JSON"
		return 1
	fi

	result[download_url]=$(jq -r '.formats."qcow2.gz".disk.location' "${FILE2}" 2>/dev/null)
	if [[ -z "${result[download_url]}" || "${result[download_url]}" == "null" ]]; then
		log_error "Failed to extract download URL from JSON"
		return 1
	fi

	result[filename]="${result[download_url]##*/}"
	result[filename]="${result[filename]%.qcow2.gz}"
	# Optionally prepend project name if PROJECT variable is set
	if is_var_set PROJECT; then
		log_info "Prepending project name (${PROJECT}) to RHCOS filename"
		result[filename]="${PROJECT}${result[filename]}"
	fi
	result[sha256]=$(jq -r '.formats."qcow2.gz".disk.sha256' "${FILE2}" 2>/dev/null)

	return 0
}

#------------------------------------------------------------------------------
# Function: csv_escape
# Description: Escape a value for safe CSV output according to RFC 4180 rules
# Arguments:
#   $1 - Raw field value
# Returns: None
# Output: CSV-safe field with embedded double quotes escaped
# Example: csv_escape 'a,b"c'
#------------------------------------------------------------------------------
function csv_escape() {
	local value="${1:-}"
	value=${value//\"/\"\"}
	printf '"%s"' "${value}"
}

#------------------------------------------------------------------------------
# Function: output_result
# Description: Output release processing result in specified format
# Arguments:
#   $1 - Release version
#   $2 - Name of associative array with image info (passed by reference)
#   $3 - Status (success, failed, not_found)
# Returns: None
# Global Variables:
#   OUTPUT_FORMAT - Determines output format (text, json, csv)
# Output Formats:
#   text - Human-readable (handled by log functions)
#   json - JSON object with all fields
#   csv - Comma-separated values with proper escaping
# Example: output_result "release-4.21" image_info "success"
#------------------------------------------------------------------------------
function output_result() {
	local release="$1"
	local -n info=$2
	local status="$3"

	case "${OUTPUT_FORMAT}" in
		json)
			jq -n \
				--arg release "${release}" \
				--arg status "${status}" \
				--arg filename "${info[filename]}" \
				--arg download_url "${info[download_url]}" \
				--arg sha256 "${info[sha256]:-}" \
				'{
				  release: $release,
				  status: $status,
				  filename: $filename,
				  download_url: $download_url,
				  sha256: $sha256
				}'
			;;
		csv)
			printf '%s,%s,%s,%s,%s\n' \
				"$(csv_escape "${release}")" \
				"$(csv_escape "${status}")" \
				"$(csv_escape "${info[filename]}")" \
				"$(csv_escape "${info[download_url]}")" \
				"$(csv_escape "${info[sha256]:-}")"
			;;
		text)
			# Already handled by log messages
			;;
	esac
}

#------------------------------------------------------------------------------
# Function: process_release
# Description: Process a single release - download JSON, extract info, verify
# Arguments:
#   $1 - Release version to process (e.g., "release-4.21")
# Returns:
#   0 - Release processed successfully
#   1 - Failed to process release
# Processing Steps:
#   1. Download CoreOS JSON from GitHub
#   2. Extract image metadata (URL, filename, SHA256)
#   3. Verify image exists in OpenStack
#   4. Output result in specified format
# Example: process_release "release-4.21"
#------------------------------------------------------------------------------
function process_release() {
	local release="$1"
	declare -A image_info
	# Initialize with default values for failure cases
	image_info[filename]=""
	image_info[download_url]=""
	image_info[sha256]=""

	log_info "Processing release: ${release}"

	# Download CoreOS JSON
	if ! download_coreos_json "${release}"; then
		output_result "${release}" image_info "failed"
		return 1
	fi

	# Extract image information
	if ! extract_image_info image_info; then
		output_result "${release}" image_info "failed"
		return 1
	fi

	log_info "Download URL: ${image_info[download_url]}"
	log_info "Filename: ${image_info[filename]}"
	log_debug "SHA256: ${image_info[sha256]}"

	# Verify OpenStack resource
	if ! verify_openstack_resource "image" "${image_info[filename]}"; then
		output_result "${release}" image_info "not_found"
		return 1
	fi

	output_result "${release}" image_info "success"
	log_success "Successfully processed release: ${release}"
	if [[ "${QUIET}" != "true" ]]; then
		echo ""
	fi
	return 0
}

#==============================================================================
# Main Execution
#==============================================================================

#------------------------------------------------------------------------------
# Function: main
# Description: Main entry point - orchestrates the entire script workflow
# Arguments:
#   $@ - All command-line arguments
# Returns:
#   0 - All releases processed successfully
#   1 - One or more releases failed
# Workflow:
#   1. Parse command-line arguments
#   2. Check required programs (curl, jq)
#   3. Validate environment variables (CLOUD)
#   4. Verify OpenStack connectivity (unless dry-run)
#   5. Process each release:
#      - Download CoreOS JSON
#      - Extract image metadata
#      - Verify image in OpenStack
#   6. Generate summary report with statistics
# Global Variables Used:
#   RELEASES - Array of releases to process
#   DRY_RUN - Dry-run mode flag
#   OUTPUT_FORMAT - Output format (text, json, csv)
#   SCRIPT_NAME - Name of this script
# Example: main "$@"
#------------------------------------------------------------------------------
function main() {
	local start_time
	local end_time
	local duration

	start_time=$(date +%s)

	# Parse command line arguments first (before any logging)
	parse_arguments "$@"

	check_openstack_cli

	log_info "Starting OpenShift RHCOS image verification script"
	log_info "Script: ${SCRIPT_NAME}"
	log_info "Working directory: $(pwd)"

	if [[ "${DRY_RUN}" == "true" ]]; then
		log_warning "Running in DRY RUN mode - no actual verification will be performed"
	fi

	# Initialize
	check_required_programs

	# Collect and validate inputs (skip OpenStack checks in dry-run mode)
	validate_environment_variables
	if [[ "${DRY_RUN}" != "true" ]]; then
		verify_openstack_connectivity
	else
		log_info "[DRY RUN] Skipping OpenStack connectivity check"
	fi

	# Output header for structured formats
	if [[ "${OUTPUT_FORMAT}" == "csv" ]]; then
		printf '%s,%s,%s,%s,%s\n' \
			"$(csv_escape "release")" \
			"$(csv_escape "status")" \
			"$(csv_escape "filename")" \
			"$(csv_escape "download_url")" \
			"$(csv_escape "sha256")"
	elif [[ "${OUTPUT_FORMAT}" == "json" ]]; then
		echo "["
	fi

	# Process each release
	log_info "Processing ${#RELEASES[@]} release(s): ${RELEASES[*]}"
	if [[ "${QUIET}" != "true" ]]; then
		echo ""
	fi

	local failed_releases=()
	local successful_releases=()
	local release_count=0

	for release in "${RELEASES[@]}"; do
		release_count=$((release_count + 1))

		if process_release "${release}"; then
			successful_releases+=("${release}")
		else
			failed_releases+=("${release}")
		fi

		# Add comma separator for JSON (except for last item)
		if [[ "${OUTPUT_FORMAT}" == "json" && ${release_count} -lt ${#RELEASES[@]} ]]; then
			echo ","
		fi
	done

	# Output footer for JSON
	if [[ "${OUTPUT_FORMAT}" == "json" ]]; then
		echo "]"
	fi

	# Calculate duration
	end_time=$(date +%s)
	duration=$((end_time - start_time))

	# Report results
	if [[ "${QUIET}" != "true" ]]; then
		echo ""
	fi
	log_info "=========================================="
	log_info "Summary:"
	log_info "  Total releases: ${#RELEASES[@]}"
	log_info "  Successful: ${#successful_releases[@]}"
	log_info "  Failed: ${#failed_releases[@]}"
	log_info "  Duration: ${duration} seconds"
	log_info "=========================================="

	if [[ ${#failed_releases[@]} -gt 0 ]]; then
		log_error "Failed to process ${#failed_releases[@]} release(s): ${failed_releases[*]}"
		exit 1
	fi

	log_success "All releases processed successfully!"
}

if [[ ! -v DEBUG ]]
then
	DEBUG=false
fi

FILE1=$(mktemp)
FILE2=$(mktemp)

trap "/bin/rm -rf ${FILE1} ${FILE2}" EXIT

# Run main function
main "$@"
