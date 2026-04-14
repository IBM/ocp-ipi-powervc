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
//   - bastionUsername: The username of the bastion VM (required)
//   - bastionRsa: The RSA filename for the bastion VM (required)
//   - baseDomain: The DNS base name to use (optional)
//   - shouldDebug: Enable debug output (true/false, default: false)
//
// The command initializes runnable objects based on the provided flags,
// sorts them by priority, and queries their status sequentially.

package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"
)

const (
	// Flag names for watch-create command
	flagWatchCreateCloud           = "cloud"
	flagWatchCreateMetadata        = "metadata"
	flagWatchCreateKubeConfig      = "kubeconfig"
	flagWatchCreateBastionUsername = "bastionUsername"
	flagWatchCreateBastionRsa      = "bastionRsa"
	flagWatchCreateBaseDomain      = "baseDomain"
	flagWatchCreateShouldDebug     = "shouldDebug"

	// Flag default values
	defaultWatchCreateCloud           = ""
	defaultWatchCreateMetadata        = ""
	defaultWatchCreateKubeConfig      = ""
	defaultWatchCreateBastionUsername = ""
	defaultWatchCreateBastionRsa      = ""
	defaultWatchCreateBaseDomain      = ""
	defaultWatchCreateShouldDebug     = "false"

	// Error message prefixes
	errPrefixWatchCreate = "Error: "

	// Usage messages
	usageWatchCreateCloud           = "The cloud to use in clouds.yaml"
	usageWatchCreateMetadata        = "The location of the metadata.json file"
	usageWatchCreateKubeConfig      = "The KUBECONFIG file"
	usageWatchCreateBastionUsername = "The username of the bastion VM to use"
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
// The function executes the following steps:
//  1. Displays program version information
//  2. Parses command-line flags
//  3. Configures logging based on debug flag
//  4. Validates IBM Cloud API key (if provided)
//  5. Validates required flags (cloud, metadata, bastionUsername, bastionRsa)
//  6. Validates metadata file accessibility
//  7. Initializes runnable objects based on provided flags
//  8. Loads metadata from file
//  9. Creates services object
// 10. Initializes and sorts runnable objects by priority
// 11. Queries status of each component
//
// Example usage:
//   err := watchCreateClusterCommand(flagSet, []string{
//       "-cloud", "mycloud",
//       "-metadata", "/path/to/metadata.json",
//       "-bastionUsername", "core",
//       "-bastionRsa", "/path/to/key.rsa",
//       "-shouldDebug", "true",
//   })
func watchCreateClusterCommand(watchCreateClusterFlags *flag.FlagSet, args []string) error {
	// Validate input parameters
	if watchCreateClusterFlags == nil {
		return fmt.Errorf("%sflag set cannot be nil", errPrefixWatchCreate)
	}

	// Display version information
	fmt.Fprintf(os.Stderr, "Program version is %v, release = %v\n", version, release)

	// Create a pre-debug log before we know that debugging is enabled
	var preLog strings.Builder

	// Parse and validate flags
	config, err := parseWatchCreateFlags(preLog, watchCreateClusterFlags, args)
	if err != nil {
		return err
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

	// Validate IBM Cloud API key if provided
	if err := validateIBMCloudAPIKey(config.apiKey); err != nil {
		return err
	}

	// Validate metadata file
	if err := validateMetadataFile(config.metadata); err != nil {
		return err
	}

	// Initialize components
	robjsFuncs := buildComponentList(config)
	log.Printf("[INFO] Initialized %d components", len(robjsFuncs))

	// Load metadata and create services
	services, err := initializeServices(config)
	if err != nil {
		return err
	}

	// Initialize and execute runnable objects
	robjsCluster, err := initializeRunnableObjects(services, robjsFuncs)
	if err != nil {
		return fmt.Errorf("%sfailed to initialize runnable objects: %w", errPrefixWatchCreate, err)
	}

	// Sort and query status
	if err := queryComponentStatus(robjsCluster); err != nil {
		return err
	}

	log.Printf("[INFO] Status query completed for all components")
	return nil
}

// watchCreateConfig holds the parsed configuration for the watch-create command
type watchCreateConfig struct {
	cloud           string
	metadata        string
	kubeConfig      string
	bastionUsername string
	bastionRsa      string
	baseDomain      string
	apiKey          string
	shouldDebug     bool
}

// parseWatchCreateFlags parses and validates command-line flags
func parseWatchCreateFlags(preLog strings.Builder, flagSet *flag.FlagSet, args []string) (*watchCreateConfig, error) {
	// Define command-line flags
	ptrCloud := flagSet.String(flagWatchCreateCloud, defaultWatchCreateCloud, usageWatchCreateCloud)
	ptrMetadata := flagSet.String(flagWatchCreateMetadata, defaultWatchCreateMetadata, usageWatchCreateMetadata)
	ptrKubeConfig := flagSet.String(flagWatchCreateKubeConfig, defaultWatchCreateKubeConfig, usageWatchCreateKubeConfig)
	ptrBastionUsername := flagSet.String(flagWatchCreateBastionUsername, defaultWatchCreateBastionUsername, usageWatchCreateBastionUsername)
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

	// Validate required flags
	if err := validateRequiredFlags(preLog, *ptrCloud, *ptrMetadata, *ptrBastionUsername, *ptrBastionRsa); err != nil {
		return nil, err
	}

	// Get API key from environment
	apiKey := os.Getenv(envIBMCloudAPIKey)

	return &watchCreateConfig{
		cloud:           *ptrCloud,
		metadata:        *ptrMetadata,
		kubeConfig:      *ptrKubeConfig,
		bastionUsername: *ptrBastionUsername,
		bastionRsa:      *ptrBastionRsa,
		baseDomain:      *ptrBaseDomain,
		apiKey:          apiKey,
		shouldDebug:     shouldDebug,
	}, nil
}

// validateRequiredFlags validates that all required flags are provided
func validateRequiredFlags(preLog strings.Builder, cloud, metadata, bastionUsername, bastionRsa string) error {
	if cloud == "" {
		return fmt.Errorf("%scloud name is required, use -%s flag", errPrefixWatchCreate, flagWatchCreateCloud)
	}
	fmt.Fprintf(&preLog, "[INFO] Using cloud: %s", cloud)

	if metadata == "" {
		return fmt.Errorf("%smetadata file location is required, use -%s flag", errPrefixWatchCreate, flagWatchCreateMetadata)
	}
	fmt.Fprintf(&preLog, "[INFO] Using metadata file: %s", metadata)

	if bastionUsername == "" {
		return fmt.Errorf("%sbastion username is required, use -%s flag", errPrefixWatchCreate, flagWatchCreateBastionUsername)
	}
	fmt.Fprintf(&preLog, "[INFO] Using bastion username: %s", bastionUsername)

	if bastionRsa == "" {
		return fmt.Errorf("%sbastion RSA key is required, use -%s flag", errPrefixWatchCreate, flagWatchCreateBastionRsa)
	}
	fmt.Fprintf(&preLog, "[INFO] Using bastion RSA key: %s", bastionRsa)

	return nil
}

// validateIBMCloudAPIKey validates the IBM Cloud API key if provided
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

// validateMetadataFile validates that the metadata file exists and is readable
func validateMetadataFile(metadataPath string) error {
	if _, err := os.ReadFile(metadataPath); err != nil {
		return fmt.Errorf("%sfailed to read metadata file '%s': %w", errPrefixWatchCreate, metadataPath, err)
	}
	log.Printf("[INFO] Metadata file validated successfully")
	return nil
}

// buildComponentList builds the list of components to monitor based on configuration
func buildComponentList(config *watchCreateConfig) []NewRunnableObjectsEntry {
	robjsFuncs := make([]NewRunnableObjectsEntry, 0, 4)

	if config.kubeConfig != "" {
		log.Printf("[INFO] KubeConfig provided, adding %s component", componentOpenShift)
		robjsFuncs = append(robjsFuncs, NewRunnableObjectsEntry{NewOc, componentOpenShift})
	}

	log.Printf("[INFO] Adding %s component", componentVMs)
	robjsFuncs = append(robjsFuncs, NewRunnableObjectsEntry{NewVMs, componentVMs})

	log.Printf("[INFO] Adding %s component", componentLB)
	robjsFuncs = append(robjsFuncs, NewRunnableObjectsEntry{NewLoadBalancer, componentLB})

	if config.baseDomain != "" {
		log.Printf("[INFO] Base domain provided, adding %s component", componentDNS)
		robjsFuncs = append(robjsFuncs, NewRunnableObjectsEntry{NewIBMDNS, componentDNS})
	}

	return robjsFuncs
}

// initializeServices loads metadata and creates the services object
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
		config.bastionUsername,
		config.bastionRsa,
		config.baseDomain,
	)
	if err != nil {
		return nil, fmt.Errorf("%sfailed to create services object: %w", errPrefixWatchCreate, err)
	}

	return services, nil
}

// queryComponentStatus sorts components by priority and queries their status
func queryComponentStatus(robjsCluster []RunnableObject) error {
	if len(robjsCluster) == 0 {
		log.Printf("[INFO] No components to query")
		return nil
	}

	log.Printf("[INFO] Sorting objects by priority")
	robjsCluster = BubbleSort(robjsCluster)

	// Log sorted order in debug mode
	for _, robj := range robjsCluster {
		if robjObjectName, err := robj.ObjectName(); err == nil {
			log.Debugf("Sorted component: %s", robjObjectName)
		}
	}
	log.Printf("[INFO] Objects sorted successfully")

	log.Printf("[INFO] Querying status of %d components", len(robjsCluster))
	for i, robj := range robjsCluster {
		robjObjectName, err := robj.ObjectName()
		if err != nil {
			robjObjectName = fmt.Sprintf("unknown-component-%d", i)
		}
		log.Printf("[INFO] Querying status of component %d/%d: %s", i+1, len(robjsCluster), robjObjectName)
		robj.ClusterStatus()
	}

	return nil
}
