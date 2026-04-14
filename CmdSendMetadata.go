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
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
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

	// Timeout for send metadata operation
	sendMetadataTimeout = 5 * time.Minute
)

// operationType represents the type of metadata operation to perform
type operationType int

const (
	// operationTypeCreate indicates a create operation
	operationTypeCreate operationType = iota
	// operationTypeDelete indicates a delete operation
	operationTypeDelete
)

// String returns the string representation of the operation type
func (o operationType) String() string {
	switch o {
	case operationTypeCreate:
		return operationCreate
	case operationTypeDelete:
		return operationDelete
	default:
		return "unknown"
	}
}

// pastTense returns the past tense form of the operation
func (o operationType) pastTense() string {
	switch o {
	case operationTypeCreate:
		return operationCreated
	case operationTypeDelete:
		return operationDeleted
	default:
		return "unknown"
	}
}

// sendMetadataError represents a structured error for send-metadata operations
type sendMetadataError struct {
	operation string
	phase     string
	cause     error
}

// Error implements the error interface
func (e *sendMetadataError) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s%s failed during %s: %v", errPrefixSend, e.operation, e.phase, e.cause)
	}
	return fmt.Sprintf("%s%s failed during %s", errPrefixSend, e.operation, e.phase)
}

// Unwrap returns the underlying error
func (e *sendMetadataError) Unwrap() error {
	return e.cause
}

// newSendMetadataError creates a new structured error
func newSendMetadataError(operation, phase string, cause error) error {
	return &sendMetadataError{
		operation: operation,
		phase:     phase,
		cause:     cause,
	}
}

// sendMetadataCommand executes the send-metadata command with the given flags and arguments.
//
// This function handles both metadata creation and deletion operations. It validates
// that exactly one operation is specified, validates the metadata file and server IP,
// and sends the metadata to the remote server with timeout support.
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
//  8. Creates context with timeout for operation
//  9. Sends metadata to remote server
//  10. Provides user feedback on success
//
// Example usage:
//   err := sendMetadataCommand(flagSet, []string{
//       "--createMetadata", "metadata.json",
//       "--serverIP", "192.168.1.100",
//   })
func sendMetadataCommand(sendMetadataFlags *flag.FlagSet, args []string) error {
	// Validate input parameters
	if sendMetadataFlags == nil {
		return newSendMetadataError("send-metadata", "initialization", fmt.Errorf("flag set cannot be nil"))
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
		return newSendMetadataError("send-metadata", "flag parsing", err)
	}

	// Parse and validate operation flags
	createFile := strings.TrimSpace(*ptrCreateMetadata)
	deleteFile := strings.TrimSpace(*ptrDeleteMetadata)

	shouldCreateMetadata := createFile != ""
	shouldDeleteMetadata := deleteFile != ""

	// Ensure exactly one operation is specified
	if shouldCreateMetadata && shouldDeleteMetadata {
		return newSendMetadataError("send-metadata", "flag validation",
			fmt.Errorf("cannot specify both --%s and --%s", flagSendCreateMetadata, flagSendDeleteMetadata))
	}
	if !shouldCreateMetadata && !shouldDeleteMetadata {
		return newSendMetadataError("send-metadata", "flag validation",
			fmt.Errorf("required flag --%s or --%s must be specified", flagSendCreateMetadata, flagSendDeleteMetadata))
	}

	// Parse debug flag
	shouldDebug, err := parseBoolFlag(*ptrShouldDebug, flagSendShouldDebug)
	if err != nil {
		return newSendMetadataError("send-metadata", "debug flag parsing", err)
	}

	// Initialize logger
	log = initLogger(shouldDebug)
	if shouldDebug {
		log.Debugf("Debug mode enabled")
	}

	// Determine which file to use and operation type
	var metadataFile string
	var opType operationType
	if shouldCreateMetadata {
		metadataFile = createFile
		opType = operationTypeCreate
	} else {
		metadataFile = deleteFile
		opType = operationTypeDelete
	}

	log.Printf("[INFO] Starting send-metadata command")
	log.Printf("[INFO] Operation: %s", opType)
	log.Printf("[INFO] Metadata file: %s", metadataFile)

	// Validate metadata file exists and is readable
	log.Printf("[INFO] Validating metadata file...")
	if err := validateFileExists(metadataFile); err != nil {
		return newSendMetadataError(opType.String(), "file validation", err)
	}
	log.Printf("[INFO] Metadata file validated successfully")

	// Validate required server IP flag
	serverIP := strings.TrimSpace(*ptrServerIP)
	if serverIP == "" {
		return newSendMetadataError(opType.String(), "server IP validation",
			fmt.Errorf("required flag --%s not specified", flagSendServerIP))
	}
	log.Printf("[INFO] Server IP: %s", serverIP)

	// Validate server IP format
	log.Printf("[INFO] Validating server IP format...")
	if err := validateServerIP(serverIP); err != nil {
		return newSendMetadataError(opType.String(), "server IP validation", err)
	}
	log.Printf("[INFO] Server IP validated successfully")

	log.Debugf("sendMetadataCommand: operation=%s, file=%s, server=%s",
		opType,
		metadataFile,
		serverIP)

	// Create context with timeout for the operation
	ctx, cancel := context.WithTimeout(context.Background(), sendMetadataTimeout)
	defer cancel()

	log.Printf("[INFO] Sending metadata to server (timeout: %v)...", sendMetadataTimeout)
	startTime := time.Now()

	// Send metadata command to server with context
	if err := sendMetadataWithContext(ctx, metadataFile, serverIP, shouldCreateMetadata); err != nil {
		return newSendMetadataError(opType.String(), "metadata transmission", err)
	}

	duration := time.Since(startTime)
	log.Printf("[INFO] Metadata sent successfully (took %v)", duration)

	// Provide user feedback
	fmt.Printf("Metadata successfully %s from file: %s\n", opType.pastTense(), metadataFile)
	log.Printf("[INFO] Send-metadata command completed successfully")

	return nil
}

// sendMetadataWithContext sends metadata with context support for cancellation
func sendMetadataWithContext(ctx context.Context, metadataFile, serverIP string, shouldCreate bool) error {
	// Create a channel to receive the result
	errChan := make(chan error, 1)

	// Run the operation in a goroutine
	go func() {
		errChan <- sendMetadata(metadataFile, serverIP, shouldCreate)
	}()

	// Wait for either completion or context cancellation
	select {
	case err := <-errChan:
		return err
	case <-ctx.Done():
		return fmt.Errorf("operation cancelled or timed out: %w", ctx.Err())
	}
}
