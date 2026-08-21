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

// upload-rhcos downloads RHCOS (Red Hat CoreOS) images from the OpenShift
// installer repository and uploads them into a PowerVC/OpenStack environment.
//
// # Overview
//
// For each requested release the program:
//  1. Downloads CoreOS JSON metadata from the openshift/installer GitHub repository.
//  2. Extracts the ppc64le OpenStack qcow2.gz image URL, filename, and SHA-256.
//  3. Checks whether the image already exists in OpenStack via the openstack CLI.
//  4. If the image is absent, optionally converts the qcow2 image to OVA format
//     with pvsadm, then imports it into PowerVC with pvcctl or powervc-image.
//
// # Dependencies
//
//   - openstack   – Image existence verification
//   - pvsadm      – qcow2 → OVA conversion (optional; detected at runtime)
//   - pvcctl OR powervc-image – PowerVC image import (one is required)
//
// # Usage
//
//	upload-rhcos [flags]
//
//	Flags:
//	  --cloud <name>           OpenStack cloud from clouds.yaml  (env: CLOUD)
//	  --project <name>         Optional prefix for image filenames (env: PROJECT)
//	  --project-upload <name>  PowerVC project for image upload   (env: PROJECT_UPLOAD)
//	  --release <version>      Release to process; repeatable     (default: release-4.21)
//	  --rhel <rhel9|rhel10>    RHEL version preference            (env: RHEL_VERSION)
//	  --svc-host <host>        PowerVC service host               (env: SVC_HOST)
//	  --template <uuid>        PowerVC template UUID              (env: TEMPLATE)
//	  -v, --verbose            Enable debug output
//	  --dry-run                Simulate operations; no real calls
//	  --quiet                  Suppress all non-error output
//	  -h, --help               Show usage and exit
//
// # Environment Variables
//
// All flags can also be supplied via the correspondingly named environment
// variable (CLOUD, PROJECT, PROJECT_UPLOAD, RHEL_VERSION, SVC_HOST, TEMPLATE).
// If a required variable is missing and stdin is a terminal the program
// prompts interactively.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
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

	// ProjectUpload is the PowerVC project name used during image import.
	ProjectUpload string

	// RhelVersion restricts which CoreOS JSON variant is preferred.
	// Valid values: "rhel9", "rhel10", or "" (auto-detect).
	RhelVersion string

	// SvcHost is the PowerVC service host required by pvcctl.
	SvcHost string

	// Template is the PowerVC template UUID required by pvcctl and powervc-image.
	Template string

	// Verbose enables [DEBUG] log output when true.
	Verbose bool

	// DryRun skips all real external operations when true.
	DryRun bool

	// Quiet suppresses all non-error output when true.
	Quiet bool

	// ScriptDir is the directory that contains this binary.  OVA files are
	// written to and read from this directory.
	ScriptDir string

	// usePvsadm is set during dependency detection.
	usePvsadm bool

	// usePvcctl is set during dependency detection.
	// true  → use pvcctl for import
	// false → use powervc-image for import
	usePvcctl bool
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

// ─── Dependency detection ─────────────────────────────────────────────────────

// commandExists reports whether the named program is available in PATH.
func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// checkRequiredPrograms checks for all external tools needed at runtime and
// configures c.usePvsadm and c.usePvcctl accordingly.
//
// Required (fatal if absent):
//   - openstack — used by verifyOpenstackConnectivity and imageExistsInOpenStack.
//   - pvcctl OR powervc-image — at least one must be present for image import.
//
// Optional:
//   - pvsadm — if found, c.usePvsadm is set to true and qcow2→OVA conversion
//     is performed before import; if absent a warning is logged and conversion
//     is skipped.
//
// Side-effects: sets c.usePvsadm and c.usePvcctl.
// Exits via die if openstack or both import tools are missing.
func (c *config) checkRequiredPrograms() {
	c.logInfo("Checking required programs...")

	// openstack is the only mandatory external binary; JSON fetching and
	// parsing are handled natively by net/http and encoding/json.
	if !commandExists("openstack") {
		c.die("Missing required program: openstack")
	}

	if commandExists("pvsadm") {
		c.usePvsadm = true
	} else {
		c.logWarning("pvsadm not found — qcow2 to OVA conversion will be skipped")
	}

	switch {
	case commandExists("pvcctl"):
		c.logInfo("Found pvcctl over powervc-image")
		c.usePvcctl = true
	case commandExists("powervc-image"):
		c.logInfo("Did not find pvcctl, but found powervc-image instead")
	default:
		c.die("Missing required programs: either pvcctl or powervc-image must exist!")
	}

	c.logSuccess("All required programs are available")
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
// set and non-empty.  If the variable is absent or empty the user is prompted
// interactively using promptInput with the given prompt string.
func envOrPrompt(envVar, prompt string) string {
	if v := os.Getenv(envVar); v != "" {
		return v
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
// Notable behaviour:
//   - --rhel must be "rhel9" or "rhel10" if supplied; any other value causes
//     the program to exit with status 1.
//   - If no --release flag is given, Releases defaults to ["release-4.21"] and
//     a warning is logged.
//   - The FlagSet uses flag.ExitOnError, so unknown flags or --help cause an
//     immediate os.Exit; fs.Parse itself never returns a non-nil error.
func parseArguments(args []string) *config {
	c := &config{}

	fs := flag.NewFlagSet("upload-rhcos", flag.ExitOnError)
	fs.Usage = func() { showUsage(fs) }

	var releases releaseFlag
	fs.Var(&releases, "release", "Specify a release version (may be used multiple times)")

	cloudFlag := fs.String("cloud", "", "OpenStack cloud name from clouds.yaml")
	dryRunFlag := fs.Bool("dry-run", false, "Simulate operations without making actual changes")
	projectFlag := fs.String("project", "", "Optional project prefix prepended to image filenames")
	projectUploadFlag := fs.String("project-upload", "", "PowerVC project name for image upload")
	quietFlag := fs.Bool("quiet", false, "Suppress all non-error output")
	rhelFlag := fs.String("rhel", "", "Prefer specific RHEL version: rhel9 or rhel10")
	svcHostFlag := fs.String("svc-host", "", "PowerVC service host")
	templateFlag := fs.String("template", "", "PowerVC template UUID")
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
	c.Releases = []string(releases)
	c.Cloud = *cloudFlag
	c.DryRun = *dryRunFlag
	c.Project = *projectFlag
	c.ProjectUpload = *projectUploadFlag
	c.Quiet = *quietFlag
	c.RhelVersion = *rhelFlag
	c.SvcHost = *svcHostFlag
	c.Template = *templateFlag
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

	// PROJECT is optional — read from env only, never prompt.
	if c.Project == "" {
		c.Project = os.Getenv("PROJECT")
	}

	if c.ProjectUpload == "" {
		c.ProjectUpload = envOrPrompt("PROJECT_UPLOAD", "Project name when uploading")
	}

	// RhelVersion requires validation; use a dedicated prompt loop.
	if c.RhelVersion == "" {
		if v := os.Getenv("RHEL_VERSION"); v != "" {
			if v != "rhel9" && v != "rhel10" {
				c.die("RHEL_VERSION has invalid value %q — must be rhel9 or rhel10", v)
			}
			c.RhelVersion = v
		} else {
			c.RhelVersion = promptRhelVersion()
		}
	}

	if c.SvcHost == "" {
		c.SvcHost = envOrPrompt("SVC_HOST", "PowerVC service host")
	}

	if c.Template == "" {
		c.Template = envOrPrompt("TEMPLATE", "PowerVC template UUID")
	}
}

// validateEnvironment is a final safety check that calls die if any required
// config field is still empty after collectFromEnvironment has run.
//
// Required fields: Cloud, ProjectUpload, RhelVersion, SvcHost, Template, and
// at least one entry in Releases.
//
// In normal operation collectFromEnvironment guarantees these are set, so this
// function acts as a defensive assertion rather than primary validation.
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

// verifyOpenstackConnectivity confirms that the openstack CLI can reach the
// configured cloud by running "openstack image list".  Both stdout and stderr
// of the subprocess are discarded; only the exit code is checked.
//
// In dry-run mode the check is skipped entirely and an info message is logged.
// If the command fails, die is called — there is no point continuing without
// a working OpenStack connection.
func (c *config) verifyOpenstackConnectivity() {
	if c.DryRun {
		c.logInfo("Skipping OpenStack connectivity check in DRY RUN mode")
		return
	}

	c.logInfo("Verifying OpenStack connectivity...")
	cmd := exec.Command("openstack", "--os-cloud="+c.Cloud, "image", "list")
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		c.die("Cannot connect to OpenStack. Please verify clouds.yaml configuration.")
	}
	c.logSuccess("OpenStack connectivity verified")
}

// imageExistsInOpenStack reports whether an image named imageName already
// exists in OpenStack by running "openstack image show <name>".  Both stdout
// and stderr of the subprocess are discarded.
//
// Returns false in dry-run mode (so that the upload path and its commands are
// always exercised and printed during a dry run).
func (c *config) imageExistsInOpenStack(imageName string) bool {
	c.logInfo("Checking whether image already exists: %s", imageName)

	if c.DryRun {
		c.logInfo("DRY RUN — skipping image existence check")
		return false
	}

	cmd := exec.Command("openstack", "--os-cloud="+c.Cloud, "image", "show", imageName)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		c.logInfo("Image not found in OpenStack: %s", imageName)
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
func (c *config) downloadCoreosJSON(release string) (string, error) {
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

	// Single client reused for all attempts.
	client := &http.Client{Timeout: downloadTimeout}

	for _, url := range urls {
		c.logDebug("Trying URL: %s", url)

		for attempt := 1; attempt <= downloadMaxRetries; attempt++ {
			c.logDebug("Download attempt %d/%d: %s", attempt, downloadMaxRetries, url)

			resp, err := client.Get(url) //nolint:noctx
			if err != nil {
				c.logDebug("Attempt %d/%d failed for %s: %v", attempt, downloadMaxRetries, url, err)
				if attempt < downloadMaxRetries {
					c.logDebug("Retrying in %s...", downloadRetryDelay)
					time.Sleep(downloadRetryDelay)
				}
				continue
			}

			// Non-200 means this file doesn't exist at this URL; skip to next.
			if resp.StatusCode != http.StatusOK {
				resp.Body.Close()
				c.logDebug("URL returned HTTP %d, skipping: %s", resp.StatusCode, url)
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
				c.logWarning("Download attempt %d/%d failed for %s: %v", attempt, downloadMaxRetries, url, copyErr)
				if attempt < downloadMaxRetries {
					c.logDebug("Retrying in %s...", downloadRetryDelay)
					time.Sleep(downloadRetryDelay)
				}
				continue
			}

			c.logInfo("Downloaded %s", url)
			return tmpPath, nil
		}
	}

	return "", fmt.Errorf("could not download CoreOS JSON from any known location for release %s", release)
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

// ─── External tool invocations ────────────────────────────────────────────────

// callPvsadm invokes "pvsadm image qcow2ova" to convert a qcow2.gz image to
// OVA format, writing the result to <ScriptDir>/<filename>.ova.gz.
//
//   - filename is the image name without extension (e.g. "rhcos-4.21.0-ppc64le").
//   - url is the remote qcow2.gz download URL passed to pvsadm via --image-url.
//
// If the output file already exists the conversion is skipped and nil is
// returned immediately.  The command is always logged via logInfo before
// execution.  In dry-run mode execution is skipped and nil is returned.
//
// The subprocess runs in c.ScriptDir so that pvsadm writes the OVA alongside
// the binary.  stdout and stderr of the subprocess are inherited.
func (c *config) callPvsadm(filename, url string) error {
	convertedFilename := filepath.Join(c.ScriptDir, filename+".ova.gz")

	if _, err := os.Stat(convertedFilename); err == nil {
		c.logInfo("OVA already exists, skipping conversion: %s", convertedFilename)
		return nil
	}

	c.logInfo("Running: pvsadm image qcow2ova --image-dist coreos --image-name %s --image-url %s --image-size 16",
		filename, url)

	if c.DryRun {
		c.logInfo("DRY RUN — skipping pvsadm execution")
		return nil
	}

	cmd := exec.Command("pvsadm",
		"image", "qcow2ova",
		"--image-dist", "coreos",
		"--image-name", filename,
		"--image-url", url,
		"--image-size", "16",
	)
	cmd.Dir = c.ScriptDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pvsadm failed: %w", err)
	}
	return nil
}

// callPvcctl invokes "pvcctl image import-linux" to import an image into
// PowerVC.
//
//   - url is the image source: either a remote HTTPS URL or an absolute local
//     path (e.g. the .ova.gz produced by callPvsadm).
//   - filename becomes the PowerVC image name (--name).
//
// When url is a local path (no http:// or https:// prefix) the file must
// already exist; an error is returned if it is missing.
//
// The command is always logged via logInfo before execution.  In dry-run mode
// execution is skipped and nil is returned.
//
// Fixed import parameters: --os-type coreos, --volume-size 120,
// --config default-config.yaml, --log-file pwr1.log.
// c.ProjectUpload, c.SvcHost, and c.Template supply the remaining flags.
func (c *config) callPvcctl(url, filename string) error {
	c.logInfo("Running: pvcctl image import-linux --image %s --name %s --os-type coreos --volume-size 120 --projects %s --svc-host %s --template %s --config default-config.yaml --log-file pwr1.log",
		url, filename, c.ProjectUpload, c.SvcHost, c.Template)

	if c.DryRun {
		c.logInfo("DRY RUN — skipping pvcctl execution")
		return nil
	}

	// Validate local file existence when URL is not remote.
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		if _, err := os.Stat(url); errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("local file missing: %s", url)
		}
	}

	cmd := exec.Command("pvcctl",
		"image", "import-linux",
		"--image", url,
		"--name", filename,
		"--os-type", "coreos",
		"--volume-size", "120",
		"--projects", c.ProjectUpload,
		"--svc-host", c.SvcHost,
		"--template", c.Template,
		"--config", "default-config.yaml",
		"--log-file", "pwr1.log",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pvcctl failed: %w", err)
	}
	return nil
}

// callPowervcImage invokes "powervc-image import" to import a previously
// converted OVA image into PowerVC.
//
//   - filename is the image name without extension; the function derives the
//     full OVA path as <ScriptDir>/<filename>.ova.gz.
//
// Returns an error immediately if the OVA file does not exist — callPvsadm
// must have run successfully before this function is called (powervc-image
// does not download images; it only imports local OVA files).
//
// The command is always logged via logInfo before execution.  In dry-run mode
// execution is skipped and nil is returned.
//
// Fixed import parameter: a single -m flag with the value
// "os-type=coreos architecture=ppc64le", matching the shell script exactly.
// c.ProjectUpload and c.Template supply the remaining flags.
func (c *config) callPowervcImage(filename string) error {
	convertedFilename := filepath.Join(c.ScriptDir, filename+".ova.gz")

	if _, err := os.Stat(convertedFilename); errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("OVA file missing: %s", convertedFilename)
	}

	c.logInfo("Running: powervc-image --project %s import -n %s -p %s -t %s -m os-type=coreos architecture=ppc64le",
		c.ProjectUpload, filename, convertedFilename, c.Template)

	if c.DryRun {
		c.logInfo("DRY RUN — skipping powervc-image execution")
		return nil
	}

	// The shell passes a single -m argument whose value contains both
	// key=value pairs separated by a space.  Two separate -m flags would be
	// semantically different and may not be accepted by the tool.
	cmd := exec.Command("powervc-image",
		"--project", c.ProjectUpload,
		"import",
		"-n", filename,
		"-p", convertedFilename,
		"-t", c.Template,
		"-m", "os-type=coreos architecture=ppc64le",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("powervc-image failed: %w", err)
	}
	return nil
}

// ─── Per-release processing ───────────────────────────────────────────────────

// processRelease executes the full workflow for a single release branch:
//
//  1. Download the CoreOS JSON metadata from GitHub into a temp file.
//  2. Parse the JSON to extract the ppc64le qcow2.gz URL, filename, and SHA256.
//  3. Check whether the image already exists in OpenStack; return early if so.
//  4. If c.usePvsadm is true, convert the qcow2 image to OVA via callPvsadm.
//  5. Import the image into PowerVC via callPvcctl (if c.usePvcctl) or
//     callPowervcImage.
//
// The temporary JSON file is removed via defer when the function returns.
// Returns a non-nil error if any step fails; partial failures are not retried.
func (c *config) processRelease(release string) error {
	c.logInfo("Processing release: %s", release)

	// Step 1: Download the CoreOS JSON metadata.
	jsonPath, err := c.downloadCoreosJSON(release)
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

	// Step 4a: Optionally convert qcow2 → OVA.
	if c.usePvsadm {
		if err := c.callPvsadm(info.Filename, info.DownloadURL); err != nil {
			return err
		}
	}

	// Step 4b: Import into PowerVC.
	// When pvsadm ran, hand pvcctl the local OVA file it produced.
	// When pvsadm was absent, fall back to the remote URL directly.
	if c.usePvcctl {
		importSource := info.DownloadURL
		if c.usePvsadm {
			importSource = filepath.Join(c.ScriptDir, info.Filename+".ova.gz")
		}
		if err := c.callPvcctl(importSource, info.Filename); err != nil {
			return err
		}
	} else {
		if err := c.callPowervcImage(info.Filename); err != nil {
			return err
		}
	}

	c.logSuccess("Release %s uploaded successfully", release)
	return nil
}

// ─── Usage ────────────────────────────────────────────────────────────────────

// showUsage prints comprehensive usage information to stdout, including all
// OPTIONS, ENVIRONMENT VARIABLES, REQUIRED TOOLS, EXAMPLES, and WORKFLOW
// sections, followed by the standard flag defaults from fs.
func showUsage(fs *flag.FlagSet) {
	programName := filepath.Base(os.Args[0])
	fmt.Printf(`Usage: %s [OPTIONS]

Download RHCOS images and upload them to PowerVC/OpenStack for OpenShift deployments.

This program automates the process of:
  1. Downloading CoreOS metadata from GitHub
  2. Extracting image information (URL, filename, SHA256)
  3. Converting qcow2 images to OVA format using pvsadm (if available)
  4. Importing OVA images into PowerVC using pvcctl or powervc-image

OPTIONS:
  --cloud <name>           OpenStack cloud name from clouds.yaml
                           Can also be set via CLOUD environment variable
  --project <name>         Optional project prefix prepended to image filenames
                           Trailing '-' is stripped if present
  --project-upload <name>  PowerVC project name for image upload
                           Can be set via PROJECT_UPLOAD environment variable
  --release <version>      Specify a release version (can be used multiple times)
                           Example: --release release-4.21 --release release-4.22
                           Default: release-4.21 if not specified
  --rhel <version>         Prefer specific RHEL version: rhel9 or rhel10
                           Can be set via RHEL_VERSION environment variable
  --svc-host <host>        PowerVC service host
                           Can be set via SVC_HOST environment variable
  --template <uuid>        PowerVC template UUID
                           Can be set via TEMPLATE environment variable
  -v, --verbose            Enable verbose output with debug information
  --dry-run                Simulate operations without making actual changes
  --quiet                  Suppress all non-error output
  -h, --help               Show this help message and exit

ENVIRONMENT VARIABLES:
  CLOUD            OpenStack cloud name from clouds.yaml
  PROJECT          Optional project prefix prepended to image filenames
  PROJECT_UPLOAD   PowerVC project name for image upload
  RHEL_VERSION     RHEL version preference (rhel9 or rhel10)
  SVC_HOST         PowerVC service host
  TEMPLATE         PowerVC template UUID

REQUIRED TOOLS:
  openstack              For verifying image existence (unless --dry-run)
  pvsadm                 For converting qcow2 images to OVA format (optional)
  pvcctl or powervc-image For importing images into PowerVC (either one required)

EXAMPLES:
  # Specify all options on command line
  %s --cloud mycloud --project-upload myproject --release release-4.21 \
              --rhel rhel9 --svc-host powervc.example.com --template <uuid>

  # Multiple releases with RHEL 9 preference
  %s --release release-4.21 --release release-4.22 --rhel rhel9

  # Dry run to test without actual operations
  %s --release release-4.21 --dry-run

  # Verbose output for debugging
  %s --release release-4.21 --verbose

WORKFLOW:
  1. Parse command-line arguments and collect missing variables interactively
  2. Validate all required environment variables are set
  3. Check for required programs and detect pvsadm/pvcctl availability
  4. Verify OpenStack connectivity
  5. For each release:
     a. Download CoreOS JSON metadata from GitHub
     b. Extract image URL, filename, and SHA256 checksum
     c. Check if image already exists in OpenStack
     d. If not present:
        - Call pvsadm to convert qcow2 to OVA (if available)
        - Call pvcctl or powervc-image to import the image

`, programName, programName, programName, programName, programName)
	fs.PrintDefaults()
}

// ─── Entry point ─────────────────────────────────────────────────────────────

// main is the program entry point.  It:
//  1. Resolves c.ScriptDir from the executable path (OVA files are written here).
//  2. Parses command-line arguments via parseArguments.
//  3. Checks for required external tools and detects pvsadm/pvcctl availability.
//  4. Collects any missing configuration from environment variables or prompts.
//  5. Validates that all required fields are set.
//  6. Verifies OpenStack connectivity.
//  7. Processes each release in order, logging errors but continuing on failure.
//  8. Exits with status 0 if all releases succeeded, or status 1 if any failed.
func main() {
	// Resolve the directory containing this binary so that OVA files can be
	// written alongside it, mirroring SCRIPT_DIR in the shell script.
	exePath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, prefixError+"Failed to resolve executable path: %v\n", err)
		os.Exit(1)
	}
	scriptDir := filepath.Dir(exePath)

	c := parseArguments(os.Args[1:])
	c.ScriptDir = scriptDir

	c.logInfo("Starting OpenShift RHCOS image upload program")
	c.logInfo("Working directory: %s", scriptDir)

	if c.DryRun {
		c.logWarning("Running in DRY RUN mode - no actual operations will be performed")
	}

	c.logDebug("Parsed arguments: releases=%v verbose=%v dry-run=%v rhel=%s project-upload=%s svc-host=%s template=%s",
		c.Releases, c.Verbose, c.DryRun, c.RhelVersion, c.ProjectUpload, c.SvcHost, c.Template)

	// Check tools first so we fail fast before prompting for variables.
	c.checkRequiredPrograms()
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
