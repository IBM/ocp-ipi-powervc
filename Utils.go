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

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/IBM/go-sdk-core/v5/core"

	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/wait"
)

const (
	// defaultTimeout is the default timeout for operations
	defaultTimeout = 15 * time.Minute

	// maxFileSize is the maximum file size allowed for validation (100MB)
	maxFileSize = 100 * 1024 * 1024

	// separatorLine is the visual separator used in command output
	separatorLine = "8<--------8<--------8<--------8<--------8<--------8<--------8<--------8<--------"
)

var (
	// ErrServerNotFound indicates the server could not be found
	ErrServerNotFound = errors.New("server not found")

	// ErrInvalidConfig indicates invalid configuration
	ErrInvalidConfig = errors.New("invalid configuration")

	// ErrFileTooBig indicates the file exceeds maximum size
	ErrFileTooBig = errors.New("file size exceeds maximum allowed")

	// validResourceNameRegex matches valid resource names
	validResourceNameRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
)

// initLogger creates a configured logger based on debug flag.
// When debug is true, logs are written to stderr; otherwise, they are discarded.
func initLogger(debug bool) *logrus.Logger {
	out := io.Discard
	if debug {
		out = os.Stderr
	}

	return &logrus.Logger{
		Out:   out,
		Formatter: &logrus.TextFormatter{
			FullTimestamp:   true,
			TimestampFormat: "2006-01-02 15:04:05",
		},
		Level: logrus.DebugLevel,
	}
}

// parseBoolFlag converts a string flag value to boolean.
// Returns an error if the value is not "true" or "false" (case-insensitive).
func parseBoolFlag(value, flagName string) (bool, error) {
	trimmedValue := strings.TrimSpace(strings.ToLower(value))

	switch trimmedValue {
	case "true", "1", "yes", "y":
		return true, nil
	case "false", "0", "no", "n":
		return false, nil
	default:
		return false, fmt.Errorf("%s must be 'true' or 'false', got: %q", flagName, value)
	}
}

// isValidResourceName checks if a resource name contains only valid characters.
// Valid characters are alphanumeric, hyphens, and underscores.
// Returns false for empty strings.
func isValidResourceName(name string) bool {
	if name == "" {
		return false
	}
	return validResourceNameRegex.MatchString(name)
}

// validateServerIP validates that the provided IP address or hostname is valid.
// Supports IPv4, IPv6 addresses, and hostnames.
// Returns an error if the input is empty or invalid.
func validateServerIP(ip string) error {
	if ip == "" {
		return fmt.Errorf("IP address or hostname cannot be empty")
	}

	// Try to parse as IP address first
	if net.ParseIP(ip) != nil {
		return nil
	}

	// If not a valid IP, check if it's a valid hostname
	if _, err := net.LookupHost(ip); err != nil {
		return fmt.Errorf("invalid IP address or hostname %q: %w", ip, err)
	}

	return nil
}

// validateFileExists checks if a file exists, is readable, and meets size constraints.
// Returns an error if the file doesn't exist, is a directory, is not readable,
// or exceeds the maximum allowed size.
func validateFileExists(filePath string) error {
	if filePath == "" {
		return fmt.Errorf("file path cannot be empty")
	}

	// Clean the file path
	cleanPath := filepath.Clean(filePath)

	// Get file info
	info, err := os.Stat(cleanPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("file does not exist: %q", cleanPath)
		}
		if os.IsPermission(err) {
			return fmt.Errorf("permission denied accessing file: %q", cleanPath)
		}
		return fmt.Errorf("cannot access file %q: %w", cleanPath, err)
	}

	// Check if it's a directory
	if info.IsDir() {
		return fmt.Errorf("path is a directory, not a file: %q", cleanPath)
	}

	// Check file size
	if info.Size() > maxFileSize {
		return fmt.Errorf("file size (%d bytes) exceeds maximum allowed (%d bytes): %q",
			info.Size(), maxFileSize, cleanPath)
	}

	// Verify file is readable by attempting to open it
	file, err := os.Open(cleanPath)
	if err != nil {
		if os.IsPermission(err) {
			return fmt.Errorf("file is not readable (permission denied): %q", cleanPath)
		}
		return fmt.Errorf("file is not readable: %q: %w", cleanPath, err)
	}
	defer file.Close()

	return nil
}

// validateDirectoryExists checks if a directory exists and is accessible.
// Returns an error if the path doesn't exist, is not a directory, or is not accessible.
func validateDirectoryExists(dirPath string) error {
	if dirPath == "" {
		return fmt.Errorf("directory path cannot be empty")
	}

	// Clean the directory path
	cleanPath := filepath.Clean(dirPath)

	// Get directory info
	info, err := os.Stat(cleanPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("directory does not exist: %q", cleanPath)
		}
		if os.IsPermission(err) {
			return fmt.Errorf("permission denied accessing directory: %q", cleanPath)
		}
		return fmt.Errorf("cannot access directory %q: %w", cleanPath, err)
	}

	// Check if it's actually a directory
	if !info.IsDir() {
		return fmt.Errorf("path is not a directory: %q", cleanPath)
	}

	return nil
}

// sanitizeInput removes leading/trailing whitespace and validates the input is not empty.
// Returns an error if the sanitized input is empty.
func sanitizeInput(input, fieldName string) (string, error) {
	sanitized := strings.TrimSpace(input)
	if sanitized == "" {
		return "", fmt.Errorf("%s cannot be empty or whitespace only", fieldName)
	}
	return sanitized, nil
}

// retryConfig holds configuration for retry operations with exponential backoff.
type retryConfig struct {
	Duration time.Duration
	Factor   float64
	Cap      time.Duration
	Steps    int
}

// defaultRetryConfig returns the default retry configuration for IBM Cloud operations.
// The configuration uses exponential backoff with a 15-second initial duration,
// 1.1x factor, and respects the context timeout.
func defaultRetryConfig(ctx context.Context) retryConfig {
	return retryConfig{
		Duration: 15 * time.Second,
		Factor:   1.1,
		Cap:      leftInContext(ctx),
		Steps:    math.MaxInt32,
	}
}

// retryWithBackoff executes an operation with exponential backoff retry logic.
// It automatically retries on transient failures and logs retry attempts.
//
// Type parameter T represents the result type of the operation.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - operation: Function to execute with retry logic
//   - operationName: Name of the operation for logging and error messages
//
// Returns:
//   - T: Result of the successful operation
//   - *core.DetailedResponse: HTTP response details from IBM Cloud SDK
//   - error: Any error encountered during the operation
func retryWithBackoff[T any](
	ctx context.Context,
	operation func(context.Context) (T, *core.DetailedResponse, error),
	operationName string,
) (T, *core.DetailedResponse, error) {
	var result T
	var response *core.DetailedResponse

	config := defaultRetryConfig(ctx)
	backoff := wait.Backoff{
		Duration: config.Duration,
		Factor:   config.Factor,
		Cap:      config.Cap,
		Steps:    config.Steps,
	}

	log.Debugf("Starting %s operation", operationName)

	err := wait.ExponentialBackoffWithContext(ctx, backoff, func(ctx context.Context) (bool, error) {
		var err error
		result, response, err = operation(ctx)
		if err != nil {
			log.Debugf("%s attempt failed: %v", operationName, err)
			return false, fmt.Errorf("%s failed: %w", operationName, err)
		}
		return true, nil
	})

	if err == nil {
		log.Debugf("%s operation completed successfully", operationName)
	}

	return result, response, err
}

// retrySshWithBackoff executes a function with exponential backoff retry logic.
//
// This helper function implements retry logic with exponential backoff for
// operations that may fail transiently (e.g., SSH connections, network operations).
// It retries the operation up to maxRetries times, with increasing delays between
// attempts.
//
// Parameters:
//   - operation: A function that returns an error; will be retried if it returns an error
//   - operationName: A descriptive name for the operation (used in log messages)
//
// Returns:
//   - error: The last error encountered, or nil if the operation succeeded
//
// The backoff delay starts at initialRetryDelay and increases by retryMultiplier
// after each failed attempt, up to maxRetryDelay.
func retrySshWithBackoff(operation func() error, operationName string) error {
	var err error
	delay := initialRetryDelay

	for attempt := 1; attempt <= maxRetries; attempt++ {
		err = operation()
		if err == nil {
			if attempt > 1 {
				log.Printf("[INFO] %s succeeded on attempt %d", operationName, attempt)
			}
			return nil
		}

		if attempt < maxRetries {
			log.Printf("[WARN] %s failed (attempt %d/%d): %v. Retrying in %v...",
				operationName, attempt, maxRetries, err, delay)
			time.Sleep(delay)

			// Calculate next delay with exponential backoff
			delay = time.Duration(float64(delay) * retryMultiplier)
			if delay > maxRetryDelay {
				delay = maxRetryDelay
			}
		} else {
			log.Printf("[ERROR] %s failed after %d attempts: %v",
				operationName, maxRetries, err)
		}
	}

	return fmt.Errorf("%s failed after %d attempts: %w", operationName, maxRetries, err)
}
