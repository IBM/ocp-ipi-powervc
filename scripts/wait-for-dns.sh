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

CLUSTER_NAME=$(jq -r .clusterName ${CLUSTER_DIR}/metadata.json)
RC=$?
if [ ${RC} -gt 0 ]
then
	echo "Error: Trying to eval clusterName returned an RC of ${RC}"
	exit 1
fi
if [ -z "${CLUSTER_NAME}" ]
then
	echo "Error: clusterName is empty?"
	exit 1
fi
${DEBUG} && echo "CLUSTER_NAME=${CLUSTER_NAME}"

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
