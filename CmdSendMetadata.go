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
	"flag"
	"fmt"
	"os"
	"strings"
)

func sendMetadataCommand(sendMetadataFlags *flag.FlagSet, args []string) error {
	var (
		ptrCreateMetadata    *string
		ptrDeleteMetadata    *string
		ptrServerIP          *string
		ptrShouldDebug       *string
		shouldCreateMetadata bool
		shouldDeleteMetadata bool
		metadataFile         string
		err                  error
	)

	fmt.Fprintf(os.Stderr, "Program version is %v, release = %v\n", version, release)

	// Define command-line flags
	ptrCreateMetadata = sendMetadataFlags.String("createMetadata", "", "Create the metadata from this file")
	ptrDeleteMetadata = sendMetadataFlags.String("deleteMetadata", "", "Delete the metadata from this file")
	ptrServerIP = sendMetadataFlags.String("serverIP", "", "The IP address of the server to send the command to")
	ptrShouldDebug = sendMetadataFlags.String("shouldDebug", "false", "Enable debug output (true/false)")

	// Parse flags
	if err = sendMetadataFlags.Parse(args); err != nil {
		return fmt.Errorf("failed to parse flags: %w", err)
	}

	// Validate mutually exclusive flags
	if ptrCreateMetadata != nil && *ptrCreateMetadata != "" {
		shouldCreateMetadata = true
		metadataFile = strings.TrimSpace(*ptrCreateMetadata)
	}
	if ptrDeleteMetadata != nil && *ptrDeleteMetadata != "" {
		shouldDeleteMetadata = true
		metadataFile = strings.TrimSpace(*ptrDeleteMetadata)
	}

	// Ensure exactly one operation is specified
	if shouldCreateMetadata && shouldDeleteMetadata {
		return fmt.Errorf("cannot specify both --createMetadata and --deleteMetadata")
	}
	if !shouldCreateMetadata && !shouldDeleteMetadata {
		return fmt.Errorf("required flag --createMetadata or --deleteMetadata must be specified")
	}

	// Validate required flags
	if ptrServerIP == nil || strings.TrimSpace(*ptrServerIP) == "" {
		return fmt.Errorf("required flag --serverIP not specified")
	}

	// Validate server IP format
	if err = validateServerIP(strings.TrimSpace(*ptrServerIP)); err != nil {
		return fmt.Errorf("invalid server IP: %w", err)
	}

	// Validate metadata file path
	if metadataFile == "" {
		return fmt.Errorf("metadata file path cannot be empty")
	}

	// Parse debug flag
	shouldDebug, err = parseBoolFlag(*ptrShouldDebug, "shouldDebug")
	if err != nil {
		return err
	}

	// Initialize logger (using utility function to avoid duplication)
	log = initLogger(shouldDebug)

	// Send metadata command to server
	if err = sendMetadata(metadataFile, strings.TrimSpace(*ptrServerIP), shouldCreateMetadata); err != nil {
		return fmt.Errorf("send metadata command failed: %w", err)
	}

	// Provide user feedback
	operation := "created"
	if !shouldCreateMetadata {
		operation = "deleted"
	}
	fmt.Printf("Metadata successfully %s from file: %s\n", operation, metadataFile)

	return nil
}
