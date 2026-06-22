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
//   - statsUser: HAProxy stats username (leave empty to disable stats)
//   - statsPassword: HAProxy stats password
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
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"syscall"
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
	usageWatchInstallationCloud            = "The cloud name to use in clouds.yaml (can be specified multiple times)"
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
	watchSleepDuration       = 30 * time.Second
	watchIterationTimeout    = 5 * time.Minute
	bastionContextTimeout    = 10 * time.Minute

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

// WatchInstallationConfig holds all configuration parameters for the watch-installation command.
// It consolidates all command-line flags and derived configuration into a single structure
// for easier management and testing.
type WatchInstallationConfig struct {
	// Cloud configuration
	Clouds []string // List of cloud names to use from clouds.yaml

	// DNS configuration
	DomainName string // DNS domain name for the cluster

	// Bastion configuration
	BastionMetadataDir string // Root directory where OpenShift cluster installs are located
	BastionUsername    string // SSH username for bastion VM
	BastionRsa         string // Path to RSA key file for bastion VM

	// DHCP configuration
	EnableDhcpd    bool   // Whether to enable the DHCP server
	DhcpInterface  string // Network interface name for DHCP server
	DhcpSubnet     string // Subnet for DHCP requests
	DhcpNetmask    string // Netmask for DHCP requests
	DhcpRouter     string // Router IP address for DHCP requests
	DhcpDnsServers string // Comma-separated list of DNS servers for DHCP requests
	DhcpServerId   string // DHCP server identifier (IP address)

	// HAProxy configuration
	StatsUser     string // HAProxy statistics username (empty to disable stats)
	StatsPassword string // HAProxy statistics password

	// Operational configuration
	ShouldDebug bool // Whether to enable debug output

	// Runtime configuration (not from flags)
	APIKey string // IBM Cloud API key from environment
}

// Validate performs comprehensive validation of all configuration parameters.
// It checks required fields, validates formats, and ensures consistency between related fields.
//
// Returns:
//   - error: An error describing the first validation failure, or nil if all validations pass
func (c *WatchInstallationConfig) Validate() error {
	// Validate cloud names
	if len(c.Clouds) == 0 {
		return fmt.Errorf("at least one cloud name must be specified")
	}
	for i, cloud := range c.Clouds {
		if cloud == "" {
			return fmt.Errorf("cloud name at index %d is empty", i)
		}
		if err := validateCloudName(cloud); err != nil {
			return fmt.Errorf("invalid cloud name at index %d: %w", i, err)
		}
	}

	// Validate required string fields
	if c.DomainName == "" {
		return fmt.Errorf("domain name is required")
	}
	if c.BastionMetadataDir == "" {
		return fmt.Errorf("bastion metadata directory is required")
	}
	if c.BastionUsername == "" {
		return fmt.Errorf("bastion username is required")
	}
	if c.BastionRsa == "" {
		return fmt.Errorf("bastion RSA key path is required")
	}

	// Validate domain name format
	if err := validateDomainName(c.DomainName); err != nil {
		return fmt.Errorf("invalid domain name: %w", err)
	}

	// Validate DHCP configuration if enabled
	if c.EnableDhcpd {
		if c.DhcpInterface == "" {
			return fmt.Errorf("DHCP interface is required when DHCP is enabled")
		}
		if c.DhcpSubnet == "" {
			return fmt.Errorf("DHCP subnet is required when DHCP is enabled")
		}
		if c.DhcpNetmask == "" {
			return fmt.Errorf("DHCP netmask is required when DHCP is enabled")
		}
		if c.DhcpRouter == "" {
			return fmt.Errorf("DHCP router is required when DHCP is enabled")
		}
		if c.DhcpDnsServers == "" {
			return fmt.Errorf("DHCP DNS servers are required when DHCP is enabled")
		}
		if c.DhcpServerId == "" {
			return fmt.Errorf("DHCP server ID is required when DHCP is enabled")
		}

		// Validate DHCP parameter formats
		if err := validateInterfaceName(c.DhcpInterface); err != nil {
			return fmt.Errorf("invalid DHCP interface: %w", err)
		}
		if err := validateIPAddress(c.DhcpSubnet); err != nil {
			return fmt.Errorf("invalid DHCP subnet: %w", err)
		}
		if err := validateNetmask(c.DhcpNetmask); err != nil {
			return fmt.Errorf("invalid DHCP netmask: %w", err)
		}
		if err := validateIPAddress(c.DhcpRouter); err != nil {
			return fmt.Errorf("invalid DHCP router: %w", err)
		}
		if err := validateDNSServerList(c.DhcpDnsServers); err != nil {
			return fmt.Errorf("invalid DHCP DNS servers: %w", err)
		}
		if err := validateIPAddress(c.DhcpServerId); err != nil {
			return fmt.Errorf("invalid DHCP server ID: %w", err)
		}
	}

	// Validate HAProxy stats credentials
	if err := validateHAProxyCredentials(c.StatsUser, c.StatsPassword); err != nil {
		return fmt.Errorf("invalid HAProxy stats credentials: %w", err)
	}

	return nil
}

// validateIPAddress validates that a string is a valid IPv4 address.
//
// Parameters:
//   - ip: The IP address string to validate
//
// Returns:
//   - error: An error if the IP address is invalid, nil otherwise
func validateIPAddress(ip string) error {
	if net.ParseIP(ip) == nil {
		return fmt.Errorf("invalid IP address: %s", ip)
	}
	return nil
}

// validateNetmask validates that a string is a valid IPv4 netmask.
//
// A valid netmask must be a valid IP address and must have all 1s followed by all 0s
// in binary representation (e.g., 255.255.255.0 is valid, 255.255.0.255 is not).
//
// Parameters:
//   - netmask: The netmask string to validate
//
// Returns:
//   - error: An error if the netmask is invalid, nil otherwise
func validateNetmask(netmask string) error {
	ip := net.ParseIP(netmask)
	if ip == nil {
		return fmt.Errorf("invalid netmask: %s", netmask)
	}

	// Convert to 4-byte representation
	ip4 := ip.To4()
	if ip4 == nil {
		return fmt.Errorf("netmask must be IPv4: %s", netmask)
	}

	// Verify it's a valid netmask (all 1s followed by all 0s in binary)
	// Convert to uint32 and verify
	maskUint := uint32(ip4[0])<<24 | uint32(ip4[1])<<16 | uint32(ip4[2])<<8 | uint32(ip4[3])

	// A valid netmask has all 1s on the left, all 0s on the right
	// Invert it, add 1, should be a power of 2 (or 0 for all 1s mask)
	inverted := ^maskUint
	if inverted != 0 && (inverted&(inverted+1)) != 0 {
		return fmt.Errorf("netmask has non-contiguous bits: %s", netmask)
	}

	return nil
}

// validateDomainName validates that a string is a valid DNS domain name.
//
// The validation follows RFC 1035 rules:
//   - Maximum length of 253 characters
//   - Labels separated by dots
//   - Each label: 1-63 characters, alphanumeric and hyphens
//   - Labels cannot start or end with hyphen
//
// Parameters:
//   - domain: The domain name string to validate
//
// Returns:
//   - error: An error if the domain name is invalid, nil otherwise
func validateDomainName(domain string) error {
	if len(domain) == 0 {
		return fmt.Errorf("domain name cannot be empty")
	}

	if len(domain) > 253 {
		return fmt.Errorf("domain name too long (max 253 characters): %s", domain)
	}

	// RFC 1035 domain name validation
	// Each label must be 1-63 characters, alphanumeric and hyphens
	// Cannot start or end with hyphen
	domainRegex := regexp.MustCompile(`^([a-zA-Z0-9]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?\.)*[a-zA-Z0-9]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?$`)
	if !domainRegex.MatchString(domain) {
		return fmt.Errorf("invalid domain name format: %s", domain)
	}

	return nil
}

// validateDNSServerList validates a comma-separated list of DNS server IP addresses.
//
// Parameters:
//   - servers: Comma-separated list of DNS server IP addresses
//
// Returns:
//   - error: An error if any DNS server IP is invalid, nil otherwise
func validateDNSServerList(servers string) error {
	if servers == "" {
		return fmt.Errorf("DNS server list cannot be empty")
	}

	serverList := strings.Split(servers, ",")
	for _, server := range serverList {
		server = strings.TrimSpace(server)
		if server == "" {
			return fmt.Errorf("DNS server list contains empty entry")
		}
		if err := validateIPAddress(server); err != nil {
			return fmt.Errorf("invalid DNS server in list: %w", err)
		}
	}

	return nil
}

// validateHAProxyUsername validates a HAProxy statistics username.
//
// Valid usernames must:
//   - Be 1-64 characters long
//   - Contain only alphanumeric characters, hyphens, underscores, and periods
//   - Not contain characters that could cause HAProxy config injection
//
// Parameters:
//   - username: The username string to validate
//
// Returns:
//   - error: An error if the username is invalid, nil otherwise
func validateHAProxyUsername(username string) error {
	if username == "" {
		return nil // Empty username is allowed (disables stats)
	}

	if len(username) > 64 {
		return fmt.Errorf("username too long (max 64 characters): %d", len(username))
	}

	// Allow alphanumeric, dash, underscore, and period
	usernameRegex := regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)
	if !usernameRegex.MatchString(username) {
		return fmt.Errorf("invalid username format (only alphanumeric, dash, underscore, period allowed): %s", username)
	}

	// Reject suspicious patterns that could be used for injection
	if strings.Contains(username, "..") || strings.Contains(username, "--") {
		return fmt.Errorf("username contains suspicious patterns: %s", username)
	}

	return nil
}

// validateHAProxyPassword validates a HAProxy statistics password.
//
// Valid passwords must:
//   - Be 1-128 characters long
//   - Not contain characters that could cause HAProxy config injection
//   - Not contain newlines, carriage returns, or null bytes
//   - Not contain unescaped quotes or backslashes
//
// Parameters:
//   - password: The password string to validate
//
// Returns:
//   - error: An error if the password is invalid, nil otherwise
func validateHAProxyPassword(password string) error {
	if password == "" {
		return nil // Empty password is allowed (disables stats)
	}

	if len(password) > 128 {
		return fmt.Errorf("password too long (max 128 characters): %d", len(password))
	}

	// Reject control characters and characters that could break HAProxy config
	for i, r := range password {
		if r == '\n' || r == '\r' || r == '\x00' {
			return fmt.Errorf("password contains invalid control character at position %d", i)
		}
		// Reject unescaped quotes and backslashes that could break config
		if r == '"' || r == '\'' || r == '\\' {
			return fmt.Errorf("password contains invalid character '%c' at position %d (quotes and backslashes not allowed)", r, i)
		}
		// Reject hash/comment characters that could inject config
		if r == '#' {
			return fmt.Errorf("password contains invalid character '#' at position %d", i)
		}
	}

	return nil
}

// validateHAProxyCredentials validates both username and password together.
//
// This function ensures that if one credential is provided, both must be provided,
// and both must pass individual validation.
//
// Parameters:
//   - username: The username to validate
//   - password: The password to validate
//
// Returns:
//   - error: An error if the credentials are invalid, nil otherwise
func validateHAProxyCredentials(username, password string) error {
	// Both empty is valid (stats disabled)
	if username == "" && password == "" {
		return nil
	}

	// If one is provided, both must be provided
	if username == "" && password != "" {
		return fmt.Errorf("password provided but username is empty")
	}
	if username != "" && password == "" {
		return fmt.Errorf("username provided but password is empty")
	}

	// Validate username
	if err := validateHAProxyUsername(username); err != nil {
		return fmt.Errorf("invalid HAProxy stats username: %w", err)
	}

	// Validate password
	if err := validateHAProxyPassword(password); err != nil {
		return fmt.Errorf("invalid HAProxy stats password: %w", err)
	}

	return nil
}

// validateInterfaceName validates that a string is a valid network interface name.
//
// Valid interface names contain only alphanumeric characters, hyphens, underscores,
// and periods. This prevents command injection through interface names.
//
// Parameters:
//   - iface: The interface name string to validate
//
// Returns:
//   - error: An error if the interface name is invalid, nil otherwise
func validateInterfaceName(iface string) error {
	if iface == "" {
		return fmt.Errorf("interface name cannot be empty")
	}

	// Allow alphanumeric, dash, underscore, and period (common in interface names)
	ifaceRegex := regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)
	if !ifaceRegex.MatchString(iface) {
		return fmt.Errorf("invalid interface name (only alphanumeric, dash, underscore, period allowed): %s", iface)
	}

	// Additional safety: reject names that look like command injection attempts
	if strings.Contains(iface, "..") || strings.Contains(iface, "//") {
		return fmt.Errorf("interface name contains suspicious patterns: %s", iface)
	}

	return nil
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
// Example usage:
//   err := watchInstallationCommand(flagSet, []string{
//       "--cloud", "mycloud",
//       "--domainName", "example.com",
//       "--bastionMetadata", "/path/to/metadata",
//       "--bastionUsername", "core",
//       "--bastionRsa", "/path/to/key.rsa",
//   })
func watchInstallationCommand(watchInstallationFlags *flag.FlagSet, args []string) error {
	err := innerWatchInstallationCommand(watchInstallationFlags, args)
	if err != nil {
		fmt.Printf("%+v\n", err)
		if watchInstallationFlags != nil {
			watchInstallationFlags.Usage()
		}
	}
	return err
}

// innerWwatchInstallationCommand executes the watch-installation command with the given flags and arguments.
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
func innerWatchInstallationCommand(watchInstallationFlags *flag.FlagSet, args []string) error {
	var (
		preLog       strings.Builder
		clouds       cloudFlags
		knownServers = sets.Set[string]{}
		err          error
	)

	// Validate input parameters
	if watchInstallationFlags == nil {
		return fmt.Errorf("%sflag set cannot be nil", errPrefixWatchInstallation)
	}

	// Display version information
	fmt.Fprintf(os.Stderr, "Program version is %v, release = %v\n", version, release)

	// Create a pre-debug log before we know that debugging is enabled
	fmt.Fprintf(&preLog, "[INFO] Starting watch-installation command\n")

	// Create configuration structure
	config := &WatchInstallationConfig{
		APIKey: os.Getenv("IBMCLOUD_API_KEY"),
	}

	// Define command-line flags
	watchInstallationFlags.Var(&clouds, flagWatchInstallationCloud, usageWatchInstallationCloud)
	ptrDomainName := watchInstallationFlags.String(flagWatchInstallationDomainName, defaultWatchInstallationDomainName, usageWatchInstallationDomainName)
	ptrBastionMetadata := watchInstallationFlags.String(flagWatchInstallationBastionMetadata, defaultWatchInstallationBastionMetadata, usageWatchInstallationBastionMetadata)
	ptrBastionUsername := watchInstallationFlags.String(flagWatchInstallationBastionUsername, defaultWatchInstallationBastionUsername, usageWatchInstallationBastionUsername)
	ptrBastionRsa := watchInstallationFlags.String(flagWatchInstallationBastionRsa, defaultWatchInstallationBastionRsa, usageWatchInstallationBastionRsa)
	ptrEnableDhcpd := watchInstallationFlags.String(flagWatchInstallationEnableDhcpd, defaultWatchInstallationEnableDhcpd, usageWatchInstallationEnableDhcpd)
	ptrDhcpInterface := watchInstallationFlags.String(flagWatchInstallationDhcpInterface, defaultWatchInstallationDhcpInterface, usageWatchInstallationDhcpInterface)
	ptrDhcpSubnet := watchInstallationFlags.String(flagWatchInstallationDhcpSubnet, defaultWatchInstallationDhcpSubnet, usageWatchInstallationDhcpSubnet)
	ptrDhcpNetmask := watchInstallationFlags.String(flagWatchInstallationDhcpNetmask, defaultWatchInstallationDhcpNetmask, usageWatchInstallationDhcpNetmask)
	ptrDhcpRouter := watchInstallationFlags.String(flagWatchInstallationDhcpRouter, defaultWatchInstallationDhcpRouter, usageWatchInstallationDhcpRouter)
	ptrDhcpDnsServers := watchInstallationFlags.String(flagWatchInstallationDhcpDnsServers, defaultWatchInstallationDhcpDnsServers, usageWatchInstallationDhcpDnsServers)
	ptrDhcpServerId := watchInstallationFlags.String(flagWatchInstallationDhcpServerId, defaultWatchInstallationDhcpServerId, usageWatchInstallationDhcpServerId)
	ptrShouldDebug := watchInstallationFlags.String(flagWatchInstallationShouldDebug, defaultWatchInstallationShouldDebug, usageWatchInstallationShouldDebug)
	ptrStatsUser := watchInstallationFlags.String("statsUser", "", "HAProxy stats username (leave empty to disable stats)")
	ptrStatsPassword := watchInstallationFlags.String("statsPassword", "", "HAProxy stats password")

	// Parse command-line arguments
	err = watchInstallationFlags.Parse(args)
	if err != nil {
		return fmt.Errorf("%sfailed to parse flags: %w", errPrefixWatchInstallation, err)
	}

	// Populate configuration from parsed flags
	config.Clouds = []string(clouds)
	config.DomainName = *ptrDomainName
	config.BastionMetadataDir = *ptrBastionMetadata
	config.BastionUsername = *ptrBastionUsername
	config.BastionRsa = *ptrBastionRsa
	config.DhcpInterface = *ptrDhcpInterface
	config.DhcpSubnet = *ptrDhcpSubnet
	config.DhcpNetmask = *ptrDhcpNetmask
	config.DhcpRouter = *ptrDhcpRouter
	config.DhcpDnsServers = *ptrDhcpDnsServers
	config.DhcpServerId = *ptrDhcpServerId
	config.StatsUser = *ptrStatsUser
	config.StatsPassword = *ptrStatsPassword

	// Parse enableDhcpd flag
	fmt.Fprintf(&preLog, "[INFO] Parsing DHCP configuration\n")
	switch strings.ToLower(*ptrEnableDhcpd) {
	case boolTrue:
		config.EnableDhcpd = true
		fmt.Fprintf(&preLog, "[INFO] DHCP server enabled\n")
	case boolFalse:
		config.EnableDhcpd = false
		fmt.Fprintf(&preLog, "[INFO] DHCP server disabled\n")
	default:
		return fmt.Errorf("%s%s must be 'true' or 'false', got '%s'", errPrefixWatchInstallation, flagWatchInstallationEnableDhcpd, *ptrEnableDhcpd)
	}

	// Parse debug flag
	shouldDebug, err := parseBoolFlag(*ptrShouldDebug, flagWatchInstallationShouldDebug)
	if err != nil {
		return fmt.Errorf("%s%w", errPrefixWatchInstallation, err)
	}
	config.ShouldDebug = shouldDebug

	// Initialize logger
	log = initLogger(config.ShouldDebug)
	if config.ShouldDebug {
		log.Debugf("Debug mode enabled")
	}

	// Dump the prelogged lines now that log has been initialized!
	scanner := bufio.NewScanner(strings.NewReader(preLog.String()))
	for scanner.Scan() {
		line := scanner.Text()
		log.Println(line)
	}

	// Validate configuration using the config's Validate method
	log.Printf("[INFO] Validating configuration")
	if err := config.Validate(); err != nil {
		return fmt.Errorf("%s%w", errPrefixWatchInstallation, err)
	}
	log.Printf("[INFO] Configuration validated successfully")

	// Log configuration details
	for i, cloud := range config.Clouds {
		log.Debugf("[INFO] Cloud[%d]: %s", i, cloud)
	}
	log.Debugf("[INFO] Domain name: %s", config.DomainName)
	log.Debugf("[INFO] Bastion metadata dir: %s", config.BastionMetadataDir)
	log.Debugf("[INFO] Bastion username: %s", config.BastionUsername)
	log.Debugf("[INFO] Bastion RSA: %s", config.BastionRsa)

	if config.EnableDhcpd {
		log.Debugf("[INFO] DHCP interface: %s", config.DhcpInterface)
		log.Debugf("[INFO] DHCP subnet: %s", config.DhcpSubnet)
		log.Debugf("[INFO] DHCP netmask: %s", config.DhcpNetmask)
		log.Debugf("[INFO] DHCP router: %s", config.DhcpRouter)
		log.Debugf("[INFO] DHCP DNS servers: %s", config.DhcpDnsServers)
		log.Debugf("[INFO] DHCP server ID: %s", config.DhcpServerId)
	}

	if config.StatsUser != "" && config.StatsPassword != "" {
		log.Printf("[INFO] HAProxy stats enabled with username: %s", config.StatsUser)
	} else {
		log.Printf("[INFO] HAProxy stats disabled (no credentials provided)")
	}

	// Store bastion RSA key path in global variable
	bastionRsa = config.BastionRsa

	// Set up signal handling for graceful shutdown
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Create error channel for listener goroutine
	listenerErrChan := make(chan error, 1)

	// Spawn metadata listener goroutine
	log.Printf("[INFO] Starting metadata listener on port %s", listenPort)
	go func() {
		if err := listenForCommands(ctx, config); err != nil {
			log.Errorf("Command listener failed: %v", err)
			listenerErrChan <- err
		}
	}()

	// Enter monitoring loop with graceful shutdown support
	log.Printf("[INFO] Entering monitoring loop")
	ticker := time.NewTicker(watchSleepDuration)
	defer ticker.Stop()

	// Perform initial check immediately
	if err := performMonitoringIteration(ctx, config, &knownServers); err != nil {
		log.Errorf("[ERROR] Initial monitoring iteration failed: %v", err)
		// Continue to loop rather than failing immediately
	}

	for {
		select {
		case <-ctx.Done():
			log.Printf("[INFO] Received shutdown signal, cleaning up...")
			return performGracefulShutdown()

		case err := <-listenerErrChan:
			log.Errorf("[ERROR] Listener failed: %v", err)
			return fmt.Errorf("command listener failed: %w", err)

		case <-ticker.C:
			log.Printf("[INFO] Waking up to check for changes")

			if err := performMonitoringIteration(ctx, config, &knownServers); err != nil {
				log.Errorf("[ERROR] Monitoring iteration failed: %v", err)
				// Continue monitoring despite errors
			}
		}
	}
}

// performMonitoringIteration executes a single monitoring iteration.
//
// This function performs all the monitoring tasks for one iteration:
//   - Gathers bastion information
//   - Retrieves all servers from OpenStack
//   - Detects server changes
//   - Updates configurations (bastion, DHCP, HAProxy, DNS)
//
// Parameters:
//   - ctx: Context for cancellation and timeout
//   - config: Configuration containing all parameters
//   - knownServers: Pointer to set of known servers (updated by this function)
//
// Returns:
//   - error: Any error encountered during the iteration
func performMonitoringIteration(ctx context.Context, config *WatchInstallationConfig, knownServers *sets.Set[string]) error {
	// Create iteration context with timeout
	iterCtx, iterCancel := context.WithTimeout(ctx, watchIterationTimeout)
	defer iterCancel()

	// Convert clouds to cloudFlags type
	clouds := cloudFlags(config.Clouds)

	// Gather bastion information from metadata directories
	log.Printf("[INFO] Gathering bastion information from: %s", config.BastionMetadataDir)
	bastionInformations, err := gatherBastionInformations(config.BastionMetadataDir, config.BastionUsername, config.BastionRsa)
	if err != nil {
		return fmt.Errorf("failed to gather bastion information: %w", err)
	}
	log.Debugf("bastionInformations [%d] = %+v", len(bastionInformations), bastionInformations)
	log.Printf("[INFO] Found %d bastion(s)", len(bastionInformations))

	// Retrieve all servers from OpenStack
	log.Printf("[INFO] Retrieving all servers from clouds: %+v", clouds)
	allServers, err := getAllServers(iterCtx, clouds)
	if err != nil {
		return fmt.Errorf("failed to get all servers: %w", err)
	}
	log.Printf("[INFO] Retrieved %d server(s)", len(allServers))

	// Detect changes in server set
	newServerSet := getServerSet(allServers)
	addedServersSet := newServerSet.Difference(*knownServers)
	deletedServerSet := knownServers.Difference(newServerSet)
	log.Debugf("knownServers     = %+v", *knownServers)
	log.Debugf("newServerSet     = %+v", newServerSet)
	log.Debugf("addedServersSet  = %+v", addedServersSet)
	log.Debugf("deletedServerSet = %+v", deletedServerSet)

	// If no changes detected, return early
	if addedServersSet.Len() == 0 && deletedServerSet.Len() == 0 {
		log.Printf("[INFO] No server changes detected")
		return nil
	}

	// Update known servers set
	log.Printf("[INFO] Server changes detected: %d added, %d deleted", addedServersSet.Len(), deletedServerSet.Len())
	*knownServers = newServerSet

	// Update bastion configurations
	log.Printf("[INFO] Updating bastion configurations")
	err = updateBastionInformations(iterCtx, clouds, bastionInformations)
	if err != nil {
		return fmt.Errorf("failed to update bastion information: %w", err)
	}
	log.Printf("[INFO] Bastion configurations updated successfully")

	// Update DHCP configuration if enabled
	log.Debugf("enableDhcpd = %v", config.EnableDhcpd)
	if config.EnableDhcpd {
		log.Printf("[INFO] Updating DHCP configuration")
		filename := "/tmp/dhcpd.conf"
		err = dhcpdConf(iterCtx,
			filename,
			clouds,
			config.DomainName,
			config.DhcpInterface,
			config.DhcpSubnet,
			config.DhcpNetmask,
			config.DhcpRouter,
			config.DhcpDnsServers,
			config.DhcpServerId,
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
	err = haproxyCfg(iterCtx, clouds, bastionInformations, config.StatsUser, config.StatsPassword)
	if err != nil {
		return fmt.Errorf("failed to update HAProxy configuration: %w", err)
	}
	log.Printf("[INFO] HAProxy configuration updated successfully")

	// Update DNS records if API key is available
	if config.APIKey != "" {
		log.Printf("[INFO] Updating DNS records")
		err = dnsRecords(iterCtx,
			clouds,
			config.APIKey,
			config.DomainName,
			bastionInformations,
			*knownServers,
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

	log.Printf("[INFO] Iteration complete")
	return nil
}

// performGracefulShutdown performs cleanup operations before exiting.
//
// This function is called when a shutdown signal is received and performs
// any necessary cleanup operations before the program exits.
//
// Returns:
//   - error: Any error encountered during shutdown (currently always returns nil)
func performGracefulShutdown() error {
	log.Printf("[INFO] Performing graceful shutdown...")
	// Add any cleanup operations here:
	// - Close connections
	// - Flush logs
	// - Save state
	// - Clean up temporary files
	log.Printf("[INFO] Shutdown complete")
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

	// Validate root path (allow absolute paths for root directory)
	if err = validatePath(rootPath, true); err != nil {
		return nil, fmt.Errorf("invalid root path: %w", err)
	}

	// Ensure path exists and is a directory
	info, err := os.Stat(rootPath)
	if err != nil {
		return nil, fmt.Errorf("cannot access root path: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("root path is not a directory: %s", rootPath)
	}

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

	// Validate filename (allow absolute paths as they come from WalkDir)
	if err = validatePath(filename, true); err != nil {
		return "", "", fmt.Errorf("invalid filename: %w", err)
	}

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
//   - clouds: Cloud names to query for servers
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
func updateBastionInformations(ctx context.Context, clouds cloudFlags, bastionInformations []bastionInformation) (err error) {
	var (
		allServers []servers.Server
	)

	allServers, err = getAllServers(ctx, clouds)
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
			if !errors.Is(err, os.ErrNotExist) {
				// Unexpected error reading metadata - this is a serious issue
				log.Errorf("[ERROR] Failed to read metadata from %s: %v", bastionInformation.Metadata, err)
				return fmt.Errorf("failed to read metadata from %s: %w", bastionInformation.Metadata, err)
			}
			// Metadata file doesn't exist - cluster may have been deleted
			log.Debugf("[INFO] Metadata file not found (cluster may be deleted): %s", bastionInformation.Metadata)
			err = nil
			continue
		}

		bastionServer, err = findServerInList(allServers, clusterName)
		if err != nil {
			// Bastion server not found in OpenStack - may be temporarily unavailable or deleted
			log.Warnf("[WARN] Bastion server %q not found in server list: %v", clusterName, err)
			err = nil
			continue
		}
		log.Debugf("updateBastionInformations: bastionServer.Name = %s", bastionServer.Name)

		_, bastionIpAddress, err = findIpAddress(bastionServer)
		log.Debugf("updateBastionInformations: bastionIpAddress = %s", bastionIpAddress)
		if err != nil {
			// Failed to get IP address - network configuration issue
			log.Warnf("[WARN] Failed to get IP address for bastion %s: %v", bastionServer.Name, err)
			continue
		}
		if bastionIpAddress == "" {
			// No IP address assigned yet - bastion may still be booting
			log.Warnf("[WARN] Bastion %s has no IP address assigned yet", bastionServer.Name)
			continue
		}

		err = addServerKnownHosts(ctx, bastionIpAddress)
		if err != nil {
			// Failed to add to known_hosts - SSH configuration issue, but not critical
			log.Warnf("[WARN] Failed to add bastion %s (%s) to known_hosts: %v", bastionServer.Name, bastionIpAddress, err)
			continue
		}

		_, err = sshAccessSuccess(newSSHConfig(bastionIpAddress, bastionInformation.InstallerRsa))
		if err != nil {
			return fmt.Errorf("failed to echo success to bastion at %s: %w", bastionInformation.IPAddress, err)
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

		log.Debugf("updateBastionInformations: NEW bastionInformation = %+v", bastionInformations[i])

		if previousVMs == 0 && currentVMs > 0 {
			// First time for this bastion - cluster is being created
			log.Printf("[INFO] Cluster %s (InfraID: %s) is initializing with %d VMs on bastion %s",
				bastionInformations[i].ClusterName,
				bastionInformations[i].InfraID,
				currentVMs,
				bastionIpAddress)
			log.Printf("[INFO] Bastion server: %s at %s", bastionServer.Name, bastionIpAddress)
		}

		if currentVMs == 0 && previousVMs > 0 {
			// Last time for this bastion - cluster is being deleted
			log.Printf("[INFO] Cluster %s (InfraID: %s) is being deleted (had %d VMs) from bastion %s",
				bastionInformations[i].ClusterName,
				bastionInformations[i].InfraID,
				previousVMs,
				bastionIpAddress)
			log.Printf("[INFO] All cluster VMs have been removed")
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
//   - macAddress: MAC address of the server's network interface
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

	return "", "", fmt.Errorf("no IP address found for server %s", server.Name)
}

// dhcpdConf generates a DHCP server configuration file for cluster nodes.
//
// Parameters:
//   - ctx: Context for the operation
//   - filename: Path where the configuration file will be written
//   - clouds: Cloud names to query for servers
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
func dhcpdConf(ctx context.Context, filename string, clouds cloudFlags, domainName string, dhcpInterface string, dhcpSubnet string, dhcpNetmask string, dhcpRouter string, dhcpDnsServers string, dhcpServerId string) error {
	var (
		allServers []servers.Server
		server     servers.Server
		file       *os.File
		err        error
	)

	allServers, err = getAllServers(ctx, clouds)
	if err != nil {
		return err
	}

	fmt.Printf("Writing %s\n\n", filename)

	err = os.Remove(filename)
	if err != nil {
		if !os.IsNotExist(err) {
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
	fmt.Fprintf(file, "   option subnet-mask %s;\n", dhcpNetmask)
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
//   - clouds: Cloud names to query for servers
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
func haproxyCfg(ctx context.Context, clouds cloudFlags, bastionInformations []bastionInformation, statsUser string, statsPassword string) error {
	var (
		allServers []servers.Server
		server      servers.Server
		err         error
	)

	allServers, err = getAllServers(ctx, clouds)
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
			if !os.IsNotExist(err) {
				return err
			}
		}

		file, err = os.OpenFile(filename, os.O_CREATE|os.O_RDWR, 0644)
		if err != nil {
			return err
		}
		defer file.Close() // Guaranteed cleanup

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
		if statsUser != "" && statsPassword != "" {
			fmt.Fprintf(file, "listen stats # Define a listen section called \"stats\"\n")
			fmt.Fprintf(file, "  bind 127.0.0.1:9000 # Listen on localhost:9000\n")
			fmt.Fprintf(file, "  mode http\n")
			fmt.Fprintf(file, "  stats enable  # Enable stats page\n")
			fmt.Fprintf(file, "  stats hide-version  # Hide HAProxy version\n")
			fmt.Fprintf(file, "  stats realm Haproxy\\ Statistics  # Title text for popup window\n")
			fmt.Fprintf(file, "  stats uri /haproxy_stats  # Stats URI\n")
			fmt.Fprintf(file, "  stats auth %s:%s  # Authentication credentials\n", statsUser, statsPassword)
			fmt.Fprintf(file, "\n")
		}

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

		// Sync file to disk before using in external commands
		if err := file.Sync(); err != nil {
			return fmt.Errorf("failed to sync haproxy config: %w", err)
		}

		err = runSplitCommand([]string{
			"scp",
			"-i",
			bastionInformation.InstallerRsa,
			filename,
			fmt.Sprintf("%s@%s:/etc/haproxy/haproxy.cfg", bastionInformation.Username, bastionInformation.IPAddress),
		})
		if err != nil {
			return fmt.Errorf("failed to scp to bastion at %s: %w", bastionInformation.IPAddress, err)
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
			return fmt.Errorf("failed to restart haproxy to bastion at %s: %w", bastionInformation.IPAddress, err)
		}
	}

	return nil
}

// atLeastOneClusterName extracts the cluster name from a list of servers.
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
func atLeastOneClusterName(allServers []servers.Server) (clusterName string) {
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

		clusterName = server.Name[0:idx]

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
//   - clouds: Cloud names to query for servers
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
func dnsRecords(ctx context.Context, clouds cloudFlags, apiKey string, domainName string, bastionInformations []bastionInformation, knownServers sets.Set[string], addedServerSet sets.Set[string], deletedServerSet sets.Set[string]) error {
	var (
		dnsService   *dnsrecordsv1.DnsRecordsV1
		cisServiceID string
		crnstr       string
		zoneID       string
		allServers   []servers.Server
		server       servers.Server
		clusterName  string
		err          error
		errs         []error
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

	allServers, err = getAllServers(ctx, clouds)
	if err != nil {
		return err
	}

	// @TODO - One cluster name from the list of all servers?
	clusterName = atLeastOneClusterName(allServers)
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
			if err != nil {
				log.Errorf("Failed to create DNS record for api.%s.%s: %v", bastionInformation.ClusterName, domainName, err)
				errs = append(errs, err)
			}
			err = createOrDeletePublicDNSRecord(ctx,
				dnsrecordsv1.CreateDnsRecordOptions_Type_A,
				fmt.Sprintf("api-int.%s.%s", bastionInformation.ClusterName, domainName),
				bastionInformation.IPAddress,
				true,
				dnsService)
			if err != nil {
				log.Errorf("Failed to create DNS record for api-int.%s.%s: %v", bastionInformation.ClusterName, domainName, err)
				errs = append(errs, err)
			}
			err = createOrDeletePublicDNSRecord(ctx,
				dnsrecordsv1.CreateDnsRecordOptions_Type_Cname,
				fmt.Sprintf("*.apps.%s.%s", bastionInformation.ClusterName, domainName),
				fmt.Sprintf("api.%s.%s", bastionInformation.ClusterName, domainName),
				true,
				dnsService)
			if err != nil {
				log.Errorf("Failed to create DNS record for *.apps.%s.%s: %v", bastionInformation.ClusterName, domainName, err)
				errs = append(errs, err)
			}
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
			if err != nil {
				log.Errorf("Failed to delete DNS record for %s.%s: %v", deletedServer, domainName, err)
				errs = append(errs, err)
			}
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
				if err != nil {
					log.Errorf("Failed to create DNS record for %s.%s: %v", server.Name, domainName, err)
					errs = append(errs, err)
				}
			}
		}
	}

	if len(knownServers) == 0 && !firstDnsRun {
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
			if err != nil {
				log.Errorf("Failed to delete DNS record for api.%s.%s: %v", bastionInformation.ClusterName, domainName, err)
				errs = append(errs, err)
			}
			err = createOrDeletePublicDNSRecord(ctx,
				dnsrecordsv1.CreateDnsRecordOptions_Type_A,
				fmt.Sprintf("api-int.%s.%s", bastionInformation.ClusterName, domainName),
				"",
				false,
				dnsService)
			if err != nil {
				log.Errorf("Failed to delete DNS record for api-int.%s.%s: %v", bastionInformation.ClusterName, domainName, err)
				errs = append(errs, err)
			}
			err = createOrDeletePublicDNSRecord(ctx,
				dnsrecordsv1.CreateDnsRecordOptions_Type_Cname,
				fmt.Sprintf("*.apps.%s.%s", bastionInformation.ClusterName, domainName),
				fmt.Sprintf("api.%s.%s", bastionInformation.ClusterName, domainName),
				false,
				dnsService)
			if err != nil {
				log.Errorf("Failed to delete DNS record for *.apps.%s.%s: %v", bastionInformation.ClusterName, domainName, err)
				errs = append(errs, err)
			}
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("failed to manage DNS records: %+v", errs)
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

	childObjResult, _, err = getChildObjects(ctx, gcv1, &getChildOpt)
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
//   - ctx: Context for cancellation and graceful shutdown
//   - config: Configuration containing cloud names and other settings
//
// Returns:
//   - error: Any error encountered starting the listener or accepting connections
//
// The function listens on the configured port (listenPort constant) and spawns
// a new goroutine to handle each incoming connection. The listener will gracefully
// shut down when the context is cancelled.
//
// Supported commands include:
//   - check-alive: Health check command
//   - create-bastion: Create bastion server
//   - create-metadata: Create cluster metadata
//   - delete-metadata: Delete cluster metadata
//   - erase-metadata: erase metadata directories matching pattern
func listenForCommands(ctx context.Context, config *WatchInstallationConfig) error {
	var (
		closeOnce sync.Once
		closeErr  error
	)

	log.Debugf("listenForCommands")

	// Listen for incoming connections on configured port
	ln, err := net.Listen("tcp", listenPort)
	if err != nil {
		return fmt.Errorf("failed to start listener on %s: %w", listenPort, err)
	}

	// Ensure listener is closed exactly once
	closeListener := func() {
		closeOnce.Do(func() {
			log.Printf("[INFO] Closing listener...")
			closeErr = ln.Close()
			if closeErr != nil {
				log.Errorf("[ERROR] Failed to close listener: %v", closeErr)
			}
		})
	}
	defer closeListener()

	// Close listener when context is cancelled
	go func() {
		<-ctx.Done()
		log.Printf("[INFO] Context cancelled, initiating listener shutdown...")
		closeListener()
	}()

	// Accept incoming connections and handle them
	for {
		conn, err := ln.Accept()
		if err != nil {
			// Check if error is due to context cancellation
			select {
			case <-ctx.Done():
				log.Printf("[INFO] Listener shutting down gracefully")
				return nil
			default:
				// Unexpected error during Accept
				log.Errorf("[ERROR] Failed to accept connection: %v", err)
				return fmt.Errorf("failed to accept connection: %w", err)
			}
		}

		// Handle the connection in a new goroutine
		go handleConnection(ctx, conn, config)
	}
}

// handleConnection processes a single client connection and dispatches commands.
//
// Parameters:
//   - ctx: Context for cancellation
//   - conn: Network connection to the client
//   - config: Configuration containing cloud names and other settings
//
// Returns:
//   - error: Any error encountered reading or processing commands
//
// The function reads JSON-formatted commands from the connection, parses the
// command header to determine the command type, and dispatches to the appropriate
// handler function. It continues reading commands until the connection is closed,
// an error occurs, or the context is cancelled.
//
// Command format: JSON object with "command" field indicating the operation type.
func handleConnection(ctx context.Context, conn net.Conn, config *WatchInstallationConfig) error {
	var (
		data      string
		cmdHeader CommandHeader
		errChan   chan error
		result    error
		err       error
	)

	// Close the connection when we're done
	defer conn.Close()

	// Set read deadline to prevent connection hanging indefinitely
	conn.SetDeadline(time.Now().Add(5 * time.Minute))

	reader := bufio.NewReader(conn)

	for {
		// Reset deadline for each command
		conn.SetDeadline(time.Now().Add(5 * time.Minute))

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

		errChan = make(chan error, 1) // Buffered channel to prevent goroutine leak

		switch cmdHeader.Command {
		case "check-alive":
			var (
				cmd            CommandIsAlive
				marshalledData []byte
			)

			// Launch handler with panic recovery
			go func() {
				defer func() {
					if r := recover(); r != nil {
						log.Errorf("[ERROR] Panic in handleCheckAlive: %v", r)
						errChan <- fmt.Errorf("handler panicked: %v", r)
					}
				}()
				handleCheckAlive(data, errChan)
			}()

			// Wait for result with timeout
			select {
			case result = <-errChan:
				log.Debugf("handleConnection: result from handleCheckAlive is %v", result)
			case <-time.After(2 * time.Minute):
				log.Errorf("[ERROR] Timeout waiting for handleCheckAlive response")
				result = fmt.Errorf("command timeout")
			case <-ctx.Done():
				log.Debugf("handleConnection: context cancelled while waiting for handleCheckAlive")
				return ctx.Err()
			}

			cmd.Command = "is-alive"
			if result != nil {
				cmd.Result = result.Error()
			}

			marshalledData, err = json.Marshal(cmd)
			if err != nil {
				log.Debugf("handleConnection: json.Marshal returns %v", err)
				return err
			}

			err = sendByteArray(conn, marshalledData, 30 * time.Second)
			if err != nil {
				return err
			}

		case "create-metadata":
			var (
				cmd            struct {
					Command string `json:"Command"`
					Result  string `json:"Result"`
				}
				marshalledData []byte
			)

			// Launch handler with panic recovery
			go func() {
				defer func() {
					if r := recover(); r != nil {
						log.Errorf("[ERROR] Panic in handleCreateMetadata: %v", r)
						errChan <- fmt.Errorf("handler panicked: %v", r)
					}
				}()
				handleCreateMetadata(config, data, true, errChan)
			}()

			// Wait for result with timeout
			select {
			case result = <-errChan:
				log.Debugf("handleConnection: result from handleCreateMetadata is %v", result)
			case <-time.After(2 * time.Minute):
				log.Errorf("[ERROR] Timeout waiting for handleCreateMetadata response")
				result = fmt.Errorf("command timeout")
			case <-ctx.Done():
				log.Debugf("handleConnection: context cancelled while waiting for handleCreateMetadata")
				return ctx.Err()
			}

			cmd.Command = "metadata-created"
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

			err = sendByteArray(conn, marshalledData, 30 * time.Second)
			if err != nil {
				return err
			}

		case "delete-metadata":
			var (
				cmd            struct {
					Command string `json:"Command"`
					Result  string `json:"Result"`
				}
				marshalledData []byte
			)

			// Launch handler with panic recovery
			go func() {
				defer func() {
					if r := recover(); r != nil {
						log.Errorf("[ERROR] Panic in handleCreateMetadata (delete): %v", r)
						errChan <- fmt.Errorf("handler panicked: %v", r)
					}
				}()
				handleCreateMetadata(config, data, false, errChan)
			}()

			// Wait for result with timeout
			select {
			case result = <-errChan:
				log.Debugf("handleConnection: result from handleCreateMetadata is %v", result)
			case <-time.After(2 * time.Minute):
				log.Errorf("[ERROR] Timeout waiting for handleCreateMetadata (delete) response")
				result = fmt.Errorf("command timeout")
			case <-ctx.Done():
				log.Debugf("handleConnection: context cancelled while waiting for handleCreateMetadata (delete)")
				return ctx.Err()
			}

			cmd.Command = "metadata-deleted"
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

			err = sendByteArray(conn, marshalledData, 30 * time.Second)
			if err != nil {
				return err
			}

		case "erase-metadata":
			// Check if this is a pattern-based deletion or single file deletion
			// by attempting to unmarshal as CommandEraseMetadata first
			var eraseCmd CommandEraseMetadata
			if unmarshalErr := json.Unmarshal([]byte(data), &eraseCmd); unmarshalErr == nil && eraseCmd.MetadataPattern != "" {
				// This is a pattern-based deletion (erase-metadata)
				var (
					cmd            CommandMetadataErased
					marshalledData []byte
				)

				log.Debugf("handleConnection: detected pattern-based erase-metadata command")

				// Launch handler with panic recovery
				go func() {
					defer func() {
						if r := recover(); r != nil {
							log.Errorf("[ERROR] Panic in handleEraseMetadata: %v", r)
							errChan <- fmt.Errorf("handler panicked: %v", r)
						}
					}()
					handleEraseMetadata(config, data, errChan)
				}()

				// Wait for result with timeout
				select {
				case result = <-errChan:
					log.Debugf("handleConnection: result from handleEraseMetadata is %v", result)
				case <-time.After(2 * time.Minute):
					log.Errorf("[ERROR] Timeout waiting for handleEraseMetadata response")
					result = fmt.Errorf("command timeout")
				case <-ctx.Done():
					log.Debugf("handleConnection: context cancelled while waiting for handleEraseMetadata")
					return ctx.Err()
				}

				cmd.Command = "metadata-erased"
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

				err = sendByteArray(conn, marshalledData, 30*time.Second)
				if err != nil {
					return err
				}
			} else {
				// This is a single file deletion (original behavior)
				var (
					cmd            struct {
						Command string `json:"Command"`
						Result  string `json:"Result"`
					}
					marshalledData []byte
				)

				log.Debugf("handleConnection: detected single file erase-metadata command")

				// Launch handler with panic recovery
				go func() {
					defer func() {
						if r := recover(); r != nil {
							log.Errorf("[ERROR] Panic in handleCreateMetadata (delete): %v", r)
							errChan <- fmt.Errorf("handler panicked: %v", r)
						}
					}()
					handleCreateMetadata(config, data, false, errChan)
				}()

				// Wait for result with timeout
				select {
				case result = <-errChan:
					log.Debugf("handleConnection: result from handleCreateMetadata is %v", result)
				case <-time.After(2 * time.Minute):
					log.Errorf("[ERROR] Timeout waiting for handleCreateMetadata (delete) response")
					result = fmt.Errorf("command timeout")
				case <-ctx.Done():
					log.Debugf("handleConnection: context cancelled while waiting for handleCreateMetadata (delete)")
					return ctx.Err()
				}

				cmd.Command = "metadata-deleted"
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

				err = sendByteArray(conn, marshalledData, 30*time.Second)
				if err != nil {
					return err
				}
			}

		case "create-bastion":
			var (
				cmd            CommandBastionCreated
				marshalledData []byte
			)

			// Launch handler with panic recovery
			go func() {
				defer func() {
					if r := recover(); r != nil {
						log.Errorf("[ERROR] Panic in handleCreateBastion: %v", r)
						errChan <- fmt.Errorf("handler panicked: %v", r)
					}
				}()
				handleCreateBastion(data, cloudFlags(config.Clouds), errChan)
			}()

			// Wait for result with timeout (longer timeout for bastion creation)
			select {
			case result = <-errChan:
				log.Debugf("handleConnection: result from handleCreateBastion is %v", result)
			case <-time.After(15 * time.Minute):
				log.Errorf("[ERROR] Timeout waiting for handleCreateBastion response")
				result = fmt.Errorf("command timeout")
			case <-ctx.Done():
				log.Debugf("handleConnection: context cancelled while waiting for handleCreateBastion")
				return ctx.Err()
			}

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

			err = sendByteArray(conn, marshalledData, 30 * time.Second)
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

// validatePath validates a file path for security and correctness.
//
// Parameters:
//   - path: The file path to validate
//   - allowAbsolute: Whether to allow absolute paths
//
// Returns:
//   - error: Validation error if path is invalid, nil otherwise
//
// The function performs the following checks:
//   - Rejects empty paths
//   - Optionally rejects absolute paths
//   - Rejects path traversal attempts (..)
//   - Ensures path is clean (no redundant separators or elements)
func validatePath(path string, allowAbsolute bool) error {
	if path == "" {
		return fmt.Errorf("path cannot be empty")
	}

	if !allowAbsolute && filepath.IsAbs(path) {
		return fmt.Errorf("absolute paths not allowed: %s", path)
	}

	cleaned := filepath.Clean(path)
	if !allowAbsolute && cleaned != path {
		return fmt.Errorf("path contains invalid components: %s", path)
	}

	if strings.Contains(cleaned, "..") {
		return fmt.Errorf("path traversal not allowed: %s", path)
	}

	return nil
}

// validateInfraID ensures the InfraID is safe for use in file paths.
// This is a convenience wrapper around validatePath for InfraID validation.
func validateInfraID(infraID string) error {
	if err := validatePath(infraID, false); err != nil {
		return fmt.Errorf("invalid infraID: %w", err)
	}
	return nil
}

// handleCreateMetadata processes a create or delete metadata command.
//
// Parameters:
//   - config: Configuration containing cloud names and other settings
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
func handleCreateMetadata(config *WatchInstallationConfig, data string, shouldCreate bool, errChan chan error) {
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

	if err = validateInfraID(cmd.Metadata.InfraID); err != nil {
		log.Debugf("handleCreateMetadata: invalid infraID: %v", err)
		errChan <- err
		return
	}

	marshalledData, err = json.Marshal(cmd.Metadata)
	if err != nil {
		log.Debugf("handleCreateMetadata: json.Marshal() returns %v", err)
		errChan <- err
		return
	}

	if shouldCreate {
		// Create the directory to save the metadata file in
		dir := filepath.Join(config.BastionMetadataDir, cmd.Metadata.InfraID)

		err = os.MkdirAll(dir, 0750)
		if err != nil {
			log.Debugf("handleCreateMetadata: os.MkdirAll() returns %v", err)
			errChan <- err
			return
		}

		file := filepath.Join(dir, "metadata.json")

		err = os.WriteFile(file, marshalledData, 0644)
		if err != nil {
			log.Debugf("handleCreateMetadata: os.WriteFile() returns %v", err)
			errChan <- err
			return
		}
		log.Debugf("handleCreateMetadata: wrote %s", file)
	} else {
		dir := filepath.Join(config.BastionMetadataDir, cmd.Metadata.InfraID)
		file := filepath.Join(dir, "metadata.json")

		err = os.Remove(file)
		if err != nil {
			log.Debugf("handleCreateMetadata: os.Remove(%s) returns %v", file, err)
			errChan <- err
			return
		}

		err = os.Remove(dir)
		if err != nil {
			log.Debugf("handleCreateMetadata: os.Remove(%s) returns %v", dir, err)
			errChan <- err
			return
		}
		log.Debugf("handleCreateMetadata: deleted %s", file)
	}

	errChan <- err
	return
}

// handleEraseMetadata processes an erase-metadata command to delete metadata matching a pattern.
//
// Parameters:
//   - config: Configuration containing bastion metadata directory
//   - data: JSON-formatted command data containing the metadata pattern
//   - errChan: Channel to send the result error (or nil for success)
//
// The function performs the following steps:
//  1. Unmarshals the command data to extract the metadata pattern
//  2. Searches for directories matching the pattern in the bastion metadata directory
//  3. Deletes matching metadata.json files and their parent directories
//  4. Sends the result to the error channel
//
// The pattern supports wildcards and is matched against infrastructure IDs.
func handleEraseMetadata(config *WatchInstallationConfig, data string, errChan chan error) {
	var (
		cmd CommandEraseMetadata
		err error
	)

	// Print the incoming data
	log.Debugf("handleEraseMetadata: Received: %s", data)

	err = json.Unmarshal([]byte(data), &cmd)
	if err != nil {
		log.Debugf("handleEraseMetadata: Unmarshal() returns %v", err)
		errChan <- fmt.Errorf("failed to unmarshal erase-metadata command: %w", err)
		return
	}
	log.Debugf("handleEraseMetadata: cmd.Command = %s", cmd.Command)
	log.Debugf("handleEraseMetadata: cmd.MetadataPattern = %s", cmd.MetadataPattern)

	// Validate the pattern
	if cmd.MetadataPattern == "" {
		log.Debugf("handleEraseMetadata: empty metadata pattern")
		errChan <- fmt.Errorf("metadata pattern cannot be empty")
		return
	}

	// Read the bastion metadata directory
	entries, err := os.ReadDir(config.BastionMetadataDir)
	if err != nil {
		log.Debugf("handleEraseMetadata: os.ReadDir() returns %v", err)
		errChan <- fmt.Errorf("failed to read metadata directory: %w", err)
		return
	}

	// Convert pattern to regex (simple wildcard support: * becomes .*)
	patternRegex := strings.ReplaceAll(cmd.MetadataPattern, "*", ".*")
	patternRegex = "^" + patternRegex + "$"

	regex, err := regexp.Compile(patternRegex)
	if err != nil {
		log.Debugf("handleEraseMetadata: invalid pattern %s: %v", cmd.MetadataPattern, err)
		errChan <- fmt.Errorf("invalid metadata pattern: %w", err)
		return
	}

	// Track deleted entries
	deletedCount := 0
	var deleteErrors []string

	// Iterate through directories and delete matching ones
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		infraID := entry.Name()

		// Check if the directory name matches the pattern
		if !regex.MatchString(infraID) {
			log.Debugf("handleEraseMetadata: skipping %s (does not match pattern)", infraID)
			continue
		}

		log.Debugf("handleEraseMetadata: matched %s", infraID)

		// Delete the metadata.json file
		dir := filepath.Join(config.BastionMetadataDir, infraID)
		file := filepath.Join(dir, "metadata.json")

		if err := os.Remove(file); err != nil {
			if !os.IsNotExist(err) {
				errMsg := fmt.Sprintf("failed to remove %s: %v", file, err)
				log.Debugf("handleEraseMetadata: %s", errMsg)
				deleteErrors = append(deleteErrors, errMsg)
				continue
			}
			// If file doesn't exist, still try to remove the directory
			log.Debugf("handleEraseMetadata: %s does not exist, continuing", file)
		} else {
			log.Debugf("handleEraseMetadata: removed %s", file)
		}

		// Delete the directory
		if err := os.Remove(dir); err != nil {
			if !os.IsNotExist(err) {
				errMsg := fmt.Sprintf("failed to remove directory %s: %v", dir, err)
				log.Debugf("handleEraseMetadata: %s", errMsg)
				deleteErrors = append(deleteErrors, errMsg)
				continue
			}
		} else {
			log.Debugf("handleEraseMetadata: removed directory %s", dir)
		}

		deletedCount++
	}

	// Report results
	if len(deleteErrors) > 0 {
		errChan <- fmt.Errorf("deleted %d entries but encountered errors: %s", deletedCount, strings.Join(deleteErrors, "; "))
		return
	}

	if deletedCount == 0 {
		log.Debugf("handleEraseMetadata: no metadata matching pattern '%s'", cmd.MetadataPattern)
		errChan <- fmt.Errorf("no metadata matching pattern '%s'", cmd.MetadataPattern)
		return
	}

	log.Printf("[INFO] handleEraseMetadata: successfully deleted %d metadata entries matching pattern '%s'", deletedCount, cmd.MetadataPattern)
	errChan <- nil
}

// handleCreateBastion processes a create-bastion command to set up a bastion server.
//
// Parameters:
//   - data: JSON-formatted command data containing bastion configuration
//   - clouds: Cloud name for the operation
//   - errChan: Channel to send the result error (or nil for success)
//
// The function performs the following steps:
//  1. Unmarshals the command data to extract server name, domain name, and HAProxy configuration
//  2. Creates a context with 10-minute timeout (using bastionContextTimeout constant)
//  3. Calls setupBastionServer to configure the bastion with the specified HAProxy setting
//  4. Sends the result to the error channel
//
// The HAProxy configuration is controlled by the EnableHAProxy field in the command structure.
func handleCreateBastion(data string, clouds cloudFlags, errChan chan error) {
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
	log.Debugf("handleCreateBastion: cmd.Command       = %s", cmd.Command)
	log.Debugf("handleCreateBastion: cmd.CloudName     = %s", cmd.CloudName)
	log.Debugf("handleCreateBastion: cmd.ServerName    = %s", cmd.ServerName)
	log.Debugf("handleCreateBastion: cmd.DomainName    = %s", cmd.DomainName)
	log.Debugf("handleCreateBastion: cmd.EnableHAProxy = %v", cmd.EnableHAProxy)

	ctx, cancel = context.WithTimeout(context.Background(), bastionContextTimeout)
	defer cancel()

	// Use the EnableHAProxy field from the command structure
	err = setupBastionServer(ctx, cmd.EnableHAProxy, clouds, cmd.ServerName, cmd.DomainName, bastionRsa)
	log.Debugf("handleCreateBastion: setupBastionServer returns %v", err)
	errChan <- err
}
