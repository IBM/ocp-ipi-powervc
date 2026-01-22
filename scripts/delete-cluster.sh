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

declare -a PROGRAMS
PROGRAMS=( PowerVC-Tool openshift-install )
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

ping -c1 ${SERVER_IP}
RC=$?
if [ ${RC} -gt 0 ]
then
	echo "Error: Trying to ping ${SERVER_IP} returned an RC of ${RC}"
	exit 1
fi

PowerVC-Tool \
	send-metadata \
	--deleteMetadata ${CLUSTER_DIR}/metadata.json \
	--serverIP ${SERVER_IP} \
	--shouldDebug true
RC=$?

if [ ${RC} -gt 0 ]
then
	echo "Error: PowerVC-Tool send-metadata failed with an RC of ${RC}"
	exit 1
fi

openshift-install destroy cluster --dir=${CLUSTER_DIR} --log-level=debug
RC=$?
if [ ${RC} -gt 0 ]
then
	echo "Error: openshift-install destroy cluster failed with an RC of ${RC}"
	exit 1
fi
