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

set -uo pipefail

if [[ ! -v DEBUG ]]
then
	DEBUG=false
fi

if [ $# -ne 1 ]
then
	echo "Usage [ bootstrap | master-0 | master-1 | master-2 ]"
	exit 1
fi

ARG=$1
if [ "${ARG}" != "bootstrap" ] && [ "${ARG}" != "master-0" ] && [ "${ARG}" != "master-1" ] && [ "${ARG}" != "master-2" ]
then
	echo "${ARG} is unrecognized"
	echo "Usage [ bootstrap | master-0 | master-1 | master-2 ]"
	exit 1
fi

declare -a PROGRAMS
PROGRAMS=( jq mktemp openstack )
for PROGRAM in ${PROGRAMS[@]}
do
	echo "Checking for program ${PROGRAM}"
	if ! hash ${PROGRAM} 1>/dev/null 2>&1
	then
		echo "Error: Missing ${PROGRAM} program!"
		exit 1
	fi
done

if [[ ! -v CLUSTER_DIR ]]
then
	read -p "What directory should be used for the installation [test]: " CLUSTER_DIR
	if [ "${CLUSTER_DIR}" == "" ]
	then
		CLUSTER_DIR="test"
	fi
	export CLUSTER_DIR
fi
${DEBUG} && echo "CLUSTER_DIR=${CLUSTER_DIR}"

if [ ! -d "${CLUSTER_DIR}" ]
then
	echo "Error: Directory ${CLUSTER_DIR} does not exist!"
	exit 1
fi

CLOUD=$(jq -r '.openstack.cloud' ${CLUSTER_DIR}/metadata.json)
if [ -z "${CLOUD}" ]
then
	echo "Error: cloud not found in ${CLUSTER_DIR}/metadata.json"
	exit 1
fi
${DEBUG} && echo "CLOUD=${CLOUD}"

INFRA_ID=$(jq -r '.infraID' ${CLUSTER_DIR}/metadata.json)
${DEBUG} && echo "INFRA_ID=${INFRA_ID}"

TMP_FILE=$(mktemp)
trap 'rm ${TMP_FILE}' EXIT

openstack --os-cloud=${CLOUD} server list --format=csv > ${TMP_FILE}
if [ $? -gt 0 ]
then
	echo "Error: Is openstack configured correctly?"
	exit 1
fi
if ${DEBUG}
then
	cat ${TMP_FILE}
fi

if [[ $(cat ${TMP_FILE} | grep ${INFRA_ID} | wc -l) -eq 0 ]]
then
	echo "Error: There were no servers with (${INFRA_ID}) found."
	exit 1
fi

openstack --os-cloud=${CLOUD} server show "${INFRA_ID}-${ARG}" --format=shell > ${TMP_FILE}
if ${DEBUG}
then
	cat ${TMP_FILE}
fi

ADDRESS_LINE=$(cat ${TMP_FILE} | grep "addresses=")
${DEBUG} && echo "ADDRESS_LINE=${ADDRESS_LINE}"

ADDRESS=$(echo "${ADDRESS_LINE}" | sed -e "s,[^[]*[[]',," -e "s,'.*,,")
${DEBUG} && echo "ADDRESS=${ADDRESS}"

cat << __EOF__
(set -e; IP="${ADDRESS}"; ssh-keygen -f ~/.ssh/known_hosts -R \${IP} || true; ssh-keyscan \${IP} | sed '/^#/d' >> ~/.ssh/known_hosts; ssh -tA -i ~/.ssh/id_installer_rsa core@\${IP})
__EOF__
