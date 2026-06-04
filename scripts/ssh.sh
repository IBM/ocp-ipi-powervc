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
readonly SSH_KEY="${HOME}/.ssh/id_installer_rsa"
readonly KNOWN_HOSTS="${HOME}/.ssh/known_hosts"

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

# Execution mode
EXECUTE_SSH=false

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
# Useful for checking dependencies before attempting to use them.
#
# Arguments:
#   $1 - Command name to check
#
# Returns:
#   0 - Command exists and is executable
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
#   CLUSTER_DIR="/path/to/cluster"
#   validate_non_empty "CLUSTER_DIR"
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
#   prompt_input "Enter cluster name" "CLUSTER_NAME" "my-cluster"
#   prompt_input "Enter optional description" "DESCRIPTION" "" "true"
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
Usage: ${SCRIPT_NAME} [OPTIONS] <server-name>

Connect to an OpenShift cluster node via SSH.

Arguments:
  server-name	 Name of the server to connect to
                 Infrastructure servers: bootstrap, master-0, master-1, master-2
                 Worker nodes: worker-0, worker-1, worker-2, etc.
                 Or any custom server name

Options:
  --execute      Execute SSH command directly instead of printing it
  --help, -h	 Show this help message

Environment Variables:
  CLOUD		  Cloud name from ~/.config/openstack/clouds.yaml
  CLUSTER_DIR     Directory containing cluster metadata (default: test)
  DEBUG		  Enable debug output (default: false)

Examples:
  # Connect to bootstrap node (prints SSH command)
  ${SCRIPT_NAME} bootstrap

  # Connect to master node and execute SSH
  ${SCRIPT_NAME} --execute master-0

  # Connect to worker node
  ${SCRIPT_NAME} worker-0

  # Connect to custom server
  ${SCRIPT_NAME} my-custom-server

  # With environment variables
  CLOUD=mycloud CLUSTER_DIR=prod ${SCRIPT_NAME} master-1

  # Debug mode
  DEBUG=true ${SCRIPT_NAME} bootstrap

Note: The script will construct the full server name using the infraID
      from metadata.json (e.g., infraID-bootstrap, infraID-master-0)
EOF
}

#------------------------------------------------------------------------------
# parse_arguments - Parse CLI options and classify the target server name
#
# Processes command-line arguments, handles help and execution flags, validates
# that a server name was provided, and determines whether the requested target
# should be treated as an infra-managed node or a custom server name. Standard
# bootstrap/master/worker names are expanded later with the cluster infraID;
# custom names are stored directly in SERVER.
#
# Arguments:
#   $@ - Command-line arguments passed to the script
#
# Returns:
#   0 - Arguments parsed successfully
#   Never returns on invalid input or --help handling (calls die or exits)
#
# Globals:
#   EXECUTE_SSH - Set to true when --execute is provided
#   ARG         - Stores the requested server name argument
#   IS_INFRA    - Indicates whether ARG should be expanded with infraID
#   SERVER      - Set immediately for custom server names
#
# Example:
#   parse_arguments --execute master-0
#------------------------------------------------------------------------------
function parse_arguments() {
	if [[ $# -eq 0 ]]; then
		show_usage
		exit 1
	fi

	# Parse options
	while [[ $# -gt 0 ]]; do
		case "$1" in
			--help|-h)
				show_usage
				exit 0
				;;
			--execute)
				EXECUTE_SSH=true
				shift
				;;
			-*)
				die "Unknown option: $1"
				;;
			*)
				# This is the server name
				ARG="$1"
				shift
				break
				;;
		esac
	done

	# Warn about extra arguments
	if [[ $# -gt 0 ]]; then
		log_warning "Ignoring extra arguments: $*"
	fi

	# Check if server name was provided
	if [[ -z "${ARG:-}" ]]; then
		die "Server name is required"
	fi

	# Validate server name pattern
	IS_INFRA=true
	case "${ARG}" in
		bootstrap|master-[0-9]|master-[0-9][0-9])
			log_info "Connecting to infrastructure server: ${ARG}"
			;;
		worker-[0-9]|worker-[0-9][0-9])
			log_info "Connecting to worker node: ${ARG}"
			;;
		*)
			log_warning "${ARG} is not a standard server name"
			log_info "Attempting to connect to custom server: ${ARG}"
			SERVER="${ARG}"
			IS_INFRA=false
			;;
	esac
}

#------------------------------------------------------------------------------
# check_required_programs - Verify external runtime dependencies are available
#
# Ensures that all required commands used by this script are present in PATH
# before any OpenStack or SSH operations are attempted. Missing commands are
# accumulated and reported together to make remediation easier.
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
	local -a required_programs=("jq" "mktemp" "openstack" "ssh" "ssh-keygen" "ssh-keyscan")
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
# collect_cluster_directory - Resolve and validate the cluster working directory
#
# Determines which cluster directory should be used for metadata lookup. If the
# CLUSTER_DIR environment variable is not already set, prompts the user for a
# directory and defaults to "test". The selected directory must exist.
#
# Arguments:
#   None
#
# Returns:
#   0 - CLUSTER_DIR is set and refers to an existing directory
#   Never returns if validation fails (calls die)
#
# Globals:
#   CLUSTER_DIR - Read from environment or prompted and exported
#
# Example:
#   collect_cluster_directory
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
# extract_metadata - Read cloud and infra identifiers from metadata.json
#
# Loads cluster metadata from CLUSTER_DIR/metadata.json and extracts the
# OpenStack cloud name and installer-generated infraID using jq. The CLOUD
# value from metadata overrides any existing environment value.
#
# Arguments:
#   None
#
# Returns:
#   0 - Metadata extracted successfully
#   Never returns if metadata.json is missing or required fields are absent
#
# Globals:
#   CLUSTER_DIR - Directory containing metadata.json
#   CLOUD       - Populated from .openstack.cloud
#   INFRA_ID    - Populated from .infraID
#
# Example:
#   extract_metadata
#------------------------------------------------------------------------------
function extract_metadata() {
	log_info "Extracting metadata from cluster configuration..."

	local metadata_file="${CLUSTER_DIR}/metadata.json"
	if [[ ! -f "${metadata_file}" ]]; then
		die "Metadata file not found: ${metadata_file}"
	fi

	# Extract cloud name
	CLOUD=$(jq -r '.openstack.cloud' "${metadata_file}" 2>/dev/null)
	if [[ -z "${CLOUD}" ]] || [[ "${CLOUD}" == "null" ]]; then
		die "Could not extract cloud name from ${metadata_file}"
	fi
	log_debug "CLOUD=${CLOUD}"

	# Extract infra ID
	INFRA_ID=$(jq -r '.infraID' "${metadata_file}" 2>/dev/null)
	if [[ -z "${INFRA_ID}" ]] || [[ "${INFRA_ID}" == "null" ]]; then
		die "Could not extract infraID from ${metadata_file}"
	fi
	log_debug "INFRA_ID=${INFRA_ID}"

	log_success "Metadata extracted successfully"
}

#------------------------------------------------------------------------------
# get_server_name - Build the OpenStack server name for the SSH target
#
# Resolves the final server name used in OpenStack queries. Infrastructure
# targets are constructed as "<infraID>-<role>", while custom targets retain
# the exact name supplied by the user during argument parsing.
#
# Arguments:
#   None
#
# Returns:
#   0 - SERVER contains a non-empty server name
#   Never returns if the resolved server name is empty (calls die)
#
# Globals:
#   IS_INFRA - Determines whether the server name must be prefixed
#   INFRA_ID - Prefix used for installer-managed infrastructure nodes
#   ARG      - User-supplied role or node name
#   SERVER   - Final resolved server name
#
# Example:
#   get_server_name
#------------------------------------------------------------------------------
function get_server_name() {
	if ${IS_INFRA}; then
		SERVER="${INFRA_ID}-${ARG}"
	fi

	if [[ -z "${SERVER}" ]]; then
		die "Server name is empty"
	fi

	log_success "Server name: ${SERVER}"
}

#------------------------------------------------------------------------------
# query_server_info - Fetch OpenStack server inventory and target details
#
# Queries OpenStack for the available server list, verifies that matching
# infraID-based servers exist when applicable, and then retrieves shell-formatted
# details for the target server. Output is staged in a temporary file that is
# removed automatically on shell exit.
#
# Arguments:
#   None
#
# Returns:
#   0 - Server information retrieved successfully
#   Never returns if OpenStack queries fail or expected servers are missing
#
# Globals:
#   CLOUD     - OpenStack cloud profile passed to the CLI
#   SERVER    - Target server name for `openstack server show`
#   INFRA_ID  - Used to confirm matching cluster servers exist
#   IS_INFRA  - Controls infraID presence validation
#   TMP_FILE  - Temporary file path storing OpenStack command output
#   DEBUG     - Enables logging of captured command output
#
# Example:
#   query_server_info
#------------------------------------------------------------------------------
function query_server_info() {
	log_info "Querying OpenStack server: ${SERVER}"

	local tmp_file
	tmp_file=$(mktemp)
	readonly TMP_FILE="${tmp_file}"

	# Setup cleanup trap
	trap 'rm -f "${TMP_FILE}"' EXIT

	# First, check if any servers exist with the infra ID
	if ! openstack --os-cloud="${CLOUD}" server list --format=csv > "${TMP_FILE}" 2>/dev/null; then
		die "Failed to list servers. Is OpenStack configured correctly?"
	fi

	if ${DEBUG}; then
		log_debug "Server list:"
		cat "${TMP_FILE}"
	fi

	if ${IS_INFRA}
	then
		# Check if any servers with infra ID exist
		if ! grep -q "${INFRA_ID}" "${TMP_FILE}"; then
			die "No servers found with infra ID: ${INFRA_ID}"
		fi
	fi

	# Get specific server information
	if ! openstack --os-cloud="${CLOUD}" server show "${SERVER}" --format=shell > "${TMP_FILE}" 2>/dev/null; then
		die "Failed to query server: ${SERVER}"
	fi

	if ${DEBUG}; then
		log_debug "Server details:"
		cat "${TMP_FILE}"
	fi

	log_success "Server information retrieved"
}

#------------------------------------------------------------------------------
# extract_server_address - Parse the SSH target IP from server details
#
# Reads the shell-formatted OpenStack server details captured in TMP_FILE,
# locates the addresses field, and extracts the IP address using the current
# sed-based parsing logic. The parsing assumes the OpenStack CLI returns the
# expected addresses representation.
#
# Arguments:
#   None
#
# Returns:
#   0 - ADDRESS extracted successfully
#   Never returns if the address field is missing or cannot be parsed
#
# Globals:
#   TMP_FILE - Temporary file containing `openstack server show` output
#   ADDRESS  - Parsed SSH destination IP address
#
# Example:
#   extract_server_address
#------------------------------------------------------------------------------
function extract_server_address() {
	log_info "Extracting server IP address..."

	# Extract addresses line
	local address_line
	address_line=$(grep "addresses=" "${TMP_FILE}" 2>/dev/null)
	if [[ -z "${address_line}" ]]; then
		die "Could not find addresses in server information"
	fi
	log_debug "ADDRESS_LINE=${address_line}"

	# Parse IP address from addresses line
	# Format: addresses="network=['10.0.0.1', '192.168.1.1']"
	ADDRESS=$(echo "${address_line}" | sed -e "s,[^[]*[[]',," -e "s,'.*,,")
	if [[ -z "${ADDRESS}" ]]; then
		die "Could not extract IP address from server information"
	fi
	log_debug "ADDRESS=${ADDRESS}"

	log_success "Server IP address: ${ADDRESS}"
}

#------------------------------------------------------------------------------
# validate_ssh_key - Confirm the installer SSH key is present and usable
#
# Verifies that the configured private SSH key exists before a connection is
# attempted. The function also inspects file permissions and warns when they
# are broader than the commonly accepted secure values for private keys.
#
# Arguments:
#   None
#
# Returns:
#   0 - SSH key exists and basic validation completed
#   Never returns if the key file is missing (calls die)
#
# Globals:
#   SSH_KEY - Path to the private key used for cluster access
#
# Example:
#   validate_ssh_key
#------------------------------------------------------------------------------
function validate_ssh_key() {
	log_info "Validating SSH key..."

	if [[ ! -f "${SSH_KEY}" ]]; then
		die "SSH key not found: ${SSH_KEY}"
	fi

	# Check key permissions
	local key_perms
	key_perms=$(stat -c %a "${SSH_KEY}" 2>/dev/null || stat -f %Lp "${SSH_KEY}" 2>/dev/null)
	if [[ "${key_perms}" != "600" ]] && [[ "${key_perms}" != "400" ]]; then
		log_warning "SSH key has insecure permissions: ${key_perms}"
		log_warning "Consider running: chmod 600 ${SSH_KEY}"
	fi

	log_success "SSH key validated: ${SSH_KEY}"
}

#------------------------------------------------------------------------------
# generate_ssh_command - Build a printable SSH helper command
#
# Constructs a single shell command string that removes any existing known_hosts
# entry for the target IP, records the current host key, and opens an interactive
# SSH session as the `core` user. This command is intended for display to users
# when --execute is not requested.
#
# Arguments:
#   None
#
# Returns:
#   0 - SSH_COMMAND generated successfully
#
# Globals:
#   ADDRESS      - Target IP address
#   KNOWN_HOSTS  - SSH known_hosts file to update
#   SSH_KEY      - Private key used for authentication
#   SSH_COMMAND  - Generated shell command string
#
# Example:
#   generate_ssh_command
#------------------------------------------------------------------------------
function generate_ssh_command() {
	log_info "Generating SSH command..."

	# Build the SSH command for display
	SSH_COMMAND="(set -e; IP=\"${ADDRESS}\"; ssh-keygen -f \"${KNOWN_HOSTS}\" -R \"\${IP}\" || true; ssh-keyscan \"\${IP}\" | sed '/^#/d' >> \"${KNOWN_HOSTS}\"; ssh -tA -i \"${SSH_KEY}\" core@\"\${IP}\")"

	log_debug "SSH_COMMAND=${SSH_COMMAND}"
	log_success "SSH command generated"
}

#------------------------------------------------------------------------------
# _execute_ssh_direct - Refresh host keys and open the SSH session immediately
#
# Performs the same connection steps represented by SSH_COMMAND, but executes
# them directly instead of returning a shell snippet. Any existing known_hosts
# entry for the target IP is removed first so a fresh host key can be recorded.
#
# Arguments:
#   $1 - IP address of the server to connect to
#
# Returns:
#   0 - SSH command completed successfully
#   Non-zero - ssh or a prerequisite command failed
#
# Globals:
#   KNOWN_HOSTS - SSH known_hosts file to update
#   SSH_KEY     - Private key used for authentication
#
# Example:
#   _execute_ssh_direct "192.168.122.10"
#------------------------------------------------------------------------------
function _execute_ssh_direct() {
	local ip="$1"
	ssh-keygen -f "${KNOWN_HOSTS}" -R "${ip}" 2>/dev/null || true
	ssh-keyscan "${ip}" 2>/dev/null | sed '/^#/d' >> "${KNOWN_HOSTS}"
	ssh -tA -i "${SSH_KEY}" "core@${ip}"
}

#------------------------------------------------------------------------------
# execute_ssh_connection - Either run the SSH session or print the command
#
# Presents the final connection result to the user. When EXECUTE_SSH is true,
# the SSH session is started immediately. Otherwise, the prebuilt command is
# printed so the user can review or run it manually.
#
# Arguments:
#   None
#
# Returns:
#   0 - Connection command displayed or executed successfully
#   Non-zero - Propagates SSH failure when executing directly
#
# Globals:
#   EXECUTE_SSH - Selects execution versus display mode
#   SERVER      - Resolved OpenStack server name
#   ADDRESS     - Target IP address
#   SSH_COMMAND - Printable SSH command shown in non-execute mode
#   SCRIPT_NAME - Used in follow-up guidance output
#   ARG         - Original server argument shown in follow-up guidance
#
# Example:
#   execute_ssh_connection
#------------------------------------------------------------------------------
function execute_ssh_connection() {
	echo ""
	log_success "SSH connection details retrieved successfully"
	echo ""

	if ${EXECUTE_SSH}; then
		log_info "Executing SSH connection to ${SERVER} (${ADDRESS})..."
		echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
		echo ""

		# Execute the SSH connection directly
		_execute_ssh_direct "${ADDRESS}"

		echo ""
		echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
	else
		log_info "To connect to ${SERVER} (${ADDRESS}), run the following command:"
		echo ""
		echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
		echo "${SSH_COMMAND}"
		echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
		echo ""
		log_info "Or run with --execute flag to connect directly:"
		echo "  ${SCRIPT_NAME} --execute ${ARG}"
	fi
}

#==============================================================================
# Main Execution
#==============================================================================

#------------------------------------------------------------------------------
# main - Main execution function orchestrating the SSH connection workflow
#
# Coordinates all steps required to establish an SSH connection to an OpenShift
# cluster node:
# 1. Parse command-line arguments
# 2. Check required programs
# 3. For infra targets, collect cluster configuration and extract metadata
# 4. For custom targets, require CLOUD from the environment
# 5. Resolve and query server information from OpenStack
# 6. Validate SSH key
# 7. Generate and execute/display SSH command
#
# Arguments:
#   $@ - All command-line arguments (passed to parse_arguments)
#
# Returns:
#   0 - Script completed successfully
#   Non-zero - Error occurred (via die or SSH failure)
#
# Example:
#   main "$@"
#------------------------------------------------------------------------------
function main() {
	log_info "Starting SSH connection script"
	log_info "Script: ${SCRIPT_NAME}"
	echo ""

	# Parse arguments
	parse_arguments "$@"

	# Check requirements
	check_required_programs
	echo ""

	if ${IS_INFRA}
	then
		# Collect configuration
		collect_cluster_directory
		extract_metadata
		echo ""
	else
		# For custom servers, CLOUD must be provided via environment
		if [[ -z "${CLOUD:-}" ]]; then
			die "CLOUD environment variable must be set for custom server names"
		fi
		log_debug "Using CLOUD=${CLOUD} from environment"
	fi

	# Get server information
	get_server_name
	query_server_info
	extract_server_address
	echo ""

	# Prepare SSH connection
	validate_ssh_key
	generate_ssh_command
	echo ""

	# Execute or display connection
	execute_ssh_connection
}

# Run main function
main "$@"

# Made with Bob
