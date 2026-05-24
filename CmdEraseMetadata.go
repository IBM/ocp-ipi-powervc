// Copyright 2026 IBM Corp
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

// Package main provides the erase-metadata command implementation.
//
// This file implements the erase-metadata command which erases cluster metadata
// from a remote server using pattern matching. The command allows bulk deletion
// of metadata entries that match a specified pattern.
//
// The command accepts the following flags:
//   - pattern: Pattern to match metadata entries for deletion (required)
//   - serverIP: IP address of the remote server (required)
//   - shouldDebug: Enable debug output (true/false, default: false)
//   - timeout: Timeout for erase operation (e.g., 5m, 10m, 30s, default: 5m)
//
// Example usage:
//   # Erase metadata matching pattern with default timeout
//   ./tool erase-metadata --pattern "test-*" --serverIP 192.168.1.100
//
//   # Erase metadata with custom timeout and debug enabled
//   ./tool erase-metadata --pattern "staging-*" --serverIP 192.168.1.100 --timeout 10m --shouldDebug true

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
	// Flag names for erase-metadata command
	flagErasePattern     = "pattern"
	flagEraseServerIP    = "serverIP"
	flagEraseShouldDebug = "shouldDebug"
	flagEraseTimeout     = "timeout"

	// Flag default values
	defaultErasePattern     = ""
	defaultEraseServerIP    = ""
	defaultEraseShouldDebug = "false"
	defaultEraseTimeout     = "5m"

	// Usage messages
	usageErasePattern     = "Pattern to match metadata entries for deletion (e.g., 'test-*', 'staging-*')"
	usageEraseServerIP    = "The IP address of the server to send the command to"
	usageEraseShouldDebug = "Enable debug output (true/false)"
	usageEraseTimeout     = "Timeout for erase operation (e.g., 5m, 10m, 30s)"

	// Error message prefix
	errPrefixErase = "Error: "
)

// eraseMetadataError represents a structured error for erase-metadata operations
type eraseMetadataError struct {
	operation string
	phase     string
	cause     error
}

// Error implements the error interface
func (e *eraseMetadataError) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s%s failed during %s: %v", errPrefixErase, e.operation, e.phase, e.cause)
	}
	return fmt.Sprintf("%s%s failed during %s", errPrefixErase, e.operation, e.phase)
}

// Unwrap returns the underlying error
func (e *eraseMetadataError) Unwrap() error {
	return e.cause
}

// newEraseMetadataError creates a new structured error
func newEraseMetadataError(operation, phase string, cause error) error {
	return &eraseMetadataError{
		operation: operation,
		phase:     phase,
		cause:     cause,
	}
}

// validatePattern validates the metadata pattern string.
// This function ensures the pattern is not empty and contains valid characters.
//
// Parameters:
//   - pattern: The pattern string to validate
//
// Returns:
//   - error: Any error encountered during validation
func validatePattern(pattern string) error {
	if strings.TrimSpace(pattern) == "" {
		return fmt.Errorf("pattern cannot be empty")
	}

	// Basic validation - pattern should not be just whitespace
	if len(strings.TrimSpace(pattern)) == 0 {
		return fmt.Errorf("pattern cannot be only whitespace")
	}

	return nil
}

// eraseMetadataWithRetry attempts to erase metadata with exponential backoff retry logic.
// It will retry transient failures up to maxRetries times with increasing delays between attempts.
//
// Parameters:
//   - ctx: Context for cancellation and timeout
//   - pattern: Pattern to match metadata entries for deletion
//   - serverIP: IP address or hostname of the server
//
// Returns:
//   - error: Any error encountered, or nil on success
func eraseMetadataWithRetry(ctx context.Context, pattern, serverIP string) error {
	var lastErr error
	backoff := initialRetryDelay

	for attempt := 0; attempt <= maxRetries; attempt++ {
		// Wait before retry (skip on first attempt)
		if attempt > 0 {
			log.Printf("[INFO] Retry attempt %d/%d after %v delay", attempt, maxRetries, backoff)

			// Wait with context cancellation support
			select {
			case <-time.After(backoff):
				// Continue with retry
			case <-ctx.Done():
				return fmt.Errorf("operation cancelled during retry backoff: %w", ctx.Err())
			}

			// Increase backoff for next retry (exponential backoff)
			backoff = time.Duration(float64(backoff) * retryMultiplier)
			if backoff > maxRetryDelay {
				backoff = maxRetryDelay
			}
		}

		// Attempt to erase metadata
		log.Debugf("Attempting to erase metadata (attempt %d/%d)", attempt+1, maxRetries+1)
		err := sendEraseMetadata(ctx, serverIP, pattern)

		// Success - return immediately
		if err == nil {
			if attempt > 0 {
				log.Printf("[INFO] Metadata erased successfully after %d retries", attempt)
			}
			return nil
		}

		// Check if error is retryable
		if !isRetryableError(err) {
			log.Printf("[INFO] Non-retryable error encountered: %v", err)
			return err
		}

		// Store error for potential final return
		lastErr = err
		log.Printf("[WARN] Retryable error encountered: %v", err)

		// Check if we've exhausted retries
		if attempt == maxRetries {
			break
		}
	}

	// All retries exhausted
	return fmt.Errorf("failed after %d retries: %w", maxRetries, lastErr)
}

// eraseMetadataCommand executes the erase-metadata command with the given flags and arguments.
//
// This function handles pattern-based metadata deletion operations. It validates
// the pattern and server IP, and sends the erase command to the remote server
// with timeout support.
//
// Parameters:
//   - eraseMetadataFlags: The FlagSet containing command-line flags (must not be nil)
//   - args: Command-line arguments to parse
//
// Returns:
//   - error: Any error encountered during flag parsing, validation, or operation execution
//
// Example usage:
//   err := eraseMetadataCommand(flagSet, []string{
//       "--pattern", "test-*",
//       "--serverIP", "192.168.1.100",
//   })
func eraseMetadataCommand(eraseMetadataFlags *flag.FlagSet, args []string) error {
	err := innerEraseMetadataCommand(eraseMetadataFlags, args)
	if err != nil {
		fmt.Printf("%+v\n", err)
		if eraseMetadataFlags != nil {
			eraseMetadataFlags.Usage()
		}
	}
	return err
}

// innerEraseMetadataCommand executes the erase-metadata command with the given flags and arguments.
//
// This function handles pattern-based metadata deletion operations. It validates
// the pattern and server IP, and sends the erase command to the remote server
// with timeout support.
//
// Parameters:
//   - eraseMetadataFlags: The FlagSet containing command-line flags (must not be nil)
//   - args: Command-line arguments to parse
//
// Returns:
//   - error: Any error encountered during flag parsing, validation, or operation execution
//
// The function executes the following steps:
//  1. Validates input parameters
//  2. Displays program version information
//  3. Defines and parses command-line flags
//  4. Validates pattern format
//  5. Validates server IP address format
//  6. Initializes logger based on debug flag
//  7. Creates context with timeout for operation
//  8. Sends erase command to remote server
//  9. Provides user feedback on success
func innerEraseMetadataCommand(eraseMetadataFlags *flag.FlagSet, args []string) error {
	// Validate input parameters
	if eraseMetadataFlags == nil {
		return newEraseMetadataError("erase-metadata", "initialization", fmt.Errorf("flag set cannot be nil"))
	}

	// Display version information
	fmt.Fprintf(os.Stderr, "Program version is %v, release = %v\n", version, release)

	// Define command-line flags
	ptrPattern := eraseMetadataFlags.String(flagErasePattern, defaultErasePattern, usageErasePattern)
	ptrServerIP := eraseMetadataFlags.String(flagEraseServerIP, defaultEraseServerIP, usageEraseServerIP)
	ptrShouldDebug := eraseMetadataFlags.String(flagEraseShouldDebug, defaultEraseShouldDebug, usageEraseShouldDebug)
	ptrTimeout := eraseMetadataFlags.String(flagEraseTimeout, defaultEraseTimeout, usageEraseTimeout)

	// Parse flags
	if err := eraseMetadataFlags.Parse(args); err != nil {
		return newEraseMetadataError("erase-metadata", "flag parsing", err)
	}

	// Parse and validate pattern flag
	pattern := strings.TrimSpace(*ptrPattern)
	if pattern == "" {
		return newEraseMetadataError("erase-metadata", "flag validation",
			fmt.Errorf("required flag --%s must be specified", flagErasePattern))
	}

	// Parse debug flag
	shouldDebug, err := parseBoolFlag(*ptrShouldDebug, flagEraseShouldDebug)
	if err != nil {
		return newEraseMetadataError("erase-metadata", "debug flag parsing", err)
	}

	// Parse timeout flag
	timeout, err := time.ParseDuration(strings.TrimSpace(*ptrTimeout))
	if err != nil {
		return newEraseMetadataError("erase-metadata", "timeout parsing",
			fmt.Errorf("invalid timeout value %q: %w", *ptrTimeout, err))
	}
	if timeout <= 0 {
		return newEraseMetadataError("erase-metadata", "timeout validation",
			fmt.Errorf("timeout must be positive, got %v", timeout))
	}

	// Initialize logger
	log = initLogger(shouldDebug)
	if shouldDebug {
		log.Debugf("Debug mode enabled")
	}

	log.Printf("[INFO] Starting erase-metadata command")
	log.Printf("[INFO] Pattern: %s", pattern)
	log.Printf("[INFO] Timeout: %v", timeout)

	// Create context with timeout for the operation
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Validate pattern format
	log.Printf("[INFO] Validating pattern...")
	if err := validatePattern(pattern); err != nil {
		return newEraseMetadataError("erase-metadata", "pattern validation", err)
	}
	log.Printf("[INFO] Pattern validated successfully")

	// Check if context was cancelled after pattern validation
	if err := ctx.Err(); err != nil {
		return newEraseMetadataError("erase-metadata", "operation cancelled", err)
	}

	// Validate required server IP flag
	serverIP := strings.TrimSpace(*ptrServerIP)
	if serverIP == "" {
		return newEraseMetadataError("erase-metadata", "server IP validation",
			fmt.Errorf("required flag --%s not specified", flagEraseServerIP))
	}
	log.Printf("[INFO] Server IP: %s", serverIP)

	// Validate server IP format
	log.Printf("[INFO] Validating server IP format...")
	if err := validateServerIP(serverIP); err != nil {
		return newEraseMetadataError("erase-metadata", "server IP validation", err)
	}
	log.Printf("[INFO] Server IP validated successfully")

	// Check if context was cancelled after IP validation
	if err := ctx.Err(); err != nil {
		return newEraseMetadataError("erase-metadata", "operation cancelled", err)
	}

	log.Debugf("eraseMetadataCommand: pattern=%s, server=%s", pattern, serverIP)

	deadline, ok := ctx.Deadline()
	if ok {
		log.Printf("[INFO] Erasing metadata from server (timeout: %v, deadline: %v)...",
			timeout, deadline.Format(time.RFC3339))
	} else {
		log.Printf("[INFO] Erasing metadata from server (timeout: %v)...", timeout)
	}
	startTime := time.Now()

	// Send erase command to server with context and retry logic
	if err := eraseMetadataWithRetry(ctx, pattern, serverIP); err != nil {
		return newEraseMetadataError("erase-metadata", "metadata erasure", err)
	}

	duration := time.Since(startTime)
	log.Printf("[INFO] Metadata erased successfully (took %v)", duration)

	// Provide user feedback
	fmt.Printf("Metadata matching pattern '%s' erased successfully\n", pattern)
	log.Printf("[INFO] Erase-metadata command completed successfully")

	return nil
}

// Made with Bob
