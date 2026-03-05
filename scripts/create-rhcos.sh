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

set -uo pipefail

if [[ ! -v DEBUG ]]
then
	DEBUG=false
fi

if [[ ! -v PULLSECRET_FILE ]]
then
	read -p "What is your pull secret file []: " PULLSECRET_FILE
	if [ -z "${PULLSECRET_FILE}" ]
	then
		echo "Error: You must enter something"
		exit 1
	fi
fi
if [[ -v PULLSECRET_FILE ]] && [[ ! -f "${PULLSECRET_FILE}" ]]
then
	echo "Error: PULLSECRET_FILE (${PULLSECRET_FILE}) does not exist"
	exit 1
fi

declare -a ENV_VARS
ENV_VARS=( "OC_RELEASE" "OC_URL" "PULLSECRET_FILE" )

for VAR in ${ENV_VARS[@]}
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

declare -a PROGRAMS
PROGRAMS=( jq mktemp oc pvsadm powervc-image uname )
for PROGRAM in ${PROGRAMS[@]}
do
	echo "Checking for program ${PROGRAM}"
	if ! hash ${PROGRAM} 1>/dev/null 2>&1
	then
		echo "Error: Missing ${PROGRAM} program!"
		exit 1
	fi
done

ARCH=$(uname -m)
if [ "${ARCH}" == "x86_64" ]
then
	ARCH=amd64
fi
${DEBUG} && echo "ARCH=${ARCH}"

TMP_DIR=$(mktemp --directory)
TMP_FILE=$(mktemp)
trap 'rm -r ${TMP_DIR} ${TMP_FILE}' EXIT
pushd ${TMP_DIR} 1>/dev/null

echo "Extracting openshift-install from release ${OC_URL}:${OC_RELEASE}"
oc adm -a ${PULLSECRET_FILE} release extract --command openshift-install ${OC_URL}:${OC_RELEASE}
RC=$?
if [ ${RC} -gt 0 ]
then
	echo "Error: oc adm returned an RC of ${RC}"
	exit 1
fi

echo "Finding CoreOS version"
${TMP_DIR}/openshift-install coreos print-stream-json > ${TMP_FILE}
URL=$(jq -r '.architectures.ppc64le.artifacts.openstack' ${TMP_FILE} | jq -r '.formats."qcow2.gz".disk.location')
${DEBUG} && echo "URL=${URL}"
if [ -z "${URL}" ]
then
	echo "Error: URL is empty?"
	exit 1
fi

FILENAME="${URL##*/}"
${DEBUG} && echo "FILENAME=${FILENAME}"

BASE_FILENAME=${FILENAME//.qcow2.gz/}
${DEBUG} && echo "BASE_FILENAME=${BASE_FILENAME}"

echo pvsadm image qcow2ova --image-dist coreos --image-name ${BASE_FILENAME} --image-url ${URL} --image-size 16

echo powervc-image --project ocp-ci import -n ${BASE_FILENAME} -p ${FILENAME} -t ... -m os-type=coreos architecture=ppc64le
