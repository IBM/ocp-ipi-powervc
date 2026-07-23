#!/usr/bin/env bash

# Copyright 2025 IBM Corp
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#	http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# Usage: ./current-servers.sh [-c <cloud>]
#
# Lists OpenStack servers grouped by cluster and standalone VMs.
# The cloud name is taken from -c <cloud>, the CLOUD env var, or OS_CLOUD.

set -euo pipefail

# ---------- argument parsing ----------
usage() {
  cat >&2 <<EOF
Usage: $0 [-c <cloud>]
  -c <cloud>   OpenStack cloud name (overrides \$CLOUD / \$OS_CLOUD)
  -h           Show this help text
EOF
  exit 1
}

while getopts ":c:h" opt; do
  case $opt in
    c) CLOUD="$OPTARG" ;;
    h) usage ;;
    :) echo "Error: -$OPTARG requires an argument." >&2; usage ;;
    \?) echo "Error: unknown option -$OPTARG." >&2; usage ;;
  esac
done

# Fall back to OS_CLOUD if CLOUD is still unset
CLOUD="${CLOUD:-${OS_CLOUD:-}}"

if [[ -z "$CLOUD" ]]; then
  echo "Error: cloud name not set. Use -c <cloud>, or set \$CLOUD / \$OS_CLOUD." >&2
  exit 1
fi

# ---------- check dependencies ----------
for cmd in openstack grep; do
  if ! command -v "$cmd" &>/dev/null; then
    echo "Error: required command '$cmd' not found." >&2
    exit 1
  fi
done

# ---------- fetch data ----------
echo "Fetching server list from cloud: ${CLOUD} ..." >&2
csv_data=$(openstack --os-cloud="${CLOUD}" server list --format=csv) || {
  echo "Error: openstack command failed." >&2
  exit 1
}

if [[ $(printf '%s\n' "$csv_data" | wc -l) -le 1 ]]; then
  echo "No servers found." >&2
  exit 0
fi

# ---------- parse ----------
declare -A cluster_images
declare -A cluster_nodes
declare -A cluster_status          # tracks any non-ACTIVE node per cluster
declare -a cluster_order
declare -a standalone_names
declare -a standalone_ips
declare -a standalone_statuses
declare -a standalone_images
standalone_max_name=4              # minimum width = length of "Name" header

while IFS=',' read -r id name status networks image flavor; do
  name="${name//\"/}"
  status="${status//\"/}"
  image="${image//\"/}"
  flavor="${flavor//\"/}"
  ip=$(grep -oE '[0-9]+(\.[0-9]+){3}' <<< "$networks" | head -1 || true)

  if [[ "$name" =~ ^p-[a-f0-9-]+-([a-z0-9]+)-(master|worker|bootstrap) ]]; then
    cluster_id="${BASH_REMATCH[1]}"
    node="${name##*${cluster_id}-}"

    if [[ -z "${cluster_images[$cluster_id]+set}" ]]; then
      cluster_images[$cluster_id]="$image"
      cluster_status[$cluster_id]="ACTIVE"
      cluster_order+=("$cluster_id")
    fi

    # Flag cluster if any node is not ACTIVE
    if [[ "$status" != "ACTIVE" ]]; then
      cluster_status[$cluster_id]="$status"
    fi

    cluster_nodes[$cluster_id]+="    $(printf "%-30s  %-18s  %s" "$node" "$ip" "$status")"$'\n'
  else
    standalone_names+=("$name")
    standalone_ips+=("$ip")
    standalone_statuses+=("$status")
    standalone_images+=("$image")
    (( ${#name} > standalone_max_name )) && standalone_max_name=${#name}
  fi
done < <(tail -n +2 <<< "$csv_data")

# ---------- output ----------
cluster_count=0
standalone_count=0

if [[ -v cluster_order[0] ]]; then
  cluster_count=${#cluster_order[@]}
fi

if [[ -v standalone_names[0] ]]; then
  standalone_count=${#standalone_names[@]}
fi

echo ""
echo "CLUSTERS (${cluster_count})"
echo "========"
for cid in "${cluster_order[@]}"; do
  overall="${cluster_status[$cid]}"
  [[ "$overall" == "ACTIVE" ]] && flag="" || flag="  [!] has non-ACTIVE nodes"
  echo ""
  echo "  Cluster : $cid${flag}"
  echo "  Image   : ${cluster_images[$cid]}"
  echo "  Nodes   :"
  printf "%s" "${cluster_nodes[$cid]}"
done

W=$standalone_max_name
sep_len=$(( W + 18 + 10 + 50 + 8 ))
echo ""
echo "STANDALONE / BASTION VMs (${standalone_count})"
echo "========================"
printf "  %-${W}s  %-18s  %-10s  %s\n" "Name" "IP" "Status" "Image"
printf '  '
printf '─%.0s' $(seq 1 "$sep_len")
printf '\n'
for i in "${!standalone_names[@]}"; do
  printf "  %-${W}s  %-18s  %-10s  %s\n" \
    "${standalone_names[$i]}" "${standalone_ips[$i]}" \
    "${standalone_statuses[$i]}" "${standalone_images[$i]}"
done
echo ""
