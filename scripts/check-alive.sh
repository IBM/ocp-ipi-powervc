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

declare -a ENV_VARS
ENV_VARS=( "BASEDOMAIN" "BASTION_USERNAME" "BASTION_RSA" "CLOUD" "CONTROLLER_IP" "DHCP_DNS_SERVERS" "DHCP_NETMASK" "DHCP_ROUTER" "DHCP_SERVER_ID" "DHCP_SUBNET" )

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

ARCH=$(uname -m)
if [ "${ARCH}" == "x86_64" ]
then
	ARCH=amd64
fi
${DEBUG} && echo "ARCH=${ARCH}"

POWERVC_TOOL="ocp-ipi-powervc-linux-${ARCH}"
${DEBUG} && echo "POWERVC_TOOL=${POWERVC_TOOL}"

declare -a PROGRAMS
PROGRAMS=( ${POWERVC_TOOL} awk cut ip tmux tr )
for PROGRAM in ${PROGRAMS[@]}
do
	echo "Checking for program ${PROGRAM}"
	if ! hash ${PROGRAM} 1>/dev/null 2>&1
	then
		echo "Error: Missing ${PROGRAM} program!"
		exit 1
	fi
done

TMUX_SESSION=$(tmux list-sessions | cut -f1 -d' ')
${DEBUG} && echo "TMUX_SESSION=${TMUX_SESSION}"

LINK=$(ip link | awk -F: '$0 !~ "lo|vir|^[^0-9]"{print $2a;getline}' | tr -d '[:space:]')
${DEBUG} && echo "LINK=${LINK}"

POWERVC_CMD=$(cat << __EOF__
${POWERVC_TOOL} watch-installation --cloud "${CLOUD}" --domainName "${BASEDOMAIN}" --bastionMetadata "${HOME}" --bastionUsername "${BASTION_USERNAME}" --bastionRsa "${BASTION_RSA}" --enableDhcpd true --dhcpInterface "${LINK}" --dhcpSubnet "${DHCP_SUBNET}" --dhcpNetmask "${DHCP_NETMASK}" --dhcpRouter "${DHCP_ROUTER}" --dhcpDnsServers "${DHCP_DNS_SERVERS}" --dhcpServerId "${DHCP_SERVER_ID}" --shouldDebug true 2>&1 | tee "output-$(date +%Y-%m-%d-%H-%M-%S)"
__EOF__
)
${DEBUG} && echo "POWERVC_CMD=${POWERVC_CMD}"

while true
do
	echo "Checking if server ${CONTROLLER_IP} is alive..."
	${POWERVC_TOOL} check-alive --serverIP "${CONTROLLER_IP}"
	RC=$?
	if [ ${RC} -gt 0 ]
	then
		echo "Server is down!"
		tmux send-keys -t "${TMUX_SESSION}0" "${POWERVC_CMD}" C-m

	fi

	sleep 60s
done
