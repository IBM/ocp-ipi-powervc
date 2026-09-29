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

// UploadRhcosAPI downloads RHCOS (Red Hat CoreOS) images from the OpenShift
// installer repository and uploads them into a PowerVC/OpenStack environment
// using OpenStack/PowerVC APIs directly.
//
// # Overview
//
// For each requested release the program:
//  1. Downloads CoreOS JSON metadata from the openshift/installer GitHub repository.
//  2. Extracts the ppc64le OpenStack qcow2.gz image URL, filename, and SHA-256.
//  3. Checks whether the image already exists in OpenStack via the gophercloud
//     Glance API.
//  4. Uploads / imports the image into PowerVC/OpenStack via API.
//
// # Dependencies
//
// OpenStack connectivity and image lookups use the gophercloud API directly
// (clouds.yaml).
//
// # Usage
//
//	UploadRhcosAPI [flags]
//
//	Flags:
//	  --cloud <name>        OpenStack cloud from clouds.yaml  (env: CLOUD)
//	  --release <version>   Release to process; repeatable    (default: release-4.21)
//	  --rhel <rhel9|rhel10> RHEL version preference           (env: RHEL_VERSION)
//	  --insecure            Skip TLS certificate verification
//	  -v, --verbose         Enable debug output
//	  --dry-run             Simulate operations; no real calls
//	  --quiet               Suppress all non-error output
//	  -h, --help            Show usage and exit
//
// # Environment Variables
//
// Sensitive and site-specific parameters must be supplied via environment
// variables (or interactively when stdin is a terminal).
// Required: PROJECT_UPLOAD, RHEL_VERSION, SVC_HOST, SVC_USER, SVC_PASSWORD,
// POWERVC_URL, POWERVC_USER, POWERVC_PASSWORD, TEMPLATE.
// Optional: CLOUD, PROJECT, POWERVC_TENANT, IMPORT_SERVICE_URL,
// VOLUME_SIZE (default 120), OS_TYPE (default coreos).
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gophercloud/gophercloud/v2/openstack/config/clouds"
	"github.com/gophercloud/utils/v2/openstack/clientconfig"
)

// ─── ANSI colour helpers ──────────────────────────────────────────────────────

const (
	colorRed    = "\033[0;31m"
	colorGreen  = "\033[0;32m"
	colorYellow = "\033[1;33m"
	colorBlue   = "\033[0;34m"
	colorCyan   = "\033[0;36m"
	colorReset  = "\033[0m"
)

// Pre-built log prefixes — avoids repeated string concatenation on every call.
const (
	prefixInfo    = colorBlue + "[INFO]" + colorReset + " "
	prefixSuccess = colorGreen + "[SUCCESS]" + colorReset + " "
	prefixWarning = colorYellow + "[WARNING]" + colorReset + " "
	prefixError   = colorRed + "[ERROR]" + colorReset + " "
	prefixDebug   = colorCyan + "[DEBUG]" + colorReset + " "
)

// Download retry configuration.
const (
	downloadMaxRetries = 3
	downloadRetryDelay = 2 * time.Second
	downloadTimeout    = 5 * time.Minute
)

// stdinReader is a single buffered reader over os.Stdin shared across all
// interactive prompts.  Using one reader prevents the internal read-ahead
// buffer of one call consuming bytes that the next call expects.
var stdinReader = bufio.NewReader(os.Stdin)

// stdinIsTerminal reports whether os.Stdin is connected to an interactive
// terminal.  When false (e.g. under cron or a pipe) interactive prompts must
// not be attempted — the program should die with a clear error instead.
func stdinIsTerminal() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

// ─── Global configuration ─────────────────────────────────────────────────────

// config holds the runtime configuration derived from flags and environment
// variables.
type config struct {
	// Releases is the list of release branches to process (e.g. "release-4.21").
	Releases []string

	// Cloud is the OpenStack cloud name from clouds.yaml.
	Cloud string

	// Project is the optional prefix prepended to generated image filenames.
	// A trailing hyphen is stripped before use.
	Project string

	// ProjectUpload is the PowerVC project/tenant name used during image import.
	ProjectUpload string

	// RhelVersion restricts which CoreOS JSON variant is preferred.
	// Valid values: "rhel9", "rhel10", or "" (auto-detect).
	RhelVersion string

	// SvcHost is the PowerVC service host.
	SvcHost string

	// SvcUser is the PowerVC service username (env: SVC_USER).
	SvcUser string

	// SvcPassword is the PowerVC service password (env: SVC_PASSWORD).
	SvcPassword string

	// ImportServiceURL is the URL for the image import service (env: IMPORT_SERVICE_URL).
	// Default: https://<powervc_host>:8181/images/import
	ImportServiceURL string

	// PowerVCURL is the PowerVC Keystone URL (e.g. https://<host>:5000/v3).
	// Extracted automatically from clouds.yaml or env: POWERVC_URL.
	PowerVCURL string

	// PowerVCUser is the PowerVC user name.
	// Extracted automatically from clouds.yaml or env: POWERVC_USER.
	PowerVCUser string

	// PowerVCPassword is the PowerVC password.
	// Extracted automatically from clouds.yaml or env: POWERVC_PASSWORD.
	PowerVCPassword string

	// PowerVCTenant is the PowerVC tenant/project name.
	PowerVCTenant string

	// VolumeSize is the volume size in GB for the imported image.
	VolumeSize string

	// OsType is the operating system type for the imported image.
	OsType string

	// Template is the PowerVC template UUID.
	Template string

	// CACert is the path to a PEM-encoded CA certificate file used to verify
	// TLS connections to the import service.  When empty, the system CA pool
	// is used (unless Insecure is true).
	CACert string

	// Insecure controls whether SSL certificate verification is skipped.
	Insecure bool

	// Verbose enables [DEBUG] log output when true.
	Verbose bool

	// DryRun skips all real external operations when true.
	DryRun bool

	// Quiet suppresses all non-error output when true.
	Quiet bool

	// ConnectTimeout is the maximum time to wait when verifying OpenStack
	// connectivity at startup (env: CONNECT_TIMEOUT, default: 2m).
	ConnectTimeout time.Duration

	// ScriptDir is the directory that contains this binary.
	ScriptDir string
}

// imageImportRequest defines the JSON payload sent to the image import service.
type imageImportRequest struct {
	PowerVCURL      string `json:"powervc_url"`
	PowerVCUser     string `json:"powervc_user"`
	PowerVCPassword string `json:"powervc_password"`
	PowerVCTenant   string `json:"powervc_tenant"`
	SvcHost         string `json:"svc_host"`
	SvcUser         string `json:"svc_user"`
	SvcPassword     string `json:"svc_password"`
	ImageURI        string `json:"image_uri"`
	ImageName       string `json:"image_name"`
	OsType          string `json:"os_type"`
	VolumeSize      string `json:"volume_size"`
	TemplateID      string `json:"template_id"`
}

// importResponse is the JSON body returned by the image import service on a
// successful POST to the import endpoint.
//
// Example:
//
//	{"id":"b2f6107e…","poll_at":"https://…/jobs/b2f6107e…","queue_position":1,"status":"queued"}
type importResponse struct {
	JobID  string `json:"id"`
	PollAt string `json:"poll_at"`
	Status string `json:"status"`
}

// jobStatus is the JSON body returned when polling GET /jobs/<id>.
// This is a different shape from importResponse — it uses "id" (not "job_id"),
// carries progress fields, and surfaces the server error string on failure.
//
// Example:
//
//	{
//	  "id":         "b2f6107e…",
//	  "status":     "failed",
//	  "image_name": "rhcos-9.6.…-openstack.ppc64le",
//	  "uri":        "https://…qcow2.gz",
//	  "created_at": "2026-09-28T17:51:15…",
//	  "updated_at": "2026-09-28T17:51:15…",
//	  "bytes_read": 0,
//	  "percent":    -1,
//	  "error":      "import: failed to initialize PowerVC: …"
//	}
type jobStatus struct {
	ID        string  `json:"id"`
	Status    string  `json:"status"`
	ImageName string  `json:"image_name"`
	URI       string  `json:"uri"`
	CreatedAt string  `json:"created_at"`
	UpdatedAt string  `json:"updated_at"`
	BytesRead int64   `json:"bytes_read"`
	Percent   float64 `json:"percent"`
	Error     string  `json:"error"`
}

// imageInfo holds the ppc64le OpenStack image metadata extracted from a
// CoreOS JSON file by extractImageInfo.
type imageInfo struct {
	// DownloadURL is the full HTTPS URL of the qcow2.gz image.
	DownloadURL string

	// Filename is the derived image name used as the PowerVC image name.
	// It is the basename of DownloadURL with the ".qcow2.gz" suffix removed,
	// and optionally prefixed with config.Project.
	Filename string

	// SHA256 is the hex-encoded SHA-256 checksum of the qcow2.gz image.
	SHA256 string
}

// ─── Logging ─────────────────────────────────────────────────────────────────

// logInfo writes a blue [INFO] line to stdout.
// Suppressed when c.Quiet is true.
func (c *config) logInfo(format string, args ...any) {
	if !c.Quiet {
		fmt.Printf(prefixInfo+format+"\n", args...)
	}
}

// logSuccess writes a green [SUCCESS] line to stdout.
// Suppressed when c.Quiet is true.
func (c *config) logSuccess(format string, args ...any) {
	if !c.Quiet {
		fmt.Printf(prefixSuccess+format+"\n", args...)
	}
}

// logWarning writes a yellow [WARNING] line to stdout.
// Suppressed when c.Quiet is true.
func (c *config) logWarning(format string, args ...any) {
	if !c.Quiet {
		fmt.Printf(prefixWarning+format+"\n", args...)
	}
}

// logError writes a red [ERROR] line to stderr.
// Always visible — not suppressed by c.Quiet.
func (c *config) logError(format string, args ...any) {
	fmt.Fprintf(os.Stderr, prefixError+format+"\n", args...)
}

// logDebug writes a cyan [DEBUG] line to stdout.
// Only emitted when c.Verbose is true.
func (c *config) logDebug(format string, args ...any) {
	if c.Verbose {
		fmt.Printf(prefixDebug+format+"\n", args...)
	}
}

// die logs format to stderr via logError and exits the process with status 1.
// It is used for unrecoverable configuration or environment errors.
func (c *config) die(format string, args ...any) {
	c.logError(format, args...)
	os.Exit(1)
}

// ─── Interactive prompts ──────────────────────────────────────────────────────

// promptInput displays prompt on stdout, reads one line from stdinReader, and
// returns the trimmed value.
//
// If defaultVal is non-empty it is shown in brackets after prompt and used
// when the user presses Enter without typing anything.  varName is only used
// in error messages.
//
// If the final value is still empty and allowEmpty is false the program prints
// an error and exits with status 1.
func promptInput(prompt, varName, defaultVal string, allowEmpty bool) string {
	if defaultVal != "" {
		fmt.Printf("%s [%s]: ", prompt, defaultVal)
	} else {
		fmt.Printf("%s: ", prompt)
	}

	line, err := stdinReader.ReadString('\n')
	if err != nil && err != io.EOF {
		fmt.Fprintf(os.Stderr, prefixError+"Failed to read input for %s: %v\n", varName, err)
		os.Exit(1)
	}

	value := strings.TrimSpace(line)
	if value == "" {
		value = defaultVal
	}

	if value == "" && !allowEmpty {
		fmt.Fprintf(os.Stderr, prefixError+"You must enter a value for %s\n", varName)
		os.Exit(1)
	}

	return value
}

// promptRhelVersion loops until the user enters exactly "rhel9" or "rhel10",
// re-prompting and printing an error on each invalid entry.
func promptRhelVersion() string {
	for {
		v := promptInput("RHEL version (rhel9 or rhel10)", "RHEL_VERSION", "", false)
		if v == "rhel9" || v == "rhel10" {
			return v
		}
		fmt.Fprintf(os.Stderr, prefixError+"Invalid value %q — must be rhel9 or rhel10\n", v)
	}
}

// envOrPrompt returns the value of the environment variable envVar when it is
// set and non-empty.  If the variable is absent or empty and stdin is a
// terminal the user is prompted interactively; otherwise the program exits
// with a clear error (safe for cron / non-interactive use).
func envOrPrompt(envVar, prompt string) string {
	if v := os.Getenv(envVar); v != "" {
		return v
	}
	if !stdinIsTerminal() {
		fmt.Fprintf(os.Stderr, prefixError+"Required environment variable %s is not set\n", envVar)
		os.Exit(1)
	}
	return promptInput(prompt, envVar, "", false)
}

// ─── Argument parsing ─────────────────────────────────────────────────────────

// releaseFlag implements flag.Value to allow --release to be specified
// multiple times on the command line, accumulating each value into a slice.
type releaseFlag []string

// String returns a comma-separated representation of all accumulated values,
// satisfying the flag.Value interface.
func (r *releaseFlag) String() string { return strings.Join(*r, ", ") }

// Set appends v to the slice, satisfying the flag.Value interface.
func (r *releaseFlag) Set(v string) error {
	*r = append(*r, v)
	return nil
}

// parseArguments parses args (typically os.Args[1:]), validates flag values,
// and returns a populated *config.
//
// Only the flags that are safe to expose on the command line are accepted here.
// All sensitive or site-specific parameters (credentials, URLs, UUIDs, etc.)
// must be provided via environment variables; they are collected later by
// collectFromEnvironment.
//
// Notable behaviour:
//   - --rhel must be "rhel9" or "rhel10" if supplied; any other value causes
//     the program to exit with status 1.
//   - If no --release flag is given, Releases defaults to ["release-4.21"] and
//     a warning is logged.
//   - The FlagSet uses flag.ExitOnError, so unknown flags or --help cause an
//     immediate os.Exit; fs.Parse itself never returns a non-nil error.
func parseArguments(args []string) *config {
	c := &config{}

	fs := flag.NewFlagSet("UploadRhcosAPI", flag.ExitOnError)
	fs.Usage = func() { showUsage(fs) }

	var releases releaseFlag
	fs.Var(&releases, "release", "Specify a release version (may be used multiple times)")

	caCertFlag := fs.String("ca-cert", "", "Path to PEM CA certificate for import service TLS (env: CACERT)")
	cloudFlag := fs.String("cloud", "", "OpenStack cloud name from clouds.yaml (env: CLOUD)")
	dryRunFlag := fs.Bool("dry-run", false, "Simulate operations without making actual changes")
	insecureFlag := fs.Bool("insecure", false, "Skip SSL/TLS certificate verification for import service")
	quietFlag := fs.Bool("quiet", false, "Suppress all non-error output")
	rhelFlag := fs.String("rhel", "", "Prefer specific RHEL version: rhel9 or rhel10 (env: RHEL_VERSION)")
	connectTimeoutFlag := fs.Duration("connect-timeout", 0, "Timeout for OpenStack connectivity check (e.g. 30s, 2m) (env: CONNECT_TIMEOUT, default: 2m)")
	verboseFlag := fs.Bool("verbose", false, "Enable verbose output with debug information")
	fs.BoolVar(verboseFlag, "v", false, "Enable verbose output with debug information")

	// ExitOnError means fs.Parse calls os.Exit(2) on failure; it never returns
	// a non-nil error, but we assign it anyway to satisfy the compiler.
	if err := fs.Parse(args); err != nil {
		// unreachable with flag.ExitOnError, but keeps the compiler happy.
		os.Exit(2)
	}

	// Validate --rhel value when supplied.
	if *rhelFlag != "" && *rhelFlag != "rhel9" && *rhelFlag != "rhel10" {
		fmt.Fprintf(os.Stderr, prefixError+"Invalid RHEL version %q — must be rhel9 or rhel10\n", *rhelFlag)
		os.Exit(1)
	}

	// Populate config from flags.
	c.CACert = *caCertFlag
	c.ConnectTimeout = *connectTimeoutFlag
	c.Releases = []string(releases)
	c.Cloud = *cloudFlag
	c.DryRun = *dryRunFlag
	c.Insecure = *insecureFlag
	c.Quiet = *quietFlag
	c.RhelVersion = *rhelFlag
	c.Verbose = *verboseFlag

	// Default release when none specified.
	if len(c.Releases) == 0 {
		c.Releases = []string{"release-4.21"}
		c.logWarning("No release specified, using default: release-4.21")
	}

	return c
}

// collectFromEnvironment fills any config field that is still empty after flag
// parsing.  For each field it first checks the corresponding environment
// variable; if that is also absent the user is prompted interactively.
//
// Special cases:
//   - Project (PROJECT) is optional and is only read from the environment;
//     the user is never prompted for it.
//   - RhelVersion (RHEL_VERSION) is validated against "rhel9"/"rhel10"; an
//     invalid env value calls die; an absent value uses promptRhelVersion which
//     loops until a valid choice is entered.
func (c *config) collectFromEnvironment() {
	c.logInfo("Collecting environment variables...")

	if c.Cloud == "" {
		c.Cloud = envOrPrompt("CLOUD", "Cloud name in ~/.config/openstack/clouds.yaml")
	}

	// Attempt to extract defaults from clouds.yaml for the given cloud.
	if c.Cloud != "" {
		if authOptions, _, _, err := clouds.Parse(clouds.WithCloudName(c.Cloud)); err == nil {
			if c.PowerVCURL == "" && authOptions.IdentityEndpoint != "" {
				c.PowerVCURL = authOptions.IdentityEndpoint
				c.logDebug("Extracted PowerVC URL from clouds.yaml: %s", c.PowerVCURL)
			}
			if c.PowerVCUser == "" && authOptions.Username != "" {
				c.PowerVCUser = authOptions.Username
				c.logDebug("Extracted PowerVC user from clouds.yaml: %s", c.PowerVCUser)
			}
			if c.PowerVCPassword == "" && authOptions.Password != "" {
				c.PowerVCPassword = authOptions.Password
				c.logDebug("Extracted PowerVC password from clouds.yaml")
			}
			if c.PowerVCTenant == "" {
				if authOptions.TenantName != "" {
					c.PowerVCTenant = authOptions.TenantName
				} else if authOptions.Scope != nil && authOptions.Scope.ProjectName != "" {
					c.PowerVCTenant = authOptions.Scope.ProjectName
				}
				if c.PowerVCTenant != "" {
					c.logDebug("Extracted PowerVC tenant from clouds.yaml: %s", c.PowerVCTenant)
				}
			}
		}

		// Extract cacert from clouds.yaml when not already set by flag.
		if c.CACert == "" {
			if cloudEntry, err := clientconfig.GetCloudFromYAML(defaultClientOpts(c.Cloud)); err == nil && cloudEntry.CACertFile != "" {
				c.CACert = cloudEntry.CACertFile
				c.logDebug("Extracted CA cert path from clouds.yaml: %s", c.CACert)
			}
		}
	}

	// CACert: flag takes precedence, then clouds.yaml cacert field, then CACERT env.
	if c.CACert == "" {
		if v := os.Getenv("CACERT"); v != "" {
			c.CACert = v
			c.logDebug("Loaded CA cert path from CACERT env: %s", c.CACert)
		}
	}

	// PROJECT is optional — read from env only, never prompt.
	if c.Project == "" {
		c.Project = os.Getenv("PROJECT")
	}

	if c.ProjectUpload == "" {
		if v := os.Getenv("PROJECT_UPLOAD"); v != "" {
			c.ProjectUpload = v
		} else if c.PowerVCTenant != "" {
			c.ProjectUpload = c.PowerVCTenant
		} else if !stdinIsTerminal() {
			c.die("Required environment variable PROJECT_UPLOAD is not set")
		} else {
			c.ProjectUpload = promptInput("Project name when uploading", "PROJECT_UPLOAD", "", false)
		}
	}

	// Default PowerVCTenant to ProjectUpload if still unset.
	if c.PowerVCTenant == "" {
		if v := os.Getenv("POWERVC_TENANT"); v != "" {
			c.PowerVCTenant = v
		} else {
			c.PowerVCTenant = c.ProjectUpload
		}
	}

	// RhelVersion requires validation; use a dedicated prompt loop.
	if c.RhelVersion == "" {
		if v := os.Getenv("RHEL_VERSION"); v != "" {
			if v != "rhel9" && v != "rhel10" {
				c.die("RHEL_VERSION has invalid value %q — must be rhel9 or rhel10", v)
			}
			c.RhelVersion = v
		} else if !stdinIsTerminal() {
			c.die("Required environment variable RHEL_VERSION is not set")
		} else {
			c.RhelVersion = promptRhelVersion()
		}
	}

	if c.SvcHost == "" {
		c.SvcHost = envOrPrompt("SVC_HOST", "PowerVC service host")
	}

	if c.SvcUser == "" {
		c.SvcUser = envOrPrompt("SVC_USER", "PowerVC service user")
	}

	if c.SvcPassword == "" {
		c.SvcPassword = envOrPrompt("SVC_PASSWORD", "PowerVC service password")
	}

	if c.PowerVCURL == "" {
		c.PowerVCURL = envOrPrompt("POWERVC_URL", "PowerVC Keystone URL (e.g. https://<host>:5000/v3)")
	}

	if c.PowerVCUser == "" {
		c.PowerVCUser = envOrPrompt("POWERVC_USER", "PowerVC username")
	}

	if c.PowerVCPassword == "" {
		c.PowerVCPassword = envOrPrompt("POWERVC_PASSWORD", "PowerVC password")
	}

	if c.Template == "" {
		c.Template = envOrPrompt("TEMPLATE", "PowerVC template UUID")
	}

	if c.ImportServiceURL == "" {
		if v := os.Getenv("IMPORT_SERVICE_URL"); v != "" {
			c.ImportServiceURL = v
		} else if c.PowerVCURL != "" {
			// Infer from PowerVC URL: use the same scheme and host as Keystone
			// but on port 8181.  The import service uses HTTPS (self-signed cert;
			// use --insecure or supply --ca-cert / clouds.yaml cacert to verify).
			if u, err := url.Parse(c.PowerVCURL); err == nil && u.Hostname() != "" {
				c.ImportServiceURL = fmt.Sprintf("%s://%s:8181/images/import", u.Scheme, u.Hostname())
			}
		}
	}
	if c.ImportServiceURL == "" {
		c.ImportServiceURL = envOrPrompt("IMPORT_SERVICE_URL", "Image import service URL")
	}

	// CONNECT_TIMEOUT is optional — read from env only, never prompt.
	if c.ConnectTimeout == 0 {
		if v := os.Getenv("CONNECT_TIMEOUT"); v != "" {
			if d, err := time.ParseDuration(v); err == nil {
				c.ConnectTimeout = d
			} else {
				c.logWarning("CONNECT_TIMEOUT %q is not a valid duration (e.g. 30s, 2m) — using default 2m", v)
			}
		}
	}
	if c.ConnectTimeout == 0 {
		c.ConnectTimeout = 2 * time.Minute
	}

	// VOLUME_SIZE and OS_TYPE are optional — read from env only, never prompt.
	if c.VolumeSize == "" {
		if v := os.Getenv("VOLUME_SIZE"); v != "" {
			c.VolumeSize = v
		} else {
			c.VolumeSize = "120"
		}
	}

	if c.OsType == "" {
		if v := os.Getenv("OS_TYPE"); v != "" {
			c.OsType = v
		} else {
			c.OsType = "coreos"
		}
	}
}

// validateEnvironment is a final safety check that calls die if any required
// config field is still empty after collectFromEnvironment has run.
//
// Required fields: Cloud, ProjectUpload, RhelVersion, SvcHost, SvcUser,
// SvcPassword, PowerVCURL, PowerVCUser, PowerVCPassword, ImportServiceURL,
// Template, and at least one entry in Releases.
func (c *config) validateEnvironment() {
	c.logInfo("Validating environment variables...")

	required := []struct {
		name  string
		value string
	}{
		{"CLOUD", c.Cloud},
		{"PROJECT_UPLOAD", c.ProjectUpload},
		{"RHEL_VERSION", c.RhelVersion},
		{"SVC_HOST", c.SvcHost},
		{"SVC_USER", c.SvcUser},
		{"SVC_PASSWORD", c.SvcPassword},
		{"POWERVC_URL", c.PowerVCURL},
		{"POWERVC_USER", c.PowerVCUser},
		{"POWERVC_PASSWORD", c.PowerVCPassword},
		{"IMPORT_SERVICE_URL", c.ImportServiceURL},
		{"TEMPLATE", c.Template},
	}

	for _, r := range required {
		if r.value == "" {
			c.die("%s must be set and non-empty", r.name)
		}
	}

	if len(c.Releases) == 0 {
		c.die("at least one --release must be specified")
	}

	c.logSuccess("All environment variables validated")
}

// ─── OpenStack helpers ────────────────────────────────────────────────────────

// verifyOpenstackConnectivity confirms that the configured cloud is reachable by
// listing images through the gophercloud API (see getAllImages in
// OpenStack.go).  A successful list proves both authentication and image-service
// availability.
//
// The check runs in both normal and dry-run mode because dry-run still queries
// OpenStack to determine whether an image already exists.
// If the lookup fails, die is called — there is no point continuing without a
// working OpenStack connection.
//
// The timeout is controlled by c.ConnectTimeout (--connect-timeout / CONNECT_TIMEOUT,
// default 2m).
func (c *config) verifyOpenstackConnectivity() {
	c.logInfo("Verifying OpenStack connectivity (timeout: %s)...", c.ConnectTimeout)

	ctx, cancel := context.WithTimeout(context.Background(), c.ConnectTimeout)
	defer cancel()

	if _, err := c.getAllImages(ctx, c.Cloud); err != nil {
		c.die("Cannot connect to OpenStack: %v. Please verify clouds.yaml configuration.", err)
	}
	c.logSuccess("OpenStack connectivity verified")
}

// imageExistsInOpenStack reports whether an image named imageName already
// exists in OpenStack by querying the Glance image service directly via the
// gophercloud API (see findImage in OpenStack.go).
//
// The check is performed in both normal and dry-run mode so that a dry run can
// skip the upload when the image is already present.  A lookup that fails for
// any reason other than a clean "found" is treated as not-present so the upload
// path proceeds; the reason is logged.
func (c *config) imageExistsInOpenStack(imageName string) bool {
	c.logInfo("Checking whether image already exists: %s", imageName)

	// Bound the API lookup so a hung cloud cannot stall the program; 2 minutes
	// matches the timeout used by the main tool's rhcos-exists command.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if _, err := c.findImage(ctx, c.Cloud, imageName); err != nil {
		c.logInfo("Image not found in OpenStack: %s (%v)", imageName, err)
		return false
	}

	c.logSuccess("Image already exists in OpenStack: %s", imageName)
	return true
}

// ─── CoreOS JSON download ─────────────────────────────────────────────────────

// downloadCoreosJSON fetches the CoreOS JSON metadata for release from the
// openshift/installer GitHub repository into a temporary file and returns its
// path.  The caller must remove the file when finished (typically via
// defer os.Remove).
//
// URL candidates are drawn from the coreos/ directory of the release branch on
// raw.githubusercontent.com.  The preference order depends on c.RhelVersion:
//   - "rhel9"  → coreos-rhel-9.json, rhcos.json, coreos-rhel-10.json
//   - "rhel10" → coreos-rhel-10.json, rhcos.json, coreos-rhel-9.json
//   - ""       → rhcos.json, coreos-rhel-9.json, coreos-rhel-10.json
//
// For each candidate URL:
//   - A non-200 HTTP status causes an immediate skip to the next URL.
//   - A network error or body-copy failure is retried up to downloadMaxRetries
//     times with a downloadRetryDelay pause between attempts.
//
// Returns an error if every URL has been exhausted without a successful download.
func (c *config) downloadCoreosJSON(ctx context.Context, release string) (string, error) {
	baseURL := "https://raw.githubusercontent.com/openshift/installer/refs/heads/" + release + "/data/data/coreos/"

	// Build URL preference order based on RHEL version setting.
	var urls []string
	switch c.RhelVersion {
	case "rhel9":
		urls = []string{
			baseURL + "coreos-rhel-9.json",
			baseURL + "rhcos.json",
			baseURL + "coreos-rhel-10.json",
		}
		c.logDebug("Prioritizing RHEL 9 CoreOS JSON")
	case "rhel10":
		urls = []string{
			baseURL + "coreos-rhel-10.json",
			baseURL + "rhcos.json",
			baseURL + "coreos-rhel-9.json",
		}
		c.logDebug("Prioritizing RHEL 10 CoreOS JSON")
	default:
		urls = []string{
			baseURL + "rhcos.json",
			baseURL + "coreos-rhel-9.json",
			baseURL + "coreos-rhel-10.json",
		}
		c.logDebug("Trying all CoreOS JSON variants in default order")
	}

	// Single client reused for all attempts.  No client-level timeout — the
	// context passed to each request already enforces the overall deadline, and
	// a redundant client timeout would race against it and produce a less
	// informative error message.
	client := &http.Client{}

	for _, rawURL := range urls {
		c.logDebug("Trying URL: %s", rawURL)

		for attempt := 1; attempt <= downloadMaxRetries; attempt++ {
			c.logDebug("Download attempt %d/%d: %s", attempt, downloadMaxRetries, rawURL)

			req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
			if err != nil {
				return "", fmt.Errorf("failed to build download request for %s: %w", rawURL, err)
			}
			resp, err := client.Do(req)
			if err != nil {
				if ctx.Err() != nil {
					return "", ctx.Err()
				}
				c.logDebug("Attempt %d/%d failed for %s: %v", attempt, downloadMaxRetries, rawURL, err)
				if attempt < downloadMaxRetries {
					c.logDebug("Retrying in %s...", downloadRetryDelay)
					t := time.NewTimer(downloadRetryDelay)
					select {
					case <-ctx.Done():
						t.Stop()
						return "", ctx.Err()
					case <-t.C:
					}
				}
				continue
			}

			// Non-200 means this file doesn't exist at this URL; skip to next.
			if resp.StatusCode != http.StatusOK {
				resp.Body.Close()
				c.logDebug("URL returned HTTP %d, skipping: %s", resp.StatusCode, rawURL)
				break
			}

			// Only allocate the temp file once we have a live 200 response.
			tmpFile, err := os.CreateTemp("", "coreos-*.json")
			if err != nil {
				resp.Body.Close()
				return "", fmt.Errorf("failed to create temp file: %w", err)
			}
			tmpPath := tmpFile.Name()

			_, copyErr := io.Copy(tmpFile, resp.Body)
			resp.Body.Close()
			tmpFile.Close()
			if copyErr != nil {
				os.Remove(tmpPath)
				c.logWarning("Download attempt %d/%d failed for %s: %v", attempt, downloadMaxRetries, rawURL, copyErr)
				if attempt < downloadMaxRetries {
					c.logDebug("Retrying in %s...", downloadRetryDelay)
					t := time.NewTimer(downloadRetryDelay)
					select {
					case <-ctx.Done():
						t.Stop()
						return "", ctx.Err()
					case <-t.C:
					}
				}
				continue
			}

			c.logInfo("Downloaded %s", rawURL)
			return tmpPath, nil
		}
	}

	return "", fmt.Errorf("could not download CoreOS JSON from any known location for release %s", release)
}

// errJobTimedOut returns a consistent timeout error for job jobID so that all
// exit points in pollJob produce identical wording.
func errJobTimedOut(jobID string) error {
	return fmt.Errorf("timed out after %s waiting for job %s to complete", jobPollTimeout, jobID)
}

// ─── JSON parsing ─────────────────────────────────────────────────────────────

// extractImageInfo opens the CoreOS JSON file at jsonPath, navigates to
// .architectures.ppc64le.artifacts.openstack.formats["qcow2.gz"].disk, and
// returns the image metadata as an *imageInfo.
//
// Filename derivation:
//  1. Take the basename of the download URL.
//  2. Strip the ".qcow2.gz" suffix.
//  3. If c.Project is non-empty, strip any trailing "-" from it and prepend
//     "<project>-" to the filename.
//
// Returns an error if the file cannot be opened, the JSON is malformed, the
// qcow2.gz format is absent, or the location field is empty or "null".
func (c *config) extractImageInfo(jsonPath string) (*imageInfo, error) {
	f, err := os.Open(jsonPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open JSON file %s: %w", jsonPath, err)
	}
	defer f.Close()

	// Navigate: .architectures.ppc64le.artifacts.openstack.formats["qcow2.gz"].disk
	var root struct {
		Architectures struct {
			Ppc64le struct {
				Artifacts struct {
					Openstack struct {
						Formats map[string]struct {
							Disk struct {
								Location string `json:"location"`
								SHA256   string `json:"sha256"`
							} `json:"disk"`
						} `json:"formats"`
					} `json:"openstack"`
				} `json:"artifacts"`
			} `json:"ppc64le"`
		} `json:"architectures"`
	}

	if err := json.NewDecoder(f).Decode(&root); err != nil {
		return nil, fmt.Errorf("failed to parse CoreOS JSON: %w", err)
	}

	qcow2gz, ok := root.Architectures.Ppc64le.Artifacts.Openstack.Formats["qcow2.gz"]
	if !ok {
		return nil, fmt.Errorf("qcow2.gz format not found in CoreOS JSON")
	}

	downloadURL := qcow2gz.Disk.Location
	if downloadURL == "" || downloadURL == "null" {
		return nil, fmt.Errorf("failed to extract download URL from CoreOS JSON")
	}
	sha256 := qcow2gz.Disk.SHA256

	// Derive filename: basename without the .qcow2.gz extension.
	base := filepath.Base(downloadURL)
	filename := strings.TrimSuffix(base, ".qcow2.gz")

	// Prepend optional project prefix (strip trailing "-" first).
	if c.Project != "" {
		project := strings.TrimSuffix(c.Project, "-")
		c.logInfo("Prepending project (%s) to RHCOS filename", project)
		filename = project + "-" + filename
	}

	return &imageInfo{
		DownloadURL: downloadURL,
		Filename:    filename,
		SHA256:      sha256,
	}, nil
}

// ─── Per-release processing ───────────────────────────────────────────────────

// jobPollInterval is the time between successive job-status polls.
const jobPollInterval = 15 * time.Second

// jobPollTimeout is the maximum total time to wait for a job to reach a
// terminal state before giving up.
const jobPollTimeout = 60 * time.Minute

// pollJob polls the job status endpoint derived from pollAt until the job
// reaches a terminal state (completed, failed, or error) or jobPollTimeout
// elapses.
//
// pollAt may be either a full URL (e.g. "https://host:8181/jobs/<id>") or a
// bare path (e.g. "/jobs/<id>").  When it is already absolute it is used
// directly; when it is a path it is resolved against ImportServiceURL.
//
// On each poll cycle the current status, progress percentage, and bytes read
// are logged.  Returns nil only when status is "completed"; any other terminal
// state, timeout, or unrecoverable error returns a non-nil error.
func (c *config) pollJob(pollClient *http.Client, jobID, pollAt string) error {
	// Use pollAt directly if it is already an absolute URL, otherwise resolve
	// it as a path against the import service base URL.
	var pollURL string
	if u, err := url.Parse(pollAt); err == nil && u.IsAbs() {
		pollURL = pollAt
	} else {
		base, err := url.Parse(c.ImportServiceURL)
		if err != nil {
			return fmt.Errorf("failed to parse ImportServiceURL %q: %w", c.ImportServiceURL, err)
		}
		base.Path = pollAt
		base.RawQuery = ""
		pollURL = base.String()
	}

	c.logInfo("─── Job polling ────────────────────────────────")
	c.logInfo("Job ID:   %s", jobID)
	c.logInfo("Poll URL: %s", pollURL)
	c.logInfo("Interval: %s   Timeout: %s", jobPollInterval, jobPollTimeout)
	c.logInfo("────────────────────────────────────────────────")

	ctx, cancel := context.WithTimeout(context.Background(), jobPollTimeout)
	defer cancel()

	// sleepOrCancel waits for jobPollInterval or ctx cancellation, whichever
	// comes first.  Uses time.NewTimer so the timer is stopped and GC'd
	// immediately on context cancellation rather than leaking until it fires.
	sleepOrCancel := func() error {
		t := time.NewTimer(jobPollInterval)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return errJobTimedOut(jobID)
		case <-t.C:
			return nil
		}
	}

	for {
		// Check overall deadline before making a request.
		if ctx.Err() != nil {
			return errJobTimedOut(jobID)
		}

		// Each GET is bounded by the pollClient's per-request timeout, and also
		// inherits the overall deadline from ctx.
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, pollURL, nil)
		if err != nil {
			return fmt.Errorf("failed to build poll request for job %s: %w", jobID, err)
		}

		resp, err := pollClient.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return errJobTimedOut(jobID)
			}
			c.logWarning("Poll request failed for job %s: %v — retrying in %s", jobID, err, jobPollInterval)
			if err := sleepOrCancel(); err != nil {
				return err
			}
			continue
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			c.logWarning("Failed to read poll response for job %s: %v — retrying in %s", jobID, err, jobPollInterval)
			if err := sleepOrCancel(); err != nil {
				return err
			}
			continue
		}

		c.logDebug("Poll response for job %s (HTTP %d):\n%s", jobID, resp.StatusCode, string(body))

		var js jobStatus
		if err := json.Unmarshal(body, &js); err != nil {
			c.logWarning("Failed to parse poll response for job %s: %v — retrying in %s", jobID, err, jobPollInterval)
			if err := sleepOrCancel(); err != nil {
				return err
			}
			continue
		}

		// Progress line: show percent when the server supplies a non-negative value.
		if js.Percent >= 0 {
			c.logInfo("  status=%-12s  progress=%5.1f%%  bytes_read=%d  image=%s",
				js.Status, js.Percent, js.BytesRead, js.ImageName)
		} else {
			c.logInfo("  status=%-12s  image=%s", js.Status, js.ImageName)
		}

		switch js.Status {
		case "completed":
			c.logSuccess("Job %s completed successfully — image: %s", jobID, js.ImageName)
			return nil
		case "failed", "error":
			return fmt.Errorf("job %s failed — image: %s\nerror: %s", jobID, js.ImageName, js.Error)
		case "pending", "running":
			// Expected in-progress states — fall through to sleep.
		default:
			c.logWarning("Job %s: unrecognised status %q — continuing to poll", jobID, js.Status)
		}

		if err := sleepOrCancel(); err != nil {
			return err
		}
	}
}

// uploadImageAPI uploads or imports an RHCOS image via PowerVC/OpenStack APIs
// by sending an HTTP POST request to the image import endpoint.
func (c *config) uploadImageAPI(info *imageInfo) error {
	reqPayload := imageImportRequest{
		PowerVCURL:      c.PowerVCURL,
		PowerVCUser:     c.PowerVCUser,
		PowerVCPassword: c.PowerVCPassword,
		PowerVCTenant:   c.PowerVCTenant,
		SvcHost:         c.SvcHost,
		SvcUser:         c.SvcUser,
		SvcPassword:     c.SvcPassword,
		ImageURI:        info.DownloadURL,
		ImageName:       info.Filename,
		OsType:          c.OsType,
		VolumeSize:      c.VolumeSize,
		TemplateID:      c.Template,
	}

	payloadBytes, err := json.MarshalIndent(reqPayload, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal image import request: %w", err)
	}

	c.logInfo("Sending image import request to %s for image %s", c.ImportServiceURL, info.Filename)

	// Log payload with credentials redacted so --verbose is safe to use.
	if c.Verbose {
		redacted := reqPayload
		redacted.PowerVCPassword = "***"
		redacted.SvcPassword = "***"
		redactedBytes, _ := json.MarshalIndent(redacted, "", "  ")
		c.logDebug("Image import payload (credentials redacted):\n%s", string(redactedBytes))
	}

	if c.DryRun {
		c.logInfo("DRY RUN — skipping API image upload POST request")
		return nil
	}

	tlsCfg := &tls.Config{
		InsecureSkipVerify: c.Insecure, //nolint:gosec
	}
	if c.CACert != "" && !c.Insecure {
		pem, err := os.ReadFile(c.CACert)
		if err != nil {
			return fmt.Errorf("failed to read CA cert %s: %w", c.CACert, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return fmt.Errorf("no valid PEM certificates found in %s", c.CACert)
		}
		tlsCfg.RootCAs = pool
		c.logDebug("Using CA cert for import service TLS: %s", c.CACert)
	}
	transport := &http.Transport{
		TLSClientConfig: tlsCfg,
	}
	// postClient: no client-level timeout — the per-request context below
	// enforces the POST deadline so cancellation is clean and informative.
	postClient := &http.Client{
		Transport: transport,
	}
	// pollClient: no client-level timeout — each GET /jobs/<id> is already
	// bounded by the per-request context inside pollJob, so a redundant
	// client Timeout would race against it and produce a less informative error.
	pollClient := &http.Client{
		Transport: transport,
	}

	postCtx, postCancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer postCancel()
	req, err := http.NewRequestWithContext(postCtx, http.MethodPost, c.ImportServiceURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return fmt.Errorf("failed to create HTTP request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := postClient.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP request to %s failed: %w", c.ImportServiceURL, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body from %s: %w", c.ImportServiceURL, err)
	}

	c.logDebug("Image import response (HTTP %d):\n%s", resp.StatusCode, string(body))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("image import failed with HTTP status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	c.logSuccess("Image import request accepted (HTTP %d)", resp.StatusCode)

	// Parse the POST response to extract job_id and poll_at.
	// NOTE: this shape is different from the poll response — see importResponse
	// vs jobStatus for the field differences.
	var impResp importResponse
	if err := json.Unmarshal(body, &impResp); err != nil || impResp.JobID == "" {
		// Not the expected shape — log raw body and treat the accepted status as
		// sufficient (we have no job to track).
		c.logInfo("Response: %s", strings.TrimSpace(string(body)))
		return nil
	}

	c.logInfo("Job ID: %s  status: %s", impResp.JobID, impResp.Status)

	if impResp.PollAt == "" {
		c.logWarning("No poll_at path in response — cannot track job progress")
		return nil
	}

	return c.pollJob(pollClient, impResp.JobID, impResp.PollAt)
}

// processRelease executes the workflow for a single release branch:
//
//  1. Download the CoreOS JSON metadata from GitHub into a temp file.
//  2. Parse the JSON to extract the ppc64le qcow2.gz URL, filename, and SHA256.
//  3. Check whether the image already exists in OpenStack; return early if so.
//  4. Upload/import the image using direct API calls via uploadImageAPI.
//
// The temporary JSON file is removed via defer when the function returns.
// Returns a non-nil error if any step fails; partial failures are not retried.
func (c *config) processRelease(release string) error {
	c.logInfo("Processing release: %s", release)

	// Step 1: Download the CoreOS JSON metadata.
	ctx, cancel := context.WithTimeout(context.Background(), downloadTimeout)
	defer cancel()
	jsonPath, err := c.downloadCoreosJSON(ctx, release)
	if err != nil {
		return err
	}
	defer os.Remove(jsonPath)

	// Step 2: Extract image metadata.
	info, err := c.extractImageInfo(jsonPath)
	if err != nil {
		return err
	}

	c.logInfo("Download URL: %s", info.DownloadURL)
	c.logInfo("Filename:     %s", info.Filename)
	c.logDebug("SHA256:       %s", info.SHA256)

	// Step 3: Skip upload if the image already exists.
	if c.imageExistsInOpenStack(info.Filename) {
		c.logSuccess("Release %s is already present — nothing to do", release)
		return nil
	}

	// Step 4: Upload image via API.
	if err := c.uploadImageAPI(info); err != nil {
		return err
	}

	c.logSuccess("Release %s import job completed successfully", release)
	return nil
}

// ─── Usage ────────────────────────────────────────────────────────────────────

// showUsage prints comprehensive usage information to stdout, including all
// OPTIONS, ENVIRONMENT VARIABLES, EXAMPLES, and WORKFLOW sections,
// followed by the standard flag defaults from fs.
func showUsage(fs *flag.FlagSet) {
	programName := filepath.Base(os.Args[0])
	fmt.Printf(`Usage: %s [OPTIONS]

Download RHCOS images and upload them to PowerVC/OpenStack using APIs for OpenShift deployments.

This program automates the process of:
  1. Downloading CoreOS metadata from GitHub
  2. Extracting image information (URL, filename, SHA256)
  3. Checking if the image already exists in OpenStack
  4. Uploading/importing images via PowerVC/OpenStack API

OPTIONS:
  --ca-cert <path>      Path to PEM CA certificate for import service TLS (env: CACERT)
                        Auto-detected from clouds.yaml cacert field when --cloud is set
  --cloud <name>        OpenStack cloud name from clouds.yaml (env: CLOUD)
  --connect-timeout <d> Timeout for OpenStack connectivity check (env: CONNECT_TIMEOUT, default: 2m)
                        Accepts Go duration strings, e.g. 30s, 90s, 2m
  --release <version>   Specify a release version (can be used multiple times)
                        Example: --release release-4.21 --release release-4.22
                        Default: release-4.21 if not specified
  --rhel <version>      Prefer specific RHEL version: rhel9 or rhel10 (env: RHEL_VERSION)
  --insecure            Skip TLS certificate verification (default: false)
  -v, --verbose         Enable verbose output with debug information
  --dry-run             Simulate operations without making actual changes
  --quiet               Suppress all non-error output
  -h, --help            Show this help message and exit

ENVIRONMENT VARIABLES (required):
  PROJECT_UPLOAD     PowerVC project name for image upload
  RHEL_VERSION       RHEL version preference (rhel9 or rhel10)
  SVC_HOST           PowerVC service host
  SVC_USER           PowerVC service username
  SVC_PASSWORD       PowerVC service password
  POWERVC_URL        PowerVC Keystone URL (e.g. https://<host>:5000/v3)
  POWERVC_USER       PowerVC username
  POWERVC_PASSWORD   PowerVC password
  TEMPLATE           PowerVC template UUID

ENVIRONMENT VARIABLES (optional):
  CACERT             Path to PEM CA certificate for import service TLS
  CLOUD              OpenStack cloud name from clouds.yaml
  CONNECT_TIMEOUT    Timeout for OpenStack connectivity check (e.g. 30s, 2m; default: 2m)
  IMPORT_SERVICE_URL Image import service URL (default: https://<powervc-host>:8181/images/import)
  OS_TYPE            Operating system type (default: coreos)
  POWERVC_TENANT     PowerVC tenant/project name (default: same as PROJECT_UPLOAD)
  PROJECT            Optional project prefix prepended to image filenames
  VOLUME_SIZE        Volume size in GB (default: 120)

EXAMPLES:
  # Run using environment variables for all sensitive parameters
  export RHEL_VERSION=rhel9
  export SVC_HOST=10.130.32.11
  export SVC_USER=myuser
  export SVC_PASSWORD=secret
  export POWERVC_URL=https://10.130.32.11:5000/v3
  export POWERVC_USER=admin
  export POWERVC_PASSWORD=secret
  export PROJECT_UPLOAD=myproject
  export TEMPLATE=<uuid>
  %s --cloud mycloud --release release-4.21

  # Multiple releases
  %s --release release-4.21 --release release-4.22

  # Dry run to test without actual operations
  %s --release release-4.21 --dry-run

  # Verbose output for debugging
  %s --release release-4.21 --verbose

WORKFLOW:
  1. Parse command-line arguments and collect missing variables interactively
  2. Validate all required environment variables are set
  3. Verify OpenStack connectivity
  4. For each release:
     a. Download CoreOS JSON metadata from GitHub
     b. Extract image URL, filename, and SHA256 checksum
     c. Check if image already exists in OpenStack
     d. If not present:
        - Upload image to PowerVC/OpenStack via API

`, programName, programName, programName, programName, programName)
	fs.PrintDefaults()
}

// ─── Entry point ─────────────────────────────────────────────────────────────

// main is the program entry point.  It:
//  1. Resolves c.ScriptDir from the executable path.
//  2. Parses command-line arguments via parseArguments.
//  3. Collects any missing configuration from environment variables or prompts.
//  4. Validates that all required fields are set.
//  5. Verifies OpenStack connectivity.
//  6. Processes each release in order, logging errors but continuing on failure.
//  7. Exits with status 0 if all releases succeeded, or status 1 if any failed.
func main() {
	exePath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, prefixError+"Failed to resolve executable path: %v\n", err)
		os.Exit(1)
	}
	scriptDir := filepath.Dir(exePath)

	c := parseArguments(os.Args[1:])
	c.ScriptDir = scriptDir

	c.logInfo("Starting OpenShift RHCOS image upload program (API)")
	c.logInfo("Working directory: %s", scriptDir)

	if c.DryRun {
		c.logWarning("Running in DRY RUN mode - no actual operations will be performed")
	}

	c.logDebug("Parsed arguments: releases=%v cloud=%s verbose=%v dry-run=%v insecure=%v rhel=%s",
		c.Releases, c.Cloud, c.Verbose, c.DryRun, c.Insecure, c.RhelVersion)

	c.collectFromEnvironment()
	c.validateEnvironment()
	c.verifyOpenstackConnectivity()

	total := len(c.Releases)
	c.logInfo("Processing %d release(s): %s", total, strings.Join(c.Releases, ", "))

	var failed int
	for _, release := range c.Releases {
		if err := c.processRelease(release); err != nil {
			c.logError("Failed to process release %s: %v", release, err)
			failed++
		}
	}

	if failed == 0 {
		c.logSuccess("All %d release(s) processed successfully", total)
	} else {
		c.logError("%d of %d release(s) failed", failed, total)
		os.Exit(1)
	}
}
