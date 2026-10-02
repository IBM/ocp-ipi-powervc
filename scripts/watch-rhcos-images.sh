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
# Script: watch-rhcos-images.sh
# Description: Watch for new RHCOS images across all OCP ppc64le CI releases
#              and upload them to PowerVC/OpenStack every 24 hours.
#
# Purpose:
#   Continuously monitors the ppc64le OCP CI release stream for all known
#   minor versions (>= 4.21). For each version and each RHEL variant (rhel9,
#   rhel10), it checks whether the corresponding RHCOS image already exists in
#   OpenStack and uploads it if not. Runs indefinitely, re-checking every 24
#   hours and refreshing the version list each cycle.
#
# Features:
#   - Automatic discovery of all OCP minor versions with ppc64le CI builds
#   - Processes both RHEL 9 and RHEL 10 RHCOS variants per release
#   - Integration with pvsadm for image conversion (qcow2 to OVA)
#   - Integration with pvcctl or powervc-image for PowerVC image import
#   - Automatic detection of available PowerVC import tool
#   - Verbose mode for detailed debugging
#   - Dry-run mode for testing without actual operations
#   - Comprehensive error handling and reporting
#   - Interactive mode for missing environment variables
#
# Dependencies:
#   - curl: For downloading CoreOS JSON metadata and checking URLs
#   - jq: For parsing the CI release stream API and CoreOS JSON
#   - openstack CLI: For checking whether images already exist in OpenStack
#   - pvsadm: For converting qcow2 images to OVA format (optional)
#   - pvcctl OR powervc-image: For importing images into PowerVC (one required)
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
USE_PVSADM=false      # true if pvsadm is available
USE_PVCCTL=true       # pvcctl is required; always true

# Directory containing secret files (SVC_HOST, SVC_USER, SVC_PASSWORD,
# POWERVC_USER_ID, POWERVC_PASSWORD, clouds.yaml).
readonly SECRETS_DIR="/var/run/powervc-ipi-cicd-secrets/powervc-creds"

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

function log_info() {
	if [[ "${QUIET}" != "true" ]]; then
		echo -e "${COLOR_BLUE}[INFO]${COLOR_RESET} $*"
	fi
}

function log_success() {
	if [[ "${QUIET}" != "true" ]]; then
		echo -e "${COLOR_GREEN}[SUCCESS]${COLOR_RESET} $*"
	fi
}

function log_warning() {
	if [[ "${QUIET}" != "true" ]]; then
		echo -e "${COLOR_YELLOW}[WARNING]${COLOR_RESET} $*"
	fi
}

function log_error() {
	echo -e "${COLOR_RED}[ERROR]${COLOR_RESET} $*" >&2
}

function log_debug() {
	if [[ "${VERBOSE}" == "true" ]]; then
		echo -e "${COLOR_CYAN}[DEBUG]${COLOR_RESET} $*"
	fi
}

function die() {
	local save_quiet=${QUIET}
	QUIET=false
	log_error "$*"
	QUIET=${save_quiet}
	exit 1
}

function command_exists() {
	command -v "$1" >/dev/null 2>&1
}

function is_var_set() {
	local var_name="$1"
	local var_value="${!var_name:-}"

	[[ -n "${var_value}" ]]
}

function validate_non_empty() {
	local var_name="$1"

	if ! is_var_set "${var_name}"; then
		die "${var_name} must be set and non-empty"
	fi
}

#------------------------------------------------------------------------------
# Function: check_required_programs
# Description: Verify all required programs are installed and available
# Arguments: None
# Returns:
#   0 - All required programs found
#   1 - One or more programs missing (exits via die)
#------------------------------------------------------------------------------
function check_required_programs() {
	local -a required_programs=("curl" "jq" "openstack" "pvcctl" "yq-v4")
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
		log_warning "pvsadm is missing — URL-based pvcctl import will be used if available"
	fi

	if command_exists "powervc-image"; then
		log_info "Found powervc-image alongside pvcctl"
	fi
	USE_PVCCTL=true

	log_success "All required programs are available"
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
# Description: Gather all required configuration interactively.
#              PROJECT is optional — read from environment only, never prompted.
# Arguments: None
# Returns: None
# Global Variables Modified:
#   CLOUD          - OpenStack cloud name from clouds.yaml
#   PROJECT        - Optional image filename prefix (env only)
#   PROJECT_UPLOAD - PowerVC project for image upload
#   SVC_HOST       - PowerVC service host
#   TEMPLATE       - PowerVC template UUID
#------------------------------------------------------------------------------
function collect_environment_variables() {
	log_info "Collecting environment variables..."

	if [[ ! -v CLOUD ]] || [[ -z "${CLOUD:-}" ]]; then
		prompt_input "What is the cloud name in ~/.config/openstack/clouds.yaml" "CLOUD"
	fi

	# PROJECT is optional — read from environment only, never prompt
	PROJECT="${PROJECT:-}"

	if [[ ! -v PROJECT_UPLOAD ]] || [[ -z "${PROJECT_UPLOAD:-}" ]]; then
		prompt_input "What is the project when uploading?" "PROJECT_UPLOAD"
	fi

	if [[ ! -v SVC_HOST ]] || [[ -z "${SVC_HOST:-}" ]]; then
		prompt_input "What is the PowerVC service host?" "SVC_HOST"
	fi

	if [[ ! -v TEMPLATE ]] || [[ -z "${TEMPLATE:-}" ]]; then
		prompt_input "What is the PowerVC template UUID?" "TEMPLATE"
	fi
}

#------------------------------------------------------------------------------
# Function: validate_environment_variables
# Description: Ensure all required environment variables are set and non-empty
# Arguments: None
# Returns:
#   0 - All required variables validated successfully
#   1 - One or more variables missing or empty (exits via die)
# Required: CLOUD, PROJECT_UPLOAD, SVC_HOST, TEMPLATE
# Optional: PROJECT (not validated)
#------------------------------------------------------------------------------
function validate_environment_variables() {
	log_info "Validating environment variables..."

	local -a required_vars=(
		"CLOUD"
		"PROJECT_UPLOAD"
		"SVC_HOST"
		"TEMPLATE"
	)

	for var in "${required_vars[@]}"; do
		validate_non_empty "${var}"
	done

	log_success "All environment variables validated"
}

#------------------------------------------------------------------------------
# Function: parse_arguments
# Description: Parse and validate command-line arguments
# Arguments:
#   $@ - All command-line arguments passed to script
# Supported Options:
#   --cloud <name>            OpenStack cloud name
#   --project <name>          Optional image filename prefix
#   --project-upload <name>   PowerVC project for image upload
#   --svc-host <host>         PowerVC service host
#   --template <uuid>         PowerVC template UUID
#   -v, --verbose             Enable verbose output
#   -q, --quiet               Suppress log output (except errors)
#   --dry-run                 Simulate operations
#   -h, --help                Show usage information
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
			-q|--quiet)
				QUIET=true
				shift
				;;
			--svc-host)
				if [[ -z "${2:-}" ]]; then
					die "Error: --svc-host requires a value"
				fi
				SVC_HOST="${2}"
				shift 2
				;;
			--template)
				if [[ -z "${2:-}" ]]; then
					die "Error: --template requires a value"
				fi
				TEMPLATE="${2}"
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
#------------------------------------------------------------------------------
function show_usage() {
	cat <<EOF
Usage: ${SCRIPT_NAME} [OPTIONS]

Watch for new RHCOS images across all OCP releases with ppc64le CI builds
and upload them to PowerVC/OpenStack. Checks every 24 hours.

OPTIONS:
	   --cloud <name>            OpenStack cloud name from clouds.yaml
	                             Can also be set via CLOUD environment variable
	   --project <name>          Optional prefix prepended to image filenames
	                             Can also be set via PROJECT environment variable
	   --project-upload <name>   PowerVC project name for image upload
	                             Can also be set via PROJECT_UPLOAD environment variable
	   --svc-host <host>         PowerVC service host
	                             Can also be set via SVC_HOST environment variable
	   --template <uuid>         PowerVC template UUID
	                             Can also be set via TEMPLATE environment variable
	   -q, --quiet               Suppress informational output (errors still shown)
	   -v, --verbose             Enable verbose output with debug information
	   --dry-run                 Simulate operations; exits after first iteration
	   -h, --help                Show this help message and exit

ENVIRONMENT VARIABLES:
	   CLOUD            OpenStack cloud name from clouds.yaml
	   PROJECT          Optional prefix prepended to image filenames (never prompted)
	   PROJECT_UPLOAD   PowerVC project name for image upload
	   SVC_HOST         PowerVC service host
	   TEMPLATE         PowerVC template UUID

EXAMPLES:
	   # Interactive mode (prompts for missing variables)
	   ${SCRIPT_NAME}

	   # Specify all options on command line
	   ${SCRIPT_NAME} --cloud mycloud --project-upload myproject \\
	                  --svc-host powervc.example.com --template <uuid>

	   # Dry run to test without actual operations
	   ${SCRIPT_NAME} --dry-run --cloud mycloud --project-upload myproject \\
	                  --svc-host powervc.example.com --template <uuid>

EOF
}

#==============================================================================
# Core Functions
#==============================================================================

#------------------------------------------------------------------------------
# Function: read_secret
# Description: Read a required secret file from SECRETS_DIR, failing with a
#              clear message if it is missing.
# Arguments:
#   $1 - Name of the secret file within SECRETS_DIR
# Returns:
#   0 - File contents written to stdout
#   1 - File not found (exits via die)
# Global Variables:
#   SECRETS_DIR - Directory containing secret files
#------------------------------------------------------------------------------
function read_secret() {
	local name="$1"
	local path="${SECRETS_DIR}/${name}"

	if [[ ! -f "${path}" ]]; then
		die "Required secret file is missing: ${path}"
	fi

	cat "${path}"
}

#------------------------------------------------------------------------------
# Function: create_default_config
# Description: Generate default-config.yaml for pvcctl in the current directory
#              by running `pvcctl image import --gen-config` to produce a
#              template and then populating it with credentials from SECRETS_DIR.
#
#              Mirrors setup_config_yaml from openshift-powervc-podman-commands.sh.
#
# Arguments: None
# Returns:
#   0 - default-config.yaml created and populated successfully
#   1 - Any step failed (exits via die)
# Global Variables:
#   SECRETS_DIR - Directory containing secret files
#   CLOUD       - OpenStack cloud name (used to extract the PowerVC auth URL)
#------------------------------------------------------------------------------
function create_default_config() {
	log_info "Creating default-config.yaml..."

	if [[ "${DRY_RUN}" == "true" ]]; then
		log_warning "DRY RUN mode - skipping default-config.yaml generation"
		return 0
	fi

	# Generate the config.yaml template into the current directory.
	pvcctl image import --gen-config

	if [[ ! -f config.yaml ]]; then
		die "config.yaml missing after pvcctl --gen-config"
	fi

	# Read each secret in a standalone assignment so errexit sees failures.
	# A combined `local var=$(...)` would mask the exit status.
	local svc_host svc_user svc_password
	svc_host="$(read_secret SVC_HOST)"
	svc_user="$(read_secret SVC_USER)"
	svc_password="$(read_secret SVC_PASSWORD)"

	# Populate SVC credentials via strenv() so special characters are safe and
	# secret values are never passed as command-line arguments.
	SVC_HOST="${svc_host}"         yq-v4 --inplace '.svc.host     = strenv(SVC_HOST)'     config.yaml
	SVC_USER="${svc_user}"         yq-v4 --inplace '.svc.user     = strenv(SVC_USER)'     config.yaml
	SVC_PASSWORD="${svc_password}" yq-v4 --inplace '.svc.password = strenv(SVC_PASSWORD)' config.yaml

	local powervc_user powervc_password
	powervc_user="$(read_secret POWERVC_USER_ID)"
	powervc_password="$(read_secret POWERVC_PASSWORD)"

	POWERVC_USER="${powervc_user}"         yq-v4 --inplace '.powervc.user     = strenv(POWERVC_USER)'     config.yaml
	POWERVC_PASSWORD="${powervc_password}" yq-v4 --inplace '.powervc.password = strenv(POWERVC_PASSWORD)' config.yaml

	# Pull the PowerVC auth URL from the mounted clouds.yaml.
	local powervc_url
	powervc_url=$(CLOUD="${CLOUD}" yq-v4 eval '.clouds[strenv(CLOUD)].auth.auth_url' "${SECRETS_DIR}/clouds.yaml")
	if [[ -z "${powervc_url}" || "${powervc_url}" == "null" ]]; then
		die "Could not extract auth_url for cloud '${CLOUD}' from ${SECRETS_DIR}/clouds.yaml"
	fi
	# Append /v3 so the auth/tokens endpoint is reachable.
	powervc_url="${powervc_url}/v3"
	POWERVC_URL="${powervc_url}" yq-v4 --inplace '.powervc.url = strenv(POWERVC_URL)' config.yaml

	POWERVC_TENANT="ocp-ci"   yq-v4 --inplace '.powervc.tenant   = strenv(POWERVC_TENANT)'   config.yaml
	POWERVC_PROJECTS="ocp-ci" yq-v4 --inplace '.powervc.projects = strenv(POWERVC_PROJECTS)' config.yaml

	# pvcctl uses default-config.yaml (not config.yaml) by default.
	cp config.yaml default-config.yaml

	log_success "default-config.yaml created successfully"
}

#------------------------------------------------------------------------------
# Function: verify_openstack_resource
# Description: Verify that a specific OpenStack resource exists
# Arguments:
#   $1 - Resource type (e.g., "image")
#   $2 - Resource name to verify
# Returns:
#   0 - Resource found
#   1 - Resource not found
# Global Variables:
#   CLOUD - OpenStack cloud name
#------------------------------------------------------------------------------
function verify_openstack_resource() {
	local resource_type="$1"
	local resource_name="$2"

	log_info "Verifying ${resource_type}: ${resource_name}"

	if ! openstack --os-cloud="${CLOUD}" "${resource_type}" show "${resource_name}" >/dev/null 2>&1; then
		log_info "${resource_type} '${resource_name}' not found in OpenStack"
		return 1
	fi

	log_success "Found ${resource_type}: ${resource_name}"
}

#------------------------------------------------------------------------------
# Function: list_openshift_versions
# Description: Fetch all OCP minor versions (>= 4.21) that have ppc64le
#              releases in the CI release stream, returned as a sorted bash
#              array in the caller-supplied variable name.
#
#              Queries https://ppc64le.ocp.releases.ci.openshift.org and uses
#              jq to flatten all stream tag names, extract major.minor prefixes,
#              deduplicate, filter to >= 4.21, and sort numerically.
#
# Arguments:
#   $1 - Name of the caller's array variable to populate (required)
# Returns:
#   0 - Variable populated with at least one version
#   1 - curl or jq failed, or no matching versions found (exits via die)
# Global Variables: None
# Dependencies: curl, jq
# Note: Must NOT be called inside $() — results are written via nameref so
#       a subshell would make them invisible to the caller.
# Example:
#   local -a versions
#   list_openshift_versions versions
#   echo "Latest: ${versions[-1]}"
#------------------------------------------------------------------------------
function list_openshift_versions() {
	local -n _out_versions="$1"
	local api_url="https://ppc64le.ocp.releases.ci.openshift.org/api/v1/releasestreams/all"
	local raw

	log_info "Fetching OCP release versions from ${api_url}..."

	if ! raw=$(curl --silent --fail --location \
			--max-time 30 --connect-timeout 10 \
			"${api_url}"); then
		log_error "Failed to retrieve release stream data from ${api_url}"
		return 1
	fi

	local versions_json
	versions_json=$(jq -r '
		[.[] | .[]?]
		| map(capture("^(?<v>[0-9]+\\.[0-9]+)") | .v)
		| unique
		| map(select(
			split(".") | map(tonumber)
			| .[0] > 4 or (.[0] == 4 and .[1] >= 21)
		  ))
		| sort_by(split(".") | map(tonumber))
		| .[]
	' <<< "${raw}")

	if [[ -z "${versions_json}" ]]; then
		log_error "No OCP versions >= 4.21 found in release stream data"
		return 1
	fi

	mapfile -t _out_versions <<< "${versions_json}"

	log_success "Found ${#_out_versions[@]} versions (${_out_versions[0]} – ${_out_versions[-1]})"
}

#------------------------------------------------------------------------------
# Function: download_coreos_json
# Description: Download the CoreOS JSON for a specific release and RHEL version.
# Arguments:
#   $1 - Release version (e.g., "release-4.21")
#   $2 - RHEL version: "rhel9" or "rhel10"
#   $3 - Path to the file where the downloaded JSON will be written
# Returns:
#   0 - Successfully downloaded to the given path
#   1 - Failed to download from all URLs (logs error)
#------------------------------------------------------------------------------
function download_coreos_json() {
	local release="$1"
	local rhel_version="$2"
	local out_file="$3"
	local -a urls=()
	local max_retries=3
	local retry_delay=2

	if [[ "${rhel_version}" == "rhel9" ]]; then
		urls=(
			"https://raw.githubusercontent.com/openshift/installer/refs/heads/${release}/data/data/coreos/coreos-rhel-9.json"
			"https://raw.githubusercontent.com/openshift/installer/refs/heads/${release}/data/data/coreos/rhcos.json"
		)
		log_debug "Using RHEL 9 CoreOS JSON URLs"
	elif [[ "${rhel_version}" == "rhel10" ]]; then
		urls=(
			"https://raw.githubusercontent.com/openshift/installer/refs/heads/${release}/data/data/coreos/coreos-rhel-10.json"
		)
		log_debug "Using RHEL 10 CoreOS JSON URL"
	else
		die "download_coreos_json: invalid rhel_version '${rhel_version}'. Must be rhel9 or rhel10"
	fi

	for url in "${urls[@]}"; do
		local attempt=1
		while [[ ${attempt} -le ${max_retries} ]]; do
			log_debug "Download attempt ${attempt}/${max_retries}: ${url}"
			# Truncate before each attempt so a previous partial write never
			# leaves stale bytes in the file.
			: > "${out_file}"
			if curl --silent --location --fail --max-time 30 --connect-timeout 10 \
					--output "${out_file}" "${url}"; then
				log_info "Downloaded ${url}"
				return 0
			fi
			log_debug "Attempt ${attempt}/${max_retries} failed for ${url}"
			attempt=$(( attempt + 1 ))
			if [[ ${attempt} -le ${max_retries} ]]; then
				log_debug "Retrying in ${retry_delay}s..."
				sleep "${retry_delay}"
			fi
		done
		log_debug "URL not available after ${max_retries} attempts: ${url}"
	done

	log_error "Could not download CoreOS JSON from any known location for release ${release}"
	return 1
}

#------------------------------------------------------------------------------
# Function: extract_image_info
# Description: Extract ppc64le OpenStack image metadata from a CoreOS JSON file.
# Arguments:
#   $1 - Name of the associative array to populate (nameref)
#   $2 - Path to the CoreOS JSON file to parse
# Returns:
#   0 - Successfully extracted download_url, filename, and sha256
#   1 - Extraction failed (logs error)
# Global Variables:
#   PROJECT - Optional prefix prepended to the image filename (trailing '-' stripped)
# Populated Keys:
#   download_url, filename, sha256
#------------------------------------------------------------------------------
function extract_image_info() {
	local -n result=$1
	local json_file="$2"
	local jq_out

	# Extract both fields in a single jq pass using | as delimiter.
	# A pipe character cannot appear in a URL or a hex SHA256, so it is safe.
	if ! jq_out=$(jq -r '
		.architectures.ppc64le.artifacts.openstack.formats["qcow2.gz"].disk
		| "\(.location)|\(.sha256)"
	' "${json_file}" 2>/dev/null); then
		log_error "Failed to extract OpenStack artifacts from JSON"
		return 1
	fi

	result[download_url]="${jq_out%%|*}"
	result[sha256]="${jq_out##*|}"

	if [[ -z "${result[download_url]}" || "${result[download_url]}" == "null" ]]; then
		log_error "Failed to extract download URL from JSON"
		return 1
	fi

	result[filename]="${result[download_url]##*/}"
	result[filename]="${result[filename]%.qcow2.gz}"
	if is_var_set PROJECT; then
		local project="${PROJECT%-}"
		log_info "Prepending project (${project}) to RHCOS filename"
		result[filename]="${project}-${result[filename]}"
	fi

	if [[ -z "${result[sha256]}" || "${result[sha256]}" == "null" ]]; then
		log_warning "SHA256 checksum not found in JSON — proceeding without integrity check"
	fi

	return 0
}

#------------------------------------------------------------------------------
# Function: call_pvsadm
# Description: Convert a qcow2 image to OVA format using pvsadm.
# Arguments:
#   $1 - Image filename (without extension)
#   $2 - Download URL for the qcow2.gz image
# Returns:
#   0 - Converted successfully, file already existed, or dry-run
#   Non-zero - pvsadm execution failed
# Global Variables:
#   DRY_RUN
#------------------------------------------------------------------------------
function call_pvsadm() {
	local filename="$1"
	local url="$2"
	local converted_filename="${PWD}/${filename}.ova.gz"

	if [[ -f "${converted_filename}" ]]; then
		log_info "OVA file already exists, skipping conversion: ${converted_filename}"
		return 0
	fi

	if [[ "${DRY_RUN}" == "true" ]]; then
		log_info    "Would run: pvsadm image qcow2ova --image-dist coreos --image-name ${filename} --image-url ${url} --image-size 16"
		log_warning "DRY RUN mode - skipping pvsadm conversion"
		return 0
	fi
	log_debug "Running: pvsadm image qcow2ova --image-dist coreos --image-name ${filename} --image-url ${url} --image-size 16"

	pvsadm image qcow2ova \
		--image-dist coreos \
		--image-name "${filename}" \
		--image-url "${url}" \
		--image-size 16
}

#------------------------------------------------------------------------------
# Function: call_pvcctl
# Description: Import an image into PowerVC via pvcctl.
#              The image source can be either a remote URL or a local OVA path.
#              When a local path is given (no http(s):// prefix) the file must
#              already exist on disk.
# Arguments:
#   $1 - Image source: remote URL or absolute local path (e.g. .ova.gz)
#   $2 - Image name used as --name in PowerVC
# Returns:
#   0 - Imported successfully or dry-run
#   1 - Local file not found or pvcctl execution failed
# Global Variables:
#   PROJECT_UPLOAD, SVC_HOST, TEMPLATE, DRY_RUN
#------------------------------------------------------------------------------
function call_pvcctl() {
	local image_source="$1"
	local filename="$2"

	if [[ "${DRY_RUN}" == "true" ]]; then
		log_info    "Would run: pvcctl image import-linux --image ${image_source} --name ${filename} --os-type coreos --volume-size 120 --projects ${PROJECT_UPLOAD} --svc-host ${SVC_HOST} --template ${TEMPLATE}"
		log_warning "DRY RUN mode - skipping pvcctl import"
		return 0
	fi

	# Validate local file existence when source is not a remote URL.
	if [[ "${image_source}" != http://* && "${image_source}" != https://* ]]; then
		if [[ ! -f "${image_source}" ]]; then
			log_error "OVA file not found: ${image_source}"
			return 1
		fi
	fi

	log_debug "Running: pvcctl image import-linux --image ${image_source} --name ${filename} --os-type coreos --volume-size 120 --projects ${PROJECT_UPLOAD} --svc-host ${SVC_HOST} --template ${TEMPLATE}"

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
# Description: Import a locally converted OVA image into PowerVC via powervc-image.
# Arguments:
#   $1 - Image filename (without extension); OVA expected at
#        ${PWD}/${filename}.ova.gz (the temp working directory)
# Returns:
#   0 - Imported successfully or dry-run
#   1 - OVA file not found or execution failed
# Global Variables:
#   PROJECT_UPLOAD, TEMPLATE, DRY_RUN
#------------------------------------------------------------------------------
function call_powervc_image() {
	local filename="$1"
	local converted_filename="${PWD}/${filename}.ova.gz"

	if [[ "${DRY_RUN}" == "true" ]]; then
		log_info    "Would run: powervc-image --project ${PROJECT_UPLOAD} import -n ${filename} -p ${converted_filename} -t ${TEMPLATE} -m os-type=coreos architecture=ppc64le"
		log_warning "DRY RUN mode - skipping powervc-image import"
		return 0
	fi
	if [[ ! -f "${converted_filename}" ]]; then
		log_error "OVA file not found: ${converted_filename}"
		return 1
	fi
	log_debug "Running: powervc-image --project ${PROJECT_UPLOAD} import -n ${filename} -p ${converted_filename} -t ${TEMPLATE} -m os-type=coreos architecture=ppc64le"

	powervc-image \
		--project "${PROJECT_UPLOAD}" \
		import \
		-n "${filename}" \
		-p "${converted_filename}" \
		-t "${TEMPLATE}" \
		-m os-type=coreos architecture=ppc64le
}

#------------------------------------------------------------------------------
# Function: process_release
# Description: Process a single OCP release + RHEL version combination —
#              download CoreOS JSON, extract image info, and upload to PowerVC
#              if not already present.
# Arguments:
#   $1 - Release version to process (e.g., "release-4.21")
#   $2 - RHEL version: "rhel9" or "rhel10"
# Returns:
#   0 - Release processed successfully (uploaded or already present)
#   1 - Any step failed
# Global Variables:
#   USE_PVSADM, USE_PVCCTL, DRY_RUN
# Processing Steps:
#   1. Download CoreOS JSON from GitHub for the given RHEL version
#   2. Extract image metadata (URL, filename, SHA256)
#   3. Verify if image already exists in OpenStack
#   4. If not: convert with pvsadm (if available) then import via pvcctl or
#              powervc-image; or import directly from URL via pvcctl if no pvsadm
#------------------------------------------------------------------------------
function process_release() {
	local release="$1"
	local rhel_version="$2"
	declare -A image_info

	# Create a per-release temp file so each release has isolated scratch space
	local json_file
	json_file=$(mktemp)
	trap '/bin/rm -f "${json_file}"' RETURN

	log_info "Processing release: ${release} (${rhel_version})"

	if ! download_coreos_json "${release}" "${rhel_version}" "${json_file}"; then
		return 1
	fi

	if ! extract_image_info image_info "${json_file}"; then
		return 1
	fi

	log_info  "Download URL: ${image_info[download_url]}"
	log_info  "Filename:     ${image_info[filename]}"
	log_debug "SHA256:       ${image_info[sha256]:-}"

	if verify_openstack_resource "image" "${image_info[filename]}"; then
		log_success "${release}: image '${image_info[filename]}' already present — skipping upload"
		return 0
	fi

	# Determine the image source for the import tool:
	#   - If pvsadm is available, convert qcow2 → local OVA first and use that.
	#   - Otherwise pass the remote URL directly (pvcctl only; powervc-image
	#     cannot import from a URL).
	local import_source
	if [[ "${USE_PVSADM}" == "true" ]]; then
		if ! call_pvsadm "${image_info[filename]}" "${image_info[download_url]}"; then
			log_error "pvsadm failed for ${release} (${rhel_version})"
			return 1
		fi
		import_source="${PWD}/${image_info[filename]}.ova.gz"
	else
		import_source="${image_info[download_url]}"
	fi

	if [[ "${USE_PVCCTL}" == "true" ]]; then
		if ! call_pvcctl "${import_source}" "${image_info[filename]}"; then
			log_error "pvcctl failed for ${release} (${rhel_version})"
			return 1
		fi
	else
		if [[ "${USE_PVSADM}" != "true" ]]; then
			log_error "powervc-image requires pvsadm to produce a local OVA file, but pvsadm is not available"
			return 1
		fi
		if ! call_powervc_image "${image_info[filename]}"; then
			log_error "powervc-image failed for ${release} (${rhel_version})"
			return 1
		fi
	fi

	log_success "${release}: image '${image_info[filename]}' uploaded successfully"
	return 0
}

#------------------------------------------------------------------------------
# Function: check_rhcos_images
# Description: Iterate over all known OCP versions and both RHEL versions,
#              processing each release+RHEL combination and tracking failures.
# Arguments:
#   $@ - List of OCP minor version strings (e.g., "4.21" "4.22" "5.0")
# Returns:
#   0 - All combinations processed without error
#   1 - One or more combinations failed (errors already logged per release)
#------------------------------------------------------------------------------
function check_rhcos_images() {
	local -a failed=()
	local -a rhel_versions=("rhel9" "rhel10")
	local version release rhel_version

	log_info "Checking RHCOS images for ${#} version(s) × ${#rhel_versions[@]} RHEL version(s)..."

	for version in "$@"; do
		release="release-${version}"
		for rhel_version in "${rhel_versions[@]}"; do
			if ! process_release "${release}" "${rhel_version}"; then
				log_error "Failed to process ${release} (${rhel_version})"
				failed+=("${release}/${rhel_version}")
			fi
		done
	done

	if [[ ${#failed[@]} -gt 0 ]]; then
		log_error "The following releases failed: ${failed[*]}"
		return 1
	fi

	log_success "All releases processed successfully"
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
#------------------------------------------------------------------------------
function main() {
	local -a versions

	# Step 1: Parse CLI args, prompt for any missing env vars, then validate all
	parse_arguments "$@"
	collect_environment_variables
	validate_environment_variables

	# Step 2: Verify required tools are installed
	check_required_programs

	log_info "Starting ${SCRIPT_NAME}"

	if [[ "${DRY_RUN}" == "true" ]]; then
		log_warning "Running in DRY RUN mode - no actual changes will be performed"
	fi

	# Step 3: Validate the secrets directory exists before doing any work.
	if [[ ! -d "${SECRETS_DIR}" ]]; then
		die "Secrets directory does not exist: ${SECRETS_DIR}"
	fi

	# Step 4: Create a temporary working directory and switch into it.
	# OVA files produced by pvsadm and pvcctl's log file will be written here.
	# The directory is removed on EXIT regardless of how the script terminates.
	local work_dir
	work_dir=$(mktemp -d)
	trap "/bin/rm -rf '${work_dir}'" EXIT
	cd "${work_dir}"
	log_info "Working directory: ${work_dir}"

	# Step 5: Generate default-config.yaml in the working directory.
	create_default_config

	# Step 6: Watch loop — refresh version list and check RHCOS images every 24 hours
	while true; do
		# Refresh the version list each iteration so newly released OCP versions
		# are picked up without restarting the script.
		if ! list_openshift_versions versions; then
			log_warning "Failed to fetch OCP version list — will retry in 24 hours"
		else
			log_debug "Versions: ${versions[*]}"
			if ! check_rhcos_images "${versions[@]}"; then
				log_warning "One or more releases failed this iteration — will retry in 24 hours"
			fi
		fi

		if [[ "${DRY_RUN}" == "true" ]]; then
			log_warning "DRY RUN mode - exiting after first iteration"
			break
		fi

		log_info "Sleeping 24 hours until next check..."
		sleep 86400
	done
}

#==============================================================================
# Script Entry Point
#==============================================================================

# Execute main function with all command-line arguments
main "$@"
