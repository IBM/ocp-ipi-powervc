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

# Color codes for output
readonly COLOR_RED='\033[0;31m'
readonly COLOR_GREEN='\033[0;32m'
readonly COLOR_YELLOW='\033[1;33m'
readonly COLOR_BLUE='\033[0;34m'
readonly COLOR_RESET='\033[0m'

# Debug mode
DEBUG="${DEBUG:-false}"

# Execution mode
EXECUTE_SSH=false

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

log_debug() {
	if ${DEBUG}; then
		echo -e "${COLOR_YELLOW}[DEBUG]${COLOR_RESET} $*"
	fi
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
		read -rp "${prompt_text}: " input_value
	fi

	if [[ -z "${input_value}" ]] && [[ "${allow_empty}" != "true" ]]; then
		die "You must enter a value for ${var_name}"
	fi

	eval "${var_name}='${input_value}'"
	export "${var_name}"
}

#==============================================================================
# Main Functions
#==============================================================================

# Display usage information
show_usage() {
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

# Parse command line arguments
parse_arguments() {
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

# Check all required programs are installed
check_required_programs() {
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

# Collect cluster directory
collect_cluster_directory() {
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

# Extract metadata from cluster directory
extract_metadata() {
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

# Get server name from infra ID or use custom name
get_server_name() {
	if ${IS_INFRA}; then
		SERVER="${INFRA_ID}-${ARG}"
	fi

	if [[ -z "${SERVER}" ]]; then
		die "Server name is empty"
	fi

	log_success "Server name: ${SERVER}"
}

# Query OpenStack server information
query_server_info() {
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
		if [[ $(grep -c "${INFRA_ID}" "${TMP_FILE}") -eq 0 ]]; then
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

# Extract server IP address
extract_server_address() {
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

# Validate SSH key exists
validate_ssh_key() {
	log_info "Validating SSH key..."

	if [[ ! -f "${SSH_KEY}" ]]; then
		die "SSH key not found: ${SSH_KEY}"
	fi

	# Check key permissions
	local key_perms
	key_perms=$(stat -c %a "${SSH_KEY}" 2>/dev/null || stat -f %A "${SSH_KEY}" 2>/dev/null)
	if [[ "${key_perms}" != "600" ]] && [[ "${key_perms}" != "400" ]]; then
		log_warning "SSH key has insecure permissions: ${key_perms}"
		log_warning "Consider running: chmod 600 ${SSH_KEY}"
	fi

	log_success "SSH key validated: ${SSH_KEY}"
}

# Generate SSH command
generate_ssh_command() {
	log_info "Generating SSH command..."

	# Build the SSH command
	SSH_COMMAND="(set -e; IP=\"${ADDRESS}\"; ssh-keygen -f ${KNOWN_HOSTS} -R \${IP} || true; ssh-keyscan \${IP} | sed '/^#/d' >> ${KNOWN_HOSTS}; ssh -tA -i ${SSH_KEY} core@\${IP})"

	log_debug "SSH_COMMAND=${SSH_COMMAND}"
	log_success "SSH command generated"
}

# Execute or display SSH connection command
execute_ssh_connection() {
	echo ""
	log_success "SSH connection details retrieved successfully"
	echo ""

	if ${EXECUTE_SSH}; then
		log_info "Executing SSH connection to ${SERVER} (${ADDRESS})..."
		echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
		echo ""

		# Execute the SSH command
		eval "${SSH_COMMAND}"

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

main() {
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
