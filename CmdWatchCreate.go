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
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/sirupsen/logrus"
)

const (
	// Flag names for watch-create command
	flagWatchCloud           = "cloud"
	flagWatchMetadata        = "metadata"
	flagWatchKubeConfig      = "kubeconfig"
	flagWatchBastionUsername = "bastionUsername"
	flagWatchBastionRsa      = "bastionRsa"
	flagWatchBaseDomain      = "baseDomain"
	flagWatchShouldDebug     = "shouldDebug"

	// Flag default values
	defaultWatchCloud           = ""
	defaultWatchMetadata        = ""
	defaultWatchKubeConfig      = ""
	defaultWatchBastionUsername = ""
	defaultWatchBastionRsa      = ""
	defaultWatchBaseDomain      = ""
	defaultWatchShouldDebug     = "false"

	// Boolean string values
	watchBoolTrue  = "true"
	watchBoolFalse = "false"

	// Error message prefixes
	errPrefixWatch = "Error: "

	// Usage messages
	usageWatchCloud           = "The cloud to use in clouds.yaml"
	usageWatchMetadata        = "The location of the metadata.json file"
	usageWatchKubeConfig      = "The KUBECONFIG file"
	usageWatchBastionUsername = "The username of the bastion VM to use"
	usageWatchBastionRsa      = "The RSA filename for the bastion VM to use"
	usageWatchBaseDomain      = "The DNS base name to use"
	usageWatchShouldDebug     = "Should output debug output"

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
		out                io.Writer
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
		return fmt.Errorf("%sflag set cannot be nil", errPrefixWatch)
	}

	// Display version information
	fmt.Fprintf(os.Stderr, "Program version is %v, release = %v\n", version, release)

	// Define command-line flags
	ptrCloud = watchCreateClusterFlags.String(flagWatchCloud, defaultWatchCloud, usageWatchCloud)
	ptrMetadata = watchCreateClusterFlags.String(flagWatchMetadata, defaultWatchMetadata, usageWatchMetadata)
	ptrKubeConfig = watchCreateClusterFlags.String(flagWatchKubeConfig, defaultWatchKubeConfig, usageWatchKubeConfig)
	ptrBastionUsername = watchCreateClusterFlags.String(flagWatchBastionUsername, defaultWatchBastionUsername, usageWatchBastionUsername)
	ptrBastionRsa = watchCreateClusterFlags.String(flagWatchBastionRsa, defaultWatchBastionRsa, usageWatchBastionRsa)
	ptrBaseDomain = watchCreateClusterFlags.String(flagWatchBaseDomain, defaultWatchBaseDomain, usageWatchBaseDomain)
	ptrShouldDebug = watchCreateClusterFlags.String(flagWatchShouldDebug, defaultWatchShouldDebug, usageWatchShouldDebug)

	// Parse command-line arguments
	err = watchCreateClusterFlags.Parse(args)
	if err != nil {
		return fmt.Errorf("%sfailed to parse flags: %w", errPrefixWatch, err)
	}

	// Parse and validate shouldDebug flag
	switch strings.ToLower(*ptrShouldDebug) {
	case watchBoolTrue:
		shouldDebug = true
		fmt.Println("[INFO] Debug mode enabled")
	case watchBoolFalse:
		shouldDebug = false
	default:
		return fmt.Errorf("%sshouldDebug must be 'true' or 'false', got '%s'", errPrefixWatch, *ptrShouldDebug)
	}

	// Configure logging based on debug flag
	if shouldDebug {
		out = os.Stderr
	} else {
		out = io.Discard
	}
	log = &logrus.Logger{
		Out:       out,
		Formatter: new(logrus.TextFormatter),
		Level:     logrus.DebugLevel,
	}

	// IBM Cloud API key is optional
	apiKey = os.Getenv(envIBMCloudAPIKey)
	if len(apiKey) != 0 {
		log.Printf("[INFO] IBM Cloud API key found, validating...")
		// Before we do a lot of work, validate the API key!
		_, err = InitBXService(apiKey)
		if err != nil {
			return fmt.Errorf("%sfailed to initialize IBM Cloud service: %w", errPrefixWatch, err)
		}
		log.Printf("[INFO] IBM Cloud API key validated successfully")
	} else {
		log.Printf("[INFO] No IBM Cloud API key provided (optional)")
	}

	// Validate required flags
	if ptrCloud == nil || *ptrCloud == "" {
		return fmt.Errorf("%scloud name is required, use -%s flag", errPrefixWatch, flagWatchCloud)
	}
	log.Printf("[INFO] Using cloud: %s", *ptrCloud)

	if *ptrMetadata == "" {
		return fmt.Errorf("%smetadata file location is required, use -%s flag", errPrefixWatch, flagWatchMetadata)
	}
	log.Printf("[INFO] Using metadata file: %s", *ptrMetadata)

	if ptrBastionUsername == nil || *ptrBastionUsername == "" {
		return fmt.Errorf("%sbastion username is required, use -%s flag", errPrefixWatch, flagWatchBastionUsername)
	}
	log.Printf("[INFO] Using bastion username: %s", *ptrBastionUsername)

	if ptrBastionRsa == nil || *ptrBastionRsa == "" {
		return fmt.Errorf("%sbastion RSA key is required, use -%s flag", errPrefixWatch, flagWatchBastionRsa)
	}
	log.Printf("[INFO] Using bastion RSA key: %s", *ptrBastionRsa)

	// Validate metadata file accessibility
	_, err = os.ReadFile(*ptrMetadata)
	if err != nil {
		return fmt.Errorf("%sfailed to read metadata file '%s': %w", errPrefixWatch, *ptrMetadata, err)
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
		return fmt.Errorf("%sfailed to load metadata from '%s': %w", errPrefixWatch, *ptrMetadata, err)
	}
	log.Debugf("metadata = %+v", metadata)

	// Create services object
	log.Printf("[INFO] Creating services object")
	services, err = NewServices(metadata, apiKey, *ptrKubeConfig, *ptrCloud, *ptrBastionUsername, *ptrBastionRsa, *ptrBaseDomain)
	if err != nil {
		return fmt.Errorf("%sfailed to create services object: %w", errPrefixWatch, err)
	}

	// Initialize runnable objects
	log.Printf("[INFO] Initializing runnable objects")
	robjsCluster, err = initializeRunnableObjects(services, robjsFuncs)
	if err != nil {
		return fmt.Errorf("%sfailed to initialize runnable objects: %w", errPrefixWatch, err)
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
