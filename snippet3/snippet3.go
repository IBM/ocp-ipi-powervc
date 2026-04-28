// Copyright 2026 IBM Corp
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

// (cd snippet3/; /bin/rm go.*; go mod init example/user/snippet3; go mod tidy)
// (set -euo pipefail; cd snippet3/; go build ./...; ./snippet3 --cloud cloudname1 --cloud cloudname2 --shouldDebug true)
// (cd snippet3/; /bin/rm go.*)

import (
	"context"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"time"

	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack/config/clouds"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"
	"github.com/gophercloud/gophercloud/v2/pagination"
	"github.com/gophercloud/utils/v2/openstack/clientconfig"

	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/sirupsen/logrus"
)

const (
	// defaultTimeout is the default timeout for operations
	defaultTimeout = 15 * time.Minute

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

var (
	// log is the global logger instance used throughout the application
	log *logrus.Logger
)

// leftInContext returns the remaining time in the context
func leftInContext(ctx context.Context) time.Duration {
	deadline, ok := ctx.Deadline()
	if !ok {
		return math.MaxInt64
	}
	return time.Until(deadline)
}

// parseBoolFlag converts a string flag value to boolean.
// Returns an error if the value is not "true" or "false" (case-insensitive).
func parseBoolFlag(value, flagName string) (bool, error) {
	trimmedValue := strings.TrimSpace(strings.ToLower(value))

	switch trimmedValue {
	case "true", "1", "yes", "y":
		return true, nil
	case "false", "0", "no", "n":
		return false, nil
	default:
		return false, fmt.Errorf("%s must be 'true' or 'false', got: %q", flagName, value)
	}
}

// initLogger creates a configured logger based on debug flag.
// When debug is true, logs are written to stderr; otherwise, they are discarded.
func initLogger(debug bool) *logrus.Logger {
	out := io.Discard
	if debug {
		out = os.Stderr
	}

	return &logrus.Logger{
		Out:   out,
		Formatter: &logrus.TextFormatter{
			FullTimestamp:   true,
			TimestampFormat: "2006-01-02 15:04:05",
		},
		Level: logrus.DebugLevel,
	}
}

// getUserAgent generates a Gophercloud UserAgent to help cloud operators
// disambiguate openshift-installer requests.
func getUserAgent() (gophercloud.UserAgent, error) {
	ua := gophercloud.UserAgent{}

	ua.Prepend(fmt.Sprintf("openshift-installer/%s", "1.0"))
	return ua, nil
}

// DefaultClientOpts generates default client opts based on cloud name
func DefaultClientOpts(cloudName string) *clientconfig.ClientOpts {
	opts := new(clientconfig.ClientOpts)
	opts.Cloud = cloudName
	// We explicitly disable reading auth data from env variables by setting an invalid EnvPrefix.
	// By doing this, we make sure that the data from clouds.yaml is enough to authenticate.
	// For more information: https://github.com/gophercloud/utils/blob/8677e053dcf1f05d0fa0a616094aace04690eb94/openstack/clientconfig/requests.go#L508
	opts.EnvPrefix = "NO_ENV_VARIABLES_"
	return opts
}

// NewServiceClient is a wrapper around Gophercloud's NewServiceClient that
// ensures we consistently set a user-agent.
func NewServiceClient(ctx context.Context, service string, opts *clientconfig.ClientOpts) (*gophercloud.ServiceClient, error) {
	ua, err := getUserAgent()
	if err != nil {
		return nil, err
	}

	client, err := clientconfig.NewServiceClient(ctx, service, opts)
	if err != nil {
		return nil, err
	}

	client.UserAgent = ua

	return client, nil
}

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

	// Test for the existence of the cloud name in clouds.yaml
	_, _, _, err = clouds.Parse(clouds.WithCloudName(cloud))
	if err != nil {
		return nil, err
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

// getAllServers retrieves all servers from the specified clouds.
// It iterates through each cloud, retrieves the compute service client,
// and lists all servers using pagination. The function handles errors
// and retries the operation if necessary.
// Parameters:
//   - ctx: Context for the operation
//   - clouds: List of cloud names to retrieve servers from
// Returns:
//   - allServers: Slice of all servers retrieved from all clouds
//   - err: Error if any operation fails
func getAllServers(ctx context.Context, clouds []string) (allServers []servers.Server, err error) {
	log.Debugf("getAllServers: clouds = %+v", clouds)
	
	// Map to track unique servers by ID
	serverMap := make(map[string]servers.Server)
	
	for _, cloud := range clouds {
		log.Debugf("getAllServers: cloud = %s", cloud)

		if cloud == "" {
			return nil, fmt.Errorf("cloud name cannot be empty")
		}

		var (
			connCompute  *gophercloud.ServiceClient
			duration     time.Duration
			pager        pagination.Page
			cloudServers []servers.Server
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

			cloudServers, err2 = servers.ExtractServers(pager)
			if err2 != nil {
				log.Debugf("getAllServers: servers.ExtractServers returned error %v", err2)
				return false, nil
			}

			return true, nil
		})

		if err != nil {
			return nil, fmt.Errorf("failed to list servers: %w", err)
		}

		log.Debugf("getAllServers: retrieved %d servers", len(cloudServers))

		// Merge cloudServers into serverMap, ensuring uniqueness by ID
		for _, cloudServer := range cloudServers {
			log.Debugf("getAllServers: %v", cloudServer.Name)
			// Only add if not already present (first occurrence wins)
			if _, exists := serverMap[cloudServer.ID]; !exists {
				serverMap[cloudServer.ID] = cloudServer
			}
		}
	}

	// Convert map to slice
	allServers = make([]servers.Server, 0, len(serverMap))
	for _, server := range serverMap {
		allServers = append(allServers, server)
	}

	return allServers, nil
}

// cloudFlags is a custom flag type that allows multiple cloud names
type cloudFlags []string

func (c *cloudFlags) String() string {
	return strings.Join(*c, ",")
}

func (c *cloudFlags) Set(value string) error {
	*c = append(*c, value)
	return nil
}

func main() {
	var clouds cloudFlags
	flag.Var(&clouds, "cloud", "Cloud name to use in clouds.yaml (can be specified multiple times)")
	ptrShouldDebug := flag.String("shouldDebug", "false", "Enable debug output")

	flag.Parse()

	shouldDebug, err := parseBoolFlag(*ptrShouldDebug, "shouldDebug")
	if err != nil {
		fmt.Printf("parseBoolFlag returns %v\n", err)
		os.Exit(1)
	}

	// Initialize logger
	log = initLogger(shouldDebug)
	if shouldDebug {
		log.Debugf("Debug mode enabled")
	}

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()

	log.Debugf("clouds = %+v\n", clouds)

	allServers, err := getAllServers(ctx, []string(clouds))
	if err != nil {
		fmt.Printf("failed to getAllServers: %v\n", err)
		os.Exit(1)
	}

	for _, server := range allServers {
		log.Debugf("server.Name = %v\n", server.Name)
	}
}
