// Copyright 2025 IBM Corp
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"fmt"
)

// Note: This file uses the global 'log' variable declared in PowerVC-Tool.go
// and the runCommand/runTwoCommands functions defined in Run.go

const (
	// OcName is the display name for the OpenShift cluster object
	OcName = "OpenShiftCluster"
)

// Oc represents an OpenShift cluster and provides methods to check its status.
// It implements the RunnableObject interface for cluster lifecycle management.
type Oc struct {
	// services provides access to cluster configuration and API clients
	services *Services
}

// NewOc creates a new Oc instance and returns it as a RunnableObject.
// This is the primary constructor used by the framework.
//
// Parameters:
//   - services: Services instance containing configuration and API clients
//
// Returns:
//   - []RunnableObject: Array containing the Oc instance as a RunnableObject
//   - []error: Array of errors encountered during initialization
func NewOc(services *Services) ([]RunnableObject, []error) {
	ocs, errs := innerNewOc(services)

	ros := make([]RunnableObject, len(ocs))
	// Go does not support type converting the entire array.
	// So we do it manually.
	for i, v := range ocs {
		ros[i] = RunnableObject(v)
	}

	return ros, errs
}

// NewOcAlt creates a new Oc instance and returns it directly.
// This is an alternative constructor that returns the concrete type.
//
// Parameters:
//   - services: Services instance containing configuration and API clients
//
// Returns:
//   - []*Oc: Array containing the Oc instance
//   - []error: Array of errors encountered during initialization
func NewOcAlt(services *Services) ([]*Oc, []error) {
	return innerNewOc(services)
}

// innerNewOc is the internal constructor that initializes the Oc instance.
//
// Parameters:
//   - services: Services instance containing configuration and API clients
//
// Returns:
//   - []*Oc: Array containing the initialized Oc instance
//   - []error: Array of errors encountered during initialization
func innerNewOc(services *Services) ([]*Oc, []error) {
	ocs := make([]*Oc, 1)
	errs := make([]error, 1)

	ocs[0] = &Oc{
		services: services,
	}

	log.Debugf("innerNewOc: Created OpenShift cluster object")
	return ocs, errs
}

// Name returns the display name of the OpenShift cluster object.
// This implements the RunnableObject interface.
//
// Returns:
//   - string: The object name (OcName)
//   - error: Always nil for this implementation
func (oc *Oc) Name() (string, error) {
	return OcName, nil
}

// ObjectName returns the object name of the OpenShift cluster object.
// This implements the RunnableObject interface.
//
// Returns:
//   - string: The object name (OcName)
//   - error: Always nil for this implementation
func (oc *Oc) ObjectName() (string, error) {
	return OcName, nil
}

// Run executes the OpenShift cluster operations.
// This implements the RunnableObject interface.
// Currently, no operations are performed during the run phase.
//
// Returns:
//   - error: Always nil for this implementation
func (oc *Oc) Run() error {
	// Nothing needs to be done here.
	log.Debugf("Run: OpenShift cluster object run (no-op)")
	return nil
}

// ClusterStatus checks and displays the status of the OpenShift cluster.
// It runs a series of oc commands to gather information about:
//   - Cluster version and operators
//   - Nodes and their status
//   - Machine API resources
//   - Cloud controller manager
//   - Network and storage operators
//   - Pod status across namespaces
//   - Certificate signing requests
//
// This implements the RunnableObject interface.
// Errors from individual commands are logged but don't stop execution.
func (oc *Oc) ClusterStatus() {
	if oc == nil || oc.services == nil {
		fmt.Println("Error: OpenShift cluster object not initialized")
		log.Debugf("ClusterStatus: Oc or services is nil")
		return
	}

	kubeConfig := oc.services.GetKubeConfig()
	if kubeConfig == "" {
		fmt.Println("Error: KUBECONFIG path is empty")
		log.Debugf("ClusterStatus: KUBECONFIG is empty")
		return
	}

	log.Debugf("ClusterStatus: Checking OpenShift cluster status with KUBECONFIG=%s", kubeConfig)

	// Commands to check cluster status
	cmds := []string{
		"oc --request-timeout=5s get clusterversion",
		"oc --request-timeout=5s get co",
		"oc --request-timeout=5s get nodes -o=wide",
		"oc --request-timeout=5s get pods -n openshift-machine-api",
		"oc --request-timeout=5s get machines.machine.openshift.io -n openshift-machine-api",
		"oc --request-timeout=5s get machineset.machine.openshift.io -n openshift-machine-api",
		"oc --request-timeout=5s logs -l k8s-app=controller -c machine-controller -n openshift-machine-api",
		"oc --request-timeout=5s describe co/cloud-controller-manager",
		"oc --request-timeout=5s describe cm/cloud-provider-config -n openshift-config",
		"oc --request-timeout=5s get pods -n openshift-cloud-controller-manager-operator",
		"oc --request-timeout=5s get events -n openshift-cloud-controller-manager",
		"oc --request-timeout=5s -n openshift-cloud-controller-manager-operator logs deployment/cluster-cloud-controller-manager-operator -c cluster-cloud-controller-manager",
		"oc --request-timeout=5s get co/network",
		"oc --request-timeout=5s get co/kube-controller-manager",
		"oc --request-timeout=5s get co/etcd",
		"oc --request-timeout=5s get machines.machine.openshift.io -n openshift-machine-api",
		"oc --request-timeout=5s get machineset.m -n openshift-machine-api",
		"oc --request-timeout=5s get pods -n openshift-machine-api",
		"oc --request-timeout=5s get pods -n openshift-kube-controller-manager",
		"oc --request-timeout=5s get pods -n openshift-ovn-kubernetes",
		"oc --request-timeout=5s describe co/machine-config",
	}

	// Pipeline commands (cmd1 | cmd2)
	pipeCmds := [][]string{
		{
			"oc --request-timeout=5s get pods -A -o=wide",
			"sed -e /\\(Running\\|Completed\\)/d",
		},
		{
			"oc --request-timeout=5s get csr",
			"grep Pending",
		},
	}

	log.Debugf("ClusterStatus: Running %d single commands", len(cmds))
	successCount := 0
	failCount := 0

	for i, cmd := range cmds {
		log.Debugf("ClusterStatus: Running command %d/%d: %s", i+1, len(cmds), cmd)
		if err := runCommand(kubeConfig, cmd); err != nil {
			fmt.Printf("Error: could not run command: %v\n", err)
			log.Debugf("ClusterStatus: Command %d failed: %v", i+1, err)
			failCount++
		} else {
			successCount++
		}
	}

	log.Debugf("ClusterStatus: Single commands completed - success: %d, failed: %d", successCount, failCount)

	log.Debugf("ClusterStatus: Running %d pipeline commands", len(pipeCmds))
	pipeSuccessCount := 0
	pipeFailCount := 0

	for i, twoCmds := range pipeCmds {
		if len(twoCmds) != 2 {
			fmt.Printf("Error: invalid pipeline command at index %d (expected 2 commands, got %d)\n", i, len(twoCmds))
			log.Debugf("ClusterStatus: Invalid pipeline command at index %d", i)
			pipeFailCount++
			continue
		}

		log.Debugf("ClusterStatus: Running pipeline %d/%d: %s | %s", i+1, len(pipeCmds), twoCmds[0], twoCmds[1])
		if err := runTwoCommands(kubeConfig, twoCmds[0], twoCmds[1]); err != nil {
			fmt.Printf("Error: could not run pipeline command: %v\n", err)
			log.Debugf("ClusterStatus: Pipeline %d failed: %v", i+1, err)
			pipeFailCount++
		} else {
			pipeSuccessCount++
		}
	}

	log.Debugf("ClusterStatus: Pipeline commands completed - success: %d, failed: %d", pipeSuccessCount, pipeFailCount)
	log.Debugf("ClusterStatus: Total commands run: %d, total failed: %d", len(cmds)+len(pipeCmds), failCount+pipeFailCount)
}

// Priority returns the execution priority for this service.
// This implements the RunnableObject interface.
// A priority of -1 indicates this service has no specific ordering requirement.
//
// Returns:
//   - int: Priority value (-1 for no specific priority)
//   - error: Always nil for this implementation
func (oc *Oc) Priority() (int, error) {
	return -1, nil
}
