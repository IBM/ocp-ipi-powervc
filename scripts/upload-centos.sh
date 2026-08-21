#!/usr/bin/env bash

# Copyright 2025 IBM Corp
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
# Script: upload-centos.sh
# Description: Download and upload CentOS Stream images to PowerVC/OpenStack
#
# Purpose:
#   This script automates the process of downloading CentOS Stream GenericCloud
#   qcow2 images and uploading them to PowerVC/OpenStack environments.
#   It supports both CentOS Stream 9 and CentOS Stream 10 for ppc64le,
#   and provides comprehensive error handling.
#
# Features:
#   - CentOS Stream version selection (CentOS9 or CentOS10) via --centos
#   - Optional pin to a specific build date via --date
#   - Integration with pvsadm for image conversion (qcow2 to OVA)
#   - Integration with pvcctl or powervc-image for PowerVC image import
#   - Automatic detection of available PowerVC import tool
#   - Verbose mode for detailed debugging
#   - Dry-run mode for testing without actual operations
#   - Comprehensive error handling and reporting
#   - Interactive mode for missing environment variables
#
# Dependencies:
#   - curl: For downloading files and checking URLs
#   - openstack CLI: For verifying OpenStack connectivity and resources
#   - pvsadm: For converting qcow2 images to OVA format (optional when using pvcctl,
#             which can import directly from a URL; required when using powervc-image)
#   - pvcctl OR powervc-image: For importing images into PowerVC (either one required)
#
# Usage: See --help for detailed usage information
#==============================================================================

set -euo pipefail

#==============================================================================
# Global Variables
#==============================================================================
readonly SCRIPT_NAME="$(basename "${BASH_SOURCE[0]}")"
readonly SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Options
VERBOSE=false
DRY_RUN=false
QUIET=false
# Note: CENTOS_VERSION and CENTOS_DATE are intentionally left unset here so
# [[ ! -v CENTOS_VERSION ]] correctly detects when the user has not provided
# them via CLI or environment.
USE_PVSADM=false      # true if pvsadm is available
USE_PVCCTL=false      # true if pvcctl is available, false if using powervc-image

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
# Example: log_info "Fetching CentOS Stream image listing"
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
# Example: log_success "Image uploaded successfully"
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
# Example: log_warning "DRY RUN mode enabled, skipping upload"
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
#   COLOR_CYAN - Cyan color code for debug messages
#   COLOR_RESET - Reset color code
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
#   - curl: For downloading files and checking URLs
#   - openstack: For OpenStack CLI operations
#   - pvsadm: For converting qcow2 to OVA format
#   - pvcctl OR powervc-image: For importing images into PowerVC (either one required)
# Global Variables Modified:
#   USE_PVSADM - Set to true if pvsadm is found, false otherwise
#   USE_PVCCTL - Set to true if pvcctl is found, false if powervc-image is used
# Behavior:
#   Checks for core required programs first, then sets USE_PVSADM based on
#   pvsadm availability, then determines which PowerVC import tool is
#   available (pvcctl preferred over powervc-image)
# Example: check_required_programs
#------------------------------------------------------------------------------
function check_required_programs() {
	local -a required_programs=("curl" "openstack")
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

	if command_exists "pvsadm"; then
		USE_PVSADM=true
	else
		log_warning "pvsadm is missing"
	fi

	if command_exists "pvcctl"; then
		log_info "Found pvcctl over powervc-image"
		USE_PVCCTL=true
	elif command_exists "powervc-image"; then
		log_info "Did not find pvcctl, but found powervc-image instead"
		USE_PVCCTL=false
	else
		die "Missing required programs: either pvcctl or powervc-image must exist!"
	fi

	log_success "All required programs are available"
}

#------------------------------------------------------------------------------
# Function: is_var_set
# Description: Check if an environment variable is set and not empty
# Arguments:
#   $1 - Variable name to check
# Returns:
#   0 - Variable is set and non-empty
#   1 - Variable is unset or empty
# Example: if is_var_set "CLUSTER_NAME"; then ...; fi
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
# Example: verify_openstack_resource "image" "CentOS-Stream-GenericCloud-9-20260720.0.ppc64le"
#------------------------------------------------------------------------------
function verify_openstack_resource() {
	local resource_type="$1"
	local resource_name="$2"
	local cloud="${3:-${CLOUD}}"

	log_info "Verifying ${resource_type}: ${resource_name}"

	if ! openstack --os-cloud="${cloud}" "${resource_type}" show "${resource_name}" >/dev/null 2>&1; then
		log_info "${resource_type} '${resource_name}' not found in OpenStack"
		return 1
	fi

	log_success "Found ${resource_type}: ${resource_name}"
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
#   VERBOSE - Enable verbose/debug output
#   DRY_RUN - Enable dry-run mode
#   QUIET - Suppress log output (except errors)
#   CENTOS_VERSION - CentOS version preference (CentOS9, CentOS10, or empty)
#   CENTOS_DATE - Build date pin (YYYYMMDD, or empty for latest)
# Supported Options:
#   -v, --verbose - Enable verbose output
#   -q, --quiet - Suppress log output (except errors)
#   --dry-run - Simulate operations
#   --centos <version> - CentOS version preference (CentOS9 or CentOS10)
#   --date <YYYYMMDD> - Pin to a specific build date
#   -h, --help - Show usage information
# Example: parse_arguments "$@"
#------------------------------------------------------------------------------
function parse_arguments() {
	while [[ $# -gt 0 ]]; do
		case "$1" in
			--cloud)
				if [[ -z "${2:-}" ]]; then
					die "Error: --cloud requires a value"
				fi
				CLOUD="${2}"
				shift 2
				;;
			--dry-run)
				DRY_RUN=true
				shift
				;;
			-h|--help)
				show_usage
				exit 0
				;;
			--project)
				if [[ -z "${2:-}" ]]; then
					die "Error: --project requires a value"
				fi
				PROJECT="${2}"
				shift 2
				;;
			--project-upload)
				if [[ -z "${2:-}" ]]; then
					die "Error: --project-upload requires a value"
				fi
				PROJECT_UPLOAD="${2}"
				shift 2
				;;
			--centos)
				if [[ -z "${2:-}" ]]; then
					die "Error: --centos requires a value (CentOS9 or CentOS10)"
				fi
				case "$2" in
					CentOS9|CentOS10)
						CENTOS_VERSION="$2"
						;;
					*)
						die "Error: Invalid CentOS version '$2'. Must be CentOS9 or CentOS10"
						;;
				esac
				shift 2
				;;
			-q|--quiet)
				QUIET=true
				shift
				;;
			--date)
				if [[ -z "${2:-}" ]]; then
					die "Error: --date requires a value (YYYYMMDD)"
				fi
				if [[ ! "$2" =~ ^[0-9]{8}$ ]]; then
					die "Error: --date value '$2' is not a valid date (expected YYYYMMDD)"
				fi
				CENTOS_DATE="$2"
				shift 2
				;;
			--svc-host)
				if [[ -z "${2:-}" ]]; then
					die "Error: --svc-host requires a value"
				fi
				SVC_HOST="$2"
				shift 2
				;;
			--template)
				if [[ -z "${2:-}" ]]; then
					die "Error: --template requires a value"
				fi
				TEMPLATE="$2"
				shift 2
				;;
			-v|--verbose)
				VERBOSE=true
				shift
				;;
			*)
				die "Unknown option: $1. Use --help for usage information."
				;;
		esac
	done
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

Download CentOS Stream images and upload them to PowerVC/OpenStack.

This script automates the process of:
	 1. Fetching the latest CentOS Stream GenericCloud qcow2 image URL from
	    the official CentOS cloud mirror
	 2. Deriving the image name and expected OVA filename
	 3. Converting the qcow2 image to OVA format using pvsadm
	 4. Importing the OVA image into PowerVC using pvcctl or powervc-image

OPTIONS:
	   --cloud <name>            OpenStack cloud name from clouds.yaml
	                             Can also be set via CLOUD environment variable
	   --centos <version>        CentOS Stream version: CentOS9 or CentOS10
	                             Can also be set via CENTOS_VERSION environment variable
	   --date <YYYYMMDD>         Pin to a specific image build date (e.g. 20260721)
	                             If omitted, the latest available build is used
	   --project <name>          Optional project prefix (sets PROJECT variable)
	   --project-upload <name>   PowerVC project name for image upload access control
	                             Can also be set via PROJECT_UPLOAD environment variable
	   --svc-host <host>         PowerVC service host for image import
	                             Can also be set via SVC_HOST environment variable
	   --template <uuid>         PowerVC template UUID for image creation
	                             Can also be set via TEMPLATE environment variable
	   -q, --quiet               Suppress informational output (errors still shown)
	   -v, --verbose             Enable verbose output with debug information
	   --dry-run                 Simulate operations without making actual changes
	                             Skips OpenStack connectivity check and image upload
	   -h, --help                Show this help message and exit

ENVIRONMENT VARIABLES:
	   CLOUD                  OpenStack cloud name from clouds.yaml
	                         Can be set via --cloud option or interactively
	   CENTOS_VERSION         CentOS Stream version preference (CentOS9 or CentOS10)
	                         Can be set via --centos option or interactively
	   PROJECT_UPLOAD         PowerVC project name for image upload
	                         Can be set via --project-upload option or interactively
	   SVC_HOST               PowerVC service host
	                         Can be set via --svc-host option or interactively
	   TEMPLATE               PowerVC template UUID
	                         Can be set via --template option or interactively

REQUIRED TOOLS:
	   curl                   For downloading files and checking URLs
	   openstack              For verifying OpenStack connectivity (unless --dry-run)
	   pvsadm                 For converting qcow2 images to OVA format
	   pvcctl or powervc-image For importing images into PowerVC (either one required)

EXAMPLES:
	   # Interactive mode (prompts for missing variables)
	   ${SCRIPT_NAME}

	   # Specify all options on command line
	   ${SCRIPT_NAME} --cloud mycloud --project-upload myproject \\
	                  --centos CentOS9 --svc-host powervc.example.com --template <uuid>

	   # Dry run to test without actual operations
	   ${SCRIPT_NAME} --centos CentOS9 --dry-run

	   # Verbose output for debugging
	   ${SCRIPT_NAME} --centos CentOS9 --verbose

	   # Use environment variables
	   export CLOUD=mycloud
	   export PROJECT_UPLOAD=myproject
	   export CENTOS_VERSION=CentOS9          # or CentOS10
	   export SVC_HOST=powervc.example.com
	   export TEMPLATE=<uuid>
	   ${SCRIPT_NAME}

WORKFLOW:
	   1. Parse command-line arguments and collect missing variables interactively
	   2. Validate all required environment variables are set
	   3. Check for required programs (curl, openstack, pvsadm)
	   4. Detect available PowerVC import tool (pvcctl or powervc-image)
	   5. Fetch the latest dated CentOS Stream GenericCloud qcow2 image URL
	      and build date from the official mirror
	   6. Derive the bare image name from the URL
	   7. Check if the image already exists in OpenStack
	   8. If not exists:
	      - If pvsadm is present: convert qcow2 to OVA format first
	        (required for powervc-image; optional for pvcctl which can use a URL)
	      - Call pvcctl (with local OVA or URL) or powervc-image (with local OVA)

NOTES:
	   - The script will prompt for any missing required variables
	   - Use --dry-run to test the workflow without making changes

EOF
}

#------------------------------------------------------------------------------
# Function: prompt_input
# Description: Interactive prompt with validation and default values
# Arguments:
#   $1 - Prompt text to display
#   $2 - Variable name to store result
#   $3 - Default value (optional)
#   $4 - Allow empty input (default: false)
#   $5 - Hide input for secrets (default: false)
# Returns: None (exits via die if validation fails)
# Global Variables Modified:
#   Sets and exports the named variable with user input
# Example: prompt_input "Enter name" "CLUSTER_NAME" "default" false false
#------------------------------------------------------------------------------
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
			read -rp "${prompt_text}: " input_value
		fi
	fi

	if [[ -z "${input_value}" ]] && [[ "${allow_empty}" != "true" ]]; then
		die "You must enter a value for ${var_name}"
	fi

	printf -v "${var_name}" '%s' "${input_value}"
	export "${var_name}"
}

#------------------------------------------------------------------------------
# Function: collect_environment_variables
# Description: Gather all required configuration interactively
# Arguments: None
# Returns: None
# Global Variables Modified:
#   CLOUD - OpenStack cloud name from clouds.yaml
#   PROJECT_UPLOAD - PowerVC project for image upload
#   CENTOS_VERSION - CentOS version preference (CentOS9 or CentOS10)
#   SVC_HOST - PowerVC service host
#   TEMPLATE - PowerVC template UUID
# Behavior:
#   Prompts user for any missing environment variables
#   Handles both interactive and automated (pre-set variables) modes
#   Sets and exports all required variables
# Example: collect_environment_variables
#------------------------------------------------------------------------------
function collect_environment_variables() {
	log_info "Collecting environment variables..."

	# Cloud name from clouds.yaml
	if [[ ! -v CLOUD ]]; then
		prompt_input "What is the cloud name in ~/.config/openstack/clouds.yaml" "CLOUD"
	fi

	# PROJECT (to prepend) is optional!

	if [[ ! -v PROJECT_UPLOAD ]]; then
		prompt_input "What is the project when uploading?" "PROJECT_UPLOAD"
	fi

	if [[ ! -v CENTOS_VERSION ]]; then
		while true; do
			prompt_input "What is the CentOS version? (CentOS9 or CentOS10)" "CENTOS_VERSION"
			case "${CENTOS_VERSION}" in
				CentOS9|CentOS10) break ;;
				*) log_warning "Invalid CentOS version '${CENTOS_VERSION}'. Please enter CentOS9 or CentOS10." ;;
			esac
		done
	fi

	if [[ ! -v SVC_HOST ]]; then
		prompt_input "What is the service host?" "SVC_HOST"
	fi

	if [[ ! -v TEMPLATE ]]; then
		prompt_input "What is the template UUID?" "TEMPLATE"
	fi
}

#------------------------------------------------------------------------------
# Function: validate_environment_variables
# Description: Ensure all required environment variables are set and non-empty
# Arguments: None
# Returns:
#   0 - All required variables validated successfully
#   1 - One or more variables missing or empty (exits via die)
# Global Variables Validated:
#   CLOUD - OpenStack cloud name (required for OpenStack operations)
#   PROJECT_UPLOAD - PowerVC project name (required for image access control)
#   CENTOS_VERSION - CentOS version preference (required for image selection)
#   SVC_HOST - PowerVC service host (required for image import)
#   TEMPLATE - PowerVC template UUID (required for image creation)
# Note: Should be called after collect_environment_variables()
#       Uses validate_non_empty() which calls die() on failure
# Example: validate_environment_variables
#------------------------------------------------------------------------------
function validate_environment_variables() {
	log_info "Validating environment variables..."

	# List of all required environment variables
	# Each must be set and non-empty for the script to proceed
	local -a required_vars=(
		"CLOUD"          # OpenStack cloud configuration
		"PROJECT_UPLOAD" # PowerVC project for image access
		"CENTOS_VERSION" # CentOS version to use
		"SVC_HOST"       # PowerVC service host
		"TEMPLATE"       # PowerVC template UUID
	)

	# Validate each required variable
	for var in "${required_vars[@]}"; do
		validate_non_empty "${var}"
	done

	log_success "All environment variables validated"
}

#------------------------------------------------------------------------------
# Function: call_pvsadm
# Description: Execute pvsadm to convert qcow2 image to OVA format
# Arguments:
#   $1 - Image filename (without extension, e.g. CentOS-Stream-GenericCloud-9-20260720.0.ppc64le)
#   $2 - Download URL for the qcow2 image
# Returns:
#   0 - Command executed successfully or file already exists or dry-run mode
#   Non-zero - Command execution failed
# Global Variables:
#   SCRIPT_DIR - Directory containing the script (used as pvsadm working directory)
#   DRY_RUN - If true, skips actual execution and only logs the command
# External Dependencies:
#   pvsadm - Power Systems Virtual Server Admin tool
# Command Details:
#   - image qcow2ova: Converts qcow2 format to OVA format
#   - --image-dist coreos: Distribution flag accepted by pvsadm for RHEL-family images
#   - --image-name: Base name for the converted OVA
#   - --image-url: URL to download the source qcow2 image
#   - --image-size 16: Minimum disk size in GB (increase if CentOS image requires more)
# Behavior:
#   1. Checks if converted file already exists at ${SCRIPT_DIR}/${filename}.ova.gz
#   2. If file exists, logs info and returns success (skips re-conversion)
#   3. Logs the command that will be executed (visible with --verbose)
#   4. If DRY_RUN is true, logs warning and returns without execution
#   5. Otherwise, executes pvsadm in a subshell rooted at SCRIPT_DIR
# Example: call_pvsadm "CentOS-Stream-GenericCloud-9-20260720.0.ppc64le" "https://cloud.centos.org/.../file.qcow2"
#------------------------------------------------------------------------------
function call_pvsadm() {
	local filename="$1"
	local url="$2"
	local converted_filename="${SCRIPT_DIR}/${filename}.ova.gz"

	if [[ -f "${converted_filename}" ]]; then
		log_info "File already exists (${converted_filename})!"
		return 0
	fi

	# In dry-run mode show the command that would run; otherwise log it at debug level.
	if [[ "${DRY_RUN}" == "true" ]]; then
		log_info  "Would run: pvsadm image qcow2ova --image-dist coreos --image-name ${filename} --image-url ${url} --image-size 16"
		log_warning "DRY RUN mode - skipping pvsadm conversion"
		return 0
	fi
	log_debug "Running: pvsadm image qcow2ova --image-dist coreos --image-name ${filename} --image-url ${url} --image-size 16"

	# Execute in a subshell so pvsadm writes its OVA output to SCRIPT_DIR,
	# and a failure exits the subshell cleanly without needing manual popd.
	( cd "${SCRIPT_DIR}" && pvsadm image qcow2ova \
		--image-dist coreos \
		--image-name "${filename}" \
		--image-url "${url}" \
		--image-size 16 )
}

#------------------------------------------------------------------------------
# Function: call_pvcctl
# Description: Execute pvcctl to import an image into PowerVC.
#              Accepts either a local OVA file path or a remote URL as the
#              image source — pvcctl handles both natively.
#              When pvsadm has already converted the image, the local
#              .ova.gz path is preferred; otherwise the original qcow2 URL
#              is passed directly.
# Arguments:
#   $1 - URL  - Remote URL of the qcow2 image (used when no local OVA exists)
#   $2 - filename - Image name without extension
#              (also used to derive the local OVA path when pvsadm ran)
# Returns:
#   0 - Command executed successfully or dry-run mode
#   Non-zero - Command execution failed
# Global Variables:
#   PROJECT_UPLOAD - PowerVC project name for image access control
#   SVC_HOST - PowerVC service host for image import
#   TEMPLATE - PowerVC template UUID for image creation
#   SCRIPT_DIR - Directory containing the script (used to locate OVA file)
#   DRY_RUN - If true, skips actual execution and only displays command
#   USE_PVSADM - If true, a local .ova.gz is expected from a prior pvsadm run
# External Dependencies:
#    pvcctl - PowerVC control tool
# Command Details:
#   - image import-linux: Import Linux image into PowerVC
#   - --image: Local .ova.gz path (when pvsadm ran) or remote URL
#   - --name: Name for the imported image in PowerVC
#   - --os-type "coreos": Operating system type
#   - --volume-size "120": Volume size in GB (120GB for OpenShift)
#   - --projects: PowerVC project for access control
#   - --svc-host: PowerVC service host
#   - --template: PowerVC template UUID
#   - --config: Configuration file (default-config.yaml)
#   - --log-file: Log file for import operation (pwr1.log)
# Example: call_pvcctl "https://cloud.centos.org/.../file.qcow2" "CentOS-Stream-GenericCloud-9-20260720.0.ppc64le"
#------------------------------------------------------------------------------
function call_pvcctl() {
	local url="$1"
	local filename="$2"
	local converted_filename="${SCRIPT_DIR}/${filename}.ova.gz"

	# Prefer the locally converted OVA when pvsadm has already produced it;
	# fall back to the remote URL so pvcctl can fetch and convert it directly.
	local image_source
	if [[ "${USE_PVSADM}" == "true" ]]; then
		if [[ "${DRY_RUN}" != "true" ]] && [[ ! -f "${converted_filename}" ]]; then
			log_error "File is missing: (${converted_filename})!"
			return 1
		fi
		image_source="${converted_filename}"
	else
		image_source="${url}"
	fi

	# In dry-run mode show the command that would run; otherwise log it at debug level.
	if [[ "${DRY_RUN}" == "true" ]]; then
		log_info  "Would run: pvcctl image import-linux --image ${image_source} --name ${filename} --os-type coreos --volume-size 120 --projects ${PROJECT_UPLOAD} --svc-host ${SVC_HOST} --template ${TEMPLATE}"
		log_warning "DRY RUN mode - skipping pvcctl import"
		return 0
	fi
	log_debug "Running: pvcctl image import-linux --image ${image_source} --name ${filename} --os-type coreos --volume-size 120 --projects ${PROJECT_UPLOAD} --svc-host ${SVC_HOST} --template ${TEMPLATE}"

	# Execute the pvcctl image import-linux command
	pvcctl \
		image import-linux \
		--image "${image_source}" \
		--name "${filename}" \
		--os-type "coreos" \
		--volume-size "120" \
		--projects "${PROJECT_UPLOAD}" \
		--svc-host "${SVC_HOST}" \
		--template "${TEMPLATE}" \
		--config default-config.yaml \
		--log-file pwr1.log
}

#------------------------------------------------------------------------------
# Function: call_powervc_image
# Description: Import OVA image into PowerVC using powervc-image CLI tool
# Arguments:
#   $1 - Image filename (without extension)
# Returns:
#   0 - Command executed successfully or dry-run mode
#   1 - OVA file not found or command execution failed
# Global Variables:
#   PROJECT_UPLOAD - PowerVC project name for image access control
#   TEMPLATE - PowerVC template UUID for image creation
#   SCRIPT_DIR - Directory containing the script (used to locate OVA file)
#   DRY_RUN - If true, skips actual execution and only displays command
# External Dependencies:
#   powervc-image - PowerVC image management CLI tool
# Command Details:
#   - import: Import an image into PowerVC
#   - --project: PowerVC project for access control
#   - -n: Name for the imported image in PowerVC
#   - -p: Path to the OVA image file (expects .ova.gz extension)
#   - -t: PowerVC template UUID to use for image creation
#   - -m: Metadata key-value pairs (os-type and architecture)
# Behavior:
#   1. Checks if OVA file exists at ${SCRIPT_DIR}/${filename}.ova.gz
#   2. If file doesn't exist, logs error and returns 1
#   3. Displays the command that will be executed (for logging/verification)
#   4. If DRY_RUN is true, logs warning and returns without execution
#   5. Otherwise, executes the powervc-image import command
# File Expectations:
#   - OVA file must exist at: ${SCRIPT_DIR}/${filename}.ova.gz
#   - File should be created by pvsadm prior to calling this function
# Example: call_powervc_image "CentOS-Stream-GenericCloud-9-20260720.0.ppc64le"
#------------------------------------------------------------------------------
function call_powervc_image() {
	local filename="$1"
	local converted_filename="${SCRIPT_DIR}/${filename}.ova.gz"

	# In dry-run mode show the command that would run; otherwise log it at debug level.
	if [[ "${DRY_RUN}" == "true" ]]; then
		log_info  "Would run: powervc-image --project ${PROJECT_UPLOAD} import -n ${filename} -p ${converted_filename} -t ${TEMPLATE} -m os-type=rhel architecture=ppc64le"
		log_warning "DRY RUN mode - skipping powervc-image import"
		return 0
	fi
	if [[ ! -f "${converted_filename}" ]]; then
		log_error "File is missing: (${converted_filename})!"
		return 1
	fi
	log_debug "Running: powervc-image --project ${PROJECT_UPLOAD} import -n ${filename} -p ${converted_filename} -t ${TEMPLATE} -m os-type=rhel architecture=ppc64le"

	# Execute the powervc-image import command
	powervc-image \
		--project "${PROJECT_UPLOAD}" \
		import \
		-n "${filename}" \
		-p "${converted_filename}" \
		-t "${TEMPLATE}" \
		-m os-type=rhel architecture=ppc64le
}

#------------------------------------------------------------------------------
# Function: find_latest_centos_qcow2_url
# Description: Find the URL and build date of the latest
#              CentOS-Stream-GenericCloud .qcow2 image for ppc64le.
#
#              Supports both CentOS Stream 9 and CentOS Stream 10.
#              Only date-stamped files are considered (e.g. ...-20260721.0...);
#              the "-latest" symlink is intentionally excluded so the returned
#              filename and date are always concrete and reproducible.
#
# Arguments:
#   $1 - CentOS version: "CentOS9" or "CentOS10" (required)
#   $2 - Name of variable to receive the full URL (required)
#   $3 - (optional) Name of variable to receive the build date (YYYYMMDD)
#   $4 - (optional) Exact build date to pin (YYYYMMDD); empty means "latest"
# Returns:
#   0 - Caller-named variables populated
#   1 - Invalid version, or could not retrieve/parse the listing
#       (exits via die)
# Global Variables:
#   None
# Note:
#   All results are returned via named output variables (printf -v) so the
#   function must NOT be called inside $() — doing so runs it in a subshell
#   where printf -v writes are invisible to the caller.
#   When $4 is empty, "latest" is determined by lexicographic sort of the
#   dated filenames. The YYYYMMDD.N scheme sorts correctly without any date
#   arithmetic. When $4 is provided, only filenames containing that date are
#   considered; the script dies if no matching file is found.
# Example:
#   local url build_date
#   find_latest_centos_qcow2_url CentOS9  url build_date ""
#   find_latest_centos_qcow2_url CentOS10 url build_date "20260721"
#   echo "URL:  ${url}"
#   echo "Date: ${build_date}"
#------------------------------------------------------------------------------
function find_latest_centos_qcow2_url() {
	local centos_version="${1:-}"
	local _out_url="${2:-}"
	local _out_date="${3:-}"
	local _pin_date="${4:-}"
	local stream_num
	local base_url
	local listing
	local latest_filename
	local _build_date

	# Resolve the stream number and base URL from the version parameter
	case "${centos_version}" in
		CentOS9)
			stream_num="9"
			;;
		CentOS10)
			stream_num="10"
			;;
		*)
			die "find_latest_centos_qcow2_url: invalid version '${centos_version}'. Must be CentOS9 or CentOS10"
			;;
	esac

	base_url="https://cloud.centos.org/centos/${stream_num}-stream/ppc64le/images/"

	log_debug "Fetching image listing from: ${base_url}"

	# Download the HTML directory listing; fail fast on network errors
	if ! listing=$(curl --silent --location --fail \
			--max-time 30 --connect-timeout 10 \
			"${base_url}"); then
		die "Failed to retrieve image listing from ${base_url}"
	fi

	# Extract only date-stamped filenames (YYYYMMDD.N scheme); exclude -latest
	# symlinks so the result is always a concrete, reproducible build.
	# When a pin date is provided, restrict the pattern to that exact date.
	local date_pattern
	if [[ -n "${_pin_date}" ]]; then
		date_pattern="${_pin_date}"
		log_debug "Pinning to build date: ${_pin_date}"
	else
		date_pattern="\\d{8}"
	fi

	latest_filename=$(grep -oP "href=\"\\KCentOS-Stream-GenericCloud-${stream_num}-${date_pattern}\\.\\d+\\.ppc64le\\.qcow2(?=\")" \
		<<< "${listing}" \
		| sort \
		| tail -n 1)

	if [[ -z "${latest_filename}" ]]; then
		if [[ -n "${_pin_date}" ]]; then
			die "No CentOS-Stream-GenericCloud-${stream_num} .qcow2 file found for date ${_pin_date} at ${base_url}"
		else
			die "No dated CentOS-Stream-GenericCloud-${stream_num} .qcow2 files found at ${base_url}"
		fi
	fi

	log_debug "Latest CentOS ${stream_num} Stream qcow2 filename: ${latest_filename}"

	# Extract the YYYYMMDD date embedded in the filename
	# Pattern: CentOS-Stream-GenericCloud-N-YYYYMMDD.N.ppc64le.qcow2
	_build_date=$(grep -oP "(?<=CentOS-Stream-GenericCloud-${stream_num}-)\\d{8}" \
		<<< "${latest_filename}")

	if [[ -z "${_build_date}" ]]; then
		die "Could not extract build date from filename: ${latest_filename}"
	fi

	log_debug "Build date: ${_build_date}"

	# Populate all caller-supplied output variables
	printf -v "${_out_url}"  '%s' "${base_url}${latest_filename}"
	if [[ -n "${_out_date}" ]]; then
		printf -v "${_out_date}" '%s' "${_build_date}"
	fi
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
#   0 - Image processed successfully (uploaded or already present)
#   1 - A step failed (pvsadm conversion or PowerVC import)
# Workflow:
#   1. Parse command-line arguments
#   2. Collect missing environment variables interactively
#   3. Validate all required environment variables
#   4. Check required programs (curl, openstack, pvsadm)
#   5. Detect available PowerVC import tool (pvcctl or powervc-image) and set USE_PVCCTL
#   6. Fetch the latest CentOS Stream qcow2 URL and build date
#   7. Derive the image name
#   8. Check if the image already exists in OpenStack
#   9. If not exists: convert with pvsadm, then import with pvcctl or powervc-image
# Global Variables Used:
#   DRY_RUN - Dry-run mode flag (skips actual operations)
#   VERBOSE - Verbose mode flag (enables debug logging)
#   CENTOS_VERSION - CentOS version preference (CentOS9, CentOS10, or empty)
#   CLOUD - OpenStack cloud name
#   PROJECT - Optional project prefix for image names
#   PROJECT_UPLOAD - PowerVC project for image upload
#   SVC_HOST - PowerVC service host
#   TEMPLATE - PowerVC template UUID
#   USE_PVCCTL - Flag indicating which PowerVC tool to use
#   SCRIPT_NAME - Name of this script
# Example: main "$@"
#------------------------------------------------------------------------------
function main() {
	local url
	local build_date
	local imagename

	# Step 1: Parse CLI args, prompt for any missing env vars, then validate all
	parse_arguments "$@"
	collect_environment_variables
	validate_environment_variables

	log_debug "Parsed arguments: VERBOSE=${VERBOSE}, DRY_RUN=${DRY_RUN}, CENTOS_VERSION=${CENTOS_VERSION:-}, PROJECT_UPLOAD=${PROJECT_UPLOAD:-}, SVC_HOST=${SVC_HOST:-}, TEMPLATE=${TEMPLATE:-}"

	# Step 2: Verify required tools are installed
	check_required_programs

	log_info "Starting CentOS Stream image upload script"
	log_info "Script: ${SCRIPT_NAME}"
	log_info "Working directory: $(pwd)"

	if [[ "${DRY_RUN}" == "true" ]]; then
		log_warning "Running in DRY RUN mode - no actual upload will be performed"
	fi

	# Step 3: Resolve the latest dated .qcow2 image URL and build date
	# from the official CentOS cloud mirror for the requested stream version.
	# Results are written into local variables via printf -v (no subshell).
	find_latest_centos_qcow2_url "${CENTOS_VERSION}" url build_date "${CENTOS_DATE:-}"
	log_debug "url=${url}"
	log_debug "build_date=${build_date}"

	# Step 4: Derive the image name used in OpenStack/PowerVC from the URL.
	# e.g. https://.../CentOS-Stream-GenericCloud-9-20260720.0.ppc64le.qcow2
	#   -> imagename = CentOS-Stream-GenericCloud-9-20260720.0.ppc64le
	imagename="$(basename "${url}")"
	imagename="${imagename%.qcow2}"
	log_debug "imagename=${imagename}"

	# Step 5: Check whether the image already exists in OpenStack/PowerVC.
	# If it does not exist, it needs to be converted and uploaded.
	if verify_openstack_resource "image" "${imagename}"; then
		log_success "Image '${imagename}' already present — skipping upload"
		return 0
	fi

	# Convert qcow2 to OVA when pvsadm is available.
	# - pvcctl can import directly from a URL, so pvsadm is optional for that path.
	# - powervc-image requires a local file, so pvsadm is mandatory for that path.
	if [[ "${USE_PVSADM}" == "true" ]]; then
		if ! call_pvsadm "${imagename}" "${url}"; then
			log_error "pvsadm failed!"
			return 1
		fi
	elif [[ "${USE_PVCCTL}" != "true" ]]; then
		die "pvsadm is required to convert the qcow2 image when using powervc-image"
	fi

	if [[ "${USE_PVCCTL}" == "true" ]]; then
		if ! call_pvcctl "${url}" "${imagename}"; then
			log_error "pvcctl failed!"
			return 1
		fi
	else
		if ! call_powervc_image "${imagename}"; then
			log_error "powervc-image failed!"
			return 1
		fi
	fi
}

#==============================================================================
# Script Entry Point
#==============================================================================

# Execute main function with all command-line arguments
# This is the entry point for the script's primary logic
main "$@"
