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

// Package main provides functionality for creating and managing RHCOS (Red Hat CoreOS) servers
// on OpenStack/PowerVC infrastructure. This file specifically handles the creation of RHCOS
// virtual machines with Ignition-based configuration.
//
// Key Features:
//   - Automated RHCOS server provisioning
//   - Ignition configuration generation for bootstrap
//   - SSH host key management
//   - Optional DNS configuration via IBM Cloud
//   - Comprehensive input validation
//
// Usage Example:
//   ./ocp-ipi-powervc create-rhcos \
//     --cloud mycloud \
//     --rhcosName my-rhcos-server \
//     --flavorName medium \
//     --imageName rhcos-4.12 \
//     --networkName private-net \
//     --passwdHash '$6$rounds=4096$...' \
//     --sshPublicKey 'ssh-rsa AAAA...' \
//     --domainName example.com \
//     --shouldDebug true
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path"
	"strings"
	"time"

	igntypes "github.com/coreos/ignition/v2/config/v3_2/types"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"
	"k8s.io/utils/ptr"
)

const (
	// RHCOS server configuration constants
	rhcosDefaultTimeout       = 15 * time.Minute
	novaUserDataMaxSize       = 65535 // 64KB limit for nova user data
	ignitionHTTPTimeout       = 120
	sshKeygenExitCodeNotFound = 1
	knownHostsFilePerms       = 0644
	sshDirPerms               = 0700

	// Error message patterns
	serverNotFoundPrefix = "could not find server named"

	// SSH key validation
	minSSHKeyLength       = 100 // Minimum reasonable SSH public key length
	minPasswordHashLength = 13  // Minimum crypt hash length

	// Retry configuration
	maxRetryAttempts     = 3
	retryInitialDelay    = 2 * time.Second
	retryMaxDelay        = 30 * time.Second
	retryBackoffMultiplier = 2.0

	// Progress tracking
	progressStepParsing    = "Parsing configuration"
	progressStepIgnition   = "Generating Ignition config"
	progressStepFinding    = "Finding or creating server"
	progressStepSetup      = "Setting up server"
	progressStepDNS        = "Configuring DNS"
	progressStepComplete   = "Complete"
)

// ValidationError represents a configuration validation error
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("validation error for field '%s': %s", e.Field, e.Message)
}

// RetryableError indicates an operation that can be retried
type RetryableError struct {
	Err     error
	Attempt int
}

func (e *RetryableError) Error() string {
	return fmt.Sprintf("retryable error (attempt %d): %v", e.Attempt, e.Err)
}

func (e *RetryableError) Unwrap() error {
	return e.Err
}

// rhcosConfig holds all configuration parameters required for RHCOS server creation.
// This struct encapsulates both required and optional settings for provisioning
// a Red Hat CoreOS virtual machine on OpenStack/PowerVC.
type rhcosConfig struct {
	// Clouds specifies the cloud name from clouds.yaml to use for OpenStack authentication
	Clouds cloudFlags

	// RhcosName is the name to assign to the RHCOS virtual machine
	RhcosName string

	// FlavorName specifies the OpenStack flavor (instance type) to use
	FlavorName string

	// ImageName is the name of the RHCOS image in OpenStack/PowerVC
	ImageName string

	// NetworkName specifies the network to attach the VM to
	NetworkName string

	// PasswdHash is the crypt-formatted password hash for the 'core' user
	// Must be in format: $<algorithm>$<salt>$<hash>
	PasswdHash string

	// SshPublicKey contains the SSH public key for the 'core' user
	// Must start with 'ssh-' or 'ecdsa-'
	SshPublicKey string

	// DomainName is the optional DNS domain for the server (requires IBMCLOUD_API_KEY)
	DomainName string

	// ShouldDebug enables verbose debug logging when true
	ShouldDebug bool

	// APIKey is the IBM Cloud API key for DNS configuration (from IBMCLOUD_API_KEY env var)
	APIKey string
}

// validate performs comprehensive validation of the RHCOS configuration.
// It checks for required fields, validates formats, and ensures security requirements.
//
// Returns a ValidationError if any validation check fails, with a descriptive message
// indicating which field failed validation and why.
func (c *rhcosConfig) validate() error {
	// Validate required string fields
	requiredFields := map[string]string{
		"RhcosName":   c.RhcosName,
		"FlavorName":  c.FlavorName,
		"ImageName":   c.ImageName,
		"NetworkName": c.NetworkName,
	}

	if len(c.Clouds) != 1 {
		return &ValidationError{
			Field:   "Cloud",
			Message: fmt.Sprintf("should only have one element: %d", len(c.Clouds)),
		}
	}

	if c.Clouds[0] == "" {
		return &ValidationError{
			Field:   "Cloud",
			Message: "is required",
		}
	}

	for field, value := range requiredFields {
		if value == "" {
			return &ValidationError{
				Field:   field,
				Message: "is required",
			}
		}
	}

	// Validate RHCOS name format
	if !isValidResourceName(c.RhcosName) {
		return &ValidationError{
			Field:   "RhcosName",
			Message: fmt.Sprintf("contains invalid characters: %s", c.RhcosName),
		}
	}

	// Validate SSH public key
	if err := c.validateSSHKey(); err != nil {
		return err
	}

	// Validate password hash
	if err := c.validatePasswordHash(); err != nil {
		return err
	}

	return nil
}

// validateSSHKey validates the SSH public key format and length
func (c *rhcosConfig) validateSSHKey() error {
	if c.SshPublicKey == "" {
		return &ValidationError{
			Field:   "SshPublicKey",
			Message: "is required",
		}
	}
	if len(c.SshPublicKey) < minSSHKeyLength {
		return &ValidationError{
			Field:   "SshPublicKey",
			Message: fmt.Sprintf("appears invalid (too short, minimum %d characters)", minSSHKeyLength),
		}
	}
	if !strings.HasPrefix(c.SshPublicKey, "ssh-") && !strings.HasPrefix(c.SshPublicKey, "ecdsa-") {
		return &ValidationError{
			Field:   "SshPublicKey",
			Message: "must start with 'ssh-' or 'ecdsa-'",
		}
	}
	return nil
}

// validatePasswordHash validates the password hash format and length
func (c *rhcosConfig) validatePasswordHash() error {
	if c.PasswdHash == "" {
		return &ValidationError{
			Field:   "PasswdHash",
			Message: "is required",
		}
	}
	if len(c.PasswdHash) < minPasswordHashLength {
		return &ValidationError{
			Field:   "PasswdHash",
			Message: fmt.Sprintf("appears invalid (too short, minimum %d characters)", minPasswordHashLength),
		}
	}
	if !strings.HasPrefix(c.PasswdHash, "$") {
		return &ValidationError{
			Field:   "PasswdHash",
			Message: "must be in crypt format (starting with $)",
		}
	}
	return nil
}

// parseRhcosFlags parses command-line flags and constructs a validated rhcosConfig.
// It handles flag parsing, environment variable loading, and comprehensive validation.
//
// Parameters:
//   - createRhcosFlags: The FlagSet containing flag definitions
//   - args: Command-line arguments to parse
//
// Returns:
//   - *rhcosConfig: Populated and validated configuration
//   - error: Any error encountered during parsing or validation
func parseRhcosFlags(createRhcosFlags *flag.FlagSet, args []string) (*rhcosConfig, error) {
	config := &rhcosConfig{}

	// Define flags
	ptrCloud := createRhcosFlags.String("cloud", "", "The cloud to use in clouds.yaml")
	ptrRhcosName := createRhcosFlags.String("rhcosName", "", "The name of the RHCOS VM to create")
	ptrFlavorName := createRhcosFlags.String("flavorName", "", "The name of the flavor to use")
	ptrImageName := createRhcosFlags.String("imageName", "", "The name of the image to use")
	ptrNetworkName := createRhcosFlags.String("networkName", "", "The name of the network to use")
	ptrPasswdHash := createRhcosFlags.String("passwdHash", "", "The password hash of the core user")
	ptrSshPublicKey := createRhcosFlags.String("sshPublicKey", "", "The contents of the SSH public key to use")
	ptrDomainName := createRhcosFlags.String("domainName", "", "The DNS domain to use (optional)")
	ptrShouldDebug := createRhcosFlags.String("shouldDebug", "false", "Enable debug output")

	if err := createRhcosFlags.Parse(args); err != nil {
		return nil, fmt.Errorf("failed to parse flags: %w", err)
	}

	// Populate config from parsed flags
	config.Clouds = []string{ *ptrCloud }
	config.RhcosName = *ptrRhcosName
	config.FlavorName = *ptrFlavorName
	config.ImageName = *ptrImageName
	config.NetworkName = *ptrNetworkName
	config.PasswdHash = *ptrPasswdHash
	config.SshPublicKey = *ptrSshPublicKey
	config.DomainName = *ptrDomainName
	config.APIKey = os.Getenv("IBMCLOUD_API_KEY")

	// Parse debug flag
	debug, err := parseBoolFlag(*ptrShouldDebug, "shouldDebug")
	if err != nil {
		return nil, err
	}
	config.ShouldDebug = debug

	// Validate configuration
	if err := config.validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return config, nil
}

// createRhcosCommand is the main entry point for the RHCOS server creation workflow.
// It orchestrates the entire process: configuration parsing, ignition generation,
// server provisioning, SSH setup, and DNS configuration.
//
// Workflow:
//  1. Parse and validate command-line flags
//  2. Initialize logging based on debug flag
//  3. Generate Ignition configuration for bootstrap
//  4. Find existing server or create new one
//  5. Configure SSH known_hosts
//  6. Set up DNS records (if IBM Cloud API key provided)
//
// Parameters:
//   - createRhcosFlags: FlagSet for parsing command-line arguments
//   - args: Command-line arguments
//
// Returns:
//   - error: Any error encountered during the workflow, nil on success
func createRhcosCommand(createRhcosFlags *flag.FlagSet, args []string) error {
	fmt.Fprintf(os.Stderr, "Program version is %v, release = %v\n", version, release)

	// Step 1: Parse and validate configuration
	printProgress(progressStepParsing)
	config, err := parseRhcosFlags(createRhcosFlags, args)
	if err != nil {
		return fmt.Errorf("configuration error: %w", err)
	}

	// Initialize logger
	log = initLogger(config.ShouldDebug)
	if config.ShouldDebug {
		log.Debugf("Debug mode enabled")
		log.Debugf("Configuration: Clouds=%s, RhcosName=%s, Flavor=%s, Image=%s, Network=%s",
			config.Clouds, config.RhcosName, config.FlavorName, config.ImageName, config.NetworkName)
	}

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), rhcosDefaultTimeout)
	defer cancel()

	// Step 2: Generate ignition user data
	printProgress(progressStepIgnition)
	userData, err := createBootstrapIgnition(config.PasswdHash, config.SshPublicKey)
	if err != nil {
		return fmt.Errorf("failed to create bootstrap ignition: %w", err)
	}
	log.Debugf("Ignition configuration generated successfully (%d bytes)", len(userData))

	// Step 3: Find or create the server
	printProgress(progressStepFinding)
	foundServer, err := findOrCreateRhcosServerWithRetry(ctx, config, userData)
	if err != nil {
		return fmt.Errorf("failed to find or create server: %w", err)
	}
	log.Debugf("Server ready: %s (ID: %s, Status: %s)", foundServer.Name, foundServer.ID, foundServer.Status)

	// Step 4: Setup the server (SSH keys, etc.)
	printProgress(progressStepSetup)
	if err := setupRhcosServerWithRetry(ctx, foundServer); err != nil {
		return fmt.Errorf("failed to setup server: %w", err)
	}

	// Step 5: Configure DNS if API key is available
	printProgress(progressStepDNS)
	if err := configureDNS(ctx, config); err != nil {
		return fmt.Errorf("failed to configure DNS: %w", err)
	}

	printProgress(progressStepComplete)
	fmt.Printf("\n✓ RHCOS server '%s' is ready!\n", config.RhcosName)
	return nil
}

// printProgress prints a progress message to stderr
func printProgress(step string) {
	fmt.Fprintf(os.Stderr, "\n==> %s...\n", step)
}

// findOrCreateRhcosServer attempts to find an existing RHCOS server by name,
// or creates a new one if not found. This function implements idempotent
// server provisioning.
//
// Parameters:
//   - ctx: Context for timeout and cancellation
//   - config: RHCOS configuration containing server details
//   - userData: Ignition configuration data for server bootstrap
//
// Returns:
//   - servers.Server: The found or newly created server
//   - error: Any error encountered during search or creation
func findOrCreateRhcosServer(ctx context.Context, config *rhcosConfig, userData []byte) (servers.Server, error) {
	log.Debugf("Looking for existing server: %s", config.RhcosName)

	foundServer, err := findServer(ctx, config.Clouds, config.RhcosName)
	if err != nil {
		// Check if error is due to server not found
		if !isServerNotFoundError(err) {
			return servers.Server{}, fmt.Errorf("error searching for server: %w", err)
		}

		// Server not found, create it
		log.Debugf("Server %s not found, creating new server", config.RhcosName)
		fmt.Printf("Server %s not found, creating...\n", config.RhcosName)

		if err := createServer(ctx,
			config.Clouds[0],
			config.FlavorName,
			config.ImageName,
			config.NetworkName,
			"", // No SSH key for RHCOS (uses ignition)
			config.RhcosName,
			userData,
		); err != nil {
			return servers.Server{}, fmt.Errorf("failed to create server: %w", err)
		}

		fmt.Println("Server created successfully!")

		// Retrieve the newly created server with retry
		log.Debugf("Retrieving newly created server: %s", config.RhcosName)
		foundServer, err = findServer(ctx, config.Clouds, config.RhcosName)
		if err != nil {
			return servers.Server{}, fmt.Errorf("failed to find newly created server: %w", err)
		}
	} else {
		log.Debugf("Found existing server: %s (ID: %s, Status: %s)",
			foundServer.Name, foundServer.ID, foundServer.Status)
		fmt.Printf("Using existing server: %s\n", config.RhcosName)
	}

	return foundServer, nil
}

// findOrCreateRhcosServerWithRetry wraps findOrCreateRhcosServer with retry logic
// for handling transient failures in server operations.
//
// Parameters:
//   - ctx: Context for timeout and cancellation
//   - config: RHCOS configuration containing server details
//   - userData: Ignition configuration data for server bootstrap
//
// Returns:
//   - servers.Server: The found or newly created server
//   - error: Any error encountered during search or creation after all retries
func findOrCreateRhcosServerWithRetry(ctx context.Context, config *rhcosConfig, userData []byte) (servers.Server, error) {
	return retryOperation(ctx, "find or create server", func() (servers.Server, error) {
		return findOrCreateRhcosServer(ctx, config, userData)
	})
}

// isServerNotFoundError determines if an error indicates a server was not found.
// This helper function provides consistent error detection across the codebase.
//
// Parameters:
//   - err: The error to check
//
// Returns:
//   - bool: true if the error indicates server not found, false otherwise
func isServerNotFoundError(err error) bool {
	log.Debugf("isServerNotFoundError: err = %+v\n", err)
	if err == nil {
		return false
	}
	return strings.HasPrefix(err.Error(), serverNotFoundPrefix)
}

// configureDNS sets up DNS records for the RHCOS server using IBM Cloud DNS.
// This function is optional and only executes if IBMCLOUD_API_KEY is set.
// If no API key is available, it logs a warning and returns successfully.
//
// Parameters:
//   - ctx: Context for timeout and cancellation
//   - config: RHCOS configuration containing DNS and API key details
//
// Returns:
//   - error: Any error encountered during DNS configuration, nil on success or skip
func configureDNS(ctx context.Context, config *rhcosConfig) error {
	if config.APIKey == "" {
		fmt.Println("Warning: IBMCLOUD_API_KEY not set. DNS configuration skipped.")
		fmt.Println("Ensure DNS is configured through another method.")
		return nil
	}

	log.Debugf("Configuring DNS for server %s", config.RhcosName)
	if err := dnsForServer(ctx, config.Clouds, config.APIKey, config.RhcosName, config.DomainName); err != nil {
		return fmt.Errorf("DNS configuration failed: %w", err)
	}

	log.Debugf("DNS configured successfully for %s", config.RhcosName)
	return nil
}

// setupRhcosServer performs post-creation setup for the RHCOS server.
// Currently, this includes adding the server's SSH host key to known_hosts
// to enable passwordless SSH access.
//
// Parameters:
//   - ctx: Context for timeout and cancellation
//   - server: The server object to set up
//
// Returns:
//   - error: Any error encountered during setup, nil on success
func setupRhcosServer(ctx context.Context, server servers.Server) error {
	log.Debugf("Setting up RHCOS server: %s (ID: %s)", server.Name, server.ID)

	// Get server IP address
	_, ipAddress, err := findIpAddress(server)
	if err != nil {
		return fmt.Errorf("failed to find IP address: %w", err)
	}
	if ipAddress == "" {
		return fmt.Errorf("server %s has no IP address", server.Name)
	}

	log.Debugf("Server IP address: %s", ipAddress)

	// Add SSH host key to known_hosts if not already present
	if err := ensureSSHHostKey(ctx, ipAddress); err != nil {
		return fmt.Errorf("failed to setup SSH host key: %w", err)
	}

	fmt.Printf("Server %s setup completed successfully\n", server.Name)
	return nil
}

// setupRhcosServerWithRetry wraps setupRhcosServer with retry logic
// for handling transient failures in SSH operations.
//
// Parameters:
//   - ctx: Context for timeout and cancellation
//   - server: The server object to set up
//
// Returns:
//   - error: Any error encountered during setup after all retries
func setupRhcosServerWithRetry(ctx context.Context, server servers.Server) error {
	_, err := retryOperation(ctx, "setup server", func() (servers.Server, error) {
		if err := setupRhcosServer(ctx, server); err != nil {
			return servers.Server{}, err
		}
		return server, nil
	})
	return err
}

// retryOperation performs an operation with exponential backoff retry logic.
// It retries transient failures up to maxRetryAttempts times.
//
// Parameters:
//   - ctx: Context for timeout and cancellation
//   - operationName: Name of the operation for logging
//   - operation: The operation to retry
//
// Returns:
//   - servers.Server: The result of the operation
//   - error: Any error encountered after all retries
func retryOperation(ctx context.Context, operationName string, operation func() (servers.Server, error)) (servers.Server, error) {
	var lastErr error
	delay := retryInitialDelay

	for attempt := 1; attempt <= maxRetryAttempts; attempt++ {
		result, err := operation()
		if err == nil {
			if attempt > 1 {
				log.Debugf("Operation '%s' succeeded on attempt %d", operationName, attempt)
			}
			return result, nil
		}

		lastErr = err
		
		// Check if we should retry
		if attempt < maxRetryAttempts && isRetryableError(err) {
			log.Debugf("Operation '%s' failed (attempt %d/%d): %v. Retrying in %v...",
				operationName, attempt, maxRetryAttempts, err, delay)
			
			// Wait with exponential backoff
			select {
			case <-time.After(delay):
				// Calculate next delay with exponential backoff
				delay = time.Duration(float64(delay) * retryBackoffMultiplier)
				if delay > retryMaxDelay {
					delay = retryMaxDelay
				}
			case <-ctx.Done():
				return servers.Server{}, fmt.Errorf("operation '%s' cancelled: %w", operationName, ctx.Err())
			}
		} else {
			break
		}
	}

	return servers.Server{}, fmt.Errorf("operation '%s' failed after %d attempts: %w", operationName, maxRetryAttempts, lastErr)
}

// isRetryableError determines if an error is transient and can be retried.
// Network errors, timeouts, and temporary failures are considered retryable.
//
// Parameters:
//   - err: The error to check
//
// Returns:
//   - bool: true if the error is retryable, false otherwise
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// Check for explicit RetryableError type
	var retryErr *RetryableError
	if errors.As(err, &retryErr) {
		return true
	}

	// Check error message for common retryable patterns
	errMsg := strings.ToLower(err.Error())
	retryablePatterns := []string{
		"timeout",
		"connection refused",
		"connection reset",
		"temporary failure",
		"try again",
		"no route to host",
		"network is unreachable",
		"i/o timeout",
	}

	for _, pattern := range retryablePatterns {
		if strings.Contains(errMsg, pattern) {
			return true
		}
	}

	return false
}

// ensureSSHHostKey ensures the server's SSH host key is present in the user's
// known_hosts file. If the key is not found, it scans the server and adds it.
// This prevents SSH from prompting for host key verification on first connection.
//
// The function:
//  1. Ensures the .ssh directory exists with proper permissions
//  2. Checks if the host key already exists using ssh-keygen
//  3. If not found, scans the server using ssh-keyscan
//  4. Appends the scanned key to known_hosts
//
// Parameters:
//   - ctx: Context for timeout and cancellation
//   - ipAddress: IP address of the server to scan
//
// Returns:
//   - error: Any error encountered during the process, nil on success
func ensureSSHHostKey(ctx context.Context, ipAddress string) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	sshDir := path.Join(homeDir, ".ssh")
	knownHostsPath := path.Join(sshDir, "known_hosts")

	// Ensure .ssh directory exists
	if err := ensureSSHDirectory(sshDir); err != nil {
		return fmt.Errorf("failed to ensure SSH directory: %w", err)
	}

	log.Debugf("Known hosts file: %s", knownHostsPath)

	// Check if host key already exists using ssh-keygen
	// Exit code 0: key found, Exit code 1: key not found
	_, err = runSplitCommand2([]string{
		"ssh-keygen",
		"-H",
		"-F",
		ipAddress,
	})

	var exitError *exec.ExitError
	if errors.As(err, &exitError) && exitError.ExitCode() == sshKeygenExitCodeNotFound {
		// Host key not found, scan and add it
		log.Debugf("SSH host key not found for %s, scanning...", ipAddress)

		hostKey, err := keyscanServer(ctx, ipAddress, false)
		if err != nil {
			return fmt.Errorf("failed to scan SSH host key: %w", err)
		}

		if len(hostKey) == 0 {
			return fmt.Errorf("received empty host key from server %s", ipAddress)
		}

		// Append to known_hosts file
		file, err := os.OpenFile(knownHostsPath, os.O_APPEND|os.O_RDWR|os.O_CREATE, knownHostsFilePerms)
		if err != nil {
			return fmt.Errorf("failed to open known_hosts file: %w", err)
		}
		defer file.Close()

		if _, err := file.Write(hostKey); err != nil {
			return fmt.Errorf("failed to write to known_hosts: %w", err)
		}

		log.Debugf("SSH host key added for %s (%d bytes)", ipAddress, len(hostKey))
	} else if err != nil {
		log.Debugf("Error checking SSH host key: %v", err)
		return fmt.Errorf("failed to check SSH host key: %w", err)
	} else {
		log.Debugf("SSH host key already exists for %s", ipAddress)
	}

	return nil
}

// ensureSSHDirectory creates the .ssh directory if it doesn't exist,
// with proper permissions (0700) for security. It also validates that
// if the path exists, it is actually a directory.
//
// Parameters:
//   - sshDir: Path to the .ssh directory to ensure exists
//
// Returns:
//   - error: Any error encountered, nil if directory exists or was created successfully
func ensureSSHDirectory(sshDir string) error {
	info, err := os.Stat(sshDir)
	if err != nil {
		if os.IsNotExist(err) {
			log.Debugf("Creating SSH directory: %s", sshDir)
			if err := os.MkdirAll(sshDir, sshDirPerms); err != nil {
				return fmt.Errorf("failed to create SSH directory: %w", err)
			}
			return nil
		}
		return fmt.Errorf("failed to stat SSH directory: %w", err)
	}

	if !info.IsDir() {
		return fmt.Errorf("SSH path exists but is not a directory: %s", sshDir)
	}

	return nil
}

// createBootstrapIgnition generates an Ignition v3.2 configuration for RHCOS bootstrap.
// The configuration includes user credentials (password hash and SSH key) for the 'core' user.
//
// The generated configuration:
//  - Uses Ignition v3.2 format (latest stable)
//  - Sets HTTP response timeout to 120 seconds
//  - Configures the 'core' user with provided credentials
//  - Is validated against OpenStack nova user data size limits (64KB)
//
// Parameters:
//   - passwdHash: Crypt-formatted password hash for the core user
//   - sshKey: SSH public key for the core user
//
// Returns:
//   - []byte: JSON-encoded Ignition configuration
//   - error: Any error encountered during generation or validation
func createBootstrapIgnition(passwdHash, sshKey string) ([]byte, error) {
	log.Debugf("Creating bootstrap ignition configuration")

	// Validate inputs
	if passwdHash == "" {
		return nil, &ValidationError{
			Field:   "passwdHash",
			Message: "cannot be empty",
		}
	}
	if sshKey == "" {
		return nil, &ValidationError{
			Field:   "sshKey",
			Message: "cannot be empty",
		}
	}

	// Build Ignition v3.2 configuration with user credentials
	config := igntypes.Config{
		Ignition: igntypes.Ignition{
			Version: igntypes.MaxVersion.String(),
			Timeouts: igntypes.Timeouts{
				HTTPResponseHeaders: ptr.To(ignitionHTTPTimeout),
			},
		},
		Passwd: igntypes.Passwd{
			Users: []igntypes.PasswdUser{
				{
					Name:         "core",
					PasswordHash: ptr.To(passwdHash),
					SSHAuthorizedKeys: []igntypes.SSHAuthorizedKey{
						igntypes.SSHAuthorizedKey(sshKey),
					},
				},
			},
		},
	}

	// Marshal configuration to JSON format
	byteData, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal ignition config: %w", err)
	}

	log.Debugf("Ignition config JSON size: %d bytes", len(byteData))

	// Validate size before encoding
	if err := validateIgnitionSize(byteData); err != nil {
		return nil, err
	}

	return byteData, nil
}

// validateIgnitionSize validates that the ignition configuration fits within
// OpenStack nova user data size limits when base64 encoded.
//
// Parameters:
//   - data: The JSON-encoded ignition configuration
//
// Returns:
//   - error: An error if the size exceeds limits, nil otherwise
func validateIgnitionSize(data []byte) error {
	// Encode to base64 for OpenStack nova user data format
	strData := base64.StdEncoding.EncodeToString(data)
	encodedSize := len(strData)

	// Validate size constraint for OpenStack nova user data
	// Reference: https://docs.openstack.org/nova/latest/user/metadata.html#user-data
	if encodedSize > novaUserDataMaxSize {
		overagePercent := float64(encodedSize-novaUserDataMaxSize) / float64(novaUserDataMaxSize) * 100
		return fmt.Errorf("ignition config exceeds nova user data limit: %d > %d bytes (%.1f%% over)",
			encodedSize, novaUserDataMaxSize, overagePercent)
	}

	utilizationPercent := float64(encodedSize) / float64(novaUserDataMaxSize) * 100
	log.Debugf("Base64 encoded ignition size: %d bytes (%.1f%% of %d byte limit)",
		encodedSize, utilizationPercent, novaUserDataMaxSize)

	// Warn if approaching limit (>80%)
	if utilizationPercent > 80.0 {
		log.Warnf("Ignition config is using %.1f%% of nova user data limit. Consider optimizing.", utilizationPercent)
	}

	return nil
}
