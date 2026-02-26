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

function cleanup_metadata()
{
	if [ ! -f "${CLUSTER_DIR}/metadata.json" ]
	then
		exit 0
	fi

	echo "Deleting metadata.json"
	PowerVC-Tool \
		send-metadata \
		--deleteMetadata "${CLUSTER_DIR}/metadata.json" \
		--serverIP "${CONTROLLER_IP}" \
		--shouldDebug true
}

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
PROGRAMS=( PowerVC-Tool openshift-install openstack jq )
for PROGRAM in ${PROGRAMS[@]}
do
	echo "Checking for program ${PROGRAM}"
	if ! hash ${PROGRAM} 1>/dev/null 2>&1
	then
		echo "Error: Missing ${PROGRAM} program!"
		exit 1
	fi
done

openstack --os-cloud=${CLOUD} image list 1>/dev/null
if [ $? -gt 0 ]
then
	echo "Error: Is openstack configured correctly?"
	exit 1
fi

if [[ ! -v BASEDOMAIN ]]
then
	read -p "What is the base domain []: " BASEDOMAIN
	if [ -z "${BASEDOMAIN}" ]
	then
		echo "Error: You must enter something"
		exit 1
	fi
	export BASEDOMAIN
fi

if [[ ! -v BASTION_IMAGE_NAME ]]
then
	read -p "What is the image name to use for the bastion []: " BASTION_IMAGE_NAME
	if [ -z "${BASTION_IMAGE_NAME}" ]
	then
		echo "Error: You must enter something"
		exit 1
	fi
	export BASTION_IMAGE_NAME
fi

openstack --os-cloud=${CLOUD} image show ${BASTION_IMAGE_NAME} 1>/dev/null
if [ $? -gt 0 ]
then
	echo "Error: Cannot find image (${BASTION_IMAGE_NAME}). Is openstack configured correctly?"
	exit 1
fi

if [[ ! -v BASTION_USERNAME ]]
then
	read -p "What is the username to use for the bastion [cloud-user]: " BASTION_USERNAME
	if [ "${BASTION_USERNAME}" == "" ]
	then
		BASTION_USERNAME="cloud-user"
	fi
	export BASTION_USERNAME
fi

if [[ ! -v CLUSTER_DIR ]]
then
	read -p "What directory should be used for the installation [test]: " CLUSTER_DIR
	if [ "${CLUSTER_DIR}" == "" ]
	then
		CLUSTER_DIR="test"
	fi
	export CLUSTER_DIR
	if [ -d "${CLUSTER_DIR}" ]
	then
		echo "Error: The directory ${CLUSTER_DIR} exists.  Please delete it and try again."
		exit 1
	fi
fi

if [[ ! -v CLUSTER_NAME ]]
then
	read -p "What is the name of the cluster []: " CLUSTER_NAME
	if [ -z "${CLUSTER_NAME}" ]
	then
		echo "Error: You must enter something"
		exit 1
	fi
	export CLUSTER_NAME
fi

if [[ ! -v FLAVOR_NAME ]]
then
	read -p "What is the OpenStack flavor []: " FLAVOR_NAME
	if [ -z "${FLAVOR_NAME}" ]
	then
		echo "Error: You must enter something"
		exit 1
	fi
	export FLAVOR_NAME
fi

openstack --os-cloud=${CLOUD} flavor show ${FLAVOR_NAME} 1>/dev/null
if [ $? -gt 0 ]
then
	echo "Error: Cannot find flavor (${FLAVOR_NAME}). Is openstack configured correctly?"
	exit 1
fi

if [[ ! -v MACHINE_TYPE ]]
then
	read -p "What is the OpenStack machine type []: " MACHINE_TYPE
	if [ -z "${MACHINE_TYPE}" ]
	then
		echo "Error: You must enter something"
		exit 1
	fi
	export MACHINE_TYPE
fi

openstack --os-cloud=${CLOUD} availability zone list --format csv | grep '"'${MACHINE_TYPE}'"' 1>/dev/null
if [ $? -gt 0 ]
then
	echo "Error: Cannot find availability zone (${MACHINE_TYPE}). Is openstack configured correctly?"
	exit 1
fi

if [[ ! -v NETWORK_NAME ]]
then
	read -p "What is the OpenStack network []: " NETWORK_NAME
	if [ -z "${NETWORK_NAME}" ]
	then
		echo "Error: You must enter something"
		exit 1
	fi
	export NETWORK_NAME
fi

if [[ -v PULLSECRET_FILE ]] && [[ ! -f "${PULLSECRET_FILE}" ]]
then
	echo "Warning: PULLSECRET_FILE (${PULLSECRET_FILE}) does not exist"
fi
if [[ -v PULLSECRET_FILE ]] && [[ -f "${PULLSECRET_FILE}" ]]
then
	PULL_SECRET=$(cat "${PULLSECRET_FILE}")
	export PULL_SECRET
else
	read -p "What is your pull secret []: " PULL_SECRET
	if [ -z "${PULL_SECRET}" ]
	then
		echo "Error: You must enter something"
		exit 1
	fi
	export PULL_SECRET
fi

if [[ ! -v INSTALLER_SSHKEY ]]
then
	read -p "What ssh key is used by the installer []: " INSTALLER_SSHKEY
	if [ -z "${INSTALLER_SSHKEY}" ]
	then
		echo "Error: You must enter something"
		exit 1
	fi
	if [ ! -f "${INSTALLER_SSHKEY}" ]
	then
		echo "Error: File ${INSTALLER_SSHKEY} must exist!"
		exit 1
	fi
fi
SSH_KEY=$(cat ${INSTALLER_SSHKEY})

openstack --os-cloud=${CLOUD} network show "${NETWORK_NAME}" --format shell 1>/dev/null
if [ $? -gt 0 ]
then
	echo "Error: Cannot find network (${NETWORK_NAME}). Is openstack configured correctly?"
	exit 1
fi

SUBNET_ID=$(openstack --os-cloud=${CLOUD} network show "${NETWORK_NAME}" --format shell | grep ^subnets | sed -e "s,^[^']*',," -e "s,'.*$,,")
if [ -z "${SUBNET_ID}" ]
then
	echo "Error: SUBNET_ID returned from OpenStack is empty?"
	exit 1
fi

MACHINE_NETWORK=$(openstack --os-cloud=${CLOUD} subnet show "${SUBNET_ID}" --format shell | grep ^cidr)
if [ -z "${MACHINE_NETWORK}" ]
then
	echo "Error: MACHINE_NETWORK returned from OpenStack is empty?"
	exit 1
fi
MACHINE_NETWORK=$(echo "${MACHINE_NETWORK}" | sed -re 's,^[^"]*"(.*)",\1,')
if [ -z "${MACHINE_NETWORK}" ]
then
	echo "Error: MACHINE_NETWORK returned from sed is empty?"
	exit 1
fi

RHCOS_URL=$(openshift-install coreos print-stream-json | jq -r '.architectures.ppc64le.artifacts.openstack' | jq -r '.formats."qcow2.gz".disk.location')
if [ -z "${RHCOS_URL}" ]
then
	echo "Error: RHCOS_URL is empty?"
	exit 1
fi
RHCOS_FILENAME="${RHCOS_URL##*/}"
RHCOS_FILENAME=${RHCOS_FILENAME//.qcow2.gz/}
if [ -z "${RHCOS_FILENAME}" ]
then
	echo "Error: RHCOS_FILENAME is empty?"
	exit 1
fi

if [[ ! -v CONTROLLER_IP ]]
then
	read -p "What is the PowerVC-Tool master controller IP []: " CONTROLLER_IP
	if [ -z "${CONTROLLER_IP}" ]
	then
		echo "Error: You must enter something"
		exit 1
	fi
	export CONTROLLER_IP
fi

ping -c1 ${CONTROLLER_IP}
RC=$?
if [ ${RC} -gt 0 ]
then
	echo "Error: Trying to ping ${CONTROLLER_IP} returned an RC of ${RC}"
	exit 1
fi

PowerVC-Tool \
	check-alive \
	--serverIP "${CONTROLLER_IP}" \
	--shouldDebug true
RC=$?
if [ ${RC} -gt 0 ]
then
	echo "Error: master controller returned an RC of ${RC}"
	exit 1
fi

openstack --os-cloud=${CLOUD} image show ${RHCOS_FILENAME} 1>/dev/null
if [ $? -gt 0 ]
then
	echo "Error: Cannot find image (${RHCOS_FILENAME}). Is openstack configured correctly?"
	export RHCOS_IMAGE_NAME=${RHCOS_FILENAME}
	#exit 1 @BUG
fi

if [[ ! -v SSHKEY_NAME ]]
then
	read -p "What is the OpenStack keypair to use for the bastion []: " SSHKEY_NAME
	if [ -z "${SSHKEY_NAME}" ]
	then
		echo "Error: You must enter something"
		exit 1
	fi
	export SSHKEY_NAME
fi

openstack --os-cloud=${CLOUD} keypair show ${SSHKEY_NAME} 1>/dev/null
if [ $? -gt 0 ]
then
	echo "Error: Cannot find OpenStack keypair (${SSHKEY_NAME}). Is openstack configured correctly?"
	exit 1
fi

# NOTE: IBMCLOUD_API_KEY is an optional environment variable
declare -a ENV_VARS
ENV_VARS=( "BASEDOMAIN" "BASTION_IMAGE_NAME" "BASTION_USERNAME" "CLOUD" "CLUSTER_DIR" "CLUSTER_NAME" "FLAVOR_NAME" "MACHINE_TYPE" "NETWORK_NAME" "RHCOS_FILENAME" "CONTROLLER_IP" "SSHKEY_NAME" )

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

mkdir ${CLUSTER_DIR}

PowerVC-Tool \
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
RC=$?
if [ ${RC} -gt 0 ]
then
	echo "Error: PowerVC-Tool create-bastion failed with an RC of ${RC}"
	exit 1
fi

if [ ! -f /tmp/bastionIp ]
then
	echo "Error: Expecting file /tmp/bastionIp"
	exit 1
fi

VIP_API=$(cat /tmp/bastionIp)
VIP_INGRESS=$(cat /tmp/bastionIp)

if [ -z "${VIP_API}" -o -z "${VIP_INGRESS}" ]
then
	echo "Error: VIP_API and VIP_INGRESS must be defined!"
	exit 1
fi

# Make sure all required DNS entries exist!
while true
do
	# Try the wildcard DNS entry first
	FOUND_ALL=true
	echo "Trying up to 60 times resolving *.apps.${CLUSTER_NAME}.${BASEDOMAIN} ..."
	for (( TRIES=0; TRIES<=60; TRIES++ ))
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
	if ! ${FOUND}
	then
		FOUND_ALL=false
		continue
	fi
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
		if ! ${FOUND}
		then
			FOUND_ALL=false
		fi
	done
	echo "FOUND_ALL=${FOUND_ALL}"
	if ${FOUND_ALL}
	then
		break
	fi
	sleep 15s
done

# Check if the VIP is the same as the LB
FOUND=false
for (( TRIES=0; TRIES<=60; TRIES++ ))
do
	set +e
	IP=$(getent ahostsv4 api.${CLUSTER_NAME}.${BASEDOMAIN} 2>/dev/null | grep STREAM | cut -f1 -d' ')
	set -e
	echo "IP=${IP}"
	echo "VIP_API=${VIP_API}"
	if [ "${IP}" == "${VIP_API}" ]
	then
		FOUND=true
		break
	else
		echo "Warning: VIP_API (${VIP_API}) is not the same as IP (${IP}), sleeping..."
	fi
	sleep 15s
done
if ! ${FOUND}
then
	echo "Error: VIP_API (${VIP_API}) is not the same as ${IP}"
	exit 1
fi

#
# Create the openshift-installer's install configuration file
#
cat << ___EOF___ > ${CLUSTER_DIR}/install-config.yaml
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
___EOF___

# @DEBUG
echo "8<-----8<-----8<-----8<-----8<-----8<-----8<-----8<-----8<-----8<-----"
cat ${CLUSTER_DIR}/install-config.yaml
echo "8<-----8<-----8<-----8<-----8<-----8<-----8<-----8<-----8<-----8<-----"

openshift-install version
RC=$?
if [ ${RC} -gt 0 ]
then
	exit 1
fi

openshift-install create install-config --dir=${CLUSTER_DIR}
RC=$?
if [ ${RC} -gt 0 ]
then
	exit 1
fi

openshift-install create ignition-configs --dir=${CLUSTER_DIR}
RC=$?
if [ ${RC} -gt 0 ]
then
	exit 1
fi

# By now, the infraID field in metadata.json is filled out
INFRA_ID=$(jq -r .infraID ${CLUSTER_DIR}/metadata.json)
echo "INFRA_ID=${INFRA_ID}"

if [ ! -f "${CLUSTER_DIR}/metadata.json" ]
then
	echo "Error: Expecting ${CLUSTER_DIR}/metadata.json to exist!"
	exit 1
fi

PowerVC-Tool \
	send-metadata \
	--createMetadata ${CLUSTER_DIR}/metadata.json \
	--serverIP ${CONTROLLER_IP} \
	--shouldDebug true
RC=$?
if [ ${RC} -gt 0 ]
then
	echo "Error: PowerVC-Tool send-metadata failed with an RC of ${RC}"
	exit 1
fi

#jq --arg NEW_INFRA_ID ${CLUSTER_NAME} -r -c '. | .infraID = $NEW_INFRA_ID' ${CLUSTER_DIR}/metadata.json
#jq --arg NEW_INFRA_ID ${CLUSTER_NAME} -r -c '. | .powervc.identifier.openshiftClusterID = $NEW_INFRA_ID' ${CLUSTER_DIR}/metadata.json

set +e
openshift-install create manifests --dir=${CLUSTER_DIR}
RC=$?
set -e
if [ ${RC} -gt 0 ]
then
	cleanup_metadata
	exit 1
fi

PowerVC-Tool \
	check-alive \
	--serverIP ${CONTROLLER_IP} \
	--shouldDebug true
RC=$?
if [ ${RC} -gt 0 ]
then
	echo "Error: PowerVC-Tool check-alive failed with an RC of ${RC}"
	cleanup_metadata
	exit 1
fi

set +e
openshift-install create cluster --dir=${CLUSTER_DIR} --log-level=debug
RC=$?
set -e
if [ ${RC} -gt 0 ]
then
	echo "Error: openshift-install create cluster failed with an RC of ${RC}"
	echo

	if [[ ! -v BASTION_USERNAME ]]
	then
		read -p "What is the username for the bastion [cloud-user]: " BASTION_USERNAME
		if [ "${BASTION_USERNAME}" == "" ]
		then
			BASTION_USERNAME="cloud-user"
		fi
	fi

	if [[ ! -v BASTION_RSA ]]
	then
		read -p "Where is the ssh private key for the bastion []: " BASTION_RSA
		if [ -z "${BASTION_RSA}" ]
		then
			echo "Error: You must enter something"
			exit 1
		fi
		if [ ! -f "${BASTION_RSA}" ]
		then
			echo "Error: ${BASTION_RSA} is not a file?"
			exit 1
		fi
	fi

	ARGS="watch-create \
		--cloud ${CLOUD} \
		--metadata ${CLUSTER_DIR}/metadata.json \
		--kubeconfig ${CLUSTER_DIR}/auth/kubeconfig \
		--bastionUsername ${BASTION_USERNAME} \
		--bastionRsa ${BASTION_RSA} \
		--baseDomain ${BASEDOMAIN} \
		--shouldDebug false"

	PowerVC-Tool ${ARGS}

	cleanup_metadata

	exit 1
fi
