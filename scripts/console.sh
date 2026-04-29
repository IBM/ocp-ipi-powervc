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

set -euo pipefail

#==============================================================================
# Global Variables
#==============================================================================
readonly SCRIPT_NAME="$(basename "${BASH_SOURCE[0]}")"
readonly SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly CLOUDS_YAML="${HOME}/.config/openstack/clouds.yaml"

# Color codes for output
readonly COLOR_RED='\033[0;31m'
readonly COLOR_GREEN='\033[0;32m'
readonly COLOR_YELLOW='\033[1;33m'
readonly COLOR_BLUE='\033[0;34m'
readonly COLOR_RESET='\033[0m'

# Debug mode
DEBUG="${DEBUG:-false}"

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

#==============================================================================
# Main Functions
#==============================================================================

# Display usage information
show_usage() {
    cat <<EOF
Usage: ${SCRIPT_NAME} <server-name>

Connect to the console of an OpenShift cluster node via PowerVC/HMC.

Arguments:
  server-name    Name of the server to connect to
                 Valid infra servers: bootstrap, master-0, master-1, master-2
                 Or any custom server name

Environment Variables:
  CLOUD          Cloud name from ~/.config/openstack/clouds.yaml
  CLUSTER_DIR    Directory containing cluster metadata (default: test)
  DEBUG          Enable debug output (default: false)

Examples:
  ${SCRIPT_NAME} bootstrap
  ${SCRIPT_NAME} master-0
  ${SCRIPT_NAME} my-custom-server

Note: You must have SSH_PASSWORDS array set in your environment:
  export SSH_PASSWORDS=("password1" "password2")
EOF
}

# Parse command line arguments
parse_arguments() {
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

# Check all required programs are installed
check_required_programs() {
    local -a required_programs=("jq" "openstack" "yq" "curl" "ping")
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

# Collect cloud configuration
collect_cloud_config() {
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

# Extract cloud configuration from clouds.yaml
extract_cloud_config() {
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

# Verify PowerVC server connectivity
verify_server_connectivity() {
    log_info "Verifying PowerVC server connectivity: ${SERVER_IP}"

    if ! ping -c1 -W5 "${SERVER_IP}" >/dev/null 2>&1; then
        die "Cannot ping PowerVC server at ${SERVER_IP}"
    fi

    log_success "PowerVC server is reachable"
}

# Get server name from infra ID or use custom name
get_server_name() {
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

# Query OpenStack server information
query_server_info() {
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

# Get authentication token from PowerVC
get_auth_token() {
    if [[ -v TOKEN_ID ]] && [[ -n "${TOKEN_ID}" ]]; then
        log_info "Using existing authentication token"
        return 0
    fi

    log_info "Obtaining authentication token from PowerVC..."

    local auth_json
    auth_json='{ "auth": { "scope": { "project": { "domain": { "name": "Default" }, "name": "'"${PROJECT_NAME}"'" } }, "identity": { "password": { "user": { "domain": { "name": "Default" }, "password": "'"${PASSWORD}"'", "name": "'"${USERNAME}"'" } }, "methods": [ "password" ] } } }'

    TOKEN_ID=$(curl --tlsv1 --insecure --silent -i \
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

# Query hypervisor information
query_hypervisor_info() {
    log_info "Querying hypervisor information..."

    if ! curl --tlsv1 --insecure --silent \
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

# Get console connection details for HMC
get_hmc_console_details() {
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
    SSH_IP=$(curl --tlsv1 --insecure --silent \
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

# Get console connection details for PowerVM
get_pvm_console_details() {
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

# Get console connection details based on manager type
get_console_details() {
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

# Display console connection command
display_console_command() {
    log_success "Console connection details retrieved successfully"
    echo ""
    log_info "To connect to the console, run the following command:"
    echo ""
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo 'for SSH_PASSWORD in "${SSH_PASSWORDS[@]}"; do sshpass -p $SSH_PASSWORD ssh -t -o PubkeyAuthentication=no '"${USER_ID}@${SSH_IP}"' mkvterm -m '"${HOST_DISPLAY_NAME}"' -p '"${INSTANCE_NAME}"'; done'
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo ""
    log_warning "Note: You must have SSH_PASSWORDS array set in your environment:"
    echo '  export SSH_PASSWORDS=("password1" "password2")'
}

#==============================================================================
# Main Execution
#==============================================================================

main() {
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
    collect_cluster_directory
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
