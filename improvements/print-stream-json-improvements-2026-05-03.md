# print-stream-json.sh Improvements - 2026-05-03 & 2026-05-04

## Overview
This document details the comprehensive improvements made to `scripts/print-stream-json.sh` to support multiple release versions, RHEL version selection, quiet mode, and enhance overall functionality, error handling, and user experience.

## Primary Objectives
1. Add support for multiple instances of the `--release` parameter to allow processing multiple OpenShift release versions in a single script execution.
2. Add `--rhel` parameter to specify RHEL version preference (rhel9 or rhel10) for CoreOS JSON selection.
3. Add `--quiet` parameter to suppress log output for cleaner automation and scripting.

## Improvements Implemented

### 1. Multiple Release Support
**Status:** ✅ Completed

#### Changes:
- Replaced hardcoded `RELEASE` variable with dynamic `RELEASES` array
- Added command-line argument parsing for `--release` parameter
- Implemented loop to process each release independently
- Added tracking for successful and failed releases

#### Benefits:
- Process multiple releases in one execution
- Reduces manual intervention and script invocations
- Provides consolidated reporting across all releases

#### Example Usage:
```bash
# Single release
./scripts/print-stream-json.sh --release release-4.21

# Multiple releases
./scripts/print-stream-json.sh --release release-4.21 --release release-4.22 --release release-4.23
```

---

### 2. RHEL Version Selection
**Status:** ✅ Completed (2026-05-04)

#### Changes:
- Added `--rhel` parameter with choices: `rhel9` or `rhel10`
- Modified `download_coreos_json()` to prioritize specified RHEL version
- Added validation to reject invalid RHEL version values
- Updated help documentation with RHEL examples

#### URL Priority Logic:
- **With `--rhel rhel9`:** Tries coreos-rhel-9.json first, then rhcos.json, then coreos-rhel-10.json
- **With `--rhel rhel10`:** Tries coreos-rhel-10.json first, then rhcos.json, then coreos-rhel-9.json
- **Without `--rhel`:** Tries rhcos.json first, then coreos-rhel-9.json, then coreos-rhel-10.json

#### Benefits:
- Control over which RHEL-based CoreOS variant to use
- Useful when specific RHEL version is required for compatibility
- Still falls back to other versions if preferred one is unavailable
- Explicit control over image selection

#### Example Usage:
```bash
# Prefer RHEL 9 based CoreOS
./scripts/print-stream-json.sh --release release-4.21 --rhel rhel9

# Prefer RHEL 10 based CoreOS
./scripts/print-stream-json.sh --release release-4.22 --rhel rhel10

# Multiple releases with RHEL 9 preference
./scripts/print-stream-json.sh --release release-4.21 --release release-4.22 --rhel rhel9
```

---

### 3. Quiet Mode
**Status:** ✅ Completed (2026-05-04)

#### Changes:
- Added `--quiet` / `-q` parameter to suppress log output
- Modified all log functions (except `log_error`) to respect QUIET flag
- Errors are always displayed regardless of quiet mode
- Added validation to prevent using `--verbose` and `--quiet` together
- Moved argument parsing before initial log messages in main()

#### Behavior:
- **Normal mode:** Shows all INFO, SUCCESS, WARNING, ERROR, and DEBUG messages
- **Quiet mode:** Only shows ERROR messages and structured output (JSON/CSV)
- **Verbose mode:** Shows all messages including DEBUG
- **Quiet + Verbose:** Rejected with error (mutually exclusive)

#### Benefits:
- Clean output for automation and scripting
- Easier to parse structured output (JSON/CSV) without log noise
- Errors still visible for troubleshooting
- Perfect for CI/CD pipelines and data processing

#### Example Usage:
```bash
# Quiet mode with text output (only errors shown)
./scripts/print-stream-json.sh --release release-4.21 --quiet

# Quiet mode with JSON output (clean JSON only)
./scripts/print-stream-json.sh --release release-4.21 --quiet --format json

# Quiet mode with CSV output
./scripts/print-stream-json.sh --release release-4.21 --release release-4.22 --quiet --format csv

# Use in pipeline
./scripts/print-stream-json.sh --release release-4.21 --quiet --format json | jq '.[] | .filename'
```

---

### 4. Enhanced Command-Line Interface
**Status:** ✅ Completed

#### New Options Added:

##### `--rhel <version>`
- Specify RHEL version preference: `rhel9` or `rhel10`
- Controls which CoreOS JSON variant to prioritize
- Validates input and rejects invalid values
- Falls back to other versions if preferred one unavailable

##### `--verbose` / `-v`
- Enable verbose output with debug information
- Shows HTTP status codes, URL attempts, and internal processing
- Cannot be used with `--quiet`

##### `--quiet` / `-q`
- Suppress all log output except errors
- Perfect for automation and clean structured output
- Cannot be used with `--verbose`

##### `--verbose` / `-v` (enhanced)
- Enables detailed debug output
- Shows HTTP status codes, URL attempts, and internal processing details
- Useful for troubleshooting and understanding script behavior

##### `--dry-run`
- Simulates operations without making actual OpenStack verification calls
- Skips OpenStack connectivity check
- Useful for testing script logic and URL availability

##### `--format <type>`
- Supports three output formats: `text` (default), `json`, `csv`
- **Text format:** Human-readable colored console output
- **JSON format:** Structured JSON array with detailed metadata
- **CSV format:** Comma-separated values with headers for data processing

##### `-h` / `--help`
- Comprehensive usage documentation
- Examples for common use cases
- Environment variable documentation

#### Benefits:
- Flexible output for different use cases (human vs. machine consumption)
- Better debugging capabilities
- Safe testing without affecting production systems

---

### 4. Improved Error Handling
**Status:** ✅ Completed

#### Enhancements:

##### Multiple URL Fallback
- Tries multiple CoreOS JSON sources automatically:
  1. `rhcos.json` (traditional location)
  2. `coreos-rhel-9.json` (RHEL 9 based)
  3. `coreos-rhel-10.json` (RHEL 10 based)
- Gracefully handles missing URLs without failing immediately

##### Better Return Code Handling
- All functions now return proper exit codes
- Errors are propagated correctly through the call stack
- Failed releases don't stop processing of remaining releases

##### Enhanced Validation
- Validates JSON extraction with proper error messages
- Checks for null/empty values in parsed data
- Provides specific error messages for each failure point

#### Benefits:
- More resilient to upstream changes in OpenShift installer repository
- Better error messages for troubleshooting
- Continues processing even when individual releases fail

---

### 5. New Utility Functions
**Status:** ✅ Completed

#### `log_debug()`
```bash
log_debug "Checking URL: ${url}"
```
- Outputs debug messages only when `--verbose` is enabled
- Uses cyan color for visual distinction
- Helps trace script execution flow

#### `download_coreos_json()`
```bash
download_coreos_json "${release}"
```
- Encapsulates URL fallback logic
- Tries multiple sources automatically
- Returns success/failure status

#### `extract_image_info()`
```bash
declare -A image_info
extract_image_info image_info
```
- Extracts comprehensive metadata from JSON
- Uses associative array for structured data
- Validates all extracted values

#### `output_result()`
```bash
output_result "${release}" image_info "success"
```
- Handles output formatting based on `--format` option
- Supports text, JSON, and CSV formats
- Provides consistent structure across formats

#### `can_curl()` (Enhanced)
- Added debug logging for HTTP status codes
- Better error detection
- More informative failure messages

#### Benefits:
- Better code organization and reusability
- Easier to maintain and extend
- Clear separation of concerns

---

### 5. Enhanced Output and Reporting
**Status:** ✅ Completed

#### Summary Statistics
- Total releases processed
- Successful releases count
- Failed releases count
- Execution duration in seconds

#### Structured Output Formats

##### JSON Format
```json
[
  {
    "release": "release-4.21",
    "status": "success",
    "filename": "rhcos-4.21.0-ppc64le-openstack.ppc64le",
    "download_url": "https://...",
    "sha256": "abc123...",
    "size": 1234567890
  }
]
```

##### CSV Format
```csv
release,status,filename,download_url,sha256,size
release-4.21,success,rhcos-4.21.0-ppc64le-openstack.ppc64le,https://...,abc123...,1234567890
```

##### Text Format
- Color-coded console output
- Progress indicators
- Clear success/failure messages

#### Benefits:
- Easy integration with automation tools
- Machine-readable output for data processing
- Human-friendly console output for interactive use

---

### 7. Code Quality Improvements
**Status:** ✅ Completed

#### Documentation
- Added comprehensive script header with feature list
- Inline comments for complex logic
- Function documentation with parameter descriptions

#### Variable Scoping
- Proper use of `local` declarations
- Readonly variables for constants
- Associative arrays for structured data

#### Error Handling
- Consistent error checking pattern
- Proper cleanup with trap handlers
- Graceful degradation on failures

#### Code Organization
- Logical grouping of functions
- Clear separation between utility and business logic
- Consistent naming conventions

#### Benefits:
- Easier to understand and maintain
- Reduces risk of variable conflicts
- Better error recovery

---

## Testing Performed

### Syntax Validation
```bash
bash -n scripts/print-stream-json.sh
# ✓ No syntax errors
```

### Help Documentation
```bash
./scripts/print-stream-json.sh --help
# ✓ Displays comprehensive usage information
```

### Argument Parsing
```bash
./scripts/print-stream-json.sh --release release-4.21 --release release-4.22 --verbose --dry-run
# ✓ Correctly parses multiple releases and options
```

### Dry Run Mode
```bash
CLOUD=test ./scripts/print-stream-json.sh --release release-4.21 --dry-run
# ✓ Skips OpenStack verification, processes release metadata
```

### RHEL Version Selection
```bash
# Test RHEL 9 preference
CLOUD=test ./scripts/print-stream-json.sh --release release-4.21 --rhel rhel9 --dry-run --verbose
# ✓ Prioritizes RHEL 9 CoreOS JSON

# Test RHEL 10 preference
CLOUD=test ./scripts/print-stream-json.sh --release release-4.22 --rhel rhel10 --dry-run --verbose
# ✓ Prioritizes RHEL 10 CoreOS JSON

# Test invalid RHEL version rejection
./scripts/print-stream-json.sh --rhel rhel8
# ✓ Rejects with error message
```

### Quiet Mode
```bash
# Test quiet mode (no log output)
CLOUD=test ./scripts/print-stream-json.sh --release release-4.21 --dry-run --quiet
# ✓ No log output, only structured data if applicable

# Test quiet mode with JSON (clean JSON only)
CLOUD=test ./scripts/print-stream-json.sh --release release-4.21 --dry-run --quiet --format json
# ✓ Clean JSON output without log messages

# Test errors still shown in quiet mode
./scripts/print-stream-json.sh --quiet --rhel invalid
# ✓ Error message displayed

# Test verbose and quiet are mutually exclusive
./scripts/print-stream-json.sh --verbose --quiet
# ✓ Rejects with error message
```

---

## Backward Compatibility

### Maintained Behaviors:
- Default release (`release-4.21`) when no `--release` specified
- Same environment variable requirements (`CLOUD`)
- Same OpenStack verification logic
- Same temporary file handling with cleanup

### Breaking Changes:
- None - script is fully backward compatible

---

## Performance Considerations

### Improvements:
- Parallel processing potential (can be added in future)
- Efficient URL checking with HTTP HEAD-like behavior
- Minimal redundant operations

### Resource Usage:
- Temporary files cleaned up properly
- No memory leaks from array operations
- Efficient string operations

---

## Future Enhancement Opportunities

### Potential Additions:
1. **Parallel Processing:** Process multiple releases concurrently
2. **Caching:** Cache downloaded JSON files to avoid redundant downloads
3. **Progress Bar:** Visual progress indicator for multiple releases
4. **Configuration File:** Support for release lists in config files
5. **Image Download:** Option to actually download images, not just verify
6. **Checksum Verification:** Verify downloaded image checksums
7. **Notification Support:** Email/Slack notifications on completion
8. **Retry Logic:** Automatic retry on transient failures

---

## Migration Guide

### For Existing Users:

#### No Changes Required
The script maintains full backward compatibility. Existing usage patterns continue to work:

```bash
# Old usage (still works with default release)
CLOUD=mycloud ./scripts/print-stream-json.sh
```

#### New Capabilities Available
Users can now leverage new features:

```bash
# Process multiple releases
CLOUD=mycloud ./scripts/print-stream-json.sh \
  --release release-4.21 \
  --release release-4.22 \
  --verbose

# Get JSON output for automation
CLOUD=mycloud ./scripts/print-stream-json.sh \
  --release release-4.21 \
  --format json > releases.json

# Test without OpenStack access
./scripts/print-stream-json.sh \
  --release release-4.21 \
  --dry-run
```

---

## Summary

The improvements to `print-stream-json.sh` significantly enhance its capabilities while maintaining full backward compatibility. The script now supports:

- ✅ Multiple release processing in single execution
- ✅ RHEL version selection (rhel9 or rhel10) with intelligent fallback
- ✅ Quiet mode for clean automation and scripting
- ✅ Flexible output formats (text, JSON, CSV)
- ✅ Enhanced debugging with verbose mode
- ✅ Safe testing with dry-run mode
- ✅ Robust error handling with fallback mechanisms
- ✅ Comprehensive reporting and statistics
- ✅ Complete function documentation with docstrings
- ✅ Better code organization and maintainability

These enhancements make the script more versatile, reliable, and suitable for both interactive use and automation workflows, with explicit control over RHEL-based CoreOS variant selection and clean output for CI/CD pipelines.

---

## Related Files
- Script: `scripts/print-stream-json.sh`
- Test Directory: `test/`
- Documentation: `docs/tools.md`

## Author
Improvements implemented on 2026-05-03 and 2026-05-04

## Version
Script version: 2.2 (with multiple release support, RHEL version selection, and quiet mode)