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

// Package main provides load balancer management functionality for OpenShift clusters.
//
// This file implements the LoadBalancer component which manages HAProxy-based
// load balancing on bastion hosts. It provides functionality to check the status
// of the load balancer configuration and service on the cluster's bastion host.
//
// The LoadBalancer implements the RunnableObject interface and integrates with
// the cluster lifecycle management system. It uses SSH to connect to the bastion
// host and inspect HAProxy configuration and service status.
//
// Key Features:
//   - Check bastion host connectivity via SSH
//   - Retrieve HAProxy configuration
//   - Check HAProxy service status
//   - Integration with OpenStack server discovery

package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"
)

const (
	// LoadBalancerName is the display name for the load balancer component
	LoadBalancerName = "Load Balancer"

	// HAProxy configuration and service constants
	haproxyConfigPath     = "/etc/haproxy/haproxy.cfg"
	haproxyService        = "haproxy.service"
	haproxyConfigPerms    = "646"
	haproxySelinuxSetting = "haproxy_connect_any"
	haproxyPackageName    = "haproxy"
	haproxyServiceName    = "haproxy.service"

	// SSH command constants
	sshKeyscanCmd = "ssh-keyscan"
	sshCmd        = "ssh"
	sudoCmd       = "sudo"
	catCmd        = "cat"
	systemctlCmd  = "systemctl"

	// systemctl command options
	systemctlStatusCmd = "status"
	systemctlNoPager   = "--no-pager"
	systemctlLongLines = "-l"

	// SSH command options
	sshIdentityFlag = "-i"

	// Retry configuration constants
	maxRetries        = 3
	initialRetryDelay = 2 * time.Second
	maxRetryDelay     = 30 * time.Second
	retryMultiplier   = 2.0
)

// LoadBalancer manages HAProxy-based load balancing on the cluster's bastion host.
//
// It implements the RunnableObject interface and provides functionality to check
// the status of the load balancer configuration and service. The LoadBalancer
// connects to the bastion host via SSH to inspect HAProxy configuration and
// service status.
//
// Fields:
//   - services: Provides access to cluster services and configuration
type LoadBalancer struct {
	services *Services
}

// NewLoadBalancer creates a new LoadBalancer instance wrapped as a RunnableObject.
//
// This is the primary constructor that returns the LoadBalancer as a RunnableObject
// interface, making it compatible with the cluster lifecycle management system.
//
// Parameters:
//   - services: The services instance providing cluster configuration (must not be nil)
//
// Returns:
//   - []RunnableObject: Array containing the LoadBalancer as a RunnableObject
//   - []error: Array of errors (currently always contains one nil error)
//
// The function internally calls innerNewLoadBalancer and converts the result
// to the RunnableObject interface.
func NewLoadBalancer(services *Services) ([]RunnableObject, []error) {
	var (
		lbs  []*LoadBalancer
		errs []error
		ros  []RunnableObject
	)

	if services == nil {
		return nil, []error{fmt.Errorf("services cannot be nil")}
	}

	lbs, errs = innerNewLoadBalancer(services)

	ros = make([]RunnableObject, len(lbs))
	// Go does not support type converting the entire array.
	// So we do it manually.
	for i, v := range lbs {
		ros[i] = RunnableObject(v)
	}

	return ros, errs
}

// NewLoadBalancerAlt creates a new LoadBalancer instance without interface wrapping.
//
// This alternative constructor returns the LoadBalancer directly as a pointer,
// without wrapping it in the RunnableObject interface. This is useful when
// direct access to LoadBalancer methods is needed.
//
// Parameters:
//   - services: The services instance providing cluster configuration (must not be nil)
//
// Returns:
//   - []*LoadBalancer: Array containing the LoadBalancer pointer
//   - []error: Array of errors (currently always contains one nil error)
func NewLoadBalancerAlt(services *Services) ([]*LoadBalancer, []error) {
	if services == nil {
		return nil, []error{fmt.Errorf("services cannot be nil")}
	}
	return innerNewLoadBalancer(services)
}

// innerNewLoadBalancer is the internal constructor that creates the LoadBalancer instance.
//
// This function performs the actual LoadBalancer creation and is called by both
// NewLoadBalancer and NewLoadBalancerAlt constructors.
//
// Parameters:
//   - services: The services instance providing cluster configuration
//
// Returns:
//   - []*LoadBalancer: Array containing the LoadBalancer pointer
//   - []error: Array of errors (currently always contains one nil error)
func innerNewLoadBalancer(services *Services) ([]*LoadBalancer, []error) {
	var (
		lbs  []*LoadBalancer
		errs []error
	)

	lbs = make([]*LoadBalancer, 1)
	errs = make([]error, 1)

	lbs[0] = &LoadBalancer{
		services: services,
	}

	return lbs, errs
}

// Name returns the display name of the LoadBalancer component.
//
// This method implements part of the RunnableObject interface.
//
// Returns:
//   - string: The name "Load Balancer"
//   - error: Always nil (no errors possible)
func (lbs *LoadBalancer) Name() (string, error) {
	return LoadBalancerName, nil
}

// ObjectName returns the object name of the LoadBalancer component.
//
// This method implements part of the RunnableObject interface and returns
// the same value as Name().
//
// Returns:
//   - string: The name "Load Balancer"
//   - error: Always nil (no errors possible)
func (lbs *LoadBalancer) ObjectName() (string, error) {
	return LoadBalancerName, nil
}

// Run executes the LoadBalancer's main operation.
//
// This method implements part of the RunnableObject interface. For LoadBalancer,
// no action is required during the Run phase as the load balancer is configured
// during cluster creation.
//
// Returns:
//   - error: Always nil (no operation performed)
func (lbs *LoadBalancer) Run() error {
	// Nothing needs to be done here.
	return nil
}

// ClusterStatus checks and displays the status of the load balancer on the bastion host.
//
// This method performs the following operations:
//  1. Finds the bastion server in OpenStack
//  2. Checks SSH connectivity to the bastion host
//  3. Retrieves and displays the HAProxy configuration
//  4. Retrieves and displays the HAProxy service status
//
// The method uses SSH to connect to the bastion host and execute commands.
// All output is printed to stdout, and errors are printed but do not cause
// the program to exit.
//
// This method implements part of the RunnableObject interface.
func (lbs *LoadBalancer) ClusterStatus() {
	var (
		ctx         context.Context
		cancel      context.CancelFunc
		clusterName string
		cloud       string
		server      servers.Server
		ipAddress   string
		outb        []byte
		outs        string
		err         error
	)

	// Validate services field
	if lbs.services == nil {
		fmt.Printf("%s: Error: services is nil\n", LoadBalancerName)
		return
	}

	ctx, cancel = lbs.services.GetContextWithTimeout()
	defer cancel()

	clusterName = lbs.services.GetMetadata().GetClusterName()
	log.Debugf("ClusterStatus: clusterName = %s", clusterName)
	if clusterName == "" {
		fmt.Printf("%s: Error: cluster name is empty\n", LoadBalancerName)
		return
	}

	cloud = lbs.services.GetMetadata().GetCloud()
	log.Debugf("ClusterStatus: cloud = %s", cloud)
	if cloud == "" {
		fmt.Printf("%s: Error: cloud name is empty\n", LoadBalancerName)
		return
	}

	log.Printf("[INFO] Finding bastion server for cluster '%s'", clusterName)
	server, err = findServer(ctx, []string{ cloud }, clusterName)
	if err != nil {
		fmt.Printf("%s: Error: failed to find bastion server: %v\n", LoadBalancerName, err)
		return
	}
	log.Debugf("ClusterStatus: FOUND server = %s", server.Name)

	_, ipAddress, err = findIpAddress(server)
	if err != nil {
		fmt.Printf("%s: Error: failed to find IP address: %v\n", LoadBalancerName, err)
		return
	}
	if ipAddress == "" {
		fmt.Printf("%s: Error: IP address is empty\n", LoadBalancerName)
		return
	}
	log.Debugf("ClusterStatus: ipAddress = %s", ipAddress)

	// Check SSH connectivity using ssh-keyscan with retry logic
	log.Printf("[INFO] Checking SSH connectivity to bastion at %s", ipAddress)
	err = retrySshWithBackoff(func() error {
		var sshErr error
		outb, sshErr = runSplitCommand2([]string{
			sshKeyscanCmd,
			ipAddress,
		})
		outs = strings.TrimSpace(string(outb))
		log.Debugf("ClusterStatus: ssh-keyscan output = \"%s\"", outs)

		var exitError *exec.ExitError
		if errors.As(sshErr, &exitError) {
			log.Debugf("ClusterStatus: ssh-keyscan exit code = %d", exitError.ExitCode())
		}

		if outs == "" {
			return fmt.Errorf("bastion host is not responding to SSH")
		}
		return nil
	}, "SSH connectivity check")

	if err != nil {
		fmt.Printf("%s: Error: %v\n", LoadBalancerName, err)
		return
	}
	fmt.Printf("%s: Cluster bastion is alive\n", LoadBalancerName)

	// Add bastion to known hosts
	log.Printf("[INFO] Adding bastion to known hosts")
	err = addServerKnownHosts(ctx, ipAddress)
	if err != nil {
		fmt.Printf("%s: Error: failed to add server to known hosts: %v\n", LoadBalancerName, err)
		return
	}

	// Retrieve HAProxy configuration with retry logic
	log.Printf("[INFO] Retrieving HAProxy configuration from bastion")
	installerRsa := lbs.services.GetInstallerRsa()
	if installerRsa == "" {
		fmt.Printf("%s: Error: installer RSA key path is empty\n", LoadBalancerName)
		return
	}
	bastionUsername := lbs.services.GetBastionUsername()
	if bastionUsername == "" {
		fmt.Printf("%s: Error: bastion username is empty\n", LoadBalancerName)
		return
	}

	err = retrySshWithBackoff(func() error {
		var configErr error
		outb, configErr = runSplitCommand2([]string{
			sshCmd,
			sshIdentityFlag,
			installerRsa,
			fmt.Sprintf("%s@%s", bastionUsername, ipAddress),
			sudoCmd,
			catCmd,
			haproxyConfigPath,
		})
		if configErr != nil {
			return fmt.Errorf("failed to retrieve HAProxy configuration: %w", configErr)
		}
		outs = strings.TrimSpace(string(outb))
		if outs == "" {
			return fmt.Errorf("HAProxy configuration is empty")
		}
		return nil
	}, "HAProxy configuration retrieval")

	if err != nil {
		fmt.Printf("%s: Error: %v\n", LoadBalancerName, err)
		return
	}
	fmt.Printf("%s: Cluster bastion has the following config:\n", LoadBalancerName)
	fmt.Println(outs)

	// Retrieve HAProxy service status with retry logic
	log.Printf("[INFO] Retrieving HAProxy service status from bastion")
	err = retrySshWithBackoff(func() error {
		var statusErr error
		outb, statusErr = runSplitCommand2([]string{
			sshCmd,
			sshIdentityFlag,
			installerRsa,
			fmt.Sprintf("%s@%s", bastionUsername, ipAddress),
			sudoCmd,
			systemctlCmd,
			systemctlStatusCmd,
			haproxyService,
			systemctlNoPager,
			systemctlLongLines,
		})
		if statusErr != nil {
			return fmt.Errorf("failed to retrieve HAProxy service status: %w", statusErr)
		}
		outs = strings.TrimSpace(string(outb))
		if outs == "" {
			return fmt.Errorf("HAProxy service status is empty")
		}
		return nil
	}, "HAProxy service status retrieval")

	if err != nil {
		fmt.Printf("%s: Error: %v\n", LoadBalancerName, err)
		return
	}

	fmt.Printf("%s: Cluster bastion has the following status:\n", LoadBalancerName)
	fmt.Println(outs)
}

// Priority returns the execution priority of the LoadBalancer component.
//
// This method implements part of the RunnableObject interface. A priority of -1
// indicates that the LoadBalancer has no specific ordering requirement relative
// to other components.
//
// Returns:
//   - int: Priority value (-1 for no specific priority)
//   - error: Always nil (no errors possible)
func (lbs *LoadBalancer) Priority() (int, error) {
	return -1, nil
}
