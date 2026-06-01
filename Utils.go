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
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/IBM/go-sdk-core/v5/core"

	"github.com/sirupsen/logrus"

	// Third-party imports - Kubernetes
	"k8s.io/apimachinery/pkg/util/wait"
)

const (
	// defaultTimeout is the default timeout for operations
	defaultTimeout = 15 * time.Minute

	// maxTimeout is the longest timeout for operations
	maxTimeout = 30 * time.Minute

	// maxFileSize is the maximum file size allowed for validation (100MB)
	maxFileSize = 100 * 1024 * 1024

	// separatorLine is the visual separator used in command output
	separatorLine = "8<--------8<--------8<--------8<--------8<--------8<--------8<--------8<--------"

	// Boolean string values
	boolTrue  = "true"
	boolFalse = "false"
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

var (
	// log is the global logger instance used throughout the application
	log *logrus.Logger

	// shouldDebug enables debug logging when set to true
	shouldDebug = false
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

// cloudFlags is a custom flag type that allows multiple cloud names
type cloudFlags []string

func (c *cloudFlags) String() string {
	return strings.Join(*c, ",")
}

func (c *cloudFlags) Set(value string) error {
	// Validate cloud name before adding
	if err := validateCloudName(value); err != nil {
		return err
	}
	*c = append(*c, value)
	return nil
}

// validateCloudName validates a cloud name for security and correctness.
//
// Valid cloud names must:
//   - Not be empty
//   - Be 1-253 characters long
//   - Contain only alphanumeric characters, hyphens, underscores, and periods
//   - Not contain path traversal sequences or command injection patterns
//
// Parameters:
//   - cloudName: The cloud name string to validate
//
// Returns:
//   - error: An error if the cloud name is invalid, nil otherwise
func validateCloudName(cloudName string) error {
	if cloudName == "" {
		return fmt.Errorf("cloud name cannot be empty")
	}

	if len(cloudName) > 253 {
		return fmt.Errorf("cloud name too long (max 253 characters): %d", len(cloudName))
	}

	// Allow alphanumeric, dash, underscore, and period (common in cloud names)
	// This matches typical OpenStack cloud naming conventions
	cloudNameRegex := regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)
	if !cloudNameRegex.MatchString(cloudName) {
		return fmt.Errorf("invalid cloud name format (only alphanumeric, dash, underscore, period allowed): %s", cloudName)
	}

	// Reject path traversal attempts
	if strings.Contains(cloudName, "..") {
		return fmt.Errorf("cloud name contains path traversal sequence: %s", cloudName)
	}

	// Reject patterns that could be used for command injection
	if strings.Contains(cloudName, "//") {
		return fmt.Errorf("cloud name contains suspicious pattern: %s", cloudName)
	}

	// Reject names that start or end with special characters
	if strings.HasPrefix(cloudName, ".") || strings.HasPrefix(cloudName, "-") ||
		strings.HasSuffix(cloudName, ".") || strings.HasSuffix(cloudName, "-") {
		return fmt.Errorf("cloud name cannot start or end with period or dash: %s", cloudName)
	}

	return nil
}

// parseBoolFlag converts a string flag value to boolean.
// Accepts "true", "1", "yes", "y" for true and "false", "0", "no", "n" for false (case-insensitive).
// Returns an error if the value doesn't match any accepted value.
func parseBoolFlag(value, flagName string) (bool, error) {
	trimmedValue := strings.TrimSpace(strings.ToLower(value))

	switch trimmedValue {
	case "true", "1", "yes", "y":
		return true, nil
	case "false", "0", "no", "n":
		return false, nil
	default:
		return false, fmt.Errorf("%s must be a boolean value (true/false/yes/no/1/0), got: %q", flagName, value)
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

	addr, err := netip.ParseAddr(ip)
	if err == nil && addr.Is4() {
		return nil
	} else if err == nil && addr.Is6() {
		return nil
	}

	// addrs, err = resolver.LookupHost("192.168.1") succeeds with
	// addrs = [192.168.0.1]
	// Which is a bug!
	re4 := regexp.MustCompile(`^[0-9.]+$`)
	re6 := regexp.MustCompile(`[0-9a-fA-F:]{2,39}`)
	if re4.MatchString(ip) || re6.MatchString(ip) {
		// We only care about this test
		if _, err := netip.ParseAddr(ip); err != nil {
			return fmt.Errorf("invalid IP address or hostname %q: %w", ip, err)
		}

		// Valid IP address
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()

	// Not a valid IP, check if it's a valid hostname
	resolver := &net.Resolver{}
	if _, err := resolver.LookupHost(ctx, ip); err != nil {
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
	cap := leftInContext(ctx)
	if cap > maxTimeout {
		cap = maxTimeout
	}
	return retryConfig{
		Duration: 15 * time.Second,
		Factor:   1.1,
		Cap:      cap,
		Steps:    100, // Reasonable upper bound
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
	var lastErr error

	config := defaultRetryConfig(ctx)
	backoff := wait.Backoff{
		Duration: config.Duration,
		Factor:   config.Factor,
		Cap:      config.Cap,
		Steps:    config.Steps,
	}

	log.Debugf("Starting %s operation", operationName)

	err := wait.ExponentialBackoffWithContext(ctx, backoff, func(ctx context.Context) (bool, error) {
		var opErr error

		result, response, opErr = operation(ctx)
		if opErr != nil {
			log.Debugf("%s attempt failed: %v", operationName, opErr)
			lastErr = opErr
			return false, nil // Continue retrying
		}

		return true, nil
	})

	if err != nil && lastErr != nil {
		return result, response, fmt.Errorf("%s failed: %w", operationName, lastErr)
	}

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

// keyscanServer scans SSH host keys from a server with retry logic
func keyscanServer(ctx context.Context, ipAddress string, silent bool) ([]byte, error) {
	backoff := wait.Backoff{
		Duration: 1 * time.Second,
		Factor:   1.1,
		Cap:      30 * time.Second,
		Steps:    math.MaxInt32,
	}

	var result []byte
	err := wait.ExponentialBackoffWithContext(ctx, backoff, func(retryCtx context.Context) (bool, error) {
		outb, err := runSplitCommandNoErrWithContext(retryCtx, []string{"ssh-keyscan", ipAddress}, silent)
		if err != nil {
			log.Debugf("keyscanServer: retry needed, error: %v", err)
			return false, nil // Retry
		}

		outs := strings.TrimSpace(string(outb))
		log.Debugf("keyscanServer: received keys from %s", ipAddress)

		// Remove comment lines generated by ssh-keyscan
		result = []byte(removeCommentLines(outs))
		return true, nil // Success
	})

	if err != nil {
		return nil, fmt.Errorf("failed to scan SSH keys after retries: %w", err)
	}

	return result, nil
}

// leftInContext returns the remaining time in the context
func leftInContext(ctx context.Context) time.Duration {
	deadline, ok := ctx.Deadline()
	if !ok {
		return math.MaxInt64
	}
	remaining := time.Until(deadline)
	if remaining < 0 {
		return 0
	}
	return remaining
}

// extractNetmask extracts the netmask from an IP address with CIDR notation.
// It accepts formats like "192.168.1.10/24" and returns just the netmask part (e.g., "24").
// If the input doesn't contain a netmask, it returns an empty string.
//
// Parameters:
//   - ipWithNetmask: IP address string that may include CIDR notation (e.g., "10.0.0.1/24")
//
// Returns:
//   - string: The netmask portion (e.g., "24"), or empty string if no netmask is present
//
// Examples:
//   - "192.168.1.10/24" returns "24"
//   - "10.0.0.1/16" returns "16"
//   - "192.168.1.10" returns ""
//   - "2001:db8::1/64" returns "64"
func extractNetmask(ipWithNetmask string) string {
	// Find the position of the slash separator
	slashIndex := strings.Index(ipWithNetmask, "/")

	// If no slash found, there's no netmask
	if slashIndex == -1 {
		return ""
	}

	// Return everything after the slash
	return ipWithNetmask[slashIndex+1:]
}

// buildResolvConf creates a formatted resolv.conf-style string with nameserver entries.
// Each DNS server address is prefixed with "nameserver ".
//
// Parameters:
//   - nameservers: Array of DNS server IP addresses or hostnames
//
// Returns:
//   - string: A formatted string with each nameserver on a new line, prefixed with "nameserver"
//
// Examples:
//   - []string{"8.8.8.8", "8.8.4.4"} returns "nameserver 8.8.8.8\nnameserver 8.8.4.4"
//   - []string{} returns ""
//   - []string{"192.168.1.1"} returns "nameserver 192.168.1.1"
func buildResolvConf(nameservers []string) string {
	if len(nameservers) == 0 {
		return ""
	}

	var builder strings.Builder

	for _, nameserver := range nameservers {
		builder.WriteString(fmt.Sprintf("nameserver %s\n", nameserver))
	}

	return builder.String()
}
