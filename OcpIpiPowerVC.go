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

// OcpIpiPowerVC is the main entry point for the OpenShift IPI PowerVC deployment tool.
// It provides a command-line interface for managing OpenShift cluster deployments on PowerVC.
//
// # Architecture Overview
//
// This tool acts as a CLI dispatcher that coordinates multiple components:
//   - OpenShift IPI Installer: Handles cluster provisioning
//   - PowerVC/OpenStack: Provides infrastructure resources
//   - HAProxy Bastion: Load balances cluster traffic
//   - IBM Cloud DNS (optional): Manages DNS records
//   - Controller Service: Monitors and manages cluster lifecycle
//
// The tool operates in a client-server model where commands can be executed locally
// or sent to a remote controller service for execution. This enables automated
// cluster management and monitoring.  Please note that the controller server needs
// to be running somewhere on the network.
//
// # Required Environment Variables
//
// Core variables (required for most operations):
//   BASEDOMAIN         - DNS domain name for the cluster (e.g., "example.ibm.com")
//   CLOUD              - OpenStack cloud name from ~/.config/openstack/clouds.yaml
//   CLUSTER_NAME       - Name prefix for the OpenShift cluster
//   CLUSTER_DIR        - Directory for installer files and cluster state
//
// Infrastructure variables:
//   FLAVOR_NAME        - OpenStack flavor for VMs (e.g., "ocp-ipi")
//   MACHINE_TYPE       - PowerPC machine type/availability zone (e.g., "s1022")
//   NETWORK_NAME       - OpenStack network name for cluster VMs
//   SSHKEY_NAME        - OpenStack SSH key name for bastion VM
//
// Bastion/HAProxy variables:
//   BASTION_IMAGE_NAME - OS image for bastion VM (e.g., "CentOS-Stream2-GenericCloud")
//   BASTION_USERNAME   - Default username for bastion VM (e.g., "cloud-user")
//   BASTION_RSA        - Path to SSH private key for bastion access
//
// OpenShift installer variables:
//   INSTALLER_SSHKEY   - Path to SSH public key for cluster nodes (~/.ssh/id_installer_rsa.pub)
//   PULLSECRET_FILE    - Path to OpenShift pull secret file (~/.pullSecretCompact)
//
// Controller variables:
//   CONTROLLER_IP      - IP address of the controller service
//
// Optional variables:
//   IBMCLOUD_API_KEY   - IBM Cloud API key for DNS management (if using IBM Cloud DNS)
//   DHCP_*             - DHCP configuration variables (if using local DHCP server)
//
// See docs/environment-variables.md for detailed setup instructions.
//
// # Configuration Requirements
//
// Before using this tool, ensure the following are configured:
//
// 1. OpenStack/PowerVC credentials:
//    - Configure ~/.config/openstack/clouds.yaml with PowerVC credentials
//    - Test access: openstack --os-cloud=<CLOUD> server list
//
// 2. SSH keys:
//    - Generate installer key: ssh-keygen -t rsa -b 4096 -f ~/.ssh/id_installer_rsa
//    - Create OpenStack keypair: openstack keypair create --public-key ~/.ssh/id_installer_rsa.pub <SSHKEY_NAME>
//
// 3. OpenShift pull secret:
//    - Download from https://console.redhat.com/openshift/install/pull-secret
//    - Save as ~/.pullSecretCompact (single line, no whitespace)
//
// 4. OpenStack resources:
//    - Create or identify appropriate flavor, network, and image
//    - Ensure sufficient quota for cluster resources
//
// 5. DNS configuration:
//    - Either set IBMCLOUD_API_KEY for IBM Cloud DNS
//    - Or configure alternative DNS solution (CoreDNS, etc.)
//
// # Build Instructions
//
// Standard build:
//   make build
//
// Development build (reinitialize module):
//   make init
//   make build
//
// # Usage
//
//   ocp-ipi-powervc-linux-${ARCH} <command> [flags]
//   ocp-ipi-powervc-linux-${ARCH} --version
//   ocp-ipi-powervc-linux-${ARCH} --help
//
// Available commands are defined in the 'commands' registry variable.
// Run the program with -h or --help to see the full list of commands and their descriptions.
//
// # Documentation
//
// For detailed documentation, see:
//   - README.md                           - Quick start and command reference
//   - docs/README.md                      - Documentation index
//   - docs/easy-installation.md           - Simple installation guide
//   - docs/complex-installation.md        - Advanced installation scenarios
//   - docs/environment-variables.md       - Environment variable setup
//   - docs/controller.md                  - Controller service documentation
//   - docs/configure-openstack.md         - OpenStack/PowerVC configuration
//   - docs/IPI-installer.md               - IPI installer integration details
//
// # Related Packages
//
// This package coordinates with several other packages in the codebase:
//   - CmdCheckAlive.go        - Health check command implementation
//   - CmdCreateBastion.go     - Bastion VM creation
//   - CmdCreateCluster.go     - Cluster creation orchestration
//   - CmdCreateRhcos.go       - RHCOS test VM creation
//   - CmdSendMetadata.go      - Metadata management
//   - CmdWatchCreate.go       - Cluster creation monitoring
//   - CmdWatchInstallation.go - Installation progress monitoring
//   - OpenStack.go            - OpenStack API interactions
//   - IBMCloud.go             - IBM Cloud DNS management
//   - LoadBalancer.go         - HAProxy configuration
//   - Services.go             - Service management utilities

package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// Command name constants
	cmdCheckAlive        = "check-alive"
	cmdCreateBastion     = "create-bastion"
	cmdCreateRhcos       = "create-rhcos"
	cmdCreateCluster     = "create-cluster"
	cmdSendMetadata      = "send-metadata"
	cmdWatchInstallation = "watch-installation"
	cmdWatchCreate       = "watch-create"

	// Version flags
	versionFlag  = "-version"
	versionFlag2 = "--version"

	// Help flags
	helpFlag  = "-help"
	helpFlag2 = "--help"
	helpFlag3 = "-h"

	// Exit codes - Different codes allow scripts and automation to distinguish failure types
	exitSuccess         = 0 // Command completed successfully
	exitError           = 1 // Generic error
	exitInvalidArgs     = 2 // Invalid command-line arguments
	exitCommandNotFound = 3 // Unknown command specified
	exitCommandFailed   = 4 // Command execution failed
)

// Command represents a CLI command with its metadata.
// It is used in the command registry to provide a single source of truth
// for command information displayed in help text and usage messages.
//
// Fields:
//   - Name: The command name as it appears on the command line (e.g., "check-alive")
//   - Description: A brief description of what the command does, shown in help output
//
// Example:
//   cmd := Command{
//       Name:        "check-alive",
//       Description: "Check if cluster nodes are alive",
//   }
type Command struct {
	Name        string
	Description string
}

// CommandHandler is a function type for command handler functions.
// Each handler receives a FlagSet for parsing command-specific flags
// and a slice of arguments to process.
//
// Parameters:
//   - flags: FlagSet configured for the specific command
//   - args: Command-line arguments to parse and process
//
// Returns:
//   - error: Any error encountered during command execution, nil on success
type CommandHandler func(*flag.FlagSet, []string) error

var (
	// version is the build version, replaced at build time with:
	//   -ldflags="-X main.version=$(git describe --always --long --dirty)"
	version = "undefined"

	// release is the release tag, replaced at build time with:
	//   -ldflags="-X main.release=$(git describe --tags --abbrev=0)"
	release = "undefined"

	// commands is the registry of all available commands.
	// This serves as the single source of truth for command information.
	commands = []Command{
		{cmdCheckAlive,        "Check if cluster nodes are alive"},
		{cmdCreateBastion,     "Create bastion host"},
		{cmdCreateRhcos,       "Create RHCOS image"},
		{cmdCreateCluster,     "Create OpenShift cluster"},
		{cmdSendMetadata,      "Send metadata to cluster"},
		{cmdWatchInstallation, "Watch cluster installation progress"},
		{cmdWatchCreate,       "Watch cluster creation process"},
	}

	// commandHandlers maps command names to their handler functions.
	// This registry pattern allows for easy addition of new commands without
	// modifying the dispatch logic, following the Open/Closed Principle.
	commandHandlers = map[string]CommandHandler{
		cmdCheckAlive:        checkAliveCommand,
		cmdCreateBastion:     createBastionCommand,
		cmdCreateCluster:     createClusterCommand,
		cmdCreateRhcos:       createRhcosCommand,
		cmdSendMetadata:      sendMetadataCommand,
		cmdWatchInstallation: watchInstallationCommand,
		cmdWatchCreate:       watchCreateClusterCommand,
	}

	// maxCommandLength is calculated from the longest command name in the registry.
	// This is computed at initialization to provide a reasonable validation limit.
	maxCommandLength = calculateMaxCommandLength()
)

// calculateMaxCommandLength returns the length of the longest command name plus a buffer.
// This allows validation to adapt automatically as commands are added or removed.
func calculateMaxCommandLength() int {
	maxLen := 0
	for _, cmd := range commands {
		if len(cmd.Name) > maxLen {
			maxLen = len(cmd.Name)
		}
	}
	// Add 50% buffer for future commands (e.g., 18 chars -> 27 chars max)
	return maxLen + (maxLen / 2)
}

// printUsage displays the program usage information to stderr.
// It uses the command registry to ensure consistency between documentation and runtime behavior.
//
// Parameters:
//   - executableName: Name of the executable binary
func printUsage(executableName string) {
	fmt.Fprintf(os.Stderr, "Program version is %v, release = %v\n", version, release)
	fmt.Fprintf(os.Stderr, "\n")
	fmt.Fprintf(os.Stderr, "Usage: %s <command> [flags]\n", executableName)
	fmt.Fprintf(os.Stderr, "\n")
	fmt.Fprintf(os.Stderr, "Available commands:\n")
	for _, cmd := range commands {
		fmt.Fprintf(os.Stderr, "  %-20s %s\n", cmd.Name, cmd.Description)
	}
	fmt.Fprintf(os.Stderr, "\n")
	fmt.Fprintf(os.Stderr, "Use '%s <command> -h' for more information about a command.\n", executableName)
}

// ErrorType represents different categories of errors with associated exit codes.
type ErrorType int

const (
	// ErrorTypeNone indicates no error (success)
	ErrorTypeNone ErrorType = iota
	// ErrorTypeInvalidArgs indicates invalid command-line arguments
	ErrorTypeInvalidArgs
	// ErrorTypeCommandNotFound indicates an unknown command was specified
	ErrorTypeCommandNotFound
	// ErrorTypeCommandFailed indicates the command execution failed
	ErrorTypeCommandFailed
	// ErrorTypeGeneric indicates a generic error
	ErrorTypeGeneric
)

// AppError wraps an error with a specific error type for proper exit code handling.
type AppError struct {
	Type ErrorType
	Err  error
}

// Error implements the error interface.
func (e *AppError) Error() string {
	return e.Err.Error()
}

// Unwrap implements the errors.Unwrap interface.
func (e *AppError) Unwrap() error {
	return e.Err
}

// NewAppError creates a new AppError with the specified type and error.
func NewAppError(errType ErrorType, err error) *AppError {
	return &AppError{Type: errType, Err: err}
}

// run contains the main application logic and returns an error instead of calling os.Exit.
// This makes the code more testable and provides consistent error handling.
// The returned error may be an *AppError with a specific error type for proper exit code handling.
//
// Parameters:
//   - args: Command-line arguments (excluding the program name)
//   - executableName: Name of the executable for usage messages
//
// Returns:
//   - error: Any error encountered during execution, nil on success
func run(args []string, executableName string) error {
	// Handle no arguments case
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "Error: No command specified\n\n")
		printUsage(executableName)
		return NewAppError(ErrorTypeInvalidArgs, fmt.Errorf("no command specified"))
	}

	// Handle version and help flags (check only first argument for efficiency)
	firstArg := args[0]
	switch firstArg {
	case versionFlag, versionFlag2:
		fmt.Fprintf(os.Stdout, "version = %v\nrelease = %v\n", version, release)
		return nil
	case helpFlag, helpFlag2, helpFlag3:
		printUsage(executableName)
		return nil
	}

	// Initialize flag sets for each command using a map to reduce repetition
	flagSets := make(map[string]*flag.FlagSet)
	for _, cmd := range commands {
		flagSets[cmd.Name] = flag.NewFlagSet(cmd.Name, flag.ContinueOnError)
	}

	// Dispatch to appropriate command handler using the registry pattern
	command := strings.ToLower(args[0])

	// Validate command name to prevent malformed input
	// Max length is calculated from longest command name + 50% buffer
	if len(command) > maxCommandLength || strings.ContainsAny(command, "/\\<>|&;$`\"'") {
		fmt.Fprintf(os.Stderr, "Error: Invalid command name '%s'\n\n", args[0])
		printUsage(executableName)
		return NewAppError(ErrorTypeInvalidArgs, fmt.Errorf("invalid command name: %s", args[0]))
	}

	handler, exists := commandHandlers[command]
	if !exists {
		fmt.Fprintf(os.Stderr, "Error: Unknown command '%s'\n\n", args[0])
		printUsage(executableName)
		return NewAppError(ErrorTypeCommandNotFound, fmt.Errorf("unknown command: %s", args[0]))
	}

	// Execute the command handler
	err := handler(flagSets[command], args[1:])
	if err != nil {
		return NewAppError(ErrorTypeCommandFailed, fmt.Errorf("command '%s' failed: %w", command, err))
	}

	return nil
}

// main is the entry point for the PowerVC-Tool application.
// It calls run() and handles the exit code based on the returned error type.
// Different exit codes allow scripts and automation to distinguish between failure types:
//   - 0: Success
//   - 1: Generic error
//   - 2: Invalid command-line arguments
//   - 3: Unknown command specified
//   - 4: Command execution failed
func main() {
	// Get executable name for usage messages
	executablePath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Failed to get executable path: %v\n", err)
		os.Exit(exitError)
	}
	executableName := filepath.Base(executablePath)

	// Call run with args (excluding program name) and executable name
	if err := run(os.Args[1:], executableName); err != nil {
		// Error message already printed by run() or command functions
		// Determine exit code based on error type
		if appErr, ok := err.(*AppError); ok {
			switch appErr.Type {
			case ErrorTypeInvalidArgs:
				os.Exit(exitInvalidArgs)
			case ErrorTypeCommandNotFound:
				os.Exit(exitCommandNotFound)
			case ErrorTypeCommandFailed:
				os.Exit(exitCommandFailed)
			default:
				os.Exit(exitError)
			}
		}
		// Generic error if not an AppError
		os.Exit(exitError)
	}
	os.Exit(exitSuccess)
}
