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
	var (
		preLog             strings.Builder
		apiKey             string
		ptrCloud           *string
		ptrMetadata        *string
		ptrKubeConfig      *string
		ptrBastionUsername *string
		ptrBastionRsa      *string
		ptrBaseDomain      *string
		ptrShouldDebug     *string
		metadata           *Metadata
		services           *Services
		robjsFuncs         []NewRunnableObjectsEntry
		robjsCluster       []RunnableObject
		robjObjectName     string
		err                error
	)

	// Validate input parameters
	if watchCreateClusterFlags == nil {
		return fmt.Errorf("%sflag set cannot be nil", errPrefixWatchCreate)
	}

	// Display version information
	fmt.Fprintf(os.Stderr, "Program version is %v, release = %v\n", version, release)

	// Define command-line flags
	ptrCloud = watchCreateClusterFlags.String(flagWatchCreateCloud, defaultWatchCreateCloud, usageWatchCreateCloud)
	ptrMetadata = watchCreateClusterFlags.String(flagWatchCreateMetadata, defaultWatchCreateMetadata, usageWatchCreateMetadata)
	ptrKubeConfig = watchCreateClusterFlags.String(flagWatchCreateKubeConfig, defaultWatchCreateKubeConfig, usageWatchCreateKubeConfig)
	ptrBastionUsername = watchCreateClusterFlags.String(flagWatchCreateBastionUsername, defaultWatchCreateBastionUsername, usageWatchCreateBastionUsername)
	ptrBastionRsa = watchCreateClusterFlags.String(flagWatchCreateBastionRsa, defaultWatchCreateBastionRsa, usageWatchCreateBastionRsa)
	ptrBaseDomain = watchCreateClusterFlags.String(flagWatchCreateBaseDomain, defaultWatchCreateBaseDomain, usageWatchCreateBaseDomain)
	ptrShouldDebug = watchCreateClusterFlags.String(flagWatchCreateShouldDebug, defaultWatchCreateShouldDebug, usageWatchCreateShouldDebug)

	// Parse command-line arguments
	err = watchCreateClusterFlags.Parse(args)
	if err != nil {
		return fmt.Errorf("%sfailed to parse flags: %w", errPrefixWatchCreate, err)
	}

	// Parse debug flag
	shouldDebug, err := parseBoolFlag(*ptrShouldDebug, flagWatchCreateShouldDebug)
	if err != nil {
		return fmt.Errorf("%s%w", errPrefixWatchCreate, err)
	}

	// Initialize logger
	log = initLogger(shouldDebug)
	if shouldDebug {
		log.Debugf("Debug mode enabled")
	}

	// Dump the prelogged lines now that log has been initialized!
	scanner := bufio.NewScanner(strings.NewReader(preLog.String()))
	for scanner.Scan() {
		line := scanner.Text() // Each line as a string
		log.Println(line)
	}

	// IBM Cloud API key is optional
	apiKey = os.Getenv(envIBMCloudAPIKey)
	if len(apiKey) != 0 {
		log.Printf("[INFO] IBM Cloud API key found, validating...")
		// Before we do a lot of work, validate the API key!
		_, err = InitBXService(apiKey)
		if err != nil {
			return fmt.Errorf("%sfailed to initialize IBM Cloud service: %w", errPrefixWatchCreate, err)
		}
		log.Printf("[INFO] IBM Cloud API key validated successfully")
	} else {
		log.Printf("[INFO] No IBM Cloud API key provided (optional)")
	}

	// Validate required flags
	if ptrCloud == nil || *ptrCloud == "" {
		return fmt.Errorf("%scloud name is required, use -%s flag", errPrefixWatchCreate, flagWatchCreateCloud)
	}
	log.Printf("[INFO] Using cloud: %s", *ptrCloud)

	if *ptrMetadata == "" {
		return fmt.Errorf("%smetadata file location is required, use -%s flag", errPrefixWatchCreate, flagWatchCreateMetadata)
	}
	log.Printf("[INFO] Using metadata file: %s", *ptrMetadata)

	if ptrBastionUsername == nil || *ptrBastionUsername == "" {
		return fmt.Errorf("%sbastion username is required, use -%s flag", errPrefixWatchCreate, flagWatchCreateBastionUsername)
	}
	log.Printf("[INFO] Using bastion username: %s", *ptrBastionUsername)

	if ptrBastionRsa == nil || *ptrBastionRsa == "" {
		return fmt.Errorf("%sbastion RSA key is required, use -%s flag", errPrefixWatchCreate, flagWatchCreateBastionRsa)
	}
	log.Printf("[INFO] Using bastion RSA key: %s", *ptrBastionRsa)

	// Validate metadata file accessibility
	_, err = os.ReadFile(*ptrMetadata)
	if err != nil {
		return fmt.Errorf("%sfailed to read metadata file '%s': %w", errPrefixWatchCreate, *ptrMetadata, err)
	}
	log.Printf("[INFO] Metadata file validated successfully")

	// Initialize runnable objects list based on provided flags
	robjsFuncs = make([]NewRunnableObjectsEntry, 0)
	if *ptrKubeConfig != "" {
		log.Printf("[INFO] KubeConfig provided, adding %s component", componentOpenShift)
		robjsFuncs = append(robjsFuncs, NewRunnableObjectsEntry{NewOc, componentOpenShift})
	}
	log.Printf("[INFO] Adding %s component", componentVMs)
	robjsFuncs = append(robjsFuncs, NewRunnableObjectsEntry{NewVMs, componentVMs})
	log.Printf("[INFO] Adding %s component", componentLB)
	robjsFuncs = append(robjsFuncs, NewRunnableObjectsEntry{NewLoadBalancer, componentLB})
	if *ptrBaseDomain != "" {
		log.Printf("[INFO] Base domain provided, adding %s component", componentDNS)
		robjsFuncs = append(robjsFuncs, NewRunnableObjectsEntry{NewIBMDNS, componentDNS})
	}
	log.Printf("[INFO] Initialized %d components", len(robjsFuncs))

	// Load metadata from file
	log.Printf("[INFO] Loading metadata from file")
	metadata, err = NewMetadataFromCCMetadata(*ptrMetadata)
	if err != nil {
		return fmt.Errorf("%sfailed to load metadata from '%s': %w", errPrefixWatchCreate, *ptrMetadata, err)
	}
	log.Debugf("metadata = %+v", metadata)

	// Create services object
	log.Printf("[INFO] Creating services object")
	services, err = NewServices(metadata, apiKey, *ptrKubeConfig, *ptrCloud, *ptrBastionUsername, *ptrBastionRsa, *ptrBaseDomain)
	if err != nil {
		return fmt.Errorf("%sfailed to create services object: %w", errPrefixWatchCreate, err)
	}

	// Initialize runnable objects
	log.Printf("[INFO] Initializing runnable objects")
	robjsCluster, err = initializeRunnableObjects(services, robjsFuncs)
	if err != nil {
		return fmt.Errorf("%sfailed to initialize runnable objects: %w", errPrefixWatchCreate, err)
	}

	// Sort the objects by their priority
	log.Printf("[INFO] Sorting objects by priority")
	robjsCluster = BubbleSort(robjsCluster)
	for _, robj := range robjsCluster {
		robjObjectName, _ = robj.ObjectName()
		log.Debugf("Sorted %s %+v", robjObjectName, robj)
	}
	log.Printf("[INFO] Objects sorted successfully")

	// Query the status of the objects
	log.Printf("[INFO] Querying status of %d components", len(robjsCluster))
	for i, robj := range robjsCluster {
		robjObjectName, _ = robj.ObjectName()
		log.Printf("[INFO] Querying status of component %d/%d: %s", i+1, len(robjsCluster), robjObjectName)
		robj.ClusterStatus()
	}

	log.Printf("[INFO] Status query completed for all components")
	return nil
}
