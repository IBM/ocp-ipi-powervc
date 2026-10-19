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

// Package main provides a tool for analyzing OpenShift CI job history from Prow.
// It fetches job data, parses build logs, and generates reports on deployment
// success rates and test failures for PowerVC-based CI runs.
//
// # Bucket Options and Authentication
//
// The tool supports both public and private GCS bucket URLs dynamically:
//
//  1. Public buckets (no authentication required):
//     - Any bucket ending in "-public" (e.g. test-platform-results-public) or origin-ci-test
//     - Format: https://prow.ci.openshift.org/job-history/gs/<bucket>/logs/<job-name>
//     - Artifacts are fetched anonymously from gcs.ci.openshift.org.
//
//  2. Private buckets (authentication required):
//     - Other buckets (e.g. test-platform-results)
//     - Format: https://prow.ci.openshift.org/job-history/gs/<bucket>/logs/<job-name>
//     - Artifacts are fetched from storage.googleapis.com.
//
//     Two separate credentials are required for private buckets:
//
//     a. OCP Bearer token — for prow.ci.openshift.org (job history pages).
//     Obtain with:  oc whoami -t
//     Export as:    OCP_CI_TOKEN=<token>  or  OCP_CI_TOKEN_FILE=<path>
//
//     b. Google ADC credential — for storage.googleapis.com (CI build artifacts).
//     Uses Application Default Credentials (ADC) loaded by newGCSTokenSource.
//     One-time setup:
//     gcloud auth application-default login \
//     --scopes=https://www.googleapis.com/auth/devstorage.read_only,\
//     https://www.googleapis.com/auth/cloud-platform
//
// # GCS access: known IAM restriction
//
// The GCS bucket "test-platform-results" is owned by the OpenShift CI team and
// is NOT publicly readable. Access requires the IAM role
// "roles/storage.objectViewer" on the bucket for your Google account.
//
// As of the time of writing, personal Red Hat Google accounts (@redhat.com) do
// NOT have this permission and receive HTTP 403 from storage.googleapis.com.
//
// To gain access, ask the OpenShift CI team (Slack: #forum-ocp-testplatform) to
// grant storage.objectViewer on the test-platform-results bucket to either:
//   - your individual account (e.g. mhamzy@redhat.com), or
//   - the group/domain redhat.com (grants all Red Hat employees read access).
//
// The IAM binding they need to add is:
//
//	gcloud storage buckets add-iam-policy-binding gs://test-platform-results \
//	  --member=user:mhamzy@redhat.com \
//	  --role=roles/storage.objectViewer
package main

import (
	"compress/gzip"
	"compress/zlib"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// Precompiled regular expressions for parsing logs, HTML, and job strings.
var (
	rePodFailed     = regexp.MustCompile(`pod "([^"]+)" failed:`)
	reImageNotFound = regexp.MustCompile(`ERROR: RHCOS image '(.*)' not found in PowerVC cloud`)
	reCreateCluster = regexp.MustCompile(`(?s)8<--------8<--------8<--------8<-------- BEGIN: create cluster 8<--------8<--------8<--------8<--------\n(.*?)8<--------8<--------8<--------8<-------- END: create cluster 8<--------8<--------8<--------8<--------\n`)
	reTestFailures  = regexp.MustCompile(`test-failures-summary_[^.]*\.json`)
	reCIInfo        = regexp.MustCompile(`([^0-9].*)-([0-9]+\.[0-9]+)-(.*)`)
	reAllBuilds     = regexp.MustCompile(`var allBuilds = (\[[^\]]+\]);`)
)

var (
	// version is the build version, replaced at build time with:
	//   -ldflags="-X main.version=$(git describe --always --long --dirty)"
	version = "undefined"

	// release is the release tag, replaced at build time with:
	//   -ldflags="-X main.release=$(git describe --tags --abbrev=0)"
	release = "undefined"
)

// HTTP client and concurrency configuration constants
const (
	defaultHTTPTimeout    = 30 * time.Second
	maxRetries            = 3
	retryDelay            = 1 * time.Second
	defaultMaxConcurrency = 8
)

// Artifact endpoints
const (
	publicArtifactBase  = "https://gcs.ci.openshift.org/gcs"
	privateArtifactBase = "https://storage.googleapis.com"

	gcsHost    = "storage.googleapis.com"
	gcswebHost = "gcs.ci.openshift.org"
)

// prowHosts are hosts that receive the OCP Bearer token if configured.
var prowHosts = map[string]struct{}{
	"prow.ci.openshift.org": {},
}

// Global file handles for output streams
var (
	outputFp = os.Stdout // Primary output destination (results, CSV data)
	infoFp   = os.Stderr // Informational/diagnostic messages
)

// Common errors
var (
	ErrInvalidURL       = errors.New("invalid URL format")
	ErrInvalidDateRange = errors.New("invalid date range")
	ErrHTTPRequest      = errors.New("HTTP request failed")
	ErrParseJSON        = errors.New("failed to parse JSON")
	ErrParseHTML        = errors.New("failed to parse HTML")
)

// CIStats tracks aggregate statistics across all processed CI runs.
// It maintains counters for deployment attempts, successes, and fully green test runs.
type CIStats struct {
	NumDeploys       int // Total number of deployment attempts processed
	DeploysSucceeded int // Number of successful cluster deployments
	GreenRuns        int // Number of runs with all tests passing (no failures)
}

// SpyglassLink represents a single CI job entry from the Prow job history page.
// This structure is parsed from the JavaScript 'allBuilds' array embedded in the HTML.
type SpyglassLink struct {
	SpyglassLink string      `json:"SpyglassLink"` // Relative URL path to the Spyglass job viewer
	ID           string      `json:"ID"`           // Unique job identifier
	Started      string      `json:"Started"`      // Human-readable start time (RFC3339)
	Duration     int64       `json:"Duration"`     // Job duration in seconds
	Result       string      `json:"Result"`       // Overall job result (SUCCESS, FAILURE, etc.)
	Refs         interface{} `json:"Refs"`         // Git references (branch, PR info)
}

// ConfFinished represents the structure of the finished.json artifact file.
// This file is created by Prow at the end of each job step to record its outcome.
type ConfFinished struct {
	Result string `json:"result"` // Step result: SUCCESS, FAILURE, ABORTED, etc.
}

// BuildFinished represents the structure of the finished.json artifact file.
// This file is created by Prow at the end of each job step to record its outcome.
type BuildFinished struct {
	Result string `json:"result"` // Step result: SUCCESS, FAILURE, ABORTED, etc.
}

// StartedJSON represents the structure of the started.json artifact file.
// This file contains the Unix timestamp when a job step began execution.
type StartedJSON struct {
	Timestamp int64 `json:"timestamp"` // Unix epoch timestamp (seconds since 1970-01-01)
}

// TestFailureSummary represents the structure of test-failures-summary_*.json files.
// These files are generated by the test framework and list all failed test cases.
type TestFailureSummary struct {
	Tests []struct {
		Test struct {
			Name string `json:"Name"` // Full test case name (e.g., "[sig-network] DNS should resolve...")
		} `json:"Test"`
	} `json:"Tests"` // Array of failed test entries
}

// HTTPClient wraps http.Client with context support, credentials, and retry logic
type HTTPClient struct {
	client         *http.Client
	bearerToken    string             // OCP Bearer token for prow.ci.openshift.org (if required)
	gcsTokenSource oauth2.TokenSource // ADC token source for storage.googleapis.com (if required)
}

// readCIToken reads the OpenShift CI bearer token from the environment variable
// OCP_CI_TOKEN or from the file specified by OCP_CI_TOKEN_FILE.
func readCIToken() (string, error) {
	if path := strings.TrimSpace(os.Getenv("OCP_CI_TOKEN_FILE")); path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read OCP_CI_TOKEN_FILE: %w", err)
		}
		if token := strings.TrimSpace(string(b)); token != "" {
			return token, nil
		}
	}

	if token := strings.TrimSpace(os.Getenv("OCP_CI_TOKEN")); token != "" {
		return token, nil
	}

	return "", errors.New("set OCP_CI_TOKEN_FILE or OCP_CI_TOKEN")
}

// newGCSTokenSource returns a Google OAuth2 token source using Application
// Default Credentials (ADC). The credentials are read from the file written by:
//
//	gcloud auth application-default login \
//	  --scopes=https://www.googleapis.com/auth/devstorage.read_only,https://www.googleapis.com/auth/cloud-platform
//
// Returns an error with the exact gcloud command to run if credentials are
// not found or have expired.
func newGCSTokenSource(ctx context.Context) (oauth2.TokenSource, error) {
	creds, err := google.FindDefaultCredentials(ctx,
		"https://www.googleapis.com/auth/devstorage.read_only")
	if err != nil {
		return nil, fmt.Errorf(
			"GCS credentials not found — run: gcloud auth application-default login "+
				"--scopes=https://www.googleapis.com/auth/devstorage.read_only,"+
				"https://www.googleapis.com/auth/cloud-platform: %w", err)
	}
	return creds.TokenSource, nil
}

// NewHTTPClient creates a new HTTP client configured for Prow CI interactions.
// It initializes a cookie jar for session management and sets a default timeout.
// If isPrivate is true, it loads the OCP bearer token and Google ADC credentials
// required for accessing private bucket artifacts.
//
// Parameters:
//   - ctx: Context for credential initialization
//   - isPrivate: Whether private bucket authentication is required
//
// Returns:
//   - *HTTPClient: Configured HTTP client wrapper
//   - error: Error if credentials cannot be loaded for private access
func NewHTTPClient(ctx context.Context, isPrivate bool) (*HTTPClient, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create cookie jar: %w", err)
	}

	var token string
	var ts oauth2.TokenSource

	if isPrivate {
		token, err = readCIToken()
		if err != nil {
			return nil, err
		}

		ts, err = newGCSTokenSource(ctx)
		if err != nil {
			return nil, err
		}
	}

	return &HTTPClient{
		client: &http.Client{
			Jar:     jar,
			Timeout: defaultHTTPTimeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 10 {
					return errors.New("stopped after 10 redirects")
				}
				// Prevent forwarding Authorization headers across different hosts.
				if len(via) > 0 && req.URL.Host != via[len(via)-1].URL.Host {
					if req.Header.Get("Authorization") != "" {
						return http.ErrUseLastResponse
					}
				}
				return nil
			},
		},
		bearerToken:    token,
		gcsTokenSource: ts,
	}, nil
}

// isRetryableStatusCode returns true if the HTTP response status code indicates
// a transient error suitable for automatic retry.
func isRetryableStatusCode(code int) bool {
	return code == http.StatusTooManyRequests || // 429
		code == http.StatusBadGateway || // 502
		code == http.StatusServiceUnavailable || // 503
		code == http.StatusGatewayTimeout // 504
}

// Get performs an HTTP GET request with context support, transient error retry,
// and timeout handling.
// It sets authorization headers when appropriate:
//   - For prow.ci.openshift.org: sends Bearer token if configured
//   - For storage.googleapis.com: fetches short-lived ADC access token and sends Bearer token
//   - For gcsweb proxy or public endpoints: makes unauthenticated request
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - urlStr: Target URL to fetch
//
// Returns:
//   - *http.Response: HTTP response (caller must close Body)
//   - error: ErrHTTPRequest if request fails or status is not 200 OK
func (c *HTTPClient) Get(ctx context.Context, urlStr string) (*http.Response, error) {
	parsed, err := url.Parse(urlStr)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrHTTPRequest, err)
	}

	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return nil, fmt.Errorf("%w: invalid scheme %s", ErrHTTPRequest, parsed.Scheme)
	}

	host := parsed.Hostname()

	for attempt := 0; attempt < maxRetries; attempt++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrHTTPRequest, err)
		}

		if _, ok := prowHosts[host]; ok {
			if c.bearerToken != "" {
				if parsed.Scheme != "https" {
					return nil, fmt.Errorf("%w: refusing to send credentials over %s", ErrHTTPRequest, parsed.Scheme)
				}
				req.Header.Set("Authorization", "Bearer "+c.bearerToken)
			}
		} else if host == gcsHost {
			if parsed.Scheme != "https" {
				return nil, fmt.Errorf("%w: refusing to send credentials over %s", ErrHTTPRequest, parsed.Scheme)
			}
			if c.gcsTokenSource == nil {
				return nil, fmt.Errorf("%w: GCS token source not initialized for %s", ErrHTTPRequest, host)
			}
			tok, err := c.gcsTokenSource.Token()
			if err != nil {
				return nil, fmt.Errorf("%w: failed to get GCS token: %v", ErrHTTPRequest, err)
			}
			req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
		}

		resp, err := c.client.Do(req)
		if err != nil {
			if attempt < maxRetries-1 {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(retryDelay * time.Duration(1<<attempt)):
					continue
				}
			}
			return nil, fmt.Errorf("%w: %v", ErrHTTPRequest, err)
		}

		if isRetryableStatusCode(resp.StatusCode) && attempt < maxRetries-1 {
			resp.Body.Close()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(retryDelay * time.Duration(1<<attempt)):
				continue
			}
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, fmt.Errorf("%w: status code %d", ErrHTTPRequest, resp.StatusCode)
		}

		return resp, nil
	}

	return nil, ErrHTTPRequest
}

// parseJobHistoryURL parses a Prow job history URL and extracts the bucket name and job name.
// Expected path format: /job-history/gs/<bucket-name>/logs/<job-name>
func parseJobHistoryURL(urlStr string) (bucket string, jobName string, err error) {
	urlStr = strings.TrimSuffix(urlStr, "/")
	parsed, err := url.Parse(urlStr)
	if err != nil {
		return "", "", fmt.Errorf("%w: %v", ErrInvalidURL, err)
	}

	parts := strings.Split(strings.TrimPrefix(parsed.Path, "/"), "/")
	if len(parts) >= 4 && parts[0] == "job-history" && parts[1] == "gs" && parts[3] == "logs" {
		bucket = parts[2]
		jobName = strings.Join(parts[4:], "/")
		return bucket, jobName, nil
	}

	return "", "", fmt.Errorf("invalid CI URL path: %s (expected /job-history/gs/<bucket>/logs/<job-name>)", parsed.Path)
}

// isPublicBucket dynamically determines if a GCS bucket is public based on its name.
// Any bucket ending in "-public" or named "origin-ci-test" is treated as public.
func isPublicBucket(bucket string) bool {
	return strings.HasSuffix(bucket, "-public") || bucket == "origin-ci-test"
}

// isPrivateURL checks whether the URL points to a private GCS bucket requiring authentication.
func isPrivateURL(urlStr string) bool {
	bucket, _, err := parseJobHistoryURL(urlStr)
	if err != nil {
		return false
	}
	return !isPublicBucket(bucket)
}

// getArtifactBaseURL returns the base artifact URL for the given job history URL.
func getArtifactBaseURL(urlStr string) string {
	bucket, _, err := parseJobHistoryURL(urlStr)
	if err != nil || isPublicBucket(bucket) {
		return publicArtifactBase
	}
	return privateArtifactBase
}

// getArtifactPath converts a Spyglass link into the relative artifact GCS path.
// It strips leading '/view/gs' or similar prefixes and ensures a leading slash.
func getArtifactPath(spyglassLink string) string {
	s := strings.TrimPrefix(spyglassLink, "/view/gs")
	s = strings.TrimPrefix(s, "view/gs")
	s = strings.TrimPrefix(s, "/gs")
	s = strings.TrimPrefix(s, "gs")
	if !strings.HasPrefix(s, "/") {
		s = "/" + s
	}
	return s
}

// getURLString fetches content from the specified URL and returns it as a string.
// It handles HTTP compression (gzip, deflate) and sanitizes invalid UTF-8 characters
// that may appear in CI logs.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - client: HTTP client to use for the request
//   - urlStr: Target URL to fetch
//
// Returns:
//   - string: Response body as a sanitized UTF-8 string
//   - error: ErrInvalidURL if URL is malformed, or HTTP/decompression errors
func getURLString(ctx context.Context, client *HTTPClient, urlStr string) (string, error) {
	if err := validateURL(urlStr); err != nil {
		return "", err
	}

	resp, err := client.Get(ctx, urlStr)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	data, err := getData(resp)
	if err != nil {
		return "", err
	}

	// Handle potential encoding issues - sanitize invalid UTF-8 characters
	str := strings.ToValidUTF8(string(data), " ")
	return str, nil
}

// validateURL checks if the URL is valid and properly formatted.
// It ensures the URL is not empty, can be parsed, and uses HTTP/HTTPS scheme.
//
// Parameters:
//   - urlStr: URL string to validate
//
// Returns:
//   - error: ErrInvalidURL with details if validation fails, nil if valid
func validateURL(urlStr string) error {
	if urlStr == "" {
		return fmt.Errorf("%w: empty URL", ErrInvalidURL)
	}

	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidURL, err)
	}

	if parsedURL.Scheme != "https" && parsedURL.Scheme != "http" {
		return fmt.Errorf("%w: invalid scheme %s", ErrInvalidURL, parsedURL.Scheme)
	}

	return nil
}

// getData handles compressed response data from HTTP responses.
// It automatically detects and decompresses gzip, x-gzip, and deflate encodings.
// For deflate, it attempts zlib decompression first, then falls back to raw deflate.
//
// Parameters:
//   - resp: HTTP response with potential compressed body
//
// Returns:
//   - []byte: Decompressed response body
//   - error: Error if decompression fails or encoding is unknown
func getData(resp *http.Response) ([]byte, error) {
	encoding := resp.Header.Get("Content-Encoding")

	switch encoding {
	case "gzip", "x-gzip":
		reader, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to create gzip reader: %w", err)
		}
		defer reader.Close()
		return io.ReadAll(reader)

	case "deflate":
		reader, err := zlib.NewReader(resp.Body)
		if err != nil {
			// Try raw deflate as fallback
			data, readErr := io.ReadAll(resp.Body)
			if readErr != nil {
				return nil, fmt.Errorf("failed to read deflate data: %w", readErr)
			}
			return data, nil
		}
		defer reader.Close()
		return io.ReadAll(reader)

	case "":
		return io.ReadAll(resp.Body)

	default:
		return nil, fmt.Errorf("unknown content encoding: %s", encoding)
	}
}

// unredact restores redacted strings (such as "XXXXXXX") back to their original
// platform names ("powervc" or "powervs") based on the CI type.
func unredact(s string, ciTypeStr string) string {
	switch getCiType(ciTypeStr) {
	case CI_TYPE_POWERVC:
		return strings.ReplaceAll(s, "XXXXXXX", "powervc")
	case CI_TYPE_POWERVS:
		return strings.ReplaceAll(s, "XXXXXXX", "powervs")
	default:
		return s
	}
}

// detectCouldNotRunSteps examines the top-level build-log.txt for a CI job and
// detects whether the run ended with a "could not run steps" error. When that
// error is present it extracts the names of every pod that is reported as
// failed inside the error message, unredacting any sanitized platform names.
//
// Parameters:
//   - ctx:             Context for cancellation and timeout control
//   - client:          HTTP client to use for fetching the build log
//   - spyglassLink:    Job metadata containing the Spyglass link path
//   - ciTypeStr:       CI type identifier (e.g., "ocp-e2e-ovn-powervc-multi-p-p")
//   - artifactBaseURL: Base URL for artifact retrieval
//
// Returns:
//   - bool:     true if the "could not run steps" error was detected
//   - []string: deduplicated list of pod names that are reported as failed
//   - error:    error if the build log cannot be fetched
func detectCouldNotRunSteps(ctx context.Context, client *HTTPClient, spyglassLink SpyglassLink, ciTypeStr string, artifactBaseURL string) (bool, []string, error) {
	buildLogURL := artifactBaseURL + getArtifactPath(spyglassLink.SpyglassLink) + "/build-log.txt"
	buildLogStr, err := getURLString(ctx, client, buildLogURL)
	if err != nil {
		return false, nil, err
	}

	if !strings.Contains(buildLogStr, "could not run steps") {
		return false, nil, nil
	}

	// Extract every pod name from 'pod "pod-name" failed:' occurrences.
	rawMatches := rePodFailed.FindAllStringSubmatch(buildLogStr, -1)

	// Deduplicate while preserving first-seen order and restoring redacted platform names.
	seen := make(map[string]bool)
	var pods []string
	for _, m := range rawMatches {
		pod := unredact(m[1], ciTypeStr)
		if !seen[pod] {
			seen[pod] = true
			pods = append(pods, pod)
		}
	}

	return true, pods, nil
}

// getJobStartTime extracts the start timestamp of a CI job from metadata or started.json.
func getJobStartTime(ctx context.Context, client *HTTPClient, spyglassLink SpyglassLink, artifactBaseURL string) (time.Time, error) {
	if spyglassLink.Started != "" {
		if startedDt, err := time.Parse(time.RFC3339, spyglassLink.Started); err == nil {
			return startedDt.UTC(), nil
		}
		if startedDt, err := fromISOFormat(spyglassLink.Started); err == nil {
			return startedDt, nil
		}
	}

	if spyglassLink.SpyglassLink == "" {
		return time.Time{}, fmt.Errorf("empty spyglass link")
	}

	startedURL := artifactBaseURL + getArtifactPath(spyglassLink.SpyglassLink) + "/started.json"
	startedStr, err := getURLString(ctx, client, startedURL)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to fetch started.json: %w", err)
	}

	var startedJSON StartedJSON
	if err := json.Unmarshal([]byte(startedStr), &startedJSON); err != nil {
		return time.Time{}, fmt.Errorf("%w: %v", ErrParseJSON, err)
	}

	if startedJSON.Timestamp <= 0 {
		return time.Time{}, fmt.Errorf("invalid timestamp: %d", startedJSON.Timestamp)
	}

	return time.Unix(startedJSON.Timestamp, 0).UTC(), nil
}

// includeWithDate checks if a CI job should be included based on date range filtering.
// It returns:
//   - bool: true if the job is included in the date range
//   - bool: true if the job started strictly before afterDt (indicating pagination can stop)
//   - error: Error if start date cannot be determined
func includeWithDate(ctx context.Context, client *HTTPClient, afterDt, beforeDt time.Time, spyglassLink SpyglassLink, artifactBaseURL string) (bool, bool, error) {
	startedDt, err := getJobStartTime(ctx, client, spyglassLink, artifactBaseURL)
	if err != nil {
		return false, false, err
	}

	isOlderThanAfter := !afterDt.IsZero() && startedDt.Before(afterDt)
	included := (startedDt.After(afterDt) && startedDt.Before(beforeDt)) || startedDt.Equal(afterDt) || startedDt.Equal(beforeDt)

	return included, isOlderThanAfter, nil
}

// CiType represents the type of CI (Continuous Integration) environment being used.
// It is used to differentiate between different cloud infrastructure platforms
// and determine the appropriate step names and configurations for each platform.
type CiType int

// CI type constants define the supported cloud infrastructure platforms.
const (
	CI_TYPE_UNKNOWN CiType = iota // Unknown or unsupported CI type (value: 0)
	CI_TYPE_POWERVC               // PowerVC cloud infrastructure platform (value: 1)
	CI_TYPE_POWERVS               // PowerVS cloud infrastructure platform (value: 2)
)

// getCiType determines the CI type from a string identifier.
//
// Parameters:
//   - ciTypeStr: A string containing the CI type identifier, typically from a job name
//
// Returns:
//   - CI_TYPE_POWERVC if the string contains "-powervc-"
//   - CI_TYPE_POWERVS if the string contains "-powervs-"
//   - CI_TYPE_UNKNOWN if no recognized platform marker is found
func getCiType(ciTypeStr string) CiType {
	if strings.Contains(ciTypeStr, "-powervc-") || strings.Contains(ciTypeStr, "-powervc") {
		return CI_TYPE_POWERVC
	} else if strings.Contains(ciTypeStr, "-powervs-") || strings.Contains(ciTypeStr, "-powervs") {
		return CI_TYPE_POWERVS
	} else {
		return CI_TYPE_UNKNOWN
	}
}

// CI step phase constants define the different phases of a CI pipeline execution.
const (
	CI_STEP_CONF    = iota // Configuration phase (value: 0) - cluster configuration setup
	CI_STEP_INSTALL        // Installation phase (value: 1) - OpenShift installation
	CI_STEP_TEST           // Testing phase (value: 2) - end-to-end testing
)

// getCiStepName returns the CI step name for a given CI type and step phase.
// It maps CI types (PowerVC or PowerVS) to their corresponding step names
// for different phases of the CI pipeline (configuration, installation, testing).
//
// Step phases:
//   - CI_STEP_CONF (0): Configuration phase ("/ipi-conf-powervc" or "/ipi-conf-powervs")
//   - CI_STEP_INSTALL (1): Installation phase ("/ipi-install-powervc-install" or "/ipi-install-powervs-install")
//   - CI_STEP_TEST (2): Testing phase ("/openshift-e2e-libvirt-test")
//
// Parameters:
//   - ciType: CI type enum value (PowerVC or PowerVS)
//   - stepPhase: Phase constant (CI_STEP_CONF, CI_STEP_INSTALL, or CI_STEP_TEST)
//
// Returns:
//   - string: CI step name path (e.g., "/ipi-conf-powervc", "/ipi-install-powervc-install", "/openshift-e2e-libvirt-test")
//   - error: Error if stepPhase is out of range or CI type is not recognized
func getCiStepName(ciType CiType, stepPhase int) (string, error) {
	var (
		ciStepName = map[CiType][]string{
			CI_TYPE_POWERVC: {
				"/ipi-conf-powervc",
				"/ipi-install-powervc-install",
				"/openshift-e2e-libvirt-test",
			},
			CI_TYPE_POWERVS: {
				"/ipi-conf-powervs",
				"/ipi-install-powervs-install",
				"/openshift-e2e-libvirt-test",
			},
		}
	)

	if stepPhase < CI_STEP_CONF || stepPhase > CI_STEP_TEST {
		return "", fmt.Errorf("unknown step phase %d", stepPhase)
	}

	if ciType <= CI_TYPE_UNKNOWN || ciType > CI_TYPE_POWERVS {
		return "", fmt.Errorf("unknown CI type %d", ciType)
	}

	return ciStepName[ciType][stepPhase], nil
}

// gatherConfRun gathers build/conf information for a CI job.
// It fetches finished.json to determine if cluster configuration succeeded,
// and extracts failure details from the build log if it failed.
//
// Parameters:
//   - ctx:             Context for cancellation and timeout control
//   - client:          HTTP client to use for fetching artifacts
//   - spyglassLink:    Job metadata containing the Spyglass link path
//   - ciTypeStr:       CI type identifier (e.g., "ocp-e2e-ovn-powervc-multi-p-p", "ocp-e2e-ovn-powervs-capi-multi-p-p")
//   - artifactBaseURL: Base URL for artifact retrieval
//
// Returns:
//   - ConfFinished: Parsed finished.json with result status
//   - string: Conf summary message (SUCCESS/FAILURE with description)
//   - string: Detailed error output from build log (empty if successful)
//   - error: Error if artifacts cannot be fetched or CI type is invalid
func gatherConfRun(ctx context.Context, client *HTTPClient, spyglassLink SpyglassLink, ciTypeStr string, artifactBaseURL string) (ConfFinished, string, string, error) {
	confFinished := ConfFinished{Result: "FAILURE"}
	confSummaryStr := ""
	confDetailsStr := ""

	if ciTypeStr == "" {
		return confFinished, confSummaryStr, confDetailsStr, fmt.Errorf("empty CI type string")
	}

	ciType := getCiType(ciTypeStr)
	if ciType == CI_TYPE_UNKNOWN {
		return confFinished, confSummaryStr, confDetailsStr, fmt.Errorf("unknown CI type %d", ciType)
	}

	ciStepName, err := getCiStepName(ciType, CI_STEP_CONF)
	if err != nil {
		return confFinished, confSummaryStr, confDetailsStr, fmt.Errorf("failed to fetch conf name: %w", err)
	}

	finishedURL := artifactBaseURL + getArtifactPath(spyglassLink.SpyglassLink) + "/artifacts/" + ciTypeStr + ciStepName + "/finished.json"
	finishedStr, err := getURLString(ctx, client, finishedURL)
	if err == nil && !strings.Contains(finishedStr, "<!doctype html>") {
		if err := json.Unmarshal([]byte(finishedStr), &confFinished); err != nil {
			fmt.Fprintf(infoFp, "WARN: Failed to parse finished.json: %v\n", err)
		}
	}

	if confFinished.Result == "SUCCESS" {
		confSummaryStr = "SUCCESS: conf succeeded!"
		return confFinished, confSummaryStr, confDetailsStr, nil
	}

	buildLogURL := artifactBaseURL + getArtifactPath(spyglassLink.SpyglassLink) + "/artifacts/" + ciTypeStr + ciStepName + "/build-log.txt"
	buildLogStr, err := getURLString(ctx, client, buildLogURL)
	if err != nil {
		return confFinished, confSummaryStr, confDetailsStr, fmt.Errorf("failed to fetch conf log: %w", err)
	}

	matches := reImageNotFound.FindStringSubmatch(buildLogStr)

	if len(matches) >= 2 {
		confSummaryStr = "FAILURE: conf failed!"
		confDetailsStr = unredact(matches[0], ciTypeStr)
	} else {
		confSummaryStr = "FAILURE: Could not find conf?"
	}

	return confFinished, confSummaryStr, confDetailsStr, nil
}

// gatherBuildRun gathers build/deployment information for a CI job.
// It fetches finished.json to determine if cluster deployment succeeded,
// and extracts failure details from the build log if it failed.
//
// Parameters:
//   - ctx:             Context for cancellation and timeout control
//   - client:          HTTP client to use for fetching artifacts
//   - spyglassLink:    Job metadata containing the Spyglass link path
//   - ciTypeStr:       CI type identifier (e.g., "ocp-e2e-ovn-powervc-multi-p-p", "ocp-e2e-ovn-powervs-capi-multi-p-p")
//   - artifactBaseURL: Base URL for artifact retrieval
//
// Returns:
//   - BuildFinished: Parsed finished.json with result status
//   - string: Build summary message (SUCCESS/FAILURE with description)
//   - string: Detailed error output from build log (empty if successful)
//   - error: Error if artifacts cannot be fetched or CI type is invalid
func gatherBuildRun(ctx context.Context, client *HTTPClient, spyglassLink SpyglassLink, ciTypeStr string, artifactBaseURL string) (BuildFinished, string, string, error) {
	buildFinished := BuildFinished{Result: "FAILURE"}
	buildSummaryStr := ""
	buildDetailsStr := ""

	if ciTypeStr == "" {
		return buildFinished, buildSummaryStr, buildDetailsStr, fmt.Errorf("empty CI type string")
	}

	ciType := getCiType(ciTypeStr)
	if ciType == CI_TYPE_UNKNOWN {
		return buildFinished, buildSummaryStr, buildDetailsStr, fmt.Errorf("unknown CI type %d", ciType)
	}

	ciStepName, err := getCiStepName(ciType, CI_STEP_INSTALL)
	if err != nil {
		return buildFinished, buildSummaryStr, buildDetailsStr, fmt.Errorf("failed to fetch install name: %w", err)
	}

	finishedURL := artifactBaseURL + getArtifactPath(spyglassLink.SpyglassLink) + "/artifacts/" + ciTypeStr + ciStepName + "/finished.json"
	finishedStr, err := getURLString(ctx, client, finishedURL)
	if err == nil && !strings.Contains(finishedStr, "<!doctype html>") {
		if err := json.Unmarshal([]byte(finishedStr), &buildFinished); err != nil {
			fmt.Fprintf(infoFp, "WARN: Failed to parse finished.json: %v\n", err)
		}
	}

	if buildFinished.Result == "SUCCESS" {
		buildSummaryStr = "SUCCESS: create cluster succeeded!"
		return buildFinished, buildSummaryStr, buildDetailsStr, nil
	}

	buildLogURL := artifactBaseURL + getArtifactPath(spyglassLink.SpyglassLink) + "/artifacts/" + ciTypeStr + ciStepName + "/build-log.txt"
	buildLogStr, err := getURLString(ctx, client, buildLogURL)
	if err != nil {
		return buildFinished, buildSummaryStr, buildDetailsStr, fmt.Errorf("failed to fetch build log: %w", err)
	}

	matches := reCreateCluster.FindStringSubmatch(buildLogStr)
	if len(matches) >= 2 {
		buildSummaryStr = "FAILURE: create cluster failed!"
		buildDetailsStr = unredact(matches[1], ciTypeStr)
	} else {
		buildSummaryStr = "FAILURE: Could not find create cluster?"
	}

	return buildFinished, buildSummaryStr, buildDetailsStr, nil
}

// gatherTestRun gathers test execution results for a CI job.
// It fetches test-failures-summary JSON from the junit artifacts directory.
//
// Parameters:
//   - ctx:             Context for cancellation and timeout control
//   - client:          HTTP client to use for fetching artifacts
//   - spyglassLink:    Job metadata containing the Spyglass link path
//   - ciTypeStr:       CI type identifier (e.g., "ocp-e2e-ovn-powervc-multi-p-p", "ocp-e2e-ovn-powervs-capi-multi-p-p")
//   - ciStats:         Statistics tracker to update (increments GreenRuns on success)
//   - artifactBaseURL: Base URL for artifact retrieval
//
// Returns:
//   - string: Test summary message (SUCCESS/FAILURE/INFO with description)
//   - string: Detailed list of failed test names (empty if all passed or skipped)
//   - error: Error if artifacts cannot be fetched or CI type is invalid
func gatherTestRun(ctx context.Context, client *HTTPClient, spyglassLink SpyglassLink, ciTypeStr string, ciStats *CIStats, artifactBaseURL string) (string, string, error) {
	testSummaryStr := ""
	testDetailsStr := ""

	if ciTypeStr == "" {
		return testSummaryStr, testDetailsStr, fmt.Errorf("empty CI type string")
	}

	ciType := getCiType(ciTypeStr)
	if ciType == CI_TYPE_UNKNOWN {
		return testSummaryStr, testDetailsStr, fmt.Errorf("unknown CI type %d", ciType)
	}

	ciStepName, err := getCiStepName(ciType, CI_STEP_TEST)
	if err != nil {
		return testSummaryStr, testDetailsStr, fmt.Errorf("failed to fetch test name: %w", err)
	}

	junitDirURL := artifactBaseURL + getArtifactPath(spyglassLink.SpyglassLink) + "/artifacts/" + ciTypeStr + ciStepName + "/artifacts/junit/"
	junitDirStr, err := getURLString(ctx, client, junitDirURL)
	if err != nil {
		return testSummaryStr, testDetailsStr, fmt.Errorf("failed to fetch junit directory: %w", err)
	}

	matches := reTestFailures.FindStringSubmatch(junitDirStr)
	if len(matches) == 0 {
		testSummaryStr = "INFO: No test failure summary found (test suite may have been skipped or aborted)"
		return testSummaryStr, testDetailsStr, nil
	}

	testFailureFilename := matches[0]
	junitURL := artifactBaseURL + getArtifactPath(spyglassLink.SpyglassLink) + "/artifacts/" + ciTypeStr + ciStepName + "/artifacts/junit/" + testFailureFilename
	junitStr, err := getURLString(ctx, client, junitURL)
	if err != nil {
		return testSummaryStr, testDetailsStr, fmt.Errorf("failed to fetch test failures: %w", err)
	}

	var testFailureSummary TestFailureSummary
	if err := json.Unmarshal([]byte(junitStr), &testFailureSummary); err != nil {
		return testSummaryStr, testDetailsStr, fmt.Errorf("%w: %v", ErrParseJSON, err)
	}

	if len(testFailureSummary.Tests) == 0 {
		testSummaryStr = "SUCCESS: All tests succeeded!"
		ciStats.GreenRuns++
	} else {
		testSummaryStr = "FAILURE: Failing tests:"
		var sb strings.Builder
		for _, test := range testFailureSummary.Tests {
			sb.WriteString(test.Test.Name)
			sb.WriteByte('\n')
		}
		testDetailsStr = sb.String()
	}

	return testSummaryStr, testDetailsStr, nil
}

// fromISOFormat attempts to parse a date string using multiple ISO 8601 formats.
// Returns the parsed time in UTC, or an error if no format matches.
func fromISOFormat(dateStr string) (time.Time, error) {
	if dateStr == "" {
		return time.Time{}, fmt.Errorf("empty date string")
	}

	formats := []string{
		"2006-01-02T15:04:05.999999Z",
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05.999999",
		"2006-01-02T15:04:05",
		"2006-01-02",
	}

	for _, format := range formats {
		t, err := time.Parse(format, dateStr)
		if err == nil {
			return t.UTC(), nil
		}
	}

	return time.Time{}, fmt.Errorf("unable to parse date: %s", dateStr)
}

// encodeString prepares a string for CSV output by escaping newline characters.
//
// Parameters:
//   - input: String to encode (may contain newlines)
//
// Returns:
//   - string: Encoded string with newlines replaced by literal "\n"
func encodeString(input string) string {
	return strings.ReplaceAll(input, "\n", "\\n")
}

// jobRunData stores the gathered data for a single CI run.
type jobRunData struct {
	jobURL           string
	included         bool
	isOlderThanAfter bool
	confFinished     ConfFinished
	confSummaryStr   string
	confDetailsStr   string
	buildFinished    BuildFinished
	buildSummaryStr  string
	buildDetailsStr  string
	testSummaryStr   string
	testDetailsStr   string
	isGreenTestRun   bool
	couldNotRunSteps bool
	failedPods       []string
	err              error
}

// processSingleJob fetches and parses artifact data for a single CI run.
func processSingleJob(ctx context.Context, client *HTTPClient, args *Args, spyglassLink SpyglassLink, ciTypeStr string, artifactBaseURL string, afterDt, beforeDt time.Time) jobRunData {
	data := jobRunData{
		jobURL: "https://prow.ci.openshift.org" + spyglassLink.SpyglassLink,
	}

	include, isOlder, err := includeWithDate(ctx, client, afterDt, beforeDt, spyglassLink, artifactBaseURL)
	data.isOlderThanAfter = isOlder
	if err != nil {
		errStr := err.Error()
		if !strings.Contains(errStr, "status code 404") &&
			!strings.Contains(errStr, "failed to fetch started.json") &&
			!strings.Contains(errStr, "failed to parse JSON") &&
			!strings.Contains(errStr, "invalid character '<'") {
			data.err = fmt.Errorf("failed to check date for job %s: %w", data.jobURL, err)
		}
		return data
	}
	if !include {
		return data
	}
	data.included = true

	// Gather deployment/conf results
	confFinished, confSummaryStr, confDetailsStr, err := gatherConfRun(ctx, client, spyglassLink, ciTypeStr, artifactBaseURL)
	if err != nil {
		data.err = fmt.Errorf("failed to gather conf run: %w", err)
		return data
	}
	data.confFinished = confFinished
	data.confSummaryStr = confSummaryStr
	data.confDetailsStr = confDetailsStr

	// Gather deployment/build results
	buildFinished, buildSummaryStr, buildDetailsStr, err := gatherBuildRun(ctx, client, spyglassLink, ciTypeStr, artifactBaseURL)
	if err != nil {
		data.err = fmt.Errorf("failed to gather build run: %w", err)
		return data
	}
	data.buildFinished = buildFinished
	data.buildSummaryStr = buildSummaryStr
	data.buildDetailsStr = buildDetailsStr

	// If deployment succeeded, gather test results
	if buildFinished.Result == "SUCCESS" {
		if !args.DeployStatusOnly {
			var tempStats CIStats
			testSummaryStr, testDetailsStr, err := gatherTestRun(ctx, client, spyglassLink, ciTypeStr, &tempStats, artifactBaseURL)
			if err != nil {
				data.testSummaryStr = fmt.Sprintf("WARN: Failed to gather test run: %v", err)
			} else {
				data.testSummaryStr = testSummaryStr
				data.testDetailsStr = testDetailsStr
				if tempStats.GreenRuns > 0 {
					data.isGreenTestRun = true
				}
			}
		}
	}

	// Detect "could not run steps" error and list failed pods
	if detected, failedPods, err := detectCouldNotRunSteps(ctx, client, spyglassLink, ciTypeStr, artifactBaseURL); err == nil && detected {
		data.couldNotRunSteps = true
		data.failedPods = failedPods
	}

	return data
}

// processURL processes a single Prow CI job history URL and extracts job data.
// It determines whether the URL uses a public or private bucket, sets up the
// appropriate authentication and artifact endpoints, iterates through job history
// pages, filters jobs by date concurrently, and outputs results in order.
//
// Parameters:
//   - ctx:       Context for cancellation and timeout control
//   - args:      Command-line arguments controlling output format and filtering
//   - ciStats:   Statistics tracker to update with job results
//   - urlStr:    Prow job history URL to process
//   - afterDt:   Start of date range filter (inclusive)
//   - beforeDt:  End of date range filter (inclusive)
//   - csvWriter: CSV writer for output (nil if not in CSV mode)
//
// Returns:
//   - error: Error if URL is invalid, parsing fails, or HTTP requests fail
func processURL(ctx context.Context, args *Args, ciStats *CIStats, urlStr string, afterDt, beforeDt time.Time, csvWriter *csv.Writer) error {
	fmt.Fprintf(infoFp, "INFO: For URL %s\n", urlStr)
	fmt.Fprintf(infoFp, "INFO: Finding CI runs between %s and %s\n", afterDt.Format(time.RFC3339), beforeDt.Format(time.RFC3339))
	fmt.Fprintln(infoFp)

	// Validate and parse URL
	if err := validateURL(urlStr); err != nil {
		return err
	}

	// Determine bucket type and artifact base URL dynamically
	isPrivate := isPrivateURL(urlStr)
	artifactBaseURL := getArtifactBaseURL(urlStr)

	// Remove trailing slash if present
	urlStr = strings.TrimSuffix(urlStr, "/")
	parts := strings.Split(urlStr, "/")
	if len(parts) < 3 {
		return fmt.Errorf("%w: insufficient URL parts", ErrInvalidURL)
	}

	baseURLStr := parts[0] + "//" + parts[2]
	idx := strings.LastIndex(urlStr, "/")
	if idx == -1 {
		return fmt.Errorf("%w: no path separator found", ErrInvalidURL)
	}
	ciStr := urlStr[idx+1:]

	// Extract CI version (e.g., "4.12", "5.0") and type (e.g., "ocp-e2e-ovn-powervc-multi-p-p") from job name
	matches := reCIInfo.FindStringSubmatch(ciStr)
	if len(matches) < 4 {
		return fmt.Errorf("failed to parse CI string: %s", ciStr)
	}

	// _ := matches[1]            (e.g. "periodic-ci-openshift-multiarch-main-nightly")
	ciVersionStr := matches[2] // OpenShift version (e.g., "4.21", "4.22")
	ciTypeStr := matches[3]    // CI type/platform (e.g., "ocp-e2e-ovn-powervc-multi-p-p", "ocp-e2e-ovn-powervs-capi-multi-p-p")

	// Create HTTP client (authenticated if private bucket)
	client, err := NewHTTPClient(ctx, isPrivate)
	if err != nil {
		return fmt.Errorf("failed to create HTTP client: %w", err)
	}

	// Main pagination loop: process current page, then follow "Older Runs" link
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		processedAny := false     // Track if we processed any jobs on this page
		reachedOlderJobs := false // Track if we encountered jobs older than afterDt

		resp, err := client.Get(ctx, urlStr)
		if err != nil {
			return err
		}

		doc, err := goquery.NewDocumentFromReader(resp.Body)
		resp.Body.Close()
		if err != nil {
			return fmt.Errorf("%w: %v", ErrParseHTML, err)
		}

		// Find the "Older Runs" pagination link
		var olderHref string
		doc.Find("td").Each(func(i int, s *goquery.Selection) {
			if strings.Contains(s.Text(), "Older Runs") {
				s.Find("a").Each(func(j int, a *goquery.Selection) {
					if href, exists := a.Attr("href"); exists {
						olderHref = href
					}
				})
			}
		})

		// Find and process the allBuilds JavaScript array
		doc.Find("script").Each(func(i int, s *goquery.Selection) {
			scriptText := s.Text()
			if !strings.Contains(scriptText, "var allBuilds") {
				return
			}

			matches := reAllBuilds.FindStringSubmatch(scriptText)
			if len(matches) < 2 {
				return
			}

			var spyglassLinks []SpyglassLink
			if err := json.Unmarshal([]byte(matches[1]), &spyglassLinks); err != nil {
				fmt.Fprintf(infoFp, "WARN: Failed to parse allBuilds: %v\n", err)
				return
			}

			// --latest: only consider the first (most recent) entry in the page
			if args.Latest && len(spyglassLinks) > 0 {
				spyglassLinks = spyglassLinks[:1]
			}

			// Process jobs concurrently with bounded concurrency worker pool
			results := make([]jobRunData, len(spyglassLinks))
			sem := make(chan struct{}, args.Concurrency)
			var wg sync.WaitGroup

			for i, link := range spyglassLinks {
				wg.Add(1)
				go func(idx int, spLink SpyglassLink) {
					defer wg.Done()
					select {
					case sem <- struct{}{}:
						defer func() { <-sem }()
					case <-ctx.Done():
						return
					}
					results[idx] = processSingleJob(ctx, client, args, spLink, ciTypeStr, artifactBaseURL, afterDt, beforeDt)
				}(i, link)
			}
			wg.Wait()

			// Output results in exact chronological page order
			for _, res := range results {
				select {
				case <-ctx.Done():
					return
				default:
				}

				if res.isOlderThanAfter {
					reachedOlderJobs = true
				}

				if res.err != nil {
					fmt.Fprintf(infoFp, "WARN: %v\n", res.err)
					continue
				}

				if !res.included {
					continue
				}

				processedAny = true
				ciStats.NumDeploys++

				// Print job header with progress counter
				fmt.Fprintf(infoFp, "INFO: 8<--------8<--------8<--------8<--------8<--------8<--------8<--------8<--------\n")
				fmt.Fprintf(infoFp, "INFO: [Job %d] URL:  %s\n", ciStats.NumDeploys, res.jobURL)

				// Output conf results unless user only wants test status
				if !args.TestStatusOnly && !args.CSV {
					fmt.Fprintf(outputFp, "%s\n%s\n", res.confSummaryStr, res.confDetailsStr)
				}

				// Output build results unless user only wants test status
				if !args.TestStatusOnly && !args.CSV {
					fmt.Fprintf(outputFp, "%s\n%s", res.buildSummaryStr, res.buildDetailsStr)
				}

				// If deployment succeeded, handle test results
				if res.buildFinished.Result == "SUCCESS" {
					ciStats.DeploysSucceeded++

					if res.isGreenTestRun {
						ciStats.GreenRuns++
					}

					if !args.DeployStatusOnly {
						if !args.CSV && res.testSummaryStr != "" {
							fmt.Fprintf(outputFp, "%s\n%s\n", res.testSummaryStr, res.testDetailsStr)
						}
					} else if !args.CSV {
						fmt.Fprintln(outputFp)
					}
				} else if !args.CSV {
					fmt.Fprintln(outputFp)
				}

				// Output could-not-run-steps error
				if res.couldNotRunSteps && !args.CSV {
					fmt.Fprintf(outputFp, "FAILURE: could not run steps\n")
					for _, pod := range res.failedPods {
						fmt.Fprintf(outputFp, "  failed pod: %s\n", pod)
					}
					fmt.Fprintln(outputFp)
				}

				// Write CSV row if CSV output is enabled
				if args.CSV {
					details := strings.TrimSuffix(res.testDetailsStr, "\n")
					csvRow := []string{
						encodeString(res.jobURL),
						encodeString(res.buildSummaryStr),
						encodeString(res.buildDetailsStr),
						encodeString(res.testSummaryStr),
						encodeString(details),
					}
					if err := csvWriter.Write(csvRow); err != nil {
						fmt.Fprintf(infoFp, "WARN: Failed to write CSV row: %v\n", err)
					}
					fmt.Fprintln(infoFp)
				}
			}
		})

		// --latest: stop after the first page that yielded a job
		if args.Latest && processedAny {
			break
		}

		// Early termination: stop pagination if all remaining jobs are older than afterDt
		if reachedOlderJobs {
			break
		}

		// Stop pagination if no "Older Runs" link exists
		if olderHref == "" {
			break
		}

		// Stop pagination if we didn't process any jobs on this page
		if !processedAny {
			break
		}

		// Continue to the next page
		urlStr = baseURLStr + olderHref
	}

	_ = ciVersionStr // Suppress unused variable warning

	return nil
}

// Args holds all command-line arguments and configuration options.
type Args struct {
	AfterStr         string    // Start date for filtering jobs (ISO 8601 format)
	BeforeStr        string    // End date for filtering jobs (ISO 8601 format)
	AfterDt          time.Time // Parsed start date (set by parseAndValidateArgs)
	BeforeDt         time.Time // Parsed end date (set by parseAndValidateArgs)
	Concurrency      int       // Number of concurrent workers (default: 8)
	CSV              bool      // Enable CSV output format
	DeployStatusOnly bool      // Only report deployment status (skip test results)
	LastNDays        int       // Filter to last N days (alternative to AfterStr/BeforeStr)
	Latest           bool      // Only process the single most recent CI run
	Output           string    // Output file path (empty = stdout)
	TestStatusOnly   bool      // Only report test status (skip deployment details)
	Today            bool      // Filter to today's jobs only
	URLs             []string  // List of Prow job history URLs to process
	Yesterday        bool      // Filter to yesterday's jobs only
}

// parseAndValidateArgs parses command-line arguments and validates them.
// It sets up flag definitions, parses the command line, validates the arguments,
// and calculates the date range based on the provided options.
//
// Returns:
//   - *Args: Parsed and validated arguments
//   - time.Time: Start date for filtering (afterDt)
//   - time.Time: End date for filtering (beforeDt)
//   - error: Error if validation fails, nil otherwise
func parseAndValidateArgs() (*Args, time.Time, time.Time, error) {
	args := &Args{}

	// Custom usage function
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [OPTIONS] URL [URL...]\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Analyze OpenShift CI job history from Prow for PowerVC deployments.\n\n")
		fmt.Fprintf(os.Stderr, "Environment Variables (for private bucket URLs):\n")
		fmt.Fprintf(os.Stderr, "  OCP_CI_TOKEN       OCP Bearer token for prow.ci.openshift.org\n")
		fmt.Fprintf(os.Stderr, "                     (obtain with: oc whoami -t)\n")
		fmt.Fprintf(os.Stderr, "  OCP_CI_TOKEN_FILE  Path to file containing the OCP Bearer token\n\n")
		fmt.Fprintf(os.Stderr, "GCS credentials (for private bucket URLs, one-time setup):\n")
		fmt.Fprintf(os.Stderr, "  gcloud auth application-default login \\\n")
		fmt.Fprintf(os.Stderr, "    --scopes=https://www.googleapis.com/auth/devstorage.read_only,https://www.googleapis.com/auth/cloud-platform\n\n")
		fmt.Fprintf(os.Stderr, "Required Arguments:\n")
		fmt.Fprintf(os.Stderr, "  URL    One or more Prow job history URLs to analyze\n")
		fmt.Fprintf(os.Stderr, "         Format: https://prow.ci.openshift.org/job-history/gs/<bucket>/logs/<job-name>\n")
		fmt.Fprintf(os.Stderr, "         Public:  gs/test-platform-results-public/logs/...\n")
		fmt.Fprintf(os.Stderr, "         Private: gs/test-platform-results/logs/...\n\n")
		fmt.Fprintf(os.Stderr, "Options (must be specified BEFORE URLs):\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExamples:\n")
		fmt.Fprintf(os.Stderr, "  # Analyze today's jobs (public bucket)\n")
		fmt.Fprintf(os.Stderr, "  %s --today https://prow.ci.openshift.org/job-history/gs/test-platform-results-public/logs/periodic-ci-openshift-multiarch-main-nightly-5.0-ocp-e2e-ovn-powervc-multi-p-p/\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  # Analyze today's jobs (private bucket)\n")
		fmt.Fprintf(os.Stderr, "  %s --today https://prow.ci.openshift.org/job-history/gs/test-platform-results/logs/periodic-ci-openshift-multiarch-main-nightly-5.0-ocp-e2e-ovn-powervc-multi-p-p/\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  # Analyze last 7 days with CSV output and 12 workers\n")
		fmt.Fprintf(os.Stderr, "  %s --last-n-days 7 -j 12 --csv --output results.csv https://prow.ci.openshift.org/job-history/gs/test-platform-results-public/logs/periodic-ci-openshift-multiarch-main-nightly-5.0-ocp-e2e-ovn-powervc-multi-p-p/\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Note: All options must be specified before the URL(s).\n")
	}

	// Define command-line flags
	flag.StringVar(&args.AfterStr, "after-date", "", "Only queries after this date (ISO 8601 format)")
	flag.StringVar(&args.AfterStr, "a", "", "Only queries after this date (shorthand)")
	flag.StringVar(&args.BeforeStr, "before-date", "", "Only queries before this date (ISO 8601 format)")
	flag.StringVar(&args.BeforeStr, "b", "", "Only queries before this date (shorthand)")
	flag.IntVar(&args.Concurrency, "concurrency", defaultMaxConcurrency, "Number of concurrent workers for artifact fetching (1-32)")
	flag.IntVar(&args.Concurrency, "j", defaultMaxConcurrency, "Number of concurrent workers for artifact fetching (shorthand)")
	flag.BoolVar(&args.CSV, "csv", false, "Output in CSV format")
	flag.BoolVar(&args.CSV, "c", false, "Output in CSV format (shorthand)")
	flag.BoolVar(&args.DeployStatusOnly, "deploy-status-only", false, "Only show deploy failures")
	flag.BoolVar(&args.DeployStatusOnly, "d", false, "Only show deploy failures (shorthand)")
	flag.IntVar(&args.LastNDays, "last-n-days", 0, "Only queries for the last n days")
	flag.IntVar(&args.LastNDays, "l", 0, "Only queries for the last n days (shorthand)")
	flag.StringVar(&args.Output, "output", "", "The filename for output")
	flag.StringVar(&args.Output, "o", "", "The filename for output (shorthand)")
	flag.BoolVar(&args.Latest, "latest", false, "Only process the single most recent CI run")
	flag.BoolVar(&args.TestStatusOnly, "test-status-only", false, "Only show test failures")
	flag.BoolVar(&args.TestStatusOnly, "t", false, "Only show test failures (shorthand)")
	flag.BoolVar(&args.Today, "today", false, "Only queries for today")
	flag.BoolVar(&args.Yesterday, "yesterday", false, "Only queries for yesterday")

	showVersion := flag.Bool("version", false, "Display the version of this program")
	flag.BoolVar(showVersion, "v", false, "Display the version of this program (shorthand)")

	flag.Parse()

	// Handle version flag
	if *showVersion {
		fmt.Printf("Program version is %v, release = %v\n", version, release)
		os.Exit(0)
	}

	// Validate concurrency
	if args.Concurrency <= 0 {
		args.Concurrency = defaultMaxConcurrency
	} else if args.Concurrency > 32 {
		args.Concurrency = 32
	}

	// Collect positional arguments as URLs
	args.URLs = flag.Args()
	if len(args.URLs) == 0 {
		return nil, time.Time{}, time.Time{}, fmt.Errorf("at least one URL is required")
	}

	// Check if any positional arguments look like flags
	for _, arg := range args.URLs {
		if strings.HasPrefix(arg, "-") || strings.HasPrefix(arg, "--") {
			return nil, time.Time{}, time.Time{}, fmt.Errorf("flag '%s' appears after URL(s); all options must be specified BEFORE the URL arguments", arg)
		}
	}

	// Check if any URL is unsupported
	for _, url := range args.URLs {
		// Validate and parse URL
		if err := validateURL(url); err != nil {
			return nil, time.Time{}, time.Time{}, err
		}

		bucket, _, err := parseJobHistoryURL(url)
		if err != nil || bucket == "" {
			return nil, time.Time{}, time.Time{}, fmt.Errorf("ERROR: Invalid CI URL format: %s\nExpected format: https://prow.ci.openshift.org/job-history/gs/<bucket>/logs/<job-name>", url)
		}

		if ciType := getCiType(url); ciType == CI_TYPE_UNKNOWN {
			return nil, time.Time{}, time.Time{}, fmt.Errorf("unknown CI type")
		}
	}

	// Date range validation and calculation
	// Capture a single consistent "now" to avoid clock drift across multiple time.Now() calls.
	now := time.Now().UTC()
	var err error

	if args.Latest {
		if args.AfterStr != "" || args.BeforeStr != "" || args.Today || args.Yesterday || args.LastNDays > 0 {
			return nil, time.Time{}, time.Time{}, fmt.Errorf("cannot combine --latest with other date options")
		}
		// Leave AfterDt zero and BeforeDt = now; processURL will stop after the first job.
		args.BeforeDt = now
	} else if args.Today {
		if args.AfterStr != "" || args.BeforeStr != "" || args.Yesterday || args.LastNDays > 0 {
			return nil, time.Time{}, time.Time{}, fmt.Errorf("cannot combine --today with other date options")
		}
		args.AfterDt = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		args.BeforeDt = time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, 999999999, time.UTC)
	} else if args.Yesterday {
		if args.AfterStr != "" || args.BeforeStr != "" || args.Today || args.LastNDays > 0 {
			return nil, time.Time{}, time.Time{}, fmt.Errorf("cannot combine --yesterday with other date options")
		}
		yesterday := now.AddDate(0, 0, -1)
		args.AfterDt = time.Date(yesterday.Year(), yesterday.Month(), yesterday.Day(), 0, 0, 0, 0, time.UTC)
		args.BeforeDt = time.Date(yesterday.Year(), yesterday.Month(), yesterday.Day(), 23, 59, 59, 999999999, time.UTC)
	} else if args.LastNDays > 0 {
		if args.Today || args.Yesterday {
			return nil, time.Time{}, time.Time{}, fmt.Errorf("cannot combine --last-n-days with other date options")
		}
		lastDate := now.AddDate(0, 0, -(args.LastNDays - 1))
		args.AfterDt = time.Date(lastDate.Year(), lastDate.Month(), lastDate.Day(), 0, 0, 0, 0, time.UTC)
		args.BeforeDt = time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, 999999999, time.UTC)
	} else {
		// Default: no start date, end date is now
		args.AfterDt = time.Time{}
		args.BeforeDt = now
	}

	// Override with explicit dates if provided
	if args.AfterStr != "" {
		args.AfterDt, err = fromISOFormat(args.AfterStr)
		if err != nil {
			return nil, time.Time{}, time.Time{}, fmt.Errorf("invalid after-date format: %w", err)
		}
	}

	if args.BeforeStr != "" {
		args.BeforeDt, err = fromISOFormat(args.BeforeStr)
		if err != nil {
			return nil, time.Time{}, time.Time{}, fmt.Errorf("invalid before-date format: %w", err)
		}
	}

	// Validate date range
	if !args.AfterDt.IsZero() && !args.BeforeDt.IsZero() && args.AfterDt.After(args.BeforeDt) {
		return nil, time.Time{}, time.Time{}, fmt.Errorf("after-date must be before before-date")
	}

	return args, args.AfterDt, args.BeforeDt, nil
}

// main is the entry point for the JobHistory program.
// It analyzes OpenShift CI job history from Prow for PowerVC deployments.
//
// The program performs the following steps:
//  1. Parses and validates command-line arguments
//  2. Sets up output file and CSV writer if requested
//  3. Initializes statistics tracking
//  4. Processes each provided Prow job history URL
//  5. Prints summary statistics of deployment and test results
//
// The program supports various filtering options:
//   - Date range filtering (--after-date, --before-date, --today, --yesterday, --last-n-days)
//   - Output format options (--csv, --output)
//   - Result filtering (--deploy-status-only, --test-status-only)
//   - Concurrency control (-j, --concurrency)
//
// Exit codes:
//   - 0: Success
//   - 1: Error (invalid arguments, file I/O error, or processing error)
func main() {
	// Parse and validate arguments
	args, afterDt, beforeDt, err := parseAndValidateArgs()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		flag.Usage()
		os.Exit(1)
	}

	// Setup output file
	if args.Output != "" {
		f, err := os.Create(args.Output)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: Cannot create output file: %v\n", err)
			os.Exit(1)
		}
		defer f.Close()
		outputFp = f
		if !args.CSV {
			infoFp = f
		}
	}

	// Initialize CSV writer
	var csvWriter *csv.Writer
	if args.CSV {
		csvWriter = csv.NewWriter(outputFp)
		defer func() {
			csvWriter.Flush()
			if err := csvWriter.Error(); err != nil {
				fmt.Fprintf(os.Stderr, "ERROR: Failed to flush CSV output: %v\n", err)
			}
		}()
		// Write CSV header (removed Zone column)
		if err := csvWriter.Write([]string{
			"Job URL",
			"Build summary",
			"Build details",
			"Test summary",
			"Test details",
		}); err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: Failed to write CSV header: %v\n", err)
			os.Exit(1)
		}
	}

	// Initialize statistics tracker
	ciStats := &CIStats{}

	// Create context with graceful signal handling (Ctrl+C / SIGINT / SIGTERM)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Process each URL
	for _, urlStr := range args.URLs {
		if ctx.Err() != nil {
			break
		}
		// Process this URL
		if err := processURL(ctx, args, ciStats, urlStr, afterDt, beforeDt, csvWriter); err != nil {
			if errors.Is(err, context.Canceled) {
				fmt.Fprintln(infoFp, "\nINFO: Operation canceled by user.")
				break
			}
			fmt.Fprintf(infoFp, "ERROR: Failed to process URL %s: %v\n", urlStr, err)
		}
	}

	// Print final summary statistics with percentages
	fmt.Fprintln(infoFp, "Finished")
	if ciStats.NumDeploys > 0 {
		if !args.TestStatusOnly {
			pct := float64(ciStats.DeploysSucceeded) / float64(ciStats.NumDeploys) * 100.0
			fmt.Fprintf(infoFp, "%d/%d deploys succeeded (%.1f%%)\n", ciStats.DeploysSucceeded, ciStats.NumDeploys, pct)
		}
		if !args.DeployStatusOnly {
			pct := float64(ciStats.GreenRuns) / float64(ciStats.NumDeploys) * 100.0
			fmt.Fprintf(infoFp, "%d/%d e2e green runs (%.1f%%)\n", ciStats.GreenRuns, ciStats.NumDeploys, pct)
		}
	} else {
		if !args.TestStatusOnly {
			fmt.Fprintf(infoFp, "%d/%d deploys succeeded\n", ciStats.DeploysSucceeded, ciStats.NumDeploys)
		}
		if !args.DeployStatusOnly {
			fmt.Fprintf(infoFp, "%d/%d e2e green runs\n", ciStats.GreenRuns, ciStats.NumDeploys)
		}
	}
}

// Made with Bob
