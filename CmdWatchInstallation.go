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

// Package main provides the watch-installation command implementation.
//
// This file implements the watch-installation command which monitors OpenShift
// cluster installations and manages associated infrastructure resources. The command
// continuously watches for changes in cluster VMs and automatically updates:
//
//   - DNS records for cluster nodes and services
//   - HAProxy load balancer configuration
//   - DHCP server configuration (optional)
//   - Bastion server setup and metadata
//
// The command accepts the following flags:
//   - cloud: The cloud to use in clouds.yaml (required)
//   - domainName: The DNS domain to use (required)
//   - bastionMetadata: Root directory where OpenShift cluster installs are located (required)
//   - bastionUsername: The username of the bastion VM (required)
//   - bastionRsa: The RSA filename for the bastion VM (required)
//   - enableDhcpd: Enable the DHCP server (true/false, default: false)
//   - dhcpInterface: The interface name for the DHCP server (required if enableDhcpd=true)
//   - dhcpSubnet: The subnet for DHCP requests (required if enableDhcpd=true)
//   - dhcpNetmask: The netmask for DHCP requests (required if enableDhcpd=true)
//   - dhcpRouter: The router for DHCP requests (required if enableDhcpd=true)
//   - dhcpDnsServers: The DNS servers for DHCP requests (required if enableDhcpd=true)
//   - dhcpServerId: The DNS server identifier for DHCP requests (required if enableDhcpd=true)
//   - shouldDebug: Enable debug output (true/false, default: false)
//
// The command runs continuously, waking up every 30 seconds to check for changes
// in the cluster infrastructure and updating configurations as needed.
//
// Example usage:
//   ./tool watch-installation --cloud mycloud --domainName example.com \
//     --bastionMetadata /path/to/metadata --bastionUsername core \
//     --bastionRsa /path/to/key.rsa --shouldDebug true

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"

	"github.com/IBM/go-sdk-core/v5/core"

	"github.com/IBM/networking-go-sdk/dnsrecordsv1"
	"github.com/IBM/networking-go-sdk/zonesv1"

	"github.com/IBM/platform-services-go-sdk/globalcatalogv1"
	"github.com/IBM/platform-services-go-sdk/resourcecontrollerv2"

	"k8s.io/apimachinery/pkg/util/sets"
)

const (
	// Flag names for watch-installation command
	flagWatchInstallationCloud            = "cloud"
	flagWatchInstallationDomainName       = "domainName"
	flagWatchInstallationBastionMetadata  = "bastionMetadata"
	flagWatchInstallationBastionUsername  = "bastionUsername"
	flagWatchInstallationBastionRsa       = "bastionRsa"
	flagWatchInstallationEnableDhcpd      = "enableDhcpd"
	flagWatchInstallationDhcpInterface    = "dhcpInterface"
	flagWatchInstallationDhcpSubnet       = "dhcpSubnet"
	flagWatchInstallationDhcpNetmask      = "dhcpNetmask"
	flagWatchInstallationDhcpRouter       = "dhcpRouter"
	flagWatchInstallationDhcpDnsServers   = "dhcpDnsServers"
	flagWatchInstallationDhcpServerId     = "dhcpServerId"
	flagWatchInstallationShouldDebug      = "shouldDebug"

	// Flag default values
	defaultWatchInstallationCloud            = ""
	defaultWatchInstallationDomainName       = ""
	defaultWatchInstallationBastionMetadata  = ""
	defaultWatchInstallationBastionUsername  = ""
	defaultWatchInstallationBastionRsa       = ""
	defaultWatchInstallationEnableDhcpd      = "false"
	defaultWatchInstallationDhcpInterface    = ""
	defaultWatchInstallationDhcpSubnet       = ""
	defaultWatchInstallationDhcpNetmask      = ""
	defaultWatchInstallationDhcpRouter       = ""
	defaultWatchInstallationDhcpDnsServers   = ""
	defaultWatchInstallationDhcpServerId     = ""
	defaultWatchInstallationShouldDebug      = "false"

	// Usage messages
	usageWatchInstallationCloud            = "The cloud to use in clouds.yaml"
	usageWatchInstallationDomainName       = "The DNS domain to use"
	usageWatchInstallationBastionMetadata  = "A root directory where OpenShift clusters installs are located"
	usageWatchInstallationBastionUsername  = "The username of the bastion VM to use"
	usageWatchInstallationBastionRsa       = "The RSA filename for the bastion VM to use"
	usageWatchInstallationEnableDhcpd      = "Should enable the dhcpd server"
	usageWatchInstallationDhcpInterface    = "The interface name for the dhcpd server"
	usageWatchInstallationDhcpSubnet       = "The subnet for a DHCP request"
	usageWatchInstallationDhcpNetmask      = "The netmask for a DHCP request"
	usageWatchInstallationDhcpRouter       = "The router for a DHCP request"
	usageWatchInstallationDhcpDnsServers   = "The DNS servers for a DHCP request"
	usageWatchInstallationDhcpServerId     = "The DNS server identifier for a DHCP request"
	usageWatchInstallationShouldDebug      = "Should output debug output"

	// Error message prefix
	errPrefixWatchInstallation = "Error: "

	// Timing constants
	watchSleepDuration    = 30 * time.Second
	watchContextTimeout   = 5 * time.Minute
	watchLongTimeout      = 24 * time.Hour
	bastionContextTimeout = 10 * time.Minute

	// Network constants
	listenPort = ":8080"
)

var (
	bastionRsa string // @HACK - Global variable for bastion RSA key path
)

//      scanner := bufio.NewScanner(conn)
//      for scanner.Scan() {
//              data = scanner.Text()
// vs

//      reader := bufio.NewReader(conn)
//      for {
//              data, err = reader.ReadString('\n')
// vs

// stringArray is a custom type to hold an array of strings.
// It implements the flag.Value interface to support multiple flag values.
type stringArray []string

// String implements the flag.Value interface's String method.
// It returns a comma-separated string of all values.
func (s *stringArray) String() string {
	return strings.Join(*s, ",")
}

// Set implements the flag.Value interface's Set method.
// It appends the provided value to the string array.
func (s *stringArray) Set(value string) error {
	*s = append(*s, value)
	return nil
}

// bastionInformation holds configuration and state information for a bastion server.
// It contains metadata about the cluster installation and connection details.
type bastionInformation struct {
	Valid        bool   // Whether the bastion information is valid
	Metadata     string // Path to metadata file
	Username     string // SSH username for bastion
	InstallerRsa string // Path to RSA key for installer

	ClusterName string // Name of the OpenShift cluster
	InfraID     string // Infrastructure ID of the cluster
	IPAddress   string // IP address of the bastion server
	NumVMs      int    // Number of VMs in the cluster
}

// watchInstallationCommand executes the watch-installation command with the given flags and arguments.
//
// This function continuously monitors OpenShift cluster installations and manages associated
// infrastructure resources including DNS records, HAProxy configuration, and optionally DHCP
// server configuration. It runs in an infinite loop, checking for changes every 30 seconds.
//
// Parameters:
//   - watchInstallationFlags: The FlagSet containing command-line flags (must not be nil)
//   - args: Command-line arguments to parse
//
// Returns:
//   - error: Any error encountered during flag parsing, validation, or operation execution
//
// The function executes the following steps:
//  1. Validates input parameters
//  2. Displays program version information
//  3. Retrieves IBM Cloud API key from environment
//  4. Defines and parses command-line flags
//  5. Validates all required flags
//  6. Configures logging based on debug flag
//  7. Spawns metadata listener goroutine
//  8. Enters infinite monitoring loop:
//     - Gathers bastion information from metadata directories
//     - Retrieves all servers from OpenStack
//     - Detects added/deleted servers
//     - Updates bastion configurations
//     - Updates DNS records
//     - Updates HAProxy configuration
//     - Updates DHCP configuration (if enabled)
//     - Sleeps for 30 seconds before next iteration
//
// Example usage:
//   err := watchInstallationCommand(flagSet, []string{
//       "--cloud", "mycloud",
//       "--domainName", "example.com",
//       "--bastionMetadata", "/path/to/metadata",
//       "--bastionUsername", "core",
//       "--bastionRsa", "/path/to/key.rsa",
//   })
func watchInstallationCommand(watchInstallationFlags *flag.FlagSet, args []string) error {
	var (
		preLog              strings.Builder
		apiKey              string
		ptrCloud            *string
		ptrDomainName       *string
		ptrBastionMetadata  *string
		ptrBastionUsername  *string
		ptrBastionRsa       *string
		ptrDhcpInterface    *string
		ptrDhcpSubnet       *string
		ptrDhcpNetmask      *string
		ptrDhcpRouter       *string
		ptrDhcpDnsServers   *string
		ptrDhcpServerId     *string
		ptrEnableDhcpd      *string
		ptrShouldDebug      *string
		enableDhcpd         = false
		ctx                 context.Context
		cancel              context.CancelFunc
		knownServers        = sets.Set[string]{}
		newServerSet        sets.Set[string]
		addedServersSet     sets.Set[string]
		deletedServerSet    sets.Set[string]
		allServers          []servers.Server
		bastionInformations []bastionInformation
		err                 error
	)


	// Validate input parameters
	if watchInstallationFlags == nil {
		return fmt.Errorf("%sflag set cannot be nil", errPrefixWatchInstallation)
	}

	// Display version information
	fmt.Fprintf(os.Stderr, "Program version is %v, release = %v\n", version, release)

	// Retrieve IBM Cloud API key from environment
	apiKey = os.Getenv("IBMCLOUD_API_KEY")

	// Create a pre-debug log before we know that debugging is enabled
	fmt.Fprintf(&preLog, "[INFO] Starting watch-installation command\n")

	// Define command-line flags
	ptrCloud = watchInstallationFlags.String(flagWatchInstallationCloud, defaultWatchInstallationCloud, usageWatchInstallationCloud)
	ptrDomainName = watchInstallationFlags.String(flagWatchInstallationDomainName, defaultWatchInstallationDomainName, usageWatchInstallationDomainName)
	ptrBastionMetadata = watchInstallationFlags.String(flagWatchInstallationBastionMetadata, defaultWatchInstallationBastionMetadata, usageWatchInstallationBastionMetadata)
	ptrBastionUsername = watchInstallationFlags.String(flagWatchInstallationBastionUsername, defaultWatchInstallationBastionUsername, usageWatchInstallationBastionUsername)
	ptrBastionRsa = watchInstallationFlags.String(flagWatchInstallationBastionRsa, defaultWatchInstallationBastionRsa, usageWatchInstallationBastionRsa)
	ptrEnableDhcpd = watchInstallationFlags.String(flagWatchInstallationEnableDhcpd, defaultWatchInstallationEnableDhcpd, usageWatchInstallationEnableDhcpd)
	ptrDhcpInterface = watchInstallationFlags.String(flagWatchInstallationDhcpInterface, defaultWatchInstallationDhcpInterface, usageWatchInstallationDhcpInterface)
	ptrDhcpSubnet = watchInstallationFlags.String(flagWatchInstallationDhcpSubnet, defaultWatchInstallationDhcpSubnet, usageWatchInstallationDhcpSubnet)
	ptrDhcpNetmask = watchInstallationFlags.String(flagWatchInstallationDhcpNetmask, defaultWatchInstallationDhcpNetmask, usageWatchInstallationDhcpNetmask)
	ptrDhcpRouter = watchInstallationFlags.String(flagWatchInstallationDhcpRouter, defaultWatchInstallationDhcpRouter, usageWatchInstallationDhcpRouter)
	ptrDhcpDnsServers = watchInstallationFlags.String(flagWatchInstallationDhcpDnsServers, defaultWatchInstallationDhcpDnsServers, usageWatchInstallationDhcpDnsServers)
	ptrDhcpServerId = watchInstallationFlags.String(flagWatchInstallationDhcpServerId, defaultWatchInstallationDhcpServerId, usageWatchInstallationDhcpServerId)
	ptrShouldDebug = watchInstallationFlags.String(flagWatchInstallationShouldDebug, defaultWatchInstallationShouldDebug, usageWatchInstallationShouldDebug)

	// Parse command-line arguments
	err = watchInstallationFlags.Parse(args)
	if err != nil {
		return fmt.Errorf("%sfailed to parse flags: %w", errPrefixWatchInstallation, err)
	}

	// Validate required flags
	fmt.Fprintf(&preLog, "[INFO] Validating required flags\n")
	if ptrCloud == nil || *ptrCloud == "" {
		return fmt.Errorf("%s--%s not specified", errPrefixWatchInstallation, flagWatchInstallationCloud)
	}
	if ptrDomainName == nil || *ptrDomainName == "" {
		return fmt.Errorf("%s--%s not specified", errPrefixWatchInstallation, flagWatchInstallationDomainName)
	}
	if ptrBastionMetadata == nil || *ptrBastionMetadata == "" {
		return fmt.Errorf("%s--%s not specified", errPrefixWatchInstallation, flagWatchInstallationBastionMetadata)
	}
	if ptrBastionUsername == nil || *ptrBastionUsername == "" {
		return fmt.Errorf("%s--%s not specified", errPrefixWatchInstallation, flagWatchInstallationBastionUsername)
	}
	if ptrBastionRsa == nil || *ptrBastionRsa == "" {
		return fmt.Errorf("%s--%s not specified", errPrefixWatchInstallation, flagWatchInstallationBastionRsa)
	}
	fmt.Fprintf(&preLog, "[INFO] Required flags validated successfully\n")

	// Parse and validate enableDhcpd flag
	fmt.Fprintf(&preLog, "[INFO] Validating DHCP configuration\n")
	switch strings.ToLower(*ptrEnableDhcpd) {
	case boolTrue:
		enableDhcpd = true
		fmt.Fprintf(&preLog, "[INFO] DHCP server enabled\n")

		if ptrDhcpInterface == nil || *ptrDhcpInterface == "" {
			return fmt.Errorf("%s--%s not specified (required when DHCP is enabled)", errPrefixWatchInstallation, flagWatchInstallationDhcpInterface)
		}
		if ptrDhcpSubnet == nil || *ptrDhcpSubnet == "" {
			return fmt.Errorf("%s--%s not specified (required when DHCP is enabled)", errPrefixWatchInstallation, flagWatchInstallationDhcpSubnet)
		}
		if ptrDhcpNetmask == nil || *ptrDhcpNetmask == "" {
			return fmt.Errorf("%s--%s not specified (required when DHCP is enabled)", errPrefixWatchInstallation, flagWatchInstallationDhcpNetmask)
		}
		if ptrDhcpRouter == nil || *ptrDhcpRouter == "" {
			return fmt.Errorf("%s--%s not specified (required when DHCP is enabled)", errPrefixWatchInstallation, flagWatchInstallationDhcpRouter)
		}
		if ptrDhcpDnsServers == nil || *ptrDhcpDnsServers == "" {
			return fmt.Errorf("%s--%s not specified (required when DHCP is enabled)", errPrefixWatchInstallation, flagWatchInstallationDhcpDnsServers)
		}
		if ptrDhcpServerId == nil || *ptrDhcpServerId == "" {
			return fmt.Errorf("%s--%s not specified (required when DHCP is enabled)", errPrefixWatchInstallation, flagWatchInstallationDhcpServerId)
		}
		fmt.Fprintf(&preLog, "[INFO] DHCP configuration validated successfully\n")
	case boolFalse:
		enableDhcpd = false
		fmt.Fprintf(&preLog, "[INFO] DHCP server disabled\n")
	default:
		return fmt.Errorf("%s%s must be 'true' or 'false', got '%s'", errPrefixWatchInstallation, flagWatchInstallationEnableDhcpd, *ptrEnableDhcpd)
	}

	// Parse debug flag
	shouldDebug, err := parseBoolFlag(*ptrShouldDebug, flagWatchInstallationShouldDebug)
	if err != nil {
		return fmt.Errorf("%s%w", errPrefixWatchInstallation, err)
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

	// Store bastion RSA key path in global variable
	bastionRsa = *ptrBastionRsa

	// Create initial context with timeout
	ctx, cancel = context.WithTimeout(context.TODO(), watchContextTimeout)
	defer cancel()

	// Spawn metadata listener goroutine
	log.Printf("[INFO] Starting metadata listener on port %s", listenPort)
	go listenForCommands(*ptrCloud)

	// Enter infinite monitoring loop
	log.Printf("[INFO] Entering monitoring loop")
	for true {
		log.Printf("[INFO] Waking up to check for changes")

		// Gather bastion information from metadata directories
		log.Printf("[INFO] Gathering bastion information from: %s", *ptrBastionMetadata)
		bastionInformations, err = gatherBastionInformations(*ptrBastionMetadata, *ptrBastionUsername, *ptrBastionRsa)
		if err != nil {
			return fmt.Errorf("failed to gather bastion information: %w", err)
		}
		log.Debugf("bastionInformations [%d] = %+v", len(bastionInformations), bastionInformations)
		log.Printf("[INFO] Found %d bastion(s)", len(bastionInformations))

		// Create new context for this iteration
		ctx, cancel = context.WithTimeout(context.TODO(), watchLongTimeout)
		defer cancel()

		// Retrieve all servers from OpenStack
		log.Printf("[INFO] Retrieving all servers from cloud: %s", *ptrCloud)
		allServers, err = getAllServers(ctx, *ptrCloud)
		if err != nil {
			return fmt.Errorf("failed to get all servers: %w", err)
		}
		log.Printf("[INFO] Retrieved %d server(s)", len(allServers))

		// Detect changes in server set
		newServerSet = getServerSet(allServers)
		addedServersSet = newServerSet.Difference(knownServers)
		deletedServerSet = knownServers.Difference(newServerSet)
		log.Debugf("knownServers     = %+v", knownServers)
		log.Debugf("newServerSet     = %+v", newServerSet)
		log.Debugf("addedServersSet  = %+v", addedServersSet)
		log.Debugf("deletedServerSet = %+v", deletedServerSet)

		// If no changes detected, sleep and continue
		if addedServersSet.Len() == 0 && deletedServerSet.Len() == 0 {
			log.Printf("[INFO] No server changes detected")
			log.Printf("[INFO] Sleeping for %v", watchSleepDuration)

			time.Sleep(watchSleepDuration)

			continue
		}

		// Update known servers set
		log.Printf("[INFO] Server changes detected: %d added, %d deleted", addedServersSet.Len(), deletedServerSet.Len())
		knownServers = newServerSet

		// Update bastion configurations
		log.Printf("[INFO] Updating bastion configurations")
		err = updateBastionInformations(ctx, *ptrCloud, bastionInformations)
		if err != nil {
			return fmt.Errorf("failed to update bastion information: %w", err)
		}
		log.Printf("[INFO] Bastion configurations updated successfully")

		// Update DHCP configuration if enabled
		log.Debugf("enableDhcpd = %v", enableDhcpd)
		if enableDhcpd {
			log.Printf("[INFO] Updating DHCP configuration")
			filename := "/tmp/dhcpd.conf"
			err = dhcpdConf(ctx,
				filename,
				*ptrCloud,
				*ptrDomainName,
				*ptrDhcpInterface,
				*ptrDhcpSubnet,
				*ptrDhcpNetmask,
				*ptrDhcpRouter,
				*ptrDhcpDnsServers,
				*ptrDhcpServerId,
			)
			if err != nil {
				return fmt.Errorf("failed to generate DHCP configuration: %w", err)
			}
			log.Printf("[INFO] DHCP configuration generated: %s", filename)

			log.Printf("[INFO] Copying DHCP configuration to /etc/dhcp/dhcpd.conf")
			err = runSplitCommand([]string{
				"sudo",
				"/usr/bin/cp",
				filename,
				"/etc/dhcp/dhcpd.conf",
			})
			if err != nil {
				return fmt.Errorf("failed to copy DHCP configuration: %w", err)
			}

			log.Printf("[INFO] Restarting DHCP service")
			err = runSplitCommand([]string{
				"sudo",
				"systemctl",
				"restart",
				"dhcpd.service",
			})
			if err != nil {
				return fmt.Errorf("failed to restart DHCP service: %w", err)
			}
			log.Printf("[INFO] DHCP service restarted successfully")
		}

		// Update HAProxy configuration
		log.Printf("[INFO] Updating HAProxy configuration")
		err = haproxyCfg(ctx, *ptrCloud, bastionInformations)
		if err != nil {
			return fmt.Errorf("failed to update HAProxy configuration: %w", err)
		}
		log.Printf("[INFO] HAProxy configuration updated successfully")

		// Update DNS records if API key is available
		if apiKey != "" {
			log.Printf("[INFO] Updating DNS records")
			err = dnsRecords(ctx,
				*ptrCloud,
				apiKey,
				*ptrDomainName,
				bastionInformations,
				knownServers,
				addedServersSet,
				deletedServerSet,
			)
			if err != nil {
				return fmt.Errorf("failed to update DNS records: %w", err)
			}
			log.Printf("[INFO] DNS records updated successfully")
		} else {
			log.Printf("[INFO] Skipping DNS updates (no API key provided)")
		}

		// Sleep before next iteration
		log.Printf("[INFO] Iteration complete, sleeping for %v", watchSleepDuration)

		time.Sleep(watchSleepDuration)
	}

	return nil
}

// gatherBastionInformations walks a directory tree to find all metadata.json files
// and creates bastionInformation entries for each cluster installation found.
//
// Parameters:
//   - rootPath: Root directory to search for cluster installations
//   - username: SSH username for bastion servers
//   - installerRsa: Path to RSA key for installer access
//
// Returns:
//   - bastionInformations: Slice of bastion information for each found cluster
//   - err: Any error encountered during directory walk
//
// The function searches for files matching the pattern "*/metadata.json" and creates
// a bastionInformation entry for each one found. Errors accessing individual paths
// are logged but do not stop the walk.
func gatherBastionInformations(rootPath string, username string, installerRsa string) (bastionInformations []bastionInformation, err error) {
	bastionInformations = make([]bastionInformation, 0)

	err = filepath.WalkDir(rootPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// Handle the error (e.g., permission denied)
			log.Debugf("gatherBastionInformations: Error accessing path %q: %v", path, err)

			// Skip this problematic entry
			return nil
		}

		// Process the current file or directory entry
		if !d.IsDir() && strings.HasSuffix(path, "/metadata.json") {
			log.Debugf("gatherBastionInformations: FOUND: %s", path)
			bastionInformations = append(bastionInformations, bastionInformation{
				Valid:        false,
				Metadata:     path,
				Username:     username,
				InstallerRsa: installerRsa,
			})
		}

		// Return nil to continue the walk
		return nil
	})

	return
}

// MinimalMetadata represents the essential cluster metadata fields needed
// for cluster identification and management.
type MinimalMetadata struct {
	ClusterName string `json:"clusterName"` // Name of the OpenShift cluster
	ClusterID   string `json:"clusterID"`   // Unique cluster identifier
	InfraID     string `json:"infraID"`     // Infrastructure ID for the cluster
}

// getMetadataClusterName reads a metadata.json file and extracts the cluster name and infrastructure ID.
//
// Parameters:
//   - filename: Path to the metadata.json file
//
// Returns:
//   - clusterName: Name of the cluster from metadata
//   - infraID: Infrastructure ID from metadata
//   - err: Any error encountered reading or parsing the file
//
// The function reads the JSON file, unmarshals it into MinimalMetadata structure,
// and returns the cluster name and infrastructure ID fields.
func getMetadataClusterName(filename string) (clusterName string, infraID string, err error) {
	var (
		content  []byte
		metadata MinimalMetadata
	)

	content, err = os.ReadFile(filename)
	if err != nil {
		log.Debugf("Error when opening file: %v", err)
		return
	}
	log.Debugf("getMetadataClusterName: content = %s", string(content))

	err = json.Unmarshal(content, &metadata)
	if err != nil {
		log.Debugf("Error during Unmarshal(): %v", err)
		return
	}
	log.Debugf("getMetadataClusterName: metadata = %+v", metadata)

	clusterName = metadata.ClusterName
	infraID = metadata.InfraID

	return
}

// updateBastionInformations refreshes bastion server information for all clusters.
//
// Parameters:
//   - ctx: Context for the operation
//   - cloud: Cloud name to query for servers
//   - bastionInformations: Slice of bastion information to update
//
// Returns:
//   - error: Any error encountered during the update process
//
// The function performs the following for each bastion:
//  1. Retrieves all servers from OpenStack
//  2. Reads cluster metadata to get cluster name and infrastructure ID
//  3. Finds the bastion server in the server list
//  4. Extracts the bastion's IP address
//  5. Adds the server to known_hosts
//  6. Counts VMs belonging to the cluster
//  7. Updates bastion information with current state
//
// Bastions that cannot be found or have errors are marked as invalid
// and skipped without failing the entire operation.
func updateBastionInformations(ctx context.Context, cloud string, bastionInformations []bastionInformation) (err error) {
	var (
		allServers []servers.Server
	)

	allServers, err = getAllServers(ctx, cloud)
	if err != nil {
		return
	}

	for i, bastionInformation := range bastionInformations {
		var (
			clusterName      string
			infraID          string
			bastionServer    servers.Server
			bastionIpAddress string
		)

		log.Debugf("updateBastionInformations: OLD bastionInformation = %+v", bastionInformation)

		bastionInformations[i].Valid = false

		// Refresh the data
		clusterName, infraID, err = getMetadataClusterName(bastionInformation.Metadata)
		if err != nil {
			errstr := strings.TrimSpace(err.Error())
			if !strings.HasSuffix(errstr, "no such file or directory") {
				return err
			}
			err = nil
			continue
		}

		bastionServer, err = findServerInList(allServers, clusterName)
		if err != nil {
			log.Debugf("updateBastionInformations: findServerInList returns %v", err)
			// Skip it
			err = nil
			continue
		}
		log.Debugf("updateBastionInformations: bastionServer.Name = %s", bastionServer.Name)

		_, bastionIpAddress, err = findIpAddress(bastionServer)
		log.Debugf("updateBastionInformations: bastionIpAddress = %s", bastionIpAddress)
		if err != nil || bastionIpAddress == "" {
			log.Debugf("ERROR: bastionIpAddress is EMPTY! (%v)", err)
			continue
		}

		err = addServerKnownHosts(ctx, bastionIpAddress)
		if err != nil {
			log.Debugf("updateBastionInformations: addServerKnownHosts returns %v", err)
			// Skip it
			continue
		}

		currentVMs := 0
		previousVMs := bastionInformation.NumVMs
		for _, server := range allServers {
			if !strings.HasPrefix(strings.ToLower(server.Name), infraID) {
				continue
			}
			currentVMs++
		}
		log.Debugf("updateBastionInformations: currentVMs = %d, NumVMs = %d", currentVMs, bastionInformation.NumVMs)

		// The range operator creates a copy of the array.
		// We need to modify the original array!
		bastionInformations[i].Valid = true
		bastionInformations[i].ClusterName = bastionServer.Name
		bastionInformations[i].InfraID = infraID
		bastionInformations[i].IPAddress = bastionIpAddress
		bastionInformations[i].NumVMs = currentVMs

		log.Debugf("updateBastionInformations: NEW bastionInformation = %+v", bastionInformation)

		if previousVMs == 0 && currentVMs > 0 {
			// First time for this bastion
		}

		if currentVMs == 0 && previousVMs > 0 {
			// Last time for this bastion
		}
	}

	return
}

// getServerSet creates a set of server names from a list of OpenStack servers.
//
// Parameters:
//   - allServers: List of all servers from OpenStack
//
// Returns:
//   - Set of server names that match cluster node types (bootstrap, master, worker)
//
// The function filters servers to include only those whose names contain
// "bootstrap", "master", or "worker", and that have valid IP addresses.
// Servers without IP addresses are excluded from the set.
func getServerSet(allServers []servers.Server) sets.Set[string] {
	var (
		knownServers = sets.Set[string]{}
		server       servers.Server
	)

	for _, server = range allServers {
		if !slices.ContainsFunc(
			[]string{"bootstrap", "master", "worker"},
			func(s string) bool {
				return strings.Contains(server.Name, s)
			}) {
			continue
		}

		_, ipAddress, err := findIpAddress(server)
		if err != nil || ipAddress == "" {
			continue
		}

		knownServers.Insert(server.Name)
	}

	return knownServers
}

// findIpAddress extracts the IP address from a server's network information.
//
// Parameters:
//   - server: OpenStack server object
//
// Returns:
//   - networkName: Name of the network (first return value, often unused)
//   - ipAddress: IP address of the server
//   - error: Any error encountered extracting the IP address
//
// The function searches through the server's addresses to find a valid IP address.
// It looks for addresses in the server's network configuration and returns the
// first valid IP address found.
func findIpAddress(server servers.Server) (string, string, error) {
	var (
		subnetContents []interface {}
		mapSubNetwork  map[string]interface{}
		ok             bool
		ipAddress      string
	)

	for key := range server.Addresses {
		// Addresses:map[vlan1337:[map[OS-EXT-IPS-MAC:mac_addr:fa:16:3e:b1:33:03 OS-EXT-IPS:type:fixed addr:10.20.182.169 version:4]]]
		subnetContents, ok = server.Addresses[key].([]interface {})
		if !ok {
			return "", "", fmt.Errorf("Error: did not convert to [] of interface {}: %v", server.Addresses)
		}

		for _, subnetValue := range subnetContents {
			mapSubNetwork, ok = subnetValue.(map[string]interface{})
			if !ok {
				return "", "", fmt.Errorf("Error: did not convert to map[string] of interface {}: %v", server.Addresses)
			}

			macAddrI, ok := mapSubNetwork["OS-EXT-IPS-MAC:mac_addr"]
			if !ok {
				return "", "", fmt.Errorf("Error: mapSubNetwork did not contain \"OS-EXT-IPS-MAC:mac_addr\": %v", mapSubNetwork)
			}
			macAddr, ok := macAddrI.(string)
			if !ok {
				return "", "", fmt.Errorf("Error: macAddrI was not a string: %v", macAddrI)
			}

			ipAddressI, ok := mapSubNetwork["addr"]
			if !ok {
				return "", "", fmt.Errorf("Error: mapSubNetwork did not contain \"addr\": %v", mapSubNetwork)
			}
			ipAddress, ok = ipAddressI.(string)
			if !ok {
				return "", "", fmt.Errorf("Error: ipAddressI was not a string: %v", ipAddressI)
			}

			return macAddr, ipAddress, nil
		}
	}

	return "", "", nil
}

// dhcpdConf generates a DHCP server configuration file for cluster nodes.
//
// Parameters:
//   - ctx: Context for the operation
//   - filename: Path where the configuration file will be written
//   - cloud: Cloud name to query for servers
//   - domainName: DNS domain name for the cluster
//   - dhcpInterface: Network interface for DHCP server
//   - dhcpSubnet: Subnet for DHCP address pool
//   - dhcpNetmask: Netmask for the subnet
//   - dhcpRouter: Default gateway/router address
//   - dhcpDnsServers: DNS server addresses (comma-separated)
//   - dhcpServerId: DHCP server identifier
//
// Returns:
//   - error: Any error encountered generating the configuration
//
// The function retrieves all servers from OpenStack, filters for cluster nodes
// (bootstrap, master, worker), and generates a dhcpd.conf file with static
// host entries mapping MAC addresses to IP addresses and hostnames.
func dhcpdConf(ctx context.Context, filename string, cloud string, domainName string, dhcpInterface string, dhcpSubnet string, dhcpNetmask string, dhcpRouter string, dhcpDnsServers string, dhcpServerId string) error {
	var (
		allServers []servers.Server
		server     servers.Server
		file       *os.File
		err        error
	)

	allServers, err = getAllServers(ctx, cloud)
	if err != nil {
		return err
	}

	fmt.Printf("Writing %s\n\n", filename)

	err = os.Remove(filename)
	if err != nil {
		if !strings.HasSuffix(err.Error(), "no such file or directory") {
			return err
		}
	}

	file, err = os.OpenFile(filename, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return err
	}

	defer file.Close()

	fmt.Fprintf(file, "#\n")
	fmt.Fprintf(file, "# DHCP Server Configuration file.\n")
	fmt.Fprintf(file, "#   see /usr/share/doc/dhcp-server/dhcpd.conf.example\n")
	fmt.Fprintf(file, "#   see dhcpd.conf(5) man page\n")
	fmt.Fprintf(file, "#\n")
	fmt.Fprintf(file, "\n")
	fmt.Fprintf(file, "# Persist interface configuration when dhcpcd exits.\n")
	fmt.Fprintf(file, "persistent;\n")
	fmt.Fprintf(file, "\n")
	fmt.Fprintf(file, "default-lease-time 2678400;\n")
	fmt.Fprintf(file, "max-lease-time 2678400;\n")
	fmt.Fprintf(file, "\n")
	fmt.Fprintf(file, "subnet %s netmask %s {\n", dhcpSubnet, dhcpNetmask)
	fmt.Fprintf(file, "   interface %s;\n", dhcpInterface)
	fmt.Fprintf(file, "   option routers %s;\n", dhcpRouter)
	fmt.Fprintf(file, "   option subnet-mask %s;\n", dhcpSubnet)
	fmt.Fprintf(file, "   option domain-name-servers %s;\n", dhcpDnsServers)
	fmt.Fprintf(file, "   option domain-name \"%s\";\n", domainName)
	fmt.Fprintf(file, "   option dhcp-server-identifier %s;\n", dhcpServerId)
	fmt.Fprintf(file, "   ignore unknown-clients;\n")
	fmt.Fprintf(file, "#  update-static-leases true;\n")
	fmt.Fprintf(file, "}\n")
	fmt.Fprintf(file, "\n")

	for _, server = range allServers {
		macAddr, ipAddress, err := findIpAddress(server)
		if err == nil && macAddr != "" && ipAddress != "" {
			fmt.Fprintf(file, "host %s {\n", server.Name)
			fmt.Fprintf(file, "    hardware ethernet    %s;\n", macAddr)
			fmt.Fprintf(file, "    fixed-address        %s;\n", ipAddress)
			fmt.Fprintf(file, "    max-lease-time       84600;\n")
			fmt.Fprintf(file, "    option host-name     \"%s\";\n", server.Name)
			fmt.Fprintf(file, "    ddns-hostname        %s;\n", server.Name)
			fmt.Fprintf(file, "}\n")
			fmt.Fprintf(file, "\n")
		}
	}

	return nil
}

// haproxyCfg generates HAProxy configuration files for each bastion server.
//
// Parameters:
//   - ctx: Context for the operation
//   - cloud: Cloud name to query for servers
//   - bastionInformations: Slice of bastion information for clusters
//
// Returns:
//   - error: Any error encountered generating the configurations
//
// The function creates HAProxy configuration files that set up load balancing for:
//   - Ingress HTTP traffic (port 80) to worker nodes
//   - Ingress HTTPS traffic (port 443) to worker nodes
//   - API traffic (port 6443) to master nodes
//   - Machine config server traffic (port 22623) to master and bootstrap nodes
//
// For each valid bastion with VMs, it generates /tmp/haproxy.cfg with backend
// server entries for all cluster nodes, then copies it to the bastion server
// and restarts the HAProxy service.
func haproxyCfg(ctx context.Context, cloud string, bastionInformations []bastionInformation) error {
	var (
		allServers []servers.Server
		server      servers.Server
		err         error
	)

	allServers, err = getAllServers(ctx, cloud)
	if err != nil {
		return err
	}

	log.Debugf("haproxyCfg: len(bastionInformations) = %d", len(bastionInformations))
	if len(bastionInformations) == 0 {
		fmt.Println("Warning: no bastion servers found!")
		return nil
	}

	for _, bastionInformation := range bastionInformations {
		var (
			file        *os.File
			filename    string
			prefixMatch string
		)

		log.Debugf("haproxyCfg: bastionInformation = %+v", bastionInformation)

		if !bastionInformation.Valid {
			continue
		}
		if bastionInformation.NumVMs == 0 {
			continue
		}

		filename = "/tmp/haproxy.cfg"
		fmt.Printf("Writing %s\n\n", filename)

		err = os.Remove(filename)
		if err != nil {
			if !strings.HasSuffix(err.Error(), "no such file or directory") {
				return err
			}
		}

		file, err = os.OpenFile(filename, os.O_CREATE|os.O_RDWR, 0644)
		if err != nil {
			return err
		}
		defer file.Close()

		fmt.Fprintf(file, "#\n")
		fmt.Fprintf(file, "global\n")
		fmt.Fprintf(file, "daemon\n")
		fmt.Fprintf(file, "\n")
		fmt.Fprintf(file, "defaults\n")
		fmt.Fprintf(file, "log global\n")
		fmt.Fprintf(file, "timeout connect 5s\n")
		fmt.Fprintf(file, "timeout client 50s\n")
		fmt.Fprintf(file, "timeout server 50s\n")
		fmt.Fprintf(file, "\n")
		fmt.Fprintf(file, "listen stats # Define a listen section called \"stats\"\n")
		fmt.Fprintf(file, "  bind :9000 # Listen on localhost:9000\n")
		fmt.Fprintf(file, "  mode http\n")
		fmt.Fprintf(file, "  stats enable  # Enable stats page\n")
		fmt.Fprintf(file, "  stats hide-version  # Hide HAProxy version\n")
		fmt.Fprintf(file, "  stats realm Haproxy\\ Statistics  # Title text for popup window\n")
		fmt.Fprintf(file, "  stats uri /haproxy_stats  # Stats URI\n")
		fmt.Fprintf(file, "  stats auth Username:Password  # Authentication credentials\n")
		fmt.Fprintf(file, "\n")

		// listen ingress-http
		fmt.Fprintf(file, "listen ingress-http\n")
		fmt.Fprintf(file, "bind *:80\n")
		fmt.Fprintf(file, "mode tcp\n")
		prefixMatch = fmt.Sprintf("%s-worker-", bastionInformation.InfraID)
		for _, server = range allServers {
			if !strings.HasPrefix(strings.ToLower(server.Name), prefixMatch) {
				continue
			}

			macAddr, ipAddress, err := findIpAddress(server)
			if err == nil && macAddr != "" && ipAddress != "" {
				fmt.Fprintf(file, "server %s %s:80 check\n", server.Name, ipAddress)
			}
		}
		fmt.Fprintf(file, "\n")

		// listen ingress-https
		fmt.Fprintf(file, "listen ingress-https\n")
		fmt.Fprintf(file, "bind *:443\n")
		fmt.Fprintf(file, "mode tcp\n")
		prefixMatch = fmt.Sprintf("%s-worker-", bastionInformation.InfraID)
		for _, server = range allServers {
			if !strings.HasPrefix(strings.ToLower(server.Name), prefixMatch) {
				continue
			}

			macAddr, ipAddress, err := findIpAddress(server)
			if err == nil && macAddr != "" && ipAddress != "" {
				fmt.Fprintf(file, "server %s %s:443 check\n", server.Name, ipAddress)
			}
		}
		fmt.Fprintf(file, "\n")

		// listen api
		fmt.Fprintf(file, "listen api\n")
		fmt.Fprintf(file, "bind *:6443\n")
		fmt.Fprintf(file, "mode tcp\n")
		for _, server = range allServers {
			if !strings.HasPrefix(strings.ToLower(server.Name), bastionInformation.InfraID) {
				continue
			}
			if !(strings.Contains(strings.ToLower(server.Name), "bootstrap") || strings.Contains(strings.ToLower(server.Name), "master")) {
				continue
			}

			macAddr, ipAddress, err := findIpAddress(server)
			if err == nil && macAddr != "" && ipAddress != "" {
				fmt.Fprintf(file, "server %s %s:6443 check\n", server.Name, ipAddress)
			}
		}
		fmt.Fprintf(file, "\n")

		// listen machine-config-server
		fmt.Fprintf(file, "listen machine-config-server\n")
		fmt.Fprintf(file, "bind *:22623\n")
		fmt.Fprintf(file, "mode tcp\n")
		for _, server = range allServers {
			if !strings.HasPrefix(strings.ToLower(server.Name), bastionInformation.InfraID) {
				continue
			}
			if !(strings.Contains(strings.ToLower(server.Name), "bootstrap") || strings.Contains(strings.ToLower(server.Name), "master")) {
				continue
			}

			macAddr, ipAddress, err := findIpAddress(server)
			if err == nil && macAddr != "" && ipAddress != "" {
				fmt.Fprintf(file, "server %s %s:22623 check\n", server.Name, ipAddress)
			}
		}

		err = runSplitCommand([]string{
			"scp",
			"-i",
			bastionInformation.InstallerRsa,
			filename,
			fmt.Sprintf("%s@%s:/etc/haproxy/haproxy.cfg", bastionInformation.Username, bastionInformation.IPAddress),
		})
		if err != nil {
			return err
		}

		err = runSplitCommand([]string{
			"ssh",
			"-i",
			bastionInformation.InstallerRsa,
			fmt.Sprintf("%s@%s", bastionInformation.Username, bastionInformation.IPAddress),
			"sudo",
			"systemctl",
			"restart",
			"haproxy.service",
		})
		if err != nil {
			return err
		}
	}

	return nil
}

// getClusterName extracts the cluster name from a list of servers.
//
// Parameters:
//   - allServers: List of all servers from OpenStack
//
// Returns:
//   - clusterName: The extracted cluster name, or empty string if not found
//
// The function searches for servers with "-bootstrap" or "-master" in their names
// and extracts the cluster name by removing the infrastructure ID suffix.
// For example, from "mycluster-abc123-master-0", it extracts "mycluster".
func getClusterName(allServers []servers.Server) (clusterName string) {
	var (
		server servers.Server
	)

	for _, server = range allServers {
		idx := strings.Index(server.Name, "-bootstrap")
		if idx < 0 {
			idx = strings.Index(server.Name, "-master")
		}
		if idx < 0 {
			continue
		}

		clusterName = server.Name[0:idx-1]

		idx = strings.LastIndex(clusterName, "-")
		if idx < 0 {
			continue
		}

		clusterName = clusterName[0:idx]
		break
	}

	return
}

var (
	firstDnsRun = true
)

// dnsRecords manages DNS records for cluster nodes in IBM Cloud Internet Services.
//
// Parameters:
//   - ctx: Context for the operation
//   - cloud: Cloud name to query for servers
//   - apiKey: IBM Cloud API key for DNS service authentication
//   - domainName: Base domain name for DNS records
//   - bastionInformations: Slice of bastion information for clusters
//   - knownServers: Set of previously known server names
//   - addedServerSet: Set of newly added server names
//   - deletedServerSet: Set of deleted server names
//
// Returns:
//   - error: Any error encountered during DNS record management
//
// The function performs the following operations:
//  1. Retrieves IBM Cloud Internet Services (CIS) service ID
//  2. Gets the DNS zone CRN and zone ID for the domain
//  3. Initializes the DNS service API client
//  4. Retrieves all servers from OpenStack
//  5. On first run: Creates DNS records for bastion servers (api, api-int, *.apps)
//  6. Creates DNS records for newly added servers
//  7. Deletes DNS records for removed servers
//
// DNS records created include:
//   - A records for api.<cluster>.<domain> pointing to bastion
//   - A records for api-int.<cluster>.<domain> pointing to bastion
//   - CNAME records for *.apps.<cluster>.<domain> pointing to bastion
//   - A records for each cluster node (bootstrap, master, worker)
func dnsRecords(ctx context.Context, cloud string, apiKey string, domainName string, bastionInformations []bastionInformation, knownServers sets.Set[string], addedServerSet sets.Set[string], deletedServerSet sets.Set[string]) error {
	var (
		dnsService   *dnsrecordsv1.DnsRecordsV1
		cisServiceID string
		crnstr       string
		zoneID       string
		allServers   []servers.Server
		server       servers.Server
		clusterName  string
		err          error
	)

	cisServiceID, _, err = getServiceInfo(ctx, apiKey, "internet-svcs", "")
	if err != nil {
		log.Errorf("getServiceInfo returns %v", err)
		return err
	}
	log.Debugf("dnsRecords: cisServiceID = %s", cisServiceID)

	crnstr, zoneID, err = getDomainCrn(ctx, apiKey, cisServiceID, domainName)
	log.Debugf("dnsRecords: crnstr = %s, zoneID = %s, err = %+v", crnstr, zoneID, err)
	if err != nil {
		log.Errorf("getDomainCrn returns %v", err)
		return err
	}

	dnsService, err = loadDnsServiceAPI(apiKey, crnstr, zoneID)
	if err != nil {
		return err
	}
	log.Debugf("dnsRecords: dnsService = %+v", dnsService)

	allServers, err = getAllServers(ctx, cloud)
	if err != nil {
		return err
	}

	clusterName = getClusterName(allServers)
	log.Debugf("dnsRecords: clusterName = %s", clusterName)
	if clusterName == "" {
		return nil
	}

	if firstDnsRun {
		log.Debugf("dnsRecords: FIRST DNS RUN!")

		firstDnsRun = false

		for _, bastionInformation := range bastionInformations {
			if !bastionInformation.Valid {
				continue
			}

			err = createOrDeletePublicDNSRecord(ctx,
				dnsrecordsv1.CreateDnsRecordOptions_Type_A,
				fmt.Sprintf("api.%s.%s", bastionInformation.ClusterName, domainName),
				bastionInformation.IPAddress,
				true,
				dnsService)
			err = createOrDeletePublicDNSRecord(ctx,
				dnsrecordsv1.CreateDnsRecordOptions_Type_A,
				fmt.Sprintf("api-int.%s.%s", bastionInformation.ClusterName, domainName),
				bastionInformation.IPAddress,
				true,
				dnsService)
			err = createOrDeletePublicDNSRecord(ctx,
				dnsrecordsv1.CreateDnsRecordOptions_Type_Cname,
				fmt.Sprintf("*.apps.%s.%s", bastionInformation.ClusterName, domainName),
				fmt.Sprintf("api.%s.%s", bastionInformation.ClusterName, domainName),
				true,
				dnsService)
		}
	}

	for deletedServer := range deletedServerSet {
		log.Debugf("dnsRecords: deletedServer = %s", deletedServer)

		if slices.ContainsFunc(
			[]string{"bootstrap", "master", "worker"},
			func(s string) bool {
//				log.Debugf("strings.Contains(%s, %s) = %v", deletedServer, s, strings.Contains(deletedServer, s))
				return strings.Contains(deletedServer, s)
			}) {
			err = createOrDeletePublicDNSRecord(ctx,
				dnsrecordsv1.CreateDnsRecordOptions_Type_A,
				fmt.Sprintf("%s.%s", deletedServer, domainName),
				"",
				false,
				dnsService)
		}
	}

	for addedServer := range addedServerSet {
		log.Debugf("dnsRecords: addedServer = %s", addedServer)

		for _, server = range allServers {
			if server.Name != addedServer {
				continue
			}

			_, ipAddress, err := findIpAddress(server)
			if err != nil || ipAddress == "" {
				continue
			}

			if slices.ContainsFunc(
				[]string{"bootstrap", "master", "worker"},
				func(s string) bool {
//					log.Debugf("strings.Contains(%s, %s) = %v", server.Name, s, strings.Contains(server.Name, s))
					return strings.Contains(server.Name, s)
				}) {
				err = createOrDeletePublicDNSRecord(ctx,
					dnsrecordsv1.CreateDnsRecordOptions_Type_A,
					fmt.Sprintf("%s.%s", server.Name, domainName),
					ipAddress,
					true,
					dnsService)
			}
		}
	}

	if len(knownServers) == 0 && !firstDnsRun {
		firstDnsRun = false

		for _, bastionInformation := range bastionInformations {
			if !bastionInformation.Valid {
				continue
			}

			err = createOrDeletePublicDNSRecord(ctx,
				dnsrecordsv1.CreateDnsRecordOptions_Type_A,
				fmt.Sprintf("api.%s.%s", bastionInformation.ClusterName, domainName),
				"",
				false,
				dnsService)
			err = createOrDeletePublicDNSRecord(ctx,
				dnsrecordsv1.CreateDnsRecordOptions_Type_A,
				fmt.Sprintf("api-int.%s.%s", bastionInformation.ClusterName, domainName),
				"",
				false,
				dnsService)
			err = createOrDeletePublicDNSRecord(ctx,
				dnsrecordsv1.CreateDnsRecordOptions_Type_Cname,
				fmt.Sprintf("*.apps.%s.%s", bastionInformation.ClusterName, domainName),
				fmt.Sprintf("api.%s.%s", bastionInformation.ClusterName, domainName),
				false,
				dnsService)
		}
	}

	return nil
}

// loadResourceControllerAPI creates and initializes an IBM Cloud Resource Controller API client.
//
// Parameters:
//   - apiKey: IBM Cloud API key for authentication
//
// Returns:
//   - controllerAPI: Initialized Resource Controller API client
//   - err: Any error encountered during initialization
//
// The function creates a new Resource Controller V2 client with IAM authentication
// for managing IBM Cloud resources and service instances.
func loadResourceControllerAPI(apiKey string) (controllerAPI *resourcecontrollerv2.ResourceControllerV2, err error) {
	controllerAPI, err = resourcecontrollerv2.NewResourceControllerV2(&resourcecontrollerv2.ResourceControllerV2Options{
		Authenticator: &core.IamAuthenticator{
			ApiKey: apiKey,
		},
	})

	return
}

// loadDnsServiceAPI creates and initializes an IBM Cloud DNS Records API client.
//
// Parameters:
//   - apiKey: IBM Cloud API key for authentication
//   - crnstr: Cloud Resource Name (CRN) for the DNS service instance
//   - zoneID: DNS zone identifier
//
// Returns:
//   - service: Initialized DNS Records API client
//   - err: Any error encountered during initialization
//
// The function creates a new DNS Records V1 client with IAM authentication
// for managing DNS records in IBM Cloud Internet Services (CIS).
func loadDnsServiceAPI(apiKey string, crnstr string, zoneID string)(service *dnsrecordsv1.DnsRecordsV1, err error) {
	service, err = dnsrecordsv1.NewDnsRecordsV1(&dnsrecordsv1.DnsRecordsV1Options{
		Authenticator:  &core.IamAuthenticator{
			ApiKey: apiKey,
		},
		Crn:            &crnstr,
		ZoneIdentifier: &zoneID,
	})

	return
}

// getServiceInfo retrieves service ID and service plan ID from IBM Cloud Global Catalog.
//
// Parameters:
//   - ctx: Context for the operation
//   - apiKey: IBM Cloud API key for authentication
//   - service: Name of the service to look up (e.g., "internet-svcs")
//   - servicePlan: Name of the service plan (optional, empty string to skip plan lookup)
//
// Returns:
//   - serviceID: The unique identifier for the service
//   - servicePlanID: The unique identifier for the service plan (empty if servicePlan is empty)
//   - error: Any error encountered during catalog lookup
//
// The function queries the IBM Cloud Global Catalog to find the service by name
// and optionally retrieves the service plan ID. If servicePlan is empty, only
// the service ID is returned.
func getServiceInfo(ctx context.Context, apiKey string, service string, servicePlan string) (string, string, error) {
	var (
		serviceID     string
		servicePlanID string
	)

	gcv1, err := globalcatalogv1.NewGlobalCatalogV1(&globalcatalogv1.GlobalCatalogV1Options{
		Authenticator: &core.IamAuthenticator{
			ApiKey: apiKey,
		},
		URL:           globalcatalogv1.DefaultServiceURL,
	})
	log.Debugf("getServiceInfo: gcv1 = %+v", gcv1)
	if err != nil {
		return "", "", err
	}

	if gcv1 == nil {
		return "", "", fmt.Errorf("unable to get global catalog")
	}

	// TO-DO need to explore paging for catalog list since ListCatalogEntriesOptions does not take start
	include := "*"
	listCatalogEntriesOpt := globalcatalogv1.ListCatalogEntriesOptions{Include: &include, Q: &service}
	catalogEntriesList, _, err := listCatalogEntries(ctx, gcv1, &listCatalogEntriesOpt)
	if err != nil {
		return "", "", err
	}
	if catalogEntriesList != nil {
		for _, catalog := range catalogEntriesList.Resources {
			log.Debugf("getServiceInfo: catalog.Name = %s, catalog.ID = %s", *catalog.Name, *catalog.ID)
			if *catalog.Name == service {
				serviceID = *catalog.ID
			}
		}
	}

	if serviceID == "" {
		return "", "", fmt.Errorf("could not retrieve service id for service %s", service)
	} else if servicePlan == "" {
		return serviceID, "", nil
	}

	kind := "plan"
	getChildOpt := globalcatalogv1.GetChildObjectsOptions{
		ID: &serviceID,
		Kind: &kind,
	}

	var childObjResult *globalcatalogv1.EntrySearchResult

	childObjResult, _, err = GetChildObjects(ctx, gcv1, &getChildOpt)
	if err != nil {
		return "", "", err
	}

	for _, plan := range childObjResult.Resources {
		if *plan.Name == servicePlan {
			servicePlanID = *plan.ID
			return serviceID, servicePlanID, nil
		}
	}

	err = fmt.Errorf("could not retrieve plan id for service name: %s & service plan name: %s", service, servicePlan)

	return "", "", err
}

// getDomainCrn retrieves the Cloud Resource Name (CRN) and zone ID for a DNS domain.
//
// Parameters:
//   - ctx: Context for the operation
//   - apiKey: IBM Cloud API key for authentication
//   - cisServiceID: Cloud Internet Services (CIS) service ID
//   - baseDomain: Base domain name to search for (e.g., "example.com")
//
// Returns:
//   - crnstr: Cloud Resource Name for the CIS instance containing the domain
//   - zoneID: DNS zone identifier for the domain
//   - err: Any error encountered during lookup
//
// The function performs the following steps:
//  1. Creates a Resource Controller API client
//  2. Lists all CIS resource instances
//  3. For each instance, creates a Zones API client
//  4. Lists zones in the instance
//  5. Searches for a zone matching the base domain
//  6. Returns the CRN and zone ID when found
//
// The function supports pagination to handle large numbers of resource instances.
func getDomainCrn(ctx context.Context, apiKey string, cisServiceID string, baseDomain string) (crnstr string, zoneID string, err error) {
	var (
		// https://github.com/IBM/platform-services-go-sdk/blob/main/resourcecontrollerv2/resource_controller_v2.go#L4525-L4534
		resources *resourcecontrollerv2.ResourceInstancesList
		perPage   int64 = 64
		moreData        = true
		zv1       *zonesv1.ZonesV1
		zoneList  *zonesv1.ListZonesResp
	)

	// Instantiate the service with an API key based IAM authenticator
	controllerSvc, err := resourcecontrollerv2.NewResourceControllerV2(&resourcecontrollerv2.ResourceControllerV2Options{
		Authenticator: &core.IamAuthenticator{
			ApiKey: apiKey,
		},
		URL:           resourcecontrollerv2.DefaultServiceURL,
	})
	log.Debugf("getDomainCrn: controllerSvc = %+v", controllerSvc)
	if err != nil {
		err = fmt.Errorf("NewResourceControllerV2 failed with: %v", err)
		return
	}

	listResourceOptions := resourcecontrollerv2.ListResourceInstancesOptions{
		ResourceID: &cisServiceID,
		Limit:      &perPage,
	}
	log.Debugf("getDomainCrn: listResourceOptions = %+v", listResourceOptions)

	for moreData {
		resources, _, err = listResourceInstances(ctx, controllerSvc, &listResourceOptions)
		if err != nil {
			err = fmt.Errorf("ListResourceInstancesWithContext failed with: %v", err)
			return
		}
		log.Debugf("getDomainCrn: RowsCount %v", *resources.RowsCount)

		for _, instance := range resources.Resources {
			log.Debugf("getDomainCrn: instance.Name = %s, instance.CRN = %s", *instance.Name, *instance.CRN)

			zv1, err = zonesv1.NewZonesV1(&zonesv1.ZonesV1Options{
				Authenticator: &core.IamAuthenticator{
					ApiKey: apiKey,
				},
				Crn:           instance.CRN,
			})
			log.Debugf("getDomainCrn: zv1 = %+v", zv1)
			if err != nil {
				err = fmt.Errorf("NewZonesV1 failed with: %v", err)
				return
			}

			zoneList, _, err = listZones(ctx, zv1, &zonesv1.ListZonesOptions{})
			if err != nil {
				err = fmt.Errorf("ListZonesWithContext failed with: %v", err)
				return
			}
			if zoneList == nil {
				err = fmt.Errorf("zoneList is nil")
				return
			}

			for _, zone := range zoneList.Result {
				log.Debugf("getDomainCrn: zone.Name = %s, zone.ID = %s", *zone.Name, *zone.ID)
				if *zone.Name == baseDomain {
					crnstr = *instance.CRN
					zoneID  = *zone.ID
					err = nil
					return
				}
			}
		}

		if resources.NextURL != nil {
			var start *string

			start, err = resources.GetNextStart()
			if err != nil {
				log.Debugf("getDomainCrn: err = %v", err)
				err = fmt.Errorf("failed to GetNextStart: %v", err)
				return
			}
			if start != nil {
				log.Debugf("getDomainCrn: start = %v", *start)
				listResourceOptions.SetStart(*start)
			}
		} else {
			log.Debugf("getDomainCrn: NextURL = nil")
			moreData = false
		}
	}

	err = fmt.Errorf("failed to find %s", baseDomain)
	return
}

// findDNSRecord searches for a DNS record by name in IBM Cloud Internet Services.
//
// Parameters:
//   - ctx: Context for the operation
//   - dnsService: Initialized DNS Records API client
//   - cname: DNS record name to search for (e.g., "api.cluster.example.com")
//
// Returns:
//   - foundID: The unique identifier of the DNS record if found
//   - content: The content/value of the DNS record (IP address or target)
//   - err: Any error encountered during the search
//
// The function queries the DNS service for records matching the specified name
// and returns the ID and content of the first matching record. If no record
// is found, empty strings are returned for foundID and content.
func findDNSRecord(ctx context.Context, dnsService *dnsrecordsv1.DnsRecordsV1, cname string)(foundID string, content string, err error) {
	var (
		listOptions *dnsrecordsv1.ListAllDnsRecordsOptions
		records     *dnsrecordsv1.ListDnsrecordsResp
		response    *core.DetailedResponse
	)

	log.Debugf("findDNSRecord: cname = %s", cname)

	listOptions = dnsService.NewListAllDnsRecordsOptions()
	listOptions.SetName(cname)

	records, response, err = listAllDnsRecords(ctx, dnsService, listOptions)
	if err != nil {
		err = fmt.Errorf("ListAllDnsRecordsWithContext response = %+v, err = %+v", response, err)
		return
	}

	log.Debugf("findDNSRecord: len(records.Result) = %d", len(records.Result))
	for _, record := range records.Result {
		log.Debugf("findDNSRecord: record.Name = %s, record.ID = %s", *record.Name, *record.ID)

		if cname == *record.Name {
			foundID = *record.ID
			content = *record.Content
			return
		}
	}

	return
}

// createOrDeletePublicDNSRecord creates or deletes a DNS record in IBM Cloud Internet Services.
//
// Parameters:
//   - ctx: Context for the operation
//   - dnsRecordType: Type of DNS record (e.g., "A", "CNAME")
//   - hostname: DNS record name/hostname (e.g., "api.cluster.example.com")
//   - cname: DNS record content/value (IP address for A records, target for CNAME records)
//   - shouldCreate: true to create/update the record, false to delete it
//   - dnsService: Initialized DNS Records API client
//
// Returns:
//   - error: Any error encountered during the operation
//
// The function performs the following logic:
//  1. Searches for an existing DNS record with the specified hostname
//  2. If the record exists:
//     - For delete operations: Deletes the record
//     - For create operations with different content: Deletes old record and creates new one
//     - For create operations with same content: Returns success (no-op)
//  3. If the record doesn't exist and shouldCreate is true: Creates the record
//  4. If the record doesn't exist and shouldCreate is false: Returns success (no-op)
//
// The function uses a TTL of 60 seconds for all created DNS records.
func createOrDeletePublicDNSRecord(ctx context.Context, dnsRecordType string, hostname string, cname string, shouldCreate bool, dnsService *dnsrecordsv1.DnsRecordsV1) error {
	var (
		foundRecordID string
		content       string
		deleteOptions *dnsrecordsv1.DeleteDnsRecordOptions
		createOptions *dnsrecordsv1.CreateDnsRecordOptions
		err           error
	)

	log.Debugf("createOrDeletePublicDNSRecord: dnsRecordType = %s, hostname = %s, cname = %s, shouldCreate = %v", dnsRecordType, hostname, cname, shouldCreate)

	foundRecordID, content, err = findDNSRecord(ctx, dnsService, hostname)
	if err != nil {
		return err
	}
	log.Debugf("createOrDeletePublicDNSRecord: foundRecordID = %s, content = %s", foundRecordID, content)

	// Does it already exist?
	if foundRecordID != "" {
		log.Debugf("createOrDeletePublicDNSRecord: !shouldCreate = %v, (content != cname && shouldCreate) = %v", !shouldCreate, (content != cname && shouldCreate))

		// If we should delete OR we are creating and the contents are different?
		if (!shouldCreate || (content != cname && shouldCreate)) {
			deleteOptions = dnsService.NewDeleteDnsRecordOptions(foundRecordID)
			log.Debugf("createOrDeletePublicDNSRecord: deleteOptions = %+v", deleteOptions)

			result, response, err := deleteDnsRecord(ctx, dnsService, deleteOptions)
			if err != nil {
				return fmt.Errorf("DeleteDnsRecordWithContext response = %+v, err = %+v", response, err)
			}

			if !*result.Success {
				for _, aerrmsg := range result.Errors {
					log.Debugf("createOrDeletePublicDNSRecord: aerrmsg = %+v", aerrmsg)
					// @TODO
				}
				return fmt.Errorf("DeleteDnsRecordWithContext result.Success is false")
			}
		}

		// If we shoud create AND the content is the same, then we are done.
		if shouldCreate && (content == cname) {
			log.Debugf("createOrDeletePublicDNSRecord: content already exists!")
			return nil
		}
	}

	if !shouldCreate {
		return nil
	}

	createOptions = dnsService.NewCreateDnsRecordOptions()
	createOptions.SetType(dnsRecordType)
	createOptions.SetName(hostname)
	createOptions.SetContent(cname)
	createOptions.SetTTL(60)
	log.Debugf("createOrDeletePublicDNSRecord: createOptions = %+v", createOptions)

	result, response, err := createDnsRecord(ctx, dnsService, createOptions)
	if err != nil {
		log.Errorf("dnsRecordService.CreateDnsRecordWithContext returns %v", err)
		return err
	}
	log.Debugf("createOrDeletePublicDNSRecord: Result.ID = %v, RawResult = %v", *result.Result.ID, response.RawResult)

	return nil
}

// listenForCommands starts a TCP server that listens for incoming command connections.
//
// Parameters:
//   - cloud: The cloud name to pass to connection handlers
//
// Returns:
//   - error: Any error encountered starting the listener or accepting connections
//
// The function listens on the configured port (listenPort constant) and spawns
// a new goroutine to handle each incoming connection. It runs indefinitely until
// an error occurs.
//
// Supported commands include:
//   - check-alive: Health check command
//   - create-metadata: Create cluster metadata
//   - delete-metadata: Delete cluster metadata
//   - create-bastion: Create bastion server
func listenForCommands(cloud string) error {
	log.Debugf("listenForCommands")

	// Listen for incoming connections on configured port
	ln, err := net.Listen("tcp", listenPort)
	if err != nil {
		return fmt.Errorf("failed to start listener on %s: %w", listenPort, err)
	}

	// Accept incoming connections and handle them
	for {
		conn, err := ln.Accept()
		if err != nil {
			return fmt.Errorf("failed to accept connection: %w", err)
		}

		// Handle the connection in a new goroutine
		go handleConnection(conn, cloud)
	}
}

// handleConnection processes a single client connection and dispatches commands.
//
// Parameters:
//   - conn: Network connection to the client
//   - cloud: The cloud name for command execution
//
// Returns:
//   - error: Any error encountered reading or processing commands
//
// The function reads JSON-formatted commands from the connection, parses the
// command header to determine the command type, and dispatches to the appropriate
// handler function. It continues reading commands until the connection is closed
// or an error occurs.
//
// Command format: JSON object with "command" field indicating the operation type.
func handleConnection(conn net.Conn, cloud string) error {
	var (
		data      string
		cmdHeader CommandHeader
		errChan   chan error
		result    error
		err       error
	)

	// Close the connection when we're done
	defer conn.Close()

	reader := bufio.NewReader(conn)

	for {
		data, err = reader.ReadString('\n')
		if err != nil {
			log.Debugf("handleConnection: reader.ReadString() returns %v", err)
			return err
		}

		err = json.Unmarshal([]byte(data), &cmdHeader)
		if err != nil {
			log.Debugf("handleConnection: Unmarshal() returns %v", err)
			return err
		}
		log.Debugf("handleConnection: cmdHeader = %+v", cmdHeader)

		errChan = make(chan error)

		switch cmdHeader.Command {
		case "check-alive":
			var (
				cmd            CommandIsAlive
				marshalledData []byte
			)

			go handleCheckAlive(data, errChan)
			result = <-errChan
			log.Debugf("handleConnection: result from handleCheckAlive is %v", result)

			cmd.Command = "is-alive"
			if result != nil {
				cmd.Result = result.Error()
			}

			marshalledData, err = json.Marshal(cmd)
			if err != nil {
				log.Debugf("handleConnection: json.Marshal returns %v", err)
				return err
			}

			err = sendByteArray(conn, marshalledData)
			if err != nil {
				return err
			}

		case "create-metadata":
			go handleCreateMetadata(data, true, errChan)
			result = <-errChan
			log.Debugf("handleConnection: result from handleCreateMetadata is %v", result)

		case "delete-metadata":
			go handleCreateMetadata(data, false, errChan)
			result = <-errChan
			log.Debugf("handleConnection: result from handleCreateMetadata is %v", result)

		case "create-bastion":
			var (
				cmd            CommandBastionCreated
				marshalledData []byte
			)

			go handleCreateBastion(data, cloud, errChan)
			result = <-errChan
			log.Debugf("handleConnection: result from handleCreateBastion is %v", result)

			cmd.Command = "bastion-created"
			if result != nil {
				cmd.Result = result.Error()
			}
			log.Debugf("handleConnection: cmd = %+v", cmd)

			marshalledData, err = json.Marshal(cmd)
			if err != nil {
				log.Debugf("handleConnection: json.Marshal returns %v", err)
				return err
			}
			log.Debugf("handleConnection: marshalledData = %+v", marshalledData)

			err = sendByteArray(conn, marshalledData)
			if err != nil {
				return err
			}
		default:
			log.Debugf("handleConnection: ERROR received unknown command %s", cmdHeader.Command)
			return fmt.Errorf("handleConnection received unknown command %s", cmdHeader.Command)
		}
	}

//	if err := scanner.Err(); err != nil {
//		log.Debugf("handleConnection: scanner.Err return %v", err)
//	}
//	return err
}

// handleCheckAlive processes a check-alive command from a client connection.
//
// Parameters:
//   - data: JSON-formatted command data
//   - errChan: Channel to send the result error (or nil for success)
//
// The function unmarshals the check-alive command and sends the result to the
// error channel. This is a simple health check command that always succeeds
// if the JSON can be parsed.
func handleCheckAlive(data string, errChan chan error) {
	var (
		cmd            CommandCheckAlive
		err            error
	)

	// Print the incoming data
	log.Debugf("handleCheckAlive: Received: %s", data)

	err = json.Unmarshal([]byte(data), &cmd)
	if err != nil {
		log.Debugf("handleCheckAlive: Unmarshal() returns %v", err)
		errChan <- err
		return
	}

	errChan <- err
	return
}

// handleCreateMetadata processes a create or delete metadata command.
//
// Parameters:
//   - data: JSON-formatted command data containing cluster metadata
//   - shouldCreate: true to create metadata, false to delete it
//   - errChan: Channel to send the result error (or nil for success)
//
// For create operations, the function:
//  1. Unmarshals the command data
//  2. Creates a directory named after the infrastructure ID
//  3. Writes metadata.json file in that directory
//
// For delete operations, the function:
//  1. Removes the metadata.json file
//  2. Removes the infrastructure ID directory
//
// The result (error or nil) is sent to the error channel.
func handleCreateMetadata(data string, shouldCreate bool, errChan chan error) {
	var (
		cmd            CommandSendMetadata
		marshalledData []byte
		err            error
	)

	// Print the incoming data
	log.Debugf("handleCreateMetadata: Received: %s", data)
	log.Debugf("handleCreateMetadata: shouldCreate = %v", shouldCreate)

	err = json.Unmarshal([]byte(data), &cmd)
	if err != nil {
		log.Debugf("handleCreateMetadata: Unmarshal() returns %v", err)
		errChan <- err
		return
	}
	log.Debugf("handleCreateMetadata: cmd.metadata = %+v", cmd.Metadata)
	log.Debugf("handleCreateMetadata: cmd.metadata.ClusterName = %+v", cmd.Metadata.ClusterName)
	log.Debugf("handleCreateMetadata: cmd.metadata.InfraID = %+v", cmd.Metadata.InfraID)

	marshalledData, err = json.Marshal(cmd.Metadata)
	if err != nil {
		log.Debugf("handleCreateMetadata: json.Marshal() returns %v", err)
		errChan <- err
		return
	}

	if shouldCreate {
		// Create the directory to save the metadata file in
		err = os.MkdirAll(cmd.Metadata.InfraID, os.ModePerm)
		if err != nil {
			log.Debugf("handleCreateMetadata: os.MkdirAll() returns %v", err)
			errChan <- err
			return
		}

		err = os.WriteFile(fmt.Sprintf("%s/metadata.json", cmd.Metadata.InfraID), marshalledData, 0644)
		if err != nil {
			log.Debugf("handleCreateMetadata: os.MkdirAll() returns %v", err)
			errChan <- err
			return
		}
	} else {
		err = os.Remove(fmt.Sprintf("%s/metadata.json", cmd.Metadata.InfraID))
		if err != nil {
			log.Debugf("handleCreateMetadata: os.Remove(%s/metadata.json) returns %v", cmd.Metadata.InfraID, err)
			errChan <- err
			return
		}

		err = os.Remove(cmd.Metadata.InfraID)
		if err != nil {
			log.Debugf("handleCreateMetadata: os.Remove(%s) returns %v", cmd.Metadata.InfraID, err)
			errChan <- err
			return
		}
	}

	errChan <- err
	return
}

// handleCreateBastion processes a create-bastion command to set up a bastion server.
//
// Parameters:
//   - data: JSON-formatted command data containing bastion configuration
//   - cloud: Cloud name for the operation
//   - errChan: Channel to send the result error (or nil for success)
//
// The function performs the following steps:
//  1. Unmarshals the command data to extract server name and domain name
//  2. Creates a context with 10-minute timeout (using bastionContextTimeout constant)
//  3. Calls setupBastionServer to configure the bastion with HAProxy enabled
//  4. Sends the result to the error channel
//
// The bastion server is configured with HAProxy enabled by default (hardcoded to true).
// Note: There's a known limitation that enableHAProxy should be part of the command structure.
func handleCreateBastion(data string, cloud string, errChan chan error) {
	var (
		cmd    CommandCreateBastion
		ctx    context.Context
		cancel context.CancelFunc
		err    error
	)

	// Print the incoming data
	log.Debugf("handleCreateBastion: Received: %s", data)

	err = json.Unmarshal([]byte(data), &cmd)
	if err != nil {
		log.Debugf("handleCreateBastion: Unmarshal() returns %v", err)
		errChan <- err
		return
	}
	log.Debugf("handleCreateBastion: cmd.Command    = %s", cmd.Command)
	log.Debugf("handleCreateBastion: cmd.CloudName  = %s", cmd.CloudName)
	log.Debugf("handleCreateBastion: cmd.ServerName = %s", cmd.ServerName)
	log.Debugf("handleCreateBastion: cmd.DomainName = %s", cmd.DomainName)

	ctx, cancel = context.WithTimeout(context.TODO(), bastionContextTimeout)
	defer cancel()

	// @HACK need to add enableHAProxy to the command structure
	err = setupBastionServer(ctx, true, cloud, cmd.ServerName, cmd.DomainName, bastionRsa)
	log.Debugf("handleCreateBastion: setupBastionServer returns %v", err)
	errChan <- err
}
