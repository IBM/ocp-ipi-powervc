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
readonly TEMP_BASTION_IP="/tmp/bastionIp"

# Color codes for output
readonly COLOR_RED='\033[0;31m'
readonly COLOR_GREEN='\033[0;32m'
readonly COLOR_YELLOW='\033[1;33m'
readonly COLOR_BLUE='\033[0;34m'
readonly COLOR_RESET='\033[0m'

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

# Verify OpenStack resource exists
verify_openstack_resource() {
	local resource_type="$1"
	local resource_name="$2"
	local cloud="${3:-${CLOUD}}"

	log_info "Verifying ${resource_type}: ${resource_name}"

	if ! openstack --os-cloud="${cloud}" "${resource_type}" show "${resource_name}" >/dev/null 2>&1; then
		die "Cannot find ${resource_type} '${resource_name}'. Please verify OpenStack configuration."
	fi

	log_success "Found ${resource_type}: ${resource_name}"
}

# Wait for DNS resolution with retries
wait_for_dns() {
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

# Cleanup metadata on failure
cleanup_metadata() {
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
}

# Trap to ensure cleanup on script exit
cleanup_on_exit() {
	local exit_code=$?

	if [[ ${exit_code} -ne 0 ]]; then
		log_error "Script failed with exit code ${exit_code}"
		if [[ -v CLUSTER_DIR ]] && [[ -n "${CLUSTER_DIR}" ]]; then
			cleanup_metadata
		fi
	fi
}

trap cleanup_on_exit EXIT

# Initialize architecture-specific tool name
initialize_powervc_tool() {
	local arch
	arch="$(uname -m)"

	case "${arch}" in
		x86_64)
			arch="amd64"
			;;
		ppc64le|aarch64)
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

# Check all required programs are installed
check_required_programs() {
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

# Verify OpenStack connectivity
verify_openstack_connectivity() {
	log_info "Verifying OpenStack connectivity..."

	if ! openstack --os-cloud="${CLOUD}" image list >/dev/null 2>&1; then
		die "Cannot connect to OpenStack. Please verify clouds.yaml configuration."
	fi

	log_success "OpenStack connectivity verified"
}

# Get and validate network information
get_network_info() {
	log_info "Retrieving network information for: ${NETWORK_NAME}"

	# Get subnet ID
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

	# Get machine network CIDR
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

# Get RHCOS image information
get_rhcos_info() {
	log_info "Retrieving RHCOS image information..."

	local rhcos_json
	rhcos_json=$(openshift-install coreos print-stream-json 2>/dev/null)

	if [[ -z "${rhcos_json}" ]]; then
		die "Failed to retrieve RHCOS stream JSON"
	fi

	RHCOS_URL=$(echo "${rhcos_json}" | jq -r '.architectures.ppc64le.artifacts.openstack.formats."qcow2.gz".disk.location')

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

# Verify controller connectivity
verify_controller() {
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

# Collect all required environment variables
collect_environment_variables() {
	log_info "Collecting environment variables..."

	# Cloud name
	if [[ ! -v CLOUD ]]; then
		prompt_input "What is the cloud name in ~/.config/openstack/clouds.yaml" "CLOUD"
	fi

	# Base domain
	if [[ ! -v BASEDOMAIN ]]; then
		prompt_input "What is the base domain" "BASEDOMAIN"
	fi

	# Bastion image
	if [[ ! -v BASTION_IMAGE_NAME ]]; then
		prompt_input "What is the image name to use for the bastion" "BASTION_IMAGE_NAME"
	fi

	# Bastion username
	if [[ ! -v BASTION_USERNAME ]]; then
		prompt_input "What is the username to use for the bastion" "BASTION_USERNAME" "cloud-user"
	fi

	# Cluster directory
	if [[ ! -v CLUSTER_DIR ]]; then
		prompt_input "What directory should be used for the installation" "CLUSTER_DIR" "test"

		if [[ -d "${CLUSTER_DIR}" ]]; then
			die "Directory ${CLUSTER_DIR} already exists. Please delete it or choose a different name."
		fi
	fi

	# Cluster name
	if [[ ! -v CLUSTER_NAME ]]; then
		prompt_input "What is the name of the cluster" "CLUSTER_NAME"
	fi

	# Flavor name
	if [[ ! -v FLAVOR_NAME ]]; then
		prompt_input "What is the OpenStack flavor" "FLAVOR_NAME"
	fi

	# Machine type (availability zone)
	if [[ ! -v MACHINE_TYPE ]]; then
		prompt_input "What is the OpenStack machine type" "MACHINE_TYPE"
	fi

	# Network name
	if [[ ! -v NETWORK_NAME ]]; then
		prompt_input "What is the OpenStack network" "NETWORK_NAME"
	fi

	# Pull secret
	if [[ -v PULLSECRET_FILE ]]; then
		if [[ ! -f "${PULLSECRET_FILE}" ]]; then
			log_warning "PULLSECRET_FILE (${PULLSECRET_FILE}) does not exist"
			prompt_input "What is your pull secret" "PULL_SECRET"
		else
			PULL_SECRET=$(cat "${PULLSECRET_FILE}")
			export PULL_SECRET
			log_success "Loaded pull secret from file"
		fi
	else
		if [[ ! -v PULL_SECRET ]]; then
			prompt_input "What is your pull secret" "PULL_SECRET"
		fi
	fi

	# SSH key for installer
	if [[ ! -v INSTALLER_SSHKEY ]]; then
		prompt_input "What ssh key is used by the installer" "INSTALLER_SSHKEY"
	fi

	if [[ ! -f "${INSTALLER_SSHKEY}" ]]; then
		die "SSH key file does not exist: ${INSTALLER_SSHKEY}"
	fi

	SSH_KEY=$(cat "${INSTALLER_SSHKEY}")
	readonly SSH_KEY

	# Controller IP
	if [[ ! -v CONTROLLER_IP ]]; then
		prompt_input "What is the ${POWERVC_TOOL} master controller IP" "CONTROLLER_IP"
	fi

	# SSH key name for bastion
	if [[ ! -v SSHKEY_NAME ]]; then
		prompt_input "What is the OpenStack keypair to use for the bastion" "SSHKEY_NAME"
	fi

	log_success "All environment variables collected"
}

# Validate all collected variables
validate_environment_variables() {
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

# Verify all OpenStack resources exist
verify_all_openstack_resources() {
	log_info "Verifying OpenStack resources..."

	verify_openstack_resource "image" "${BASTION_IMAGE_NAME}"
	verify_openstack_resource "flavor" "${FLAVOR_NAME}"
	verify_openstack_resource "network" "${NETWORK_NAME}"
	verify_openstack_resource "keypair" "${SSHKEY_NAME}"

	# Verify availability zone
	log_info "Verifying availability zone: ${MACHINE_TYPE}"
	if ! openstack --os-cloud="${CLOUD}" availability zone list --format csv 2>/dev/null | grep -q "\"${MACHINE_TYPE}\""; then
		die "Cannot find availability zone: ${MACHINE_TYPE}"
	fi
	log_success "Found availability zone: ${MACHINE_TYPE}"

	# Verify RHCOS image
	verify_openstack_resource "image" "${RHCOS_FILENAME}"

	log_success "All OpenStack resources verified"
}

# Create bastion host
create_bastion_host() {
	log_info "Creating bastion host..."

	# Remove previous bastion IP file
	[[ -f "${TEMP_BASTION_IP}" ]] && rm -f "${TEMP_BASTION_IP}"

	# Create cluster directory
	mkdir -p "${CLUSTER_DIR}"

	execute_with_check "Bastion creation" \
		"${POWERVC_TOOL}" \
		create-bastion \
		--cloud "${CLOUD}" \
		--bastionName "${CLUSTER_NAME}" \
		--flavorName "${FLAVOR_NAME}" \
		--imageName "${BASTION_IMAGE_NAME}" \
		--networkName "${NETWORK_NAME}" \
		--sshKeyName "${SSHKEY_NAME}" \
		--domainName "${BASEDOMAIN}" \
		--enableHAProxy true \
		--serverIP "${CONTROLLER_IP}" \
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

# Wait for all DNS entries to be resolvable
wait_for_all_dns_entries() {
	log_info "Waiting for DNS entries to propagate..."

	local max_outer_attempts=20
	local outer_attempt=0

	while ((outer_attempt < max_outer_attempts)); do
		((outer_attempt+=1))
		local all_found=true

		# Check wildcard DNS entry
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

		if ! ${wildcard_found}; then
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

		if ${all_found}; then
			log_success "All DNS entries are resolvable"
			break
		fi

		sleep 15
	done

	if ! ${all_found}; then
		die "DNS entries did not become available within timeout"
	fi
}

# Verify VIP matches DNS
verify_vip_matches_dns() {
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

# Create install-config.yaml
create_install_config() {
	log_info "Creating install-config.yaml..."

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
pullSecret: '${PULL_SECRET}'
sshKey: |
  ${SSH_KEY}
EOF

	log_info "Install configuration:"
	echo "8<-----8<-----8<-----8<-----8<-----8<-----8<-----8<-----8<-----8<-----"
	cat "${CLUSTER_DIR}/install-config.yaml"
	echo "8<-----8<-----8<-----8<-----8<-----8<-----8<-----8<-----8<-----8<-----"

	log_success "install-config.yaml created"
}

# Run openshift-install commands
run_openshift_install() {
	log_info "Running openshift-install commands..."

	# Show version
	execute_with_check "OpenShift installer version check" \
		openshift-install version

	# Create install config
	execute_with_check "Create install config" \
		openshift-install create install-config --dir="${CLUSTER_DIR}"

	# Create ignition configs
	execute_with_check "Create ignition configs" \
		openshift-install create ignition-configs --dir="${CLUSTER_DIR}"

	# Extract and display infraID
	if [[ ! -f "${CLUSTER_DIR}/metadata.json" ]]; then
		die "Expected metadata.json not found: ${CLUSTER_DIR}/metadata.json"
	fi

	local infra_id
	infra_id=$(jq -r .infraID "${CLUSTER_DIR}/metadata.json")
	log_info "Infrastructure ID: ${infra_id}"

	# Send metadata to controller
	execute_with_check "Send metadata to controller" \
		"${POWERVC_TOOL}" \
		send-metadata \
		--createMetadata "${CLUSTER_DIR}/metadata.json" \
		--serverIP "${CONTROLLER_IP}" \
		--shouldDebug true

	# Create manifests
	log_info "Creating manifests..."
	if ! openshift-install create manifests --dir="${CLUSTER_DIR}"; then
		log_error "Failed to create manifests"
		cleanup_metadata
		exit 1
	fi
	log_success "Manifests created"

	# Verify controller is still alive
	execute_with_check "Controller health check" \
		"${POWERVC_TOOL}" \
		check-alive \
		--serverIP "${CONTROLLER_IP}" \
		--shouldDebug true

	# Create cluster
	log_info "Creating OpenShift cluster (this may take 30-45 minutes)..."
	if ! openshift-install create cluster --dir="${CLUSTER_DIR}" --log-level=debug; then
		log_error "Cluster creation failed"
		handle_cluster_creation_failure
		exit 1
	fi

	log_success "Cluster created successfully!"
}

# Handle cluster creation failure
handle_cluster_creation_failure() {
	log_warning "Handling cluster creation failure..."

	cleanup_metadata

	# Get bastion credentials if not set
	if [[ ! -v BASTION_USERNAME ]]; then
		prompt_input "What is the username for the bastion" "BASTION_USERNAME" "cloud-user"
	fi

	if [[ ! -v BASTION_RSA ]]; then
		prompt_input "Where is the ssh private key for the bastion" "BASTION_RSA"

		if [[ ! -f "${BASTION_RSA}" ]]; then
			die "SSH key file does not exist: ${BASTION_RSA}"
		fi
	fi

	# Run watch-create to monitor the cluster
	log_info "Starting cluster monitoring..."
	"${POWERVC_TOOL}" \
		watch-create \
		--cloud "${CLOUD}" \
		--metadata "${CLUSTER_DIR}/metadata.json" \
		--kubeconfig "${CLUSTER_DIR}/auth/kubeconfig" \
		--bastionUsername "${BASTION_USERNAME}" \
		--bastionRsa "${BASTION_RSA}" \
		--baseDomain "${BASEDOMAIN}" \
		--shouldDebug false
}

#==============================================================================
# Main Execution
#==============================================================================

main() {
	log_info "Starting OpenShift cluster creation script"
	log_info "Script: ${SCRIPT_NAME}"
	log_info "Working directory: $(pwd)"

	# Initialize
	initialize_powervc_tool
	check_required_programs

	# Collect and validate inputs
	collect_environment_variables
	verify_openstack_connectivity
	validate_environment_variables

	# Get additional information
	get_network_info
	get_rhcos_info

	# Verify resources
	verify_controller
	verify_all_openstack_resources

	# Create infrastructure
	create_bastion_host
	wait_for_all_dns_entries
	verify_vip_matches_dns

	# Create cluster
	create_install_config
	run_openshift_install

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
