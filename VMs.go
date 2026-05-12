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
	"context"
	"fmt"
	"strings"

	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/hypervisors"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"
)

// Note: This file uses the global 'log' variable declared in PowerVC-Tool.go
// and functions from OpenStack.go (getAllServers, getAllHypervisors, findHypervisorInList)
// and Utils.go (findIpAddress, keyscanServer).

const (
	// VMsName is the display name for the Virtual Machines service
	VMsName = "Virtual Machines"

	// Status constants for various "not available" scenarios
	statusNotAvailable = "N/A"

	// SSH status constants
	sshStatusNA    = statusNotAvailable
	sshStatusAlive = "ALIVE"
	sshStatusDead  = "DEAD"

	// Network status constants
	networkStatusNA = statusNotAvailable

	// Hypervisor status constants
	hypervisorStatusNA = statusNotAvailable
)

// VMs manages virtual machine status checking for OpenShift cluster nodes.
// It implements the RunnableObject interface for cluster lifecycle management.
type VMs struct {
	// services provides access to cluster configuration and API clients
	services *Services
}

// NewVMs creates a new VMs instance and returns it as a RunnableObject.
// This is the primary constructor used by the framework.
//
// Parameters:
//   - services: Services instance containing configuration and API clients
//
// Returns:
//   - []RunnableObject: Array containing the VMs instance as a RunnableObject
//   - []error: Array of errors encountered during initialization
func NewVMs(services *Services) ([]RunnableObject, []error) {
	var (
		vms  []*VMs
		errs []error
		ros  []RunnableObject
	)

	vms, errs = innerNewVMs(services)

	ros = make([]RunnableObject, len(vms))
	// Go does not support type converting the entire array.
	// So we do it manually.
	for i, v := range vms {
		ros[i] = RunnableObject(v)
	}

	return ros, errs
}

// NewVMsAlt creates a new VMs instance and returns it directly.
// This is an alternative constructor that returns the concrete type.
//
// Parameters:
//   - services: Services instance containing configuration and API clients
//
// Returns:
//   - []*VMs: Array containing the VMs instance
//   - []error: Array of errors encountered during initialization
func NewVMsAlt(services *Services) ([]*VMs, []error) {
	return innerNewVMs(services)
}

// innerNewVMs is the internal constructor that initializes the VMs instance.
//
// Parameters:
//   - services: Services instance containing configuration and API clients
//
// Returns:
//   - []*VMs: Array containing the initialized VMs instance
//   - []error: Array of errors encountered during initialization (currently always contains one nil error)
func innerNewVMs(services *Services) ([]*VMs, []error) {
	var (
		vms  []*VMs
		errs []error
	)

	vms = make([]*VMs, 1)
	errs = make([]error, 1)

	vms[0] = &VMs{
		services: services,
	}

	log.Debugf("innerNewVMs: Created VMs object")
	return vms, errs
}

// Name returns the display name of the VMs service.
// This implements the RunnableObject interface.
//
// Returns:
//   - string: The service name (VMsName)
//   - error: Always nil for this implementation
func (vms *VMs) Name() (string, error) {
	return VMsName, nil
}

// ObjectName returns the object name of the VMs service.
// This implements the RunnableObject interface.
//
// Returns:
//   - string: The service name (VMsName)
//   - error: Always nil for this implementation
func (vms *VMs) ObjectName() (string, error) {
	return VMsName, nil
}

// Run executes the VMs service operations.
// This implements the RunnableObject interface.
// Currently, no operations are performed during the run phase.
//
// Returns:
//   - error: Always nil for this implementation
func (vms *VMs) Run() error {
	// Nothing needs to be done here.
	log.Debugf("Run: VMs service run (no-op)")
	return nil
}

// ClusterStatus checks and displays the status of all virtual machines in the cluster.
// It retrieves all servers and hypervisors, then displays detailed information about
// each VM that belongs to the cluster, including:
//   - Server status and power state
//   - MAC and IP addresses
//   - SSH connectivity status
//   - Hypervisor placement
//
// This implements the RunnableObject interface.
// Errors from individual operations are logged but don't stop execution.
func (vms *VMs) ClusterStatus() error {
	if vms == nil || vms.services == nil {
		fmt.Printf("%s is NOTOK. It has not been initialized.\n", VMsName)
		return fmt.Errorf("ClusterStatus: VMs or services is nil")
	}

	metadata := vms.services.GetMetadata()
	if metadata == nil {
		fmt.Printf("%s is NOTOK. Metadata is not available.\n", VMsName)
		return fmt.Errorf("ClusterStatus: Metadata is nil")
	}

	var (
		ctx            context.Context
		cancel         context.CancelFunc
		connCompute    *gophercloud.ServiceClient
		infraID        string
		allServers     []servers.Server
		server         servers.Server
		allHypervisors []hypervisors.Hypervisor
		err            error
	)

	cloud := vms.services.GetCloud()
	if cloud == "" {
		fmt.Printf("%s is NOTOK. Cloud configuration is empty.\n", VMsName)
		return fmt.Errorf("ClusterStatus: Cloud configuration is empty")
	}

	infraID = metadata.GetInfraID()
	if infraID == "" {
		fmt.Printf("%s is NOTOK. Infrastructure ID is empty.\n", VMsName)
		return fmt.Errorf("ClusterStatus: InfraID is empty")
	}
	log.Debugf("ClusterStatus: infraID = %s", infraID)

	log.Debugf("ClusterStatus: Checking VMs status for cloud %s", cloud)

	// Create context after validation checks, just before first use
	ctx, cancel = vms.services.GetContextWithTimeout()
	if ctx == nil || cancel == nil {
		fmt.Printf("%s is NOTOK. Failed to get context with timeout.\n", VMsName)
		return fmt.Errorf("ClusterStatus: GetContextWithTimeout returned nil context or cancel function")
	}
	defer cancel()

	connCompute, err = NewServiceClient(ctx, "compute", DefaultClientOpts(cloud))
	if err != nil {
		fmt.Printf("%s is NOTOK. Failed to create compute service client: %v\n", VMsName, err)
		return fmt.Errorf("ClusterStatus: NewServiceClient returned error: %v", err)
	}

	allServers, err = getAllServers(ctx, []string{ cloud })
	if err != nil {
		fmt.Printf("%s is NOTOK. Failed to get servers: %v\n", VMsName, err)
		return fmt.Errorf("ClusterStatus: getAllServers returned error: %v", err)
	}
	log.Debugf("ClusterStatus: Retrieved %d servers", len(allServers))

	allHypervisors, err = getAllHypervisors(ctx, connCompute)
	if err != nil {
		fmt.Printf("%s is NOTOK. Failed to get hypervisors: %v\n", VMsName, err)
		return fmt.Errorf("ClusterStatus: getAllHypervisors returned error: %v", err)
	}
	log.Debugf("ClusterStatus: Retrieved %d hypervisors", len(allHypervisors))

	fmt.Println("8<--------8<--------8<--------8<--------8<--------8<--------8<--------8<--------")

	clusterServerCount := 0
	for _, server = range allServers {
		var (
			macAddress string
			ipAddress  string
			sshAlive   = sshStatusNA
			hypervisor hypervisors.Hypervisor
		)

		if !strings.HasPrefix(strings.ToLower(server.Name), strings.ToLower(infraID)) {
			log.Debugf("ClusterStatus: SKIPPING server = %s (not part of cluster)", server.Name)
			continue
		}
		log.Debugf("ClusterStatus: FOUND cluster server = %s", server.Name)
		clusterServerCount++

		macAddress, ipAddress, err = findIpAddress(server)
		if err != nil {
			log.Debugf("ClusterStatus: findIpAddress for server %s returned error: %v", server.Name, err)
			// Continue to show server info even without IP address
			macAddress = networkStatusNA
			ipAddress = networkStatusNA
		} else {
			log.Debugf("ClusterStatus: findIpAddress for server %s returned %s and %s", server.Name, macAddress, ipAddress)
		}

		if ipAddress != networkStatusNA {
			sshAlive = sshStatusDead

			var outb []byte
			outb, err = keyscanServer(ctx, ipAddress, true)
			if err == nil && len(outb) > 0 {
				sshAlive = sshStatusAlive
				log.Debugf("ClusterStatus: SSH is alive for server %s at %s", server.Name, ipAddress)
			} else {
				log.Debugf("ClusterStatus: SSH check failed for server %s at %s: %v", server.Name, ipAddress, err)
			}
		}

		hypervisorName := hypervisorStatusNA

		if server.HypervisorHostname != "" {
			log.Debugf("ClusterStatus: server.HypervisorHostname = %s", server.HypervisorHostname)
			hypervisor, err = findHypervisorInList(allHypervisors, server.HypervisorHostname)
			if err != nil {
				log.Debugf("ClusterStatus: findHypervisorInList for %s returned error: %v", server.HypervisorHostname, err)
			} else {
				log.Debugf("ClusterStatus: Found hypervisor %s with HostIP %s", hypervisor.HypervisorHostname, hypervisor.HostIP)
				hypervisorName = hypervisor.HypervisorHostname
			}
		} else {
			log.Debugf("ClusterStatus: server %s has no hypervisor hostname", server.Name)
		}

		fmt.Printf("%s: %s has status (%s), power state (%s), MAC address (%s), IP address (%s), and ssh status (%s), and hypervisor (%s)\n",
			VMsName,
			server.Name,
			server.Status,
			server.PowerState.String(),
			macAddress,
			ipAddress,
			sshAlive,
			hypervisorName,
		)
		fmt.Println()
	}

	log.Debugf("ClusterStatus: Found %d cluster servers out of %d total servers", clusterServerCount, len(allServers))

	if clusterServerCount == 0 {
		fmt.Printf("%s: Warning: No servers found for cluster with infraID %s\n", VMsName, infraID)
	}

	return nil
}

// Priority returns the execution priority for this service.
// This implements the RunnableObject interface.
// A priority of -1 indicates this service has no specific ordering requirement.
//
// Returns:
//   - int: Priority value (-1 for no specific priority)
//   - error: Always nil for this implementation
func (vms *VMs) Priority() (int, error) {
	return -1, nil
}
