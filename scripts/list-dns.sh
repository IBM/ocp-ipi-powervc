#!/usr/bin/env bash

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
# Script: list-dns.sh
# Description: List DNS records from an IBM Cloud CIS instance.
#              Interactively selects the CIS instance and domain, then
#              paginates through all DNS records, optionally filtering by
#              a regex pattern supplied via --match.
#
# Usage: ./list-dns.sh [OPTIONS]
#
# Options:
#   -h, --help             Show this help and exit
#   -m, --match <pattern>  Filter DNS records by name (regex)
#   -r, --region <region>  IBM Cloud region to target (default: us-south)
#
# Environment Variables:
#   IBMCLOUD_API_KEY  (required) IBM Cloud API key used for authentication
#   BASEDOMAIN        (required) DNS base domain; used to auto-select the
#                                matching CIS domain when only one matches
#   DEBUG             (optional) Set to 'true' to enable debug output
#
# Prerequisites:
#   ibmcloud  IBM Cloud CLI with the CIS plugin installed
#   jq        Command-line JSON processor (https://stedolan.github.io/jq/)
#
# Exit Codes:
#   0 - Success
#   1 - Error (missing dependencies, invalid configuration, operation failure)
#==============================================================================

set -euo pipefail

#==============================================================================
# Global Variables
#==============================================================================
readonly SCRIPT_NAME="$(basename "${BASH_SOURCE[0]}")"

# DNS record name filter pattern (set via --match; empty means show all)
MATCH=""

# IBM Cloud region to target (set via --region; defaults to us-south)
REGION="us-south"

# Temporary file for storing JSON data
readonly TEMP_JSON="$(mktemp)"

# ANSI color codes
readonly COLOR_RED='\033[0;31m'      # Error messages
readonly COLOR_GREEN='\033[0;32m'    # Success messages
readonly COLOR_YELLOW='\033[1;33m'   # Warning messages
readonly COLOR_BLUE='\033[0;34m'     # Info messages
readonly COLOR_CYAN='\033[0;36m'     # Debug messages
readonly COLOR_RESET='\033[0m'       # Reset to default

# Enable debug output when DEBUG=true (default: false)
: "${DEBUG:=false}"

#==============================================================================
# Utility Functions
#==============================================================================

function log_debug()   { [[ "${DEBUG}" == "true" ]] && echo -e "${COLOR_CYAN}[DEBUG]${COLOR_RESET} $*" || true; }
function log_info()    { echo -e "${COLOR_BLUE}[INFO]${COLOR_RESET} $*"; }
function log_success() { echo -e "${COLOR_GREEN}[SUCCESS]${COLOR_RESET} $*"; }
function log_warning() { echo -e "${COLOR_YELLOW}[WARNING]${COLOR_RESET} $*"; }
function log_error()   { echo -e "${COLOR_RED}[ERROR]${COLOR_RESET} $*" >&2; }
function die()         { log_error "$*"; exit 1; }

#==============================================================================
# Return 0 if the given command is available in PATH, 1 otherwise.
# $1 - command name to test
#==============================================================================
function command_exists() {
	command -v "$1" >/dev/null 2>&1
}

################################################################################
# is_var_set: Check if an environment variable is set and not empty
# Parameters:
#   $1 - Variable name to check
# Returns: 0 (true) if variable is set and non-empty, 1 (false) otherwise
# Usage: if is_var_set "CLUSTER_NAME"; then ...; fi
################################################################################
function is_var_set() {
	local var_name="$1"
	local var_value="${!var_name:-}"

	[[ -n "${var_value}" ]]
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

	if ! is_var_set "${var_name}"; then
		die "${var_name} must be set and non-empty"
	fi
}

################################################################################
# check_required_programs: Verify all required programs are installed
# Checks for: ibmcloud, jq
# Exits: With error listing every missing program if any are absent
################################################################################
function check_required_programs() {
	local -a required_programs=("ibmcloud" "jq")
	local -a missing_programs=()

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
# Prompt the user for a value, storing it in the named variable.
# $1 - prompt text; $2 - variable name
################################################################################
function prompt_input() {
	local prompt_text="$1"
	local var_name="$2"

	local input_value
	read -rp "${prompt_text} []: " input_value

	if [[ -z "${input_value}" ]]; then
		die "You must enter a value for ${var_name}"
	fi

	printf -v "${var_name}" '%s' "${input_value}"
	export "${var_name}"
}

################################################################################
# collect_environment_variables: Gather all required configuration
# Prompts the user for any missing environment variables.
# Currently collects: BASEDOMAIN
################################################################################
function collect_environment_variables() {
	log_info "Collecting environment variables..."

	collect_basedomain
}

################################################################################
# Collect the DNS base domain name
################################################################################
function collect_basedomain() {
	log_info "Collecting DNS base domain name..."

	if [[ ! -v BASEDOMAIN ]] || [[ -z "${BASEDOMAIN}" ]]; then
		prompt_input "What is the DNS base domain name" "BASEDOMAIN"
	fi

	export BASEDOMAIN
	log_success "Base domain name: ${BASEDOMAIN}"
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
		"IBMCLOUD_API_KEY"
	)

	for var in "${required_vars[@]}"; do
		validate_non_empty "${var}"
	done

	log_success "All environment variables validated"
}

################################################################################
# Print usage information to stderr and exit 0.
################################################################################
function usage() {
	echo "Usage: ${SCRIPT_NAME} [OPTIONS]" >&2
	echo ""                                >&2
	echo "  -h, --help             Show this help and exit"              >&2
	echo "  -m, --match <pattern>  Filter DNS records by name pattern"   >&2
	echo "  -r, --region <region>  IBM Cloud region to target (default: us-south)" >&2
	exit 0
}

################################################################################
# parse_arguments: Process command-line arguments
# Called before any setup so -h/--help exits immediately without side-effects.
# $@ - all script arguments
# Exits: 0 on -h/--help; 1 on unknown option
################################################################################
function parse_arguments() {
	while [[ $# -gt 0 ]]; do
		case "$1" in
			-h|--help)
				usage ;;
			-m|--match)
				[[ $# -lt 2 ]] && { echo "Error: --match requires a value." >&2; exit 1; }
				MATCH="$2"
				shift 2 ;;
			-r|--region)
				[[ $# -lt 2 ]] && { echo "Error: --region requires a value." >&2; exit 1; }
				REGION="$2"
				shift 2 ;;
			*)
				echo "Error: unknown option $1." >&2; exit 1 ;;
		esac
	done
}

################################################################################
# cleanup_on_exit: Trap handler for script exit
# Registered via: trap cleanup_on_exit EXIT
# Cleans up:
#   - Temporary JSON file
# Logs a failure message when the script exits non-zero
################################################################################
function cleanup_on_exit() {
	local exit_code=$?

	# Always clean up temporary files
	[[ -f "${TEMP_JSON}" ]] && /bin/rm -f "${TEMP_JSON}"

	if [[ ${exit_code} -ne 0 ]]; then
		log_error "Script failed with exit code ${exit_code}"
	fi
}

################################################################################
# ibmcloud_login: Authenticate to IBM Cloud using an API key
# Targets the region specified by REGION and the public cloud.ibm.com endpoint.
# Requires: IBMCLOUD_API_KEY environment variable
################################################################################
function ibmcloud_login() {
	if ! ibmcloud login -a "https://cloud.ibm.com" --apikey "${IBMCLOUD_API_KEY}" -r "${REGION}" > "${TEMP_JSON}" 2>&1; then
		log_error "$(cat "${TEMP_JSON}")"
		return 1
	fi

	log_info "Logged into IBMCloud"
}

################################################################################
# ibmcloud_logout: Log out from the current IBM Cloud session
# Should be called before ibmcloud_login to clear any stale credentials.
################################################################################
function ibmcloud_logout() {
	if ! ibmcloud logout > "${TEMP_JSON}" 2>&1; then
		log_error "$(cat "${TEMP_JSON}")"
		return 1
	fi

	log_info "Logged out of IBMCloud"
}

################################################################################
# ibmcloud_cis_instances: Fetch all CIS service instances and populate globals
# Writes raw JSON to TEMP_JSON then extracts CRN and name for each instance.
# Sets globals:
#   CIS_INSTANCE_CRNS  - indexed array of CRN strings
#   CIS_INSTANCE_NAMES - indexed array of instance name strings (same order)
################################################################################
function ibmcloud_cis_instances() {
	if ! ibmcloud cis instances --output json > "${TEMP_JSON}" > "${TEMP_JSON}" 2>&1; then
		log_error "$(cat "${TEMP_JSON}")"
		return 1
	fi

	declare -g -a CIS_INSTANCE_CRNS=()
	declare -g -a CIS_INSTANCE_NAMES=()
	while IFS=' ' read -r crn name; do
		CIS_INSTANCE_CRNS+=("${crn}")
		CIS_INSTANCE_NAMES+=("${name}")
	done < <(jq -r '.[] | "\(.crn) \(.name)"' "${TEMP_JSON}")

	log_debug "CIS_INSTANCE_CRNS=${CIS_INSTANCE_CRNS[*]}"
	log_debug "CIS_INSTANCE_NAMES=${CIS_INSTANCE_NAMES[*]}"

	log_info "Found CIS instances"
}

################################################################################
# ibmcloud_cis_instance_set: Select one CIS instance and activate it
# If exactly one instance exists it is chosen automatically; otherwise the user
# is prompted to pick from a numbered list.
# Sets global:
#   CIS_CRN - the CRN of the selected instance
# Calls: ibmcloud cis instance-set to activate the chosen instance
# Requires: ibmcloud_cis_instances must have been called first
################################################################################
function ibmcloud_cis_instance_set() {
	local crn
	local choice
	local count="${#CIS_INSTANCE_NAMES[@]}"

	if [[ ${count} -eq 0 ]]; then
		die "No CIS instances found"
	elif [[ ${count} -eq 1 ]]; then
		crn="${CIS_INSTANCE_CRNS[0]}"
		log_info "Using CIS instance: ${CIS_INSTANCE_NAMES[0]}"
	else
		echo ""
		log_info "Multiple CIS instances found. Please select one:"
		local i
		for (( i=0; i<count; i++ )); do
			printf "  %d) %s\n" $(( i + 1 )) "${CIS_INSTANCE_NAMES[i]}"
		done

		while true; do
			read -rp "Enter selection [1-${count}]: " choice
			if [[ "${choice}" =~ ^[0-9]+$ ]] && \
			   [[ "${choice}" -ge 1 ]] && \
			   [[ "${choice}" -le "${count}" ]]; then
				break
			fi
			log_warning "Invalid selection. Please enter a number between 1 and ${count}."
		done

		crn="${CIS_INSTANCE_CRNS[$(( choice - 1 ))]}"
		log_info "Selected CIS instance: ${CIS_INSTANCE_NAMES[$(( choice - 1 ))]}"
	fi

	declare -g CIS_CRN="${crn}"

	if !  ibmcloud cis instance-set "${crn}" > "${TEMP_JSON}" > "${TEMP_JSON}" 2>&1; then
		log_error "$(cat "${TEMP_JSON}")"
		return 1
	fi

	log_info "Set CIS instance to ${crn}"
}

################################################################################
# ibmcloud_cis_domains: Fetch active CIS domains for the selected instance
# Writes raw JSON to TEMP_JSON then filters for status == "active".
# Sets globals:
#   CIS_DOMAIN_IDS   - indexed array of domain ID strings
#   CIS_DOMAIN_NAMES - indexed array of domain name strings (same order)
# Requires: ibmcloud_cis_instance_set must have been called first
################################################################################
function ibmcloud_cis_domains() {
	ibmcloud cis domains --output json > "${TEMP_JSON}"

	declare -g -a CIS_DOMAIN_IDS=()
	declare -g -a CIS_DOMAIN_NAMES=()
	while IFS=' ' read -r id name; do
		CIS_DOMAIN_IDS+=("${id}")
		CIS_DOMAIN_NAMES+=("${name}")
	done < <(jq -r '.[] | select(.status == "active") | "\(.id) \(.name)"' "${TEMP_JSON}")

	log_debug "CIS_DOMAIN_IDS=${CIS_DOMAIN_IDS[*]}"
	log_debug "CIS_DOMAIN_NAMES=${CIS_DOMAIN_NAMES[*]}"
}

################################################################################
# ibmcloud_cis_domain: Select one domain from the CIS_DOMAIN_* globals
# If BASEDOMAIN matches exactly one active domain it is chosen automatically.
# If exactly one domain exists it is also chosen automatically.
# Otherwise the user is prompted to pick from a numbered list.
# Sets globals:
#   CIS_DOMAIN - the domain ID of the selected entry
#   BASEDOMAIN  - updated to the exact domain name actually used (only when it
#                 differs from the value supplied by the caller)
# Requires: ibmcloud_cis_domains must have been called first
################################################################################
function ibmcloud_cis_domain() {
	local id
	local name
	local choice
	local count="${#CIS_DOMAIN_NAMES[@]}"

	if [[ ${count} -eq 0 ]]; then
		die "No active CIS domains found"
	fi

	# Try to auto-select by BASEDOMAIN
	local matched_id=""
	local matched_name=""
	local i
	for (( i=0; i<count; i++ )); do
		if [[ "${CIS_DOMAIN_NAMES[i]}" == *"${BASEDOMAIN}"* ]]; then
			if [[ -n "${matched_id}" ]]; then
				# More than one domain matches — fall through to interactive selection
				matched_id=""
				matched_name=""
				break
			fi
			matched_id="${CIS_DOMAIN_IDS[i]}"
			matched_name="${CIS_DOMAIN_NAMES[i]}"
		fi
	done

	if [[ -n "${matched_id}" ]]; then
		id="${matched_id}"
		name="${matched_name}"
		log_info "Auto-selected CIS domain matching '${BASEDOMAIN}': ${matched_name}"
	elif [[ ${count} -eq 1 ]]; then
		id="${CIS_DOMAIN_IDS[0]}"
		name="${CIS_DOMAIN_NAMES[0]}"
		log_info "Using CIS domain: ${CIS_DOMAIN_NAMES[0]}"
	else
		echo ""
		log_info "Multiple CIS domains found. Please select one:"
		for (( i=0; i<count; i++ )); do
			printf "  %d) %s\n" $(( i + 1 )) "${CIS_DOMAIN_NAMES[i]}"
		done

		while true; do
			read -rp "Enter selection [1-${count}]: " choice
			if [[ "${choice}" =~ ^[0-9]+$ ]] && \
			   [[ "${choice}" -ge 1 ]] && \
			   [[ "${choice}" -le "${count}" ]]; then
				break
			fi
			log_warning "Invalid selection. Please enter a number between 1 and ${count}."
		done

		id="${CIS_DOMAIN_IDS[$(( choice - 1 ))]}"
		name="${CIS_DOMAIN_NAMES[$(( choice - 1 ))]}"
		log_info "Selected CIS domain: ${CIS_DOMAIN_NAMES[$(( choice - 1 ))]}"
	fi

	declare -g CIS_DOMAIN="${id}"

	# Update BASEDOMAIN to the exact domain name that was selected
	if [[ "${name}" != "${BASEDOMAIN}" ]]; then
		log_info "BASEDOMAIN updated from '${BASEDOMAIN}' to '${name}'"
		BASEDOMAIN="${name}"
		export BASEDOMAIN
	fi
}

################################################################################
# ibmcloud_cis_dns_records: List DNS records for the selected domain
# Paginates through all records and prints those whose name matches MATCH.
# If MATCH is empty, all records are printed.
# Output format: <name> <uuid>
# Requires: ibmcloud_cis_domain must have been called first (CIS_DOMAIN set)
################################################################################
function ibmcloud_cis_dns_records() {
	local page=1
	local total=0

	log_debug "MATCH=${MATCH}"
	log_info "Fetching DNS records for domain ${CIS_DOMAIN}${MATCH:+ matching '${MATCH}'}..."

	while true; do
		ibmcloud cis dns-records "${CIS_DOMAIN}" --page "${page}" --output json > "${TEMP_JSON}"

		local count
		count=$(jq -r 'length' < "${TEMP_JSON}")

		if (( count == 0 )); then
			break
		fi

		local jq_filter
		if [[ -n "${MATCH}" ]]; then
			jq_filter='.[] | select(.name | test("'"${MATCH}"'")) | "\(.id) \(.name)"'
		else
			jq_filter='.[] | "\(.id) \(.name)"'
		fi

		while read -r uuid name; do
			echo "${name} ${uuid}"
			(( total++ )) || true
		done < <(jq -r "${jq_filter}" < "${TEMP_JSON}")

		page=$(( page + 1 ))
	done

	log_info "Total records listed: ${total}"
}

#==============================================================================
# Function: main
# Description: Main entry point - orchestrates the entire script workflow
# Arguments:
#   $@ - All command-line arguments
# Returns:
#   0 - Success
#   1 - Error (missing programs, bad input, or ibmcloud failure)
# Workflow:
#   1. Parse CLI arguments
#   2. Collect and validate environment variables (BASEDOMAIN, IBMCLOUD_API_KEY)
#   3. Verify required programs (ibmcloud, jq)
#   4. Log out then log in to IBM Cloud
#   5. List CIS instances and prompt user to select one
#   6. List active CIS domains and auto-select or prompt user to select one
#   7. Fetch and print DNS records, filtered by --match if provided
#==============================================================================
function main() {
	# Step 1: Parse CLI args, prompt for any missing env vars, then validate all
	parse_arguments "$@"

	# Step 2: Collect and validate environment variables
	collect_environment_variables
	validate_environment_variables

	# Step 3: Verify required tools are installed
	check_required_programs

	# Step 4: Authenticate to IBM Cloud
	ibmcloud_logout
	ibmcloud_login

	# Step 5: Select CIS instance
	ibmcloud_cis_instances
	ibmcloud_cis_instance_set

	# Step 6: Select active CIS domain (auto-selects when BASEDOMAIN matches)
	ibmcloud_cis_domains
	ibmcloud_cis_domain

	# Step 7: Fetch and print DNS records
	ibmcloud_cis_dns_records
}

#==============================================================================
# Script Initialization and Cleanup
#==============================================================================
trap cleanup_on_exit EXIT

#==============================================================================
# Script Entry Point
#==============================================================================
main "$@"
