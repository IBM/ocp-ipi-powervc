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

// (cd snippet5/; /bin/rm go.*; go mod init example/user/snippet5; go mod tidy)
// (set -euo pipefail; cd snippet5/; go build ./...; ./snippet5 --cloud cloudname1 --name my-volume --size 10 --shouldDebug true)
// (set -euo pipefail; cd snippet5/; go build ./...; ./snippet5 --cloud cloudname1 --name my-volume --size 10 --type my-type --shouldDebug true)
// (cd snippet5/; /bin/rm go.*)

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"time"

	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack/blockstorage/v3/volumes"
	"github.com/gophercloud/gophercloud/v2/openstack/config/clouds"
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

	// Volume wait configuration
	volumeWaitDuration = 15 * time.Second

	// Volume status constants
	volumeStatusAvailable = "available"
	volumeStatusError     = "error"

	// Default volume size in GiB
	defaultVolumeSize = 10
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

// parseBoolFlag converts a string flag value to boolean, accepting "true", "false",
// "1", "0", "yes", "no", "y", "n" (case-insensitive).
func parseBoolFlag(value, flagName string) (bool, error) {
	switch strings.TrimSpace(strings.ToLower(value)) {
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
		Out: out,
		Formatter: &logrus.TextFormatter{
			FullTimestamp:   true,
			TimestampFormat: "2006-01-02 15:04:05",
		},
		Level: logrus.DebugLevel,
	}
}

// getUserAgent generates a Gophercloud UserAgent to help cloud operators
// disambiguate openshift-installer requests.
func getUserAgent() gophercloud.UserAgent {
	var ua gophercloud.UserAgent
	ua.Prepend("openshift-installer/1.0")
	return ua
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
	client, err := clientconfig.NewServiceClient(ctx, service, opts)
	if err != nil {
		return nil, err
	}

	client.UserAgent = getUserAgent()

	return client, nil
}

// createDefaultBackoff creates a standard backoff configuration for retry logic.
func createDefaultBackoff(ctx context.Context) wait.Backoff {
	return wait.Backoff{
		Duration: defaultBackoffDuration,
		Factor:   defaultBackoffFactor,
		Cap:      leftInContext(ctx),
		Steps:    defaultBackoffSteps,
	}
}

// createVolumeWaitBackoff creates a backoff configuration for polling volume status.
func createVolumeWaitBackoff(ctx context.Context) wait.Backoff {
	return wait.Backoff{
		Duration: volumeWaitDuration,
		Factor:   defaultBackoffFactor,
		Cap:      leftInContext(ctx),
		Steps:    defaultBackoffSteps,
	}
}

// getServiceClient creates and returns an OpenStack service client with retry logic.
// It uses exponential backoff to handle transient failures when connecting to OpenStack services.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - serviceType: Type of OpenStack service (e.g., "compute", "volume", "network")
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
		var err2 error

		log.Debugf("getServiceClient: duration = %v, calling NewServiceClient(%s, %s)", leftInContext(ctx), serviceType, cloud)
		client, err2 = NewServiceClient(ctx, serviceType, DefaultClientOpts(cloud))
		if err2 != nil {
			log.Debugf("getServiceClient: Error: NewServiceClient returns error %v", err2)
			// Propagate permanent errors immediately rather than retrying until timeout.
			return false, err2
		}

		return true, nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to create %s service client for cloud %s: %w", serviceType, cloud, err)
	}

	return client, nil
}

// createVolume creates an OpenStack block storage volume (PVC) and waits for it
// to reach the "available" status.
//
// Parameters:
//   - ctx:              Context for cancellation and timeout control
//   - connBlockStorage: Authenticated block-storage service client
//   - volumeName:       Name for the new volume
//   - sizeGiB:          Size of the volume in GiB
//   - volumeType:       Optional volume type (pass "" to use the cloud default)
//
// Returns:
//   - *volumes.Volume: The created volume
//   - error: Any error encountered during creation or the wait
func createVolume(ctx context.Context, connBlockStorage *gophercloud.ServiceClient, volumeName string, sizeGiB int, volumeType string) (*volumes.Volume, error) {
	if volumeName == "" {
		return nil, fmt.Errorf("volume name cannot be empty")
	}
	if sizeGiB <= 0 {
		return nil, fmt.Errorf("volume size must be greater than 0, got: %d", sizeGiB)
	}

	log.Debugf("createVolume: name=%s size=%d volumeType=%q", volumeName, sizeGiB, volumeType)

	createOpts := volumes.CreateOpts{
		Name: volumeName,
		Size: sizeGiB,
	}
	if volumeType != "" {
		createOpts.VolumeType = volumeType
	}

	log.Debugf("createVolume: calling volumes.Create with opts %+v", createOpts)

	vol, err := volumes.Create(ctx, connBlockStorage, createOpts, nil).Extract()
	if err != nil {
		return nil, fmt.Errorf("failed to create volume %q: %w", volumeName, err)
	}

	log.Debugf("createVolume: volume created with ID=%s, waiting for status=%q", vol.ID, volumeStatusAvailable)

	// Poll until the volume reaches "available" or an error status
	waitBackoff := createVolumeWaitBackoff(ctx)

	err = wait.ExponentialBackoffWithContext(ctx, waitBackoff, func(context.Context) (bool, error) {
		current, err2 := volumes.Get(ctx, connBlockStorage, vol.ID).Extract()
		if err2 != nil {
			log.Debugf("createVolume: volumes.Get returned error %v", err2)
			return false, nil
		}

		log.Debugf("createVolume: volume %s status=%s", vol.ID, current.Status)

		switch current.Status {
		case volumeStatusAvailable:
			vol = current
			return true, nil
		case volumeStatusError:
			return false, fmt.Errorf("volume %q entered error status", volumeName)
		}

		return false, nil
	})

	if err != nil {
		return nil, fmt.Errorf("volume %q did not become available: %w", volumeName, err)
	}

	return vol, nil
}

// deleteVolume deletes the OpenStack block storage volume with the given ID and
// waits until it is fully removed (i.e. a Get returns a 404).
//
// Parameters:
//   - ctx:              Context for cancellation and timeout control
//   - connBlockStorage: Authenticated block-storage service client
//   - volumeID:         ID of the volume to delete
//
// Returns:
//   - error: Any error encountered during deletion or the wait
func deleteVolume(ctx context.Context, connBlockStorage *gophercloud.ServiceClient, volumeID string) error {
	if volumeID == "" {
		return fmt.Errorf("volume ID cannot be empty")
	}

	log.Debugf("deleteVolume: volumeID=%s", volumeID)

	if err := volumes.Delete(ctx, connBlockStorage, volumeID, volumes.DeleteOpts{}).ExtractErr(); err != nil {
		return fmt.Errorf("failed to delete volume %q: %w", volumeID, err)
	}

	log.Debugf("deleteVolume: delete request accepted, polling until volume is gone")

	// Poll until the volume no longer exists (404 from Get)
	waitBackoff := createVolumeWaitBackoff(ctx)

	err := wait.ExponentialBackoffWithContext(ctx, waitBackoff, func(context.Context) (bool, error) {
		_, err2 := volumes.Get(ctx, connBlockStorage, volumeID).Extract()
		if err2 != nil {
			if gophercloud.ResponseCodeIs(err2, 404) {
				return true, nil
			}
			log.Debugf("deleteVolume: volumes.Get returned error %v", err2)
			return false, nil
		}
		// Volume still exists, keep waiting
		return false, nil
	})

	if err != nil {
		return fmt.Errorf("volume %q was not fully deleted: %w", volumeID, err)
	}

	return nil
}

// promptContinue formats and prints a prompt to stdout and reads a line from stdin.
// Returns true only when the user types "yes" (case-insensitive).
// Returns false on EOF or I/O error.
func promptContinue(format string, args ...any) bool {
	fmt.Printf(format+" [yes/no]: ", args...)
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		return strings.EqualFold(strings.TrimSpace(scanner.Text()), "yes")
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to read stdin: %v\n", err)
	}
	return false
}

func main() {
	ptrCloud := flag.String("cloud", "", "Cloud name to use in clouds.yaml")
	ptrName := flag.String("name", "", "Name of the volume to create")
	ptrSize := flag.Int("size", defaultVolumeSize, "Volume size in GiB")
	ptrVolumeType := flag.String("type", "", "Volume type (optional, uses cloud default if omitted)")
	ptrShouldDebug := flag.String("shouldDebug", "false", "Enable debug output (true/false)")

	flag.Parse()

	shouldDebug, err := parseBoolFlag(*ptrShouldDebug, "shouldDebug")
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	// Initialize logger
	log = initLogger(shouldDebug)
	if shouldDebug {
		log.Debugf("Debug mode enabled")
	}

	if *ptrCloud == "" {
		fmt.Println("Error: --cloud is required")
		flag.Usage()
		os.Exit(1)
	}
	if *ptrName == "" {
		fmt.Println("Error: --name is required")
		flag.Usage()
		os.Exit(1)
	}

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()

	log.Debugf("cloud=%s name=%s size=%d type=%q", *ptrCloud, *ptrName, *ptrSize, *ptrVolumeType)

	// Obtain one block-storage client shared by create and delete.
	// clientconfig.NewServiceClient uses "volume" as the service key (not the catalog type).
	// Worth keeping in mind if you ever add other services — always check the case statements
	// in clientconfig/requests.go rather than guessing from the catalog.
	connBlockStorage, err := getServiceClient(ctx, "volume", *ptrCloud)
	if err != nil {
		fmt.Printf("failed to get block-storage service client: %v\n", err)
		os.Exit(1)
	}

	vol, err := createVolume(ctx, connBlockStorage, *ptrName, *ptrSize, *ptrVolumeType)
	if err != nil {
		fmt.Printf("failed to createVolume: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Volume created successfully:\n")
	fmt.Printf("  ID:         %s\n", vol.ID)
	fmt.Printf("  Name:       %s\n", vol.Name)
	fmt.Printf("  Size (GiB): %d\n", vol.Size)
	fmt.Printf("  Status:     %s\n", vol.Status)
	fmt.Printf("  Type:       %s\n", vol.VolumeType)

	if !promptContinue("\nDelete volume %q (%s)?", vol.Name, vol.ID) {
		fmt.Println("Skipping deletion.")
		return
	}

	if err := deleteVolume(ctx, connBlockStorage, vol.ID); err != nil {
		fmt.Printf("failed to deleteVolume: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Volume %q (%s) deleted successfully.\n", vol.Name, vol.ID)
}
