# JobHistory - Go Implementation

This is a Go conversion of the Python JobHistory.py script. It extracts CI run information from OpenShift Prow job history pages.

## Version

- **Version**: 1.0.0
- **Date**: 2026-06-04
- **Original Author**: Mark Hamzy (mhamzy@redhat.com)
- **Converted to Go**: 2026-06-04

## Building

### Using Makefile (Recommended)

From the project root directory:

```bash
# Initialize dependencies
make init-jobhistory

# Build the binary
make build-jobhistory

# Install to $GOPATH/bin
make install-jobhistory
```

### Manual Build

```bash
cd JobHistory
go mod download
go mod tidy
go build -o JobHistory *.go
```

## Usage

**Important**: All command-line options must be specified BEFORE the URL(s). Flags placed after URLs will trigger an error.

```bash
# Basic usage (all jobs, no date filter)
./JobHistory https://prow.ci.openshift.org/job-history/gs/test-platform-results/logs/periodic-ci-openshift-multiarch-main-nightly-5.0-ocp-e2e-ovn-powervc-multi-p-p/

# Query today's runs (note: --today comes BEFORE URL)
./JobHistory --today https://prow.ci.openshift.org/job-history/gs/test-platform-results/logs/periodic-ci-openshift-multiarch-main-nightly-5.0-ocp-e2e-ovn-powervc-multi-p-p/

# Query yesterday's runs
./JobHistory --yesterday <URL>

# Query last N days
./JobHistory --last-n-days 7 <URL>

# Output to CSV
./JobHistory --csv --output output.csv <URL>

# Multiple URLs
./JobHistory --today --csv --output output.csv <URL1> <URL2> <URL3>

# Show only deploy failures
./JobHistory --deploy-status-only <URL>

# Show only test failures
./JobHistory --test-status-only <URL>
```

### Common Mistakes

Wrong - Flags after URL:
```bash
./JobHistory <URL> --today  # This will fail!
```

Correct - Flags before URL:
```bash
./JobHistory --today <URL>  # This works!
```

## Command-Line Options

**Note**: All options must be specified BEFORE the URL(s).

- `-a, --after-date <date>`: Only queries after this date (ISO 8601 format)
- `-b, --before-date <date>`: Only queries before this date (ISO 8601 format)
- `-c, --csv`: Output in CSV format
- `-d, --deploy-status-only`: Only show deploy failures
- `-l, --last-n-days <n>`: Only queries for the last n days
- `-o, --output <file>`: The filename for output
- `-t, --test-status-only`: Only show test failures
- `--today`: Only queries for today
- `-v, --version`: Display the version of this program
- `--yesterday`: Only queries for yesterday

## Examples

### Query today's runs for 5.0 OCP e2e tests
```bash
./JobHistory --today https://prow.ci.openshift.org/job-history/gs/test-platform-results/logs/periodic-ci-openshift-multiarch-main-nightly-5.0-ocp-e2e-ovn-powervc-multi-p-p/
```

### Generate CSV report for multiple CI jobs
```bash
./JobHistory --today --csv --output output.csv \
  https://prow.ci.openshift.org/job-history/gs/test-platform-results/logs/periodic-ci-openshift-multiarch-main-nightly-5.0-ocp-e2e-ovn-powervc-multi-p-p/ \
  https://prow.ci.openshift.org/job-history/gs/test-platform-results/logs/periodic-ci-openshift-multiarch-main-nightly-4.23-ocp-e2e-ovn-powervc-multi-p-p/
```

### Query specific date range
```bash
./JobHistory --after-date 2023-07-01 --before-date 2023-07-31 --csv --output july-results.csv <URL>
```

## Key Features

1. **Context Support**: All HTTP requests support context for proper cancellation and timeout handling
2. **Improved Error Handling**: Comprehensive error checking with descriptive error messages
3. **URL Validation**: Validates URLs before processing to catch issues early
4. **Flag Position Validation**: Automatically detects and reports when flags are incorrectly placed after URLs
5. **URL Trailing Slash Handling**: Works correctly with URLs that have or don't have trailing slashes
6. **Comprehensive Help**: Built-in help with examples and usage guidelines
7. **Multiple Date Filters**: Support for --today, --yesterday, --last-n-days, or custom date ranges
8. **Robust HTTP Client**: Configurable timeout and proper resource cleanup

## Key Improvements in v1.0.0

1. **Context Support**: Added context.Context throughout for proper cancellation and timeout handling
2. **Better Error Handling**: All errors are properly checked and wrapped with context
3. **URL Validation**: Added validateURL() function to catch malformed URLs early
4. **HTTP Client Wrapper**: Created HTTPClient type with context-aware methods
5. **Input Validation**: Added validation for dates, URLs, and other inputs
6. **Improved Logging**: Better warning and error messages with context
7. **CSV Output Fix**: Removed non-existent "Zone" column from CSV header
8. **Code Organization**: Better structured with clear separation of concerns
9. **Resource Management**: Proper cleanup of HTTP responses and file handles
10. **Type Safety**: Strongly typed structs for JSON parsing with validation

## Key Differences from Python Version

1. **Dependencies**: Uses `goquery` for HTML parsing instead of BeautifulSoup
2. **HTTP Client**: Uses Go's standard `http.Client` with cookie jar support and context
3. **Concurrency**: Single-threaded but context-aware for future enhancement with goroutines
4. **Error Handling**: Explicit error handling with Go's error return pattern and error wrapping
5. **Type Safety**: Strongly typed structs for JSON parsing with validation
6. **Command-line Interface**: Uses Go's flag package with validation for proper flag placement

## Dependencies

- `github.com/PuerkitoBio/goquery` - HTML parsing and DOM manipulation

## Output Format

### Standard Output
The program outputs information about each CI run including:
- Job URL
- Build status (SUCCESS/FAILURE)
- Build details (error messages if failed)
- Test status (if build succeeded)
- Test details (failing test names if any)

### CSV Output
When using `--csv` flag, outputs a CSV file with columns:
1. Job URL
2. Build summary
3. Build details
4. Test summary
5. Test details

## Notes

- The program queries OpenShift Prow CI job history pages
- It extracts build and test information from GCS (Google Cloud Storage) artifacts
- Supports filtering by date range
- Can process multiple CI job URLs in a single run
- Progress and errors are written to stderr, results to stdout (or specified output file)