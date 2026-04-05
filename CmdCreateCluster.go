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

// Package main provides the create-cluster command implementation.
//
// This file implements the create-cluster command which orchestrates the
// multi-phase cluster creation process. The cluster creation is divided into
// multiple phases, each handling a specific aspect of the deployment:
//
// Phase 1: Initial setup and validation
// Phase 2: Infrastructure preparation
// Phase 3: Network configuration
// Phase 4: Compute resources
// Phase 5: Storage configuration
// Phase 6: Service deployment
// Phase 7: Final configuration and validation
//
// The command accepts the following flags:
//   - directory: The location of the installation directory (required)
//   - shouldDebug: Enable debug output (true/false, default: false)
//
// Each phase is executed sequentially, and if any phase fails, the entire
// operation is aborted with an error.

package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"
)

const (
	// Flag names
	flagDirectory   = "directory"
	flagShouldDebug = "shouldDebug"

	// Flag default values
	defaultDirectory   = ""
	defaultShouldDebug = "false"

	// Boolean string values
	boolTrue  = "true"
	boolFalse = "false"

	// Error message prefixes
	errPrefixFlag      = "Error: "
	errPrefixPhase     = "Phase execution failed: "

	// Usage messages
	usageDirectory   = "The location of the installation directory"
	usageShouldDebug = "Should output debug output"
)

// createClusterCommand executes the create-cluster command with the given flags and arguments.
//
// This function orchestrates the multi-phase cluster creation process. It parses
// command-line flags, configures logging based on the debug flag, validates the
// installation directory, and executes each cluster creation phase sequentially.
//
// Parameters:
//   - createClusterFlags: The FlagSet containing command-line flags (must not be nil)
//   - args: Command-line arguments to parse
//
// Returns:
//   - error: Any error encountered during flag parsing, validation, or phase execution
//
// The function executes the following steps:
//  1. Displays program version information
//  2. Parses command-line flags (directory and shouldDebug)
//  3. Configures logging based on debug flag
//  4. Validates the installation directory
//  5. Executes each cluster creation phase in sequence
//  6. Returns error if any phase fails
//
// Example usage:
//   err := createClusterCommand(flagSet, []string{"-directory", "/path/to/install", "-shouldDebug", "true"})
func createClusterCommand(createClusterFlags *flag.FlagSet, args []string) error {
	var (
		out            io.Writer
		ptrDirectory   *string
		ptrShouldDebug *string
		functions      = []func(string) error{
			createClusterPhase1,
			createClusterPhase2,
			createClusterPhase3,
			createClusterPhase4,
			createClusterPhase5,
			createClusterPhase6,
			createClusterPhase7,
		}
		err error
	)

	// Validate input parameters
	if createClusterFlags == nil {
		return fmt.Errorf("%sflag set cannot be nil", errPrefixFlag)
	}

	// Display version information
	fmt.Fprintf(os.Stderr, "Program version is %v, release = %v\n", version, release)

	// Define command-line flags
	ptrDirectory = createClusterFlags.String(flagDirectory, defaultDirectory, usageDirectory)
	ptrShouldDebug = createClusterFlags.String(flagShouldDebug, defaultShouldDebug, usageShouldDebug)

	// Parse command-line arguments
	err = createClusterFlags.Parse(args)
	if err != nil {
		return fmt.Errorf("%sfailed to parse flags: %w", errPrefixFlag, err)
	}

	// Parse and validate shouldDebug flag
	switch strings.ToLower(*ptrShouldDebug) {
	case boolTrue:
		shouldDebug = true
		log.Printf("[INFO] Debug mode enabled")
	case boolFalse:
		shouldDebug = false
	default:
		return fmt.Errorf("%sshouldDebug must be 'true' or 'false', got '%s'", errPrefixFlag, *ptrShouldDebug)
	}

	// Configure logging based on debug flag
	if shouldDebug {
		out = os.Stderr
	} else {
		out = io.Discard
	}
	log = &logrus.Logger{
		Out:       out,
		Formatter: new(logrus.TextFormatter),
		Level:     logrus.DebugLevel,
	}

	// Validate directory flag
	if *ptrDirectory == "" {
		return fmt.Errorf("%sinstallation directory is required, use -%s flag", errPrefixFlag, flagDirectory)
	}

	// Validate directory path
	absDirectory, err := filepath.Abs(*ptrDirectory)
	if err != nil {
		return fmt.Errorf("%sfailed to resolve absolute path for directory '%s': %w", errPrefixFlag, *ptrDirectory, err)
	}
	log.Printf("[INFO] Using installation directory: %s", absDirectory)

	// Check if directory exists
	dirInfo, err := os.Stat(absDirectory)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%sinstallation directory does not exist: %s", errPrefixFlag, absDirectory)
		}
		return fmt.Errorf("%sfailed to access installation directory '%s': %w", errPrefixFlag, absDirectory, err)
	}
	if !dirInfo.IsDir() {
		return fmt.Errorf("%spath is not a directory: %s", errPrefixFlag, absDirectory)
	}

	// Execute cluster creation phases sequentially
	log.Printf("[INFO] Starting cluster creation with %d phases", len(functions))
	for i, function := range functions {
		phaseNum := i + 1
		log.Printf("[INFO] Executing phase %d of %d", phaseNum, len(functions))

		err = function(absDirectory)
		if err != nil {
			return fmt.Errorf("%sphase %d failed: %w", errPrefixPhase, phaseNum, err)
		}

		log.Printf("[INFO] Phase %d completed successfully", phaseNum)
	}

	log.Printf("[INFO] All cluster creation phases completed successfully")
	return nil
}
