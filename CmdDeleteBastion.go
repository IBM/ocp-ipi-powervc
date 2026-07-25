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

// Package main provides functionality for managing bastion hosts on OpenStack/PowerVC
// infrastructure. This file specifically handles the deletion of an existing bastion
// virtual machine.
//
// Key Features:
//   - Locate an existing bastion server by name
//   - Delete the server via the OpenStack compute API
//   - Comprehensive input validation
//
// Usage Example:
//   ./ocp-ipi-powervc delete-bastion \
//     --cloud mycloud \
//     --bastionName my-bastion \
//     --shouldDebug true
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
)

const (
	// Progress tracking
	progressStepDeleteFinding = "Finding server"
	progressStepDeleting      = "Deleting server"
)

// deleteConfig holds the configuration required to delete a bastion server.
type deleteConfig struct {
	// Clouds specifies the cloud name from clouds.yaml to use for OpenStack authentication
	Clouds cloudFlags

	// BastionName is the name of the bastion to delete
	BastionName string

	// ShouldDebug enables verbose debug logging when true
	ShouldDebug bool
}

// validate checks that all required fields are present in the deleteConfig.
//
// Returns a *ValidationError if any required field is missing or invalid.
func (c *deleteConfig) validate() error {
	if len(c.Clouds) == 0 || c.Clouds[0] == "" {
		return &ValidationError{Field: "Cloud", Message: "is required"}
	}

	if c.BastionName == "" {
		return &ValidationError{Field: "BastionName", Message: "is required"}
	}

	return nil
}

// parseDeleteBastionFlags parses command-line flags and constructs a validated deleteConfig.
// It handles flag parsing and validation.
//
// Parameters:
//   - deleteBastionFlags: The FlagSet containing flag definitions
//   - args: Command-line arguments to parse
//
// Returns:
//   - *deleteConfig: Populated and validated configuration
//   - error: Any error encountered during parsing or validation
func parseDeleteBastionFlags(deleteBastionFlags *flag.FlagSet, args []string) (*deleteConfig, error) {
	config := &deleteConfig{}

	// Define flags
	deleteBastionFlags.Var(&config.Clouds, "cloud", "The cloud to use in clouds.yaml")
	ptrBastionName := deleteBastionFlags.String("bastionName", "", "The name of the bastion server to delete")
	ptrShouldDebug := deleteBastionFlags.String("shouldDebug", "false", "Enable debug output")

	if err := deleteBastionFlags.Parse(args); err != nil {
		return nil, fmt.Errorf("failed to parse flags: %w", err)
	}

	// Populate config from parsed flags
	config.BastionName = *ptrBastionName

	// Parse debug flag
	var err error
	config.ShouldDebug, err = parseBoolFlag(*ptrShouldDebug, "shouldDebug")
	if err != nil {
		return nil, err
	}

	// Validate configuration
	if err := config.validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return config, nil
}

// deleteBastionCommand is the top-level handler for the delete-bastion command.
// It delegates to innerDeleteBastionCommand and, on failure, prints the error
// and displays flag usage before returning the error.
func deleteBastionCommand(deleteBastionFlags *flag.FlagSet, args []string) error {
	err := innerDeleteBastionCommand(deleteBastionFlags, args)
	if err != nil {
		fmt.Printf("%+v\n",err)
		if deleteBastionFlags != nil {
			deleteBastionFlags.Usage()
		}
	}
	return err
}

// innerDeleteBastionCommand is the core handler for the delete-bastion workflow.
// It executes the following steps:
//  1. Parse and validate command-line flags.
//  2. Initialize logging and create a context with the configured timeout.
//  3. Locate the bastion server by name.
//  4. Delete the server.
//
// Parameters:
//   - deleteBastionFlags: FlagSet for parsing command-line arguments
//   - args: Command-line arguments
//
// Returns:
//   - error: Any error encountered during the workflow, nil on success
func innerDeleteBastionCommand(deleteBastionFlags *flag.FlagSet, args []string) error {
	fmt.Fprintf(os.Stderr, "Program version is %v, release = %v\n", version, release)

	// Step 1: Parse and validate configuration
	printProgress(progressStepParsing)
	config, err := parseDeleteBastionFlags(deleteBastionFlags, args)
	if err != nil {
		return fmt.Errorf("configuration error: %w", err)
	}

	// Step 2: Initialize logger
	log = initLogger(config.ShouldDebug)
	if config.ShouldDebug {
		log.Debugf("Debug mode enabled")
		log.Debugf("Configuration: Clouds=%s, BastionName=%s", config.Clouds, config.BastionName)
	}

	// Create context
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()

	// Step 3: Find the server
	printProgress(progressStepDeleteFinding)
	foundServer, err := findServer(ctx, config.Clouds, config.BastionName)
	if err != nil {
		return fmt.Errorf("failed to find server: %w", err)
	}
	log.Debugf("Server found: %s (ID: %s, Status: %s)", foundServer.Name, foundServer.ID, foundServer.Status)

	// Step 4: Delete the server
	printProgress(progressStepDeleting)
	err = deleteServer(ctx, config.Clouds[0], &foundServer)
	if err != nil {
		return fmt.Errorf("failed to delete server: %w", err)
	}

	printProgress(progressStepComplete)
	fmt.Printf("\n✓ Bastion server '%s' has been deleted.\n", config.BastionName)

	return nil
}
