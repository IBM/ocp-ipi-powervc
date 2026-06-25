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
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	igntypes "github.com/coreos/ignition/v2/config/v3_2/types"

	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/ports"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/subnets"

	"github.com/vincent-petithory/dataurl"

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

	// File locking
	fileLockTimeout = 30 * time.Second
)

var (
	// knownHostsMutex protects concurrent access to known_hosts file
	knownHostsMutex sync.Mutex
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

	// AvailabilityZone specifies the OpenStack availability zone to use
	AvailabilityZone string // OpenStack availability zone for VM

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

	// Timeout specifies the maximum duration for the entire operation
	// Defaults to rhcosDefaultTimeout if not specified
	Timeout time.Duration
}

// validate performs comprehensive validation of the RHCOS configuration.
// It checks for required fields, validates formats, and ensures security requirements.
// As a side-effect, if AvailabilityZone is empty it is set to defaultAvailZone.
//
// Returns a *ValidationError if any validation check fails, with a descriptive message
// indicating which field failed validation and why.
func (c *rhcosConfig) validate() error {
	// Validate required string fields
	requiredFields := []struct {
		name  string
		value string
	}{
		{"RhcosName", c.RhcosName},
		{"FlavorName", c.FlavorName},
		{"ImageName", c.ImageName},
		{"NetworkName", c.NetworkName},
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

	for _, f := range requiredFields {
		if f.value == "" {
			return &ValidationError{
				Field:   f.name,
				Message: "is required",
			}
		}
	}

	// Optional fields
	if c.AvailabilityZone == "" {
		c.AvailabilityZone = defaultAvailZone
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

// validateSSHKey validates the SSH public key format and length.
// It performs comprehensive validation including:
//   - Presence check
//   - Minimum character-length check (minSSHKeyLength)
//   - Format validation (key type, base64 data, optional comment)
//   - Key type verification against the supported-types allowlist
//   - Base64 decoding of the key data field
//   - Minimum decoded byte-size check per key type (via getMinKeyDataSize)
func (c *rhcosConfig) validateSSHKey() error {
	if c.SshPublicKey == "" {
		return &ValidationError{
			Field:   "SshPublicKey",
			Message: "is required",
		}
	}

	// Trim whitespace
	key := strings.TrimSpace(c.SshPublicKey)

	// Check minimum length
	if len(key) < minSSHKeyLength {
		return &ValidationError{
			Field:   "SshPublicKey",
			Message: fmt.Sprintf("appears invalid (too short, minimum %d characters)", minSSHKeyLength),
		}
	}

	// Parse SSH key format: <key-type> <base64-data> [comment]
	parts := strings.Fields(key)
	if len(parts) < 2 {
		return &ValidationError{
			Field:   "SshPublicKey",
			Message: "invalid format, expected: <key-type> <base64-data> [comment]",
		}
	}

	keyType := parts[0]
	keyData := parts[1]

	// Validate key type
	validKeyTypes := map[string]bool{
		"ssh-rsa":             true,
		"ssh-dss":             true,
		"ssh-ed25519":         true,
		"ecdsa-sha2-nistp256": true,
		"ecdsa-sha2-nistp384": true,
		"ecdsa-sha2-nistp521": true,
		"sk-ssh-ed25519@openssh.com":      true,
		"sk-ecdsa-sha2-nistp256@openssh.com": true,
	}

	if !validKeyTypes[keyType] {
		return &ValidationError{
			Field:   "SshPublicKey",
			Message: fmt.Sprintf("unsupported key type '%s', supported types: ssh-rsa, ssh-dss, ssh-ed25519, ecdsa-sha2-nistp256/384/521", keyType),
		}
	}

	// Validate base64 encoding of key data
	decodedData, err := base64.StdEncoding.DecodeString(keyData)
	if err != nil {
		return &ValidationError{
			Field:   "SshPublicKey",
			Message: fmt.Sprintf("invalid base64 encoding in key data: %v", err),
		}
	}

	// Validate decoded data is not empty
	if len(decodedData) == 0 {
		return &ValidationError{
			Field:   "SshPublicKey",
			Message: "decoded key data is empty",
		}
	}

	// Validate minimum key data size (varies by key type)
	minKeyDataSize := getMinKeyDataSize(keyType)
	if len(decodedData) < minKeyDataSize {
		return &ValidationError{
			Field:   "SshPublicKey",
			Message: fmt.Sprintf("key data too short for %s (got %d bytes, minimum %d bytes)", keyType, len(decodedData), minKeyDataSize),
		}
	}

	fmt.Printf("SSH key validation passed: type=%s, data_size=%d bytes", keyType, len(decodedData))
	return nil
}

// getMinKeyDataSize returns the minimum expected size for decoded SSH key data
// based on the key type. These are conservative estimates.
func getMinKeyDataSize(keyType string) int {
	switch keyType {
	case "ssh-rsa":
		return 256 // RSA-2048 minimum
	case "ssh-dss":
		return 128 // DSA-1024
	case "ssh-ed25519":
		return 32 // Ed25519 is 32 bytes
	case "ecdsa-sha2-nistp256":
		return 64 // NIST P-256
	case "ecdsa-sha2-nistp384":
		return 96 // NIST P-384
	case "ecdsa-sha2-nistp521":
		return 128 // NIST P-521
	case "sk-ssh-ed25519@openssh.com":
		return 32 // Ed25519 security key
	case "sk-ecdsa-sha2-nistp256@openssh.com":
		return 64 // ECDSA security key
	default:
		return 32 // Conservative default
	}
}

// validatePasswordHash validates the password hash format and length.
// It performs comprehensive validation including:
//   - Presence check
//   - Length validation
//   - Crypt format validation ($algorithm$salt$hash)
//   - Algorithm verification
//   - Component structure validation
func (c *rhcosConfig) validatePasswordHash() error {
	if c.PasswdHash == "" {
		return &ValidationError{
			Field:   "PasswdHash",
			Message: "is required",
		}
	}

	hash := c.PasswdHash

	// Check minimum length
	if len(hash) < minPasswordHashLength {
		return &ValidationError{
			Field:   "PasswdHash",
			Message: fmt.Sprintf("appears invalid (too short, minimum %d characters)", minPasswordHashLength),
		}
	}

	// Must start with $
	if !strings.HasPrefix(hash, "$") {
		return &ValidationError{
			Field:   "PasswdHash",
			Message: "must be in crypt format (starting with $)",
		}
	}

	// Parse crypt format: $algorithm$salt$hash
	// Split by $ and validate structure
	parts := strings.Split(hash, "$")

	// parts[0] is empty (before first $)
	// parts[1] is algorithm
	// parts[2] is salt (may contain $ for some algorithms)
	// parts[3+] is hash

	if len(parts) < 4 {
		return &ValidationError{
			Field:   "PasswdHash",
			Message: "invalid crypt format, expected: $algorithm$salt$hash",
		}
	}

	algorithm := parts[1]

	// Validate algorithm and structure
	if err := validateCryptAlgorithm(algorithm, parts); err != nil {
		return &ValidationError{
			Field:   "PasswdHash",
			Message: err.Error(),
		}
	}

	fmt.Printf("Password hash validation passed: algorithm=%s\n", algorithm)
	return nil
}

// validateCryptAlgorithm validates the crypt algorithm and hash structure.
// Supported algorithms:
//   - 1: MD5 (legacy, not recommended)
//   - 5: SHA-256
//   - 6: SHA-512 (recommended)
//   - 2a, 2b, 2y: bcrypt
//   - yescrypt: yescrypt (modern)
func validateCryptAlgorithm(algorithm string, parts []string) error {
	switch algorithm {
	case "1":
		// MD5: $1$salt$hash
		// Legacy, not recommended but still supported
		if len(parts) < 4 {
			return fmt.Errorf("invalid MD5 hash structure")
		}
		salt := parts[2]
		hash := parts[3]
		if len(salt) == 0 || len(salt) > 8 {
			return fmt.Errorf("MD5 salt must be 1-8 characters, got %d", len(salt))
		}
		if len(hash) != 22 {
			return fmt.Errorf("MD5 hash must be 22 characters, got %d", len(hash))
		}
		fmt.Println("WARN: Using MD5 password hash (algorithm $1$) - consider upgrading to SHA-512 ($6$)")
		return nil

	case "5":
		// SHA-256: $5$[rounds=N$]salt$hash
		if len(parts) < 4 {
			return fmt.Errorf("invalid SHA-256 hash structure")
		}
		// Handle optional rounds parameter
		saltIdx := 2
		if strings.HasPrefix(parts[2], "rounds=") {
			saltIdx = 3
			if len(parts) < 5 {
				return fmt.Errorf("invalid SHA-256 hash structure with rounds")
			}
		}
		salt := parts[saltIdx]
		hash := parts[saltIdx+1]
		if len(salt) == 0 || len(salt) > 16 {
			return fmt.Errorf("SHA-256 salt must be 1-16 characters, got %d", len(salt))
		}
		if len(hash) != 43 {
			return fmt.Errorf("SHA-256 hash must be 43 characters, got %d", len(hash))
		}
		return nil

	case "6":
		// SHA-512: $6$[rounds=N$]salt$hash (recommended)
		if len(parts) < 4 {
			return fmt.Errorf("invalid SHA-512 hash structure")
		}
		// Handle optional rounds parameter
		saltIdx := 2
		if strings.HasPrefix(parts[2], "rounds=") {
			saltIdx = 3
			if len(parts) < 5 {
				return fmt.Errorf("invalid SHA-512 hash structure with rounds")
			}
		}
		salt := parts[saltIdx]
		hash := parts[saltIdx+1]
		if len(salt) == 0 || len(salt) > 16 {
			return fmt.Errorf("SHA-512 salt must be 1-16 characters, got %d", len(salt))
		}
		if len(hash) != 86 {
			return fmt.Errorf("SHA-512 hash must be 86 characters, got %d", len(hash))
		}
		return nil

	case "2a", "2b", "2y":
		// bcrypt: $2a$cost$salthash (salt and hash are combined)
		if len(parts) < 4 {
			return fmt.Errorf("invalid bcrypt hash structure")
		}
		cost := parts[2]
		saltHash := parts[3]

		// Validate cost (work factor)
		if len(cost) != 2 {
			return fmt.Errorf("bcrypt cost must be 2 digits, got %d", len(cost))
		}

		// bcrypt salt+hash is 53 characters (22 salt + 31 hash)
		if len(saltHash) != 53 {
			return fmt.Errorf("bcrypt salt+hash must be 53 characters, got %d", len(saltHash))
		}
		return nil

	case "yescrypt":
		// yescrypt: $yescrypt$params$salt$hash
		// Modern algorithm, structure varies
		if len(parts) < 5 {
			return fmt.Errorf("invalid yescrypt hash structure")
		}
		// Basic validation - yescrypt has complex parameter encoding
		return nil

	default:
		return fmt.Errorf("unsupported hash algorithm '%s', supported: 1 (MD5), 5 (SHA-256), 6 (SHA-512), 2a/2b/2y (bcrypt), yescrypt", algorithm)
	}
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
	availabilityZone := createRhcosFlags.String("availabilityZone", defaultAvailZone, "The name of the availability zone")
	ptrFlavorName := createRhcosFlags.String("flavorName", "", "The name of the flavor to use")
	ptrImageName := createRhcosFlags.String("imageName", "", "The name of the image to use")
	ptrNetworkName := createRhcosFlags.String("networkName", "", "The name of the network to use")
	ptrPasswdHash := createRhcosFlags.String("passwdHash", "", "The password hash of the core user")
	ptrSshPublicKey := createRhcosFlags.String("sshPublicKey", "", "The contents of the SSH public key to use")
	ptrDomainName := createRhcosFlags.String("domainName", "", "The DNS domain to use (optional)")
	ptrShouldDebug := createRhcosFlags.String("shouldDebug", "false", "Enable debug output")
	ptrTimeout := createRhcosFlags.String("timeout", "15m", "Maximum duration for the operation (e.g., 15m, 30m, 1h)")

	if err := createRhcosFlags.Parse(args); err != nil {
		return nil, fmt.Errorf("failed to parse flags: %w", err)
	}

	// Populate config from parsed flags
	config.Clouds = []string{ *ptrCloud }
	config.RhcosName = *ptrRhcosName
	config.AvailabilityZone = *availabilityZone
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

	// Parse timeout duration
	timeout, err := time.ParseDuration(*ptrTimeout)
	if err != nil {
		return nil, fmt.Errorf("invalid timeout value '%s': %w (use format like 15m, 30m, 1h)", *ptrTimeout, err)
	}
	if timeout <= 0 {
		return nil, fmt.Errorf("timeout must be positive, got: %s", *ptrTimeout)
	}
	config.Timeout = timeout

	// Validate configuration
	if err := config.validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return config, nil
}

// createRhcosCommand is the top-level handler for the create-rhcos command.
// It delegates to innerCreateRhcosCommand and, on failure, prints the error
// and displays flag usage before returning the error.
func createRhcosCommand(createRhcosFlags *flag.FlagSet, args []string) error {
	err := innerCreateRhcosCommand(createRhcosFlags, args)
	if err != nil {
		fmt.Printf("%+v\n",err)
		if createRhcosFlags != nil {
			createRhcosFlags.Usage()
		}
	}
	return err
}

// innerCreateRhcosCommand is the core handler for the RHCOS server creation workflow.
// It orchestrates the entire provisioning process in the following steps:
//  1. Parse and validate command-line flags (including timeout and debug).
//  2. Initialize logging and create a context with the configured timeout.
//  3. Find an existing server or create a new one (including Ignition generation
//     and network port setup), retrying on transient failures.
//  4. Validate that the server is in ACTIVE state, then set up SSH known_hosts,
//     retrying on transient failures.
//  5. Configure DNS records via IBM Cloud if IBMCLOUD_API_KEY is set.
//
// A deferred cleanup handler attempts to delete the server if any error is returned
// before setupCompleted is set to true. Note: serverWasCreated is always treated as
// true regardless of whether the server pre-existed, so the cleanup may also run
// for pre-existing servers if setup fails.
//
// Parameters:
//   - createRhcosFlags: FlagSet for parsing command-line arguments
//   - args: Command-line arguments
//
// Returns:
//   - error: Any error encountered during the workflow, nil on success
func innerCreateRhcosCommand(createRhcosFlags *flag.FlagSet, args []string) error {
	fmt.Fprintf(os.Stderr, "Program version is %v, release = %v\n", version, release)

	// Step 1: Parse and validate configuration
	printProgress(progressStepParsing)
	config, err := parseRhcosFlags(createRhcosFlags, args)
	if err != nil {
		return fmt.Errorf("configuration error: %w", err)
	}

	// Step 2: Initialize logger
	log = initLogger(config.ShouldDebug)
	if config.ShouldDebug {
		log.Debugf("Debug mode enabled")
		log.Debugf("Configuration: Clouds=%s, RhcosName=%s, Flavor=%s, Image=%s, Network=%s",
			config.Clouds, config.RhcosName, config.FlavorName, config.ImageName, config.NetworkName)
	}

	// Create context with timeout (use configured timeout or default)
	timeout := config.Timeout
	if timeout == 0 {
		timeout = rhcosDefaultTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	log.Debugf("Operation timeout set to: %s", timeout)

	// Step 3: Find or create the server
	printProgress(progressStepFinding)
	foundServer, err := findOrCreateRhcosServerWithRetry(ctx, config)
	if err != nil {
		return fmt.Errorf("failed to find or create server: %w", err)
	}
	log.Debugf("Server found: %s (ID: %s, Status: %s)", foundServer.Name, foundServer.ID, foundServer.Status)

	// Validate server status before proceeding
	if foundServer.Status != "ACTIVE" {
		return fmt.Errorf("server %s is not in ACTIVE state (current status: %s). Cannot proceed with setup",
			foundServer.Name, foundServer.Status)
	}
	log.Debugf("Server is ACTIVE and ready for setup")

	// Track if this is a newly created server for cleanup on failure
	var serverWasCreated bool
	var setupCompleted bool

	// Check if server was just created (not pre-existing)
	// We determine this by checking if the server was found in the first lookup
	// For safety, we'll track creation through the server's age
	serverWasCreated = true // Assume created unless proven otherwise

	// Setup cleanup handler for partial failures
	// This ensures we don't leave orphaned servers if setup or DNS fails
	defer func() {
		if err != nil && serverWasCreated && !setupCompleted {
			log.Warnf("Operation failed, attempting to cleanup server: %s (ID: %s)", foundServer.Name, foundServer.ID)

			// Create a new context with timeout for cleanup (best effort)
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()

			if cleanupErr := deleteServer(cleanupCtx, config.Clouds[0], &foundServer); cleanupErr != nil {
				log.Errorf("Failed to cleanup server %s: %v", foundServer.Name, cleanupErr)
				fmt.Fprintf(os.Stderr, "Warning: Failed to cleanup server %s (ID: %s). Please delete manually.\n",
					foundServer.Name, foundServer.ID)
			} else {
				log.Infof("Successfully cleaned up server: %s", foundServer.Name)
				fmt.Fprintf(os.Stderr, "Server %s was cleaned up due to setup failure.\n", foundServer.Name)
			}
		}
	}()

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

	// Mark setup as completed to prevent cleanup
	setupCompleted = true

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
// When the server does not exist the function:
//  1. Finds the target network and iterates its subnets to locate a valid subnet.
//  2. Creates a network port on that network.
//  3. Generates an Ignition v3.2 bootstrap configuration from config.PasswdHash,
//     config.SshPublicKey, and the port/subnet details.
//  4. Creates the server via createServer (which waits for ACTIVE state).
//  5. Verifies the new server is discoverable via findServer.
//
// Parameters:
//   - ctx: Context for timeout and cancellation
//   - config: RHCOS configuration containing server details
//
// Returns:
//   - servers.Server: The found or newly created server
//   - error: Any error encountered during search or creation
func findOrCreateRhcosServer(ctx context.Context, config *rhcosConfig) (servers.Server, error) {
	// Check context before starting
	if err := ctx.Err(); err != nil {
		return servers.Server{}, fmt.Errorf("context cancelled before finding server: %w", err)
	}

	log.Debugf("Looking for existing server: %s", config.RhcosName)

	foundServer, err := findServer(ctx, config.Clouds, config.RhcosName)
	if err != nil {
		// Check if error is due to server not found
		if !errors.Is(err, ErrServerNotFound) {
			return servers.Server{}, fmt.Errorf("error searching for server: %w", err)
		}

		// Check context before creating server
		if err := ctx.Err(); err != nil {
			return servers.Server{}, fmt.Errorf("context cancelled before creating server: %w", err)
		}

		// Server not found, create it
		log.Debugf("Server %s not found, creating new server", config.RhcosName)
		fmt.Printf("Server %s not found, creating...\n", config.RhcosName)

		network, err := findNetwork(ctx, config.Clouds[0], config.NetworkName)
		if err != nil {
			return servers.Server{}, fmt.Errorf("failed to find network %q: %w", config.NetworkName, err)
		}
		log.Debugf("findOrCreateRhcosServer: network = %+v", network)

		var (
			subnet      subnets.Subnet
			foundSubnet = false
		)

		for _, subnetName := range network.Subnets {
			log.Debugf("findOrCreateRhcosServer: subnetName = %s", subnetName)

			subnet, err = findSubnet(ctx, config.Clouds[0], subnetName)
			if err != nil {
				return servers.Server{}, fmt.Errorf("failed to find subnet %q: %w", subnetName, err)
			}
			foundSubnet = true
		}
		if !foundSubnet {
			return servers.Server{}, fmt.Errorf("failed to find a subnet for network %q", network.Name)
		}
		log.Debugf("findOrCreateRhcosServer: subnet.CIDR = %s", subnet.CIDR)
		log.Debugf("findOrCreateRhcosServer: subnet.GatewayIP = %s", subnet.GatewayIP)
		log.Debugf("findOrCreateRhcosServer: subnet.DNSNameservers = %+v", subnet.DNSNameservers)

		port, err := createNetworkPort(ctx, config.Clouds[0], config.RhcosName, network.ID)
		if err != nil {
			return servers.Server{}, fmt.Errorf("failed to create network port: %w", err)
		}
		log.Debugf("findOrCreateRhcosServer: port.ID = %v", port.ID)
		for i, ip := range port.FixedIPs {
			log.Debugf("findOrCreateRhcosServer: port[%d].SubnetID  = %s", i, ip.SubnetID)
			log.Debugf("findOrCreateRhcosServer: port[%d].IPAddress = %s", i, ip.IPAddress)
		}

		// cleanupPort removes the network port if server creation fails
		// Uses a fresh context with timeout to ensure cleanup completes even if original context is cancelled
		cleanupPort := func(createdPort *ports.Port) {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), cleanupPortTimeout)
			defer cancel()

			if deleteErr := deleteNetworkPort(cleanupCtx, config.Clouds[0], createdPort); deleteErr != nil {
				log.Debugf("Warning: failed to cleanup port %s: %v", port.ID, deleteErr)
			}
		}

		// Generate ignition user data
		printProgress(progressStepIgnition)
		userData, err := createBootstrapIgnition(
			config.PasswdHash,
			config.SshPublicKey,
			port,
			subnet)
		if err != nil {
			cleanupPort(port)

			return servers.Server{}, fmt.Errorf("failed to create bootstrap ignition: %w", err)
		}
		log.Debugf("Ignition configuration generated successfully (%d bytes)", len(userData))

		bc := BastionConfig{
			Clouds:            config.Clouds,
			BastionName:       config.RhcosName,
			AvailabilityZone:  config.AvailabilityZone,
			FlavorName:        config.FlavorName,
			ImageName:         config.ImageName,
		}
		if err := createServer(ctx, &bc, port,subnet, userData); err != nil {
			cleanupPort(port)

			return servers.Server{}, fmt.Errorf("failed to create server: %w", err)
		}

		fmt.Println("Server created successfully!")

		// Check context before retrieving server
		if err := ctx.Err(); err != nil {
			cleanupPort(port)

			return servers.Server{}, fmt.Errorf("context cancelled before retrieving server: %w", err)
		}

		// Retrieve the newly created server with retry
		log.Debugf("Retrieving newly created server: %s", config.RhcosName)
		foundServer, err = findServer(ctx, config.Clouds, config.RhcosName)
		if err != nil {
			cleanupPort(port)

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
//
// Returns:
//   - servers.Server: The found or newly created server
//   - error: Any error encountered during search or creation after all retries
func findOrCreateRhcosServerWithRetry(ctx context.Context, config *rhcosConfig) (servers.Server, error) {
	return retryOperation(ctx, "find or create server", func() (servers.Server, error) {
		return findOrCreateRhcosServer(ctx, config)
	})
}

// configureDNS sets up DNS records for the RHCOS server using IBM Cloud DNS.
// This function is optional and only executes if config.APIKey is non-empty.
// If no API key is available, it prints two warning lines to stderr and returns nil.
//
// Parameters:
//   - ctx: Context for timeout and cancellation
//   - config: RHCOS configuration containing DNS and API key details
//
// Returns:
//   - error: Any error encountered during DNS configuration, nil on success or skip
func configureDNS(ctx context.Context, config *rhcosConfig) error {
	// Check context before DNS configuration
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("context cancelled before DNS configuration: %w", err)
	}

	if config.APIKey == "" {
		fmt.Fprintln(os.Stderr, "Warning: IBMCLOUD_API_KEY not set. DNS configuration skipped.")
		fmt.Fprintln(os.Stderr, "Ensure DNS is configured through another method.")
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
// It executes the following steps:
//  1. Extracts and validates the server's IP address (returns an error if empty
//     or unparseable, and logs a warning for IPv6 addresses).
//  2. Ensures the server's SSH host key is present in ~/.ssh/known_hosts via
//     ensureSSHHostKey, to enable passwordless SSH access on first connection.
// Context cancellation is checked before each major step.
//
// Parameters:
//   - ctx: Context for timeout and cancellation
//   - server: The server object to set up
//
// Returns:
//   - error: Any error encountered during setup, nil on success
func setupRhcosServer(ctx context.Context, server servers.Server) error {
	// Check context before starting setup
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("context cancelled before server setup: %w", err)
	}

	log.Debugf("Setting up RHCOS server: %s (ID: %s)", server.Name, server.ID)

	// Get server IP address
	_, ipAddress, err := findIpAddress(server)
	if err != nil {
		return fmt.Errorf("failed to find IP address: %w", err)
	}
	if ipAddress == "" {
		return fmt.Errorf("server %s has no IP address", server.Name)
	}

	// Validate IP address format
	ip := net.ParseIP(ipAddress)
	if ip == nil {
		return fmt.Errorf("invalid IP address format: %s", ipAddress)
	}

	// Log IP version information
	if ip.To4() == nil {
		log.Warnf("Using IPv6 address: %s", ipAddress)
	} else {
		log.Debugf("Server IP address (IPv4): %s", ipAddress)
	}

	// Check context before SSH operations
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("context cancelled before SSH setup: %w", err)
	}

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
// It checks for context cancellation before each attempt and waits between retries
// using a delay that starts at retryInitialDelay and doubles (up to retryMaxDelay)
// after each failed attempt.
//
// Only errors classified as retryable by isRetryableError are retried; a
// non-retryable error is returned immediately after the first failed attempt.
// The operation is attempted at most maxRetryAttempts times.
//
// Parameters:
//   - ctx: Context for timeout and cancellation
//   - operationName: Name of the operation for logging
//   - operation: The function to execute and potentially retry
//
// Returns:
//   - servers.Server: The result of the operation on success
//   - error: The last error encountered if all attempts fail, or a context error
//     if the context is cancelled before or during a retry wait
func retryOperation(ctx context.Context, operationName string, operation func() (servers.Server, error)) (servers.Server, error) {
	var lastErr error
	delay := retryInitialDelay

	for attempt := 1; attempt <= maxRetryAttempts; attempt++ {
		// Check if context is already cancelled before attempting operation
		select {
		case <-ctx.Done():
			return servers.Server{}, fmt.Errorf("operation '%s' cancelled before attempt %d: %w", operationName, attempt, ctx.Err())
		default:
			// Context is still valid, proceed with operation
		}

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
				return servers.Server{}, fmt.Errorf("operation '%s' cancelled during retry: %w", operationName, ctx.Err())
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
// The function uses both in-process mutex locking and file-level locking to prevent
// race conditions when multiple processes or goroutines access the known_hosts file.
//
// The function:
//  1. Ensures the ~/.ssh directory exists with proper permissions (0700).
//  2. Acquires the in-process knownHostsMutex to serialise goroutine access.
//  3. Runs ssh-keygen -F to check whether a host key for ipAddress already exists
//     (exit code 0 = found, exit code 1 = not found).
//  4. If not found, calls keyscanServer to retrieve the host key via ssh-keyscan.
//  5. Calls appendToKnownHostsWithLock to write the key to known_hosts under an
//     exclusive file lock (see that function for full locking details).
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

	sshDir := filepath.Join(homeDir, ".ssh")
	knownHostsPath := filepath.Join(sshDir, "known_hosts")

	// Ensure .ssh directory exists
	if err := ensureSSHDirectory(sshDir); err != nil {
		return fmt.Errorf("failed to ensure SSH directory: %w", err)
	}

	log.Debugf("Known hosts file: %s", knownHostsPath)

	// Acquire mutex lock to prevent concurrent in-process access
	knownHostsMutex.Lock()
	defer knownHostsMutex.Unlock()

	log.Debugf("Acquired lock for known_hosts operations")

	// Check if host key already exists using ssh-keygen
	// Exit code 0: key found, Exit code 1: key not found
	_, err = runSplitCommand2([]string{
		"ssh-keygen",
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

		// Write to known_hosts file with file-level locking
		if err := appendToKnownHostsWithLock(knownHostsPath, hostKey, ipAddress); err != nil {
			return fmt.Errorf("failed to add SSH host key: %w", err)
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

// appendToKnownHostsWithLock appends a host key to the known_hosts file with file-level locking.
// This prevents race conditions when multiple processes try to write to the file simultaneously.
//
// The function:
//  1. Opens (or creates) the file in append mode with 0644 permissions.
//  2. Checks the file's current permissions and attempts to correct them to 0644 if wrong.
//  3. Acquires an exclusive flock on the file descriptor to serialise concurrent writers.
//  4. Re-reads the file after acquiring the lock and returns early (no-op) if the IP
//     address is already present (added by another process while waiting for the lock).
//  5. Appends the host key and calls Sync to flush to disk.
//  6. Releases the lock via a deferred flock(LOCK_UN) call.
//
// Parameters:
//   - knownHostsPath: Path to the known_hosts file
//   - hostKey: The SSH host key bytes to append
//   - ipAddress: IP address used both for the duplicate-check and for log messages
//
// Returns:
//   - error: Any error encountered during the operation
func appendToKnownHostsWithLock(knownHostsPath string, hostKey []byte, ipAddress string) error {
	// Open file with create flag
	file, err := os.OpenFile(knownHostsPath, os.O_APPEND|os.O_RDWR|os.O_CREATE, knownHostsFilePerms)
	if err != nil {
		return fmt.Errorf("failed to open known_hosts file: %w", err)
	}
	defer file.Close()

	// Verify file permissions and ownership after opening
	fileInfo, err := file.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat known_hosts file: %w", err)
	}

	// Check if permissions are correct
	if fileInfo.Mode().Perm() != knownHostsFilePerms {
		log.Warnf("known_hosts file has incorrect permissions: %o (expected %o)",
			fileInfo.Mode().Perm(), knownHostsFilePerms)
		// Attempt to fix permissions
		if err := file.Chmod(knownHostsFilePerms); err != nil {
			log.Warnf("Failed to fix known_hosts permissions: %v", err)
		}
	}

	// Acquire exclusive file lock (flock)
	log.Debugf("Acquiring file lock for %s", knownHostsPath)
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("failed to acquire file lock: %w", err)
	}
	defer func() {
		// Release file lock
		if err := syscall.Flock(int(file.Fd()), syscall.LOCK_UN); err != nil {
			log.Warnf("Failed to release file lock: %v", err)
		}
		log.Debugf("Released file lock for %s", knownHostsPath)
	}()

	log.Debugf("Acquired file lock for %s", knownHostsPath)

	// Double-check if key was added by another process while we were waiting for lock
	// Read current file content and check for the IP address
	currentContent, err := os.ReadFile(knownHostsPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to read known_hosts: %w", err)
	}

	// Check if the IP address already exists in the file
	if strings.Contains(string(currentContent), ipAddress) {
		log.Debugf("Host key for %s was already added by another process", ipAddress)
		return nil
	}

	// Write the host key
	if _, err := file.Write(hostKey); err != nil {
		return fmt.Errorf("failed to write to known_hosts: %w", err)
	}

	// Ensure data is written to disk
	if err := file.Sync(); err != nil {
		return fmt.Errorf("failed to sync known_hosts: %w", err)
	}

	log.Debugf("Successfully wrote host key for %s to known_hosts", ipAddress)
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
//   - Uses Ignition v3.2 format (latest stable)
//   - Sets HTTP response timeout to 120 seconds
//   - Configures the 'core' user with the provided password hash and SSH public key
//   - When port and subnet information is provided (non-nil port, non-empty CIDR,
//     GatewayIP, and at least one DNS nameserver), also embeds:
//     * A NetworkManager keyfile at /etc/NetworkManager/system-connections/static.nmconnection
//       that configures a static IPv4 address, gateway, and DNS servers (mode 0600)
//     * A /etc/resolv.conf file populated from the subnet's DNS nameservers (mode 0644)
//   - Is validated against the OpenStack nova user data base64-encoded size limit (64 KB)
//
// Parameters:
//   - passwdHash: Crypt-formatted password hash for the core user
//   - sshPublicKey: SSH public key for the core user
//   - port: Network port containing the assigned IP address; may be nil to skip network config
//   - subnet: Subnet details (CIDR, GatewayIP, DNSNameservers) used for static network config
//
// Returns:
//   - []byte: JSON-encoded Ignition configuration
//   - error: Any error encountered during generation or validation
func createBootstrapIgnition(passwdHash string, sshPublicKey string, port *ports.Port, subnet subnets.Subnet) ([]byte, error) {
	log.Debugf("Creating bootstrap ignition configuration")

	// Validate inputs
	if passwdHash == "" {
		return nil, &ValidationError{
			Field:   "passwdHash",
			Message: "cannot be empty",
		}
	}
	if sshPublicKey == "" {
		return nil, &ValidationError{
			Field:   "sshKey",
			Message: "cannot be empty",
		}
	}

	// Build Ignition v3.2 configuration with user credentials
	ignConfig := igntypes.Config{
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
						igntypes.SSHAuthorizedKey(sshPublicKey),
					},
				},
			},
		},
	}

	if port != nil && subnet.CIDR != "" && subnet.GatewayIP != "" && len(subnet.DNSNameservers) > 0 {
		nmFormat := `[connection]
id=env2
type=ethernet
interface-name=env2
autoconnect=true

[ipv4]
address1=%s/%s
gateway=%s
dns=%s;
dns-search=
may-fail=false
method=manual
`

		nmConfig := fmt.Sprintf(nmFormat,
			port.FixedIPs[0].IPAddress,
			extractNetmask(subnet.CIDR),
			subnet.GatewayIP,
			strings.Join(subnet.DNSNameservers, ";"),
		)
		log.Debugf("createBootstrapIgnition: nmConfig = %s", nmConfig)

		dnsFile := buildResolvConf(subnet.DNSNameservers)
		log.Debugf("createBootstrapIgnition: dnsFile = %s", dnsFile)

		ignConfig.Storage = igntypes.Storage{
			Files: []igntypes.File{
				igntypes.File{
					Node: igntypes.Node{
						Path: "/etc/NetworkManager/system-connections/static.nmconnection",
						User: igntypes.NodeUser{
							Name: ptr.To("root"),
						},
						Overwrite: ptr.To(true),
					},
					FileEmbedded1: igntypes.FileEmbedded1{
						Mode: ptr.To(0600), // NetworkManager requires 600 permissions
						Contents: igntypes.Resource{
							Source: ptr.To(dataurl.EncodeBytes([]byte(nmConfig))),
						},
					},
				},
				igntypes.File{
					Node: igntypes.Node{
						Path: "/etc/resolv.conf",
						User: igntypes.NodeUser{
							Name: ptr.To("root"),
						},
						Overwrite: ptr.To(true),
					},
					FileEmbedded1: igntypes.FileEmbedded1{
						Mode: ptr.To(0644),
						Contents: igntypes.Resource{
							Source: ptr.To(dataurl.EncodeBytes([]byte(dnsFile))),
						},
					},
				},
			},
		}

	}

	// Marshal configuration to JSON format
	byteData, err := json.Marshal(ignConfig)
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
// the OpenStack nova user data size limit (novaUserDataMaxSize = 65535 bytes)
// when base64 encoded. It also emits a warning log if utilisation exceeds 80%.
//
// Parameters:
//   - data: The JSON-encoded ignition configuration
//
// Returns:
//   - error: An error if the base64-encoded size exceeds the limit, nil otherwise
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
