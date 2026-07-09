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

// Package main provides the watch-create command implementation.
//
// This file implements the watch-create command which monitors and displays
// the status of cluster resources during and after cluster creation. The
// command queries the status of various cluster components including:
//
//   - OpenShift Cluster (if kubeconfig is provided)
//   - Virtual Machines
//   - Load Balancer
//   - IBM Domain Name Service (if base domain is provided)
//
// The command accepts the following flags:
//   - cloud: The cloud to use in clouds.yaml (required)
//   - metadata: The location of the metadata.json file (required)
//   - kubeconfig: The KUBECONFIG file (optional)
//   - bastionRsa: The RSA filename for the bastion VM (required)
//   - baseDomain: The DNS base name to use (optional)
//   - shouldDebug: Enable debug output (true/false, default: false)
//
// The command initializes runnable objects based on the provided flags,
// sorts them by priority, and queries their status sequentially.

package main

import (
	"context"
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

const (
	// Flag names for watch-create command
	flagWatchCreateCloud           = "cloud"
	flagWatchCreateMetadata        = "metadata"
	flagWatchCreateKubeConfig      = "kubeconfig"
	flagWatchCreateBastionRsa      = "bastionRsa"
	flagWatchCreateBaseDomain      = "baseDomain"
	flagWatchCreateShouldDebug     = "shouldDebug"

	// Flag default values
	defaultWatchCreateCloud           = ""
	defaultWatchCreateMetadata        = ""
	defaultWatchCreateKubeConfig      = ""
	defaultWatchCreateBastionRsa      = ""
	defaultWatchCreateBaseDomain      = ""
	defaultWatchCreateShouldDebug     = "false"

	// Error message prefixes
	errPrefixWatchCreate = "Error: "

	// Usage messages
	usageWatchCreateCloud           = "The cloud to use in clouds.yaml"
	usageWatchCreateMetadata        = "The location of the metadata.json file"
	usageWatchCreateKubeConfig      = "The KUBECONFIG file"
	usageWatchCreateBastionRsa      = "The RSA filename for the bastion VM to use"
	usageWatchCreateBaseDomain      = "The DNS base name to use"
	usageWatchCreateShouldDebug     = "Should output debug output"

	// Environment variable names
	envIBMCloudAPIKey = "IBMCLOUD_API_KEY"

	// Component names
	componentOpenShift = "OpenShift Cluster"
	componentVMs       = "Virtual Machines"
	componentLB        = "Load Balancer"
	componentDNS       = "IBM Domain Name Service"
)

// watchCreateClusterCommand executes the watch-create command with the given flags and arguments.
//
// This function monitors and displays the status of cluster resources. It parses
// command-line flags, validates required parameters, initializes cluster components,
// and queries their status in priority order.
//
// Parameters:
//   - watchCreateClusterFlags: The FlagSet containing command-line flags (must not be nil)
//   - args: Command-line arguments to parse
//
// Returns:
//   - error: Any error encountered during flag parsing, validation, initialization, or status query
//
// Example usage:
//   err := watchCreateClusterCommand(flagSet, []string{
//       "-cloud", "mycloud",
//       "-metadata", "/path/to/metadata.json",
//       "-bastionRsa", "/path/to/key.rsa",
//       "-shouldDebug", "true",
//   })
func watchCreateClusterCommand(watchCreateClusterFlags *flag.FlagSet, args []string) error {
	err := innerWatchCreateClusterCommand(watchCreateClusterFlags, args)
	if err != nil {
		fmt.Printf("%+v\n", err)
		if watchCreateClusterFlags != nil {
			watchCreateClusterFlags.Usage()
		}
	}
	return err
}

// innerWatchCreateClusterCommand executes the watch-create command with the given flags and arguments.
//
// This function monitors and displays the status of cluster resources. It parses
// command-line flags, validates required parameters, initializes cluster components,
// and queries their status in priority order.
//
// Parameters:
//   - watchCreateClusterFlags: The FlagSet containing command-line flags (must not be nil)
//   - args: Command-line arguments to parse
//
// Returns:
//   - error: Any error encountered during flag parsing, validation, initialization, or status query
//
// The function executes the following steps:
//  1. Displays program version information
//  2. Parses command-line flags
//  3. Configures logging based on debug flag
//  4. Validates IBM Cloud API key (if provided)
//  5. Validates required flags (cloud, metadata, bastionRsa)
//  6. Validates metadata file accessibility
//  7. Initializes runnable objects based on provided flags
//  8. Loads metadata from file
//  9. Creates services object
// 10. Initializes and sorts runnable objects by priority
// 11. Queries status of each component
func innerWatchCreateClusterCommand(watchCreateClusterFlags *flag.FlagSet, args []string) error {
	// Validate input parameters
	if watchCreateClusterFlags == nil {
		return fmt.Errorf("%sflag set cannot be nil", errPrefixWatchCreate)
	}

	// Display version information
	fmt.Fprintf(os.Stderr, "Program version is %v, release = %v\n", version, release)

	// Create a pre-debug log before we know that debugging is enabled
	var preLog strings.Builder

	// Parse and validate flags
	config, err := parseWatchCreateFlags(&preLog, watchCreateClusterFlags, args)
	if err != nil {
		return fmt.Errorf("%sfailed to parse and validate flags: %w", errPrefixWatchCreate, err)
	}

	// Initialize logger with debug mode if enabled
	log = initLogger(config.shouldDebug)
	if config.shouldDebug {
		log.Debugf("Debug mode enabled")
	}

	// Dump the prelogged lines now that log has been initialized!
	scanner := bufio.NewScanner(strings.NewReader(preLog.String()))
	for scanner.Scan() {
		line := scanner.Text() // Each line as a string
		log.Println(line)
	}
	if err := scanner.Err(); err != nil {
		log.Printf("[WARN] Error reading pre-log buffer: %v", err)
	}

	// Validate IBM Cloud API key if provided
	if err := validateIBMCloudAPIKey(config.apiKey); err != nil {
		return fmt.Errorf("%sfailed to validate IBM Cloud API key: %w", errPrefixWatchCreate, err)
	}

	// Validate metadata file
	if err := validateMetadataFile(config.metadata); err != nil {
		return fmt.Errorf("%sfailed to validate metadata file: %w", errPrefixWatchCreate, err)
	}

	// Validate bastion RSA file
	if err := validateBastionRsaFile(config.bastionRsa); err != nil {
		return fmt.Errorf("%sfailed to validate bastion RSA file: %w", errPrefixWatchCreate, err)
	}

	// Validate kubeconfig file if provided
	if config.kubeConfig != "" {
		if err := validateKubeConfigFile(config.kubeConfig); err != nil {
			return fmt.Errorf("%sfailed to validate kubeconfig file: %w", errPrefixWatchCreate, err)
		}
	}

	// Execute check-alive command with 15 minute timeout
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	// Initialize components
	robjsFuncs := buildComponentList(config)
	log.Printf("[INFO] Initialized %d components", len(robjsFuncs))

	// Load metadata and create services
	services, err := initializeServices(config)
	if err != nil {
		return fmt.Errorf("%sfailed to initialize services: %w", errPrefixWatchCreate, err)
	}
	defer func() {
		if err := services.Close(); err != nil {
			log.Printf("[WARN] Failed to close services: %v", err)
		}
	}()

	// Initialize and execute runnable objects
	robjsCluster, err := initializeRunnableObjects(ctx, services, robjsFuncs)
	if err != nil {
		return fmt.Errorf("%sfailed to initialize runnable objects: %w", errPrefixWatchCreate, err)
	}

	// Sort and query status
	if err := queryComponentStatus(ctx, robjsCluster); err != nil {
		return fmt.Errorf("%sfailed to query component status: %w", errPrefixWatchCreate, err)
	}

	log.Printf("[INFO] Status query completed for all components")
	return nil
}

// watchCreateConfig holds the parsed configuration for the watch-create command
type watchCreateConfig struct {
	cloud           string
	metadata        string
	kubeConfig      string
	bastionRsa      string
	baseDomain      string
	apiKey          string
	shouldDebug     bool
}

// parseWatchCreateFlags parses command-line flags into a watchCreateConfig.
// It defines all flags on flagSet, parses args, validates required fields, and
// reads the IBMCLOUD_API_KEY environment variable. Validation messages are written
// to preLog so they can be replayed after the logger is initialised.
//
// Parameters:
//   - preLog: Buffer for pre-logger messages
//   - flagSet: FlagSet to define and parse flags on
//   - args: Command-line arguments to parse
//
// Returns:
//   - *watchCreateConfig: Populated configuration on success
//   - error: Any error encountered during parsing or validation
func parseWatchCreateFlags(preLog *strings.Builder, flagSet *flag.FlagSet, args []string) (*watchCreateConfig, error) {
	// Define command-line flags
	ptrCloud := flagSet.String(flagWatchCreateCloud, defaultWatchCreateCloud, usageWatchCreateCloud)
	ptrMetadata := flagSet.String(flagWatchCreateMetadata, defaultWatchCreateMetadata, usageWatchCreateMetadata)
	ptrKubeConfig := flagSet.String(flagWatchCreateKubeConfig, defaultWatchCreateKubeConfig, usageWatchCreateKubeConfig)
	ptrBastionRsa := flagSet.String(flagWatchCreateBastionRsa, defaultWatchCreateBastionRsa, usageWatchCreateBastionRsa)
	ptrBaseDomain := flagSet.String(flagWatchCreateBaseDomain, defaultWatchCreateBaseDomain, usageWatchCreateBaseDomain)
	ptrShouldDebug := flagSet.String(flagWatchCreateShouldDebug, defaultWatchCreateShouldDebug, usageWatchCreateShouldDebug)

	// Parse command-line arguments
	if err := flagSet.Parse(args); err != nil {
		return nil, fmt.Errorf("%sfailed to parse flags: %w", errPrefixWatchCreate, err)
	}

	// Parse debug flag
	shouldDebug, err := parseBoolFlag(*ptrShouldDebug, flagWatchCreateShouldDebug)
	if err != nil {
		return nil, fmt.Errorf("%s%w", errPrefixWatchCreate, err)
	}

	// Trim spaces from cloud parameter
	cloud := strings.TrimSpace(*ptrCloud)

	// Validate required flags
	if err := validateRequiredFlags(preLog, cloud, *ptrMetadata, *ptrBastionRsa); err != nil {
		return nil, fmt.Errorf("%w", err)
	}

	// Get API key from environment
	apiKey := os.Getenv(envIBMCloudAPIKey)

	return &watchCreateConfig{
		cloud:           cloud,
		metadata:        *ptrMetadata,
		kubeConfig:      *ptrKubeConfig,
		bastionRsa:      *ptrBastionRsa,
		baseDomain:      *ptrBaseDomain,
		apiKey:          apiKey,
		shouldDebug:     shouldDebug,
	}, nil
}

// validateRequiredFlags checks that all required watch-create flags are non-empty.
// Informational messages for each valid field are written to preLog.
//
// Parameters:
//   - preLog: Buffer for pre-logger messages
//   - cloud: OpenStack cloud name
//   - metadata: Path to the metadata.json file
//   - bastionRsa: Path to the RSA private key for the bastion VM
//
// Returns:
//   - error: An error naming the first missing required flag, or nil if all are present
func validateRequiredFlags(preLog *strings.Builder, cloud, metadata, bastionRsa string) error {
	if cloud == "" {
		return fmt.Errorf("%scloud name is required, use -%s flag", errPrefixWatchCreate, flagWatchCreateCloud)
	}
	fmt.Fprintf(preLog, "[INFO] Using cloud: %s\n", cloud)

	if metadata == "" {
		return fmt.Errorf("%smetadata file location is required, use -%s flag", errPrefixWatchCreate, flagWatchCreateMetadata)
	}

	if bastionRsa == "" {
		return fmt.Errorf("%sbastion RSA key is required, use -%s flag", errPrefixWatchCreate, flagWatchCreateBastionRsa)
	}
	fmt.Fprintf(preLog, "[INFO] Using bastion RSA key: %s\n", bastionRsa)

	return nil
}

// validateIBMCloudAPIKey validates the IBM Cloud API key when one is provided.
// An empty key is accepted (DNS component will simply be skipped).
// A non-empty key is validated by attempting to initialise the IBM Cloud service.
//
// Parameters:
//   - apiKey: IBM Cloud API key, may be empty
//
// Returns:
//   - error: nil if the key is empty or valid; an error if initialisation fails
func validateIBMCloudAPIKey(apiKey string) error {
	if apiKey == "" {
		log.Printf("[INFO] No IBM Cloud API key provided (optional)")
		return nil
	}

	log.Printf("[INFO] IBM Cloud API key found, validating...")
	if _, err := InitBXService(apiKey); err != nil {
		return fmt.Errorf("%sfailed to initialize IBM Cloud service: %w", errPrefixWatchCreate, err)
	}
	log.Printf("[INFO] IBM Cloud API key validated successfully")
	return nil
}

// validateMetadataFile checks that the metadata file at metadataPath exists and is readable.
//
// Parameters:
//   - metadataPath: Path to the metadata.json file
//
// Returns:
//   - error: nil if the file can be read; an error describing the access failure otherwise
func validateMetadataFile(metadataPath string) error {
	if _, err := os.ReadFile(metadataPath); err != nil {
		return fmt.Errorf("%sfailed to read metadata file '%s': %w", errPrefixWatchCreate, metadataPath, err)
	}
	log.Printf("[INFO] Metadata file validated successfully")
	return nil
}

// validateBastionRsaFile checks that the bastion RSA private key file exists,
// is a regular file, and is readable.
//
// Parameters:
//   - rsaPath: Path to the RSA private key file
//
// Returns:
//   - error: nil if the file is accessible; an error describing the failure otherwise
func validateBastionRsaFile(rsaPath string) error {
	fileInfo, err := os.Stat(rsaPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%sbastion RSA file '%s' does not exist", errPrefixWatchCreate, rsaPath)
		}
		return fmt.Errorf("%sbastion RSA file '%s' is not accessible: %w", errPrefixWatchCreate, rsaPath, err)
	}

	// Check if it's a regular file
	if !fileInfo.Mode().IsRegular() {
		return fmt.Errorf("%sbastion RSA file '%s' is not a regular file", errPrefixWatchCreate, rsaPath)
	}

	// Check if file is readable
	if _, err := os.ReadFile(rsaPath); err != nil {
		return fmt.Errorf("%sbastion RSA file '%s' is not readable: %w", errPrefixWatchCreate, rsaPath, err)
	}

	log.Printf("[INFO] Bastion RSA file validated successfully")
	return nil
}

// validateKubeConfigFile checks that the kubeconfig file exists, is a regular file,
// and is readable. It is only called when a kubeconfig path was provided.
//
// Parameters:
//   - kubeConfigPath: Path to the kubeconfig file
//
// Returns:
//   - error: nil if the file is accessible; an error describing the failure otherwise
func validateKubeConfigFile(kubeConfigPath string) error {
	fileInfo, err := os.Stat(kubeConfigPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%skubeconfig file '%s' does not exist", errPrefixWatchCreate, kubeConfigPath)
		}
		return fmt.Errorf("%skubeconfig file '%s' is not accessible: %w", errPrefixWatchCreate, kubeConfigPath, err)
	}

	// Check if it's a regular file
	if !fileInfo.Mode().IsRegular() {
		return fmt.Errorf("%skubeconfig file '%s' is not a regular file", errPrefixWatchCreate, kubeConfigPath)
	}

	// Check if file is readable
	if _, err := os.ReadFile(kubeConfigPath); err != nil {
		return fmt.Errorf("%skubeconfig file '%s' is not readable: %w", errPrefixWatchCreate, kubeConfigPath, err)
	}

	log.Printf("[INFO] Kubeconfig file validated successfully")
	return nil
}

// buildComponentList constructs the ordered list of RunnableObject factories to
// initialise based on the provided configuration.
//
// The OpenShift Cluster component is added only when a kubeconfig is provided.
// The IBM DNS component is added only when a base domain is provided.
// VMs and Load Balancer are always added.
//
// Parameters:
//   - config: Parsed watch-create configuration
//
// Returns:
//   - []NewRunnableObjectsEntry: Ordered list of component factories
func buildComponentList(config *watchCreateConfig) []NewRunnableObjectsEntry {
	robjsFuncs := make([]NewRunnableObjectsEntry, 0, 4)

if false {
	if config.kubeConfig != "" {
		log.Printf("[INFO] KubeConfig provided, adding %s component", componentOpenShift)
		robjsFuncs = append(robjsFuncs, NewRunnableObjectsEntry{NewOc, componentOpenShift})
	}

	log.Printf("[INFO] Adding %s component", componentVMs)
	robjsFuncs = append(robjsFuncs, NewRunnableObjectsEntry{NewVMs, componentVMs})

	log.Printf("[INFO] Adding %s component", componentLB)
	robjsFuncs = append(robjsFuncs, NewRunnableObjectsEntry{NewLoadBalancer, componentLB})
}
	if config.baseDomain != "" {
		log.Printf("[INFO] Base domain provided, adding %s component", componentDNS)
		robjsFuncs = append(robjsFuncs, NewRunnableObjectsEntry{NewIBMDNS, componentDNS})
	}

	return robjsFuncs
}

// initializeServices reads the metadata file and constructs a Services instance
// that provides shared cluster configuration to all RunnableObject implementations.
//
// Parameters:
//   - config: Parsed watch-create configuration
//
// Returns:
//   - *Services: Initialised services object
//   - error: Any error encountered loading metadata or creating services
func initializeServices(config *watchCreateConfig) (*Services, error) {
	log.Printf("[INFO] Loading metadata from file")
	metadata, err := NewMetadataFromCCMetadata(config.metadata)
	if err != nil {
		return nil, fmt.Errorf("%sfailed to load metadata from '%s': %w", errPrefixWatchCreate, config.metadata, err)
	}
	log.Debugf("metadata = %+v", metadata)

	log.Printf("[INFO] Creating services object")
	services, err := NewServices(
		metadata,
		config.apiKey,
		config.kubeConfig,
		config.cloud,
		config.bastionRsa,
		config.baseDomain,
	)
	if err != nil {
		return nil, fmt.Errorf("%sfailed to create services object: %w", errPrefixWatchCreate, err)
	}

	return services, nil
}

// queryComponentStatus sorts the provided RunnableObjects by priority and calls
// ClusterStatus on each in order. All components are queried even if individual
// ones fail; errors are accumulated and returned together at the end.
//
// Parameters:
//   - ctx: Context for cancellation support (checked before each component)
//   - robjsCluster: List of initialised RunnableObjects to query
//
// Returns:
//   - error: nil if all components succeed; an aggregated error listing each failure
func queryComponentStatus(ctx context.Context, robjsCluster []RunnableObject) error {
	if len(robjsCluster) == 0 {
		log.Printf("[INFO] No components to query")
		return nil
	}

	log.Printf("[INFO] Sorting objects by priority")
	robjsCluster = BubbleSort(robjsCluster)

	// Log sorted order in debug mode
	for _, robj := range robjsCluster {
		// Check if context was cancelled
		select {
		case <-ctx.Done():
			return fmt.Errorf("operation cancelled: %w", ctx.Err())
		default:
		}

		if robjObjectName, err := robj.ObjectName(); err == nil {
			log.Debugf("Sorted component: %s", robjObjectName)
		}
	}
	log.Printf("[INFO] Objects sorted successfully")

	log.Printf("[INFO] Querying status of %d components", len(robjsCluster))
	var errs []error
	successCount := 0

	for i, robj := range robjsCluster {
		// Check if context was cancelled
		select {
		case <-ctx.Done():
			return fmt.Errorf("operation cancelled after %d/%d components: %w", successCount, len(robjsCluster), ctx.Err())
		default:
		}

		robjObjectName, err := robj.ObjectName()
		if err != nil {
			robjObjectName = fmt.Sprintf("unknown-component-%d", i)
		}
		log.Printf("[INFO] Querying status of component %d/%d: %s", i+1, len(robjsCluster), robjObjectName)

		err = robj.ClusterStatus()
		if err != nil {
			log.Printf("[ERROR] Component %s failed: %v", robjObjectName, err)
			errs = append(errs, fmt.Errorf("%s: %w", robjObjectName, err))
		} else {
			log.Printf("[INFO] Component %s status query completed successfully", robjObjectName)
			successCount++
		}
	}

	// Report results
	if len(errs) > 0 {
		log.Printf("[WARN] Status query completed with %d errors out of %d components", len(errs), len(robjsCluster))
		return fmt.Errorf("%sfailed to query status for %d/%d components: %v", errPrefixWatchCreate, len(errs), len(robjsCluster), errs)
	}

	return nil
}
