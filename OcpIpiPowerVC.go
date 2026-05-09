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

// PowerVC-Tool is the main entry point for the OpenShift IPI PowerVC deployment tool.
// It provides a command-line interface for managing OpenShift cluster deployments on PowerVC.
//
// Build instructions:
//   /bin/rm go.*; go mod init example/user/PowerVC-Tool; go mod tidy
//   go build -ldflags="-X main.version=$(git describe --always --long --dirty) -X main.release=$(git describe --tags --abbrev=0)" -o "ocp-ipi-powervc-linux-${ARCH}" *.go
//
// Usage:
//   ocp-ipi-powervc-linux-${ARCH} <command> [flags]
//
// Available commands are defined in the 'commands' registry variable.
// Run the program with -h or --help to see the full list of commands and their descriptions.

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

	// Exit codes
	exitSuccess = 0
	exitError   = 1
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
)

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

// run contains the main application logic and returns an error instead of calling os.Exit.
// This makes the code more testable and provides consistent error handling.
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
		return fmt.Errorf("no command specified")
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
	handler, exists := commandHandlers[command]
	if !exists {
		fmt.Fprintf(os.Stderr, "Error: Unknown command '%s'\n\n", args[0])
		printUsage(executableName)
		return fmt.Errorf("unknown command: %s", args[0])
	}

	// Execute the command handler
	err := handler(flagSets[command], args[1:])
	if err != nil {
		return fmt.Errorf("command '%s' failed: %w", command, err)
	}

	return nil
}

// main is the entry point for the PowerVC-Tool application.
// It calls run() and handles the exit code based on the returned error.
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
		os.Exit(exitError)
	}
	os.Exit(exitSuccess)
}
