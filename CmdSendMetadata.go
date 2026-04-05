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

// Package main provides the send-metadata command implementation.
//
// This file implements the send-metadata command which sends cluster metadata
// to a remote server for creation or deletion. The command supports two mutually
// exclusive operations:
//
//   - Create: Sends metadata to the server to create cluster resources
//   - Delete: Sends metadata to the server to delete cluster resources
//
// The command accepts the following flags:
//   - createMetadata: Path to metadata file for creation (mutually exclusive with deleteMetadata)
//   - deleteMetadata: Path to metadata file for deletion (mutually exclusive with createMetadata)
//   - serverIP: IP address of the remote server (required)
//   - shouldDebug: Enable debug output (true/false, default: false)
//
// Example usage:
//   # Create metadata
//   ./tool send-metadata --createMetadata metadata.json --serverIP 192.168.1.100
//
//   # Delete metadata
//   ./tool send-metadata --deleteMetadata metadata.json --serverIP 192.168.1.100

package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

const (
	// Flag names for send-metadata command
	flagSendCreateMetadata = "createMetadata"
	flagSendDeleteMetadata = "deleteMetadata"
	flagSendServerIP       = "serverIP"
	flagSendShouldDebug    = "shouldDebug"

	// Flag default values
	defaultSendCreateMetadata = ""
	defaultSendDeleteMetadata = ""
	defaultSendServerIP       = ""
	defaultSendShouldDebug    = "false"

	// Usage messages
	usageSendCreateMetadata = "Create the metadata from this file"
	usageSendDeleteMetadata = "Delete the metadata from this file"
	usageSendServerIP       = "The IP address of the server to send the command to"
	usageSendShouldDebug    = "Enable debug output (true/false)"

	// Operation names
	operationCreate  = "create"
	operationDelete  = "delete"
	operationCreated = "created"
	operationDeleted = "deleted"

	// Error message prefix
	errPrefixSend = "Error: "
)

// sendMetadataCommand executes the send-metadata command with the given flags and arguments.
//
// This function handles both metadata creation and deletion operations. It validates
// that exactly one operation is specified, validates the metadata file and server IP,
// and sends the metadata to the remote server.
//
// Parameters:
//   - sendMetadataFlags: The FlagSet containing command-line flags (must not be nil)
//   - args: Command-line arguments to parse
//
// Returns:
//   - error: Any error encountered during flag parsing, validation, or operation execution
//
// The function executes the following steps:
//  1. Validates input parameters
//  2. Displays program version information
//  3. Defines and parses command-line flags
//  4. Validates mutual exclusivity of create/delete operations
//  5. Validates metadata file existence and readability
//  6. Validates server IP address format
//  7. Initializes logger based on debug flag
//  8. Sends metadata to remote server
//  9. Provides user feedback on success
//
// Example usage:
//   err := sendMetadataCommand(flagSet, []string{
//       "--createMetadata", "metadata.json",
//       "--serverIP", "192.168.1.100",
//   })
func sendMetadataCommand(sendMetadataFlags *flag.FlagSet, args []string) error {
	// Validate input parameters
	if sendMetadataFlags == nil {
		return fmt.Errorf("%sflag set cannot be nil", errPrefixSend)
	}

	// Display version information
	fmt.Fprintf(os.Stderr, "Program version is %v, release = %v\n", version, release)

	// Define command-line flags
	ptrCreateMetadata := sendMetadataFlags.String(flagSendCreateMetadata, defaultSendCreateMetadata, usageSendCreateMetadata)
	ptrDeleteMetadata := sendMetadataFlags.String(flagSendDeleteMetadata, defaultSendDeleteMetadata, usageSendDeleteMetadata)
	ptrServerIP := sendMetadataFlags.String(flagSendServerIP, defaultSendServerIP, usageSendServerIP)
	ptrShouldDebug := sendMetadataFlags.String(flagSendShouldDebug, defaultSendShouldDebug, usageSendShouldDebug)

	// Parse flags
	if err := sendMetadataFlags.Parse(args); err != nil {
		return fmt.Errorf("%sfailed to parse flags: %w", errPrefixSend, err)
	}

	// Parse and validate operation flags
	createFile := strings.TrimSpace(*ptrCreateMetadata)
	deleteFile := strings.TrimSpace(*ptrDeleteMetadata)

	shouldCreateMetadata := createFile != ""
	shouldDeleteMetadata := deleteFile != ""

	// Ensure exactly one operation is specified
	if shouldCreateMetadata && shouldDeleteMetadata {
		return fmt.Errorf("%scannot specify both --%s and --%s", errPrefixSend, flagSendCreateMetadata, flagSendDeleteMetadata)
	}
	if !shouldCreateMetadata && !shouldDeleteMetadata {
		return fmt.Errorf("%srequired flag --%s or --%s must be specified", errPrefixSend, flagSendCreateMetadata, flagSendDeleteMetadata)
	}

	// Determine which file to use and operation type
	metadataFile := createFile
	operationType := operationCreate
	if shouldDeleteMetadata {
		metadataFile = deleteFile
		operationType = operationDelete
	}
	log.Printf("[INFO] Starting send-metadata command")
	log.Printf("[INFO] Operation: %s", operationType)
	log.Printf("[INFO] Metadata file: %s", metadataFile)

	// Validate metadata file exists and is readable
	log.Printf("[INFO] Validating metadata file...")
	if err := validateFileExists(metadataFile); err != nil {
		return fmt.Errorf("%smetadata file validation failed: %w", errPrefixSend, err)
	}
	log.Printf("[INFO] Metadata file validated successfully")

	// Validate required server IP flag
	serverIP := strings.TrimSpace(*ptrServerIP)
	if serverIP == "" {
		return fmt.Errorf("%srequired flag --%s not specified", errPrefixSend, flagSendServerIP)
	}
	log.Printf("[INFO] Server IP: %s", serverIP)

	// Validate server IP format
	log.Printf("[INFO] Validating server IP format...")
	if err := validateServerIP(serverIP); err != nil {
		return fmt.Errorf("%sinvalid server IP: %w", errPrefixSend, err)
	}
	log.Printf("[INFO] Server IP validated successfully")

	// Parse debug flag
	shouldDebug, err := parseBoolFlag(*ptrShouldDebug, flagSendShouldDebug)
	if err != nil {
		return fmt.Errorf("%s%w", errPrefixSend, err)
	}

	// Initialize logger
	log = initLogger(shouldDebug)
	if shouldDebug {
		log.Printf("[INFO] Debug mode enabled")
	}

	log.Debugf("sendMetadataCommand: operation=%s, file=%s, server=%s",
		operationType,
		metadataFile,
		serverIP)

	// Send metadata command to server
	log.Printf("[INFO] Sending metadata to server...")
	if err := sendMetadata(metadataFile, serverIP, shouldCreateMetadata); err != nil {
		return fmt.Errorf("%ssend metadata command failed: %w", errPrefixSend, err)
	}
	log.Printf("[INFO] Metadata sent successfully")

	// Provide user feedback
	operation := operationCreated
	if !shouldCreateMetadata {
		operation = operationDeleted
	}
	fmt.Printf("Metadata successfully %s from file: %s\n", operation, metadataFile)
	log.Printf("[INFO] Send-metadata command completed successfully")

	return nil
}
