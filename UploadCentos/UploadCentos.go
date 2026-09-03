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

// upload-centos downloads CentOS Stream GenericCloud qcow2 images and uploads
// them into a PowerVC/OpenStack environment.
//
// # Overview
//
// The program:
//  1. Fetches the HTML directory listing from cloud.centos.org to find the
//     latest (or pinned) dated CentOS Stream GenericCloud qcow2 image for ppc64le.
//  2. Derives the bare image name from the URL.
//  3. Checks whether the image already exists in OpenStack via the gophercloud
//     Glance API.
//  4. If the image is absent, optionally converts the qcow2 image to OVA format
//     with pvsadm, then imports it into PowerVC with pvcctl or powervc-image.
//
// # Dependencies
//
// OpenStack connectivity and image lookups use the gophercloud API directly
// (clouds.yaml), so no openstack CLI binary is required.
//
//   - net/http        — image-listing fetch (handled natively)
//   - pvsadm          — qcow2 → OVA conversion (optional; detected at runtime)
//   - pvcctl OR powervc-image — PowerVC image import (one is required)
//
// # Usage
//
//	UploadCentos [flags]
//
//	Flags:
//	  --cloud <name>           OpenStack cloud from clouds.yaml  (env: CLOUD)
//	  --centos <CentOS9|CentOS10>  CentOS Stream version         (env: CENTOS_VERSION)
//	  --date <YYYYMMDD>        Pin to a specific build date (default: latest)
//	  --project <name>         Optional prefix for image filenames (env: PROJECT)
//	  --project-upload <name>  PowerVC project for image upload   (env: PROJECT_UPLOAD)
//	  --svc-host <host>        PowerVC service host               (env: SVC_HOST)
//	  --template <uuid>        PowerVC template UUID              (env: TEMPLATE)
//	  -v, --verbose            Enable debug output
//	  --dry-run                Simulate operations; no real calls
//	  -q, --quiet              Suppress all non-error output
//	  -h, --help               Show usage and exit
//
// # Environment Variables
//
// All flags can also be supplied via the correspondingly named environment
// variable (CLOUD, CENTOS_VERSION, PROJECT, PROJECT_UPLOAD, SVC_HOST, TEMPLATE).
// If a required variable is missing and stdin is a terminal the program
// prompts interactively.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
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

// HTTP fetch configuration for the image-listing request.
const listingTimeout = 30 * time.Second

// reDate matches a valid YYYYMMDD build-date pin supplied via --date.
var reDate = regexp.MustCompile(`^\d{8}$`)

// stdinReader is a single buffered reader over os.Stdin shared across all
// interactive prompts.  Using one reader prevents the internal read-ahead
// buffer of one call consuming bytes that the next call expects.
var stdinReader = bufio.NewReader(os.Stdin)

// ─── Global configuration ─────────────────────────────────────────────────────

// config holds the runtime configuration derived from flags and environment
// variables.
type config struct {
	// Cloud is the OpenStack cloud name from clouds.yaml.
	Cloud string

	// CentosVersion is the requested CentOS Stream version: "CentOS9" or "CentOS10".
	CentosVersion string

	// CentosDate is an optional YYYYMMDD build-date pin.  When empty the latest
	// available dated image is selected automatically.
	CentosDate string

	// Project is the optional prefix prepended to generated image filenames.
	// A trailing hyphen is stripped before use.
	Project string

	// ProjectUpload is the PowerVC project name used during image import.
	ProjectUpload string

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
//   - pvcctl OR powervc-image — at least one must be present for image import.
//
// Optional:
//   - pvsadm — if found, c.usePvsadm is set to true and qcow2→OVA conversion
//     is performed before import; if absent a warning is logged and conversion
//     is skipped.
//
// OpenStack connectivity and image existence are handled natively via the
// gophercloud API (see OpenStack.go), so no openstack CLI binary is required.
//
// Side-effects: sets c.usePvsadm and c.usePvcctl.
// Exits via die if both import tools are missing.
func (c *config) checkRequiredPrograms() {
	c.logInfo("Checking required programs...")

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

// promptCentosVersion loops until the user enters exactly "CentOS9" or
// "CentOS10", re-prompting and printing an error on each invalid entry.
func promptCentosVersion() string {
	for {
		v := promptInput("CentOS Stream version (CentOS9 or CentOS10)", "CENTOS_VERSION", "", false)
		if v == "CentOS9" || v == "CentOS10" {
			return v
		}
		fmt.Fprintf(os.Stderr, prefixError+"Invalid CentOS version %q — must be CentOS9 or CentOS10\n", v)
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

// parseArguments parses args (typically os.Args[1:]), validates flag values,
// and returns a populated *config.
//
// Notable behaviour:
//   - --centos must be "CentOS9" or "CentOS10" if supplied; any other value
//     causes the program to exit with status 1.
//   - --date must match the YYYYMMDD pattern if supplied; any other value
//     causes the program to exit with status 1.
//   - The FlagSet uses flag.ExitOnError, so unknown flags or --help cause an
//     immediate os.Exit; fs.Parse itself never returns a non-nil error.
func parseArguments(args []string) *config {
	c := &config{}

	fs := flag.NewFlagSet("UploadCentos", flag.ExitOnError)
	fs.Usage = func() { showUsage(fs) }

	centosFlag := fs.String("centos", "", "CentOS Stream version: CentOS9 or CentOS10")
	cloudFlag := fs.String("cloud", "", "OpenStack cloud name from clouds.yaml")
	dateFlag := fs.String("date", "", "Pin to a specific build date (YYYYMMDD)")
	dryRunFlag := fs.Bool("dry-run", false, "Simulate operations without making actual changes")
	projectFlag := fs.String("project", "", "Optional project prefix prepended to image filenames")
	projectUploadFlag := fs.String("project-upload", "", "PowerVC project name for image upload")
	quietFlag := fs.Bool("quiet", false, "Suppress all non-error output")
	fs.BoolVar(quietFlag, "q", false, "Suppress all non-error output")
	svcHostFlag := fs.String("svc-host", "", "PowerVC service host")
	templateFlag := fs.String("template", "", "PowerVC template UUID")
	verboseFlag := fs.Bool("verbose", false, "Enable verbose output with debug information")
	fs.BoolVar(verboseFlag, "v", false, "Enable verbose output with debug information")

	// ExitOnError means fs.Parse calls os.Exit(2) on failure; it never returns
	// a non-nil error, but we assign it anyway to satisfy the compiler.
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	// Validate --centos value when supplied.
	if *centosFlag != "" && *centosFlag != "CentOS9" && *centosFlag != "CentOS10" {
		fmt.Fprintf(os.Stderr, prefixError+"Invalid CentOS version %q — must be CentOS9 or CentOS10\n", *centosFlag)
		os.Exit(1)
	}

	// Validate --date value when supplied.
	if *dateFlag != "" && !reDate.MatchString(*dateFlag) {
		fmt.Fprintf(os.Stderr, prefixError+"Invalid date %q — expected YYYYMMDD\n", *dateFlag)
		os.Exit(1)
	}

	c.CentosVersion = *centosFlag
	c.Cloud = *cloudFlag
	c.CentosDate = *dateFlag
	c.DryRun = *dryRunFlag
	c.Project = *projectFlag
	c.ProjectUpload = *projectUploadFlag
	c.Quiet = *quietFlag
	c.SvcHost = *svcHostFlag
	c.Template = *templateFlag
	c.Verbose = *verboseFlag

	return c
}

// collectFromEnvironment fills any config field that is still empty after flag
// parsing.  For each field it first checks the corresponding environment
// variable; if that is also absent the user is prompted interactively.
//
// Special cases:
//   - Project (PROJECT) is optional and is only read from the environment;
//     the user is never prompted for it.
//   - CentosVersion (CENTOS_VERSION) is validated against "CentOS9"/"CentOS10";
//     an invalid env value calls die; an absent value uses promptCentosVersion
//     which loops until a valid choice is entered.
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

	// CentosVersion requires validation; use a dedicated prompt loop.
	if c.CentosVersion == "" {
		if v := os.Getenv("CENTOS_VERSION"); v != "" {
			if v != "CentOS9" && v != "CentOS10" {
				c.die("CENTOS_VERSION has invalid value %q — must be CentOS9 or CentOS10", v)
			}
			c.CentosVersion = v
		} else {
			c.CentosVersion = promptCentosVersion()
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
// Required fields: Cloud, CentosVersion, ProjectUpload, SvcHost, Template.
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
		{"CENTOS_VERSION", c.CentosVersion},
		{"SVC_HOST", c.SvcHost},
		{"TEMPLATE", c.Template},
	}

	for _, r := range required {
		if r.value == "" {
			c.die("%s must be set and non-empty", r.name)
		}
	}

	c.logSuccess("All environment variables validated")
}

// ─── Image listing fetch ──────────────────────────────────────────────────────

// findLatestCentosQcow2URL fetches the HTML directory listing from the official
// CentOS cloud mirror and returns the full URL of the latest (or pinned) dated
// CentOS Stream GenericCloud qcow2 image for ppc64le.
//
// centosVersion must be "CentOS9" or "CentOS10".
// pinDate is an optional YYYYMMDD string; when non-empty only filenames
// containing that exact date are considered.
//
// Only date-stamped filenames are considered (e.g.
// CentOS-Stream-GenericCloud-9-20260720.0.ppc64le.qcow2); the "-latest"
// symlink is intentionally excluded so the returned URL is always concrete and
// reproducible.
//
// Returns the full image URL and the extracted YYYYMMDD build date, or an
// error if the listing cannot be fetched or no matching file is found.
func (c *config) findLatestCentosQcow2URL(centosVersion, pinDate string) (url, buildDate string, err error) {
	var streamNum string
	switch centosVersion {
	case "CentOS9":
		streamNum = "9"
	case "CentOS10":
		streamNum = "10"
	default:
		return "", "", fmt.Errorf("invalid CentOS version %q — must be CentOS9 or CentOS10", centosVersion)
	}

	baseURL := fmt.Sprintf("https://cloud.centos.org/centos/%s-stream/ppc64le/images/", streamNum)
	c.logDebug("Fetching image listing from: %s", baseURL)

	client := &http.Client{Timeout: listingTimeout}
	resp, err := client.Get(baseURL) //nolint:noctx
	if err != nil {
		return "", "", fmt.Errorf("failed to retrieve image listing from %s: %w", baseURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("failed to retrieve image listing from %s: HTTP %d", baseURL, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", fmt.Errorf("failed to read image listing from %s: %w", baseURL, err)
	}

	listing := string(body)

	// Build a regexp that matches only date-stamped filenames for the requested
	// stream number.  When a pin date is requested, substitute it directly.
	var datePattern string
	if pinDate != "" {
		datePattern = regexp.QuoteMeta(pinDate)
		c.logDebug("Pinning to build date: %s", pinDate)
	} else {
		datePattern = `\d{8}`
	}

	// Match href="CentOS-Stream-GenericCloud-N-YYYYMMDD.N.ppc64le.qcow2"
	// The outer group captures just the filename (without the surrounding quote).
	reStr := fmt.Sprintf(`href="(CentOS-Stream-GenericCloud-%s-%s\.\d+\.ppc64le\.qcow2)"`, streamNum, datePattern)
	re := regexp.MustCompile(reStr)

	matches := re.FindAllStringSubmatch(listing, -1)
	if len(matches) == 0 {
		if pinDate != "" {
			return "", "", fmt.Errorf("no CentOS-Stream-GenericCloud-%s .qcow2 file found for date %s at %s",
				streamNum, pinDate, baseURL)
		}
		return "", "", fmt.Errorf("no dated CentOS-Stream-GenericCloud-%s .qcow2 files found at %s",
			streamNum, baseURL)
	}

	// Collect all matching filenames and sort lexicographically; the
	// YYYYMMDD.N scheme sorts correctly without any date arithmetic.
	filenames := make([]string, 0, len(matches))
	for _, m := range matches {
		filenames = append(filenames, m[1])
	}
	sort.Strings(filenames)
	latestFilename := filenames[len(filenames)-1]

	c.logDebug("Latest CentOS %s Stream qcow2 filename: %s", streamNum, latestFilename)

	// Extract the YYYYMMDD date embedded in the filename.
	// Pattern: CentOS-Stream-GenericCloud-N-YYYYMMDD.N.ppc64le.qcow2
	reDateStr := fmt.Sprintf(`CentOS-Stream-GenericCloud-%s-(\d{8})`, streamNum)
	reBuildDate := regexp.MustCompile(reDateStr)
	dateMatch := reBuildDate.FindStringSubmatch(latestFilename)
	if len(dateMatch) < 2 {
		return "", "", fmt.Errorf("could not extract build date from filename: %s", latestFilename)
	}
	extractedDate := dateMatch[1]
	c.logDebug("Build date: %s", extractedDate)

	return baseURL + latestFilename, extractedDate, nil
}

// ─── OpenStack helpers ────────────────────────────────────────────────────────

// verifyOpenstackConnectivity confirms that the configured cloud is reachable by
// listing images through the gophercloud API (see getAllImages in
// OpenStack.go).  A successful list proves both authentication and image-service
// availability.
//
// In dry-run mode the check is skipped entirely and an info message is logged.
// If the lookup fails, die is called — there is no point continuing without a
// working OpenStack connection.
func (c *config) verifyOpenstackConnectivity() {
	if c.DryRun {
		c.logInfo("Skipping OpenStack connectivity check in DRY RUN mode")
		return
	}

	c.logInfo("Verifying OpenStack connectivity...")

	// Bound the API call so a hung cloud cannot stall startup.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
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
// Returns false in dry-run mode (so that the upload path and its commands are
// always exercised and printed during a dry run).  A lookup that fails for any
// reason other than a clean "found" is treated as not-present so the upload
// path proceeds; the reason is logged.
func (c *config) imageExistsInOpenStack(imageName string) bool {
	c.logInfo("Checking whether image already exists: %s", imageName)

	if c.DryRun {
		c.logInfo("DRY RUN — skipping image existence check")
		return false
	}

	// Bound the API lookup so a hung cloud cannot stall the program.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if _, err := c.findImage(ctx, c.Cloud, imageName); err != nil {
		c.logInfo("Image not found in OpenStack: %s (%v)", imageName, err)
		return false
	}

	c.logSuccess("Image already exists in OpenStack: %s", imageName)
	return true
}

// ─── External tool invocations ────────────────────────────────────────────────

// callPvsadm invokes "pvsadm image qcow2ova" to convert a qcow2 image to OVA
// format, writing the result to <ScriptDir>/<filename>.ova.gz.
//
//   - filename is the image name without extension
//     (e.g. "CentOS-Stream-GenericCloud-9-20260720.0.ppc64le").
//   - url is the remote qcow2 download URL passed to pvsadm via --image-url.
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
//     path (the .ova.gz produced by callPvsadm).  pvcctl handles both natively.
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
func (c *config) callPvcctl(imageURL, filename string) error {
	convertedFilename := filepath.Join(c.ScriptDir, filename+".ova.gz")

	// Prefer the locally converted OVA when pvsadm has already produced it;
	// fall back to the remote URL so pvcctl can fetch and convert it directly.
	imageSource := imageURL
	if c.usePvsadm {
		imageSource = convertedFilename
	}

	c.logInfo("Running: pvcctl image import-linux --image %s --name %s --os-type coreos --volume-size 120 --projects %s --svc-host %s --template %s --config default-config.yaml --log-file pwr1.log",
		imageSource, filename, c.ProjectUpload, c.SvcHost, c.Template)

	if c.DryRun {
		c.logInfo("DRY RUN — skipping pvcctl execution")
		return nil
	}

	// Validate local file existence when URL is not remote.
	if !strings.HasPrefix(imageSource, "http://") && !strings.HasPrefix(imageSource, "https://") {
		if _, err := os.Stat(imageSource); errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("local file missing: %s", imageSource)
		}
	}

	cmd := exec.Command("pvcctl",
		"image", "import-linux",
		"--image", imageSource,
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
// "os-type=coreos architecture=ppc64le".
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

// ─── Usage ────────────────────────────────────────────────────────────────────

// showUsage prints comprehensive usage information to stdout, including all
// OPTIONS, ENVIRONMENT VARIABLES, REQUIRED TOOLS, EXAMPLES, and WORKFLOW
// sections, followed by the standard flag defaults from fs.
func showUsage(fs *flag.FlagSet) {
	programName := filepath.Base(os.Args[0])
	fmt.Printf(`Usage: %s [OPTIONS]

Download CentOS Stream images and upload them to PowerVC/OpenStack.

This program automates the process of:
  1. Fetching the latest CentOS Stream GenericCloud qcow2 image URL from
     the official CentOS cloud mirror
  2. Deriving the image name from the URL
  3. Converting the qcow2 image to OVA format using pvsadm (if available)
  4. Importing the OVA image into PowerVC using pvcctl or powervc-image

OPTIONS:
  --cloud <name>           OpenStack cloud name from clouds.yaml
                           Can also be set via CLOUD environment variable
  --centos <version>       CentOS Stream version: CentOS9 or CentOS10
                           Can also be set via CENTOS_VERSION environment variable
  --date <YYYYMMDD>        Pin to a specific image build date (e.g. 20260721)
                           If omitted, the latest available build is used
  --project <name>         Optional project prefix prepended to image filenames
                           Trailing '-' is stripped if present
  --project-upload <name>  PowerVC project name for image upload access control
                           Can also be set via PROJECT_UPLOAD environment variable
  --svc-host <host>        PowerVC service host for image import
                           Can also be set via SVC_HOST environment variable
  --template <uuid>        PowerVC template UUID for image creation
                           Can also be set via TEMPLATE environment variable
  -q, --quiet              Suppress informational output (errors still shown)
  -v, --verbose            Enable verbose output with debug information
  --dry-run                Simulate operations without making actual changes
  -h, --help               Show this help message and exit

ENVIRONMENT VARIABLES:
  CLOUD            OpenStack cloud name from clouds.yaml
  CENTOS_VERSION   CentOS Stream version (CentOS9 or CentOS10)
  PROJECT          Optional project prefix prepended to image filenames
  PROJECT_UPLOAD   PowerVC project name for image upload
  SVC_HOST         PowerVC service host
  TEMPLATE         PowerVC template UUID

REQUIRED TOOLS:
  pvsadm                 For converting qcow2 images to OVA format (optional)
  pvcctl or powervc-image For importing images into PowerVC (either one required)

EXAMPLES:
  # Interactive mode (prompts for missing variables)
  %s

  # Specify all options on command line
  %s --cloud mycloud --project-upload myproject \
             --centos CentOS9 --svc-host powervc.example.com --template <uuid>

  # Dry run to test without actual operations
  %s --centos CentOS9 --dry-run

  # Verbose output for debugging
  %s --centos CentOS9 --verbose

  # Pin to a specific build date
  %s --centos CentOS9 --date 20260720

WORKFLOW:
  1. Parse command-line arguments
  2. Check for required programs (pvcctl or powervc-image; optionally pvsadm)
  3. Collect missing config from environment variables or interactive prompts
  4. Validate all required environment variables are set
  5. Verify OpenStack connectivity
  6. Fetch the latest dated CentOS Stream GenericCloud qcow2 image URL
  7. Derive the image name from the URL (apply --project prefix if set)
  8. Check if the image already exists in OpenStack
  9. If not present:
     - Call pvsadm to convert qcow2 to OVA (if available)
     - Call pvcctl (with local OVA or URL) or powervc-image (with local OVA)

`, programName, programName, programName, programName, programName, programName)
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
//  7. Finds the latest (or pinned) CentOS Stream qcow2 image URL.
//  8. Checks if the image already exists in OpenStack; exits cleanly if so.
//  9. Converts the image with pvsadm (if available) then imports with pvcctl
//     or powervc-image.
// 10. Exits with status 0 on success or status 1 on failure.
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

	c.logInfo("Starting CentOS Stream image upload program")
	c.logInfo("Working directory: %s", scriptDir)

	if c.DryRun {
		c.logWarning("Running in DRY RUN mode - no actual operations will be performed")
	}

	// Step 1: Check tools first so we fail fast before prompting for variables.
	c.checkRequiredPrograms()

	// Step 2: Collect and validate configuration.
	c.collectFromEnvironment()
	c.validateEnvironment()
	c.verifyOpenstackConnectivity()

	c.logDebug("Parsed arguments: verbose=%v dry-run=%v centos=%s project-upload=%s svc-host=%s template=%s",
		c.Verbose, c.DryRun, c.CentosVersion, c.ProjectUpload, c.SvcHost, c.Template)

	// Step 3: Resolve the latest dated qcow2 image URL and build date from the
	// official CentOS cloud mirror for the requested stream version.
	imageURL, buildDate, err := c.findLatestCentosQcow2URL(c.CentosVersion, c.CentosDate)
	if err != nil {
		c.die("%v", err)
	}
	c.logDebug("url=%s", imageURL)
	c.logInfo("Build date: %s", buildDate)

	// Step 4: Derive the image name used in OpenStack/PowerVC from the URL.
	// e.g. https://.../CentOS-Stream-GenericCloud-9-20260720.0.ppc64le.qcow2
	//   -> imagename = CentOS-Stream-GenericCloud-9-20260720.0.ppc64le
	base := filepath.Base(imageURL)
	imagename := strings.TrimSuffix(base, ".qcow2")

	// Prepend optional project prefix (strip trailing "-" first).
	if c.Project != "" {
		project := strings.TrimSuffix(c.Project, "-")
		c.logInfo("Prepending project (%s) to image filename", project)
		imagename = project + "-" + imagename
	}

	c.logDebug("imagename=%s", imagename)

	// Step 5: Skip upload if the image already exists in OpenStack/PowerVC.
	if c.imageExistsInOpenStack(imagename) {
		c.logSuccess("Image '%s' already present — skipping upload", imagename)
		return
	}

	// Step 6: Convert qcow2 to OVA when pvsadm is available.
	// - pvcctl can import directly from a URL, so pvsadm is optional for that path.
	// - powervc-image requires a local file, so pvsadm is mandatory for that path.
	if c.usePvsadm {
		if err := c.callPvsadm(imagename, imageURL); err != nil {
			c.die("%v", err)
		}
	} else if !c.usePvcctl {
		c.die("pvsadm is required to convert the qcow2 image when using powervc-image")
	}

	// Step 7: Import the image into PowerVC.
	if c.usePvcctl {
		if err := c.callPvcctl(imageURL, imagename); err != nil {
			c.die("%v", err)
		}
	} else {
		if err := c.callPowervcImage(imagename); err != nil {
			c.die("%v", err)
		}
	}

	c.logSuccess("Image '%s' uploaded successfully", imagename)
}
