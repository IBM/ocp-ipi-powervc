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

# Usage: ./current-servers.sh [-c <cloud>|--cloud <cloud>] [-i|--show-ips]
#
# Lists OpenStack servers grouped by cluster and standalone VMs.
# The cloud name is taken from -c <cloud>, the CLOUD env var, or OS_CLOUD.
# Pass -i / --show-ips to include IP addresses in the output.

# Exit on errors, unset variables, and failed pipelines.
set -o nounset
set -o errexit
set -o pipefail


#######################################
# Log an informational message with a timestamp.
# Arguments:
#   Message text.
#######################################
function log_info() {
        echo "[$(date +'%Y-%m-%d %H:%M:%S')] INFO: $*"
}

#######################################
# Log an error message with a timestamp.
# Arguments:
#   Message text.
#######################################
function log_error() {
        echo "[$(date +'%Y-%m-%d %H:%M:%S')] ERROR: $*" >&2
}

#######################################
# Log a warning message with a timestamp.
# Arguments:
#   Message text.
#######################################
function log_warning() {
        echo "[$(date +'%Y-%m-%d %H:%M:%S')] WARNING: $*" >&2
}

#######################################
# Retry a command a fixed number of times with a constant delay between
# attempts.
#
# The function logs every attempt, returns immediately on the first success,
# and emits a final error after the last failure.
#
# Arguments:
#   $1 - Maximum number of attempts.
#   $2 - Delay in seconds between attempts.
#   $3 - Description of the operation for log messages.
#   $@ - Command and arguments to execute after the first three parameters.
# Returns:
#   0 if the command succeeds within the allowed attempts.
#   1 if the command fails on every attempt.
#######################################
function retry_command() {
	local max_attempts="${1}"
	local delay="${2}"
	local description="${3}"
	shift 3
	local cmd=("$@")

	local attempt=1
	while (( attempt <= max_attempts )); do
		log_info "Attempt ${attempt}/${max_attempts}: ${description}"
		if "${cmd[@]}"; then
			log_info "Success: ${description}"
			return 0
		fi

		if (( attempt < max_attempts )); then
			log_warning "Failed, retrying in ${delay}s..."
			sleep "${delay}"
		fi
		((attempt++))
	done

	log_error "Failed after ${max_attempts} attempts: ${description}"
	return 1
}

#######################################
# Resolve the latest mikefarah/yq release tag from the GitHub API.
#
# Arguments:
#   None.
# Returns:
#   0 on success; 1 if the API call fails or returns no tag.
# Outputs:
#   Prints the version tag (e.g. v4.53.6) to stdout.
#######################################
function get_latest_yq_version() {
	local tag
	tag="$(curl --fail --silent --show-error --location \
		--connect-timeout 30 --max-time 30 \
		"https://api.github.com/repos/mikefarah/yq/releases/latest" \
		| grep '"tag_name"' \
		| sed 's/.*"tag_name": *"\([^"]*\)".*/\1/')"
	if [[ -z "${tag}" ]]; then
		log_error "Could not determine latest yq version from GitHub API"
		return 1
	fi
	echo "${tag}"
}

#######################################
# Download a mikefarah/yq release asset, verify its SHA256 checksum, and install
# it at the requested path.
#
# Arguments:
#   $1 - yq release version tag (required, e.g. v4.53.6).
#   $2 - (optional) release asset name. Defaults to yq_linux_amd64.
#   $3 - (optional) destination path for the installed binary. Defaults to yq.
# Returns:
#   0 on success; 1 if a download fails, the checksum file is malformed, or
#   verification fails.
# Side Effects:
#   Creates the parent directory of the destination path if it does not exist.
#   Writes the verified binary (mode 0755) to the destination path.
#######################################
function download_yq() (
	local version="${1:?Usage: download_yq <version> [asset] [output]}"
	local asset="${2:-yq_linux_amd64}"
	local output="${3:-yq}"
	local base_url="https://github.com/mikefarah/yq/releases/download/${version}"
	local tmpdir expected actual sha256_col

	tmpdir="$(mktemp -d)"
	# Runs in a subshell (parenthesised body) so this EXIT trap is isolated:
	# it fires when the subshell exits (on any return path) and never affects
	# the parent shell's trap state.
	trap 'rm -rf -- "${tmpdir}"' EXIT

	# All downloads are wrapped in explicit retry_command checks; curl itself
	# does not use --retry.
	if ! retry_command 3 5 "Download ${asset}" \
		curl --fail --silent --show-error --location \
			--connect-timeout 30 --max-time 120 \
			--output "$tmpdir/$asset" "$base_url/$asset"; then
		log_error "Could not download ${base_url}/${asset}"
		return 1
	fi

	if ! retry_command 3 5 "Download checksums" \
		curl --fail --silent --show-error --location \
			--connect-timeout 30 --max-time 120 \
			--output "$tmpdir/checksums" "$base_url/checksums"; then
		log_error "Could not download ${base_url}/checksums"
		return 1
	fi

	if ! retry_command 3 5 "Download checksums_hashes_order" \
		curl --fail --silent --show-error --location \
			--connect-timeout 30 --max-time 120 \
			--output "$tmpdir/checksums_hashes_order" "$base_url/checksums_hashes_order"; then
		log_error "Could not download ${base_url}/checksums_hashes_order"
		return 1
	fi

	# Determine which 1-based line number SHA-256 occupies in the hash order
	# file, then add 1 because the checksums file prepends the filename as $1.
	sha256_col="$(grep -n '^SHA-256$' "$tmpdir/checksums_hashes_order" | cut -d: -f1)"
	if [[ -z "${sha256_col}" ]]; then
		log_error "Could not locate SHA-256 in checksums_hashes_order"
		return 1
	fi
	(( sha256_col += 1 ))

	expected="$(awk -v asset="${asset}" -v col="${sha256_col}" \
		'$1 == asset { print $col }' "$tmpdir/checksums" \
		| tr '[:upper:]' '[:lower:]')"

	if [[ ! "$expected" =~ ^[0-9a-f]{64}$ ]]; then
		log_error "Could not extract SHA-256 checksum for ${asset} from checksums file"
		return 1
	fi

	actual="$(sha256sum -- "$tmpdir/$asset" | awk '{ print $1 }')"

	if [[ "$actual" != "$expected" ]]; then
		log_error "Checksum verification FAILED for ${asset}"
		log_error "Expected: ${expected} Actual: ${actual}"
		return 1
	fi

	mkdir -p -- "$(dirname -- "${output}")"
	chmod 0755 "$tmpdir/$asset"
	mv -- "$tmpdir/$asset" "$output"

	log_info "Checksum OK: ${output}"
)

#######################################
# Entry point.
#######################################
function main() {
	# Install yq-v4 if not present
	log_info "Checking for yq-v4..."

	local tmp_bin_dir="/tmp/bin"
	local cmd_yq yq_version yq_arch

	cmd_yq="$(command -v yq-v4 2>/dev/null || true)"

	if [[ ! -x "${cmd_yq}" ]]; then
		log_info "Resolving latest yq version..."
		if ! yq_version="$(get_latest_yq_version)"; then
			exit 1
		fi
		log_info "Latest yq version: ${yq_version}"

		yq_arch=$(uname -m | sed 's/aarch64/arm64/;s/x86_64/amd64/')

		if ! download_yq "${yq_version}" "yq_linux_${yq_arch}" "${tmp_bin_dir}/yq-v4"; then
			log_error "Could not download yq-v4 version ${yq_version}"
			exit 1
		fi
	else
		log_info "yq-v4 already installed at ${cmd_yq}"
	fi
}

main
