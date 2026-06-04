#!/usr/bin/env bash

#==============================================================================
# console.sh - OpenShift Cluster Node Console Connection Script
#==============================================================================
#
# Description:
#   Connects to the console of an OpenShift cluster node via PowerVC/HMC.
#   This script automates the process of obtaining console access to cluster
#   nodes (bootstrap, masters, or custom servers) by:
#   - Querying OpenStack/PowerVC for server information
#   - Authenticating with PowerVC API
#   - Retrieving hypervisor and console connection details
#   - Generating the appropriate SSH command for console access
#
# Usage:
#   ./console.sh <server-name>
#
# Arguments:
#   server-name - Name of the server to connect to
#                 Valid infra servers: bootstrap, master-0, master-1, master-2
#                 Or any custom server name
#
# Environment Variables:
#   CLOUD       - Cloud name from ~/.config/openstack/clouds.yaml (required)
#   CLUSTER_DIR - Directory containing cluster metadata (default: test)
#   DEBUG       - Enable debug output (true/false, default: false)
#   TOKEN_ID    - Existing PowerVC authentication token (optional)
#
# Prerequisites:
#   - OpenStack CLI tools (openstack command)
#   - jq (JSON processor)
#   - yq (YAML processor)
#   - curl (HTTP client)
#   - ping (network utility)
#   - sshpass (for password-based SSH)
#   - SSH_PASSWORDS array set in environment
#
# Examples:
#   export CLOUD="mycloud"
#   export SSH_PASSWORDS=("password1" "password2")
#   ./console.sh bootstrap
#   ./console.sh master-0
#
# Exit Codes:
#   0 - Success
#   1 - Error (various failure conditions)
#
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
#==============================================================================

set -euo pipefail

#==============================================================================
# Global Variables
#==============================================================================
readonly SCRIPT_NAME="$(basename "${BASH_SOURCE[0]}")"
readonly SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly CLOUDS_YAML="${HOME}/.config/openstack/clouds.yaml"

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
# Outputs an informational message to stdout with [INFO] prefix in blue.
#
# Arguments:
#   $* - Message text to display
#
# Returns:
#   0 - Always succeeds
#------------------------------------------------------------------------------
function log_info() {
	echo -e "${COLOR_BLUE}[INFO]${COLOR_RESET} $*"
}

#------------------------------------------------------------------------------
# log_success - Print success message with green color
#
# Outputs a success message to stdout with [SUCCESS] prefix in green.
#
# Arguments:
#   $* - Message text to display
#
# Returns:
#   0 - Always succeeds
#------------------------------------------------------------------------------
function log_success() {
	echo -e "${COLOR_GREEN}[SUCCESS]${COLOR_RESET} $*"
}

#------------------------------------------------------------------------------
# log_warning - Print warning message with yellow color
#
# Outputs a warning message to stdout with [WARNING] prefix in yellow.
#
# Arguments:
#   $* - Message text to display
#
# Returns:
#   0 - Always succeeds
#------------------------------------------------------------------------------
function log_warning() {
	echo -e "${COLOR_YELLOW}[WARNING]${COLOR_RESET} $*"
}

#------------------------------------------------------------------------------
# log_error - Print error message with red color
#
# Outputs an error message to stderr with [ERROR] prefix in red.
#
# Arguments:
#   $* - Message text to display
#
# Returns:
#   0 - Always succeeds
#------------------------------------------------------------------------------
function log_error() {
	echo -e "${COLOR_RED}[ERROR]${COLOR_RESET} $*" >&2
}

#------------------------------------------------------------------------------
# log_debug - Print debug message when DEBUG mode is enabled
#
# Outputs a debug message to stdout with [DEBUG] prefix in yellow, but only
# when the DEBUG global variable is set to true.
#
# Arguments:
#   $* - Message text to display
#
# Globals:
#   DEBUG - Boolean flag controlling debug output
#
# Returns:
#   0 - Always succeeds
#------------------------------------------------------------------------------
function log_debug() {
	if ${DEBUG}; then
		echo -e "${COLOR_YELLOW}[DEBUG]${COLOR_RESET} $*"
	fi
}

#------------------------------------------------------------------------------
# die - Exit script with error message
#
# Logs an error message and exits the script with status code 1.
#
# Arguments:
#   $* - Error message to display
#
# Returns:
#   Never returns (exits with code 1)
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
#   0 - Command exists
#   1 - Command not found
#
# Example:
#   if command_exists "jq"; then
#       echo "jq is installed"
#   fi
#------------------------------------------------------------------------------
function command_exists() {
	command -v "$1" >/dev/null 2>&1
}

#------------------------------------------------------------------------------
# validate_non_empty - Validate that a variable is set and non-empty
#
# Checks if a named variable is set and contains a non-empty value. Exits
# the script with an error if the variable is empty or unset.
#
# Arguments:
#   $1 - Name of the variable to validate
#
# Returns:
#   0 - Variable is set and non-empty
#   Never returns if validation fails (calls die)
#
# Example:
#   CLOUD="mycloud"
#   validate_non_empty "CLOUD"  # Succeeds
#------------------------------------------------------------------------------
function validate_non_empty() {
	local var_name="$1"
	local var_value="${!var_name:-}"

	if [[ -z "${var_value}" ]]; then
		die "${var_name} must be set and non-empty"
	fi
}

#------------------------------------------------------------------------------
# prompt_input - Prompt user for input with optional default value
#
# Prompts the user for input, optionally providing a default value. The input
# is stored in the specified variable and exported to the environment.
#
# Arguments:
#   $1 - Prompt text to display
#   $2 - Variable name to store the input
#   $3 - Default value (optional)
#   $4 - Allow empty input: "true" or "false" (optional, default: "false")
#
# Returns:
#   0 - Input successfully collected
#   Never returns if validation fails and allow_empty is false (calls die)
#
# Example:
#   prompt_input "Enter cloud name" "CLOUD" "default-cloud"
#   prompt_input "Enter optional value" "OPTIONAL" "" "true"
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
# execute_with_check - Execute command with error handling and logging
#
# Executes a command with automatic error checking and logging. Logs the
# command description before execution and success/failure after completion.
#
# Arguments:
#   $1 - Description of the command being executed
#   $@ - Command and arguments to execute (shift applied to $1)
#
# Returns:
#   0 - Command executed successfully
#   Never returns if command fails (calls die)
#
# Example:
#   execute_with_check "Installing packages" apt-get install -y jq
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
# Main Functions
#==============================================================================

#------------------------------------------------------------------------------
# show_usage - Display script usage information
#
# Prints comprehensive usage information including command syntax, arguments,
# environment variables, examples, and prerequisites to stdout.
#
# Arguments:
#   None
#
# Returns:
#   0 - Always succeeds
#
# Output:
#   Formatted usage text to stdout
#------------------------------------------------------------------------------
function show_usage() {
	cat <<EOF
Usage: ${SCRIPT_NAME} <server-name>

Connect to the console of an OpenShift cluster node via PowerVC/HMC.

Arguments:
  server-name	Name of the server to connect to
				 Valid infra servers: bootstrap, master-0, master-1, master-2
				 Or any custom server name

Environment Variables:
  CLOUD		  Cloud name from ~/.config/openstack/clouds.yaml
  CLUSTER_DIR	Directory containing cluster metadata (default: test)
  DEBUG		  Enable debug output (default: false)

Examples:
  ${SCRIPT_NAME} bootstrap
  ${SCRIPT_NAME} master-0
  ${SCRIPT_NAME} my-custom-server

Note: You must have SSH_PASSWORDS array set in your environment:
  export SSH_PASSWORDS=("password1" "password2")
EOF
}

#------------------------------------------------------------------------------
# parse_arguments - Parse and validate command line arguments
#
# Parses the server name argument and determines if it's a standard
# infrastructure server (bootstrap, master-0, master-1, master-2) or a
# custom server name. Sets global variables ARG, IS_INFRA, and SERVER.
#
# Arguments:
#   $1 - Server name to connect to
#
# Globals Set:
#   ARG      - The server name argument
#   IS_INFRA - Boolean flag (true if standard infra server, false otherwise)
#   SERVER   - The server name (set only for custom servers)
#
# Returns:
#   0 - Valid argument provided
#   1 - Invalid number of arguments (calls show_usage and exits)
#
# Example:
#   parse_arguments "bootstrap"  # Sets IS_INFRA=true, ARG="bootstrap"
#   parse_arguments "my-server"  # Sets IS_INFRA=false, SERVER="my-server"
#------------------------------------------------------------------------------
function parse_arguments() {
	if [[ $# -ne 1 ]]; then
		show_usage
		exit 1
	fi

	ARG="$1"
	IS_INFRA=true

	# Check if it's a standard infra server
	case "${ARG}" in
		bootstrap|master-0|master-1|master-2)
			log_info "Connecting to infrastructure server: ${ARG}"
			;;
		*)
			log_warning "${ARG} is not a standard infra server"
			log_info "Attempting to connect to custom server: ${ARG}"
			SERVER="${ARG}"
			IS_INFRA=false
			;;
	esac
}

#------------------------------------------------------------------------------
# check_required_programs - Verify all required programs are installed
#
# Checks that all required command-line tools are available in PATH.
# Required programs: jq, openstack, yq, curl, ping, sshpass
#
# Arguments:
#   None
#
# Returns:
#   0 - All required programs are available
#   Never returns if any program is missing (calls die)
#
# Example:
#   check_required_programs  # Verifies all tools are installed
#------------------------------------------------------------------------------
function check_required_programs() {
	local -a required_programs=("jq" "openstack" "yq" "curl" "ping" "sshpass")
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
# collect_cloud_config - Collect and validate cloud configuration
#
# Prompts for cloud name if not set in environment, validates it's non-empty,
# and verifies the clouds.yaml file exists. The cloud name is used to access
# OpenStack/PowerVC credentials.
#
# Arguments:
#   None
#
# Globals Read:
#   CLOUD       - Cloud name (optional, will prompt if not set)
#   CLOUDS_YAML - Path to clouds.yaml file
#
# Globals Set:
#   CLOUD - Cloud name (if prompted)
#
# Returns:
#   0 - Cloud configuration collected successfully
#   Never returns if validation fails (calls die)
#
# Example:
#   collect_cloud_config  # Prompts for CLOUD if not set
#------------------------------------------------------------------------------
function collect_cloud_config() {
	log_info "Collecting cloud configuration..."

	if [[ ! -v CLOUD ]]; then
		prompt_input "What is the cloud name in ${CLOUDS_YAML}" "CLOUD"
	fi

	validate_non_empty "CLOUD"

	if [[ ! -f "${CLOUDS_YAML}" ]]; then
		die "Clouds configuration file not found: ${CLOUDS_YAML}"
	fi

	log_success "Cloud: ${CLOUD}"
}

#------------------------------------------------------------------------------
# collect_cluster_directory - Collect and validate cluster directory
#
# Prompts for cluster directory if not set in environment, validates it's
# non-empty, and verifies the directory exists. The cluster directory
# contains metadata.json needed for infrastructure servers.
#
# Arguments:
#   None
#
# Globals Read:
#   CLUSTER_DIR - Cluster directory path (optional, will prompt if not set)
#
# Globals Set:
#   CLUSTER_DIR - Cluster directory path (if prompted)
#
# Returns:
#   0 - Cluster directory collected and validated successfully
#   Never returns if validation fails (calls die)
#
# Example:
#   collect_cluster_directory  # Prompts for CLUSTER_DIR if not set
#------------------------------------------------------------------------------
function collect_cluster_directory() {
	log_info "Collecting cluster directory information..."

	if [[ ! -v CLUSTER_DIR ]]; then
		prompt_input "What directory should be used for the installation" "CLUSTER_DIR" "test"
	fi

	validate_non_empty "CLUSTER_DIR"

	if [[ ! -d "${CLUSTER_DIR}" ]]; then
		die "Directory ${CLUSTER_DIR} does not exist!"
	fi

	log_success "Cluster directory: ${CLUSTER_DIR}"
}

#------------------------------------------------------------------------------
# extract_cloud_config - Extract cloud configuration from clouds.yaml
#
# Parses the clouds.yaml file to extract authentication and connection
# details for the specified cloud. Extracts:
# - auth_url (PowerVC API endpoint)
# - Server IP address
# - project_id and project_name
# - username and password
#
# Arguments:
#   None
#
# Globals Read:
#   CLOUD       - Cloud name to extract configuration for
#   CLOUDS_YAML - Path to clouds.yaml file
#
# Globals Set:
#   SERVER_URL   - Full authentication URL
#   HOSTNAME_URL - Hostname portion of auth_url
#   SERVER_IP    - IP address of PowerVC server
#   PROJECT_ID   - OpenStack project ID
#   PROJECT_NAME - OpenStack project name
#   USERNAME     - Authentication username
#   PASSWORD     - Authentication password
#
# Returns:
#   0 - Configuration extracted successfully
#   Never returns if extraction fails (calls die)
#
# Security Note:
#   Password is not logged for security reasons
#------------------------------------------------------------------------------
function extract_cloud_config() {
	log_info "Extracting cloud configuration from ${CLOUDS_YAML}..."

	# Extract auth_url
	SERVER_URL=$(yq eval ".clouds.${CLOUD}.auth.auth_url" "${CLOUDS_YAML}" 2>/dev/null)
	if [[ -z "${SERVER_URL}" ]] || [[ "${SERVER_URL}" == "null" ]]; then
		die "Could not get auth_url from ${CLOUDS_YAML}"
	fi
	log_debug "SERVER_URL=${SERVER_URL}"

	# Extract hostname and IP from auth_url
	HOSTNAME_URL=$(echo "${SERVER_URL}" | awk -F/ '{print $3}')
	log_debug "HOSTNAME_URL=${HOSTNAME_URL}"

	SERVER_IP=$(echo "${HOSTNAME_URL}" | awk -F: '{print $1}')
	log_debug "SERVER_IP=${SERVER_IP}"

	if [[ -z "${SERVER_IP}" ]]; then
		die "Could not extract SERVER_IP from auth_url"
	fi

	# Extract project_id
	PROJECT_ID=$(yq eval ".clouds.${CLOUD}.auth.project_id" "${CLOUDS_YAML}" 2>/dev/null)
	if [[ -z "${PROJECT_ID}" ]] || [[ "${PROJECT_ID}" == "null" ]]; then
		die "Could not get project_id from ${CLOUDS_YAML}"
	fi
	log_debug "PROJECT_ID=${PROJECT_ID}"

	# Extract project_name
	PROJECT_NAME=$(yq eval ".clouds.${CLOUD}.auth.project_name" "${CLOUDS_YAML}" 2>/dev/null)
	if [[ -z "${PROJECT_NAME}" ]] || [[ "${PROJECT_NAME}" == "null" ]]; then
		die "Could not get project_name from ${CLOUDS_YAML}"
	fi
	log_debug "PROJECT_NAME=${PROJECT_NAME}"

	# Extract username
	USERNAME=$(yq eval ".clouds.${CLOUD}.auth.username" "${CLOUDS_YAML}" 2>/dev/null)
	if [[ -z "${USERNAME}" ]] || [[ "${USERNAME}" == "null" ]]; then
		die "Could not get username from ${CLOUDS_YAML}"
	fi
	log_debug "USERNAME=${USERNAME}"

	# Extract password
	PASSWORD=$(yq eval ".clouds.${CLOUD}.auth.password" "${CLOUDS_YAML}" 2>/dev/null)
	if [[ -z "${PASSWORD}" ]] || [[ "${PASSWORD}" == "null" ]]; then
		die "Could not get password from ${CLOUDS_YAML}"
	fi
	# Don't log password for security

	log_success "Cloud configuration extracted successfully"
}

#------------------------------------------------------------------------------
# verify_server_connectivity - Verify PowerVC server is reachable
#
# Tests network connectivity to the PowerVC server using ping. Sends a
# single ping with 5-second timeout to verify the server is accessible.
#
# Arguments:
#   None
#
# Globals Read:
#   SERVER_IP - IP address of PowerVC server to test
#
# Returns:
#   0 - Server is reachable
#   Never returns if server is unreachable (calls die)
#
# Example:
#   verify_server_connectivity  # Pings SERVER_IP
#------------------------------------------------------------------------------
function verify_server_connectivity() {
	log_info "Verifying PowerVC server connectivity: ${SERVER_IP}"

	local timeout_flag="-W"
	# macOS uses -t for timeout in seconds, Linux uses -W
	if [[ "$(uname)" == "Darwin" ]]; then
		timeout_flag="-t"
	fi
	if ! ping -c1 "${timeout_flag}" 5 "${SERVER_IP}" >/dev/null 2>&1; then
		die "Cannot ping PowerVC server at ${SERVER_IP}"
	fi

	log_success "PowerVC server is reachable"
}

#------------------------------------------------------------------------------
# get_server_name - Determine full server name
#
# For infrastructure servers (bootstrap, masters), constructs the full
# server name by combining the infrastructure ID from metadata.json with
# the server type. For custom servers, uses the name as-is.
#
# Arguments:
#   None
#
# Globals Read:
#   IS_INFRA    - Boolean flag indicating if this is an infra server
#   ARG         - Server type (bootstrap, master-0, etc.)
#   SERVER      - Custom server name (if IS_INFRA is false)
#   CLUSTER_DIR - Directory containing metadata.json
#
# Globals Set:
#   INFRA_ID - Infrastructure ID (only for infra servers)
#   SERVER   - Full server name (for infra servers)
#
# Returns:
#   0 - Server name determined successfully
#   Never returns if metadata extraction fails (calls die)
#
# Example:
#   # For infra server: INFRA_ID="test-abc123", ARG="bootstrap"
#   get_server_name  # Sets SERVER="test-abc123-bootstrap"
#
#   # For custom server: SERVER="my-server"
#   get_server_name  # SERVER remains "my-server"
#------------------------------------------------------------------------------
function get_server_name() {
	if ${IS_INFRA}; then
		log_info "Getting infrastructure ID from metadata..."

		local metadata_file="${CLUSTER_DIR}/metadata.json"
		if [[ ! -f "${metadata_file}" ]]; then
			die "Metadata file not found: ${metadata_file}"
		fi

		INFRA_ID=$(jq -r .infraID "${metadata_file}" 2>/dev/null)
		if [[ -z "${INFRA_ID}" ]] || [[ "${INFRA_ID}" == "null" ]]; then
			die "Could not extract infraID from ${metadata_file}"
		fi
		log_debug "INFRA_ID=${INFRA_ID}"

		SERVER="${INFRA_ID}-${ARG}"
	fi

	if [[ -z "${SERVER}" ]]; then
		die "Server name is empty"
	fi

	log_success "Server name: ${SERVER}"
}

#------------------------------------------------------------------------------
# query_server_info - Query OpenStack server information
#
# Queries OpenStack for detailed server information including hypervisor
# hostname and instance name. Creates temporary files to store the query
# results and sets up cleanup trap.
#
# Arguments:
#   None
#
# Globals Read:
#   CLOUD  - Cloud name for OpenStack authentication
#   SERVER - Server name to query
#
# Globals Set:
#   SERVER_FILE      - Path to temporary file containing server info (readonly)
#   HYPERVISOR_FILE  - Path to temporary file for hypervisor info (readonly)
#   HYPERVISOR       - Hypervisor hostname where server is running
#   INSTANCE_NAME    - OpenStack instance name for the server
#
# Returns:
#   0 - Server information retrieved successfully
#   Never returns if query fails (calls die)
#
# Side Effects:
#   - Creates temporary files (cleaned up via EXIT trap)
#   - Sets EXIT trap for cleanup
#
# Example:
#   query_server_info  # Queries server and extracts hypervisor details
#------------------------------------------------------------------------------
function query_server_info() {
	log_info "Querying OpenStack server: ${SERVER}"

	SERVER_FILE=$(mktemp)
	HYPERVISOR_FILE=$(mktemp)
	readonly SERVER_FILE HYPERVISOR_FILE

	# Setup cleanup trap
	trap 'rm -f "${SERVER_FILE}" "${HYPERVISOR_FILE}"' EXIT

	# Get server information
	if ! openstack --os-cloud="${CLOUD}" server show "${SERVER}" --format=json > "${SERVER_FILE}" 2>/dev/null; then
		die "Failed to query server: ${SERVER}"
	fi

	# Extract hypervisor hostname
	HYPERVISOR=$(jq -r '."OS-EXT-SRV-ATTR:hypervisor_hostname"' "${SERVER_FILE}" 2>/dev/null)
	if [[ -z "${HYPERVISOR}" ]] || [[ "${HYPERVISOR}" == "null" ]]; then
		die "Could not extract hypervisor hostname from server info"
	fi
	log_debug "HYPERVISOR=${HYPERVISOR}"

	# Extract instance name
	INSTANCE_NAME=$(jq -r '."OS-EXT-SRV-ATTR:instance_name"' "${SERVER_FILE}" 2>/dev/null)
	if [[ -z "${INSTANCE_NAME}" ]] || [[ "${INSTANCE_NAME}" == "null" ]]; then
		die "Could not extract instance name from server info"
	fi
	log_debug "INSTANCE_NAME=${INSTANCE_NAME}"

	log_success "Server information retrieved"
}

#------------------------------------------------------------------------------
# get_auth_token - Get authentication token from PowerVC
#
# Obtains an authentication token from PowerVC API using password-based
# authentication. If TOKEN_ID is already set in the environment, reuses
# the existing token. The token is required for subsequent API calls.
#
# Arguments:
#   None
#
# Globals Read:
#   TOKEN_ID     - Existing token (optional, checked first)
#   SERVER_IP    - PowerVC server IP address
#   PROJECT_NAME - OpenStack project name
#   USERNAME     - Authentication username
#   PASSWORD     - Authentication password
#
# Globals Set:
#   TOKEN_ID - Authentication token for PowerVC API
#
# Returns:
#   0 - Token obtained or existing token reused
#   Never returns if authentication fails (calls die)
#
# Security Note:
#   Uses HTTPS with TLS 1.0+ and insecure mode (skips certificate validation)
#
# Example:
#   get_auth_token  # Obtains new token or reuses existing TOKEN_ID
#------------------------------------------------------------------------------
function get_auth_token() {
	if [[ -v TOKEN_ID ]] && [[ -n "${TOKEN_ID}" ]]; then
		log_info "Using existing authentication token"
		return 0
	fi

	log_info "Obtaining authentication token from PowerVC..."

	local auth_json
	auth_json=$(jq -n \
		--arg project_name "${PROJECT_NAME}" \
		--arg username "${USERNAME}" \
		--arg password "${PASSWORD}" \
		'{
			auth: {
				scope: {
					project: {
						domain: { name: "Default" },
						name: $project_name
					}
				},
				identity: {
					password: {
						user: {
							domain: { name: "Default" },
							password: $password,
							name: $username
						}
					},
					methods: ["password"]
				}
			}
		}')

	TOKEN_ID=$(curl --tlsv1.2 --insecure --silent -i \
		--request POST \
		--header "Accept: application/json" \
		--header "Content-Type: application/json" \
		--data "${auth_json}" \
		"https://${SERVER_IP}:5000/v3/auth/tokens" 2>/dev/null | \
		grep -i x-subject-token | \
		cut -d ' ' -f2 | \
		tr -d '\r')

	if [[ -z "${TOKEN_ID}" ]]; then
		die "Failed to obtain authentication token"
	fi

	log_debug "TOKEN_ID obtained"
	log_success "Authentication successful"
}

#------------------------------------------------------------------------------
# query_hypervisor_info - Query hypervisor information from PowerVC
#
# Queries PowerVC API for detailed hypervisor information including
# registration details and manager type (HMC or PowerVM). The information
# is stored in HYPERVISOR_FILE for subsequent processing.
#
# Arguments:
#   None
#
# Globals Read:
#   TOKEN_ID        - PowerVC authentication token
#   SERVER_IP       - PowerVC server IP address
#   PROJECT_ID      - OpenStack project ID
#   HYPERVISOR      - Hypervisor hostname to query
#   HYPERVISOR_FILE - Path to file for storing hypervisor info
#
# Globals Set:
#   MANAGER_TYPE - Type of hypervisor manager (hmc or pvm)
#
# Returns:
#   0 - Hypervisor information retrieved successfully
#   Never returns if query fails (calls die)
#
# Example:
#   query_hypervisor_info  # Queries hypervisor and extracts manager type
#------------------------------------------------------------------------------
function query_hypervisor_info() {
	log_info "Querying hypervisor information..."

	if ! curl --tlsv1.2 --insecure --silent \
		--request GET \
		--header "X-Auth-Token:${TOKEN_ID}" \
		"https://${SERVER_IP}:8774/v2.1/${PROJECT_ID}/os-hosts/${HYPERVISOR}" \
		> "${HYPERVISOR_FILE}" 2>/dev/null; then
		die "Failed to query hypervisor information"
	fi

	# Extract manager type
	MANAGER_TYPE=$(jq -r '.host[].registration | select(length > 0) | .manager_type' "${HYPERVISOR_FILE}" 2>/dev/null)
	if [[ -z "${MANAGER_TYPE}" ]] || [[ "${MANAGER_TYPE}" == "null" ]]; then
		die "Could not extract manager type from hypervisor info"
	fi
	log_debug "MANAGER_TYPE=${MANAGER_TYPE}"

	log_success "Hypervisor information retrieved"
}

#------------------------------------------------------------------------------
# get_hmc_console_details - Get console connection details for HMC-managed systems
#
# Retrieves console connection details for systems managed by Hardware
# Management Console (HMC). Extracts the primary HMC UUID from hypervisor
# info, queries the HMC API for access details, and sets connection parameters.
#
# Arguments:
#   None
#
# Globals Read:
#   HYPERVISOR_FILE - Path to file containing hypervisor information
#   TOKEN_ID        - PowerVC authentication token
#   SERVER_IP       - PowerVC server IP address
#   PROJECT_ID      - OpenStack project ID
#
# Globals Set:
#   PRIMARY_HMC_UUID - UUID of the primary HMC managing the hypervisor
#   SSH_IP           - IP address of the HMC for SSH connection
#   USER_ID          - Username for HMC connection (always "hscroot")
#
# Returns:
#   0 - HMC console details retrieved successfully
#   Never returns if extraction fails (calls die)
#
# Example:
#   get_hmc_console_details  # Retrieves HMC connection parameters
#------------------------------------------------------------------------------
function get_hmc_console_details() {
	log_info "Getting HMC console details..."

	# Extract primary HMC UUID
	PRIMARY_HMC_UUID=$(jq -r '.host[].registration | select(length > 0) | .primary_hmc_uuid' "${HYPERVISOR_FILE}" 2>/dev/null)
	if [[ -z "${PRIMARY_HMC_UUID}" ]] || [[ "${PRIMARY_HMC_UUID}" == "null" ]]; then
		log_error "Could not find primary HMC UUID in hypervisor info:"
		cat "${HYPERVISOR_FILE}"
		die "Primary HMC UUID not found"
	fi
	log_debug "PRIMARY_HMC_UUID=${PRIMARY_HMC_UUID}"

	# Query HMC information
	log_info "Querying HMC information..."
	SSH_IP=$(curl --tlsv1.2 --insecure --silent \
		--request GET \
		--header "X-Auth-Token:${TOKEN_ID}" \
		"https://${SERVER_IP}:8774/v2.1/${PROJECT_ID}/ibm-hmcs/${PRIMARY_HMC_UUID}" 2>/dev/null | \
		jq -r .hmc.registration.access_ip 2>/dev/null)

	if [[ -z "${SSH_IP}" ]] || [[ "${SSH_IP}" == "null" ]]; then
		die "Could not extract HMC access IP"
	fi
	log_debug "SSH_IP=${SSH_IP}"

	USER_ID="hscroot"
	log_debug "USER_ID=${USER_ID}"

	log_success "HMC console details retrieved"
}

#------------------------------------------------------------------------------
# get_pvm_console_details - Get console connection details for PowerVM systems
#
# Retrieves console connection details for systems managed directly by
# PowerVM (without HMC). Extracts user ID and access IP from hypervisor
# registration information.
#
# Arguments:
#   None
#
# Globals Read:
#   HYPERVISOR_FILE - Path to file containing hypervisor information
#
# Globals Set:
#   USER_ID - Username for PowerVM connection
#   SSH_IP  - IP address of PowerVM host for SSH connection
#
# Returns:
#   0 - PowerVM console details retrieved successfully
#   Never returns if extraction fails (calls die)
#
# Example:
#   get_pvm_console_details  # Retrieves PowerVM connection parameters
#------------------------------------------------------------------------------
function get_pvm_console_details() {
	log_info "Getting PowerVM console details..."

	# Extract user ID
	USER_ID=$(jq -r '.host[].registration | select(length > 0) | .user_id' "${HYPERVISOR_FILE}" 2>/dev/null)
	if [[ -z "${USER_ID}" ]] || [[ "${USER_ID}" == "null" ]]; then
		die "Could not extract user_id from hypervisor info"
	fi
	log_debug "USER_ID=${USER_ID}"

	# Extract access IP
	SSH_IP=$(jq -r '.host[].registration | select(length > 0) | .access_ip' "${HYPERVISOR_FILE}" 2>/dev/null)
	if [[ -z "${SSH_IP}" ]] || [[ "${SSH_IP}" == "null" ]]; then
		die "Could not extract access_ip from hypervisor info"
	fi
	log_debug "SSH_IP=${SSH_IP}"

	log_success "PowerVM console details retrieved"
}

#------------------------------------------------------------------------------
# get_console_details - Get console connection details based on manager type
#
# Dispatches to the appropriate function (HMC or PowerVM) based on the
# manager type, then extracts the host display name common to both types.
# This is the main entry point for retrieving console connection details.
#
# Arguments:
#   None
#
# Globals Read:
#   MANAGER_TYPE    - Type of hypervisor manager (hmc or pvm)
#   HYPERVISOR_FILE - Path to file containing hypervisor information
#
# Globals Set:
#   HOST_DISPLAY_NAME - Display name of the host system
#   SSH_IP            - IP address for SSH connection (via sub-functions)
#   USER_ID           - Username for connection (via sub-functions)
#
# Returns:
#   0 - Console details retrieved successfully
#   Never returns if manager type is unknown or extraction fails (calls die)
#
# Example:
#   get_console_details  # Calls appropriate function based on MANAGER_TYPE
#------------------------------------------------------------------------------
function get_console_details() {
	case "${MANAGER_TYPE}" in
		hmc)
			get_hmc_console_details
			;;
		pvm)
			get_pvm_console_details
			;;
		*)
			die "Unknown manager type: ${MANAGER_TYPE}"
			;;
	esac

	# Extract host display name
	HOST_DISPLAY_NAME=$(jq -r '.host[].registration | select(length > 0) | .host_display_name' "${HYPERVISOR_FILE}" 2>/dev/null)
	if [[ -z "${HOST_DISPLAY_NAME}" ]] || [[ "${HOST_DISPLAY_NAME}" == "null" ]]; then
		die "Could not extract host display name from hypervisor info"
	fi
	log_debug "HOST_DISPLAY_NAME=${HOST_DISPLAY_NAME}"
}

#------------------------------------------------------------------------------
# display_console_command - Display console connection command
#
# Formats and displays the SSH command needed to connect to the server
# console via mkvterm. The command uses sshpass for password authentication
# and iterates through SSH_PASSWORDS array to handle multiple possible
# passwords.
#
# Arguments:
#   None
#
# Globals Read:
#   USER_ID           - Username for SSH connection
#   SSH_IP            - IP address for SSH connection
#   HOST_DISPLAY_NAME - Display name of the host system
#   INSTANCE_NAME     - OpenStack instance name
#
# Returns:
#   0 - Always succeeds
#
# Output:
#   Formatted console connection command to stdout
#
# Example:
#   display_console_command  # Displays mkvterm SSH command
#------------------------------------------------------------------------------
function display_console_command() {
	log_success "Console connection details retrieved successfully"
	echo ""
	log_info "To connect to the console, run the following command:"
	echo ""
	echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
	echo 'for SSH_PASSWORD in "${SSH_PASSWORDS[@]}"; do sshpass -p "$SSH_PASSWORD" ssh -t -o PubkeyAuthentication=no '"${USER_ID}@${SSH_IP}"' mkvterm -m '"${HOST_DISPLAY_NAME}"' -p '"${INSTANCE_NAME}"' && break; done'
	echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
	echo ""
	log_warning "Note: You must have SSH_PASSWORDS array set in your environment:"
	echo '  export SSH_PASSWORDS=("password1" "password2")'
}

#==============================================================================
# Main Execution
#==============================================================================

#------------------------------------------------------------------------------
# main - Main execution function
#
# Orchestrates the entire console connection process by calling functions
# in the correct order to:
# 1. Parse arguments
# 2. Check requirements
# 3. Collect configuration
# 4. Extract cloud configuration
# 5. Get server information
# 6. Authenticate and query hypervisor
# 7. Display connection command
#
# Arguments:
#   $@ - Command line arguments (passed to parse_arguments)
#
# Returns:
#   0 - Script completed successfully
#   Never returns on error (sub-functions call die)
#------------------------------------------------------------------------------
function main() {
	log_info "Starting console connection script"
	log_info "Script: ${SCRIPT_NAME}"
	echo ""

	# Parse arguments
	parse_arguments "$@"

	# Check requirements
	check_required_programs
	echo ""

	# Collect configuration
	collect_cloud_config
	if ${IS_INFRA}
	then
		collect_cluster_directory
	fi
	echo ""

	# Extract cloud configuration
	extract_cloud_config
	verify_server_connectivity
	echo ""

	# Get server information
	get_server_name
	query_server_info
	echo ""

	# Authenticate and query
	get_auth_token
	query_hypervisor_info
	get_console_details
	echo ""

	# Display connection command
	display_console_command
}

# Run main function
main "$@"

# Made with Bob
