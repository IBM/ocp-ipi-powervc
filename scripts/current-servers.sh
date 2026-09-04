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

# Usage: ./current-servers.sh [-c <cloud>|--cloud <cloud>] [-i|--show-ips]
#
# Lists OpenStack servers grouped by cluster and standalone VMs.
# The cloud name is taken from -c <cloud>, the CLOUD env var, or OS_CLOUD.
# Pass -i / --show-ips to include IP addresses in the output.

set -euo pipefail

# ---------- argument parsing ----------
SHOW_IPS=false

usage() {
  cat >&2 <<EOF
Usage: $0 [-c <cloud>|--cloud <cloud>] [-i|--show-ips]
  -c / --cloud <cloud>   OpenStack cloud name (overrides \$CLOUD / \$OS_CLOUD)
  -i / --show-ips        Show IP addresses
  -h                     Show this help text
EOF
  exit 1
}

# Pre-process long options into short ones
args=()
while [[ $# -gt 0 ]]; do
  case "$1" in
    --cloud)    args+=("-c" "${2?Error: --cloud requires an argument.}"); shift 2 ;;
    --show-ips) args+=("-i"); shift ;;
    --)         args+=("--"); shift; break ;;
    *)          args+=("$1"); shift ;;
  esac
done
set -- "${args[@]}"

while getopts ":c:ih" opt; do
  case $opt in
    c) CLOUD="$OPTARG" ;;
    i) SHOW_IPS=true ;;
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
csv_data=$(openstack --os-cloud="${CLOUD}" server list --format=csv -c ID -c Name -c Status -c Networks -c Image -c Flavor -c "Created At") || {
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
declare -A cluster_created         # earliest creation time per cluster
declare -a cluster_order
declare -a standalone_names
declare -a standalone_ips
declare -a standalone_statuses
declare -a standalone_images
declare -a standalone_created
standalone_max_name=4              # minimum width = length of "Name" header

while IFS=',' read -r id name status networks image flavor created_at; do
  name="${name//\"/}"
  status="${status//\"/}"
  image="${image//\"/}"
  flavor="${flavor//\"/}"
  created_at="${created_at//\"/}"
  ip=$(grep -oE '[0-9]+(\.[0-9]+){3}' <<< "$networks" | head -1 || true)

  if [[ "$name" =~ ^p-[a-f0-9-]+-([a-z0-9]+)-(master|worker|bootstrap) ]]; then
    cluster_id="${BASH_REMATCH[1]}"
    node="${name##*${cluster_id}-}"

    if [[ -z "${cluster_images[$cluster_id]+set}" ]]; then
      cluster_images[$cluster_id]="$image"
      cluster_status[$cluster_id]="ACTIVE"
      cluster_created[$cluster_id]="$created_at"
      cluster_order+=("$cluster_id")
    fi

    # Flag cluster if any node is not ACTIVE
    if [[ "$status" != "ACTIVE" ]]; then
      cluster_status[$cluster_id]="$status"
    fi

    if [[ "$SHOW_IPS" == true ]]; then
      cluster_nodes[$cluster_id]+="    $(printf "%-30s  %-18s  %-22s  %s" "$node" "$ip" "$created_at" "$status")"$'\n'
    else
      cluster_nodes[$cluster_id]+="    $(printf "%-30s  %-22s  %s" "$node" "$created_at" "$status")"$'\n'
    fi
  else
    standalone_names+=("$name")
    standalone_ips+=("$ip")
    standalone_statuses+=("$status")
    standalone_images+=("$image")
    standalone_created+=("$created_at")
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
  echo "  Created : ${cluster_created[$cid]}"
  echo "  Image   : ${cluster_images[$cid]}"
  echo "  Nodes   :"
  printf "%s" "${cluster_nodes[$cid]}"
done

W=$standalone_max_name
if [[ "$SHOW_IPS" == true ]]; then
  sep_len=$(( W + 18 + 10 + 50 + 22 + 8 ))
else
  sep_len=$(( W + 10 + 50 + 22 + 6 ))
fi
echo ""
echo "STANDALONE / BASTION VMs (${standalone_count})"
echo "========================"
if [[ "$SHOW_IPS" == true ]]; then
  printf "  %-${W}s  %-18s  %-10s  %-22s  %s\n" "Name" "IP" "Status" "Created At" "Image"
else
  printf "  %-${W}s  %-10s  %-22s  %s\n" "Name" "Status" "Created At" "Image"
fi
printf '  '
printf '─%.0s' $(seq 1 "$sep_len")
printf '\n'
for i in "${!standalone_names[@]}"; do
  if [[ "$SHOW_IPS" == true ]]; then
    printf "  %-${W}s  %-18s  %-10s  %-22s  %s\n" \
      "${standalone_names[$i]}" "${standalone_ips[$i]}" \
      "${standalone_statuses[$i]}" "${standalone_created[$i]}" \
      "${standalone_images[$i]}"
  else
    printf "  %-${W}s  %-10s  %-22s  %s\n" \
      "${standalone_names[$i]}" \
      "${standalone_statuses[$i]}" "${standalone_created[$i]}" \
      "${standalone_images[$i]}"
  fi
done
echo ""
