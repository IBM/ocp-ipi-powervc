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

if [[ ! -v CLOUD ]]
then
	read -p "What is the cloud name in ~/.config/openstack/clouds.yaml []: " CLOUD
	if [ -z "${CLOUD}" ]
	then
		echo "Error: You must enter something"
		exit 1
	fi
	export CLOUD
fi

declare -a PROGRAMS
PROGRAMS=( jq openstack yq )
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

if [ ! -d "${CLUSTER_DIR}" ]
then
	echo "Error: Directory ${CLUSTER_DIR} does not exist!"
	exit 1
fi

SERVER_URL=$(yq eval ".clouds.${CLOUD}.auth.auth_url" ~/.config/openstack/clouds.yaml)
RC=$?
if [ ${RC} -gt 0 ]
then
	echo "Error: Trying to eval auth_url returned an RC of ${RC}"
	exit 1
fi
if [ -z "${SERVER_URL}" ]
then
	echo "Error: Could not get auth_url from ~/.config/openstack/clouds.yaml"
	exit 1
fi
${DEBUG} && echo "SERVER_URL=${SERVER_URL}"

HOSTNAME_URL=$(echo "${SERVER_URL}" | awk -F/ '{print $3}')
${DEBUG} && echo "HOSTNAME_URL=${HOSTNAME_URL}"

SERVER_IP=$(echo "${HOSTNAME_URL}" | awk -F: '{print $1}')
${DEBUG} && echo "SERVER_IP=${SERVER_IP}"

if [[ ! -v SERVER_IP ]]
then
	read -p "What is the PowerVC server IP []: " SERVER_IP
	if [ -z "${SERVER_IP}" ]
	then
		echo "Error: You must enter something"
		exit 1
	fi
	export SERVER_IP
fi

echo "Pinging ${SERVER_IP}"
ping -c1 ${SERVER_IP} > /dev/null
RC=$?
if [ ${RC} -gt 0 ]
then
	echo "Error: Trying to ping ${SERVER_IP} returned an RC of ${RC}"
	exit 1
fi

PROJECT_ID=$(yq eval ".clouds.${CLOUD}.auth.project_id" ~/.config/openstack/clouds.yaml)
RC=$?
if [ ${RC} -gt 0 ]
then
	echo "Error: Trying to eval project_id returned an RC of ${RC}"
	exit 1
fi
if [ -z "${PROJECT_ID}" ]
then
	echo "Error: Could not get project_id from ~/.config/openstack/clouds.yaml"
	exit 1
fi
${DEBUG} && echo "PROJECT_ID=${PROJECT_ID}"

PROJECT_NAME=$(yq eval ".clouds.${CLOUD}.auth.project_name" ~/.config/openstack/clouds.yaml)
RC=$?
if [ ${RC} -gt 0 ]
then
	echo "Error: Trying to eval project_name returned an RC of ${RC}"
	exit 1
fi
if [ -z "${PROJECT_NAME}" ]
then
	echo "Error: Could not get project_name from ~/.config/openstack/clouds.yaml"
	exit 1
fi
${DEBUG} && echo "PROJECT_NAME=${PROJECT_NAME}"

USERNAME=$(yq eval ".clouds.${CLOUD}.auth.username" ~/.config/openstack/clouds.yaml)
RC=$?
if [ ${RC} -gt 0 ]
then
	echo "Error: Trying to eval username returned an RC of ${RC}"
	exit 1
fi
if [ -z "${USERNAME}" ]
then
	echo "Error: Could not get username from ~/.config/openstack/clouds.yaml"
	exit 1
fi
${DEBUG} && echo "USERNAME=${USERNAME}"

PASSWORD=$(yq eval ".clouds.${CLOUD}.auth.password" ~/.config/openstack/clouds.yaml)
RC=$?
if [ ${RC} -gt 0 ]
then
	echo "Error: Trying to eval password returned an RC of ${RC}"
	exit 1
fi
if [ -z "${PASSWORD}" ]
then
	echo "Error: Could not get password from ~/.config/openstack/clouds.yaml"
	exit 1
fi
#${DEBUG} && echo "PASSWORD=${PASSWORD}"

INFRA_ID=$(jq -r .infraID ${CLUSTER_DIR}/metadata.json)
RC=$?
if [ ${RC} -gt 0 ]
then
	echo "Error: Trying to eval infraID returned an RC of ${RC}"
	exit 1
fi
if [ -z "${INFRA_ID}" ]
then
	echo "Error: infraID is empty?"
	exit 1
fi
${DEBUG} && echo "INFRA_ID=${INFRA_ID}"

SERVER="${INFRA_ID}-${ARG}"
if [ -z "${SERVER}" ]
then
	echo "Error: server is empty?"
	exit 1
fi
${DEBUG} && echo "SERVER=${SERVER}"

SERVER_FILE=$(mktemp)
HYPERVISOR_FILE=$(mktemp)
trap "/bin/rm -rf ${SERVER_FILE} ${HYPERVISOR_FILE}" EXIT

echo "Querying the openstack server"
openstack --os-cloud=${CLOUD} server show "${SERVER}" --format=json > ${SERVER_FILE}
RC=$?
if [ ${RC} -gt 0 ]
then
	echo "Error: Trying to server show returned an RC of ${RC}"
	exit 1
fi

HYPERVISOR=$(jq -r '."OS-EXT-SRV-ATTR:hypervisor_hostname"' ${SERVER_FILE})
RC=$?
if [ ${RC} -gt 0 ]
then
	echo "Error: Trying to eval hypervisor_hostname returned an RC of ${RC}"
	exit 1
fi
if [ -z "${HYPERVISOR}" ]
then
	echo "Error: hypervisor is empty?"
	exit 1
fi
${DEBUG} && echo "HYPERVISOR=${HYPERVISOR}"

INSTANCE_NAME=$(jq -r '."OS-EXT-SRV-ATTR:instance_name"' ${SERVER_FILE})
RC=$?
if [ ${RC} -gt 0 ]
then
	echo "Error: Trying to eval instance_name returned an RC of ${RC}"
	exit 1
fi
if [ -z "${INSTANCE_NAME}" ]
then
	echo "Error: instance name is empty?"
	exit 1
fi
${DEBUG} && echo "INSTANCE_NAME=${INSTANCE_NAME}"

AUTH_JSON='{ "auth": { "scope": { "project": { "domain": { "name": "Default" }, "name": "'${PROJECT_NAME}'" } }, "identity": { "password": { "user": { "domain": { "name": "Default" }, "password": "'${PASSWORD}'", "name": "'${USERNAME}'" } }, "methods": [ "password" ] } } }'
#${DEBUG} && echo "AUTH_JSON=${AUTH_JSON}"

if [[ ! -v TOKEN_ID ]]
then
	echo "Querying the token"
	TOKEN_ID=$(curl --tlsv1 --insecure --silent -i --silent --request POST --header "Accept: application/json" --header "Content-Type: application/json" --data "${AUTH_JSON}" https://${SERVER_IP}:5000/v3/auth/tokens | grep x-subject-token | cut -d ' ' -f2 | sed 's/\^M//')
fi
${DEBUG} && echo "TOKEN_ID=${TOKEN_ID}"

if [ -z "${TOKEN_ID}" ]
then
	echo "Error: TOKEN_ID is empty!"
	exit 1
fi

echo "Querying the hypervisor"
curl --tlsv1 --insecure --silent --request GET --header "X-Auth-Token:${TOKEN_ID}" https://${SERVER_IP}:8774/v2.1/${PROJECT_ID}/os-hosts/${HYPERVISOR} > ${HYPERVISOR_FILE}
MANAGER_TYPE=$(jq -r '.host[].registration | select(length > 0) | .manager_type' ${HYPERVISOR_FILE})
RC=$?
if [ ${RC} -gt 0 ]
then
	echo "Error: Trying to eval(1) os-hosts returned an RC of ${RC}"
	exit 1
fi
if [ -z "${MANAGER_TYPE}" ]
then
	echo "Error: manager type is empty?"
	exit 1
fi
${DEBUG} && echo "MANAGER_TYPE=${MANAGER_TYPE}"

if [ "${MANAGER_TYPE}" == "hmc" ]
then
	PRIMARY_HMC_UUID=$(jq -r '.host[].registration | select(length > 0) | .primary_hmc_uuid' ${HYPERVISOR_FILE})
	RC=$?
	if [ ${RC} -gt 0 ]
	then
		echo "Error: Trying to eval(2) os-hosts returned an RC of ${RC}"
		exit 1
	fi
	if [ -z "${PRIMARY_HMC_UUID}" ]
	then
		echo "Error: primary HMC UUID is empty?"
		exit 1
	fi
	${DEBUG} && echo "PRIMARY_HMC_UUID=${PRIMARY_HMC_UUID}"
	if [ "${PRIMARY_HMC_UUID}" == "null" ]
	then
		echo "Error: Could not find primary HMC UUID in:"
		cat ${HYPERVISOR_FILE}
		echo
		exit 1
	fi

	echo "Querying the ibm-hmcs"
	SSH_IP=$(curl --tlsv1 --insecure --silent --request GET --header "X-Auth-Token:${TOKEN_ID}" https://${SERVER_IP}:8774/v2.1/${PROJECT_ID}/ibm-hmcs/${PRIMARY_HMC_UUID} | jq -r .hmc.registration.access_ip)
	RC=$?
	if [ ${RC} -gt 0 ]
	then
		echo "Error: Trying to eval ibm-hmcs/hmc returned an RC of ${RC}"
		exit 1
	fi
	if [ -z "${SSH_IP}" ]
	then
		echo "Error: HMC IP is empty?"
		exit 1
	fi
	${DEBUG} && echo "SSH_IP=${SSH_IP}"

	USER_ID="hscroot"

elif [ "${MANAGER_TYPE}" == "pvm" ]
then

	USER_ID=$(jq -r '.host[].registration | select(length > 0) | .user_id' ${HYPERVISOR_FILE})
	RC=$?
	if [ ${RC} -gt 0 ]
	then
		echo "Error: Trying to eval(3) os-hosts returned an RC of ${RC}"
		exit 1
	fi
	if [ -z "${USER_ID}" ]
	then
		echo "Error: user_id is empty?"
		exit 1
	fi
	${DEBUG} && echo "USER_ID=${USER_ID}"

	SSH_IP=$(jq -r '.host[].registration | select(length > 0) | .access_ip' ${HYPERVISOR_FILE})
	RC=$?
	if [ ${RC} -gt 0 ]
	then
		echo "Error: Trying to eval(4) os-hosts returned an RC of ${RC}"
		exit 1
	fi
	if [ -z "${SSH_IP}" ]
	then
		echo "Error: access_ip is empty?"
		exit 1
	fi
	${DEBUG} && echo "SSH_IP=${SSH_IP}"

else

	echo "Error: Unknown manager type (${MANAGER_TYPE})"
	exit 1
fi

HOST_DISPLAY_NAME=$(jq -r '.host[].registration | select(length > 0) | .host_display_name' ${HYPERVISOR_FILE})
RC=$?
if [ ${RC} -gt 0 ]
then
	echo "Error: Trying to eval os-hosts/hypervisor returned an RC of ${RC}"
	exit 1
fi
if [ -z "${HOST_DISPLAY_NAME}" ]
then
	echo "Error: primary HMC display name is empty?"
	exit 1
fi
${DEBUG} && echo "HOST_DISPLAY_NAME=${HOST_DISPLAY_NAME}"

#
# Setup something like this previously:
# $ SSH_PASSWORDS=( "pass1" "pa552" )
#
echo -n 'for SSH_PASSWORD in "${SSH_PASSWORDS[@]}"; do sshpass -p $SSH_PASSWORD '
echo -n "ssh -t -o PubkeyAuthentication=no ${USER_ID}@${SSH_IP} mkvterm -m ${HOST_DISPLAY_NAME} -p ${INSTANCE_NAME}"
echo "; done"
