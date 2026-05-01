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

declare -a ENV_VARS
ENV_VARS=( "CLOUD" )

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

set -euo pipefail
#set -x

while read CONTAINER
do
	echo "CONTAINER=${CONTAINER}"

	while read OBJECT
	do
		echo "OBJECT=${OBJECT}"

		openstack --os-cloud=${CLOUD} object delete ${CONTAINER} ${OBJECT}

	done < <(openstack --os-cloud=${CLOUD} object list ${CONTAINER} --format csv | sed -e '/\(Name\)/d' -e 's,",,g')

	openstack --os-cloud=${CLOUD} container delete ${CONTAINER} ${OBJECT}

done < <(openstack --os-cloud=${CLOUD} container list --format csv | sed -e '/\(Name\|container_name\)/d' -e 's,",,g')
