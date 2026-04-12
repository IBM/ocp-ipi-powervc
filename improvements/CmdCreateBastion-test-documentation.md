# CmdCreateBastion Test Documentation

## Overview
This document describes the Bastion test suite implemented for `CmdCreateBastion.go`. The tests focus on configuration construction, validation rules, helper behavior, and command-line flag parsing for Bastion creation.

## Test File Information
- **Source File**: `CmdCreateBastion.go`
- **Primary Test File**: `CmdCreateBastion_test.go`
- **Supporting Placeholder File**: `TestCmdCreateBastion.go`
- **Package**: `main`
- **Purpose**: Validate Bastion configuration handling and flag parsing behavior
- **Created**: 2026-04-12
- **Test Count**: 6 test functions

## Test Coverage Summary

### Covered Areas
- ✅ Default constructor behavior
- ✅ Custom default constructor behavior
- ✅ Bastion configuration validation
- ✅ Mutual exclusivity of setup modes
- ✅ File path validation for RSA key
- ✅ Server IP format validation
- ✅ Required OpenStack field validation
- ✅ Validation caching behavior
- ✅ Helper methods for setup mode and DNS detection
- ✅ Safe string rendering with redaction
- ✅ Bastion flag parsing
- ✅ Boolean flag parsing integration
- ✅ Unknown flag handling
- ✅ Validation errors returned from parsed flags

### Not Covered
- ⚠️ `createBastionCommand()` end-to-end behavior
- ⚠️ OpenStack resource lookup and creation paths
- ⚠️ SSH connection/setup helpers
- ⚠️ DNS creation against live IBM Cloud services
- ⚠️ Remote server setup execution flow

These uncovered areas require integration tests or mocks because they depend on external infrastructure, SSH, or cloud APIs.

## Test Functions

### 1. `TestNewBastionConfig`
**Purpose**: Verifies default constructor values.

**What It Tests**:
- `EnableHAProxy` defaults to `true`
- `ShouldDebug` defaults to `false`
- `validated` defaults to `false`

**Why It Matters**:
This confirms that newly constructed Bastion configs start with safe and expected defaults.

---

### 2. `TestNewBastionConfigWithDefaults`
**Purpose**: Verifies alternate constructor behavior when custom defaults are provided.

**What It Tests**:
- Custom `EnableHAProxy` value is honored
- Custom `ShouldDebug` value is honored

**Why It Matters**:
This ensures tests and future code can intentionally create configs with non-standard defaults.

---

### 3. `TestBastionConfigValidate`
**Purpose**: Validates the main `(*BastionConfig).Validate()` method across success and failure paths.

**Test Cases**:
- ✅ Valid local setup
- ✅ Valid remote setup
- ✅ Missing required fields and missing setup mode
- ✅ Invalid Bastion name characters
- ✅ Missing both setup modes
- ✅ Both setup modes provided
- ✅ Missing RSA file
- ✅ Invalid server IP
- ✅ Missing OpenStack resource fields

**Validation Rules Covered**:
- `cloud` is required
- `bastionName` is required
- `bastionName` must use valid resource characters
- Exactly one setup mode must be chosen:
  - local via `bastionRsa`
  - remote via `serverIP`
- `bastionRsa` file must exist if provided
- `serverIP` must parse as a valid IP address
- `flavorName`, `imageName`, `networkName`, and `sshKeyName` are required

**Why It Matters**:
This is the core correctness gate for Bastion command input. Most user-facing errors will originate here.

---

### 4. `TestBastionConfigValidate_CachesSuccess`
**Purpose**: Verifies that successful validation sets the internal cache flag and skips revalidation.

**What It Tests**:
- A valid config sets `validated = true`
- Subsequent calls to `Validate()` return success even after the original RSA file is removed

**Why It Matters**:
The implementation intentionally caches successful validation. This test documents and protects that behavior.

**Important Note**:
This behavior is useful for performance and consistency, but it also means changes after first validation are not re-checked.

---

### 5. `TestBastionConfigHelpers`
**Purpose**: Verifies helper methods and safe string formatting.

**Subtests**:

#### `local setup and string redaction`
**What It Tests**:
- `IsLocalSetup()` returns true when `BastionRsa` is set
- `IsRemoteSetup()` returns false in local mode
- `String()` does not expose the actual RSA path
- `String()` includes `RSA=<redacted>` instead of sensitive content

#### `remote setup and dns config`
**What It Tests**:
- `IsRemoteSetup()` returns true when `ServerIP` is set
- `IsLocalSetup()` returns false in remote mode
- `HasDNSConfig()` returns false when `IBMCLOUD_API_KEY` is not set
- `HasDNSConfig()` returns true when both domain and API key are available

**Why It Matters**:
These helpers are small but important pieces of behavior:
- they influence control flow
- they affect logging safety
- they determine whether DNS work is possible

---

### 6. `TestParseBastionFlags`
**Purpose**: Verifies command-line flag parsing into a validated `BastionConfig`.

**Test Cases**:
- ✅ Valid local flags with defaults
- ✅ Valid remote flags with optional values
- ✅ Invalid boolean flag
- ✅ Unknown flag
- ✅ Validation failure after parsing

**Flags Covered**:
- `--cloud`
- `--bastionName`
- `--bastionRsa`
- `--serverIP`
- `--flavorName`
- `--imageName`
- `--networkName`
- `--sshKeyName`
- `--domainName`
- `--enableHAProxy`
- `--shouldDebug`

**Boolean Parsing Behavior Verified**:
- `"no"` maps to `false`
- `"yes"` maps to `true`
- invalid values fail with a clear error

**Why It Matters**:
This test ensures CLI input becomes a correct config object and that invalid user input fails early with meaningful errors.

## Test Data and Fixtures

### Temporary RSA Key File
Several tests create a temporary RSA key file using `t.TempDir()` and `os.WriteFile()`.

**Purpose**:
- simulate valid local Bastion setup
- avoid dependency on a real user SSH key
- ensure test isolation

### Sample Valid Server IP
```text
192.168.122.10
```

### Sample Invalid Bastion Name
```text
bastion@1
```

### Sample Invalid Server IP
```text
not-an-ip
```

## Execution

### Run Bastion-Specific Tests
```bash
go test -run 'Test(NewBastionConfig|NewBastionConfigWithDefaults|BastionConfigValidate|BastionConfigValidate_CachesSuccess|BastionConfigHelpers|ParseBastionFlags)$' ./...
```

### Run Full Package Tests
```bash
go test ./...
```

### Run with Coverage
```bash
go test -cover ./...
```

## Latest Verified Result

### Test Run
```bash
go test -run 'Test(NewBastionConfig|NewBastionConfigWithDefaults|BastionConfigValidate|BastionConfigValidate_CachesSuccess|BastionConfigHelpers|ParseBastionFlags)$' ./...
```

### Output
```text
ok  	example/user/PowerVC-Tool	0.004s
```

## Implementation Notes

### File Naming Fix
The repository already contained `TestCmdCreateBastion.go`, but Go does not treat that filename as a test file because it does not end with `_test.go`.

To make the tests executable:
- the real tests were added to `CmdCreateBastion_test.go`
- `TestCmdCreateBastion.go` was retained only as a placeholder to avoid duplicate symbol definitions

### Why This Matters
Without the `_test.go` suffix, `go test` does not discover test functions in the file. This documentation records that repository-specific detail for future maintenance.

## Best Practices Demonstrated

### Table-Driven Validation Tests
Validation and flag parsing use table-driven tests for broad scenario coverage and maintainability.

### Temporary File Isolation
Tests that require a key file use `t.TempDir()` to avoid mutating user or repository state.

### Error Message Assertions
Tests validate meaningful substrings from returned errors instead of relying on exact full-string matches.

### Environment Variable Hygiene
`IBMCLOUD_API_KEY` is saved and restored so tests do not leak environment changes.

### Security-Aware Assertions
`String()` output is explicitly verified to redact the RSA path, protecting against accidental secret exposure in logs.

## Known Limitations

### 1. No Integration Coverage
The suite does not currently verify:
- actual Bastion VM creation
- OpenStack flavor/image/network lookups
- SSH readiness polling
- SSH command execution
- DNS record creation

### 2. No Mocked External Dependencies
The current tests stay at the pure unit layer and avoid cloud/network operations entirely.

### 3. Validation Cache Behavior Is Intentional
The validation cache is tested as implemented. If future changes require revalidation on every call, this test and documentation must be updated.

## Future Improvements

### 1. Add Integration Tests
Potential additions:
- create or locate Bastion server in a test OpenStack project
- verify SSH setup behavior with a disposable VM
- validate DNS behavior against a mocked or sandbox IBM Cloud account

### 2. Add Unit Tests for SSH Helpers
Candidate functions:
- `newSSHConfig`
- `waitForSSHReady`
- `execSSHCommand`

These likely require command execution abstraction or mocking.

### 3. Add End-to-End Command Tests
Test `createBastionCommand()` with controlled fakes for:
- server lookup
- server creation
- DNS setup
- SSH setup stages

### 4. Add Constant Validation Tests
A dedicated constants test could verify values such as:
- `bastionIpFilename`
- `defaultAvailZone`
- `maxSSHRetries`
- `sshRetryDelay`
- `sshUser`

## Maintenance Notes

When changing Bastion validation or flags:
1. update `CmdCreateBastion_test.go`
2. add or adjust table-driven cases
3. rerun targeted tests
4. update this documentation

When changing logging behavior:
1. confirm `String()` still redacts sensitive data
2. update helper tests if formatting changes

When changing setup mode semantics:
1. update `Validate()`
2. update helper method tests
3. update this document’s validation rules section

## Related Files
- `CmdCreateBastion.go`
- `CmdCreateBastion_test.go`
- `TestCmdCreateBastion.go`
- `improvements/CmdCreateBastion-code-improvement-plan.md`
- `improvements/CmdCreateBastion-improvements-summary.md`
- `improvements/CmdCreateBastion-refactoring-summary.md`

---

**Made with Bob**