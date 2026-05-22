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

################################################################################
# Script: create-cluster.sh
# Description: Automated OpenShift cluster creation on PowerVC/OpenStack
#
# This script orchestrates the complete process of creating an OpenShift cluster
# on PowerVC infrastructure, including:
#   - Environment validation and prerequisite checks
#   - Bastion host creation with HAProxy load balancer
#   - DNS propagation verification
#   - OpenShift installation configuration generation
#   - Cluster deployment via openshift-install
#   - Metadata management and cleanup on failure
#
# The script provides interactive prompts for required parameters or accepts
# them via environment variables for automation.
#
# Environment Variables (all optional, will prompt if not set):
#   CLOUD               - OpenStack cloud name from clouds.yaml
#   BASEDOMAIN          - Base DNS domain for the cluster (e.g., example.com)
#   BASTION_IMAGE_NAME  - OpenStack image for bastion host
#   BASTION_USERNAME    - SSH username for bastion (default: cloud-user)
#   CLUSTER_DIR         - Installation directory (default: test)
#   CLUSTER_NAME        - Name of the OpenShift cluster
#   FLAVOR_NAME         - OpenStack flavor for cluster nodes
#   MACHINE_TYPE        - OpenStack availability zone
#   NETWORK_NAME        - OpenStack network for cluster
#   PULL_SECRET         - Red Hat pull secret for image registry
#   PULLSECRET_FILE     - Path to file containing pull secret
#   INSTALLER_SSHKEY    - Path to SSH public key for cluster nodes
#   CONTROLLER_IP       - IP address of PowerVC controller
#   SSHKEY_NAME         - OpenStack keypair name for bastion
#   BASTION_RSA         - Path to SSH private key for bastion (failure recovery)
#
# Required Files:
#   ~/.config/openstack/clouds.yaml - OpenStack authentication configuration
#   ${INSTALLER_SSHKEY}             - SSH public key file
#   ${PULLSECRET_FILE}              - Pull secret file (if using file)
#
# Dependencies:
#   - ocp-ipi-powervc-linux-{arch}: PowerVC automation tool
#   - openshift-install: OpenShift installer binary
#   - openstack: OpenStack CLI client
#   - jq: JSON processor
#   - getent: DNS resolution utility
#   - ping: Network connectivity testing
#
# Generated Files:
#   ${CLUSTER_DIR}/install-config.yaml  - OpenShift installation configuration
#   ${CLUSTER_DIR}/metadata.json        - Cluster metadata
#   ${CLUSTER_DIR}/auth/kubeconfig      - Cluster admin credentials
#   ${CLUSTER_DIR}/auth/kubeadmin-password - Admin password
#   /tmp/bastionIp                      - Temporary bastion IP storage
#
# Exit Codes:
#   0 - Cluster created successfully
#   1 - Validation failure, missing dependencies, or cluster creation failure
#
# Usage Examples:
#   # Interactive mode (prompts for all inputs)
#   ./create-cluster.sh
#
#   # With environment variables (automated)
#   export CLOUD=mycloud
#   export BASEDOMAIN=example.com
#   export CLUSTER_NAME=mycluster
#   export CLUSTER_DIR=test-cluster
#   export FLAVOR_NAME=medium
#   export MACHINE_TYPE=zone1
#   export NETWORK_NAME=private-net
#   export BASTION_IMAGE_NAME=rhel-8
#   export BASTION_USERNAME=cloud-user
#   export SSHKEY_NAME=my-keypair
#   export INSTALLER_SSHKEY=~/.ssh/id_rsa.pub
#   export PULLSECRET_FILE=~/pull-secret.txt
#   export CONTROLLER_IP=192.168.1.100
#   ./create-cluster.sh
#
#   # With inline pull secret
#   export PULL_SECRET='{"auths":{"cloud.openshift.com":{"auth":"..."}}}'
#   ./create-cluster.sh
#
# Process Flow:
#   1. Initialize and validate prerequisites
#   2. Collect and validate environment variables
#   3. Verify OpenStack connectivity and resources
#   4. Retrieve network and RHCOS image information
#   5. Verify PowerVC controller connectivity
#   6. Create bastion host with HAProxy
#   7. Wait for DNS propagation (wildcard, api, api-int)
#   8. Verify VIP matches DNS resolution
#   9. Generate install-config.yaml
#   10. Create ignition configs and send metadata to controller
#   11. Create manifests
#   12. Deploy OpenShift cluster (30-45 minutes)
#   13. Handle failures with automatic cleanup
#
# Error Handling:
#   - Automatic metadata cleanup on failure via trap
#   - Temporary file cleanup on exit
#   - Detailed error messages with context
#   - Cluster monitoring on installation failure
#
# Security Considerations:
#   - Pull secrets are masked in output
#   - SSH keys are masked in output
#   - Symlink attack prevention for temporary files
#   - Secure password input (hidden from terminal)
#
# Notes:
#   - Cluster creation typically takes 30-45 minutes
#   - DNS propagation may take several minutes
#   - Bastion host acts as load balancer for cluster API and ingress
#   - Script uses colored output for better readability
#   - Supports ppc64le, x86_64, and aarch64 architectures
################################################################################

set -euo pipefail

#==============================================================================
# Global Variables
# Defines script-wide constants and configuration values
#==============================================================================
readonly SCRIPT_NAME="$(basename "${BASH_SOURCE[0]}")"
readonly SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Temporary file for storing bastion IP address
readonly TEMP_BASTION_IP="$(mktemp -t bastionIp.XXXXXX)"

# Security: Prevent symlink attacks on temporary file
if [[ -L "${TEMP_BASTION_IP}" ]]; then
	echo "Security error: ${TEMP_BASTION_IP} is a symlink" >&2
	exit 1
fi

################################################################################
# Security helper: Safe write to temp file (prevents symlink attacks)
################################################################################
function safe_write_temp_file() {
	local content="$1"
	local target="${TEMP_BASTION_IP}"

	# Remove any existing file/symlink
	rm -f "${target}" 2>/dev/null || true

	# Create with restrictive permissions, fail if path is a symlink
	(
		set -o noclobber
		umask 077
		# Re-check for symlink inside subshell to minimize race window
		if [[ -L "${target}" ]]; then
			echo "Security error: ${target} is a symlink" >&2
			exit 1
		fi
		echo "${content}" > "${target}"
	)
}

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
# validate_non_empty: Ensure environment variable is set and non-empty
# Parameters:
#   $1 - Variable name to validate
# Exits: With error if variable is empty or unset
# Usage: validate_non_empty "CLUSTER_NAME"
################################################################################
function validate_non_empty() {
	local var_name="$1"
	local var_value="${!var_name:-}"

	if [[ -z "${var_value}" ]]; then
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
# wait_for_dns: Wait for DNS hostname to resolve with retry logic
# Parameters:
#   $1 - Hostname to resolve
#   $2 - Maximum attempts (default: 10)
#   $3 - Sleep interval in seconds (default: 5)
# Returns: 0 if resolved, 1 if timeout
# Usage: wait_for_dns "api.cluster.example.com" 20 10
################################################################################
function wait_for_dns() {
	local hostname="$1"
	local max_attempts="${2:-10}"
	local sleep_interval="${3:-5}"

	log_info "Waiting for DNS resolution: ${hostname}"

	for ((attempt = 1; attempt <= max_attempts; attempt++)); do
		if getent ahostsv4 "${hostname}" >/dev/null 2>&1; then
			log_success "DNS resolved: ${hostname}"
			return 0
		fi

		if [[ ${attempt} -lt ${max_attempts} ]]; then
			log_info "Attempt ${attempt}/${max_attempts} failed, retrying in ${sleep_interval}s..."
			sleep "${sleep_interval}"
		fi
	done

	return 1
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
# Core business logic for cluster creation workflow
#==============================================================================

# Global flag to track metadata cleanup status
METADATA_CLEANED=false

################################################################################
# cleanup_metadata: Remove cluster metadata from controller on failure
# Deletes metadata.json from PowerVC controller to prevent orphaned resources
# Uses global METADATA_CLEANED flag to prevent duplicate cleanup attempts
# Safe to call multiple times (idempotent)
################################################################################
function cleanup_metadata() {
	if [[ "${METADATA_CLEANED}" == "true" ]]; then
		return 0
	fi

	# Guard against early failures where variables aren't set
	if [[ ! -v POWERVC_TOOL ]] || [[ ! -v CONTROLLER_IP ]]; then
		log_info "Cleanup skipped: required variables not initialized"
		return 0
	fi

	if [[ ! -v CLUSTER_DIR ]] || [[ -z "${CLUSTER_DIR}" ]]; then
		log_info "Cleanup skipped: CLUSTER_DIR not set"
		return 0
	fi

	# Guard against the file not existing
	if [[ ! -f "${CLUSTER_DIR}/metadata.json" ]]; then
		log_info "No metadata.json to clean up"
		return 0
	fi

	log_warning "Cleaning up metadata.json"

	if ! "${POWERVC_TOOL}" \
		send-metadata \
		--deleteMetadata "${CLUSTER_DIR}/metadata.json" \
		--serverIP "${CONTROLLER_IP}" \
		--shouldDebug true; then
		log_error "Failed to delete metadata, but continuing..."
	fi

	METADATA_CLEANED=true
}

################################################################################
# cleanup_on_exit: Trap handler for script exit
# Automatically cleans up resources on script failure
# Registered via: trap cleanup_on_exit EXIT
# Cleans up:
#   - Cluster metadata from controller
#   - Temporary bastion IP file
################################################################################
function cleanup_on_exit() {
	local exit_code=$?

	# Always clean up temporary files
	[[ -f "${TEMP_BASTION_IP}" ]] && /bin/rm -f "${TEMP_BASTION_IP}"

	if [[ ${exit_code} -ne 0 ]]; then
		log_error "Script failed with exit code ${exit_code}"
	fi
}

trap cleanup_on_exit EXIT

################################################################################
# initialize_powervc_tool: Determine architecture-specific tool binary
# Detects system architecture and sets POWERVC_TOOL to appropriate binary name
# Supported architectures: x86_64, amd64, ppc64le, aarch64
# Sets: POWERVC_TOOL global variable
# Exits: If architecture is unsupported
################################################################################
function initialize_powervc_tool() {
	local arch
	arch="$(uname -m)"

	case "${arch}" in
		x86_64|amd64|ppc64le|aarch64)
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
	local -a required_programs=("${POWERVC_TOOL}" "openshift-install" "openstack" "jq" "getent")
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
# get_network_info: Retrieve network configuration from OpenStack
# Queries OpenStack for subnet ID and CIDR information
# Sets global variables:
#   - SUBNET_ID: OpenStack subnet UUID
#   - MACHINE_NETWORK: Network CIDR (e.g., 192.168.1.0/24)
# Uses: $CLOUD, $NETWORK_NAME
# Exits: If network information cannot be retrieved
################################################################################
function get_network_info() {
	log_info "Retrieving network information for: ${NETWORK_NAME}"

	# Get subnet ID from network
	local subnet_output
	subnet_output=$(openstack --os-cloud="${CLOUD}" network show "${NETWORK_NAME}" --format shell 2>/dev/null | grep '^subnets=')

	if [[ -z "${subnet_output}" ]]; then
		die "Failed to retrieve subnet information for network: ${NETWORK_NAME}"
	fi

	SUBNET_ID=$(echo "${subnet_output}" | sed -e "s,^[^']*',," -e "s,'.*$,,")

	if [[ -z "${SUBNET_ID}" ]]; then
		die "SUBNET_ID is empty after parsing"
	fi

	log_info "Subnet ID: ${SUBNET_ID}"

	# Get machine network CIDR from subnet
	local cidr_output
	cidr_output=$(openstack --os-cloud="${CLOUD}" subnet show "${SUBNET_ID}" --format shell 2>/dev/null | grep '^cidr=')

	if [[ -z "${cidr_output}" ]]; then
		die "Failed to retrieve CIDR for subnet: ${SUBNET_ID}"
	fi

	MACHINE_NETWORK=$(echo "${cidr_output}" | sed -re 's,^[^"]*"(.*)",\1,')

	if [[ -z "${MACHINE_NETWORK}" ]]; then
		die "MACHINE_NETWORK is empty after parsing"
	fi

	log_success "Machine network CIDR: ${MACHINE_NETWORK}"
}

################################################################################
# get_rhcos_info: Retrieve RHCOS image information from installer
# Queries openshift-install for the appropriate RHCOS image for ppc64le
# Sets global variables:
#   - RHCOS_URL: Download URL for RHCOS image
#   - RHCOS_FILENAME: Image name without .qcow2.gz extension
# Exits: If RHCOS information cannot be retrieved
################################################################################
function get_rhcos_info() {
	log_info "Retrieving RHCOS image information..."

	# Determine target architecture for RHCOS
	# Note: This is the cluster architecture, which may differ from host
	local target_arch="ppc64le"  # PowerVC clusters are always ppc64le

	local rhcos_json
	rhcos_json=$(openshift-install coreos print-stream-json 2>/dev/null)

	if [[ -z "${rhcos_json}" ]]; then
		die "Failed to retrieve RHCOS stream JSON"
	fi

	RHCOS_URL=$(echo "${rhcos_json}" | jq -r ".architectures.${target_arch}.artifacts.openstack.formats.\"qcow2.gz\".disk.location")

	if [[ -z "${RHCOS_URL}" ]] || [[ "${RHCOS_URL}" == "null" ]]; then
		die "Failed to extract RHCOS URL from stream JSON"
	fi

	RHCOS_FILENAME="${RHCOS_URL##*/}"
	RHCOS_FILENAME="${RHCOS_FILENAME//.qcow2.gz/}"

	if [[ -z "${RHCOS_FILENAME}" ]]; then
		die "RHCOS_FILENAME is empty after parsing"
	fi

	log_success "RHCOS image: ${RHCOS_FILENAME}"
}

################################################################################
# verify_controller: Verify PowerVC controller connectivity and health
# Tests network connectivity via ping and performs health check
# Uses: $CONTROLLER_IP, $POWERVC_TOOL
# Exits: If controller is unreachable or health check fails
################################################################################
function verify_controller() {
	log_info "Verifying controller connectivity: ${CONTROLLER_IP}"

	if ! ping -c1 -W5 "${CONTROLLER_IP}" >/dev/null 2>&1; then
		die "Cannot ping controller at ${CONTROLLER_IP}"
	fi

	log_success "Controller is reachable"

	if ! "${POWERVC_TOOL}" \
		check-alive \
		--serverIP "${CONTROLLER_IP}" \
		--shouldDebug true; then
		die "Controller health check failed"
	fi

	log_success "Controller health check passed"
}

################################################################################
# collect_environment_variables: Gather all required configuration
# Prompts user for any missing environment variables
# Handles both interactive and automated (pre-set variables) modes
# Supports pull secret from file or direct input
# Validates SSH key file existence
# Sets and exports all required variables
################################################################################
function collect_environment_variables() {
	log_info "Collecting environment variables..."

	# Cloud name from clouds.yaml
	if [[ ! -v CLOUD ]]; then
		prompt_input "What is the cloud name in ~/.config/openstack/clouds.yaml" "CLOUD"
	fi

	# Base domain for cluster DNS
	if [[ ! -v BASEDOMAIN ]]; then
		prompt_input "What is the base domain" "BASEDOMAIN"
	fi

	# Bastion host image
	if [[ ! -v BASTION_IMAGE_NAME ]]; then
		prompt_input "What is the image name to use for the bastion" "BASTION_IMAGE_NAME"
	fi

	# Bastion SSH username
	if [[ ! -v BASTION_USERNAME ]]; then
		prompt_input "What is the username to use for the bastion" "BASTION_USERNAME" "cloud-user"
	fi

	# Cluster installation directory
	if [[ ! -v CLUSTER_DIR ]]; then
		prompt_input "What directory should be used for the installation" "CLUSTER_DIR" "test"

		validate_cluster_dir_not_exists
	fi

	# Cluster name
	if [[ ! -v CLUSTER_NAME ]]; then
		prompt_input "What is the name of the cluster" "CLUSTER_NAME"
	fi

	# OpenStack flavor for nodes
	if [[ ! -v FLAVOR_NAME ]]; then
		prompt_input "What is the OpenStack flavor" "FLAVOR_NAME"
	fi

	# Availability zone (machine type)
	if [[ ! -v MACHINE_TYPE ]]; then
		prompt_input "What is the OpenStack machine type" "MACHINE_TYPE"
	fi

	# OpenStack network
	if [[ ! -v NETWORK_NAME ]]; then
		prompt_input "What is the OpenStack network" "NETWORK_NAME"
	fi

	# Pull secret (from file or direct input)
	if [[ -v PULLSECRET_FILE ]]; then
		if [[ ! -f "${PULLSECRET_FILE}" ]]; then
			log_warning "PULLSECRET_FILE (${PULLSECRET_FILE}) does not exist"
			prompt_input "What is your pull secret" "PULL_SECRET" "" false true
		else
			if ! PULL_SECRET=$(cat "${PULLSECRET_FILE}"); then
				die "Failed to read pull secret from: ${PULLSECRET_FILE}"
			fi
			if [[ -z "${PULL_SECRET}" ]]; then
				die "Pull secret file is empty: ${PULLSECRET_FILE}"
			fi
			export PULL_SECRET
			log_success "Loaded pull secret from file"
		fi
	else
		if [[ ! -v PULL_SECRET ]]; then
			prompt_input "What is your pull secret" "PULL_SECRET" "" false true
		fi
	fi

	# SSH public key for cluster nodes
	if [[ ! -v INSTALLER_SSHKEY ]]; then
		prompt_input "What ssh key is used by the installer" "INSTALLER_SSHKEY"
	fi

	if [[ ! -f "${INSTALLER_SSHKEY}" ]]; then
		die "SSH key file does not exist: ${INSTALLER_SSHKEY}"
	fi

	# Strip carriage returns before validation
	SSH_KEY=$(cat "${INSTALLER_SSHKEY}" | tr -d '\r')
	if [[ -z "${SSH_KEY}" ]]; then
		die "SSH key file is empty: ${INSTALLER_SSHKEY}"
	fi

	# Verify that INSTALLER_SSHKEY is a valid public SSH key
	# Public SSH keys should start with ssh-rsa, ssh-ed25519, ecdsa-sha2-nistp256, etc.
	# Format: ssh-<type> <base64-key-data> [optional-comment]
	if ! echo "${SSH_KEY}" | grep -qE '^(ssh-rsa|ssh-ed25519|ecdsa-sha2-nistp(256|384|521)|ssh-dss) [A-Za-z0-9+/]+=*( .*)?$'; then
		die "Invalid SSH public key format in ${INSTALLER_SSHKEY}. Expected format: 'ssh-<type> <key-data> [optional-comment]'"
	fi

	# Additional validation: try to use ssh-keygen to verify the key format
	if command -v ssh-keygen &> /dev/null; then
		if ! ssh-keygen -l -f "${INSTALLER_SSHKEY}" &> /dev/null; then
			die "SSH key validation failed: ${INSTALLER_SSHKEY} is not a valid public SSH key"
		fi
		log_success "Verified SSH public key format"
	else
		log_warning "ssh-keygen not found, skipping advanced SSH key validation"
	fi

	readonly SSH_KEY

	# PowerVC controller IP
	if [[ ! -v CONTROLLER_IP ]]; then
		prompt_input "What is the ${POWERVC_TOOL} master controller IP" "CONTROLLER_IP"
	fi

	# OpenStack keypair for bastion
	if [[ ! -v SSHKEY_NAME ]]; then
		prompt_input "What is the OpenStack keypair to use for the bastion" "SSHKEY_NAME"
	fi

	log_success "All environment variables collected"
}

################################################################################
# validate_cluster_dir_not_exists: Ensures the cluster directory does not exist
################################################################################
function validate_cluster_dir_not_exists() {
	if [[ -d "${CLUSTER_DIR}" ]]; then
		die "Directory ${CLUSTER_DIR} already exists. Please delete it or choose a different name."
	fi
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
		"BASEDOMAIN"
		"BASTION_IMAGE_NAME"
		"BASTION_USERNAME"
		"CLOUD"
		"CLUSTER_DIR"
		"CLUSTER_NAME"
		"CONTROLLER_IP"
		"FLAVOR_NAME"
		"MACHINE_TYPE"
		"NETWORK_NAME"
		"SSHKEY_NAME"
	)

	for var in "${required_vars[@]}"; do
		validate_non_empty "${var}"
	done

	log_success "All environment variables validated"
}

################################################################################
# verify_all_openstack_resources: Verify all OpenStack resources exist
# Checks that all required OpenStack resources are available before proceeding
# Verifies:
#   - Bastion image
#   - Node flavor
#   - Network
#   - SSH keypair
#   - Availability zone
#   - RHCOS image
# Exits: If any resource is not found
################################################################################
function verify_all_openstack_resources() {
	log_info "Verifying OpenStack resources..."

	verify_openstack_resource "image" "${BASTION_IMAGE_NAME}"
	verify_openstack_resource "flavor" "${FLAVOR_NAME}"
	verify_openstack_resource "network" "${NETWORK_NAME}"
	verify_openstack_resource "keypair" "${SSHKEY_NAME}"

	# Verify availability zone
	log_info "Verifying availability zone: ${MACHINE_TYPE}"
	if ! openstack --os-cloud="${CLOUD}" availability zone list --format csv 2>/dev/null | grep -qF "\"${MACHINE_TYPE}\""; then
		die "Cannot find availability zone: ${MACHINE_TYPE}"
	fi
	log_success "Found availability zone: ${MACHINE_TYPE}"

	# Verify RHCOS image
	verify_openstack_resource "image" "${RHCOS_FILENAME}"

	log_success "All OpenStack resources verified"
}

################################################################################
# create_bastion_host: Create bastion host with HAProxy load balancer
# Creates a bastion VM that serves as:
#   - Jump host for cluster access
#   - HAProxy load balancer for API and ingress traffic
# Sets global variables:
#   - VIP_API: Virtual IP for cluster API endpoint
#   - VIP_INGRESS: Virtual IP for cluster ingress (same as VIP_API)
# Creates: ${TEMP_BASTION_IP} file containing the bastion's IP address
# Exits: If bastion creation fails or IP file is not created
################################################################################
function create_bastion_host() {
	log_info "Creating bastion host..."

	# Remove previous bastion IP file if it exists
	[[ -f "${TEMP_BASTION_IP}" ]] && rm -f "${TEMP_BASTION_IP}"

	# Create cluster directory for installation files
	mkdir "${CLUSTER_DIR}"

	execute_with_check "Bastion creation" \
		"${POWERVC_TOOL}" \
		create-bastion \
		--cloud "${CLOUD}" \
		--bastionName "${CLUSTER_NAME}" \
		--availabilityZone "${MACHINE_TYPE}" \
		--flavorName "${FLAVOR_NAME}" \
		--imageName "${BASTION_IMAGE_NAME}" \
		--networkName "${NETWORK_NAME}" \
		--sshKeyName "${SSHKEY_NAME}" \
		--domainName "${BASEDOMAIN}" \
		--enableHAProxy true \
		--serverIP "${CONTROLLER_IP}" \
		--bastionIpFile "${TEMP_BASTION_IP}" \
		--shouldDebug true

	if [[ ! -f "${TEMP_BASTION_IP}" ]]; then
		die "Bastion IP file not created: ${TEMP_BASTION_IP}"
	fi

	VIP_API=$(cat "${TEMP_BASTION_IP}")
	VIP_INGRESS=$(cat "${TEMP_BASTION_IP}")

	if [[ -z "${VIP_API}" ]] || [[ -z "${VIP_INGRESS}" ]]; then
		die "VIP_API and VIP_INGRESS must be defined"
	fi

	log_success "Bastion created with VIP: ${VIP_API}"
}

################################################################################
# wait_for_all_dns_entries: Wait for DNS propagation of all cluster entries
# Polls DNS servers until all required entries are resolvable:
#   - Wildcard DNS: *.apps.<cluster>.<domain>
#   - API endpoint: api.<cluster>.<domain>
#   - Internal API: api-int.<cluster>.<domain>
# Retry strategy:
#   - Outer loop: Up to 20 attempts
#   - Wildcard: Up to 60 attempts with 5s intervals
#   - API endpoints: Up to 10 attempts each with 5s intervals
#   - Between outer attempts: 15s delay
# Exits: If DNS entries don't propagate within timeout
################################################################################
function wait_for_all_dns_entries() {
	log_info "Waiting for DNS entries to propagate..."

	local max_outer_attempts=20
	local outer_attempt=0

	while ((outer_attempt < max_outer_attempts)); do
		((outer_attempt+=1))
		local all_found=true

		# Check wildcard DNS entry (*.apps.<cluster>.<domain>)
		log_info "Checking wildcard DNS entry (attempt ${outer_attempt}/${max_outer_attempts})..."
		local wildcard_found=false

		for ((tries = 0; tries <= 60; tries++)); do
			local dns_name="a${tries}.apps.${CLUSTER_NAME}.${BASEDOMAIN}"

			if getent ahostsv4 "${dns_name}" >/dev/null 2>&1; then
				log_success "Wildcard DNS resolved: ${dns_name}"
				wildcard_found=true
				break
			fi

			sleep 5
		done

		if [[ "${wildcard_found}" != "true" ]]; then
			all_found=false
			log_warning "Wildcard DNS not yet available, retrying..."
			sleep 15
			continue
		fi

		# Check api and api-int DNS entries
		for prefix in api api-int; do
			local dns_name="${prefix}.${CLUSTER_NAME}.${BASEDOMAIN}"

			if ! wait_for_dns "${dns_name}" 10 5; then
				log_warning "DNS entry not yet available: ${dns_name}"
				all_found=false
			fi
		done

		if [[ "${all_found}" == "true" ]]; then
			log_success "All DNS entries are resolvable"
			break
		fi

		sleep 15
	done

	if [[ "${all_found}" != "true" ]]; then
		die "DNS entries did not become available within timeout"
	fi
}

################################################################################
# verify_vip_matches_dns: Verify bastion VIP matches DNS resolution
# Ensures that the DNS entry for api.<cluster>.<domain> resolves to the
# bastion's VIP address. This confirms DNS is properly configured.
# Retry strategy: Up to 60 attempts with 15s intervals (15 minutes total)
# Exits: If VIP doesn't match DNS after all attempts
################################################################################
function verify_vip_matches_dns() {
	log_info "Verifying VIP matches DNS..."

	local max_attempts=60
	local dns_name="api.${CLUSTER_NAME}.${BASEDOMAIN}"

	for ((attempt = 1; attempt <= max_attempts; attempt++)); do
		local resolved_ip
		resolved_ip=$(getent ahostsv4 "${dns_name}" 2>/dev/null | grep STREAM | cut -f1 -d' ' | head -n1)

		log_info "Attempt ${attempt}/${max_attempts}: DNS=${resolved_ip}, VIP=${VIP_API}"

		if [[ "${resolved_ip}" == "${VIP_API}" ]]; then
			log_success "VIP matches DNS resolution"
			return 0
		fi

		if [[ ${attempt} -lt ${max_attempts} ]]; then
			log_warning "VIP mismatch, retrying in 15s..."
			sleep 15
		fi
	done

	die "VIP (${VIP_API}) does not match DNS resolution after ${max_attempts} attempts"
}

################################################################################
# create_install_config: Generate OpenShift install-config.yaml
# Creates the installation configuration file with:
#   - Cluster metadata (name, domain)
#   - Network configuration (CIDR ranges)
#   - Platform-specific settings (PowerVC)
#   - Node configuration (replicas, zones)
#   - Authentication (pull secret, SSH key)
# Masks sensitive data (pull secret, SSH key) in console output
# Creates: ${CLUSTER_DIR}/install-config.yaml
################################################################################
function create_install_config() {
	log_info "Creating install-config.yaml..."

	local pull_secret=$(echo "${PULL_SECRET}" | sed "s/'/'\\\\''/g")
	local ssh_key=$(echo "${SSH_KEY}" | sed 's/^/  /')

	cat > "${CLUSTER_DIR}/install-config.yaml" <<EOF
apiVersion: v1
baseDomain: ${BASEDOMAIN}
compute:
- architecture: ppc64le
  hyperthreading: Enabled
  name: worker
  platform:
    powervc:
      zones:
        - ${MACHINE_TYPE}
  replicas: 3
controlPlane:
  architecture: ppc64le
  hyperthreading: Enabled
  name: master
  platform:
    powervc:
      zones:
        - ${MACHINE_TYPE}
  replicas: 3
metadata:
  creationTimestamp: null
  name: ${CLUSTER_NAME}
networking:
  clusterNetwork:
  - cidr: 10.116.0.0/14
    hostPrefix: 23
  machineNetwork:
  - cidr: ${MACHINE_NETWORK}
  networkType: OVNKubernetes
  serviceNetwork:
  - 172.30.0.0/16
platform:
  powervc:
    loadBalancer:
      type: UserManaged
    apiVIPs:
    - ${VIP_API}
    cloud: ${CLOUD}
    clusterOSImage: ${RHCOS_FILENAME}
    defaultMachinePlatform:
      type: ${FLAVOR_NAME}
    ingressVIPs:
    - ${VIP_INGRESS}
    controlPlanePort:
      fixedIPs:
        - subnet:
            id: ${SUBNET_ID}
credentialsMode: Passthrough
pullSecret: '${pull_secret}'
sshKey: |
${ssh_key}
EOF

# Example:
#featureSet: CustomNoUpgrade
#featureGates:
#   - ClusterAPIInstall=true

	log_info "Install configuration:"
	echo "8<-----8<-----8<-----8<-----8<-----8<-----8<-----8<-----8<-----8<-----"
	sed \
		-e '/^pullSecret: |/{n; d;}' \
		-e 's/^pullSecret: '\''.*$/pullSecret: /' \
		-e 's/^sshKey: '\''.*$/sshKey: /' \
		"${CLUSTER_DIR}/install-config.yaml" |
		awk '
/^sshKey:/ { print "sshKey: "; skip=1; next }
skip && /^  / { next }
skip { skip=0 }
{ print }
'
	echo "8<-----8<-----8<-----8<-----8<-----8<-----8<-----8<-----8<-----8<-----"

	log_success "install-config.yaml created"
}

################################################################################
# run_openshift_install: Execute OpenShift installation workflow
# Orchestrates the complete OpenShift installation process:
#   1. Display installer version
#   2. Generate ignition configs
#   3. Send metadata to PowerVC controller
#   4. Create manifests
#   5. Verify controller health
#   6. Deploy cluster (30-45 minutes)
# Handles failures with automatic cleanup and monitoring
# Creates: Multiple files in ${CLUSTER_DIR} including auth/kubeconfig
################################################################################
function run_openshift_install() {
	log_info "Running openshift-install commands..."

	# Show installer version for troubleshooting
	execute_with_check "OpenShift installer version check" \
		openshift-install version

	# Create ignition configs from install-config.yaml
	execute_with_check "Create ignition configs" \
		openshift-install create ignition-configs --dir="${CLUSTER_DIR}"

	# Extract and display infrastructure ID
	if [[ ! -f "${CLUSTER_DIR}/metadata.json" ]]; then
		die "Expected metadata.json not found: ${CLUSTER_DIR}/metadata.json"
	fi

	local infra_id
	infra_id=$(jq -r .infraID "${CLUSTER_DIR}/metadata.json")
	log_info "Infrastructure ID: ${infra_id}"

	# Send metadata to PowerVC controller for tracking
	execute_with_check "Send metadata to controller" \
		"${POWERVC_TOOL}" \
		send-metadata \
		--createMetadata "${CLUSTER_DIR}/metadata.json" \
		--serverIP "${CONTROLLER_IP}" \
		--shouldDebug true

	# Create Kubernetes manifests
	log_info "Creating manifests..."
	if ! openshift-install create manifests --dir="${CLUSTER_DIR}"; then
		log_error "Failed to create manifests"
		cleanup_metadata
		exit 1
	fi
	log_success "Manifests created"

	# Verify controller is still responsive before long-running operation
	execute_with_check "Controller health check" \
		"${POWERVC_TOOL}" \
		check-alive \
		--serverIP "${CONTROLLER_IP}" \
		--shouldDebug true

	# Deploy the OpenShift cluster (this is the longest operation)
	log_info "Creating OpenShift cluster (this may take 30-45 minutes)..."
	if ! openshift-install create cluster --dir="${CLUSTER_DIR}" --log-level=debug; then
		log_error "Cluster creation failed"
		handle_cluster_creation_failure
		exit 1
	fi

	log_success "Cluster created successfully!"
}

################################################################################
# handle_cluster_creation_failure: Recovery workflow for failed installations
# Performs cleanup and starts cluster monitoring to help diagnose issues
# Steps:
#   1. Clean up metadata from controller
#   2. Collect bastion credentials if not already set
#   3. Start watch-create tool for cluster monitoring
# This helps identify what went wrong during installation
################################################################################
function handle_cluster_creation_failure() {
	log_warning "Handling cluster creation failure..."

	# Get bastion credentials if not already set
	if [[ ! -v BASTION_USERNAME ]]; then
		prompt_input "What is the username for the bastion" "BASTION_USERNAME" "cloud-user"
	fi

	if [[ ! -v BASTION_RSA ]]; then
		prompt_input "Where is the ssh private key for the bastion" "BASTION_RSA"

		if [[ ! -f "${BASTION_RSA}" ]]; then
			die "SSH key file does not exist: ${BASTION_RSA}"
		fi
	fi

	# Run watch-create to monitor and diagnose the cluster state
	log_info "Starting cluster monitoring..."

	if [[ -f "${CLUSTER_DIR}/auth/kubeconfig" ]]; then
		"${POWERVC_TOOL}" \
			watch-create \
			--cloud "${CLOUD}" \
			--metadata "${CLUSTER_DIR}/metadata.json" \
			--kubeconfig "${CLUSTER_DIR}/auth/kubeconfig" \
			--bastionUsername "${BASTION_USERNAME}" \
			--bastionRsa "${BASTION_RSA}" \
			--baseDomain "${BASEDOMAIN}" \
			--shouldDebug false
	else
		"${POWERVC_TOOL}" \
			watch-create \
			--cloud "${CLOUD}" \
			--metadata "${CLUSTER_DIR}/metadata.json" \
			--bastionUsername "${BASTION_USERNAME}" \
			--bastionRsa "${BASTION_RSA}" \
			--baseDomain "${BASEDOMAIN}" \
			--shouldDebug false
	fi
}

#==============================================================================
# Main Execution
# Orchestrates the complete cluster creation workflow
#==============================================================================

################################################################################
# main: Primary entry point for cluster creation
# Executes the complete workflow in logical phases:
#   Phase 1: Initialization and validation
#   Phase 2: Environment collection and verification
#   Phase 3: Resource verification
#   Phase 4: Infrastructure creation
#   Phase 5: Cluster deployment
# Each phase must complete successfully before proceeding to the next
################################################################################
function main() {
	log_info "Starting OpenShift cluster creation script"
	log_info "Script: ${SCRIPT_NAME}"
	log_info "Working directory: $(pwd)"

	# Phase 1: Initialize and validate prerequisites
	initialize_powervc_tool
	check_required_programs

	# Phase 2: Collect and validate inputs
	collect_environment_variables
	verify_openstack_connectivity
	validate_environment_variables
	validate_cluster_dir_not_exists

	# Phase 3: Get additional information from OpenStack
	get_network_info
	get_rhcos_info

	# Phase 4: Verify resources exist and are accessible
	verify_controller
	verify_all_openstack_resources

	# Phase 5: Create infrastructure and deploy cluster
	create_bastion_host
	wait_for_all_dns_entries
	verify_vip_matches_dns

	# Phase 6: Generate configuration and install OpenShift
	create_install_config
	run_openshift_install

	# Success! Display cluster information
	log_success "Cluster creation completed successfully!"
	log_info "Cluster name: ${CLUSTER_NAME}"
	log_info "Base domain: ${BASEDOMAIN}"
	log_info "API VIP: ${VIP_API}"
	log_info "Ingress VIP: ${VIP_INGRESS}"
	log_info "Cluster directory: ${CLUSTER_DIR}"
}

# Run main function
main "$@"

# Made with Bob
