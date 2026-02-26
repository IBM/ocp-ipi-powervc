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
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"
)

const (
	LoadBalancerName = "Load Balancer"
)

type LoadBalancer struct {
	services *Services
}

func NewLoadBalancer(services *Services) ([]RunnableObject, []error) {
	var (
		lbs  []*LoadBalancer
		errs []error
		ros  []RunnableObject
	)

	lbs, errs = innerNewLoadBalancer(services)

	ros = make([]RunnableObject, len(lbs))
	// Go does not support type converting the entire array.
	// So we do it manually.
	for i, v := range lbs {
		ros[i] = RunnableObject(v)
	}

	return ros, errs
}

func NewLoadBalancerAlt(services *Services) ([]*LoadBalancer, []error) {
	return innerNewLoadBalancer(services)
}

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

func (lbs *LoadBalancer) Name() (string, error) {
	return LoadBalancerName, nil
}

func (lbs *LoadBalancer) ObjectName() (string, error) {
	return LoadBalancerName, nil
}

func (lbs *LoadBalancer) Run() error {
	// Nothing needs to be done here.
	return nil
}

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
		exitError   *exec.ExitError
		err         error
	)

	ctx, cancel = lbs.services.GetContextWithTimeout()
	defer cancel()

	clusterName = lbs.services.GetMetadata().GetClusterName()
	log.Debugf("ClusterStatus: clusterName = %s", clusterName)

	cloud = lbs.services.GetMetadata().GetCloud()
	log.Debugf("ClusterStatus: cloud = %s", cloud)
	if cloud == "" {
		fmt.Printf("%s: Error: GetCloud returns empty string\n", LoadBalancerName)
		return
	}

	server, err = findServer(ctx, cloud, clusterName)
	if err != nil {
		fmt.Printf("%s: Error: findServer returns error %v\n", LoadBalancerName, err)
		return
	}
	log.Debugf("ClusterStatus: FOUND server = %s", server.Name)

	_, ipAddress, err = findIpAddress(server)
	if err != nil {
		fmt.Printf("%s: Error: findIpAddress returns error %v\n", LoadBalancerName, err)
		return
	}
	if ipAddress == "" {
		fmt.Printf("%s: Error: findIpAddress returns empty string\n", LoadBalancerName)
		return
	}
	log.Debugf("ClusterStatus: ipAddress = %s", ipAddress)

	outb, err = runSplitCommand2([]string{
		"ssh-keyscan",
		ipAddress,
	})
	outs = strings.TrimSpace(string(outb))
	log.Debugf("ClusterStatus: outs = \"%s\"", outs)
	if errors.As(err, &exitError) {
		log.Debugf("ClusterStatus: exitError.ExitCode() = %+v\n", exitError.ExitCode())
	}
	if outs != "" {
		fmt.Printf("%s: Cluster bastion is alive\n", LoadBalancerName)
	} else {
		return
	}

	err = addServerKnownHosts(ctx, ipAddress)
	if err != nil {
		fmt.Printf("%s: Error: addServerKnownHosts returns error %v\n", LoadBalancerName, err)
		return
	}

	outb, err = runSplitCommand2([]string{
		"ssh",
		"-i",
		lbs.services.GetInstallerRsa(),
		fmt.Sprintf("%s@%s", lbs.services.GetBastionUsername(), ipAddress),
		"sudo",
		"cat",
		"/etc/haproxy/haproxy.cfg",
	})
	outs = strings.TrimSpace(string(outb))
	if err != nil {
		fmt.Printf("%s: Error: Catting haproxy configuration returns error %v\n", LoadBalancerName, err)
		return
	}

	outb, err = runSplitCommand2([]string{
		"ssh",
		"-i",
		lbs.services.GetInstallerRsa(),
		fmt.Sprintf("%s@%s", lbs.services.GetBastionUsername(), ipAddress),
		"sudo",
		"systemctl",
		"status",
		"haproxy.service",
		"--no-pager",
		"-l",
	})
	outs = strings.TrimSpace(string(outb))
	if err != nil {
		fmt.Printf("%s: Error: Finding haproxy status returns error %v\n", LoadBalancerName, err)
		return
	}

	fmt.Printf("%s: Cluster bastion has the following status:\n", LoadBalancerName)
	fmt.Println(outs)
}

func (lbs *LoadBalancer) Priority() (int, error) {
	return -1, nil
}
