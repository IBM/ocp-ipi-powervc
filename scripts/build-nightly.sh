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

USE_PATCH=false

declare -a ENV_VARS
ENV_VARS=( )

for VAR in "${ENV_VARS[@]}"
do
	if [[ ! -v ${VAR} ]]
	then
		echo "${VAR} must be set!"
		exit 1
	fi
	VALUE=$(eval "echo \"\${${VAR}}\"")
	if [[ -z "${VALUE}" ]]
	then
		echo "${VAR} must be set!"
		exit 1
	fi
done

set -euo pipefail

function usage {
	echo "Usage: $0 [ -v ]" 1>&2
	exit 1
}

#set -x

SCRIPT_DIR=$( cd -- "$( dirname -- "${BASH_SOURCE[0]}" )" &> /dev/null && pwd )

while getopts "vV" OPT
do
	#echo "OPT=${OPT}"
	case "${OPT}" in
	v|V)
		VERSION=$(cd "${SCRIPT_DIR}"; git describe --tags --abbrev=0)
		echo "The version is ${VERSION}"
		exit 0
		;;
	*)
		usage
		;;
	esac
done
#echo "OPTIND=${OPTIND}"
shift $((OPTIND - 1))

if [[ $# -ne 1 ]]
then
	usage
fi

RELEASE=$1
RELEASE_DIR=$(mktemp --directory); trap 'rm -rf "${RELEASE_DIR}"' EXIT
echo "RELEASE=${RELEASE}"

if [ -z "${HOME}" ]
then
	echo "Error: Expecting the bash variable HOME to be set!"
	exit 1
fi

if [ ! -d "${HOME}/installer/" ]
then
	echo "Error: Expecting the GIT directory ${HOME}/installer/ to exist!"
	exit 1
fi

if [ ! -f "${HOME}/.pullSecretCompact" ]
then
	echo "Error: Expecting the file ${HOME}/.pullSecretCompact to exist!"
	exit 1
fi

echo "8<--------8<--------8<--------8<--------8<--------8<--------8<--------8<--------"

declare -a PROGRAMS
PROGRAMS=( git go make oc patch tar zip )
for PROGRAM in "${PROGRAMS[@]}"
do
	echo "Checking for program ${PROGRAM}"
	if ! hash "${PROGRAM}" 1>/dev/null 2>&1
	then
		echo "Error: Missing ${PROGRAM} program!"
		exit 1
	fi
done

pushd "${RELEASE_DIR}"

echo "8<--------8<--------8<--------8<--------8<--------8<--------8<--------8<--------"
echo "Downloading the tools for ${RELEASE}"

oc adm release extract --tools -a "${HOME}/.pullSecretCompact" "${RELEASE}"

echo "8<--------8<--------8<--------8<--------8<--------8<--------8<--------8<--------"
echo "Extracting the OpenShift installer from the tools"

tar xvzf openshift-install-linux-*.tar.gz

./openshift-install version

COMMIT=$(./openshift-install version | grep 'built from commit' | awk '{print $4}')
echo "COMMIT=${COMMIT}"

popd

pushd "${HOME}/installer/"

echo "8<--------8<--------8<--------8<--------8<--------8<--------8<--------8<--------"
echo "Gitting"

git reset --hard main

git clean -fxd .

git fetch origin

git checkout "${COMMIT}"

if ${USE_PATCH}
then

	echo "8<--------8<--------8<--------8<--------8<--------8<--------8<--------8<--------"
	echo "Patching"

	patch -p1 < /home/OpenShift/PowerVC/0001-PowerVC-Add-new-platform-for-PowerVC-2025-11-18.patch
	patch -p1 < /home/OpenShift/PowerVC/0002-PowerVC-Do-not-use-Security-Groups-2025-11-18.patch
	patch -p1 < /home/OpenShift/PowerVC/0003-PowerVC-Does-not-support-OpenStack-Load-Balancers-2025-11-18.patch
	patch -p1 < /home/OpenShift/PowerVC/0004-PowerVC-Allow-bootstrap-ignition-upload-to-Swift-2025-11-18.patch

fi

echo "8<--------8<--------8<--------8<--------8<--------8<--------8<--------8<--------"
echo "Building"

DEFAULT_ARCH=ppc64le ./hack/build.sh

echo "DONE!"
