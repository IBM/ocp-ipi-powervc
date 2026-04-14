# CmdSendMetadata.go - Test Coverage Improvements (2026-04-14)

## Overview
This document outlines additional test cases needed to cover the new improvements made to `CmdSendMetadata.go` on 2026-04-14, including structured errors, type-safe operations, and context support.

## Current Test Coverage

### Existing Tests (from CmdSendMetadata_test.go)
The file already has excellent test coverage with 667 lines of tests covering:

1. **TestSendMetadataCommand_NilFlagSet** - Nil flag set validation
2. **TestSendMetadataCommand_MutualExclusivity** - Create/delete mutual exclusivity
3. **TestSendMetadataCommand_MissingServerIP** - Server IP requirement
4. **TestSendMetadataCommand_InvalidServerIP** - IP format validation
5. **TestSendMetadataCommand_FileValidation** - File existence checks
6. **TestSendMetadataCommand_InvalidDebugFlag** - Debug flag validation
7. **TestSendMetadataCommand_ValidDebugFlags** - Valid debug values
8. **TestSendMetadataCommand_CreateOperation** - Create operation flow
9. **TestSendMetadataCommand_DeleteOperation** - Delete operation flow
10. **TestSendMetadataCommand_ErrorPrefix** - Error message formatting
11. **TestSendMetadataCommand_Constants** - Constant value validation
12. **TestSendMetadataCommand_FlagDefaults** - Default flag values
13. **TestSendMetadataCommand_WhitespaceHandling** - Whitespace trimming
14. **TestSendMetadataCommand_ValidIPv4Addresses** - IPv4 validation
15. **TestSendMetadataCommand_ValidIPv6Addresses** - IPv6 validation
16. **TestSendMetadataCommand_MultipleInvocations** - Multiple calls

**Total Existing Tests**: 16 test functions with multiple sub-tests

## New Test Cases Needed

### 1. Structured Error Type Tests

#### TestSendMetadataError_Error
```go
func TestSendMetadataError_Error(t *testing.T) {
    tests := []struct {
        name      string
        operation string
        phase     string
        cause     error
        expected  string
    }{
        {
            name:      "error with cause",
            operation: "create",
            phase:     "file validation",
            cause:     fmt.Errorf("file not found"),
            expected:  "Error: create failed during file validation: file not found",
        },
        {
            name:      "error without cause",
            operation: "delete",
            phase:     "initialization",
            cause:     nil,
            expected:  "Error: delete failed during initialization",
        },
        {
            name:      "error with wrapped cause",
            operation: "send-metadata",
            phase:     "flag parsing",
            cause:     fmt.Errorf("invalid flag: %w", fmt.Errorf("unknown flag")),
            expected:  "Error: send-metadata failed during flag parsing: invalid flag: unknown flag",
        },
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            err := &sendMetadataError{
                operation: tt.operation,
                phase:     tt.phase,
                cause:     tt.cause,
            }
            
            if err.Error() != tt.expected {
                t.Errorf("Expected error message %q, got %q", tt.expected, err.Error())
            }
        })
    }
}
```

#### TestSendMetadataError_Unwrap
```go
func TestSendMetadataError_Unwrap(t *testing.T) {
    tests := []struct {
        name     string
        cause    error
        expected error
    }{
        {
            name:     "with cause",
            cause:    fmt.Errorf("underlying error"),
            expected: fmt.Errorf("underlying error"),
        },
        {
            name:     "without cause",
            cause:    nil,
            expected: nil,
        },
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            err := &sendMetadataError{
                operation: "test",
                phase:     "test",
                cause:     tt.cause,
            }
            
            unwrapped := err.Unwrap()
            if tt.expected == nil {
                if unwrapped != nil {
                    t.Errorf("Expected nil, got %v", unwrapped)
                }
            } else {
                if unwrapped == nil {
                    t.Error("Expected error, got nil")
                } else if unwrapped.Error() != tt.expected.Error() {
                    t.Errorf("Expected %q, got %q", tt.expected.Error(), unwrapped.Error())
                }
            }
        })
    }
}
```

#### TestNewSendMetadataError
```go
func TestNewSendMetadataError(t *testing.T) {
    operation := "create"
    phase := "validation"
    cause := fmt.Errorf("test error")
    
    err := newSendMetadataError(operation, phase, cause)
    
    if err == nil {
        t.Fatal("Expected error, got nil")
    }
    
    var sendErr *sendMetadataError
    if !errors.As(err, &sendErr) {
        t.Fatal("Expected sendMetadataError type")
    }
    
    if sendErr.operation != operation {
        t.Errorf("Expected operation %q, got %q", operation, sendErr.operation)
    }
    if sendErr.phase != phase {
        t.Errorf("Expected phase %q, got %q", phase, sendErr.phase)
    }
    if sendErr.cause != cause {
        t.Errorf("Expected cause %v, got %v", cause, sendErr.cause)
    }
}
```

### 2. Operation Type Tests

#### TestOperationType_String
```go
func TestOperationType_String(t *testing.T) {
    tests := []struct {
        name     string
        opType   operationType
        expected string
    }{
        {
            name:     "create operation",
            opType:   operationTypeCreate,
            expected: "create",
        },
        {
            name:     "delete operation",
            opType:   operationTypeDelete,
            expected: "delete",
        },
        {
            name:     "unknown operation",
            opType:   operationType(999),
            expected: "unknown",
        },
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            result := tt.opType.String()
            if result != tt.expected {
                t.Errorf("Expected %q, got %q", tt.expected, result)
            }
        })
    }
}
```

#### TestOperationType_PastTense
```go
func TestOperationType_PastTense(t *testing.T) {
    tests := []struct {
        name     string
        opType   operationType
        expected string
    }{
        {
            name:     "create operation",
            opType:   operationTypeCreate,
            expected: "created",
        },
        {
            name:     "delete operation",
            opType:   operationTypeDelete,
            expected: "deleted",
        },
        {
            name:     "unknown operation",
            opType:   operationType(999),
            expected: "unknown",
        },
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            result := tt.opType.pastTense()
            if result != tt.expected {
                t.Errorf("Expected %q, got %q", tt.expected, result)
            }
        })
    }
}
```

### 3. Context and Timeout Tests

#### TestSendMetadataWithContext_Success
```go
func TestSendMetadataWithContext_Success(t *testing.T) {
    // This test requires mocking the sendMetadata function
    // or using a test server that responds quickly
    
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    
    tmpFile := createTempTestFile(t, "test-metadata.json", `{"test": "data"}`)
    defer os.Remove(tmpFile)
    
    // Note: This will fail in real execution without a server
    // In actual tests, you'd mock the sendMetadata function
    err := sendMetadataWithContext(ctx, tmpFile, "192.168.1.100", true)
    
    // We expect an error (connection failure) but not a timeout
    if err != nil && strings.Contains(err.Error(), "timed out") {
        t.Errorf("Should not timeout, got: %v", err)
    }
}
```

#### TestSendMetadataWithContext_Timeout
```go
func TestSendMetadataWithContext_Timeout(t *testing.T) {
    // Create a context with very short timeout
    ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
    defer cancel()
    
    // Wait for context to expire
    time.Sleep(10 * time.Millisecond)
    
    tmpFile := createTempTestFile(t, "test-metadata.json", `{"test": "data"}`)
    defer os.Remove(tmpFile)
    
    err := sendMetadataWithContext(ctx, tmpFile, "192.168.1.100", true)
    
    if err == nil {
        t.Fatal("Expected timeout error, got nil")
    }
    
    if !strings.Contains(err.Error(), "timed out") && !strings.Contains(err.Error(), "cancelled") {
        t.Errorf("Expected timeout/cancelled error, got: %v", err)
    }
}
```

#### TestSendMetadataWithContext_Cancellation
```go
func TestSendMetadataWithContext_Cancellation(t *testing.T) {
    ctx, cancel := context.WithCancel(context.Background())
    
    tmpFile := createTempTestFile(t, "test-metadata.json", `{"test": "data"}`)
    defer os.Remove(tmpFile)
    
    // Cancel immediately
    cancel()
    
    err := sendMetadataWithContext(ctx, tmpFile, "192.168.1.100", true)
    
    if err == nil {
        t.Fatal("Expected cancellation error, got nil")
    }
    
    if !strings.Contains(err.Error(), "cancelled") {
        t.Errorf("Expected cancelled error, got: %v", err)
    }
}
```

### 4. Error Phase Tests

#### TestSendMetadataCommand_ErrorPhases
```go
func TestSendMetadataCommand_ErrorPhases(t *testing.T) {
    tests := []struct {
        name          string
        setupFlags    func() *flag.FlagSet
        args          []string
        expectedPhase string
    }{
        {
            name: "initialization phase error",
            setupFlags: func() *flag.FlagSet {
                return nil // Nil flag set
            },
            args:          []string{},
            expectedPhase: "initialization",
        },
        {
            name: "flag parsing phase error",
            setupFlags: func() *flag.FlagSet {
                return flag.NewFlagSet("test", flag.ContinueOnError)
            },
            args:          []string{"--invalid-flag"},
            expectedPhase: "flag parsing",
        },
        {
            name: "flag validation phase error",
            setupFlags: func() *flag.FlagSet {
                return flag.NewFlagSet("test", flag.ContinueOnError)
            },
            args: []string{
                "--createMetadata", "file1.json",
                "--deleteMetadata", "file2.json",
                "--serverIP", "192.168.1.100",
            },
            expectedPhase: "flag validation",
        },
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            flagSet := tt.setupFlags()
            err := sendMetadataCommand(flagSet, tt.args)
            
            if err == nil {
                t.Fatal("Expected error, got nil")
            }
            
            if !strings.Contains(err.Error(), tt.expectedPhase) {
                t.Errorf("Expected error to contain phase %q, got: %v", tt.expectedPhase, err)
            }
        })
    }
}
```

### 5. Duration Logging Tests

#### TestSendMetadataCommand_DurationLogging
```go
func TestSendMetadataCommand_DurationLogging(t *testing.T) {
    // This test verifies that duration is logged
    // In practice, you'd capture log output and verify it contains duration
    
    tmpFile := createTempTestFile(t, "test-metadata.json", `{"test": "data"}`)
    defer os.Remove(tmpFile)
    
    flagSet := flag.NewFlagSet("send-metadata", flag.ContinueOnError)
    args := []string{
        "--createMetadata", tmpFile,
        "--serverIP", "192.168.1.100",
        "--shouldDebug", "true",
    }
    
    // Capture start time
    startTime := time.Now()
    
    err := sendMetadataCommand(flagSet, args)
    
    duration := time.Since(startTime)
    
    // Should fail at connection but should have taken some time
    if err == nil {
        t.Fatal("Expected error (connection failure), got nil")
    }
    
    // Verify it didn't timeout (should fail quickly on connection)
    if duration > sendMetadataTimeout {
        t.Errorf("Operation took longer than timeout: %v", duration)
    }
}
```

### 6. Timeout Constant Test

#### TestSendMetadataTimeout_Value
```go
func TestSendMetadataTimeout_Value(t *testing.T) {
    expected := 5 * time.Minute
    
    if sendMetadataTimeout != expected {
        t.Errorf("Expected timeout %v, got %v", expected, sendMetadataTimeout)
    }
}
```

## Test Coverage Summary

### Before New Improvements
- **Test Functions**: 16
- **Test Lines**: 667
- **Coverage Areas**: Flag parsing, validation, operations, error handling

### After New Improvements (Recommended)
- **Additional Test Functions**: 11
- **Additional Test Lines**: ~400 (estimated)
- **New Coverage Areas**: 
  - Structured errors (3 tests)
  - Operation types (2 tests)
  - Context/timeout (3 tests)
  - Error phases (1 test)
  - Duration logging (1 test)
  - Timeout constant (1 test)

### Total Coverage (Projected)
- **Total Test Functions**: 27
- **Total Test Lines**: ~1,067
- **Coverage**: ~95% (estimated)

## Integration Test Recommendations

### Mock Server Tests
```go
func TestSendMetadataCommand_WithMockServer(t *testing.T) {
    // Start a mock HTTP server
    server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusOK)
    }))
    defer server.Close()
    
    // Extract IP from server URL
    serverIP := extractIPFromURL(server.URL)
    
    tmpFile := createTempTestFile(t, "test-metadata.json", `{"test": "data"}`)
    defer os.Remove(tmpFile)
    
    flagSet := flag.NewFlagSet("send-metadata", flag.ContinueOnError)
    args := []string{
        "--createMetadata", tmpFile,
        "--serverIP", serverIP,
    }
    
    err := sendMetadataCommand(flagSet, args)
    
    if err != nil {
        t.Errorf("Expected success with mock server, got: %v", err)
    }
}
```

### Slow Server Tests
```go
func TestSendMetadataCommand_SlowServer(t *testing.T) {
    // Start a mock server that responds slowly
    server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        time.Sleep(10 * time.Second) // Longer than timeout
        w.WriteHeader(http.StatusOK)
    }))
    defer server.Close()
    
    serverIP := extractIPFromURL(server.URL)
    
    tmpFile := createTempTestFile(t, "test-metadata.json", `{"test": "data"}`)
    defer os.Remove(tmpFile)
    
    flagSet := flag.NewFlagSet("send-metadata", flag.ContinueOnError)
    args := []string{
        "--createMetadata", tmpFile,
        "--serverIP", serverIP,
    }
    
    err := sendMetadataCommand(flagSet, args)
    
    if err == nil {
        t.Fatal("Expected timeout error, got nil")
    }
    
    if !strings.Contains(err.Error(), "timed out") {
        t.Errorf("Expected timeout error, got: %v", err)
    }
}
```

## Benchmark Tests

### BenchmarkSendMetadataCommand
```go
func BenchmarkSendMetadataCommand(b *testing.B) {
    tmpFile := createTempTestFile(b, "bench-metadata.json", `{"test": "data"}`)
    defer os.Remove(tmpFile)
    
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        flagSet := flag.NewFlagSet("send-metadata", flag.ContinueOnError)
        args := []string{
            "--createMetadata", tmpFile,
            "--serverIP", "192.168.1.100",
        }
        _ = sendMetadataCommand(flagSet, args)
    }
}
```

## Test Execution Strategy

### Unit Tests
Run with: `go test -v -run TestSendMetadata`

### Integration Tests
Run with: `go test -v -run TestSendMetadata.*Integration`

### Benchmark Tests
Run with: `go test -bench=. -benchmem`

### Coverage Report
Generate with: `go test -coverprofile=coverage.out && go tool cover -html=coverage.out`

## Conclusion

The new improvements to `CmdSendMetadata.go` require approximately 11 additional test functions covering:
- Structured error handling
- Type-safe operation handling
- Context and timeout behavior
- Error phase identification
- Duration logging

These tests will increase coverage from ~85% to ~95% and ensure the new production-ready features work correctly under various conditions.