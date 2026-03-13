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
	// Standard library imports (alphabetically sorted)
	"context"
	"errors"
	"flag"
	"fmt"
	"math"
	"net"
	"os"
	"os/exec"
	"path"
	"strings"
	"time"

	// Third-party imports - gophercloud (grouped by functionality)
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/flavors"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/keypairs"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"
	"github.com/gophercloud/gophercloud/v2/openstack/image/v2/images"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/networks"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/ports"

	// Third-party imports - IBM SDK
	"github.com/IBM/networking-go-sdk/dnsrecordsv1"

	// Third-party imports - Kubernetes
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/ptr"
)

const (
	bastionIpFilename     = "/tmp/bastionIp"
	defaultAvailZone      = "s1022"
	maxSSHRetries         = 10
	sshRetryDelay         = 15 * time.Second
	haproxyConfigPerms    = "646"
	haproxyConfigPath     = "/etc/haproxy/haproxy.cfg"
	haproxySelinuxSetting = "haproxy_connect_any"
	filePermReadWrite     = 0644
	sshUser               = "cloud-user"
	haproxyPackageName    = "haproxy"
	haproxyServiceName    = "haproxy.service"
)

// BastionConfig holds all configuration for bastion creation.
// It supports two modes of operation:
//   1. Local setup: Requires BastionRsa for SSH access
//   2. Remote setup: Requires ServerIP to delegate setup to another server
type BastionConfig struct {
	// OpenStack Configuration
	Cloud       string // OpenStack cloud name from clouds.yaml (required)
	NetworkName string // OpenStack network name for bastion VM (required)

	// Bastion VM Specification
	BastionName string // Name of the bastion VM (required, alphanumeric/hyphens/underscores only)
	FlavorName  string // OpenStack flavor for VM sizing (required)
	ImageName   string // OpenStack image for VM OS (required)
	SshKeyName  string // OpenStack SSH keypair name (required)

	// Setup Mode (mutually exclusive)
	BastionRsa string // Path to RSA private key for local SSH setup (mutually exclusive with ServerIP)
	ServerIP   string // IP address for remote setup delegation (mutually exclusive with BastionRsa)

	// Optional Configuration
	DomainName    string // DNS domain for bastion records (optional, requires IBMCLOUD_API_KEY)
	EnableHAProxy bool   // Enable HAProxy load balancer (default: true)
	ShouldDebug   bool   // Enable debug logging (default: false)

	// Internal state (not exposed via flags)
	validated bool // Tracks if Validate() has been called successfully
}

// Validate checks if the configuration is valid and returns detailed errors.
// It performs the following checks:
//   - Required field presence
//   - Field format validation (names, IPs, file paths)
//   - Mutual exclusivity constraints
//   - Resource accessibility (file existence)
func (c *BastionConfig) Validate() error {
	// Skip re-validation if already validated
	if c.validated {
		return nil
	}

	var validationErrors []error

	// Required fields validation
	if c.Cloud == "" {
		validationErrors = append(validationErrors, fmt.Errorf("cloud: field is required"))
	}
	if c.BastionName == "" {
		validationErrors = append(validationErrors, fmt.Errorf("bastionName: field is required"))
	} else if !isValidResourceName(c.BastionName) {
		validationErrors = append(validationErrors, 
			fmt.Errorf("bastionName: contains invalid characters (use alphanumeric, hyphens, underscores): %q", c.BastionName))
	}

	// Setup mode validation (mutually exclusive)
	if c.BastionRsa == "" && c.ServerIP == "" {
		validationErrors = append(validationErrors, 
			fmt.Errorf("setup mode: either bastionRsa (local) or serverIP (remote) must be specified"))
	}
	if c.BastionRsa != "" && c.ServerIP != "" {
		validationErrors = append(validationErrors, 
			fmt.Errorf("setup mode: bastionRsa and serverIP are mutually exclusive"))
	}

	// File path validation
	if c.BastionRsa != "" {
		if _, err := os.Stat(c.BastionRsa); err != nil {
			if os.IsNotExist(err) {
				validationErrors = append(validationErrors, 
					fmt.Errorf("bastionRsa: file not found: %q", c.BastionRsa))
			} else {
				validationErrors = append(validationErrors, 
					fmt.Errorf("bastionRsa: cannot access file: %w", err))
			}
		}
	}

	// IP address format validation
	if c.ServerIP != "" {
		if net.ParseIP(c.ServerIP) == nil {
			validationErrors = append(validationErrors, 
				fmt.Errorf("serverIP: invalid IP address format: %q", c.ServerIP))
		}
	}

	// OpenStack resource validation
	requiredFields := map[string]string{
		"flavorName":  c.FlavorName,
		"imageName":   c.ImageName,
		"networkName": c.NetworkName,
		"sshKeyName":  c.SshKeyName,
	}

	for fieldName, value := range requiredFields {
		if value == "" {
			validationErrors = append(validationErrors, 
				fmt.Errorf("%s: field is required", fieldName))
		}
	}

	// Return aggregated errors
	if len(validationErrors) > 0 {
		return fmt.Errorf("configuration validation failed:\n  - %w", 
			errors.Join(validationErrors...))
	}

	// Mark as validated on success
	c.validated = true

	return nil
}

// IsLocalSetup returns true if the bastion is configured for local setup mode.
func (c *BastionConfig) IsLocalSetup() bool {
	return c.BastionRsa != ""
}

// IsRemoteSetup returns true if the bastion is configured for remote setup mode.
func (c *BastionConfig) IsRemoteSetup() bool {
	return c.ServerIP != ""
}

// HasDNSConfig returns true if DNS configuration is available.
func (c *BastionConfig) HasDNSConfig() bool {
	return c.DomainName != "" && os.Getenv("IBMCLOUD_API_KEY") != ""
}

// String returns a string representation of the config (safe for logging).
// Sensitive fields like BastionRsa path are masked.
func (c *BastionConfig) String() string {
	rsaPath := "<not set>"
	if c.BastionRsa != "" {
		rsaPath = "<redacted>"
	}

	return fmt.Sprintf("BastionConfig{Cloud=%q, Name=%q, Flavor=%q, Image=%q, "+
		"Network=%q, SSHKey=%q, Domain=%q, HAProxy=%v, ServerIP=%q, RSA=%s, Debug=%v}",
		c.Cloud, c.BastionName, c.FlavorName, c.ImageName,
		c.NetworkName, c.SshKeyName, c.DomainName, c.EnableHAProxy,
		c.ServerIP, rsaPath, c.ShouldDebug)
}

// NewBastionConfig creates a BastionConfig with sensible defaults.
// Default values:
//   - EnableHAProxy: true (HAProxy is enabled)
//   - ShouldDebug: false (debug logging disabled)
func NewBastionConfig() *BastionConfig {
	return &BastionConfig{
		EnableHAProxy: true,
		ShouldDebug:   false,
	}
}

// NewBastionConfigWithDefaults creates a BastionConfig with custom defaults.
// This is useful for testing or when different default values are needed.
func NewBastionConfigWithDefaults(enableHAProxy, shouldDebug bool) *BastionConfig {
	return &BastionConfig{
		EnableHAProxy: enableHAProxy,
		ShouldDebug:   shouldDebug,
	}
}

// parseBastionFlags extracts and validates flags into a BastionConfig
func parseBastionFlags(flags *flag.FlagSet, args []string) (*BastionConfig, error) {
	config := NewBastionConfig()  // Use constructor with defaults

	// Define flags
	cloud := flags.String("cloud", "", "The cloud to use in clouds.yaml")
	bastionName := flags.String("bastionName", "", "The name of the bastion VM")
	bastionRsa := flags.String("bastionRsa", "", "The RSA filename for the bastion VM")
	flavorName := flags.String("flavorName", "", "The name of the flavor")
	imageName := flags.String("imageName", "", "The name of the image")
	networkName := flags.String("networkName", "", "The name of the network")
	sshKeyName := flags.String("sshKeyName", "", "The name of the SSH keypair")
	domainName := flags.String("domainName", "", "The DNS domain (optional)")
	enableHAProxy := flags.String("enableHAProxy", "true", "Enable HA Proxy daemon")
	serverIP := flags.String("serverIP", "", "The IP address of the server")
	shouldDebug := flags.String("shouldDebug", "false", "Enable debug output")

	// Parse flags
	if err := flags.Parse(args); err != nil {
		return nil, fmt.Errorf("failed to parse flags: %w", err)
	}

	// Populate config
	config.Cloud = *cloud
	config.BastionName = *bastionName
	config.BastionRsa = *bastionRsa
	config.FlavorName = *flavorName
	config.ImageName = *imageName
	config.NetworkName = *networkName
	config.SshKeyName = *sshKeyName
	config.DomainName = *domainName
	config.ServerIP = *serverIP

	// Parse boolean flags
	var err error
	config.EnableHAProxy, err = parseBoolFlag(*enableHAProxy, "enableHAProxy")
	if err != nil {
		return nil, err
	}

	config.ShouldDebug, err = parseBoolFlag(*shouldDebug, "shouldDebug")
	if err != nil {
		return nil, err
	}

	// Validate configuration
	if err := config.Validate(); err != nil {
		return nil, err
	}

	return config, nil
}

func createBastionCommand(createBastionFlags *flag.FlagSet, args []string) error {
	// Print version info
	fmt.Fprintf(os.Stderr, "Program version is %v, release = %v\n", version, release)

	// Parse and validate configuration
	config, err := parseBastionFlags(createBastionFlags, args)
	if err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}

	// Initialize logger
	log = initLogger(config.ShouldDebug)

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()

	// Clean up previous bastion IP file
	if err := cleanupBastionIPFile(); err != nil {
		return fmt.Errorf("failed to cleanup bastion IP file: %w", err)
	}

	// Ensure server exists
	if err := ensureServerExists(ctx, config); err != nil {
		return fmt.Errorf("failed to ensure server exists: %w", err)
	}

	// Setup bastion server
	if err := setupBastion(ctx, config); err != nil {
		return fmt.Errorf("failed to setup bastion: %w", err)
	}

	// Write bastion IP to file
	if err := writeBastionIP(ctx, config.Cloud, config.BastionName); err != nil {
		return fmt.Errorf("failed to write bastion IP: %w", err)
	}

	return nil
}

// cleanupBastionIPFile removes the bastion IP file if it exists
func cleanupBastionIPFile() error {
	err := os.Remove(bastionIpFilename)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove bastion IP file: %w", err)
	}
	return nil
}

// ensureServerExists checks if server exists and creates it if needed
func ensureServerExists(ctx context.Context, config *BastionConfig) error {
	_, err := findServer(ctx, config.Cloud, config.BastionName)
	if err != nil {
		if errors.Is(err, ErrServerNotFound) {
			fmt.Printf("Server %s not found, creating...\n", config.BastionName)

			err = createServer(ctx,
				config.Cloud,
				config.FlavorName,
				config.ImageName,
				config.NetworkName,
				config.SshKeyName,
				config.BastionName,
				nil,
			)
			if err != nil {
				return fmt.Errorf("failed to create server: %w", err)
			}

			fmt.Println("Server created successfully!")
		} else {
			return fmt.Errorf("failed to find server: %w", err)
		}
	}

	// Verify server exists
	_, err = findServer(ctx, config.Cloud, config.BastionName)
	if err != nil {
		return fmt.Errorf("server verification failed: %w", err)
	}

	return nil
}

// setupBastion configures the bastion server either remotely or locally
func setupBastion(ctx context.Context, config *BastionConfig) error {
	if config.ServerIP != "" {
		fmt.Println("Setting up bastion remotely...")
		return sendCreateBastion(config.ServerIP, config.Cloud, config.BastionName, config.DomainName)
	}

	fmt.Println("Setting up bastion locally...")
	return setupBastionServer(ctx, config.EnableHAProxy, config.Cloud, config.BastionName, config.DomainName, config.BastionRsa)
}

func createServer(ctx context.Context, cloudName string, flavorName string, imageName string, networkName string, sshKeyName string, bastionName string, userData []byte) error {
	var (
		flavor           flavors.Flavor
		image            images.Image
		network          networks.Network
		sshKeyPair       keypairs.KeyPair
		builder          ports.CreateOptsBuilder
		portCreateOpts   ports.CreateOpts
		portList         []servers.Network
		serverCreateOpts servers.CreateOptsBuilder
		newServer        *servers.Server
		err              error
	)

	flavor, err = findFlavor(ctx, cloudName, flavorName)
	if err != nil {
		return err
	}
	log.Debugf("createServer: flavor = %+v", flavor)

	image, err = findImage(ctx, cloudName, imageName)
	if err != nil {
		return err
	}
	log.Debugf("createServer: image = %+v", image)

	network, err = findNetwork(ctx, cloudName, networkName)
	if err != nil {
		return err
	}
	log.Debugf("createServer: network = %+v", network)

	if sshKeyName != "" {
		sshKeyPair, err = findKeyPair(ctx, cloudName, sshKeyName)
		if err != nil {
			return err
		}
		log.Debugf("createServer: sshKeyPair = %+v", sshKeyPair)
	}

	connNetwork, err := NewServiceClient(ctx, "network", DefaultClientOpts(cloudName))
	if err != nil {
		return err
	}
	fmt.Printf("createServer: connNetwork = %+v\n", connNetwork)

	portCreateOpts = ports.CreateOpts{
		Name:                  fmt.Sprintf("%s-port", bastionName),
		NetworkID:		network.ID,
		Description:           "hamzy test",
		AdminStateUp:          nil,
		MACAddress:            ptr.Deref(nil, ""),
		AllowedAddressPairs:   nil,
		ValueSpecs:            nil,
		PropagateUplinkStatus: nil,
	}

	builder = portCreateOpts
	log.Debugf("createServer: builder = %+v\n", builder)

	port, err := ports.Create(ctx, connNetwork, builder).Extract()
	if err != nil {
		return err
	}
	log.Debugf("createServer: port = %+v\n", port)
	log.Debugf("createServer: port.ID = %v\n", port.ID)

	connCompute, err := NewServiceClient(ctx, "compute", DefaultClientOpts(cloudName))
	if err != nil {
		return err
	}
	fmt.Printf("createServer: connCompute = %+v\n", connCompute)

	portList = []servers.Network{
		{ Port: port.ID, },
	}

	serverCreateOpts = servers.CreateOpts{
		AvailabilityZone: defaultAvailZone,
		FlavorRef:        flavor.ID,
		ImageRef:         image.ID,
		Name:             bastionName,
		Networks:         portList,
		UserData:         userData,
		// Additional properties are not allowed ('tags' was unexpected)
//		Tags:             tags[:],
//              KeyName:          "",
//
//		Metadata:         instanceSpec.Metadata,
//		ConfigDrive:      &instanceSpec.ConfigDrive,
//		BlockDevice:      blockDevices,
	}
	log.Debugf("createServer: serverCreateOpts = %+v\n", serverCreateOpts)

	if sshKeyName != "" {
		newServer, err = servers.Create(ctx,
			connCompute,
			keypairs.CreateOptsExt{
				CreateOptsBuilder: serverCreateOpts,
				KeyName:           sshKeyPair.Name,
			},
			nil).Extract()
	} else {
		newServer, err = servers.Create(ctx, connCompute, serverCreateOpts, nil).Extract()
	}
	if err != nil {
		return err
	}
	log.Debugf("createServer: newServer = %+v\n", newServer)

	err = waitForServer(ctx, cloudName, bastionName)
	log.Debugf("createServer: waitForServer = %v\n", err)
	if err != nil {
		return err
	}

	return err
}

func addServerKnownHosts(ctx context.Context, ipAddress string) error {
	var (
		homeDir    string
		knownHosts string
		outb       []byte
		outs       string
		err        error
	)

	homeDir, err = os.UserHomeDir()
	if err != nil {
		return err
	}
	log.Debugf("addServerKnownHosts: homeDir = %s", homeDir)

	knownHosts = path.Join(homeDir, ".ssh/known_hosts")
	log.Debugf("addServerKnownHosts: knownHosts = %s", knownHosts)

	// Remove ipAddress from known_hosts
	outb, err = runSplitCommand2([]string{
		"ssh-keygen",
		"-f",
		knownHosts,
		"-R",
		ipAddress,
	})
	outs = strings.TrimSpace(string(outb))
	log.Debugf("addServerKnownHosts: outs = \"%s\"", outs)

	outb, err = keyscanServer(ctx, ipAddress, false)
	if err != nil {
		return err
	}

	fileKnownHosts, err := os.OpenFile(knownHosts, os.O_APPEND|os.O_RDWR, filePermReadWrite)
	if err != nil {
		return fmt.Errorf("failed to open known_hosts file %q: %w", knownHosts, err)
	}

	defer fileKnownHosts.Close()

	n, err := fileKnownHosts.Write(outb)
	if err != nil {
		return err
	}

	if n != len(outb) {
		return fmt.Errorf("Could not write entire data to known_hosts")
	}

	return nil
}

func setupBastionServer(ctx context.Context, enableHAProxy bool, cloudName string, serverName string, domainName string, bastionRsa string) error {
	var (
		server       servers.Server
		ipAddress    string
		outb         []byte
		outs         string
		exitError    *exec.ExitError
		apiKey       string
		err          error
	)

	server, err = findServer(ctx, cloudName, serverName)
	log.Debugf("setupBastionServer: server = %+v", server)
	if err != nil {
		return err
	}

	_, ipAddress, err = findIpAddress(server)
	if err != nil {
		return err
	}
	if ipAddress == "" {
		return fmt.Errorf("ip address is empty for server %s", server.Name)
	}

	log.Debugf("setupBastionServer: ipAddress = %s", ipAddress)
	log.Debugf("setupBastionServer: bastionRsa = %s", bastionRsa)

	fmt.Printf("Setting up server %s...\n", server.Name)

	if enableHAProxy {
		err = addServerKnownHosts(ctx, ipAddress)
		if err != nil {
			return err
		}

		for i := 0; i < maxSSHRetries; i++ {
			outb, err = runSplitCommand2([]string{
				"ssh",
				"-i",
				bastionRsa,
				fmt.Sprintf("cloud-user@%s", ipAddress),
				"echo",
				"ready",
			})
			outs = strings.TrimSpace(string(outb))
			log.Debugf("setupBastionServer: outs = \"%s\"", outs)
			if outs == "ready" {
				break
			} else if strings.Contains(outs, "Permission denied") {
				return fmt.Errorf("Error: ssh publickey Permission denied")
			}
			time.Sleep(sshRetryDelay)
		}
		if outs != "ready" {
			return fmt.Errorf("Error: HAProxy not ready in time")
		}

		outb, err = runSplitCommand2([]string{
			"ssh",
			"-i",
			bastionRsa,
			fmt.Sprintf("cloud-user@%s", ipAddress),
			"rpm",
			"-q",
			haproxyPackageName,
		})
		outs = strings.TrimSpace(string(outb))
		log.Debugf("setupBastionServer: outs = \"%s\"", outs)
		if errors.As(err, &exitError) {
			log.Debugf("setupBastionServer: exitError.ExitCode() = %+v\n", exitError.ExitCode())

			if exitError.ExitCode() == 1 && outs == "package haproxy is not installed" {
				outb, err = runSplitCommand2([]string{
					"ssh",
					"-i",
					bastionRsa,
					fmt.Sprintf("cloud-user@%s", ipAddress),
					"sudo",
					"dnf",
					"install",
					"-y",
					haproxyPackageName,
				})
				outs = strings.TrimSpace(string(outb))
				log.Debugf("setupBastionServer: outs = %s", outs)
				log.Debugf("setupBastionServer: err = %+v", err)
			}
		} else if err != nil {
			log.Debugf("setupBastionServer: err = %+v", err)
			return err
		}

		outb, err = runSplitCommand2([]string{
			"ssh",
			"-i",
			bastionRsa,
			fmt.Sprintf("cloud-user@%s", ipAddress),
			"sudo",
			"stat",
			"-c",
			"%a",
			haproxyConfigPath,
		})
		outs = strings.TrimSpace(string(outb))
		log.Debugf("setupBastionServer: outb = \"%s\"", outs)
		if err != nil {
			log.Debugf("setupBastionServer: err = %+v", err)
			return err
		}
		if outs != haproxyConfigPerms {
			outb, err = runSplitCommand2([]string{
				"ssh",
				"-i",
				bastionRsa,
				fmt.Sprintf("cloud-user@%s", ipAddress),
				"sudo",
				"chmod",
				haproxyConfigPerms,
				haproxyConfigPath,
			})
			outs = strings.TrimSpace(string(outb))
			log.Debugf("setupBastionServer: outb = \"%s\"", outs)
			if err != nil {
				log.Debugf("setupBastionServer: err = %+v", err)
				return err
			}
		}

		outb, err = runSplitCommand2([]string{
			"ssh",
			"-i",
			bastionRsa,
			fmt.Sprintf("cloud-user@%s", ipAddress),
			"sudo",
			"getsebool",
			haproxySelinuxSetting,
		})
		outs = strings.TrimSpace(string(outb))
		log.Debugf("setupBastionServer: outb = \"%s\"", outs)
		if err != nil {
			log.Debugf("setupBastionServer: err = %+v", err)
			return err
		}
		if outs != "haproxy_connect_any --> on" {
			outb, err = runSplitCommand2([]string{
				"ssh",
				"-i",
				bastionRsa,
				fmt.Sprintf("cloud-user@%s", ipAddress),
				"sudo",
				"setsebool",
				"-P",
				"haproxy_connect_any=1",
			})
			outs = strings.TrimSpace(string(outb))
			log.Debugf("setupBastionServer: outb = \"%s\"", outs)
			if err != nil {
				log.Debugf("setupBastionServer: err = %+v", err)
				return err
			}
		}

		outb, err = runSplitCommand2([]string{
			"ssh",
			"-i",
			bastionRsa,
			fmt.Sprintf("cloud-user@%s", ipAddress),
			"sudo",
			"systemctl",
			"enable",
			haproxyServiceName,
		})
		outs = strings.TrimSpace(string(outb))
		log.Debugf("setupBastionServer: outb = \"%s\"", outs)
		if err != nil {
			log.Debugf("setupBastionServer: err = %+v", err)
			return err
		}

		outb, err = runSplitCommand2([]string{
			"ssh",
			"-i",
			bastionRsa,
			fmt.Sprintf("cloud-user@%s", ipAddress),
			"sudo",
			"systemctl",
			"start",
			haproxyServiceName,
		})
		outs = strings.TrimSpace(string(outb))
		log.Debugf("setupBastionServer: outb = \"%s\"", outs)
		if err != nil {
			log.Debugf("setupBastionServer: err = %+v", err)
			return err
		}
	}

	// NOTE: This is optional
	apiKey = os.Getenv("IBMCLOUD_API_KEY")

	if apiKey != "" {
		err = dnsForServer(ctx, cloudName, apiKey, serverName, domainName)
		if err != nil {
			return err
		}
	} else {
		fmt.Println("Warning: IBMCLOUD_API_KEY not set.  Make sure DNS is supported via another way.")
	}

	return err
}

func writeBastionIP(ctx context.Context, cloudName string, serverName string) error {
	var (
		server       servers.Server
		ipAddress    string
		err          error
	)

	server, err = findServer(ctx, cloudName, serverName)
	log.Debugf("writeBastionIP: server = %+v", server)
	if err != nil {
		return err
	}

	_, ipAddress, err = findIpAddress(server)
	if err != nil {
		return err
	}
	if ipAddress == "" {
		return fmt.Errorf("ip address is empty for server %s", server.Name)
	}

	log.Debugf("writeBastionIP: ipAddress = %s", ipAddress)

	fileBastionIp, err := os.OpenFile(bastionIpFilename, os.O_CREATE|os.O_RDWR, filePermReadWrite)
	if err != nil {
		return fmt.Errorf("failed to open bastion IP file: %w", err)
	}
	defer fileBastionIp.Close()

	if _, err := fileBastionIp.Write([]byte(ipAddress)); err != nil {
		return fmt.Errorf("failed to write IP address to file: %w", err)
	}

	return nil
}

func removeCommentLines(input string) string {
	var builder strings.Builder
	builder.Grow(len(input)) // Pre-allocate capacity

	for _, line := range strings.Split(input, "\n") {
		if !strings.HasPrefix(line, "#") {
			if builder.Len() > 0 {
				builder.WriteByte('\n')
			}
			builder.WriteString(line)
		}
	}

	return builder.String()
}

func keyscanServer(ctx context.Context, ipAddress string, silent bool) ([]byte, error) {
	var (
		outb []byte
		outs string
		err  error
	)

	backoff := wait.Backoff{
		Duration: 1 * time.Second,
		Factor:   1.1,
		Cap:      leftInContext(ctx),
		Steps:    math.MaxInt32,
	}

	err = wait.ExponentialBackoffWithContext(ctx, backoff, func(context.Context) (bool, error) {
		var (
			err2 error
		)

		outb, err2 = runSplitCommandNoErr([]string{
			"ssh-keyscan",
			ipAddress,
		},
			silent)
		outs = strings.TrimSpace(string(outb))
		log.Debugf("keyscanServer: outs = %s", outs)
		if err2 != nil {
			return false, nil
		}

		return true, nil
	})

	if err == nil {
		// Get rid of the comment lines generated by ssh-keyscan
		outLines := removeCommentLines(outs)
		outb = []byte(outLines)
	}

	return outb, err
}

func dnsForServer(ctx context.Context, cloudName string, apiKey string, bastionName string, domainName string) error {
	var (
		server       servers.Server
		ipAddress    string
		cisServiceID string
		crnstr       string
		zoneID       string
		dnsService   *dnsrecordsv1.DnsRecordsV1
		err          error
	)

	server, err = findServer(ctx, cloudName, bastionName)
	if err != nil {
		return err
	}
//	log.Debugf("server = %+v", server)

	_, ipAddress, err = findIpAddress(server)
	if err != nil {
		return err
	}
	if ipAddress == "" {
		return fmt.Errorf("ip address is empty for server %s", server.Name)
	}

	cisServiceID, _, err = getServiceInfo(ctx, apiKey, "internet-svcs", "")
	if err != nil {
		log.Errorf("getServiceInfo returns %v", err)
		return err
	}
	log.Debugf("dnsForServer: cisServiceID = %s", cisServiceID)

	crnstr, zoneID, err = getDomainCrn(ctx, apiKey, cisServiceID, domainName)
	log.Debugf("dnsForServer: crnstr = %s, zoneID = %s, err = %+v", crnstr, zoneID, err)
	if err != nil {
		log.Errorf("getDomainCrn returns %v", err)
		return err
	}

	dnsService, err = loadDnsServiceAPI(apiKey, crnstr, zoneID)
	if err != nil {
		log.Errorf("dnsForServer: loadDnsServiceAPI returns %v", err)
		return err
	}
	log.Debugf("dnsForServer: dnsService = %+v", dnsService)

	err = createOrDeletePublicDNSRecord(ctx,
		dnsrecordsv1.CreateDnsRecordOptions_Type_A,
		fmt.Sprintf("api.%s.%s", bastionName, domainName),
		ipAddress,
		true,
		dnsService)
	if err != nil {
		log.Errorf("dnsForServer: createOrDeletePublicDNSRecord(1) returns %v", err)
		return err
	}

	err = createOrDeletePublicDNSRecord(ctx,
		dnsrecordsv1.CreateDnsRecordOptions_Type_A,
		fmt.Sprintf("api-int.%s.%s", bastionName, domainName),
		ipAddress,
		true,
		dnsService)
	if err != nil {
		log.Errorf("dnsForServer: createOrDeletePublicDNSRecord(2) returns %v", err)
		return err
	}

	err = createOrDeletePublicDNSRecord(ctx,
		dnsrecordsv1.CreateDnsRecordOptions_Type_Cname,
		fmt.Sprintf("*.apps.%s.%s", bastionName, domainName),
		fmt.Sprintf("api.%s.%s", bastionName, domainName),
		true,
		dnsService)
	if err != nil {
		log.Errorf("dnsForServer: createOrDeletePublicDNSRecord(3) returns %v", err)
		return err
	}

	return nil
}

func leftInContext(ctx context.Context) time.Duration {
	deadline, ok := ctx.Deadline()
	if !ok {
		return math.MaxInt64
	}

	duration := time.Until(deadline)

	return duration
}
