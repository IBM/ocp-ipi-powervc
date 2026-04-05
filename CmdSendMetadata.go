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
	fmt.Fprintf(os.Stderr, "Program version is %v, release = %v\n", version, release)

	// Define command-line flags
	ptrCreateMetadata := sendMetadataFlags.String("createMetadata", "", "Create the metadata from this file")
	ptrDeleteMetadata := sendMetadataFlags.String("deleteMetadata", "", "Delete the metadata from this file")
	ptrServerIP := sendMetadataFlags.String("serverIP", "", "The IP address of the server to send the command to")
	ptrShouldDebug := sendMetadataFlags.String("shouldDebug", "false", "Enable debug output (true/false)")

	// Parse flags
	if err := sendMetadataFlags.Parse(args); err != nil {
		return fmt.Errorf("failed to parse flags: %w", err)
	}

	// Parse and validate operation flags
	createFile := strings.TrimSpace(*ptrCreateMetadata)
	deleteFile := strings.TrimSpace(*ptrDeleteMetadata)

	shouldCreateMetadata := createFile != ""
	shouldDeleteMetadata := deleteFile != ""

	// Ensure exactly one operation is specified
	if shouldCreateMetadata && shouldDeleteMetadata {
		return fmt.Errorf("cannot specify both --createMetadata and --deleteMetadata")
	}
	if !shouldCreateMetadata && !shouldDeleteMetadata {
		return fmt.Errorf("required flag --createMetadata or --deleteMetadata must be specified")
	}

	// Determine which file to use
	metadataFile := createFile
	if shouldDeleteMetadata {
		metadataFile = deleteFile
	}

	// Validate metadata file exists and is readable
	if err := validateFileExists(metadataFile); err != nil {
		return fmt.Errorf("metadata file validation failed: %w", err)
	}

	// Validate required server IP flag
	serverIP := strings.TrimSpace(*ptrServerIP)
	if serverIP == "" {
		return fmt.Errorf("required flag --serverIP not specified")
	}

	// Validate server IP format
	if err := validateServerIP(serverIP); err != nil {
		return fmt.Errorf("invalid server IP: %w", err)
	}

	// Parse debug flag
	shouldDebug, err := parseBoolFlag(*ptrShouldDebug, "shouldDebug")
	if err != nil {
		return err
	}

	// Initialize logger
	log = initLogger(shouldDebug)

	log.Debugf("sendMetadataCommand: operation=%s, file=%s, server=%s",
		map[bool]string{true: "create", false: "delete"}[shouldCreateMetadata],
		metadataFile,
		serverIP)

	// Send metadata command to server
	if err := sendMetadata(metadataFile, serverIP, shouldCreateMetadata); err != nil {
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
