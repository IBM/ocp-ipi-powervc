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

# check-alive.sh - Monitor server availability and restart watch-installation on failure
#
# This script continuously monitors a controller server's availability and automatically
# restarts the watch-installation command in a tmux session when the server goes down.
# It's designed to provide resilient monitoring for OpenShift cluster installations.
#
# USAGE:
#   ./check-alive.sh
#
# REQUIRED ENVIRONMENT VARIABLES:
#   BASEDOMAIN          - Base domain for the cluster (e.g., example.com)
#   BASTION_USERNAME    - Username for bastion host access
#   BASTION_RSA         - Path to RSA private key for bastion authentication
#   CLOUD               - Cloud provider name (e.g., powervc)
#   CONTROLLER_IP       - IP address of the controller server to monitor
#   DHCP_DNS_SERVERS    - DNS servers for DHCP configuration (comma-separated)
#   DHCP_NETMASK        - Network mask for DHCP subnet
#   DHCP_ROUTER         - Router/gateway IP for DHCP configuration
#   DHCP_SERVER_ID      - Server ID for DHCP server
#   DHCP_SUBNET         - Subnet for DHCP configuration
#
# OPTIONAL ENVIRONMENT VARIABLES:
#   DEBUG               - Enable debug output (true/false, default: false)
#   CHECK_INTERVAL      - Seconds between health checks (default: 60)
#   TMUX_SESSION_NAME   - Name of tmux session to use (default: auto-detect)
#
# FEATURES:
#   - Validates all required environment variables before starting
#   - Checks for required programs (ocp-ipi-powervc, awk, cut, ip, tmux, tr)
#   - Continuously monitors controller server availability
#   - Automatically restarts watch-installation on server failure
#   - Logs all output with timestamps
#   - Supports graceful shutdown on SIGINT/SIGTERM
#   - Auto-detects system architecture (amd64/ppc64le)
#   - Auto-detects network interface for DHCP
#
# EXIT CODES:
#   0 - Normal exit (interrupted by signal)
#   1 - Missing required environment variable
#   2 - Missing required program
#   3 - Failed to detect tmux session
#   4 - Failed to detect network interface
#
# EXAMPLES:
#   # Basic usage with environment file
#   source environmentDevelopment && ./check-alive.sh
#
#   # With custom check interval
#   CHECK_INTERVAL=30 ./check-alive.sh
#
#   # With debug output
#   DEBUG=true ./check-alive.sh

set -uo pipefail

#
# Constants and Configuration
#

readonly SCRIPT_NAME="check-alive.sh"
readonly SCRIPT_VERSION="2.0"

# Default values
readonly DEFAULT_DEBUG="false"
readonly DEFAULT_CHECK_INTERVAL=60

# Exit codes
readonly EXIT_SUCCESS=0
readonly EXIT_MISSING_ENV_VAR=1
readonly EXIT_MISSING_PROGRAM=2
readonly EXIT_TMUX_DETECTION_FAILED=3
readonly EXIT_INTERFACE_DETECTION_FAILED=4

# Required environment variables
readonly REQUIRED_ENV_VARS=(
	"BASEDOMAIN"
	"BASTION_USERNAME"
	"BASTION_RSA"
	"CLOUD"
	"CONTROLLER_IP"
	"DHCP_DNS_SERVERS"
	"DHCP_NETMASK"
	"DHCP_ROUTER"
	"DHCP_SERVER_ID"
	"DHCP_SUBNET"
)

# Required programs
readonly REQUIRED_PROGRAMS=(
	"awk"
	"cut"
	"date"
	"ip"
	"tmux"
	"tr"
)

#
# Global Variables
#

# Set from environment or defaults
DEBUG="${DEBUG:-${DEFAULT_DEBUG}}"
CHECK_INTERVAL="${CHECK_INTERVAL:-${DEFAULT_CHECK_INTERVAL}}"

# Will be set during initialization
ARCH=""
POWERVC_TOOL=""
LINK=""
TMUX_SESSION=""
POWERVC_CMD=""

# Shutdown flag for graceful exit
SHUTDOWN_REQUESTED=false

#
# Utility Functions
#

# log_message - Print timestamped log message
# Arguments:
#   $1 - Log level (INFO, WARN, ERROR, DEBUG)
#   $2 - Message to log
log_message() {
	local level="$1"
	shift
	local message="$*"
	local timestamp
	timestamp=$(date '+%Y-%m-%d %H:%M:%S')

	if [[ "${level}" == "DEBUG" && "${DEBUG}" != "true" ]]; then
		return
	fi

	echo "[${timestamp}] [${level}] [${SCRIPT_NAME}] ${message}"
}

# log_info - Log informational message
log_info() {
	log_message "INFO" "$@"
}

# log_warn - Log warning message
log_warn() {
	log_message "WARN" "$@"
}

# log_error - Log error message
log_error() {
	log_message "ERROR" "$@"
}

# log_debug - Log debug message (only if DEBUG=true)
log_debug() {
	log_message "DEBUG" "$@"
}

# cleanup - Cleanup function called on exit
cleanup() {
	log_info "Cleanup initiated"
	SHUTDOWN_REQUESTED=true
	log_info "Script terminated gracefully"
}

# signal_handler - Handle interrupt signals
signal_handler() {
	log_warn "Received interrupt signal, shutting down..."
	cleanup
	exit ${EXIT_SUCCESS}
}

#
# Validation Functions
#

# validate_environment_variables - Check all required environment variables are set
# Returns:
#   0 - All variables are set
#   1 - One or more variables are missing
validate_environment_variables() {
	local missing_vars=()

	log_info "Validating required environment variables"

	for var in "${REQUIRED_ENV_VARS[@]}"; do
		if [[ ! -v ${var} ]]; then
			missing_vars+=("${var}")
			log_error "Required environment variable ${var} is not set"
		else
			local value
			value=$(eval "echo \"\${${var}}\"")
			if [[ -z "${value}" ]]; then
				missing_vars+=("${var}")
				log_error "Required environment variable ${var} is set but empty"
			else
				log_debug "Environment variable ${var} is set"
			fi
		fi
	done

	if [[ ${#missing_vars[@]} -gt 0 ]]; then
		log_error "Missing or empty environment variables: ${missing_vars[*]}"
		log_error "Please set all required environment variables before running this script"
		return 1
	fi

	log_info "All required environment variables are set"
	return 0
}

# validate_programs - Check all required programs are available
# Returns:
#   0 - All programs are available
#   1 - One or more programs are missing
validate_programs() {
	local missing_programs=()

	log_info "Validating required programs"

	# Add architecture-specific PowerVC tool to required programs
	local programs_to_check=("${REQUIRED_PROGRAMS[@]}" "${POWERVC_TOOL}")

	for program in "${programs_to_check[@]}"; do
		log_debug "Checking for program: ${program}"
		if ! command -v "${program}" &>/dev/null; then
			missing_programs+=("${program}")
			log_error "Required program '${program}' is not installed or not in PATH"
		else
			log_debug "Program '${program}' found: $(command -v "${program}")"
		fi
	done

	if [[ ${#missing_programs[@]} -gt 0 ]]; then
		log_error "Missing required programs: ${missing_programs[*]}"
		log_error "Please install missing programs before running this script"
		return 1
	fi

	log_info "All required programs are available"
	return 0
}

#
# Detection Functions
#

# detect_architecture - Detect system architecture and set ARCH variable
# Sets:
#   ARCH - System architecture (amd64 or ppc64le)
detect_architecture() {
	local uname_arch
	uname_arch=$(uname -m)

	log_debug "Detected uname architecture: ${uname_arch}"

	case "${uname_arch}" in
		x86_64)
			ARCH="amd64"
			;;
		ppc64le)
			ARCH="ppc64le"
			;;
		*)
			ARCH="${uname_arch}"
			log_warn "Unknown architecture '${uname_arch}', using as-is"
			;;
	esac

	log_info "System architecture: ${ARCH}"
}

# detect_powervc_tool - Set PowerVC tool name based on architecture
# Sets:
#   POWERVC_TOOL - Name of the PowerVC tool binary
detect_powervc_tool() {
	POWERVC_TOOL="ocp-ipi-powervc-linux-${ARCH}"
	log_info "PowerVC tool: ${POWERVC_TOOL}"
}

# detect_tmux_session - Detect active tmux session
# Sets:
#   TMUX_SESSION - Name of the tmux session
# Returns:
#   0 - Session detected successfully
#   1 - Failed to detect session
detect_tmux_session() {
	log_info "Detecting tmux session"

	if [[ -n "${TMUX_SESSION_NAME:-}" ]]; then
		TMUX_SESSION="${TMUX_SESSION_NAME}"
		log_info "Using specified tmux session: ${TMUX_SESSION}"
		return 0
	fi

	local sessions
	sessions=$(tmux list-sessions 2>/dev/null | cut -f1 -d':')

	if [[ -z "${sessions}" ]]; then
		log_error "No tmux sessions found"
		log_error "Please create a tmux session before running this script"
		return 1
	fi

	# Use first session if multiple exist
	TMUX_SESSION=$(echo "${sessions}" | head -n1)

	if [[ -z "${TMUX_SESSION}" ]]; then
		log_error "Failed to detect tmux session"
		return 1
	fi

	log_info "Detected tmux session: ${TMUX_SESSION}"
	return 0
}

# detect_network_interface - Detect primary network interface
# Sets:
#   LINK - Name of the network interface
# Returns:
#   0 - Interface detected successfully
#   1 - Failed to detect interface
detect_network_interface() {
	log_info "Detecting network interface"

	LINK=$(ip link | awk -F: '$0 !~ "lo|vir|^[^0-9]"{print $2;getline}' | tr -d '[:space:]')

	if [[ -z "${LINK}" ]]; then
		log_error "Failed to detect network interface"
		log_error "Please specify network interface manually"
		return 1
	fi

	log_info "Detected network interface: ${LINK}"
	return 0
}

#
# Command Building Functions
#

# build_powervc_command - Build the PowerVC watch-installation command
# Sets:
#   POWERVC_CMD - Complete command to execute in tmux
build_powervc_command() {
	log_info "Building PowerVC watch-installation command"

	local timestamp
	timestamp=$(date +%Y-%m-%d-%H-%M-%S)
	local output_file="output-${timestamp}"

	POWERVC_CMD=$(cat <<-EOF
		${POWERVC_TOOL} watch-installation \\
		  --cloud "${CLOUD}" \\
		  --domainName "${BASEDOMAIN}" \\
		  --bastionMetadata "${HOME}" \\
		  --bastionUsername "${BASTION_USERNAME}" \\
		  --bastionRsa "${BASTION_RSA}" \\
		  --enableDhcpd true \\
		  --dhcpInterface "${LINK}" \\
		  --dhcpSubnet "${DHCP_SUBNET}" \\
		  --dhcpNetmask "${DHCP_NETMASK}" \\
		  --dhcpRouter "${DHCP_ROUTER}" \\
		  --dhcpDnsServers "${DHCP_DNS_SERVERS}" \\
		  --dhcpServerId "${DHCP_SERVER_ID}" \\
		  --shouldDebug true \\
		  2>&1 | tee "${output_file}"
	EOF
	)

	log_debug "PowerVC command: ${POWERVC_CMD}"
	log_info "Output will be logged to: ${output_file}"
}

#
# Monitoring Functions
#

# check_server_alive - Check if controller server is responding
# Returns:
#   0 - Server is alive
#   1 - Server is not responding
check_server_alive() {
	log_debug "Checking if server ${CONTROLLER_IP} is alive"

	if ${POWERVC_TOOL} check-alive --serverIP "${CONTROLLER_IP}" &>/dev/null; then
		log_debug "Server ${CONTROLLER_IP} is responding"
		return 0
	else
		log_warn "Server ${CONTROLLER_IP} is not responding"
		return 1
	fi
}

# restart_watch_installation - Send watch-installation command to tmux session
restart_watch_installation() {
	log_warn "Restarting watch-installation in tmux session ${TMUX_SESSION}"

	if tmux send-keys -t "${TMUX_SESSION}:0" "${POWERVC_CMD}" C-m; then
		log_info "Successfully sent watch-installation command to tmux"
	else
		log_error "Failed to send command to tmux session"
	fi
}

# monitor_loop - Main monitoring loop
monitor_loop() {
	log_info "Starting monitoring loop (check interval: ${CHECK_INTERVAL}s)"
	log_info "Monitoring controller server: ${CONTROLLER_IP}"
	log_info "Press Ctrl+C to stop monitoring"

	local check_count=0
	local failure_count=0

	while [[ "${SHUTDOWN_REQUESTED}" == "false" ]]; do
		check_count=$((check_count + 1))
		log_info "Health check #${check_count}: Checking server ${CONTROLLER_IP}"

		if ! check_server_alive; then
			failure_count=$((failure_count + 1))
			log_error "Server is down! (failure #${failure_count})"
			restart_watch_installation
		else
			log_info "Server is alive and responding"
			if [[ ${failure_count} -gt 0 ]]; then
				log_info "Server recovered after ${failure_count} failure(s)"
				failure_count=0
			fi
		fi

		log_debug "Sleeping for ${CHECK_INTERVAL} seconds"
		sleep "${CHECK_INTERVAL}"
	done

	log_info "Monitoring loop terminated"
}

#
# Main Function
#

main() {
	log_info "=========================================="
	log_info "${SCRIPT_NAME} v${SCRIPT_VERSION} starting"
	log_info "=========================================="

	# Set up signal handlers for graceful shutdown
	trap signal_handler SIGINT SIGTERM

	# Validate environment
	if ! validate_environment_variables; then
		log_error "Environment validation failed"
		exit ${EXIT_MISSING_ENV_VAR}
	fi

	# Detect system configuration
	detect_architecture
	detect_powervc_tool

	# Validate programs
	if ! validate_programs; then
		log_error "Program validation failed"
		exit ${EXIT_MISSING_PROGRAM}
	fi

	# Detect runtime configuration
	if ! detect_tmux_session; then
		log_error "Tmux session detection failed"
		exit ${EXIT_TMUX_DETECTION_FAILED}
	fi

	if ! detect_network_interface; then
		log_error "Network interface detection failed"
		exit ${EXIT_INTERFACE_DETECTION_FAILED}
	fi

	# Build command
	build_powervc_command

	# Start monitoring
	log_info "Initialization complete, starting monitoring"
	monitor_loop

	log_info "${SCRIPT_NAME} exiting normally"
	exit ${EXIT_SUCCESS}
}

# Execute main function
main "$@"

# Made with Bob
