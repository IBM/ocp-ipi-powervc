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
	"path/filepath"
	"strings"
	"time"

	// Third-party imports - gophercloud (grouped by functionality)
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/flavors"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/keypairs"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"
	"github.com/gophercloud/gophercloud/v2/openstack/image/v2/images"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/ports"

	// Third-party imports - IBM SDK
	"github.com/IBM/networking-go-sdk/dnsrecordsv1"
)

const (
	defaultAvailZone     = "s1022"
	maxSSHRetries        = 10
	sshRetryDelay        = 15 * time.Second
	filePermReadWrite    = 0644
	sshUser              = "cloud-user"
	cleanupPortTimeout   = 30 * time.Second
	cleanupServerTimeout = 60 * time.Second
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

		outs, err := sshAccessSuccess(cfg)
		log.Debugf("sshAccessSuccess: err = %v", err)
		log.Debugf("waitForSSHReady: SSH check attempt %d/%d: %q", i+1, cfg.MaxRetries, outs)

		if err == nil {
			return err
		}

		shouldContinue := false
		if err != nil {
			if strings.Contains(err.Error(), "No route to host") ||
			   strings.Contains(err.Error(), "Connection timed out") {
				shouldContinue = true
			}
		}

		if !shouldContinue {
			return err
		}

		if i < cfg.MaxRetries-1 {
			log.Debugf("Sleeping for %s seconds", cfg.RetryDelay)
			time.Sleep(cfg.RetryDelay)
		}
	}

	return fmt.Errorf("SSH not ready after %d attempts on %s", cfg.MaxRetries, cfg.Host)
}

// sshAccessSuccess tests SSH connectivity to a remote host.
// It attempts a single SSH connection using the provided configuration and executes
// a simple "echo ready" command to verify that SSH access is working properly.
//
// The function uses the following SSH options:
//   - BatchMode=yes: Disables password prompts and interactive authentication
//   - ConnectTimeout=30: Sets a 30-second timeout for the connection attempt
//   - StrictHostKeyChecking=no: Disables host key verification (accepts unknown hosts)
//
// Parameters:
//   - cfg: SSH configuration containing host, user, key path, and retry settings
//
// Returns:
//   - string: The trimmed output from the SSH command
//   - error: nil if SSH is ready (output is "ready"), otherwise an error describing the failure
//
// Error cases:
//   - Returns "SSH publickey permission denied" error if authentication fails
//   - Returns "unknown ssh response" error if the output is not "ready" and no permission error
//
// Note: This function is typically called by waitForSSH in a retry loop to handle
// transient connection failures during host initialization.
func sshAccessSuccess(cfg *sshConfig) (string, error) {
	outb, _ := runSplitCommand2([]string{
		"ssh",
		"-o", "IdentitiesOnly=yes",
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=30",
		"-o", "StrictHostKeyChecking=no",
		"-i", cfg.KeyPath,
		fmt.Sprintf("%s@%s", cfg.User, cfg.Host),
		"echo", "bastion-is-ready",
	})

	outs := strings.TrimSpace(string(outb))

	if strings.Contains(outs, "bastion-is-ready") {
		log.Debugf("SSH is ready on %s", cfg.Host)
		return outs, nil
	}

	if strings.Contains(outs, "Permission denied") {
		return outs, fmt.Errorf("SSH publickey permission denied for %s", cfg.Host)
	}

	return outs, fmt.Errorf("unknown ssh response: %v", outs)
}

// execSSHCommand executes a command via SSH with context support.
// It returns the trimmed output and any error encountered.
// The command will be cancelled if the context is cancelled or times out.
func execSSHCommand(ctx context.Context, cfg *sshConfig, command []string) (string, error) {
	args := []string{
		"ssh",
		"-o", "IdentitiesOnly=yes",
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=30",
		"-o", "StrictHostKeyChecking=no",
		"-i", cfg.KeyPath,
		fmt.Sprintf("%s@%s", cfg.User, cfg.Host),
	}
	args = append(args, command...)
	log.Debugf("execSSHCommand: args = %+v", args)

	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	outb, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(outb)), err
}

// execSSHSudoCommand executes a command with sudo via SSH with context support.
// The command is prefixed with "sudo" automatically.
// The command will be cancelled if the context is cancelled or times out.
func execSSHSudoCommand(ctx context.Context, cfg *sshConfig, command []string) (string, error) {
	sudoCmd := append([]string{"sudo"}, command...)
	return execSSHCommand(ctx, cfg, sudoCmd)
}

// ============================================================================
// HAProxy Package Management
// ============================================================================

// isHAProxyInstalled checks if HAProxy is installed on the remote server.
// It uses rpm -q to query the package database.
// The operation respects context cancellation and timeout.
func isHAProxyInstalled(ctx context.Context, cfg *sshConfig) (bool, error) {
	log.Debugf("Checking if HAProxy is installed on %s", cfg.Host)

	output, err := execSSHCommand(ctx, cfg, []string{"rpm", "-q", haproxyPackageName})

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
// The operation respects context cancellation and timeout.
func installHAProxy(ctx context.Context, cfg *sshConfig) error {
	log.Debugf("Installing HAProxy on %s", cfg.Host)

	output, err := execSSHSudoCommand(ctx, cfg, []string{
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
// The operation respects context cancellation and timeout.
func ensureHAProxyInstalled(ctx context.Context, cfg *sshConfig) error {
	installed, err := isHAProxyInstalled(ctx, cfg)
	if err != nil {
		return err
	}

	if !installed {
		return installHAProxy(ctx, cfg)
	}

	return nil
}

// ============================================================================
// HAProxy Configuration Management
// ============================================================================

// getFilePermissions retrieves file permissions in octal format using stat.
// Returns a string like "644" or "755".
// The operation respects context cancellation and timeout.
func getFilePermissions(ctx context.Context, cfg *sshConfig, filePath string) (string, error) {
	output, err := execSSHSudoCommand(ctx, cfg, []string{
		"stat", "-c", "%a", filePath,
	})

	if err != nil {
		return "", fmt.Errorf("failed to get permissions for %s: %w", filePath, err)
	}

	return output, nil
}

// setFilePermissions sets file permissions using chmod.
// The perms parameter should be in octal format (e.g., "644", "755").
// The operation respects context cancellation and timeout.
func setFilePermissions(ctx context.Context, cfg *sshConfig, filePath, perms string) error {
	log.Debugf("Setting permissions %s on %s", perms, filePath)

	_, err := execSSHSudoCommand(ctx, cfg, []string{
		"chmod", perms, filePath,
	})

	if err != nil {
		return fmt.Errorf("failed to set permissions on %s: %w", filePath, err)
	}

	return nil
}

// ensureHAProxyConfigPermissions ensures HAProxy config has correct permissions.
// It checks current permissions and updates them if they don't match the expected value.
// The operation respects context cancellation and timeout.
func ensureHAProxyConfigPermissions(ctx context.Context, cfg *sshConfig) error {
	currentPerms, err := getFilePermissions(ctx, cfg, haproxyConfigPath)
	if err != nil {
		return err
	}

	log.Debugf("Current HAProxy config permissions: %s", currentPerms)

	if currentPerms != haproxyConfigPerms {
		log.Debugf("Updating HAProxy config permissions from %s to %s",
			currentPerms, haproxyConfigPerms)
		return setFilePermissions(ctx, cfg, haproxyConfigPath, haproxyConfigPerms)
	}

	log.Debugf("HAProxy config permissions are correct")
	return nil
}

// ============================================================================
// SELinux Configuration
// ============================================================================

// getSELinuxBool retrieves the value of an SELinux boolean.
// It parses the output format: "boolean_name --> on" or "boolean_name --> off".
// The operation respects context cancellation and timeout.
func getSELinuxBool(ctx context.Context, cfg *sshConfig, boolName string) (bool, error) {
	output, err := execSSHSudoCommand(ctx, cfg, []string{
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
// The operation respects context cancellation and timeout.
func setSELinuxBool(ctx context.Context, cfg *sshConfig, boolName string, value bool) error {
	valueStr := "0"
	if value {
		valueStr = "1"
	}

	log.Debugf("Setting SELinux boolean %s to %s", boolName, valueStr)

	_, err := execSSHSudoCommand(ctx, cfg, []string{
		"setsebool", "-P", fmt.Sprintf("%s=%s", boolName, valueStr),
	})

	if err != nil {
		return fmt.Errorf("failed to set SELinux boolean %s: %w", boolName, err)
	}

	return nil
}

// ensureHAProxySELinux ensures HAProxy SELinux settings are correct.
// It enables the haproxy_connect_any boolean if not already enabled.
// The operation respects context cancellation and timeout.
func ensureHAProxySELinux(ctx context.Context, cfg *sshConfig) error {
	isEnabled, err := getSELinuxBool(ctx, cfg, haproxySelinuxSetting)
	if err != nil {
		return err
	}

	if !isEnabled {
		log.Debugf("Enabling SELinux boolean %s", haproxySelinuxSetting)
		return setSELinuxBool(ctx, cfg, haproxySelinuxSetting, true)
	}

	log.Debugf("SELinux boolean %s is already enabled", haproxySelinuxSetting)
	return nil
}

// ============================================================================
// Systemd Service Management
// ============================================================================

// systemctlCommand executes a systemctl command via SSH.
// Common actions include: enable, disable, start, stop, restart, status.
// The operation respects context cancellation and timeout.
func systemctlCommand(ctx context.Context, cfg *sshConfig, action, service string) error {
	log.Debugf("Executing systemctl %s %s", action, service)

	_, err := execSSHSudoCommand(ctx, cfg, []string{
		"systemctl", action, service,
	})

	if err != nil {
		return fmt.Errorf("failed to %s %s: %w", action, service, err)
	}

	return nil
}

// enableService enables a systemd service to start on boot.
// The operation respects context cancellation and timeout.
func enableService(ctx context.Context, cfg *sshConfig, service string) error {
	return systemctlCommand(ctx, cfg, "enable", service)
}

// startService starts a systemd service immediately.
// The operation respects context cancellation and timeout.
func startService(ctx context.Context, cfg *sshConfig, service string) error {
	return systemctlCommand(ctx, cfg, "start", service)
}

// enableAndStartHAProxy enables and starts the HAProxy service.
// It first enables the service to start on boot, then starts it immediately.
// The operation respects context cancellation and timeout.
func enableAndStartHAProxy(ctx context.Context, cfg *sshConfig) error {
	if err := enableService(ctx, cfg, haproxyServiceName); err != nil {
		return err
	}

	return startService(ctx, cfg, haproxyServiceName)
}

// ============================================================================
// HAProxy Setup Orchestration
// ============================================================================

// setupHAProxyOnServer performs complete HAProxy setup on the bastion server.
// It executes the following steps in order:
//  1. Wait for SSH to be ready
//  2. Add server to known_hosts
//  3. Ensure HAProxy is installed
//  4. Configure HAProxy file permissions
//  5. Configure SELinux for HAProxy
//  6. Enable and start HAProxy service
// The operation respects context cancellation and timeout, checking before each major step.
func setupHAProxyOnServer(ctx context.Context, ipAddress, bastionRsa string) error {
	cfg := newSSHConfig(ipAddress, bastionRsa)

	cfg.MaxRetries = 20
	cfg.RetryDelay = 20 * time.Second

	// Step 1: Wait for SSH to be ready
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("context cancelled before SSH ready check: %w", err)
	}
	if err := waitForSSHReady(ctx, cfg); err != nil {
		return fmt.Errorf("SSH not ready: %w", err)
	}

	// Step 2: Add server to known_hosts
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("context cancelled before adding to known_hosts: %w", err)
	}
	if err := addServerKnownHosts(ctx, ipAddress); err != nil {
		return fmt.Errorf("failed to add server to known_hosts: %w", err)
	}

	// Step 3: Ensure HAProxy is installed
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("context cancelled before HAProxy installation: %w", err)
	}
	if err := ensureHAProxyInstalled(ctx, cfg); err != nil {
		return fmt.Errorf("failed to ensure HAProxy installation: %w", err)
	}

	// Step 4: Configure HAProxy file permissions
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("context cancelled before HAProxy permissions: %w", err)
	}
	if err := ensureHAProxyConfigPermissions(ctx, cfg); err != nil {
		return fmt.Errorf("failed to configure HAProxy permissions: %w", err)
	}

	// Step 5: Configure SELinux for HAProxy
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("context cancelled before HAProxy SELinux: %w", err)
	}
	if err := ensureHAProxySELinux(ctx, cfg); err != nil {
		return fmt.Errorf("failed to configure HAProxy SELinux: %w", err)
	}

	// Step 6: Enable and start HAProxy service
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("context cancelled before HAProxy service start: %w", err)
	}
	if err := enableAndStartHAProxy(ctx, cfg); err != nil {
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
	BastionName      string // Name of the bastion VM (required, alphanumeric/hyphens/underscores only)
	AvailabilityZone string // OpenStack availability zone for VM
	FlavorName       string // OpenStack flavor for VM sizing (required)
	ImageName        string // OpenStack image for VM OS (required)
	SshKeyName       string // OpenStack SSH keypair name (required)

	// Setup Mode (mutually exclusive)
	BastionRsa string // Path to RSA private key for local SSH setup (mutually exclusive with ServerIP)
	ServerIP   string // IP address for remote setup delegation (mutually exclusive with BastionRsa)

	// Output
	BastionIpFile string // The filename containing the IP of the bastion server

	// Optional Configuration
	DomainName    string // DNS domain for bastion records (optional, requires IBMCLOUD_API_KEY)
	EnableHAProxy bool   // Enable HAProxy load balancer (default: true)
	ShouldDebug   bool   // Enable debug logging (default: false)
}

// Validate checks if the configuration is valid and returns detailed errors.
// It performs the following checks:
//   - Required field presence
//   - Field format validation (names, IPs, file paths)
//   - Mutual exclusivity constraints
//   - Resource accessibility (file existence)
func (c *BastionConfig) Validate() error {
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

	// Optional parameters
	if c.AvailabilityZone == "" {
		c.AvailabilityZone = defaultAvailZone
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
		errStrings := make([]string, len(validationErrors))
		for i, e := range validationErrors {
			errStrings[i] = "  - " + e.Error()
		}
		return fmt.Errorf("configuration validation failed:\n%s", strings.Join(errStrings, "\n"))
	}

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
	availabilityZone := flags.String("availabilityZone", defaultAvailZone, "The name of the availability zone")
	flavorName := flags.String("flavorName", "", "The name of the flavor")
	imageName := flags.String("imageName", "", "The name of the image")
	networkName := flags.String("networkName", "", "The name of the network")
	sshKeyName := flags.String("sshKeyName", "", "The name of the SSH keypair")
	domainName := flags.String("domainName", "", "The DNS domain (optional)")
	enableHAProxy := flags.String("enableHAProxy", "true", "Enable HA Proxy daemon")
	serverIP := flags.String("serverIP", "", "The IP address of the server")
	bastionIpFile := flags.String("bastionIpFile", "/tmp/bastionIp", "The filename containing the IP of the bastion server")
	shouldDebug := flags.String("shouldDebug", "false", "Enable debug output")

	// Parse flags
	if err := flags.Parse(args); err != nil {
		return nil, fmt.Errorf("failed to parse flags: %w", err)
	}

	// Populate config
	config.Clouds = clouds
	config.BastionName = *bastionName
	config.BastionRsa = *bastionRsa
	config.AvailabilityZone = *availabilityZone
	config.FlavorName = *flavorName
	config.ImageName = *imageName
	config.NetworkName = *networkName
	config.SshKeyName = *sshKeyName
	config.DomainName = *domainName
	config.ServerIP = *serverIP
	config.BastionIpFile = *bastionIpFile

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
	err := innerCreateBastionCommand(createBastionFlags, args)
	if err != nil {
		fmt.Printf("%+v\n", err)
		if createBastionFlags != nil {
			createBastionFlags.Usage()
		}
	}
	return err
}

// innerCreateBastionCommand is the main entry point for the create-bastion command.
// It orchestrates the entire bastion creation process:
//  1. Parse and validate configuration
//  2. Initialize logging
//  3. Clean up previous bastion IP file
//  4. Ensure server exists (create if needed)
//  5. Setup bastion server (HAProxy, DNS)
//  6. Write bastion IP to file
func innerCreateBastionCommand(createBastionFlags *flag.FlagSet, args []string) error {
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
	if err := cleanupBastionIPFile(config.BastionIpFile); err != nil {
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
func cleanupBastionIPFile(filename string) error {
	err := os.Remove(filename)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove bastion IP file: %w", err)
	}
	return nil
}

// ensureServerExists checks if server exists and creates it if needed.
// If the server already exists, it returns immediately.
// ensureServerExists checks if a bastion server exists and creates it if necessary.
//
// The function performs the following operations:
//  1. Checks if the server already exists using findServer
//  2. If the server exists, returns immediately (idempotent operation)
//  3. If the server doesn't exist, creates it by:
//     a. Finding the target network by name
//     b. Creating a network port on that network
//     c. Creating the server with the specified configuration
//  4. Verifies the server was successfully created
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - config: BastionConfig containing all server creation parameters including:
//     * BastionName: Name of the server to create
//     * Clouds: OpenStack cloud configurations (uses first cloud for creation)
//     * NetworkName: Network to attach the server to
//     * AvailabilityZone: Zone where the server will be created
//     * FlavorName: Server flavor/size
//     * ImageName: OS image to use
//     * SshKeyName: SSH key for server access
//
// Returns:
//   - nil if the server exists or was successfully created
//   - ErrServerNotFound wrapped error if server lookup fails unexpectedly
//   - Wrapped error if network lookup, port creation, server creation, or verification fails
//
// The function is idempotent - calling it multiple times with the same configuration
// will not create duplicate servers.
func ensureServerExists(ctx context.Context, config *BastionConfig) error {
	// Step 1: Check if the server already exists (idempotent check)
	// This allows the function to be called multiple times safely
	_, err := findServer(ctx, config.Clouds, config.BastionName)
	if err == nil {
		// Server found - nothing to do, return success
		log.Debugf("Server %s already exists", config.BastionName)
		return nil
	}

	// Handle unexpected errors from findServer (not just "not found")
	// ErrServerNotFound is expected and means we should create the server
	if !errors.Is(err, ErrServerNotFound) {
		return fmt.Errorf("unknown error found in ensureServerExists: %w", err)
	}

	// Step 2: Server doesn't exist, proceed with creation
	fmt.Printf("Server %s not found, creating...\n", config.BastionName)

	// Step 3: Locate the target network where the server will be attached
	// The network must exist before we can create a port on it
	network, err := findNetwork(ctx, config.Clouds[0], config.NetworkName)
	if err != nil {
		return fmt.Errorf("failed to find network %q: %w", config.NetworkName, err)
	}
	log.Debugf("ensureServerExists: network = %+v", network)

	// Step 4: Create a network port for the server
	// The port provides the network interface and IP address allocation
	port, err := createNetworkPort(ctx, config.Clouds[0], config.BastionName, network.ID)
	if err != nil {
		return fmt.Errorf("failed to create network port: %w", err)
	}
	// Log port details for debugging network connectivity issues
	log.Debugf("ensureServerExists: port.ID = %v", port.ID)
	for i, ip := range port.FixedIPs {
		log.Debugf("ensureServerExists: port[%d].SubnetID  = %s", i, ip.SubnetID)
		log.Debugf("ensureServerExists: port[%d].IPAddress = %s", i, ip.IPAddress)
	}

	// Step 5: Create the server instance with all specified configuration
	// The server is created with the network port, SSH key, and other settings
	if err := createServer(ctx,
		config.BastionName,
		config.Clouds[0],
		config.AvailabilityZone,
		config.FlavorName,
		config.ImageName,
		port,
		config.SshKeyName,
		nil, // No user data script
	); err != nil {
		return fmt.Errorf("failed to create server: %w", err)
	}

	fmt.Println("Server created successfully!")

	// Step 6: Verify the server was actually created and is discoverable
	// This ensures the creation operation completed successfully and the server
	// is visible in the OpenStack API before we proceed with further operations
	if _, err := findServer(ctx, config.Clouds, config.BastionName); err != nil {
		return fmt.Errorf("server verification failed after creation: %w", err)
	}

	return nil
}

// setupBastion configures the bastion server either remotely or locally.
// The setup mode is determined by the BastionConfig:
//  - Remote setup: delegates to another server via sendCreateBastion
//  - Local setup: performs setup directly via SSH
// The operation respects context cancellation and timeout.
func setupBastion(ctx context.Context, config *BastionConfig) error {
	if config.IsRemoteSetup() {
		fmt.Println("Setting up bastion remotely...")
		if err := sendCreateBastion(ctx,
			config.ServerIP,
			config.Clouds[0],
			config.BastionName,
			config.DomainName,
			config.EnableHAProxy,
		); err != nil {
			return fmt.Errorf("remote setup failed: %w", err)
		}
		return nil
	}

	fmt.Println("Setting up bastion locally...")
	if err := setupBastionServer(ctx,
		config.EnableHAProxy,
		config.Clouds,
		config.BastionName,
		config.DomainName,
		config.BastionRsa,
	); err != nil {
		return fmt.Errorf("local setup failed: %w", err)
	}
	return nil
}

// createServer creates a new OpenStack server (bastion host) with the specified configuration.
//
// This function orchestrates the complete server creation workflow by:
//  1. Looking up required OpenStack resources (flavor, image, SSH keypair)
//  2. Creating the server instance with the provided network port
//  3. Waiting for the server to reach ACTIVE state
//  4. Performing automatic cleanup on any failures
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - bastionName: Name to assign to the new server instance
//   - cloudName: OpenStack cloud name from clouds.yaml
//   - availabilityZone: Availability zone where the server will be created
//   - flavorName: Name of the compute flavor (defines CPU, RAM, disk)
//   - imageName: Name of the OS image to boot from
//   - port: Pre-created network port to attach to the server
//   - sshKeyName: Name of SSH keypair for authentication (empty string to skip)
//   - userData: Cloud-init user data for server initialization (can be nil)
//
// Returns:
//   - error: nil on success, or an error describing what went wrong
//
// Error Handling:
//   - If resource lookup fails, returns immediately with error
//   - If server creation fails, automatically deletes the network port
//   - If server doesn't become ACTIVE in time, deletes both server and port
//   - All cleanup operations use independent contexts to ensure completion
func createServer(ctx context.Context, bastionName, cloudName, availabilityZone, flavorName, imageName string, port *ports.Port, sshKeyName string, userData []byte) error {
	var (
		flavor           flavors.Flavor
		image            images.Image
		sshKeyPair       keypairs.KeyPair
		newServer        *servers.Server
		err              error
	)

	// Step 1: Lookup OpenStack resources by name to get their IDs
	// These lookups validate that all required resources exist before attempting server creation
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

	// SSH keypair is optional - only lookup if a key name was provided
	if sshKeyName != "" {
		sshKeyPair, err = findKeyPair(ctx, cloudName, sshKeyName)
		if err != nil {
			return fmt.Errorf("failed to find SSH keypair %q: %w", sshKeyName, err)
		}
		log.Debugf("createServer: sshKeyPair = %+v", sshKeyPair)
	}

	// Step 2: Define cleanup functions for error recovery
	// These use independent contexts to ensure cleanup completes even if the original context is cancelled

	// cleanupPort removes the network port if server creation fails
	// Uses a fresh context with timeout to ensure cleanup completes even if original context is cancelled
	cleanupPort := func(createdPort *ports.Port) {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), cleanupPortTimeout)
		defer cancel()

		if deleteErr := deleteNetworkPort(cleanupCtx, cloudName, createdPort); deleteErr != nil {
			log.Debugf("Warning: failed to cleanup port %s: %v", port.ID, deleteErr)
		}
	}

	// cleanupServerAndPort removes both the server and its network port
	// Server must be deleted first before the port can be removed
	cleanupServerAndPort := func(server *servers.Server, createdPort *ports.Port) {
		// Uses a fresh context with timeout to ensure cleanup completes even if original context is cancelled
		cleanupCtx, cancel := context.WithTimeout(context.Background(), cleanupServerTimeout)
		defer cancel()

		// Delete server first - port cannot be deleted while still attached to a server
		if deleteErr := deleteServer(cleanupCtx, cloudName, server); deleteErr != nil {
			if server == nil {
				log.Debugf("Warning: failed to cleanup nil server: %v", deleteErr)
			} else {
				log.Debugf("Warning: failed to cleanup server %v: %v", server.ID, deleteErr)
			}
		}
		// Then cleanup the port
		cleanupPort(createdPort)
	}

	// Step 3: Create the server instance with all validated resources
	// Pass resource IDs (not names) to the actual creation function
	newServer, err = createServerInstance(ctx,
		bastionName,
		cloudName,
		availabilityZone,
		flavor.ID,
		image.ID,
		port.ID,
		sshKeyName,
		sshKeyPair.Name,
		userData,
	)
	if err != nil {
		// Server creation failed - cleanup the port since it won't be used
		cleanupPort(port)

		return fmt.Errorf("failed to create server instance: %w", err)
	}
	log.Debugf("createServer: newServer = %+v", newServer)

	// Step 4: Wait for server to reach ACTIVE state
	// This ensures the server is fully provisioned and ready for use
	if err := waitForServer(ctx, cloudName, bastionName); err != nil {
		// Server didn't become ready in time - cleanup both server and port
		cleanupServerAndPort(newServer, port)

		return fmt.Errorf("server creation timeout: %w", err)
	}

	return nil
}

// createServerInstance creates the actual OpenStack server instance via the Nova API.
//
// This is a lower-level function that performs the actual server creation API call.
// It should be called by createServer after all resources have been validated and looked up.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - bastionName: Name to assign to the server instance
//   - cloudName: OpenStack cloud name from clouds.yaml
//   - availabilityZone: Availability zone where the server will be created
//   - flavorID: UUID of the compute flavor (not the name)
//   - imageID: UUID of the OS image (not the name)
//   - portID: UUID of the pre-created network port to attach
//   - sshKeyName: Original SSH key name (used for conditional logic, can be empty)
//   - sshKeyPairName: OpenStack keypair name to inject (used only if sshKeyName is not empty)
//   - userData: Cloud-init user data for server initialization (can be nil)
//
// Returns:
//   - *servers.Server: The newly created server object with its ID and initial state
//   - error: nil on success, or an error if the API call fails
//
// Behavior:
//   - If sshKeyName is empty, creates server without SSH key injection
//   - If sshKeyName is provided, uses keypairs.CreateOptsExt to inject the SSH key
//   - The server is created in BUILD state and transitions to ACTIVE asynchronously
//   - Caller is responsible for waiting until the server reaches ACTIVE state
func createServerInstance(ctx context.Context, bastionName, cloudName, availabilityZone, flavorID, imageID, portID, sshKeyName, sshKeyPairName string, userData []byte) (*servers.Server, error) {
	// Establish connection to OpenStack Nova (compute) service
	connCompute, err := NewServiceClient(ctx, "compute", DefaultClientOpts(cloudName))
	if err != nil {
		return nil, fmt.Errorf("failed to create compute client: %w", err)
	}

	// Build the base server creation options with all required parameters
	// Note: All IDs (flavor, image, port) must be UUIDs, not names
	serverCreateOpts := servers.CreateOpts{
		AvailabilityZone: availabilityZone,
		FlavorRef:        flavorID,
		ImageRef:         imageID,
		Name:             bastionName,
		Networks:         []servers.Network{{Port: portID}}, // Attach to pre-created port
		UserData:         userData,                          // Cloud-init configuration
	}

	var newServer *servers.Server

	// Conditionally inject SSH keypair based on whether one was provided
	if sshKeyName != "" {
		// Create server with SSH key injection using the keypairs extension
		// This allows SSH access using the private key corresponding to sshKeyPairName
		newServer, err = servers.Create(ctx,
			connCompute,
			keypairs.CreateOptsExt{
				CreateOptsBuilder: serverCreateOpts,
				KeyName:           sshKeyPairName, // OpenStack keypair name
			},
			nil).Extract()
	} else {
		// Create server without SSH key injection
		// Access will depend on cloud-init userData or other authentication methods
		newServer, err = servers.Create(ctx, connCompute, serverCreateOpts, nil).Extract()
	}

	if err != nil {
		return nil, fmt.Errorf("failed to create server: %w", err)
	}

	// Return the server object - it will be in BUILD state initially
	return newServer, nil
}

// deleteServer deletes a server instance by its ID.
// Returns an error if the server cannot be deleted.
func deleteServer(ctx context.Context, cloudName string, server *servers.Server) error {
	if server == nil || server.ID == "" {
		return nil
	}

	connCompute, err := NewServiceClient(ctx, "compute", DefaultClientOpts(cloudName))
	if err != nil {
		return fmt.Errorf("failed to create compute client: %w", err)
	}

	err = servers.Delete(ctx, connCompute, server.ID).ExtractErr()
	if err != nil {
		return fmt.Errorf("failed to delete server: %w", err)
	}

	return nil
}


// addServerKnownHosts adds the server's SSH host keys to known_hosts file.
// It removes any existing host keys for the IP address before adding new ones.
func addServerKnownHosts(ctx context.Context, ipAddress string) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get user home directory: %w", err)
	}

	knownHostsPath := filepath.Join(homeDir, ".ssh", "known_hosts")
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
// It returns an error only if the removal fails for reasons other than the key not existing.
// "Not found" errors are ignored since the key may not exist yet.
func removeHostKey(knownHostsPath, ipAddress string) error {
	outb, err := runSplitCommand2([]string{
		"ssh-keygen",
		"-f", knownHostsPath,
		"-R", ipAddress,
	})

	output := strings.TrimSpace(string(outb))
	log.Debugf("removeHostKey: output = %q", output)

	if err != nil {
		// Ignore "not found" errors - the host key may not exist yet
		if strings.Contains(output, "not found") ||
		   strings.Contains(output, "No such file or directory") {
			log.Debugf("removeHostKey: host key not found (expected), ignoring error")
			return nil
		}
		return fmt.Errorf("failed to remove host key for %s: %w", ipAddress, err)
	}

	return nil
}

// appendToFile appends data to a file.
// It returns an error if the file cannot be opened, written to, or if the write is incomplete.
func appendToFile(filePath string, data []byte) error {
	file, err := os.OpenFile(filePath, os.O_APPEND|os.O_RDWR|os.O_CREATE, filePermReadWrite)
	if err != nil {
		return fmt.Errorf("failed to open file %q: %w", filePath, err)
	}

	n, err := file.Write(data)
	if err != nil {
		file.Close()
		return fmt.Errorf("failed to write to file: %w", err)
	}

	if n != len(data) {
		file.Close()
		return fmt.Errorf("incomplete write: wrote %d of %d bytes", n, len(data))
	}

	if err := file.Close(); err != nil {
		return fmt.Errorf("failed to close file: %w", err)
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

	if err := os.WriteFile(config.BastionIpFile, []byte(ipAddress), filePermReadWrite); err != nil {
		return fmt.Errorf("failed to write bastion IP to %q: %w", config.BastionIpFile, err)
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
