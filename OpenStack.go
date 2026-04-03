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
	"math"
	"strings"
	"time"

	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/flavors"
	"github.com/gophercloud/gophercloud/v2/openstack/image/v2/images"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/networks"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/keypairs"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/hypervisors"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"
	"github.com/gophercloud/gophercloud/v2/pagination"

	"k8s.io/apimachinery/pkg/util/wait"
)

// Note: This file uses the global 'log' variable declared in PowerVC-Tool.go
// and the leftInContext, NewServiceClient, and DefaultClientOpts functions.

const (
	// Backoff configuration constants for retry logic
	defaultBackoffDuration = 1 * time.Minute
	defaultBackoffFactor   = 1.1
	defaultBackoffSteps    = math.MaxInt32

	// Server wait configuration
	serverWaitDuration = 15 * time.Second

	// Server status constants
	serverStatusActive = "ACTIVE"

	// Error message prefixes
	errMsgServerNotFound = "Could not find server named"
)

// getServiceClient creates and returns an OpenStack service client with retry logic.
// It uses exponential backoff to handle transient failures when connecting to OpenStack services.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - serviceType: Type of OpenStack service (e.g., "compute", "image", "network")
//   - cloud: Cloud configuration name
//
// Returns:
//   - *gophercloud.ServiceClient: Initialized service client
//   - error: Any error encountered during client creation
func getServiceClient(ctx context.Context, serviceType string, cloud string) (client *gophercloud.ServiceClient, err error) {
	if serviceType == "" {
		return nil, fmt.Errorf("service type cannot be empty")
	}
	if cloud == "" {
		return nil, fmt.Errorf("cloud name cannot be empty")
	}

	backoff := createDefaultBackoff(ctx)

	err = wait.ExponentialBackoffWithContext(ctx, backoff, func(context.Context) (bool, error) {
		var (
			err2 error
		)

		log.Debugf("getServiceClient: duration = %v, calling NewServiceClient(%s, %s)", leftInContext(ctx), serviceType, cloud)
		client, err2 = NewServiceClient(ctx, serviceType, DefaultClientOpts(cloud))
		if err2 != nil {
			log.Debugf("getServiceClient: Error: NewServiceClient returns error %v", err2)
			return false, nil
		}

		return true, nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to create %s service client for cloud %s: %w", serviceType, cloud, err)
	}

	return client, nil
}

// createDefaultBackoff creates a standard backoff configuration for retry logic.
// This helper function eliminates code duplication across OpenStack API calls.
//
// Parameters:
//   - ctx: Context for determining the maximum retry duration
//
// Returns:
//   - wait.Backoff: Configured backoff structure
func createDefaultBackoff(ctx context.Context) wait.Backoff {
	return wait.Backoff{
		Duration: defaultBackoffDuration,
		Factor:   defaultBackoffFactor,
		Cap:      leftInContext(ctx),
		Steps:    defaultBackoffSteps,
	}
}

// createServerWaitBackoff creates a backoff configuration for waiting on server operations.
// Uses a shorter initial duration than the default backoff.
//
// Parameters:
//   - ctx: Context for determining the maximum retry duration
//
// Returns:
//   - wait.Backoff: Configured backoff structure
func createServerWaitBackoff(ctx context.Context) wait.Backoff {
	return wait.Backoff{
		Duration: serverWaitDuration,
		Factor:   defaultBackoffFactor,
		Cap:      leftInContext(ctx),
		Steps:    defaultBackoffSteps,
	}
}

// findFlavor searches for an OpenStack compute flavor by name.
// It retrieves all available flavors and returns the one matching the specified name.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - cloudName: Name of the cloud configuration
//   - name: Name of the flavor to find
//
// Returns:
//   - flavors.Flavor: The found flavor
//   - error: Error if flavor not found or API call fails
func findFlavor(ctx context.Context, cloudName string, name string) (foundFlavor flavors.Flavor, err error) {
	if cloudName == "" {
		return flavors.Flavor{}, fmt.Errorf("cloud name cannot be empty")
	}
	if name == "" {
		return flavors.Flavor{}, fmt.Errorf("flavor name cannot be empty")
	}

	var (
		pager      pagination.Page
		allFlavors []flavors.Flavor
		flavor     flavors.Flavor
	)

	connCompute, err := getServiceClient(ctx, "compute", cloudName)
	if err != nil {
		return flavors.Flavor{}, fmt.Errorf("failed to get compute service client: %w", err)
	}

	backoff := createDefaultBackoff(ctx)

	err = wait.ExponentialBackoffWithContext(ctx, backoff, func(context.Context) (bool, error) {
		var (
			err2 error
		)

		log.Debugf("findFlavor: duration = %v, calling flavors.ListDetail", leftInContext(ctx))
		pager, err2 = flavors.ListDetail(connCompute, flavors.ListOpts{}).AllPages(ctx)
		if err2 != nil {
			log.Debugf("findFlavor: flavors.ListDetail returned error: %v", err2)
			return false, nil
		}

		allFlavors, err2 = flavors.ExtractFlavors(pager)
		if err2 != nil {
			log.Debugf("findFlavor: flavors.ExtractFlavors returned error: %v", err2)
			return false, nil
		}

		return true, nil
	})
	if err != nil {
		return flavors.Flavor{}, fmt.Errorf("failed to list flavors: %w", err)
	}

	for _, flavor = range allFlavors {
		if flavor.Name == name {
			log.Debugf("findFlavor: found flavor %s with ID %s", flavor.Name, flavor.ID)
			foundFlavor = flavor
			return foundFlavor, nil
		}
	}

	return flavors.Flavor{}, fmt.Errorf("could not find flavor named %s", name)
}

// findImage searches for an OpenStack image by name.
// It retrieves all available images and returns the one matching the specified name.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - cloudName: Name of the cloud configuration
//   - name: Name of the image to find
//
// Returns:
//   - images.Image: The found image
//   - error: Error if image not found or API call fails
func findImage(ctx context.Context, cloudName string, name string) (foundImage images.Image, err error) {
	if cloudName == "" {
		return images.Image{}, fmt.Errorf("cloud name cannot be empty")
	}
	if name == "" {
		return images.Image{}, fmt.Errorf("image name cannot be empty")
	}

	var (
		pager     pagination.Page
		allImages []images.Image
		image     images.Image
	)

	connImage, err := getServiceClient(ctx, "image", cloudName)
	if err != nil {
		return images.Image{}, fmt.Errorf("failed to get image service client: %w", err)
	}

	backoff := createDefaultBackoff(ctx)

	err = wait.ExponentialBackoffWithContext(ctx, backoff, func(context.Context) (bool, error) {
		var (
			err2 error
		)

		log.Debugf("findImage: duration = %v, calling images.List", leftInContext(ctx))
		pager, err2 = images.List(connImage, images.ListOpts{}).AllPages(ctx)
		if err2 != nil {
			log.Debugf("findImage: images.List returned error: %v", err2)
			return false, nil
		}

		allImages, err2 = images.ExtractImages(pager)
		if err2 != nil {
			log.Debugf("findImage: images.ExtractImages returned error: %v", err2)
			return false, nil
		}

		return true, nil
	})
	if err != nil {
		return images.Image{}, fmt.Errorf("failed to list images: %w", err)
	}

	for _, image = range allImages {
		log.Debugf("findImage: checking image.Name = %s, image.ID = %s", image.Name, image.ID)

		if image.Name == name {
			log.Debugf("findImage: found image %s with ID %s", image.Name, image.ID)
			foundImage = image
			return foundImage, nil
		}
	}

	return images.Image{}, fmt.Errorf("could not find image named %s", name)
}

// findNetwork searches for an OpenStack network by name.
// It retrieves all available networks and returns the one matching the specified name.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - cloudName: Name of the cloud configuration
//   - name: Name of the network to find
//
// Returns:
//   - networks.Network: The found network
//   - error: Error if network not found or API call fails
func findNetwork(ctx context.Context, cloudName string, name string) (foundNetwork networks.Network, err error) {
	if cloudName == "" {
		return networks.Network{}, fmt.Errorf("cloud name cannot be empty")
	}
	if name == "" {
		return networks.Network{}, fmt.Errorf("network name cannot be empty")
	}

	var (
		pager       pagination.Page
		allNetworks []networks.Network
		network     networks.Network
	)

	connNetwork, err := getServiceClient(ctx, "network", cloudName)
	if err != nil {
		return networks.Network{}, fmt.Errorf("failed to get network service client: %w", err)
	}

	backoff := createDefaultBackoff(ctx)

	err = wait.ExponentialBackoffWithContext(ctx, backoff, func(context.Context) (bool, error) {
		var (
			err2 error
		)

		log.Debugf("findNetwork: duration = %v, calling networks.List", leftInContext(ctx))
		pager, err2 = networks.List(connNetwork, networks.ListOpts{}).AllPages(ctx)
		if err2 != nil {
			log.Debugf("findNetwork: networks.List returned error: %v", err2)
			return false, nil
		}

		allNetworks, err2 = networks.ExtractNetworks(pager)
		if err2 != nil {
			log.Debugf("findNetwork: networks.ExtractNetworks returned error: %v", err2)
			return false, nil
		}

		return true, nil
	})
	if err != nil {
		return networks.Network{}, fmt.Errorf("failed to list networks: %w", err)
	}

	for _, network = range allNetworks {
		log.Debugf("findNetwork: checking network.Name = %s, network.ID = %s", network.Name, network.ID)

		if network.Name == name {
			log.Debugf("findNetwork: found network %s with ID %s", network.Name, network.ID)
			foundNetwork = network
			return foundNetwork, nil
		}
	}

	return networks.Network{}, fmt.Errorf("could not find network named %s", name)
}

// findServer searches for an OpenStack server (VM) by name.
// It retrieves all servers and returns the one matching the specified name.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - cloudName: Name of the cloud configuration
//   - name: Name of the server to find
//
// Returns:
//   - servers.Server: The found server
//   - error: Error if server not found or API call fails
func findServer(ctx context.Context, cloudName string, name string) (foundServer servers.Server, err error) {
	if cloudName == "" {
		return servers.Server{}, fmt.Errorf("cloud name cannot be empty")
	}
	if name == "" {
		return servers.Server{}, fmt.Errorf("server name cannot be empty")
	}

	var (
		allServers []servers.Server
		server     servers.Server
	)

	allServers, err = getAllServers(ctx, cloudName)
	if err != nil {
		return servers.Server{}, fmt.Errorf("failed to get all servers: %w", err)
	}

	for _, server = range allServers {
		if server.Name == name {
			log.Debugf("findServer: FOUND server.Name = %s, server.ID = %s", server.Name, server.ID)
			foundServer = server
			return foundServer, nil
		} else {
			log.Debugf("findServer: SKIP  server.Name = %s, server.ID = %s", server.Name, server.ID)
		}
	}

	return servers.Server{}, fmt.Errorf("could not find server named %s", name)
}

// waitForServer waits for a server to reach ACTIVE status with RUNNING power state.
// It uses exponential backoff to poll the server status until it's ready or the context times out.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - cloudName: Name of the cloud configuration
//   - name: Name of the server to wait for
//
// Returns:
//   - error: Error if server doesn't become active or API call fails
func waitForServer(ctx context.Context, cloudName string, name string) error {
	if cloudName == "" {
		return fmt.Errorf("cloud name cannot be empty")
	}
	if name == "" {
		return fmt.Errorf("server name cannot be empty")
	}

	backoff := createServerWaitBackoff(ctx)

	err := wait.ExponentialBackoffWithContext(ctx, backoff, func(context.Context) (bool, error) {
		var (
			foundServer servers.Server
			err2        error
		)

		// Check server status
		foundServer, err2 = findServer(ctx, cloudName, name)
		if err2 != nil {
			log.Debugf("waitForServer: findServer returned %v", err2)

			if strings.HasPrefix(err2.Error(), errMsgServerNotFound) {
				// Server not found yet, keep waiting
				return false, nil
			}

			// Other error, stop retrying
			return false, err2
		}

		log.Debugf("waitForServer: foundServer.Status = %s, foundServer.PowerState = %d", foundServer.Status, foundServer.PowerState)
		if foundServer.Status == serverStatusActive && foundServer.PowerState == servers.RUNNING {
			log.Debugf("waitForServer: server %s is active and running", name)
			return true, nil
		}
		
		log.Debugf("waitForServer: server %s not ready yet (Status=%s, PowerState=%d)", name, foundServer.Status, foundServer.PowerState)
		return false, nil
	})
	
	if err != nil {
		return fmt.Errorf("failed waiting for server %s to become active: %w", name, err)
	}

	return nil
}

// getAllServers retrieves all servers from the specified cloud.
// It uses exponential backoff to handle transient API failures.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - cloud: Name of the cloud configuration
//
// Returns:
//   - []servers.Server: List of all servers
//   - error: Error if API call fails
func getAllServers(ctx context.Context, cloud string) (allServers []servers.Server, err error) {
	if cloud == "" {
		return nil, fmt.Errorf("cloud name cannot be empty")
	}

	var (
		connCompute *gophercloud.ServiceClient
		duration    time.Duration
		pager       pagination.Page
	)

	connCompute, err = getServiceClient(ctx, "compute", cloud)
	if err != nil {
		return nil, fmt.Errorf("failed to get compute service client: %w", err)
	}

	backoff := createDefaultBackoff(ctx)

	err = wait.ExponentialBackoffWithContext(ctx, backoff, func(context.Context) (bool, error) {
		var (
			err2 error
		)

		duration = leftInContext(ctx)
		log.Debugf("getAllServers: duration = %v, calling servers.List", duration)
		pager, err2 = servers.List(connCompute, nil).AllPages(ctx)
		if err2 != nil {
			log.Debugf("getAllServers: servers.List returned error %v", err2)
			return false, nil
		}

		allServers, err2 = servers.ExtractServers(pager)
		if err2 != nil {
			log.Debugf("getAllServers: servers.ExtractServers returned error %v", err2)
			return false, nil
		}

		return true, nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to list servers: %w", err)
	}

	log.Debugf("getAllServers: retrieved %d servers", len(allServers))
	return allServers, nil
}

// findServerInList searches for a server by name in a pre-fetched list of servers.
// This is more efficient than calling findServer when you already have the server list.
//
// Parameters:
//   - allServers: Pre-fetched list of servers
//   - name: Name of the server to find
//
// Returns:
//   - servers.Server: The found server
//   - error: Error if server not found in the list
func findServerInList(allServers []servers.Server, name string) (foundServer servers.Server, err error) {
	if name == "" {
		return servers.Server{}, fmt.Errorf("server name cannot be empty")
	}

	var (
		server servers.Server
	)

	for _, server = range allServers {
		if server.Name == name {
			log.Debugf("findServerInList: found server %s with ID %s", server.Name, server.ID)
			foundServer = server
			return foundServer, nil
		}
	}

	return servers.Server{}, fmt.Errorf("could not find server named %s in list of %d servers", name, len(allServers))
}

// findKeyPair searches for an OpenStack SSH keypair by name.
// It retrieves all available keypairs and returns the one matching the specified name.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - cloudName: Name of the cloud configuration
//   - name: Name of the keypair to find
//
// Returns:
//   - keypairs.KeyPair: The found keypair
//   - error: Error if keypair not found or API call fails
func findKeyPair(ctx context.Context, cloudName string, name string) (foundKeyPair keypairs.KeyPair, err error) {
	if cloudName == "" {
		return keypairs.KeyPair{}, fmt.Errorf("cloud name cannot be empty")
	}
	if name == "" {
		return keypairs.KeyPair{}, fmt.Errorf("keypair name cannot be empty")
	}

	var (
		pager       pagination.Page
		allKeyPairs []keypairs.KeyPair
		keypair     keypairs.KeyPair
	)

	connServer, err := getServiceClient(ctx, "compute", cloudName)
	if err != nil {
		return keypairs.KeyPair{}, fmt.Errorf("failed to get compute service client: %w", err)
	}

	backoff := createDefaultBackoff(ctx)

	err = wait.ExponentialBackoffWithContext(ctx, backoff, func(context.Context) (bool, error) {
		var (
			err2 error
		)

		log.Debugf("findKeyPair: duration = %v, calling keypairs.List", leftInContext(ctx))
		pager, err2 = keypairs.List(connServer, keypairs.ListOpts{}).AllPages(ctx)
		if err2 != nil {
			log.Debugf("findKeyPair: keypairs.List returned error: %v", err2)
			return false, nil
		}

		allKeyPairs, err2 = keypairs.ExtractKeyPairs(pager)
		if err2 != nil {
			log.Debugf("findKeyPair: keypairs.ExtractKeyPairs returned error: %v", err2)
			return false, nil
		}

		return true, nil
	})
	if err != nil {
		return keypairs.KeyPair{}, fmt.Errorf("failed to list keypairs: %w", err)
	}

	for _, keypair = range allKeyPairs {
		log.Debugf("findKeyPair: checking keypair.Name = %s", keypair.Name)

		if keypair.Name == name {
			log.Debugf("findKeyPair: found keypair %s", keypair.Name)
			foundKeyPair = keypair
			return foundKeyPair, nil
		}
	}

	return keypairs.KeyPair{}, fmt.Errorf("could not find keypair named %s", name)
}

// findHypervisor searches for an OpenStack hypervisor by hostname.
// It retrieves all available hypervisors and returns the one matching the specified hostname.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - cloudName: Name of the cloud configuration
//   - name: Hostname of the hypervisor to find
//
// Returns:
//   - hypervisors.Hypervisor: The found hypervisor
//   - error: Error if hypervisor not found or API call fails
func findHypervisor(ctx context.Context, cloudName string, name string) (foundHypervisor hypervisors.Hypervisor, err error) {
	if cloudName == "" {
		return hypervisors.Hypervisor{}, fmt.Errorf("cloud name cannot be empty")
	}
	if name == "" {
		return hypervisors.Hypervisor{}, fmt.Errorf("hypervisor name cannot be empty")
	}

	var (
		pager          pagination.Page
		allHypervisors []hypervisors.Hypervisor
		hypervisor     hypervisors.Hypervisor
	)

	connServer, err := getServiceClient(ctx, "compute", cloudName)
	if err != nil {
		return hypervisors.Hypervisor{}, fmt.Errorf("failed to get compute service client: %w", err)
	}

	backoff := createDefaultBackoff(ctx)

	err = wait.ExponentialBackoffWithContext(ctx, backoff, func(context.Context) (bool, error) {
		var (
			err2 error
		)

		log.Debugf("findHypervisor: duration = %v, calling hypervisors.List", leftInContext(ctx))
		pager, err2 = hypervisors.List(connServer, nil).AllPages(ctx)
		if err2 != nil {
			log.Debugf("findHypervisor: hypervisors.List returned error: %v", err2)
			return false, nil
		}

		allHypervisors, err2 = hypervisors.ExtractHypervisors(pager)
		if err2 != nil {
			log.Debugf("findHypervisor: hypervisors.ExtractHypervisors returned error: %v", err2)
			return false, nil
		}

		return true, nil
	})
	if err != nil {
		return hypervisors.Hypervisor{}, fmt.Errorf("failed to list hypervisors: %w", err)
	}

	for _, hypervisor = range allHypervisors {
		log.Debugf("findHypervisor: checking hypervisor.HypervisorHostname = %s, hypervisor.ID = %s", hypervisor.HypervisorHostname, hypervisor.ID)

		if hypervisor.HypervisorHostname == name {
			log.Debugf("findHypervisor: found hypervisor %s with ID %s", hypervisor.HypervisorHostname, hypervisor.ID)
			foundHypervisor = hypervisor
			return foundHypervisor, nil
		}
	}

	return hypervisors.Hypervisor{}, fmt.Errorf("could not find hypervisor named %s", name)
}

// getAllHypervisors retrieves all hypervisors using the provided compute service client.
// It uses exponential backoff to handle transient API failures.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - connCompute: Pre-configured compute service client
//
// Returns:
//   - []hypervisors.Hypervisor: List of all hypervisors
//   - error: Error if API call fails
func getAllHypervisors(ctx context.Context, connCompute *gophercloud.ServiceClient) (allHypervisors []hypervisors.Hypervisor, err error) {
	if connCompute == nil {
		return nil, fmt.Errorf("compute service client cannot be nil")
	}

	var (
		duration time.Duration
		pager    pagination.Page
	)

	backoff := createDefaultBackoff(ctx)

	err = wait.ExponentialBackoffWithContext(ctx, backoff, func(context.Context) (bool, error) {
		var (
			err2 error
		)

		duration = leftInContext(ctx)
		log.Debugf("getAllHypervisors: duration = %v, calling hypervisors.List", duration)
		pager, err2 = hypervisors.List(connCompute, nil).AllPages(ctx)
		if err2 != nil {
			log.Debugf("getAllHypervisors: hypervisors.List returned error %v", err2)

			if strings.Contains(err2.Error(), "The request you have made requires authentication") {
				// Authentication error, stop retrying
				return true, err2
			}

			// Transient error, retry
			return false, nil
		}

		allHypervisors, err2 = hypervisors.ExtractHypervisors(pager)
		if err2 != nil {
			log.Debugf("getAllHypervisors: hypervisors.ExtractHypervisors returned error %v", err2)
			return false, nil
		}

		return true, nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to list hypervisors: %w", err)
	}

	log.Debugf("getAllHypervisors: retrieved %d hypervisors", len(allHypervisors))
	return allHypervisors, nil
}

// findHypervisorInList searches for a hypervisor by hostname in a pre-fetched list of hypervisors.
// This is more efficient than calling findHypervisor when you already have the hypervisor list.
//
// Note: This function was previously named findHypervisorverInList (typo fixed).
//
// Parameters:
//   - allHypervisors: Pre-fetched list of hypervisors
//   - name: Hostname of the hypervisor to find
//
// Returns:
//   - hypervisors.Hypervisor: The found hypervisor
//   - error: Error if hypervisor not found in the list
func findHypervisorInList(allHypervisors []hypervisors.Hypervisor, name string) (foundHypervisor hypervisors.Hypervisor, err error) {
	if name == "" {
		return hypervisors.Hypervisor{}, fmt.Errorf("hypervisor name cannot be empty")
	}

	var (
		hypervisor hypervisors.Hypervisor
	)

	for _, hypervisor = range allHypervisors {
		if hypervisor.HypervisorHostname == name {
			log.Debugf("findHypervisorInList: found hypervisor %s with ID %s", hypervisor.HypervisorHostname, hypervisor.ID)
			foundHypervisor = hypervisor
			return foundHypervisor, nil
		}
	}

	return hypervisors.Hypervisor{}, fmt.Errorf("could not find hypervisor named %s in list of %d hypervisors", name, len(allHypervisors))
}
