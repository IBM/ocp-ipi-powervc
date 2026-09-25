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

set -euo pipefail

# Allow passing custom namespace pattern as first argument (default: ^e2e-pod-network-disruption-test-)
NS_PATTERN="${1:-^e2e-pod-network-disruption-test-}"

# Find all matching namespaces
NAMESPACES=$(oc get namespaces -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' | grep -E "$NS_PATTERN" || true)

if [ -z "$NAMESPACES" ]; then
  echo "No namespaces found matching pattern '$NS_PATTERN'."
  exit 0
fi

for ns in $NAMESPACES; do
  echo "=== Processing namespace: $ns ==="

  # 1. Delete higher-level controllers managing the pods so they are not recreated
  echo "Deleting workloads (deployments, daemonsets, statefulsets, jobs, replicasets, cronjobs) in $ns..."
  oc delete deployment,daemonset,statefulset,job,replicaset,cronjob --all -n "$ns" --cascade=foreground --timeout=30s 2>/dev/null || true

  # 2. Force delete all remaining pods
  echo "Force deleting all pods in $ns..."
  oc delete pods --all -n "$ns" --force --grace-period=0 2>/dev/null || true

  # 3. Strip finalizers for any remaining/terminating pods
  STUCK_PODS=$(oc get pods -n "$ns" -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' 2>/dev/null || true)
  if [ -n "$STUCK_PODS" ]; then
    for pod in $STUCK_PODS; do
      echo "Stripping finalizers for pod: $ns/$pod"
      oc patch pod "$pod" -n "$ns" -p '{"metadata":{"finalizers":null}}' --type=merge 2>/dev/null || true
    done
  fi

  # 4. Strip finalizers on the namespace itself if it gets stuck terminating
  echo "Deleting namespace $ns..."
  oc delete namespace "$ns" --wait=false 2>/dev/null || true

  # Check if namespace is stuck Terminating and clear spec.finalizers if needed
  NS_STATUS=$(oc get namespace "$ns" -o jsonpath='{.status.phase}' 2>/dev/null || true)
  if [ "$NS_STATUS" == "Terminating" ]; then
    echo "Namespace $ns is Terminating; clearing namespace finalizers..."
    oc get namespace "$ns" -o json \
      | tr -d "\r\n" \
      | sed -e 's/"finalizers": \[[^]]*\]/"finalizers": []/' \
      | oc replace --raw "/api/v1/namespaces/$ns/finalize" -f - 2>/dev/null || true
  fi
done

echo "Done cleaning up namespaces matching '$NS_PATTERN'."
