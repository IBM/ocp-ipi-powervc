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
//   - curl        – URL accessibility checks and JSON downloads
//   - jq          – JSON parsing
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

// imageInfo holds the metadata extracted from a CoreOS JSON file.
type imageInfo struct {
	DownloadURL string
	Filename    string
	SHA256      string
}

// ─── Logging ─────────────────────────────────────────────────────────────────

func (c *config) logInfo(format string, args ...any) {
	if !c.Quiet {
		fmt.Printf(colorBlue+"[INFO]"+colorReset+" "+format+"\n", args...)
	}
}

func (c *config) logSuccess(format string, args ...any) {
	if !c.Quiet {
		fmt.Printf(colorGreen+"[SUCCESS]"+colorReset+" "+format+"\n", args...)
	}
}

func (c *config) logWarning(format string, args ...any) {
	if !c.Quiet {
		fmt.Printf(colorYellow+"[WARNING]"+colorReset+" "+format+"\n", args...)
	}
}

func (c *config) logError(format string, args ...any) {
	if !c.Quiet {
		fmt.Fprintf(os.Stderr, colorRed+"[ERROR]"+colorReset+" "+format+"\n", args...)
	}
}

func (c *config) logDebug(format string, args ...any) {
	if c.Verbose {
		fmt.Printf(colorCyan+"[DEBUG]"+colorReset+" "+format+"\n", args...)
	}
}

// die logs the message unconditionally and exits with status 1.
func (c *config) die(format string, args ...any) {
	savedQuiet := c.Quiet
	c.Quiet = false
	c.logError(format, args...)
	c.Quiet = savedQuiet
	os.Exit(1)
}

// ─── Dependency detection ─────────────────────────────────────────────────────

// commandExists returns true when the named program is found in PATH.
func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// checkRequiredPrograms verifies that all required external tools are available
// and sets c.usePvsadm / c.usePvcctl accordingly.
func (c *config) checkRequiredPrograms() {
	c.logInfo("Checking required programs...")

	required := []string{"curl", "jq", "openstack"}
	var missing []string
	for _, prog := range required {
		if !commandExists(prog) {
			missing = append(missing, prog)
			c.logError("Missing required program: %s", prog)
		}
	}
	if len(missing) > 0 {
		c.die("Missing required programs: %s", strings.Join(missing, ", "))
	}

	if commandExists("pvsadm") {
		c.usePvsadm = true
	} else {
		c.logWarning("pvsadm is missing")
		c.usePvsadm = false
	}

	switch {
	case commandExists("pvcctl"):
		c.logInfo("Found pvcctl over powervc-image")
		c.usePvcctl = true
	case commandExists("powervc-image"):
		c.logInfo("Did not find pvcctl, but found powervc-image instead")
		c.usePvcctl = false
	default:
		c.die("Missing required programs: either pvcctl or powervc-image must exist!")
	}

	c.logSuccess("All required programs are available")
}

// ─── Interactive prompts ──────────────────────────────────────────────────────

// promptInput reads a value from stdin with an optional default.  If the input
// is empty and allowEmpty is false the program exits with an error.
func promptInput(prompt, varName, defaultVal string, allowEmpty bool) string {
	reader := bufio.NewReader(os.Stdin)

	if defaultVal != "" {
		fmt.Printf("%s [%s]: ", prompt, defaultVal)
	} else {
		fmt.Printf("%s []: ", prompt)
	}

	line, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		fmt.Fprintf(os.Stderr, colorRed+"[ERROR]"+colorReset+" Failed to read input for %s: %v\n", varName, err)
		os.Exit(1)
	}

	value := strings.TrimSpace(line)
	if value == "" {
		value = defaultVal
	}

	if value == "" && !allowEmpty {
		fmt.Fprintf(os.Stderr, colorRed+"[ERROR]"+colorReset+" You must enter a value for %s\n", varName)
		os.Exit(1)
	}

	return value
}

// ─── Argument parsing ─────────────────────────────────────────────────────────

// releaseFlag supports --release being specified multiple times.
type releaseFlag []string

func (r *releaseFlag) String() string { return strings.Join(*r, ", ") }
func (r *releaseFlag) Set(v string) error {
	*r = append(*r, v)
	return nil
}

// parseArguments processes os.Args[1:] and populates the configuration.  Any
// required variable that is still empty after flag parsing is collected from the
// environment, and if still absent, via an interactive prompt.
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
	rhelFlag := fs.String("rhel", "", "Prefer specific RHEL version: rhel9 or rhel10")
	svcHostFlag := fs.String("svc-host", "", "PowerVC service host")
	templateFlag := fs.String("template", "", "PowerVC template UUID")
	verboseFlag := fs.Bool("verbose", false, "Enable verbose output with debug information")
	fs.BoolVar(verboseFlag, "v", false, "Enable verbose output with debug information")

	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, colorRed+"[ERROR]"+colorReset+" Failed to parse arguments: %v\n", err)
		os.Exit(1)
	}

	// Validate --rhel value when supplied.
	if *rhelFlag != "" && *rhelFlag != "rhel9" && *rhelFlag != "rhel10" {
		fmt.Fprintf(os.Stderr, colorRed+"[ERROR]"+colorReset+" Invalid RHEL version '%s'. Must be rhel9 or rhel10\n", *rhelFlag)
		os.Exit(1)
	}

	// Populate config from flags.
	c.Releases = []string(releases)
	c.Cloud = *cloudFlag
	c.DryRun = *dryRunFlag
	c.Project = *projectFlag
	c.ProjectUpload = *projectUploadFlag
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

// collectFromEnvironment fills missing configuration fields from environment
// variables, then interactively prompts for any that remain empty.
func (c *config) collectFromEnvironment() {
	c.logInfo("Collecting environment variables...")

	// Cloud
	if c.Cloud == "" {
		if v := os.Getenv("CLOUD"); v != "" {
			c.Cloud = v
		} else {
			c.Cloud = promptInput("What is the cloud name in ~/.config/openstack/clouds.yaml", "CLOUD", "", false)
		}
	}

	// PROJECT is optional — only fill from environment, no prompt.
	if c.Project == "" {
		c.Project = os.Getenv("PROJECT")
	}

	// ProjectUpload
	if c.ProjectUpload == "" {
		if v := os.Getenv("PROJECT_UPLOAD"); v != "" {
			c.ProjectUpload = v
		} else {
			c.ProjectUpload = promptInput("What is the project when uploading?", "PROJECT_UPLOAD", "", false)
		}
	}

	// RhelVersion
	if c.RhelVersion == "" {
		if v := os.Getenv("RHEL_VERSION"); v != "" {
			c.RhelVersion = v
		} else {
			c.RhelVersion = promptInput("What is the RHEL version?", "RHEL_VERSION", "", false)
		}
	}

	// SvcHost
	if c.SvcHost == "" {
		if v := os.Getenv("SVC_HOST"); v != "" {
			c.SvcHost = v
		} else {
			c.SvcHost = promptInput("What is the service host?", "SVC_HOST", "", false)
		}
	}

	// Template
	if c.Template == "" {
		if v := os.Getenv("TEMPLATE"); v != "" {
			c.Template = v
		} else {
			c.Template = promptInput("What is the template UUID?", "TEMPLATE", "", false)
		}
	}
}

// validateEnvironment ensures that every required variable is non-empty.
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

	// RELEASES must have at least one element.
	if len(c.Releases) == 0 {
		c.die("RELEASES must be set and non-empty")
	}

	c.logSuccess("All environment variables validated")
}

// ─── OpenStack helpers ────────────────────────────────────────────────────────

// verifyOpenstackConnectivity runs a quick image list to confirm credentials work.
func (c *config) verifyOpenstackConnectivity() {
	c.logInfo("Verifying OpenStack connectivity...")
	cmd := exec.Command("openstack", "--os-cloud="+c.Cloud, "image", "list")
	if err := cmd.Run(); err != nil {
		c.die("Cannot connect to OpenStack. Please verify clouds.yaml configuration.")
	}
	c.logSuccess("OpenStack connectivity verified")
}

// imageExistsInOpenStack returns true when an OpenStack image with the given
// name already exists.  In dry-run mode it always returns false (triggering
// the upload path so that the commands are printed).
func (c *config) imageExistsInOpenStack(imageName string) bool {
	c.logInfo("Verifying image: %s", imageName)

	if c.DryRun {
		c.logWarning("Running in DRY RUN mode - no actual call will be performed")
		return false
	}

	cmd := exec.Command("openstack", "--os-cloud="+c.Cloud, "image", "show", imageName)
	if err := cmd.Run(); err != nil {
		c.logError("Cannot find image '%s'", imageName)
		return false
	}

	c.logSuccess("Found image: %s", imageName)
	return true
}

// ─── CoreOS JSON download ─────────────────────────────────────────────────────

// canFetchURL performs a HEAD request (falling back to GET) to check whether the
// URL returns HTTP 200.
func (c *config) canFetchURL(url string) bool {
	c.logDebug("Checking URL: %s", url)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Head(url)
	if err != nil {
		// HEAD might be blocked; try GET with range to minimise transfer.
		req, rerr := http.NewRequest(http.MethodGet, url, nil)
		if rerr != nil {
			return false
		}
		req.Header.Set("Range", "bytes=0-0")
		resp, err = client.Do(req)
		if err != nil {
			return false
		}
	}
	defer resp.Body.Close()
	// Accept 200 OK or 206 Partial Content (range request succeeded).
	ok := resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusPartialContent
	c.logDebug("HTTP status code: %d", resp.StatusCode)
	return ok
}

// downloadCoreosJSON downloads the CoreOS JSON metadata for the given release
// into a temporary file, trying multiple URL locations in preference order.
// It returns the path to the temporary file on success.
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

	const maxRetries = 3
	const retryDelay = 2 * time.Second

	for _, url := range urls {
		c.logDebug("Trying URL: %s", url)
		if !c.canFetchURL(url) {
			c.logDebug("URL not available: %s", url)
			continue
		}

		for attempt := 1; attempt <= maxRetries; attempt++ {
			c.logDebug("Download attempt %d/%d: %s", attempt, maxRetries, url)

			tmpFile, err := os.CreateTemp("", "coreos-*.json")
			if err != nil {
				return "", fmt.Errorf("failed to create temp file: %w", err)
			}
			tmpPath := tmpFile.Name()

			client := &http.Client{Timeout: 5 * time.Minute}
			resp, err := client.Get(url) //nolint:noctx
			if err != nil {
				tmpFile.Close()
				os.Remove(tmpPath)
				c.logWarning("Download attempt %d/%d failed for %s: %v", attempt, maxRetries, url, err)
			} else {
				_, copyErr := io.Copy(tmpFile, resp.Body)
				resp.Body.Close()
				tmpFile.Close()
				if copyErr != nil {
					os.Remove(tmpPath)
					c.logWarning("Download attempt %d/%d failed for %s: %v", attempt, maxRetries, url, copyErr)
				} else {
					c.logInfo("Downloaded %s", url)
					return tmpPath, nil
				}
			}

			if attempt < maxRetries {
				c.logDebug("Retrying in %s...", retryDelay)
				time.Sleep(retryDelay)
			}
		}
	}

	return "", fmt.Errorf("could not download CoreOS JSON from any known location for release %s", release)
}

// ─── JSON parsing ─────────────────────────────────────────────────────────────

// extractImageInfo parses a CoreOS JSON file and returns the ppc64le OpenStack
// image metadata.  PROJECT prefix handling mirrors the shell script behaviour:
// any trailing "-" is stripped before prepending.
func (c *config) extractImageInfo(jsonPath string) (*imageInfo, error) {
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read JSON file %s: %w", jsonPath, err)
	}

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

	if err := json.Unmarshal(data, &root); err != nil {
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

// callPvsadm converts a qcow2 image to OVA format using pvsadm.
// If the output file already exists the conversion step is skipped.
func (c *config) callPvsadm(filename, url string) error {
	convertedFilename := filepath.Join(c.ScriptDir, filename+".ova.gz")

	if _, err := os.Stat(convertedFilename); err == nil {
		c.logInfo("File already exists (%s)!", convertedFilename)
		return nil
	}

	// Always print the command that would be / will be executed.
	fmt.Printf("pvsadm image qcow2ova --image-dist coreos --image-name %s --image-url %s --image-size 16\n",
		filename, url)

	if c.DryRun {
		c.logWarning("Running in DRY RUN mode - no actual call will be performed")
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

// callPvcctl imports an image into PowerVC using pvcctl.
// When url is a local path (not http:// / https://) the file must already exist.
func (c *config) callPvcctl(url, filename string) error {
	// Always print the command that would be / will be executed.
	fmt.Printf("pvcctl image import-linux --image %s --name %s --os-type coreos --volume-size 120 --projects %s --svc-host %s --template %s --config default-config.yaml --log-file pwr1.log\n",
		url, filename, c.ProjectUpload, c.SvcHost, c.Template)

	if c.DryRun {
		c.logWarning("Running in DRY RUN mode - no actual call will be performed")
		return nil
	}

	// Validate local file existence when URL is not remote.
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		if _, err := os.Stat(url); os.IsNotExist(err) {
			c.logError("File is missing: (%s)!", url)
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

// callPowervcImage imports an OVA image into PowerVC using powervc-image.
// The OVA file must exist at <ScriptDir>/<filename>.ova.gz before calling this.
func (c *config) callPowervcImage(filename string) error {
	convertedFilename := filepath.Join(c.ScriptDir, filename+".ova.gz")

	if _, err := os.Stat(convertedFilename); os.IsNotExist(err) {
		c.logError("File is missing: (%s)!", convertedFilename)
		return fmt.Errorf("OVA file missing: %s", convertedFilename)
	}

	// Always print the command that would be / will be executed.
	fmt.Printf("powervc-image --project %s import -n %s -p %s -t %s -m os-type=coreos architecture=ppc64le\n",
		c.ProjectUpload, filename, convertedFilename, c.Template)

	if c.DryRun {
		c.logWarning("Running in DRY RUN mode - no actual call will be performed")
		return nil
	}

	cmd := exec.Command("powervc-image",
		"--project", c.ProjectUpload,
		"import",
		"-n", filename,
		"-p", convertedFilename,
		"-t", c.Template,
		"-m", "os-type=coreos",
		"-m", "architecture=ppc64le",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("powervc-image failed: %w", err)
	}
	return nil
}

// ─── Per-release processing ───────────────────────────────────────────────────

// processRelease executes the full download → check → upload workflow for a
// single release.
func (c *config) processRelease(release string) error {
	c.logInfo("Processing release: %s", release)

	// Step 1: Download the CoreOS JSON metadata.
	jsonPath, err := c.downloadCoreosJSON(release)
	if err != nil {
		return fmt.Errorf("%s failed: %w", release, err)
	}
	defer os.Remove(jsonPath)

	// Step 2: Extract image metadata.
	info, err := c.extractImageInfo(jsonPath)
	if err != nil {
		return fmt.Errorf("%s failed: %w", release, err)
	}

	c.logInfo("Download URL: %s", info.DownloadURL)
	c.logInfo("Filename: %s", info.Filename)
	c.logDebug("SHA256: %s", info.SHA256)

	// Step 3: Skip upload if the image already exists.
	if c.imageExistsInOpenStack(info.Filename) {
		return nil
	}

	// Step 4a: Optionally convert qcow2 → OVA.
	if c.usePvsadm {
		if err := c.callPvsadm(info.Filename, info.DownloadURL); err != nil {
			c.logError("pvsadm failed!")
			return err
		}
	}

	// Step 4b: Import into PowerVC.
	if c.usePvcctl {
		if err := c.callPvcctl(info.DownloadURL, info.Filename); err != nil {
			c.logError("pvcctl failed!")
			return err
		}
	} else {
		if err := c.callPowervcImage(info.Filename); err != nil {
			c.logError("call_powervc_image failed!")
			return err
		}
	}

	return nil
}

// ─── Usage ────────────────────────────────────────────────────────────────────

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
  -h, --help               Show this help message and exit

ENVIRONMENT VARIABLES:
  CLOUD            OpenStack cloud name from clouds.yaml
  PROJECT          Optional project prefix prepended to image filenames
  PROJECT_UPLOAD   PowerVC project name for image upload
  RHEL_VERSION     RHEL version preference (rhel9 or rhel10)
  SVC_HOST         PowerVC service host
  TEMPLATE         PowerVC template UUID

REQUIRED TOOLS:
  curl                   For downloading files and checking URLs
  jq                     For parsing JSON metadata (invoked via openstack)
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
  4. For each release:
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

func main() {
	// Resolve the directory containing this binary so that OVA files can be
	// written alongside it, mirroring SCRIPT_DIR in the shell script.
	exePath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, colorRed+"[ERROR]"+colorReset+" Failed to resolve executable path: %v\n", err)
		os.Exit(1)
	}
	scriptDir := filepath.Dir(exePath)

	// Handle -h / --help before full flag parsing so the usage is always available.
	for _, arg := range os.Args[1:] {
		if arg == "-h" || arg == "--help" {
			fs := flag.NewFlagSet("upload-rhcos", flag.ContinueOnError)
			showUsage(fs)
			os.Exit(0)
		}
	}

	c := parseArguments(os.Args[1:])
	c.ScriptDir = scriptDir

	c.logDebug("Parsed arguments: releases=%v verbose=%v dry-run=%v rhel=%s project-upload=%s svc-host=%s template=%s",
		c.Releases, c.Verbose, c.DryRun, c.RhelVersion, c.ProjectUpload, c.SvcHost, c.Template)

	c.collectFromEnvironment()
	c.validateEnvironment()
	c.checkRequiredPrograms()

	c.logInfo("Starting OpenShift RHCOS image upload program")
	c.logInfo("Working directory: %s", scriptDir)

	if c.DryRun {
		c.logWarning("Running in DRY RUN mode - no actual operations will be performed")
	}

	c.logInfo("Processing %d release(s): %s", len(c.Releases), strings.Join(c.Releases, ", "))

	var failed int
	for _, release := range c.Releases {
		if err := c.processRelease(release); err != nil {
			c.logError("Failed to process %s: %v", release, err)
			failed++
		}
	}

	if failed > 0 {
		os.Exit(1)
	}
}
