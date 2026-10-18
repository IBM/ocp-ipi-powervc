#!/usr/bin/env bash

# Copyright 2026 IBM Corp
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#       http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# Extract the openshift-install and oc binaries for a release into ./<VERSION>/
#
# Supported versions (run with --help for the options):
#   1) ppc64le nightly: 5.0.0-0.nightly-ppc64le-2026-09-27-003844
#      (https://ppc64le.ocp.releases.ci.openshift.org/)
#   2) multi nightly:   5.0.0-0.nightly-multi-2026-10-17-025624
#      (https://multi.ocp.releases.ci.openshift.org/)
#   3) released/RC:     5.0.0-rc.7  (quay.io ocp-release, ppc64le or multi)

set -euo pipefail

PULL_SECRET=${PULL_SECRET:-${HOME}/.pullSecretCompact}

ARCH=ppc64le
IMAGE=

# usage [EXIT_CODE]
# Print the help text and exit.  Help goes to stdout when requested (exit 0),
# otherwise to stderr.
usage() {
	local rc=${1:-1}
	local fd=2

	if [ "${rc}" -eq 0 ]; then
		fd=1
	fi

	cat >&"${fd}" <<- EOF
	Usage: ${0##*/} [-a|--arch ARCH] [-p|--pull-spec PULL_SPEC] [-s|--pull-secret FILE] VERSION

	Extract the openshift-install and oc binaries of a release into ./VERSION/

	  -a, --arch ARCH            architecture of a released/RC version
	                             (ppc64le or multi, default: ppc64le).
	                             Nightlies ignore it; their type is taken from
	                             the version name.
	  -p, --pull-spec PULL_SPEC  release image pull spec; overrides the image
	                             that is looked up.
	  -s, --pull-secret FILE     pull secret file used to access the registries
	                             (default: $PULL_SECRET or ~/.pullSecretCompact).
	  -h, --help                 show this help.

	Environment:
	  PULL_SECRET                pull secret file (default: ~/.pullSecretCompact)
	EOF

	exit "${rc}"
}

die() {
	echo "Error: $*" >&2
	exit 1
}

# require_value OPTION [VALUE]
# Fail if an option that needs a value has none.
require_value() {
	if [ $# -lt 2 ]; then
		echo "Error: option $1 requires a value" >&2
		usage
	fi
}

POSITIONAL=()
while [ $# -gt 0 ]; do
	case "$1" in
	-a|--arch)
		require_value "$@"
		ARCH=$2
		shift 2
		;;
	--arch=*)
		ARCH=${1#*=}
		shift
		;;
	-p|--pull-spec)
		require_value "$@"
		IMAGE=$2
		shift 2
		;;
	--pull-spec=*)
		IMAGE=${1#*=}
		shift
		;;
	-s|--pull-secret)
		require_value "$@"
		PULL_SECRET=$2
		shift 2
		;;
	--pull-secret=*)
		PULL_SECRET=${1#*=}
		shift
		;;
	-h|--help)
		usage 0
		;;
	--)
		shift
		POSITIONAL+=("$@")
		break
		;;
	-*)
		echo "Error: unknown option: $1" >&2
		usage
		;;
	*)
		POSITIONAL+=("$1")
		shift
		;;
	esac
done
set -- "${POSITIONAL[@]+"${POSITIONAL[@]}"}"

if [ $# -ne 1 ]; then
	usage
fi

VERSION=$1

case "${ARCH}" in
ppc64le|multi) ;;
*) die "invalid architecture: ${ARCH}" ;;
esac

# Reject anything that could escape the current directory or inject into URLs
if ! [[ "${VERSION}" =~ ^[0-9]+\.[0-9]+\.[0-9]+[A-Za-z0-9._-]*$ ]]; then
	die "invalid version: ${VERSION}"
fi

# A pull spec is a registry image reference, not a file
if [ -n "${IMAGE}" ] && { [ -e "${IMAGE}" ] || ! [[ "${IMAGE}" =~ ^[A-Za-z0-9._:/@-]+/[A-Za-z0-9._:/@-]+$ ]]; }; then
	die "invalid pull spec: ${IMAGE} (use --pull-secret for the pull secret file)"
fi

[ -r "${PULL_SECRET}" ] || die "pull secret not readable: ${PULL_SECRET}"

require_command() {
	command -v "$1" > /dev/null || die "$1 not found in PATH"
}

# lookup_pullspec HOST VERSION
# Print the pull spec of a nightly, taken from a release controller.
lookup_pullspec() {
	local host=$1 version=$2 stream

	# Strip the trailing timestamp to get the stream name,
	# e.g. 5.0.0-0.nightly-multi-2026-10-17-025624 -> 5.0.0-0.nightly-multi
	stream=$(sed -E 's/-[0-9]{4}-[0-9]{2}-[0-9]{2}-[0-9]{6}$//' <<< "${version}")

	curl --silent --fail --show-error --max-time 30 \
		"https://${host}/api/v1/releasestream/${stream}/tags" | \
		jq -r --arg v "${version}" '.tags[] | select(.name == $v) | .pullSpec' | \
		head -n1
}

require_command oc

if [ -z "${IMAGE}" ]; then
	case "${VERSION}" in
	*nightly-ppc64le-*) host=ppc64le.ocp.releases.ci.openshift.org ;;
	*nightly-multi-*)   host=multi.ocp.releases.ci.openshift.org ;;
	*nightly*)          die "unknown nightly type: ${VERSION}" ;;
	*)                  host= ;;
	esac

	if [ -n "${host}" ]; then
		require_command curl
		require_command jq
		IMAGE=$(lookup_pullspec "${host}" "${VERSION}") || true
		[ -n "${IMAGE}" ] || die "could not find a pull spec for ${VERSION} on ${host}"
	else
		IMAGE="quay.io/openshift-release-dev/ocp-release:${VERSION}-${ARCH}"
	fi
fi

echo "Version: ${VERSION}"
echo "Image:   ${IMAGE}"

mkdir -p "${VERSION}"

for command in openshift-install oc; do
	oc adm -a "${PULL_SECRET}" release extract --command="${command}" --to="${VERSION}" "${IMAGE}"
done

ls -l "${VERSION}"
