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
//   /bin/rm go.*; go mod init example/user/PowerVS-Check; go mod tidy
//   go build -ldflags="-X main.version=$(git describe --always --long --dirty) -X main.release=$(git describe --tags --abbrev=0)" -o "ocp-ipi-powervc-linux-${ARCH}" *.go
//
// Usage:
//   ocp-ipi-powervc-linux-${ARCH} <command> [flags]
//
// Available commands:
//   check-alive        - Check if cluster nodes are alive
//   create-bastion     - Create bastion host
//   create-rhcos       - Create RHCOS image
//   create-cluster     - Create OpenShift cluster
//   send-metadata      - Send metadata to cluster
//   watch-installation - Watch cluster installation progress
//   watch-create       - Watch cluster creation process

package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"
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

	// Version flag
	versionFlag = "-version"

	// Exit codes
	exitSuccess = 0
	exitError   = 1
)

var (
	// version is the build version, replaced at build time with:
	//   -ldflags="-X main.version=$(git describe --always --long --dirty)"
	version = "undefined"

	// release is the release tag, replaced at build time with:
	//   -ldflags="-X main.release=$(git describe --tags --abbrev=0)"
	release = "undefined"

	// shouldDebug enables debug logging when set to true
	shouldDebug = false

	// log is the global logger instance used throughout the application
	log *logrus.Logger
)

// printUsage displays the program usage information to stderr.
//
// Parameters:
//   - executableName: Name of the executable binary
func printUsage(executableName string) {
	fmt.Fprintf(os.Stderr, "Program version is %v, release = %v\n", version, release)
	fmt.Fprintf(os.Stderr, "\n")
	fmt.Fprintf(os.Stderr, "Usage: %s <command> [flags]\n", executableName)
	fmt.Fprintf(os.Stderr, "\n")
	fmt.Fprintf(os.Stderr, "Available commands:\n")
	fmt.Fprintf(os.Stderr, "  %-20s Check if cluster nodes are alive\n", cmdCheckAlive)
	fmt.Fprintf(os.Stderr, "  %-20s Create bastion host\n", cmdCreateBastion)
	fmt.Fprintf(os.Stderr, "  %-20s Create RHCOS image\n", cmdCreateRhcos)
	fmt.Fprintf(os.Stderr, "  %-20s Create OpenShift cluster\n", cmdCreateCluster)
	fmt.Fprintf(os.Stderr, "  %-20s Send metadata to cluster\n", cmdSendMetadata)
	fmt.Fprintf(os.Stderr, "  %-20s Watch cluster installation progress\n", cmdWatchInstallation)
	fmt.Fprintf(os.Stderr, "  %-20s Watch cluster creation process\n", cmdWatchCreate)
	fmt.Fprintf(os.Stderr, "\n")
	fmt.Fprintf(os.Stderr, "Use '%s <command> -h' for more information about a command.\n", executableName)
}

// main is the entry point for the PowerVC-Tool application.
// It parses command-line arguments and dispatches to the appropriate command handler.
func main() {
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
		fmt.Fprintf(os.Stderr, "Error: Failed to get executable path: %v\n", err)
		os.Exit(exitError)
	}
	executableName = filepath.Base(executablePath)

	// Handle no arguments case
	if len(os.Args) == 1 {
		fmt.Fprintf(os.Stderr, "Error: No command specified\n\n")
		printUsage(executableName)
		os.Exit(exitError)
	}

	// Handle version flag
	if len(os.Args) == 2 && os.Args[1] == versionFlag {
		fmt.Fprintf(os.Stdout, "version = %v\nrelease = %v\n", version, release)
		os.Exit(exitSuccess)
	}

	// Initialize flag sets for each command
	checkAliveFlags = flag.NewFlagSet(cmdCheckAlive, flag.ExitOnError)
	createBastionFlags = flag.NewFlagSet(cmdCreateBastion, flag.ExitOnError)
	createClusterFlags = flag.NewFlagSet(cmdCreateCluster, flag.ExitOnError)
	createRhcosFlags = flag.NewFlagSet(cmdCreateRhcos, flag.ExitOnError)
	sendMetadataFlags = flag.NewFlagSet(cmdSendMetadata, flag.ExitOnError)
	watchInstallationFlags = flag.NewFlagSet(cmdWatchInstallation, flag.ExitOnError)
	watchCreateClusterFlags = flag.NewFlagSet(cmdWatchCreate, flag.ExitOnError)

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
		os.Exit(exitError)
	}

	// Handle command execution errors
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: Command '%s' failed: %v\n", command, err)
		os.Exit(exitError)
	}

	os.Exit(exitSuccess)
}
