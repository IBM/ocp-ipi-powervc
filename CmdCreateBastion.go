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
)

const (
	bastionIpFilename = "/tmp/bastionIp"
	defaultAvailZone  = "s1022"
	maxSSHRetries     = 10
	sshRetryDelay     = 15 * time.Second
	filePermReadWrite = 0644
	sshUser           = "cloud-user"
)

// ============================================================================
// SSH Connection Management
// ============================================================================

// sshConfig holds SSH connection parameters
type sshConfig struct {
	Host       string
	User       string
	KeyPath    string
	MaxRetries int
	RetryDelay time.Duration
}

// newSSHConfig creates SSH configuration with defaults.
// It sets the user to "cloud-user" and configures retry parameters.
func newSSHConfig(host, keyPath string) *sshConfig {
	return &sshConfig{
		Host:       host,
		User:       sshUser,
		KeyPath:    keyPath,
		MaxRetries: maxSSHRetries,
		RetryDelay: sshRetryDelay,
	}
}

// waitForSSHReady waits for SSH to become available on the server.
// It retries up to MaxRetries times with RetryDelay between attempts.
// Returns an error if SSH doesn't become ready or if permission is denied.
func waitForSSHReady(ctx context.Context, cfg *sshConfig) error {
	log.Debugf("Waiting for SSH to be ready on %s", cfg.Host)

	for i := 0; i < cfg.MaxRetries; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		outb, _ := runSplitCommand2([]string{
			"ssh",
			"-i", cfg.KeyPath,
			fmt.Sprintf("%s@%s", cfg.User, cfg.Host),
			"echo", "ready",
		})

		outs := strings.TrimSpace(string(outb))
		log.Debugf("SSH check attempt %d/%d: %q", i+1, cfg.MaxRetries, outs)

		if outs == "ready" {
			log.Debugf("SSH is ready on %s", cfg.Host)
			return nil
		}

		if strings.Contains(outs, "Permission denied") {
			return fmt.Errorf("SSH publickey permission denied for %s", cfg.Host)
		}

		if i < cfg.MaxRetries-1 {
			time.Sleep(cfg.RetryDelay)
		}
	}

	return fmt.Errorf("SSH not ready after %d attempts on %s", cfg.MaxRetries, cfg.Host)
}

// execSSHCommand executes a command via SSH.
// It returns the trimmed output and any error encountered.
func execSSHCommand(cfg *sshConfig, command []string) (string, error) {
	args := []string{
		"ssh",
		"-i", cfg.KeyPath,
		fmt.Sprintf("%s@%s", cfg.User, cfg.Host),
	}
	args = append(args, command...)

	outb, err := runSplitCommand2(args)
	return strings.TrimSpace(string(outb)), err
}

// execSSHSudoCommand executes a command with sudo via SSH.
// The command is prefixed with "sudo" automatically.
func execSSHSudoCommand(cfg *sshConfig, command []string) (string, error) {
	sudoCmd := append([]string{"sudo"}, command...)
	return execSSHCommand(cfg, sudoCmd)
}

// ============================================================================
// HAProxy Package Management
// ============================================================================

// isHAProxyInstalled checks if HAProxy is installed on the remote server.
// It uses rpm -q to query the package database.
func isHAProxyInstalled(cfg *sshConfig) (bool, error) {
	log.Debugf("Checking if HAProxy is installed on %s", cfg.Host)

	output, err := execSSHCommand(cfg, []string{"rpm", "-q", haproxyPackageName})

	// rpm -q returns exit code 1 if package is not installed
	var exitError *exec.ExitError
	if errors.As(err, &exitError) && exitError.ExitCode() == 1 {
		if output == fmt.Sprintf("package %s is not installed", haproxyPackageName) {
			log.Debugf("HAProxy is not installed")
			return false, nil
		}
	}

	if err != nil {
		return false, fmt.Errorf("failed to check HAProxy installation: %w", err)
	}

	log.Debugf("HAProxy is installed: %s", output)
	return true, nil
}

// installHAProxy installs HAProxy package on the remote server using dnf.
func installHAProxy(cfg *sshConfig) error {
	log.Debugf("Installing HAProxy on %s", cfg.Host)

	output, err := execSSHSudoCommand(cfg, []string{
		"dnf", "install", "-y", haproxyPackageName,
	})

	if err != nil {
		return fmt.Errorf("failed to install HAProxy: %w (output: %s)", err, output)
	}

	log.Debugf("HAProxy installed successfully")
	return nil
}

// ensureHAProxyInstalled ensures HAProxy is installed, installing if necessary.
// It checks if HAProxy is installed and installs it if not found.
func ensureHAProxyInstalled(cfg *sshConfig) error {
	installed, err := isHAProxyInstalled(cfg)
	if err != nil {
		return err
	}

	if !installed {
		return installHAProxy(cfg)
	}

	return nil
}

// ============================================================================
// HAProxy Configuration Management
// ============================================================================

// getFilePermissions retrieves file permissions in octal format using stat.
// Returns a string like "644" or "755".
func getFilePermissions(cfg *sshConfig, filePath string) (string, error) {
	output, err := execSSHSudoCommand(cfg, []string{
		"stat", "-c", "%a", filePath,
	})

	if err != nil {
		return "", fmt.Errorf("failed to get permissions for %s: %w", filePath, err)
	}

	return output, nil
}

// setFilePermissions sets file permissions using chmod.
// The perms parameter should be in octal format (e.g., "644", "755").
func setFilePermissions(cfg *sshConfig, filePath, perms string) error {
	log.Debugf("Setting permissions %s on %s", perms, filePath)

	_, err := execSSHSudoCommand(cfg, []string{
		"chmod", perms, filePath,
	})

	if err != nil {
		return fmt.Errorf("failed to set permissions on %s: %w", filePath, err)
	}

	return nil
}

// ensureHAProxyConfigPermissions ensures HAProxy config has correct permissions.
// It checks current permissions and updates them if they don't match the expected value.
func ensureHAProxyConfigPermissions(cfg *sshConfig) error {
	currentPerms, err := getFilePermissions(cfg, haproxyConfigPath)
	if err != nil {
		return err
	}

	log.Debugf("Current HAProxy config permissions: %s", currentPerms)

	if currentPerms != haproxyConfigPerms {
		log.Debugf("Updating HAProxy config permissions from %s to %s", 
			currentPerms, haproxyConfigPerms)
		return setFilePermissions(cfg, haproxyConfigPath, haproxyConfigPerms)
	}

	log.Debugf("HAProxy config permissions are correct")
	return nil
}

// ============================================================================
// SELinux Configuration
// ============================================================================

// getSELinuxBool retrieves the value of an SELinux boolean.
// It parses the output format: "boolean_name --> on" or "boolean_name --> off".
func getSELinuxBool(cfg *sshConfig, boolName string) (bool, error) {
	output, err := execSSHSudoCommand(cfg, []string{
		"getsebool", boolName,
	})

	if err != nil {
		return false, fmt.Errorf("failed to get SELinux boolean %s: %w", boolName, err)
	}

	// Output format: "haproxy_connect_any --> on" or "haproxy_connect_any --> off"
	isOn := strings.HasSuffix(output, "--> on")
	log.Debugf("SELinux boolean %s is %s", boolName, output)

	return isOn, nil
}

// setSELinuxBool sets an SELinux boolean persistently (-P flag).
// The value is set to "1" for true and "0" for false.
func setSELinuxBool(cfg *sshConfig, boolName string, value bool) error {
	valueStr := "0"
	if value {
		valueStr = "1"
	}

	log.Debugf("Setting SELinux boolean %s to %s", boolName, valueStr)

	_, err := execSSHSudoCommand(cfg, []string{
		"setsebool", "-P", fmt.Sprintf("%s=%s", boolName, valueStr),
	})

	if err != nil {
		return fmt.Errorf("failed to set SELinux boolean %s: %w", boolName, err)
	}

	return nil
}

// ensureHAProxySELinux ensures HAProxy SELinux settings are correct.
// It enables the haproxy_connect_any boolean if not already enabled.
func ensureHAProxySELinux(cfg *sshConfig) error {
	isEnabled, err := getSELinuxBool(cfg, haproxySelinuxSetting)
	if err != nil {
		return err
	}

	if !isEnabled {
		log.Debugf("Enabling SELinux boolean %s", haproxySelinuxSetting)
		return setSELinuxBool(cfg, haproxySelinuxSetting, true)
	}

	log.Debugf("SELinux boolean %s is already enabled", haproxySelinuxSetting)
	return nil
}

// ============================================================================
// Systemd Service Management
// ============================================================================

// systemctlCommand executes a systemctl command via SSH.
// Common actions include: enable, disable, start, stop, restart, status.
func systemctlCommand(cfg *sshConfig, action, service string) error {
	log.Debugf("Executing systemctl %s %s", action, service)

	_, err := execSSHSudoCommand(cfg, []string{
		"systemctl", action, service,
	})

	if err != nil {
		return fmt.Errorf("failed to %s %s: %w", action, service, err)
	}

	return nil
}

// enableService enables a systemd service to start on boot.
func enableService(cfg *sshConfig, service string) error {
	return systemctlCommand(cfg, "enable", service)
}

// startService starts a systemd service immediately.
func startService(cfg *sshConfig, service string) error {
	return systemctlCommand(cfg, "start", service)
}

// enableAndStartHAProxy enables and starts the HAProxy service.
// It first enables the service to start on boot, then starts it immediately.
func enableAndStartHAProxy(cfg *sshConfig) error {
	if err := enableService(cfg, haproxyServiceName); err != nil {
		return err
	}

	return startService(cfg, haproxyServiceName)
}

// ============================================================================
// HAProxy Setup Orchestration
// ============================================================================

// setupHAProxyOnServer performs complete HAProxy setup on the bastion server.
// It executes the following steps in order:
//  1. Add server to known_hosts
//  2. Wait for SSH to be ready
//  3. Ensure HAProxy is installed
//  4. Configure HAProxy file permissions
//  5. Configure SELinux for HAProxy
//  6. Enable and start HAProxy service
func setupHAProxyOnServer(ctx context.Context, ipAddress, bastionRsa string) error {
	cfg := newSSHConfig(ipAddress, bastionRsa)

	// Step 1: Add server to known_hosts
	if err := addServerKnownHosts(ctx, ipAddress); err != nil {
		return fmt.Errorf("failed to add server to known_hosts: %w", err)
	}

	// Step 2: Wait for SSH to be ready
	if err := waitForSSHReady(ctx, cfg); err != nil {
		return fmt.Errorf("SSH not ready: %w", err)
	}

	// Step 3: Ensure HAProxy is installed
	if err := ensureHAProxyInstalled(cfg); err != nil {
		return fmt.Errorf("failed to ensure HAProxy installation: %w", err)
	}

	// Step 4: Configure HAProxy file permissions
	if err := ensureHAProxyConfigPermissions(cfg); err != nil {
		return fmt.Errorf("failed to configure HAProxy permissions: %w", err)
	}

	// Step 5: Configure SELinux for HAProxy
	if err := ensureHAProxySELinux(cfg); err != nil {
		return fmt.Errorf("failed to configure HAProxy SELinux: %w", err)
	}

	// Step 6: Enable and start HAProxy service
	if err := enableAndStartHAProxy(cfg); err != nil {
		return fmt.Errorf("failed to start HAProxy service: %w", err)
	}

	log.Debugf("HAProxy setup completed successfully on %s", ipAddress)
	return nil
}

// getServerIPAddress extracts and validates the IP address from a server.
// It returns an error if the IP address cannot be found or is empty.
func getServerIPAddress(server servers.Server) (string, error) {
	_, ipAddress, err := findIpAddress(server)
	if err != nil {
		return "", fmt.Errorf("failed to get IP address: %w", err)
	}

	if ipAddress == "" {
		return "", fmt.Errorf("IP address is empty for server %s", server.Name)
	}

	return ipAddress, nil
}

// BastionConfig holds all configuration for bastion creation.
// It supports two modes of operation:
//   1. Local setup: Requires BastionRsa for SSH access
//   2. Remote setup: Requires ServerIP to delegate setup to another server
type BastionConfig struct {
	// OpenStack Configuration
	Clouds      cloudFlags // OpenStack cloud name from clouds.yaml (required only 1 entry allowed)
	NetworkName string     // OpenStack network name for bastion VM (required)

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
	if len(c.Clouds) == 0 {
		validationErrors = append(validationErrors, fmt.Errorf("cloud: field is required"))
	}
	if len(c.Clouds) > 1 {
		validationErrors = append(validationErrors, fmt.Errorf("cloud: only one cloud is allowed"))
	}
	if len(c.Clouds) == 1 && c.Clouds[0] == "" {
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

	return fmt.Sprintf("BastionConfig{Clouds=%q, Name=%q, Flavor=%q, Image=%q, "+
		"Network=%q, SSHKey=%q, Domain=%q, HAProxy=%v, ServerIP=%q, RSA=%s, Debug=%v}",
		c.Clouds, c.BastionName, c.FlavorName, c.ImageName,
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

// parseBastionFlags extracts and validates flags into a BastionConfig.
// It parses command-line flags, populates the configuration, and validates it.
// Returns an error if flag parsing fails or configuration is invalid.
func parseBastionFlags(flags *flag.FlagSet, args []string) (*BastionConfig, error) {
	config := NewBastionConfig() // Use constructor with defaults

	// Define flags
	var clouds cloudFlags
	flags.Var(&clouds, "cloud", "Cloud name to use in clouds.yaml")
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
	config.Clouds = clouds
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

// createBastionCommand is the main entry point for the create-bastion command.
// It orchestrates the entire bastion creation process:
//  1. Parse and validate configuration
//  2. Initialize logging
//  3. Clean up previous bastion IP file
//  4. Ensure server exists (create if needed)
//  5. Setup bastion server (HAProxy, DNS)
//  6. Write bastion IP to file
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
	if config.ShouldDebug {
		log.Debugf("Debug mode enabled")
	}

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
	if err := writeBastionIP(ctx, config, config.BastionName); err != nil {
		return fmt.Errorf("failed to write bastion IP: %w", err)
	}

	return nil
}

// cleanupBastionIPFile removes the bastion IP file if it exists.
// It returns an error only if the file exists but cannot be removed.
func cleanupBastionIPFile() error {
	err := os.Remove(bastionIpFilename)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove bastion IP file: %w", err)
	}
	return nil
}

// ensureServerExists checks if server exists and creates it if needed.
// If the server already exists, it returns immediately.
// If the server doesn't exist, it creates it and verifies the creation.
func ensureServerExists(ctx context.Context, config *BastionConfig) error {
	_, err := findServer(ctx, config.Clouds, config.BastionName)
	if err == nil {
		log.Debugf("Server %s already exists", config.BastionName)
		return nil
	}

	// Check if error is "server not found" - using string prefix check as errors.Is doesn't work
	// with the current error wrapping in findServer
	if !strings.HasPrefix(strings.ToLower(err.Error()), "could not find server named") {
		return fmt.Errorf("failed to find server: %w", err)
	}

	// Server doesn't exist, create it
	fmt.Printf("Server %s not found, creating...\n", config.BastionName)

	if err := createServer(ctx,
		config.Clouds[0],
		config.FlavorName,
		config.ImageName,
		config.NetworkName,
		config.SshKeyName,
		config.BastionName,
		nil,
	); err != nil {
		return fmt.Errorf("failed to create server: %w", err)
	}

	fmt.Println("Server created successfully!")

	// Verify server was created
	if _, err := findServer(ctx, config.Clouds, config.BastionName); err != nil {
		return fmt.Errorf("server verification failed after creation: %w", err)
	}

	return nil
}

// setupBastion configures the bastion server either remotely or locally.
// The setup mode is determined by the BastionConfig:
//  - Remote setup: delegates to another server via sendCreateBastion
//  - Local setup: performs setup directly via SSH
func setupBastion(ctx context.Context, config *BastionConfig) error {
	if config.IsRemoteSetup() {
		fmt.Println("Setting up bastion remotely...")
		if err := sendCreateBastion(config.ServerIP, config.Clouds[0], config.BastionName, config.DomainName); err != nil {
			return fmt.Errorf("remote setup failed: %w", err)
		}
		return nil
	}

	fmt.Println("Setting up bastion locally...")
	if err := setupBastionServer(ctx, config.EnableHAProxy, config.Clouds, config.BastionName, config.DomainName, config.BastionRsa); err != nil {
		return fmt.Errorf("local setup failed: %w", err)
	}
	return nil
}

// createServer creates a new OpenStack server with the specified configuration.
// It handles resource lookup, port creation, and server provisioning.
func createServer(ctx context.Context, cloudName, flavorName, imageName, networkName, sshKeyName, bastionName string, userData []byte) error {
	var (
		flavor           flavors.Flavor
		image            images.Image
		network          networks.Network
		sshKeyPair       keypairs.KeyPair
		newServer        *servers.Server
		err              error
	)

	// Step 1: Lookup OpenStack resources
	flavor, err = findFlavor(ctx, cloudName, flavorName)
	if err != nil {
		return fmt.Errorf("failed to find flavor %q: %w", flavorName, err)
	}
	log.Debugf("createServer: flavor = %+v", flavor)

	image, err = findImage(ctx, cloudName, imageName)
	if err != nil {
		return fmt.Errorf("failed to find image %q: %w", imageName, err)
	}
	log.Debugf("createServer: image = %+v", image)

	network, err = findNetwork(ctx, cloudName, networkName)
	if err != nil {
		return fmt.Errorf("failed to find network %q: %w", networkName, err)
	}
	log.Debugf("createServer: network = %+v", network)

	if sshKeyName != "" {
		sshKeyPair, err = findKeyPair(ctx, cloudName, sshKeyName)
		if err != nil {
			return fmt.Errorf("failed to find SSH keypair %q: %w", sshKeyName, err)
		}
		log.Debugf("createServer: sshKeyPair = %+v", sshKeyPair)
	}

	// Step 2: Create network port
	port, err := createNetworkPort(ctx, cloudName, bastionName, network.ID)
	if err != nil {
		return fmt.Errorf("failed to create network port: %w", err)
	}
	log.Debugf("createServer: port.ID = %v", port.ID)

	// Step 3: Create server
	newServer, err = createServerInstance(ctx, cloudName, bastionName, flavor.ID, image.ID, port.ID, sshKeyName, sshKeyPair.Name, userData)
	if err != nil {
		return fmt.Errorf("failed to create server instance: %w", err)
	}
	log.Debugf("createServer: newServer = %+v", newServer)

	// Step 4: Wait for server to be ready
	if err := waitForServer(ctx, cloudName, bastionName); err != nil {
		return fmt.Errorf("server creation timeout: %w", err)
	}

	return nil
}

// createNetworkPort creates a network port for the server.
// The port is named "<bastionName>-port" and attached to the specified network.
func createNetworkPort(ctx context.Context, cloudName, bastionName, networkID string) (*ports.Port, error) {
	connNetwork, err := NewServiceClient(ctx, "network", DefaultClientOpts(cloudName))
	if err != nil {
		return nil, fmt.Errorf("failed to create network client: %w", err)
	}

	portCreateOpts := ports.CreateOpts{
		Name:        fmt.Sprintf("%s-port", bastionName),
		NetworkID:   networkID,
		Description: "Bastion server network port",
	}

	port, err := ports.Create(ctx, connNetwork, portCreateOpts).Extract()
	if err != nil {
		return nil, fmt.Errorf("failed to create port: %w", err)
	}

	return port, nil
}

// createServerInstance creates the actual server instance.
// If sshKeyName is empty, the server is created without an SSH key.
func createServerInstance(ctx context.Context, cloudName, bastionName, flavorID, imageID, portID, sshKeyName, sshKeyPairName string, userData []byte) (*servers.Server, error) {
	connCompute, err := NewServiceClient(ctx, "compute", DefaultClientOpts(cloudName))
	if err != nil {
		return nil, fmt.Errorf("failed to create compute client: %w", err)
	}

	serverCreateOpts := servers.CreateOpts{
		AvailabilityZone: defaultAvailZone,
		FlavorRef:        flavorID,
		ImageRef:         imageID,
		Name:             bastionName,
		Networks:         []servers.Network{{Port: portID}},
		UserData:         userData,
	}

	var newServer *servers.Server
	if sshKeyName != "" {
		newServer, err = servers.Create(ctx,
			connCompute,
			keypairs.CreateOptsExt{
				CreateOptsBuilder: serverCreateOpts,
				KeyName:           sshKeyPairName,
			},
			nil).Extract()
	} else {
		newServer, err = servers.Create(ctx, connCompute, serverCreateOpts, nil).Extract()
	}

	if err != nil {
		return nil, fmt.Errorf("failed to create server: %w", err)
	}

	return newServer, nil
}

// addServerKnownHosts adds the server's SSH host keys to known_hosts file.
// It removes any existing host keys for the IP address before adding new ones.
func addServerKnownHosts(ctx context.Context, ipAddress string) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get user home directory: %w", err)
	}

	knownHostsPath := path.Join(homeDir, ".ssh/known_hosts")
	log.Debugf("addServerKnownHosts: known_hosts path = %s", knownHostsPath)

	// Remove old host key if it exists
	if err := removeHostKey(knownHostsPath, ipAddress); err != nil {
		log.Debugf("Warning: failed to remove old host key: %v", err)
	}

	// Scan new host keys
	hostKeys, err := keyscanServer(ctx, ipAddress, false)
	if err != nil {
		return fmt.Errorf("failed to scan SSH keys from %s: %w", ipAddress, err)
	}

	// Append new host keys to known_hosts
	if err := appendToFile(knownHostsPath, hostKeys); err != nil {
		return fmt.Errorf("failed to update known_hosts: %w", err)
	}

	log.Debugf("Successfully added host keys for %s", ipAddress)
	return nil
}

// removeHostKey removes a host's key from known_hosts file.
// This function intentionally ignores errors as the host key may not exist.
func removeHostKey(knownHostsPath, ipAddress string) error {
	outb, _ := runSplitCommand2([]string{
		"ssh-keygen",
		"-f", knownHostsPath,
		"-R", ipAddress,
	})
	log.Debugf("removeHostKey: output = %q", strings.TrimSpace(string(outb)))
	return nil
}

// appendToFile appends data to a file.
// It returns an error if the file cannot be opened, written to, or if the write is incomplete.
func appendToFile(filePath string, data []byte) error {
	file, err := os.OpenFile(filePath, os.O_APPEND|os.O_RDWR, filePermReadWrite)
	if err != nil {
		return fmt.Errorf("failed to open file %q: %w", filePath, err)
	}
	defer file.Close()

	n, err := file.Write(data)
	if err != nil {
		return fmt.Errorf("failed to write to file: %w", err)
	}

	if n != len(data) {
		return fmt.Errorf("incomplete write: wrote %d of %d bytes", n, len(data))
	}

	return nil
}

// setupBastionServer orchestrates bastion server configuration.
// It performs the following steps:
//  1. Find the server in OpenStack
//  2. Extract and validate the server's IP address
//  3. Setup HAProxy if enabled
//  4. Configure DNS records if IBM Cloud API key is available
func setupBastionServer(ctx context.Context, enableHAProxy bool, clouds cloudFlags, serverName, domainName, bastionRsa string) error {
	// Step 1: Find the server
	server, err := findServer(ctx, clouds, serverName)
	if err != nil {
		return fmt.Errorf("failed to find server: %w", err)
	}
	log.Debugf("Found server: %+v", server)

	// Step 2: Get server IP address
	ipAddress, err := getServerIPAddress(server)
	if err != nil {
		return err
	}
	log.Debugf("Server IP address: %s", ipAddress)
	log.Debugf("Bastion RSA key: %s", bastionRsa)

	fmt.Printf("Setting up server %s...\n", server.Name)

	// Step 3: Setup HAProxy if enabled
	if enableHAProxy {
		if err := setupHAProxyOnServer(ctx, ipAddress, bastionRsa); err != nil {
			return fmt.Errorf("failed to setup HAProxy: %w", err)
		}
	}

	// Step 4: Setup DNS if API key is available
	apiKey := os.Getenv("IBMCLOUD_API_KEY")
	if apiKey != "" {
		if err := dnsForServer(ctx, clouds, apiKey, serverName, domainName); err != nil {
			return fmt.Errorf("failed to setup DNS: %w", err)
		}
	} else {
		fmt.Println("Warning: IBMCLOUD_API_KEY not set. Ensure DNS is supported via another method.")
	}

	return nil
}

// writeBastionIP writes the bastion server's IP address to a file.
// The IP address is written to the file specified by bastionIpFilename constant.
func writeBastionIP(ctx context.Context, config *BastionConfig, serverName string) error {
	server, err := findServer(ctx, config.Clouds, serverName)
	if err != nil {
		return fmt.Errorf("failed to find server %q: %w", serverName, err)
	}
	log.Debugf("writeBastionIP: server = %+v", server)

	ipAddress, err := getServerIPAddress(server)
	if err != nil {
		return fmt.Errorf("failed to get IP address: %w", err)
	}
	log.Debugf("writeBastionIP: ipAddress = %s", ipAddress)

	if err := os.WriteFile(bastionIpFilename, []byte(ipAddress), filePermReadWrite); err != nil {
		return fmt.Errorf("failed to write bastion IP to %q: %w", bastionIpFilename, err)
	}

	return nil
}

// removeCommentLines filters out lines starting with '#' from input text.
// Empty lines are also removed. The function pre-allocates capacity for efficiency.
func removeCommentLines(input string) string {
	var builder strings.Builder
	builder.Grow(len(input)) // Pre-allocate capacity

	lines := strings.Split(input, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			if builder.Len() > 0 {
				builder.WriteByte('\n')
			}
			builder.WriteString(line)
		}
	}

	return builder.String()
}

// dnsRecord represents a DNS record to be created
type dnsRecord struct {
	recordType string
	name       string
	content    string
}

// dnsForServer configures DNS records for the bastion server.
// It creates three DNS records:
//  1. A record for api.<bastionName>.<domainName>
//  2. A record for api-int.<bastionName>.<domainName>
//  3. CNAME record for *.apps.<bastionName>.<domainName> pointing to api
func dnsForServer(ctx context.Context, clouds cloudFlags, apiKey, bastionName, domainName string) error {
	// Step 1: Get server IP address
	server, err := findServer(ctx, clouds, bastionName)
	if err != nil {
		return fmt.Errorf("failed to find server %q: %w", bastionName, err)
	}

	ipAddress, err := getServerIPAddress(server)
	if err != nil {
		return fmt.Errorf("failed to get IP address: %w", err)
	}

	// Step 2: Get IBM Cloud DNS service information
	cisServiceID, _, err := getServiceInfo(ctx, apiKey, "internet-svcs", "")
	if err != nil {
		return fmt.Errorf("failed to get CIS service info: %w", err)
	}
	log.Debugf("dnsForServer: cisServiceID = %s", cisServiceID)

	crnstr, zoneID, err := getDomainCrn(ctx, apiKey, cisServiceID, domainName)
	if err != nil {
		return fmt.Errorf("failed to get domain CRN for %q: %w", domainName, err)
	}
	log.Debugf("dnsForServer: crnstr = %s, zoneID = %s", crnstr, zoneID)

	dnsService, err := loadDnsServiceAPI(apiKey, crnstr, zoneID)
	if err != nil {
		return fmt.Errorf("failed to load DNS service API: %w", err)
	}
	log.Debugf("dnsForServer: dnsService initialized")

	// Step 3: Create DNS records
	records := []dnsRecord{
		{
			recordType: dnsrecordsv1.CreateDnsRecordOptions_Type_A,
			name:       fmt.Sprintf("api.%s.%s", bastionName, domainName),
			content:    ipAddress,
		},
		{
			recordType: dnsrecordsv1.CreateDnsRecordOptions_Type_A,
			name:       fmt.Sprintf("api-int.%s.%s", bastionName, domainName),
			content:    ipAddress,
		},
		{
			recordType: dnsrecordsv1.CreateDnsRecordOptions_Type_Cname,
			name:       fmt.Sprintf("*.apps.%s.%s", bastionName, domainName),
			content:    fmt.Sprintf("api.%s.%s", bastionName, domainName),
		},
	}

	for i, record := range records {
		if err := createOrDeletePublicDNSRecord(ctx, record.recordType, record.name, record.content, true, dnsService); err != nil {
			return fmt.Errorf("failed to create DNS record %d (%s): %w", i+1, record.name, err)
		}
		log.Debugf("Created DNS record: %s -> %s", record.name, record.content)
	}

	return nil
}
