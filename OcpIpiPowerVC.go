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

// Command represents a CLI command with its name and description.
type Command struct {
	Name        string
	Description string
}

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
// Returns:
//   - error: Any error encountered during execution, nil on success
func run() error {
	var (
		executableName          string
		checkAliveFlags         *flag.FlagSet
		createBastionFlags      *flag.FlagSet
		createClusterFlags      *flag.FlagSet
		createRhcosFlags        *flag.FlagSet
		sendMetadataFlags       *flag.FlagSet
		watchInstallationFlags  *flag.FlagSet
		watchCreateClusterFlags *flag.FlagSet
		err                     error
	)

	// Get executable name for usage messages
	executablePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}
	executableName = filepath.Base(executablePath)

	// Handle no arguments case
	if len(os.Args) == 1 {
		fmt.Fprintf(os.Stderr, "Error: No command specified\n\n")
		printUsage(executableName)
		return fmt.Errorf("no command specified")
	}

	// Handle version and help flags
	for _, arg := range os.Args[1:] {
		if arg == versionFlag || arg == versionFlag2 {
			fmt.Fprintf(os.Stdout, "version = %v\nrelease = %v\n", version, release)
			return nil
		}
		if arg == helpFlag || arg == helpFlag2 || arg == helpFlag3 {
			printUsage(executableName)
			return nil
		}
	}

	// Initialize flag sets for each command
	checkAliveFlags = flag.NewFlagSet(cmdCheckAlive, flag.ContinueOnError)
	createBastionFlags = flag.NewFlagSet(cmdCreateBastion, flag.ContinueOnError)
	createClusterFlags = flag.NewFlagSet(cmdCreateCluster, flag.ContinueOnError)
	createRhcosFlags = flag.NewFlagSet(cmdCreateRhcos, flag.ContinueOnError)
	sendMetadataFlags = flag.NewFlagSet(cmdSendMetadata, flag.ContinueOnError)
	watchInstallationFlags = flag.NewFlagSet(cmdWatchInstallation, flag.ContinueOnError)
	watchCreateClusterFlags = flag.NewFlagSet(cmdWatchCreate, flag.ContinueOnError)

	// Dispatch to appropriate command handler
	command := strings.ToLower(os.Args[1])
	switch command {
	case cmdCheckAlive:
		err = checkAliveCommand(checkAliveFlags, os.Args[2:])

	case cmdCreateBastion:
		err = createBastionCommand(createBastionFlags, os.Args[2:])

	case cmdCreateCluster:
		err = createClusterCommand(createClusterFlags, os.Args[2:])

	case cmdCreateRhcos:
		err = createRhcosCommand(createRhcosFlags, os.Args[2:])

	case cmdSendMetadata:
		err = sendMetadataCommand(sendMetadataFlags, os.Args[2:])

	case cmdWatchInstallation:
		err = watchInstallationCommand(watchInstallationFlags, os.Args[2:])

	case cmdWatchCreate:
		err = watchCreateClusterCommand(watchCreateClusterFlags, os.Args[2:])

	default:
		fmt.Fprintf(os.Stderr, "Error: Unknown command '%s'\n\n", os.Args[1])
		printUsage(executableName)
		return fmt.Errorf("unknown command: %s", os.Args[1])
	}

	// Handle command execution errors
	if err != nil {
		return fmt.Errorf("command '%s' failed: %w", command, err)
	}

	return nil
}

// main is the entry point for the PowerVC-Tool application.
// It calls run() and handles the exit code based on the returned error.
func main() {
	if err := run(); err != nil {
		// Error message already printed by run() or command functions
		os.Exit(exitError)
	}
	os.Exit(exitSuccess)
}
