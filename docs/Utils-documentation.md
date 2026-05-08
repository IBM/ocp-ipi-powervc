# Utils.go Documentation

**Package:** main  
**File:** Utils.go  
**Purpose:** Utility functions for OCP IPI PowerVC project  
**Last Updated:** 2026-05-08

## Table of Contents

1. [Overview](#overview)
2. [Constants](#constants)
3. [Variables](#variables)
4. [Types](#types)
5. [Functions](#functions)
6. [Usage Examples](#usage-examples)
7. [Best Practices](#best-practices)
8. [Known Issues](#known-issues)

## Overview

The `Utils.go` file provides a collection of utility functions used throughout the OCP IPI PowerVC project. It includes:

- Logger initialization
- Input validation and sanitization
- Retry logic with exponential backoff
- Network validation (IP addresses, hostnames)
- File and directory validation
- SSH key scanning utilities
- Context timeout management

## Constants

### Timeout Constants

```go
const (
    // defaultTimeout is the default timeout for operations
    defaultTimeout = 15 * time.Minute

    // maxTimeout is the longest timeout for operations
    maxTimeout = 30 * time.Minute
)
```

**Usage:** These constants define timeout boundaries for long-running operations like IBM Cloud API calls and SSH operations.

### File Size Constants

```go
const (
    // maxFileSize is the maximum file size allowed for validation (100MB)
    maxFileSize = 100 * 1024 * 1024
)
```

**Usage:** Prevents processing of excessively large files that could cause memory issues.

### Display Constants

```go
const (
    // separatorLine is the visual separator used in command output
    separatorLine = "8<--------8<--------8<--------8<--------8<--------8<--------8<--------8<--------"
)
```

**Usage:** Provides visual separation in command output for better readability.

### Boolean String Constants

```go
const (
    // Boolean string values
    boolTrue  = "true"
    boolFalse = "false"
)
```

**Usage:** Standard string representations of boolean values for consistency.

## Variables

### Error Variables

```go
var (
    // ErrServerNotFound indicates the server could not be found
    ErrServerNotFound = errors.New("server not found")

    // ErrInvalidConfig indicates invalid configuration
    ErrInvalidConfig = errors.New("invalid configuration")

    // ErrFileTooBig indicates the file exceeds maximum size
    ErrFileTooBig = errors.New("file size exceeds maximum allowed")
)
```

**Usage:** Sentinel errors that can be checked with `errors.Is()` for specific error handling.

### Validation Variables

```go
var (
    // validResourceNameRegex matches valid resource names
    validResourceNameRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
)
```

**Usage:** Pre-compiled regex for efficient resource name validation.

## Types

### cloudFlags

```go
type cloudFlags []string
```

Custom flag type that allows multiple cloud names to be specified on the command line.

**Methods:**

- `String() string` - Returns comma-separated list of cloud names
- `Set(value string) error` - Adds a cloud name to the list

**Example:**
```go
var clouds cloudFlags
flag.Var(&clouds, "cloud", "Cloud name (can be specified multiple times)")
// Usage: -cloud ibm -cloud aws -cloud azure
```

### retryConfig

```go
type retryConfig struct {
    Duration time.Duration  // Initial retry delay
    Factor   float64        // Backoff multiplier
    Cap      time.Duration  // Maximum delay between retries
    Steps    int            // Maximum number of retry attempts
}
```

Configuration for exponential backoff retry logic.

**Fields:**
- `Duration`: Starting delay between retries
- `Factor`: Multiplier applied to delay after each retry
- `Cap`: Maximum delay between retries (prevents excessive waiting)
- `Steps`: Maximum number of retry attempts

## Functions

### Logger Functions

#### initLogger

```go
func initLogger(debug bool) *logrus.Logger
```

Creates a configured logger based on debug flag.

**Parameters:**
- `debug` (bool): If true, logs to stderr; if false, discards logs

**Returns:**
- `*logrus.Logger`: Configured logger instance

**Example:**
```go
logger := initLogger(true)
logger.Debugf("Debug message: %s", "test")
```

**Features:**
- Full timestamp with format "2006-01-02 15:04:05"
- Debug level logging
- Conditional output (stderr or discard)

### Validation Functions

#### parseBoolFlag

```go
func parseBoolFlag(value, flagName string) (bool, error)
```

Converts a string flag value to boolean with flexible input handling.

**Parameters:**
- `value` (string): The string value to parse
- `flagName` (string): Name of the flag (for error messages)

**Returns:**
- `bool`: Parsed boolean value
- `error`: Error if value is invalid

**Accepted Values:**
- **True:** "true", "1", "yes", "y" (case-insensitive)
- **False:** "false", "0", "no", "n" (case-insensitive)

**Example:**
```go
debug, err := parseBoolFlag("yes", "debug")
if err != nil {
    log.Fatal(err)
}
// debug == true
```

#### isValidResourceName

```go
func isValidResourceName(name string) bool
```

Checks if a resource name contains only valid characters.

**Parameters:**
- `name` (string): Resource name to validate

**Returns:**
- `bool`: True if valid, false otherwise

**Valid Characters:** Alphanumeric, hyphens (-), and underscores (_)

**Example:**
```go
if !isValidResourceName("my-cluster_01") {
    return fmt.Errorf("invalid resource name")
}
```

#### validateServerIP

```go
func validateServerIP(ip string) error
```

Validates that the provided IP address or hostname is valid.

**Parameters:**
- `ip` (string): IP address or hostname to validate

**Returns:**
- `error`: Error if invalid, nil if valid

**Supports:**
- IPv4 addresses (e.g., "192.168.1.1")
- IPv6 addresses (e.g., "2001:db8::1")
- Hostnames (e.g., "example.com")

**Example:**
```go
if err := validateServerIP("192.168.1.100"); err != nil {
    log.Fatalf("Invalid IP: %v", err)
}
```

**Note:** Includes workaround for DNS resolver bug with partial IP addresses.

#### validateFileExists

```go
func validateFileExists(filePath string) error
```

Checks if a file exists, is readable, and meets size constraints.

**Parameters:**
- `filePath` (string): Path to file to validate

**Returns:**
- `error`: Error if validation fails, nil if valid

**Validates:**
- File exists
- Path is not a directory
- File is readable
- File size ≤ 100MB

**Example:**
```go
if err := validateFileExists("/path/to/config.yaml"); err != nil {
    log.Fatalf("Config file invalid: %v", err)
}
```

#### validateDirectoryExists

```go
func validateDirectoryExists(dirPath string) error
```

Checks if a directory exists and is accessible.

**Parameters:**
- `dirPath` (string): Path to directory to validate

**Returns:**
- `error`: Error if validation fails, nil if valid

**Validates:**
- Directory exists
- Path is actually a directory
- Directory is accessible

**Example:**
```go
if err := validateDirectoryExists("/var/lib/data"); err != nil {
    log.Fatalf("Data directory invalid: %v", err)
}
```

#### sanitizeInput

```go
func sanitizeInput(input, fieldName string) (string, error)
```

Removes leading/trailing whitespace and validates input is not empty.

**Parameters:**
- `input` (string): Input string to sanitize
- `fieldName` (string): Name of the field (for error messages)

**Returns:**
- `string`: Sanitized input
- `error`: Error if input is empty after trimming

**Example:**
```go
username, err := sanitizeInput("  admin  ", "username")
if err != nil {
    log.Fatal(err)
}
// username == "admin"
```

### Retry Functions

#### defaultRetryConfig

```go
func defaultRetryConfig(ctx context.Context) retryConfig
```

Returns the default retry configuration for IBM Cloud operations.

**Parameters:**
- `ctx` (context.Context): Context for timeout calculation

**Returns:**
- `retryConfig`: Configuration with exponential backoff settings

**Configuration:**
- Initial duration: 15 seconds
- Backoff factor: 1.1x
- Cap: Lesser of context timeout or 30 minutes
- Steps: 100 attempts

**Example:**
```go
ctx := context.Background()
config := defaultRetryConfig(ctx)
// Use config for retry logic
```

#### retryWithBackoff

```go
func retryWithBackoff[T any](
    ctx context.Context,
    operation func(context.Context) (T, *core.DetailedResponse, error),
    operationName string,
) (T, *core.DetailedResponse, error)
```

Executes an operation with exponential backoff retry logic for IBM Cloud SDK operations.

**Type Parameters:**
- `T`: Result type of the operation

**Parameters:**
- `ctx` (context.Context): Context for cancellation and timeout
- `operation` (func): Function to execute with retry logic
- `operationName` (string): Name for logging and error messages

**Returns:**
- `T`: Result of successful operation
- `*core.DetailedResponse`: HTTP response details from IBM Cloud SDK
- `error`: Any error encountered

**Features:**
- Automatic retry on transient failures
- Exponential backoff with configurable parameters
- Context-aware timeout handling
- Debug logging of retry attempts

**Example:**
```go
ctx := context.Background()
result, response, err := retryWithBackoff(ctx,
    func(ctx context.Context) (*vpcv1.Instance, *core.DetailedResponse, error) {
        return vpcService.GetInstance(&vpcv1.GetInstanceOptions{
            ID: core.StringPtr(instanceID),
        })
    },
    "GetInstance",
)
if err != nil {
    log.Fatalf("Failed to get instance: %v", err)
}
```

#### retrySshWithBackoff

```go
func retrySshWithBackoff(operation func() error, operationName string) error
```

Executes a function with exponential backoff retry logic for SSH operations.

**Parameters:**
- `operation` (func): Function to execute (returns error)
- `operationName` (string): Descriptive name for logging

**Returns:**
- `error`: Last error encountered, or nil if successful

**Configuration:**
- Uses constants: `initialRetryDelay`, `maxRetries`, `retryMultiplier`, `maxRetryDelay`
- Logs retry attempts with warnings
- Logs final failure with error level

**Example:**
```go
err := retrySshWithBackoff(func() error {
    return sshClient.Connect()
}, "SSH Connection")
if err != nil {
    log.Fatalf("SSH connection failed: %v", err)
}
```

**Note:** Requires constants to be defined (see Known Issues).

### SSH Functions

#### keyscanServer

```go
func keyscanServer(ctx context.Context, ipAddress string, silent bool) ([]byte, error)
```

Scans SSH host keys from a server with retry logic.

**Parameters:**
- `ctx` (context.Context): Context for timeout and cancellation
- `ipAddress` (string): IP address or hostname to scan
- `silent` (bool): If true, suppress command output

**Returns:**
- `[]byte`: SSH host keys (comment lines removed)
- `error`: Error if scanning fails

**Features:**
- Exponential backoff retry (1s initial, 1.1x factor, 30s cap)
- Automatic comment line removal
- Context-aware timeout handling

**Example:**
```go
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
defer cancel()

keys, err := keyscanServer(ctx, "192.168.1.100", false)
if err != nil {
    log.Fatalf("Failed to scan SSH keys: %v", err)
}
fmt.Printf("SSH Keys:\n%s\n", keys)
```

**Note:** Requires `runSplitCommandNoErr()` and `removeCommentLines()` functions.

### Context Functions

#### leftInContext

```go
func leftInContext(ctx context.Context) time.Duration
```

Returns the remaining time in the context before deadline.

**Parameters:**
- `ctx` (context.Context): Context to check

**Returns:**
- `time.Duration`: Remaining time, or math.MaxInt64 if no deadline

**Example:**
```go
ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
defer cancel()

remaining := leftInContext(ctx)
log.Printf("Time remaining: %v", remaining)
```

**Note:** Returns very large value (math.MaxInt64) when no deadline is set.

## Usage Examples

### Complete Validation Example

```go
package main

import (
    "context"
    "log"
    "time"
)

func main() {
    // Initialize logger
    logger := initLogger(true)
    
    // Validate inputs
    serverIP := "192.168.1.100"
    if err := validateServerIP(serverIP); err != nil {
        logger.Fatalf("Invalid server IP: %v", err)
    }
    
    configFile := "/etc/config.yaml"
    if err := validateFileExists(configFile); err != nil {
        logger.Fatalf("Config file error: %v", err)
    }
    
    dataDir := "/var/lib/data"
    if err := validateDirectoryExists(dataDir); err != nil {
        logger.Fatalf("Data directory error: %v", err)
    }
    
    // Sanitize user input
    clusterName, err := sanitizeInput("  my-cluster  ", "cluster name")
    if err != nil {
        logger.Fatalf("Invalid cluster name: %v", err)
    }
    
    if !isValidResourceName(clusterName) {
        logger.Fatalf("Cluster name contains invalid characters")
    }
    
    logger.Printf("All validations passed for cluster: %s", clusterName)
}
```

### Retry with Backoff Example

```go
package main

import (
    "context"
    "fmt"
    "time"
)

func main() {
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
    defer cancel()
    
    // Retry an operation with exponential backoff
    result, response, err := retryWithBackoff(ctx,
        func(ctx context.Context) (string, *core.DetailedResponse, error) {
            // Simulate an operation that might fail
            return performOperation()
        },
        "PerformOperation",
    )
    
    if err != nil {
        log.Fatalf("Operation failed after retries: %v", err)
    }
    
    fmt.Printf("Operation succeeded: %s\n", result)
}
```

### SSH Key Scanning Example

```go
package main

import (
    "context"
    "fmt"
    "time"
)

func main() {
    ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
    defer cancel()
    
    servers := []string{"192.168.1.100", "192.168.1.101", "192.168.1.102"}
    
    for _, server := range servers {
        keys, err := keyscanServer(ctx, server, false)
        if err != nil {
            log.Printf("Failed to scan %s: %v", server, err)
            continue
        }
        
        fmt.Printf("Keys from %s:\n%s\n", server, keys)
    }
}
```

## Best Practices

### 1. Always Use Context with Timeout

```go
// Good
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
defer cancel()
result, _, err := retryWithBackoff(ctx, operation, "MyOperation")

// Bad - no timeout
ctx := context.Background()
result, _, err := retryWithBackoff(ctx, operation, "MyOperation")
```

### 2. Validate All External Inputs

```go
// Validate before using
if err := validateServerIP(userProvidedIP); err != nil {
    return fmt.Errorf("invalid IP: %w", err)
}

if err := validateFileExists(configPath); err != nil {
    return fmt.Errorf("config file error: %w", err)
}
```

### 3. Use Sanitization for User Input

```go
// Always sanitize user input
username, err := sanitizeInput(rawUsername, "username")
if err != nil {
    return err
}

if !isValidResourceName(username) {
    return fmt.Errorf("username contains invalid characters")
}
```

### 4. Handle Errors Appropriately

```go
// Check for specific errors
if errors.Is(err, ErrServerNotFound) {
    // Handle server not found specifically
    return fmt.Errorf("server does not exist: %w", err)
}

// Wrap errors with context
if err != nil {
    return fmt.Errorf("failed to process request: %w", err)
}
```

### 5. Use Descriptive Operation Names

```go
// Good - descriptive
retryWithBackoff(ctx, operation, "CreateVPCInstance")

// Bad - vague
retryWithBackoff(ctx, operation, "Operation")
```

## Known Issues

### Critical Issues

1. **Missing Constants** (Lines 355-374)
   - `initialRetryDelay`, `maxRetries`, `retryMultiplier`, `maxRetryDelay` are undefined
   - Required for `retrySshWithBackoff()` to compile
   - **Workaround:** Define these constants in Utils.go or import from LoadBalancer.go

2. **Missing Functions** (Lines 396, 406)
   - `runSplitCommandNoErr()` is undefined (defined in Run.go)
   - `removeCommentLines()` is undefined (defined in CmdCreateBastion.go)
   - **Workaround:** Ensure proper imports or move functions to Utils.go

3. **Missing Logger Variable** (Lines 311, 318, 331)
   - Global `log` variable is undefined
   - **Workaround:** Pass logger as parameter or define package-level logger

### Medium Priority Issues

4. **Inconsistent Logging** (Lines 361, 367, 377)
   - `retrySshWithBackoff()` uses `log.Printf()` instead of structured logger
   - **Impact:** Inconsistent log format
   - **Recommendation:** Use logrus consistently

5. **IPv6 Regex Imprecision** (Line 146)
   - Regex pattern may match invalid IPv6 addresses
   - **Impact:** Potential false positives
   - **Recommendation:** Use more precise regex or rely on netip.ParseAddr()

### Low Priority Issues

6. **Large Default Value** (Line 421)
   - `leftInContext()` returns `math.MaxInt64` when no deadline
   - **Impact:** Could cause overflow in calculations
   - **Recommendation:** Return reasonable maximum (e.g., 24 hours)

## Dependencies

### Standard Library
- `context` - Context management
- `errors` - Error handling
- `fmt` - Formatting
- `io` - I/O operations
- `math` - Mathematical constants
- `net` - Network operations
- `net/netip` - IP address parsing
- `os` - Operating system interface
- `path/filepath` - File path manipulation
- `regexp` - Regular expressions
- `strings` - String manipulation
- `time` - Time operations

### Third-Party Libraries
- `github.com/IBM/go-sdk-core/v5/core` - IBM Cloud SDK core
- `github.com/sirupsen/logrus` - Structured logging
- `k8s.io/apimachinery/pkg/util/wait` - Retry utilities

## Related Files

- `LoadBalancer.go` - Contains retry constants
- `Run.go` - Contains `runSplitCommandNoErr()`
- `CmdCreateBastion.go` - Contains `removeCommentLines()`

## Version History

- **2026-05-08:** Initial documentation created
- **2026-05-08:** Issues documented in improvements/Utils-issues-2026-05-08.md

## See Also

- [Utils Issues Document](../improvements/Utils-issues-2026-05-08.md)
- [Environment Variables Documentation](environment-variables.md)
- [Debugging Guide](debugging.md)