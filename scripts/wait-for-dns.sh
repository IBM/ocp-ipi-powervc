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

################################################################################
# Script: wait-for-dns.sh
# Description: Waits for DNS entries to propagate for an OpenShift cluster
#
# This script polls DNS servers to verify that all required DNS entries for an
# OpenShift cluster are resolvable before proceeding with installation. It checks:
#   - Wildcard DNS entries (*.apps.<cluster>.<domain>)
#   - API endpoint (api.<cluster>.<domain>)
#   - Internal API endpoint (api-int.<cluster>.<domain>)
#
# The script will continuously retry until all DNS entries are found or until
# manual intervention is required.
#
# Environment Variables:
#   CLUSTER_DIR  - Directory containing cluster metadata (default: prompts user)
#   BASEDOMAIN   - Base domain for the cluster (default: prompts user)
#   DEBUG        - Enable debug output (default: false)
#
# Required Files:
#   ${CLUSTER_DIR}/metadata.json - Cluster metadata containing clusterName
#
# Dependencies:
#   - jq: JSON processor for parsing metadata
#   - getent: Query name service databases for DNS resolution
#
# Exit Codes:
#   0 - All DNS entries successfully resolved
#   1 - Missing required programs, invalid input, or file errors
#
# Usage Examples:
#   # Interactive mode (prompts for inputs)
#   ./wait-for-dns.sh
#
#   # With environment variables
#   CLUSTER_DIR=test BASEDOMAIN=example.com ./wait-for-dns.sh
#
#   # With debug output
#   DEBUG=true CLUSTER_DIR=test BASEDOMAIN=example.com ./wait-for-dns.sh
#
# Notes:
#   - The script uses fixed-interval polling with sleep intervals
#   - Wildcard DNS is tested up to 60 times with 5-second intervals
#   - API endpoints are tested up to 10 times with 5-second intervals
#   - If any DNS entry fails, the script waits 15 seconds before retrying all
################################################################################

set -euo pipefail

################################################################################
# Initialize DEBUG flag
# Sets DEBUG to false if not already defined in the environment
################################################################################
if [[ ! -v DEBUG ]]
then
	DEBUG=false
fi

################################################################################
# Verify Required Programs
# Checks that all necessary command-line tools are installed and available
# Required programs:
#   - jq: For parsing JSON metadata files
#   - getent: For DNS resolution queries
################################################################################
declare -a PROGRAMS
PROGRAMS=( jq getent )
for PROGRAM in ${PROGRAMS[@]}
do
	echo "Checking for program ${PROGRAM}"
	if ! hash ${PROGRAM} 1>/dev/null 2>&1
	then
		echo "Error: Missing ${PROGRAM} program!"
		exit 1
	fi
done

################################################################################
# Prompt for CLUSTER_DIR if not set
# Determines the installation directory containing cluster metadata
# Default: "test"
# Validation: Directory must exist (contains cluster metadata files)
################################################################################
if [[ ! -v CLUSTER_DIR ]]
then
	read -p "What directory should be used for the installation [test]: " CLUSTER_DIR
	if [ "${CLUSTER_DIR}" == "" ]
	then
		CLUSTER_DIR="test"
	fi
fi
export CLUSTER_DIR
if [ ! -d "${CLUSTER_DIR}" ]
then
	echo "Error: The directory ${CLUSTER_DIR} does not exist."
	exit 1
fi

################################################################################
# Prompt for BASEDOMAIN if not set
# The base domain is the parent domain for all cluster DNS entries
# Example: If BASEDOMAIN=example.com and cluster name is "mycluster",
#          DNS entries will be: api.mycluster.example.com, etc.
# Validation: Must not be empty
################################################################################
if [[ ! -v BASEDOMAIN ]]
then
	read -p "What is the base domain []: " BASEDOMAIN
fi
if [ -z "${BASEDOMAIN}" ]
then
	echo "Error: You must enter something"
	exit 1
fi
export BASEDOMAIN

################################################################################
# Extract Cluster Name from Metadata
# Reads the cluster name from the metadata.json file in CLUSTER_DIR
# This name is used to construct the full DNS entries to check
# Format: <prefix>.<cluster-name>.<base-domain>
################################################################################
if ! CLUSTER_NAME=$(jq -r .clusterName "${CLUSTER_DIR}/metadata.json"); then
	echo "Error: Failed to read clusterName from ${CLUSTER_DIR}/metadata.json"
	exit 1
fi
if [ -z "${CLUSTER_NAME}" ] || [ "${CLUSTER_NAME}" = "null" ]; then
	echo "Error: clusterName is empty?"
	exit 1
fi
[[ "${DEBUG}" == "true" ]] && echo "CLUSTER_NAME=${CLUSTER_NAME}"

################################################################################
# DNS Resolution Verification Loop
# Continuously checks for all required DNS entries until they are all resolvable
#
# This loop verifies three types of DNS entries:
#   1. Wildcard DNS (*.apps.<cluster>.<domain>) - for application routes
#   2. API endpoint (api.<cluster>.<domain>) - for cluster API access
#   3. Internal API (api-int.<cluster>.<domain>) - for internal cluster communication
#
# Retry Strategy:
#   - Wildcard DNS: Up to 60 attempts with 5-second intervals (5 minutes total)
#   - API endpoints: Up to 10 attempts each with 5-second intervals (50 seconds each)
#   - If any entry fails, wait 15 seconds and retry all entries from the beginning
#
# The loop continues indefinitely until all DNS entries are successfully resolved
################################################################################
while true
do
	################################################################################
	# Check Wildcard DNS Entry
	# Tests the wildcard DNS by attempting to resolve incrementing subdomains
	# (a0.apps..., a1.apps..., etc.) until one resolves successfully
	# This verifies that *.apps.<cluster>.<domain> is properly configured
	################################################################################
	FOUND_ALL=true
	echo "Trying up to 60 times resolving *.apps.${CLUSTER_NAME}.${BASEDOMAIN} ..."
	for (( TRIES=0; TRIES<60; TRIES++ ))
	do
		DNS="a${TRIES}.apps.${CLUSTER_NAME}.${BASEDOMAIN}"
		FOUND=false
		if getent ahostsv4 ${DNS}
		then
			echo "Found! ${DNS}"
			FOUND=true
			break
		fi
		sleep 5s
	done
	# If wildcard DNS not found after all attempts, restart the entire check
	if [[ "${FOUND}" != "true" ]]
	then
		FOUND_ALL=false
		sleep 15s
		continue
	fi
	
	################################################################################
	# Check API Endpoints
	# Verifies that both the external API (api) and internal API (api-int)
	# endpoints are resolvable. These are critical for cluster operations.
	# Each endpoint is tested up to 10 times with 5-second intervals.
	################################################################################
	for PREFIX in api api-int
	do
		DNS="${PREFIX}.${CLUSTER_NAME}.${BASEDOMAIN}"
		FOUND=false
		for ((I=0; I < 10; I++))
		do
			echo "Trying ${DNS}"
			if getent ahostsv4 ${DNS}
			then
				echo "Found! ${DNS}"
				FOUND=true
				break
			fi
			sleep 5s
		done
		# Mark as incomplete if this endpoint was not found
		if [[ "${FOUND}" != "true" ]]
		then
			FOUND_ALL=false
		fi
	done
	
	################################################################################
	# Check Completion Status
	# If all DNS entries (wildcard + both API endpoints) are found, exit the loop
	# Otherwise, wait 15 seconds and retry all checks from the beginning
	################################################################################
	echo "FOUND_ALL=${FOUND_ALL}"
	if ${FOUND_ALL}
	then
		break
	fi
	sleep 15s
done
