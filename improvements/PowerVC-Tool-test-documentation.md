# PowerVC-Tool.go Test Documentation

## Overview
This document describes the comprehensive test suite created for `PowerVC-Tool.go`, the main entry point for the OpenShift IPI PowerVC deployment tool.

## Test File
- **File**: `PowerVC-Tool_test.go`
- **Package**: `main`
- **Total Tests**: 19 test functions with 100+ sub-tests
- **Coverage Areas**: Constants, version handling, usage display, command dispatch, argument parsing, and error handling

## Test Categories

### 1. Constants and Configuration Tests

#### TestConstants
Tests that all command name constants are defined correctly:
- `cmdCheckAlive` = "check-alive"
- `cmdCreateBastion` = "create-bastion"
- `cmdCreateRhcos` = "create-rhcos"
- `cmdCreateCluster` = "create-cluster"
- `cmdSendMetadata` = "send-metadata"
- `cmdWatchInstallation` = "watch-installation"
- `cmdWatchCreate` = "watch-create"
- `versionFlag` = "-version"

#### TestExitCodes
Verifies exit code constants:
- `exitSuccess` = 0
- `exitError` = 1

#### TestVersionVariables
Ensures version variables exist and are not empty:
- `version` variable
- `release` variable

### 2. Usage Display Tests

#### TestPrintUsage
Verifies that `printUsage()` outputs all required information:
- Program version and release
- Usage syntax
- All available commands
- Command descriptions
- Help text

#### TestPrintUsage_ExecutableName
Tests that the executable name is correctly displayed in usage output with various formats:
- Simple names
- Paths
- Extensions
- Names with spaces

#### TestPrintUsage_VersionInfo
Verifies that version information is properly displayed in usage output.

#### TestPrintUsage_CommandFormatting
Ensures all commands are properly formatted with their descriptions in the usage output.

### 3. Main Function Logic Tests

#### TestMain_NoArguments
Tests the logic for handling when no command-line arguments are provided.

#### TestMain_VersionFlag
Verifies version flag handling:
- Correct detection of `-version` flag
- Proper output format: "version = X\nrelease = Y\n"

#### TestMain_CommandDispatch
Tests command name recognition and case-insensitive handling:
- All valid commands (lowercase, uppercase, mixed case)
- Unknown commands
- Empty commands

#### TestMain_FlagSetCreation
Verifies that flag sets can be created for each command with correct names.

#### TestMain_CaseInsensitiveCommands
Tests that commands are properly normalized to lowercase:
- CHECK-ALIVE → check-alive
- Create-Bastion → create-bastion
- WATCH-INSTALLATION → watch-installation

#### TestMain_ErrorHandling
Verifies error message formatting for command failures.

#### TestMain_UnknownCommand
Tests handling of unknown/invalid commands:
- "invalid", "test", "help", "--help", "-h", "version", ""

#### TestMain_GlobalVariables
Verifies global variable initialization:
- `shouldDebug` defaults to false
- `log` variable state

#### TestMain_ExecutablePathHandling
Tests executable path retrieval and base name extraction logic.

#### TestMain_ArgumentParsing
Verifies argument array handling:
- No arguments
- Single argument
- Multiple arguments

#### TestMain_CommandArguments
Tests extraction of command and its arguments from os.Args:
- Command only
- Command with flags
- Command with multiple flags

#### TestMain_VersionFlagVariations
Tests different version flag formats:
- Exact match: "-version"
- Invalid variations: "-VERSION", "version", "--version"
- With extra arguments

#### TestMain_ExitCodeLogic
Verifies exit code selection based on error conditions:
- Success case (no error) → exitSuccess
- Error case → exitError

## Test Patterns and Best Practices

### Table-Driven Tests
Most tests use table-driven patterns for comprehensive coverage:
```go
tests := []struct {
    name        string
    input       string
    expected    string
    expectError bool
}{
    // test cases
}
```

### Output Capture
Tests that verify stderr output use pipe redirection:
```go
oldStderr := os.Stderr
r, w, _ := os.Pipe()
os.Stderr = w
// ... function call ...
w.Close()
os.Stderr = oldStderr
```

### Subtests
All table-driven tests use `t.Run()` for better test organization and parallel execution support.

### Error Message Validation
Tests verify not just that errors occur, but that they contain expected messages.

## Test Execution

### Run All PowerVC-Tool Tests
```bash
go test -v -run "^TestConstants$|^TestExitCodes$|^TestVersionVariables$|^TestPrintUsage|^TestMain_"
```

### Run Specific Test Category
```bash
# Constants and configuration
go test -v -run "^TestConstants$|^TestExitCodes$|^TestVersionVariables$"

# Usage display
go test -v -run "^TestPrintUsage"

# Main function logic
go test -v -run "^TestMain_"
```

### Run Single Test
```bash
go test -v -run "^TestMain_CommandDispatch$"
```

## Coverage Summary

### What is Tested
✅ All command name constants  
✅ Exit code constants  
✅ Version variables  
✅ Usage display formatting  
✅ Command dispatch logic  
✅ Case-insensitive command handling  
✅ Version flag detection  
✅ Unknown command handling  
✅ Argument parsing  
✅ Error message formatting  
✅ Flag set creation  
✅ Executable path handling  
✅ Global variable initialization  

### What is NOT Tested
❌ Actual command execution (tested in individual command test files)  
❌ Network operations  
❌ File system operations beyond executable path  
❌ Integration with external systems  
❌ The `main()` function directly (uses os.Exit, not testable)  

## Test Results

All tests pass successfully:
```
PASS
ok      example/user/PowerVC-Tool    0.008s
```

## Maintenance Notes

### Adding New Commands
When adding a new command to PowerVC-Tool.go:
1. Add the command constant to `TestConstants`
2. Add the command to `TestMain_CommandDispatch`
3. Add the command to `TestMain_FlagSetCreation`
4. Update `TestPrintUsage_CommandFormatting` with the new description

### Modifying Exit Codes
If exit codes change, update `TestExitCodes` and `TestMain_ExitCodeLogic`.

### Changing Version Flag
If the version flag format changes, update:
- `TestConstants`
- `TestMain_VersionFlag`
- `TestMain_VersionFlagVariations`

## Related Test Files
- `CmdCheckAlive_test.go` - Tests for check-alive command
- `CmdCreateBastion_test.go` - Tests for create-bastion command
- `CmdCreateRhcos_test.go` - Tests for create-rhcos command
- `CmdSendMetadata_test.go` - Tests for send-metadata command
- `CmdWatchCreate_test.go` - Tests for watch-create command
- `CmdWatchInstallation_test.go` - Tests for watch-installation command

## Author
Made with Bob - AI Assistant

## Date
2026-04-12