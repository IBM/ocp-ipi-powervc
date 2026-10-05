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

// GrabConsole connects to the serial console of an OpenShift cluster node via
// a PowerVC-managed HMC or PowerVM host, captures output for a configurable
// duration, then exits cleanly.
//
// It replaces scripts/console.sh and requires no external programs: OpenStack
// server details are fetched via the gophercloud API, clouds.yaml is parsed
// with gopkg.in/yaml.v3, the PowerVC REST API is called directly for
// hypervisor/HMC details, and the SSH connection uses golang.org/x/crypto/ssh
// with password authentication and multi-password retry.
//
// # Design summary
//
// The following table maps each shell script dependency to its Go replacement:
//
//	External program   Replaced by
//	────────────────   ────────────────────────────────────────────────────
//	jq                 encoding/json  (stdlib)
//	yq                 gopkg.in/yaml.v3
//	openstack CLI      github.com/gophercloud/gophercloud/v2
//	curl (token)       net/http POST /v3/auth/tokens
//	curl (hypervisor)  net/http GET  /os-hosts/<hypervisor>
//	curl (HMC)         net/http GET  /ibm-hmcs/<uuid>
//	sshpass + ssh      golang.org/x/crypto/ssh  (password auth, multi-password retry)
//
// # Flow
//
//  1. Read metadata.json → infraID, cloud name
//  2. Parse clouds.yaml  → PowerVC auth_url, username, password
//  3. Query OpenStack compute API → instance hypervisor hostname + instance name
//  4. POST /v3/auth/tokens       → PowerVC token
//  5. GET  /os-hosts/<hypervisor>  → manager type (hmc or pvm) + HMC UUID
//  6. GET  /ibm-hmcs/<uuid>        → HMC access IP  (HMC path only)
//  7. SSH to <hmc-or-pvm-ip> as <user> with password from SSH_PASSWORDS
//  8. Run: mkvterm -m <host_display_name> -p <instance_name>
//  9. Stream output for --duration, then close
//
// # Usage
//
//	GrabConsole [OPTIONS] <server-name>
//
//	Arguments:
//	  server-name   bootstrap | master-N | worker-N  — prefixed with infraID
//	                any other name                    — used as-is (needs --cloud)
//
//	Options:
//	  --cloud <name>       OpenStack cloud from clouds.yaml (env: CLOUD)
//	  --cluster-dir <dir>  Directory containing metadata.json (env: CLUSTER_DIR, default: test)
//	  --duration <dur>     Console capture duration (default: 30s)
//	  --debug              Enable verbose debug output (env: DEBUG=true)
//	  -h, --help           Show this help message
//
// # Environment Variables
//
//	CLOUD           OpenStack cloud name (overridden by --cloud)
//	CLUSTER_DIR     Cluster directory    (overridden by --cluster-dir)
//	DEBUG           Set to "true" to enable debug output
//	SSH_PASSWORDS   Space-separated list of passwords to try for HMC/PowerVM SSH
//	TOKEN_ID        Existing PowerVC auth token (skips token acquisition if set)
package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"
	"github.com/gophercloud/utils/v2/openstack/clientconfig"
	"golang.org/x/crypto/ssh"
	"gopkg.in/yaml.v3"
)

// newHTTPClient builds an http.Client used for every HTTPS call in this
// program: the direct PowerVC REST calls (token, hypervisor, HMC) as well as
// the gophercloud OpenStack calls (Keystone auth, compute API).
//
// # Why a single shared client matters
//
// PowerVC commonly presents a certificate signed by a private CA that is not
// in the system trust store.  The original console.sh worked around this with
// curl --insecure on every request.  When we replaced that with Go's
// net/http the equivalent would be InsecureSkipVerify — which suppresses the
// error but also defeats certificate validation entirely.
//
// The better approach is to load the CA certificate that PowerVC's Keystone
// advertises and add it to a dedicated CertPool so that Go can verify the
// chain without trusting arbitrary unknown CAs.  The CA cert path is read
// from the "cacert" field of the cloud entry in clouds.yaml, which is the
// standard OpenStack client convention.
//
// # Fallback behaviour
//
// If no cacert is configured, InsecureSkipVerify is used with a [WARNING] so
// existing deployments without a cacert entry keep working.  Add a cacert line
// to clouds.yaml to silence the warning and enable proper verification:
//
//	clouds:
//	  ocp-ci:
//	    cacert: /path/to/powervc-ca.pem
//	    auth:
//	      ...
func newHTTPClient(caCertFile string, debug bool) (*http.Client, error) {
	tlsCfg := &tls.Config{}
	if caCertFile != "" {
		pemBytes, err := os.ReadFile(caCertFile)
		if err != nil {
			return nil, fmt.Errorf("cannot read CA cert %s: %w", caCertFile, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pemBytes) {
			return nil, fmt.Errorf("no valid PEM certificates found in %s", caCertFile)
		}
		tlsCfg.RootCAs = pool
		logDebug(debug, "TLS: loaded CA cert pool from %s", caCertFile)

		// When debug is on, decode and log every certificate in the file so
		// the operator can confirm the right CA is being loaded and spot
		// mismatches (wrong issuer, expired cert, DER-encoded file, etc.).
		if debug {
			debugLogCertFile(pemBytes, caCertFile)
		}
	} else {
		logWarning("no cacert found in clouds.yaml — falling back to InsecureSkipVerify")
		tlsCfg.InsecureSkipVerify = true //nolint:gosec // no CA cert available
	}
	return &http.Client{
		Transport: &http.Transport{TLSClientConfig: tlsCfg},
		Timeout:   30 * time.Second,
	}, nil
}

// debugLogCertFile decodes every PEM block in pemBytes and logs the Subject,
// Issuer, and validity window of each certificate.  Call only when debug=true.
func debugLogCertFile(pemBytes []byte, filename string) {
	rest := pemBytes
	idx := 0
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			logDebug(true, "TLS CA file: block %d type=%q (skipped)", idx, block.Type)
			idx++
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			logDebug(true, "TLS CA file: block %d parse error: %v", idx, err)
			idx++
			continue
		}
		logDebug(true, "TLS CA cert[%d] subject=%q issuer=%q notBefore=%s notAfter=%s",
			idx,
			cert.Subject.String(),
			cert.Issuer.String(),
			cert.NotBefore.Format(time.RFC3339),
			cert.NotAfter.Format(time.RFC3339),
		)
		idx++
	}
	if idx == 0 {
		logDebug(true, "TLS CA file %s: no PEM blocks found", filename)
	}
}

// debugLogServerChain opens a TLS connection to the host extracted from
// identityEndpoint and logs every certificate in the chain the server
// presents.  The dial uses InsecureSkipVerify so the chain is always logged
// even when verification fails — the goal is to show what the server is
// sending so the operator can compare it against the loaded CA cert.
func debugLogServerChain(identityEndpoint string, hc *http.Client) {
	u, err := url.Parse(identityEndpoint)
	if err != nil {
		logDebug(true, "TLS server chain: cannot parse identity endpoint %q: %v", identityEndpoint, err)
		return
	}
	host := u.Hostname()
	port := u.Port()
	if port == "" {
		port = "443"
	}
	addr := net.JoinHostPort(host, port)

	// Dial with InsecureSkipVerify so we always receive the full chain,
	// regardless of whether it verifies against our pool.  We use the
	// same timeout as the main HTTP client.
	dialCfg := &tls.Config{InsecureSkipVerify: true} //nolint:gosec // debug dial only — never used for real requests
	conn, err := tls.DialWithDialer(
		&net.Dialer{Timeout: 10 * time.Second},
		"tcp", addr, dialCfg,
	)
	if err != nil {
		logDebug(true, "TLS server chain: dial %s failed: %v", addr, err)
		return
	}
	defer conn.Close()

	chains := conn.ConnectionState().PeerCertificates
	logDebug(true, "TLS server chain: %d certificate(s) presented by %s", len(chains), addr)
	for i, cert := range chains {
		logDebug(true, "TLS server cert[%d] subject=%q issuer=%q notBefore=%s notAfter=%s",
			i,
			cert.Subject.String(),
			cert.Issuer.String(),
			cert.NotBefore.Format(time.RFC3339),
			cert.NotAfter.Format(time.RFC3339),
		)
	}

	// Attempt verification against the loaded CA pool and report the outcome
	// for each cert.  When every cert fails, also emit a plain-language
	// diagnosis: the most common cause is that the cacert file contains the
	// server's leaf cert (grabbed with openssl s_client or a browser export)
	// rather than the root CA that issued it.
	if t, ok := hc.Transport.(*http.Transport); ok && t.TLSClientConfig != nil && t.TLSClientConfig.RootCAs != nil {
		anyOK := false
		for i, cert := range chains {
			opts := x509.VerifyOptions{Roots: t.TLSClientConfig.RootCAs}
			if _, err := cert.Verify(opts); err != nil {
				logDebug(true, "TLS verify cert[%d] (%s): FAIL: %v", i, cert.Subject.String(), err)
			} else {
				logDebug(true, "TLS verify cert[%d] (%s): OK", i, cert.Subject.String())
				anyOK = true
			}
		}
		if !anyOK && len(chains) > 0 {
			// Collect the issuer of the server's leaf cert and the subject(s)
			// of the certs we loaded, so the operator can see the mismatch at
			// a glance without having to mentally parse the lines above.
			leafIssuer := chains[0].Issuer.String()
			logDebug(true, "TLS diagnosis: none of the server certs verified against the loaded CA pool")
			logDebug(true, "TLS diagnosis: server leaf cert is issued by: %s", leafIssuer)
			logDebug(true, "TLS diagnosis: the cacert file must contain the root CA (or full chain) that issued that cert,")
			logDebug(true, "TLS diagnosis: not the server leaf cert itself")
			logDebug(true, "TLS diagnosis: to fetch the correct root CA, run:")
			logDebug(true, "TLS diagnosis:   openssl s_client -connect %s -showcerts 2>/dev/null | openssl x509 -noout -text | grep Issuer", addr)
			logDebug(true, "TLS diagnosis:   then obtain the PEM for: %s", leafIssuer)
		}
	}
}

// ─── ANSI colour helpers ──────────────────────────────────────────────────────

const (
	colorRed    = "\033[0;31m"
	colorGreen  = "\033[0;32m"
	colorYellow = "\033[1;33m"
	colorBlue   = "\033[0;34m"
	colorReset  = "\033[0m"
)

func logInfo(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "%s[INFO]%s "+format+"\n", append([]any{colorBlue, colorReset}, a...)...)
}
func logSuccess(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "%s[SUCCESS]%s "+format+"\n", append([]any{colorGreen, colorReset}, a...)...)
}
func logWarning(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "%s[WARNING]%s "+format+"\n", append([]any{colorYellow, colorReset}, a...)...)
}
func logError(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "%s[ERROR]%s "+format+"\n", append([]any{colorRed, colorReset}, a...)...)
}
func logDebug(debug bool, format string, a ...any) {
	if debug {
		fmt.Fprintf(os.Stderr, "%s[DEBUG]%s "+format+"\n", append([]any{colorYellow, colorReset}, a...)...)
	}
}

// ─── CLI arguments ────────────────────────────────────────────────────────────

// Args holds all command-line arguments after parsing.
type Args struct {
	ServerArg  string        // raw server argument (bootstrap, master-0, worker-1, or custom)
	Cloud      string        // OpenStack cloud name from clouds.yaml
	ClusterDir string        // directory containing metadata.json
	Duration   time.Duration // how long to capture console output
	Debug      bool          // verbose debug output
	Nudge      bool          // send Enter to the console to elicit a prompt
}

func showUsage() {
	fmt.Fprintf(os.Stderr, `Usage: GrabConsole [OPTIONS] <server-name>

Connect to the serial console of an OpenShift cluster node via PowerVC HMC/PowerVM.

Arguments:
  server-name   Infrastructure node: bootstrap, master-N, worker-N
                Custom server: any other name (requires --cloud)

Options:
  --cloud <name>       OpenStack cloud from clouds.yaml (env: CLOUD)
  --cluster-dir <dir>  Directory with metadata.json     (env: CLUSTER_DIR, default: test)
  --duration <dur>     Capture duration                 (default: 30s)
  --nudge              Send Enter to the console after connecting to elicit a prompt
  --debug              Enable debug output              (env: DEBUG=true)
  -h, --help           Show this message

Environment Variables:
  SSH_PASSWORDS   Space-separated list of passwords to try for HMC/PowerVM SSH
                  e.g.: export SSH_PASSWORDS="pass1 2word example3"
  TOKEN_ID        Reuse an existing PowerVC auth token (skips token acquisition)

Examples:
  GrabConsole bootstrap
  GrabConsole --duration 60s master-0
  GrabConsole --cloud mycloud --cluster-dir prod worker-1
  CLOUD=mycloud GrabConsole my-custom-server
`)
}

// parseArgs parses os.Args and returns a populated Args struct.
func parseArgs() (*Args, error) {
	fs := flag.NewFlagSet("GrabConsole", flag.ContinueOnError)
	fs.Usage = showUsage

	defaultClusterDir := "test"
	if v := os.Getenv("CLUSTER_DIR"); v != "" {
		defaultClusterDir = v
	}
	defaultCloud := os.Getenv("CLOUD")
	defaultDebug := strings.EqualFold(os.Getenv("DEBUG"), "true")

	cloud      := fs.String("cloud", defaultCloud, "OpenStack cloud name")
	clusterDir := fs.String("cluster-dir", defaultClusterDir, "Cluster directory")
	duration   := fs.Duration("duration", 30*time.Second, "Console capture duration")
	nudge      := fs.Bool("nudge", false, "Send Enter to the console to elicit a prompt")
	debug      := fs.Bool("debug", defaultDebug, "Enable debug output")
	help       := fs.Bool("help", false, "Show usage")

	for _, a := range os.Args[1:] {
		if a == "-h" {
			showUsage()
			os.Exit(0)
		}
	}

	if err := fs.Parse(os.Args[1:]); err != nil {
		return nil, err
	}
	if *help {
		showUsage()
		os.Exit(0)
	}
	if fs.NArg() == 0 {
		showUsage()
		return nil, errors.New("server-name argument is required")
	}

	return &Args{
		ServerArg:  fs.Arg(0),
		Cloud:      *cloud,
		ClusterDir: *clusterDir,
		Duration:   *duration,
		Debug:      *debug,
		Nudge:      *nudge,
	}, nil
}

// isInfraNode returns true for the standard installer-managed node names.
func isInfraNode(name string) bool {
	return name == "bootstrap" ||
		strings.HasPrefix(name, "master-") ||
		strings.HasPrefix(name, "worker-")
}

// ─── metadata.json ────────────────────────────────────────────────────────────

// clusterMetadata is the subset of metadata.json we need.
type clusterMetadata struct {
	InfraID   string `json:"infraID"`
	OpenStack struct {
		Cloud string `json:"cloud"`
	} `json:"openstack"`
}

// readMetadata parses CLUSTER_DIR/metadata.json and returns infraID and cloud name.
func readMetadata(clusterDir string) (infraID, cloud string, err error) {
	path := filepath.Join(clusterDir, "metadata.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", fmt.Errorf("cannot read %s: %w", path, err)
	}
	var m clusterMetadata
	if err := json.Unmarshal(data, &m); err != nil {
		return "", "", fmt.Errorf("cannot parse %s: %w", path, err)
	}
	if m.InfraID == "" {
		return "", "", fmt.Errorf("infraID is empty in %s", path)
	}
	if m.OpenStack.Cloud == "" {
		return "", "", fmt.Errorf("openstack.cloud is empty in %s", path)
	}
	return m.InfraID, m.OpenStack.Cloud, nil
}

// ─── clouds.yaml parsing ──────────────────────────────────────────────────────

// cloudAuth holds the credentials for one cloud entry.
type cloudAuth struct {
	AuthURL     string `yaml:"auth_url"`
	Username    string `yaml:"username"`
	Password    string `yaml:"password"`
	ProjectID   string `yaml:"project_id"`
	ProjectName string `yaml:"project_name"`
}

// cloudsYAML mirrors the top-level structure of ~/.config/openstack/clouds.yaml.
type cloudsYAML struct {
	Clouds map[string]struct {
		Auth    cloudAuth `yaml:"auth"`
		CACert  string    `yaml:"cacert"`
	} `yaml:"clouds"`
}

// cloudsYAMLPath returns the resolved path to clouds.yaml.
//
// We use os/user.Current().HomeDir rather than os.Getenv("HOME") so that the
// path matches what gophercloud/utils clientconfig.FindAndReadCloudsYAML uses
// internally.  If $HOME and the real home directory differ (e.g. the process
// was started with a modified environment), using os.Getenv("HOME") here would
// let our readCloudsYAML succeed while clientconfig.AuthOptions would still
// fail with "no clouds.yml file found" because its independent search never
// finds the file.
func cloudsYAMLPath() (string, error) {
	u, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("cannot determine current user home directory: %w", err)
	}
	return filepath.Join(u.HomeDir, ".config", "openstack", "clouds.yaml"), nil
}

// readCloudsYAML parses clouds.yaml and returns the auth block for the named
// cloud, the resolved file path, and the CA cert path from the cloud entry's
// "cacert" field (empty string when absent).
func readCloudsYAML(cloud string) (auth cloudAuth, caCert string, resolvedPath string, err error) {
	path, err := cloudsYAMLPath()
	if err != nil {
		return cloudAuth{}, "", "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return cloudAuth{}, "", "", fmt.Errorf("cannot read %s: %w", path, err)
	}
	var cy cloudsYAML
	if err := yaml.Unmarshal(data, &cy); err != nil {
		return cloudAuth{}, "", "", fmt.Errorf("cannot parse %s: %w", path, err)
	}
	entry, ok := cy.Clouds[cloud]
	if !ok {
		return cloudAuth{}, "", "", fmt.Errorf("cloud %q not found in %s", cloud, path)
	}
	a := entry.Auth
	switch {
	case a.AuthURL == "":
		return cloudAuth{}, "", "", fmt.Errorf("auth_url missing for cloud %q in %s", cloud, path)
	case a.Username == "":
		return cloudAuth{}, "", "", fmt.Errorf("username missing for cloud %q in %s", cloud, path)
	case a.Password == "":
		return cloudAuth{}, "", "", fmt.Errorf("password missing for cloud %q in %s", cloud, path)
	case a.ProjectID == "":
		return cloudAuth{}, "", "", fmt.Errorf("project_id missing for cloud %q in %s", cloud, path)
	}
	return a, entry.CACert, path, nil
}

// powervcHost extracts the hostname from an auth_url such as
// "https://10.20.27.162:5000/v3" using the standard net/url parser.
func powervcHost(authURL string) (string, error) {
	u, err := url.Parse(authURL)
	if err != nil {
		return "", fmt.Errorf("cannot parse auth_url %q: %w", authURL, err)
	}
	host := u.Hostname() // strips port
	if host == "" {
		return "", fmt.Errorf("cannot extract host from auth_url %q", authURL)
	}
	return host, nil
}

// ─── OpenStack (gophercloud) ──────────────────────────────────────────────────

// serverDetails holds the fields we need from `openstack server show`.
type serverDetails struct {
	HypervisorHostname string
	InstanceName       string
}

// serverExtAttrs is a minimal flat struct used to decode only the two
// OS-EXT-SRV-ATTR fields we need from the raw server JSON object.
// We deliberately do NOT embed servers.Server here: servers.Server has a
// custom UnmarshalJSON that only knows its own fields, so embedding it would
// prevent the JSON decoder from ever populating HypervisorHostname /
// InstanceName on this outer struct.
type serverExtAttrs struct {
	HypervisorHostname string `json:"OS-EXT-SRV-ATTR:hypervisor_hostname"`
	InstanceName       string `json:"OS-EXT-SRV-ATTR:instance_name"`
}

// getServerDetails queries the OpenStack compute API for the named server and
// returns its hypervisor hostname and instance name.
//
// hc must be the client returned by newHTTPClient so that the CA certificate
// (or InsecureSkipVerify fallback) is in effect for every outbound TLS
// connection, including the very first GET to the Keystone identity endpoint.
//
// cloudsYAMLFile is the resolved absolute path to clouds.yaml as returned by
// readCloudsYAML.  It is exported to the environment as OS_CLIENT_CONFIG_FILE
// before calling clientconfig.AuthOptions so that the library's internal
// FindAndReadCloudsYAML search uses the exact same file we already parsed.
//
// # Why OS_CLIENT_CONFIG_FILE must be set
//
// clientconfig.AuthOptions calls FindAndReadCloudsYAML, which searches:
//
//  1. $OS_CLIENT_CONFIG_FILE  (checked first — this is what we set)
//  2. ./clouds.yaml  (current working directory)
//  3. ~/.config/openstack/clouds.yaml  (via user.Current().HomeDir)
//  4. /etc/openstack/clouds.yaml
//
// Our own readCloudsYAML uses user.Current().HomeDir, which matches search
// path 3 above.  However, if the library reaches path 3 and still does not
// find the file — for example because the CWD search (path 2) contains a
// stale or misnamed file — it falls through to try clouds.yml, producing the
// misleading error:
//
//	unable to load clouds.yaml: no clouds.yml file found: file does not exist
//
// Setting OS_CLIENT_CONFIG_FILE to the absolute path we already resolved
// short-circuits the search at step 1 and guarantees both code paths read
// identical files.
//
// # Why we do NOT use clientconfig.AuthenticatedClient
//
// The obvious call would be:
//
//	clientconfig.AuthenticatedClient(ctx, &clientconfig.ClientOpts{
//	    Cloud:      cloud,
//	    HTTPClient: hc,   // ← looks right, but is silently ignored
//	})
//
// However, clientconfig.AuthenticatedClient is implemented as:
//
//	func AuthenticatedClient(ctx, opts) (*ProviderClient, error) {
//	    ao, _ := AuthOptions(opts)
//	    return openstack.AuthenticatedClient(ctx, *ao)  // opts dropped here
//	}
//
// The ClientOpts (including HTTPClient) are discarded before
// openstack.AuthenticatedClient is called, which constructs its own default
// transport from scratch.  The result is that the private-CA cert we loaded
// is never used for the Keystone GET, causing:
//
//	tls: failed to verify certificate: x509: certificate signed by unknown authority
//
// # Fix
//
// We replicate what clientconfig.NewServiceClient does internally — the steps
// that keep opts alive all the way through authentication:
//
//  1. os.Setenv OS_CLIENT_CONFIG_FILE   — pin the file for clientconfig's search
//  2. clientconfig.AuthOptions          — translate clouds.yaml into AuthOptions
//  3. openstack.NewClient               — create a blank ProviderClient (no I/O yet)
//  4. providerClient.HTTPClient = *hc   — inject our CA-cert client before any I/O
//  5. openstack.Authenticate            — POST /v3/auth/tokens using our client
func getServerDetails(ctx context.Context, cloud, serverName, cloudsYAMLFile string, hc *http.Client, debug bool) (serverDetails, error) {
	logDebug(debug, "querying OpenStack cloud=%q server=%q", cloud, serverName)

	// Pin the file path so clientconfig.AuthOptions uses exactly the same file
	// as readCloudsYAML — see function doc for why this is necessary.
	if err := os.Setenv("OS_CLIENT_CONFIG_FILE", cloudsYAMLFile); err != nil {
		return serverDetails{}, fmt.Errorf("failed to set OS_CLIENT_CONFIG_FILE: %w", err)
	}

	opts := &clientconfig.ClientOpts{Cloud: cloud}
	ao, err := clientconfig.AuthOptions(opts)
	if err != nil {
		return serverDetails{}, fmt.Errorf("failed to build OpenStack auth options: %w", err)
	}

	// Create the provider client without triggering any network I/O, then
	// inject hc so that the subsequent Authenticate call uses our transport.
	providerClient, err := openstack.NewClient(ao.IdentityEndpoint)
	if err != nil {
		return serverDetails{}, fmt.Errorf("failed to create OpenStack provider client: %w", err)
	}
	providerClient.HTTPClient = *hc // must be set before Authenticate makes any TLS connections

	// When debug is on, make a raw TLS dial to the identity endpoint before
	// the real auth call so we can log exactly what certificate chain the
	// server is presenting.  This lets us confirm whether the CA in the cert
	// pool actually signs the leaf cert the server sends, or whether the chain
	// is broken (missing intermediate, wrong CA, SNI mismatch, etc.).
	if debug {
		debugLogServerChain(ao.IdentityEndpoint, hc)
	}

	if err := openstack.Authenticate(ctx, providerClient, *ao); err != nil {
		return serverDetails{}, fmt.Errorf("OpenStack authentication failed: %w", err)
	}

	computeClient, err := openstack.NewComputeV2(providerClient, gophercloud.EndpointOpts{})
	if err != nil {
		return serverDetails{}, fmt.Errorf("failed to create compute client: %w", err)
	}

	// List to find the server ID by exact name match (ListOpts.Name is a
	// server-side substring/regex filter, so we confirm locally).
	allPages, err := servers.List(computeClient, servers.ListOpts{Name: serverName}).AllPages(ctx)
	if err != nil {
		return serverDetails{}, fmt.Errorf("failed to list servers: %w", err)
	}
	allServers, err := servers.ExtractServers(allPages)
	if err != nil {
		return serverDetails{}, fmt.Errorf("failed to extract server list: %w", err)
	}

	var serverID string
	for i := range allServers {
		if allServers[i].Name == serverName {
			serverID = allServers[i].ID
			break
		}
	}
	if serverID == "" {
		return serverDetails{}, fmt.Errorf("server %q not found in cloud %q", serverName, cloud)
	}
	logDebug(debug, "found server ID=%s", serverID)

	// Fetch full server details by ID so that OS-EXT-SRV-ATTR fields are
	// present.  We decode the raw Result.Body ourselves rather than going
	// through ExtractInto: servers.Server has a custom UnmarshalJSON that
	// would shadow our extra fields if we embedded it.
	getResult := servers.Get(ctx, computeClient, serverID)
	if getResult.Err != nil {
		return serverDetails{}, fmt.Errorf("cannot get server details: %w", getResult.Err)
	}
	// Result.Body is map[string]any{"server": map[string]any{...}}.
	bodyMap, ok := getResult.Body.(map[string]any)
	if !ok {
		return serverDetails{}, fmt.Errorf("unexpected server response body type %T", getResult.Body)
	}
	serverObj, ok := bodyMap["server"]
	if !ok {
		return serverDetails{}, fmt.Errorf("no \"server\" key in server response body")
	}
	// Re-marshal just the server object then unmarshal into our flat struct.
	raw, err := json.Marshal(serverObj)
	if err != nil {
		return serverDetails{}, fmt.Errorf("cannot re-marshal server object: %w", err)
	}
	var s serverExtAttrs
	if err := json.Unmarshal(raw, &s); err != nil {
		return serverDetails{}, fmt.Errorf("cannot decode server ext attrs: %w", err)
	}

	if s.HypervisorHostname == "" {
		return serverDetails{}, fmt.Errorf("OS-EXT-SRV-ATTR:hypervisor_hostname is empty for server %q", serverName)
	}
	if s.InstanceName == "" {
		return serverDetails{}, fmt.Errorf("OS-EXT-SRV-ATTR:instance_name is empty for server %q", serverName)
	}
	logDebug(debug, "hypervisor=%s instanceName=%s", s.HypervisorHostname, s.InstanceName)
	return serverDetails{
		HypervisorHostname: s.HypervisorHostname,
		InstanceName:       s.InstanceName,
	}, nil
}

// ─── PowerVC REST API ─────────────────────────────────────────────────────────

// tokenRequestBody is the typed representation of the Keystone v3 token request.
type tokenRequestBody struct {
	Auth tokenAuth `json:"auth"`
}

type tokenAuth struct {
	Identity tokenIdentity `json:"identity"`
	Scope    tokenScope    `json:"scope"`
}

type tokenIdentity struct {
	Methods  []string          `json:"methods"`
	Password tokenPasswordAuth `json:"password"`
}

type tokenPasswordAuth struct {
	User tokenUser `json:"user"`
}

type tokenUser struct {
	Name     string       `json:"name"`
	Password string       `json:"password"`
	Domain   tokenDomain  `json:"domain"`
}

type tokenScope struct {
	Project tokenProject `json:"project"`
}

type tokenProject struct {
	Name   string      `json:"name"`
	Domain tokenDomain `json:"domain"`
}

type tokenDomain struct {
	Name string `json:"name"`
}

// getToken obtains a scoped Keystone token from PowerVC and returns it.
// If the TOKEN_ID environment variable is already set, that value is reused.
func getToken(hc *http.Client, serverIP, projectName, username, password string, debug bool) (string, error) {
	if existing := strings.TrimSpace(os.Getenv("TOKEN_ID")); existing != "" {
		logInfo("reusing existing TOKEN_ID from environment")
		return existing, nil
	}

	logInfo("obtaining PowerVC authentication token…")

	body := tokenRequestBody{
		Auth: tokenAuth{
			Identity: tokenIdentity{
				Methods: []string{"password"},
				Password: tokenPasswordAuth{
					User: tokenUser{
						Name:     username,
						Password: password,
						Domain:   tokenDomain{Name: "Default"},
					},
				},
			},
			Scope: tokenScope{
				Project: tokenProject{
					Name:   projectName,
					Domain: tokenDomain{Name: "Default"},
				},
			},
		},
	}

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("cannot marshal token request: %w", err)
	}
	// Zero the serialised body (which contains the password) as soon as it is
	// no longer needed, to minimise its lifetime in heap memory.
	defer func() {
		for i := range bodyBytes {
			bodyBytes[i] = 0
		}
	}()

	tokenURL := fmt.Sprintf("https://%s:5000/v3/auth/tokens", serverIP)
	logDebug(debug, "POST %s", tokenURL)

	req, err := http.NewRequest(http.MethodPost, tokenURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("cannot create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("token request failed: %w", err)
	}
	defer func() {
		// Drain and close so the pooled TCP connection can be reused.
		io.Copy(io.Discard, resp.Body) //nolint:errcheck
		resp.Body.Close()
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("token request returned HTTP %d", resp.StatusCode)
	}

	token := strings.TrimSpace(resp.Header.Get("X-Subject-Token"))
	if token == "" {
		return "", fmt.Errorf("X-Subject-Token header missing in response")
	}
	logDebug(debug, "token obtained")
	logSuccess("PowerVC authentication successful")
	return token, nil
}

// hypervisorInfo holds what we need from /os-hosts/<hostname>.
type hypervisorInfo struct {
	ManagerType     string // "hmc" or "pvm"
	PrimaryHMCUUID  string // only set when ManagerType == "hmc"
	UserID          string // only set when ManagerType == "pvm"
	AccessIP        string // only set when ManagerType == "pvm"
	HostDisplayName string
}

// getHypervisorInfo queries the PowerVC /os-hosts/<hypervisor> endpoint and
// extracts registration details.
func getHypervisorInfo(hc *http.Client, serverIP, projectID, token, hypervisor string, debug bool) (hypervisorInfo, error) {
	hvURL := fmt.Sprintf("https://%s:8774/v2.1/%s/os-hosts/%s", serverIP, projectID, hypervisor)
	logDebug(debug, "GET %s", hvURL)

	req, err := http.NewRequest(http.MethodGet, hvURL, nil)
	if err != nil {
		return hypervisorInfo{}, fmt.Errorf("cannot create hypervisor request: %w", err)
	}
	req.Header.Set("X-Auth-Token", token)

	resp, err := hc.Do(req)
	if err != nil {
		return hypervisorInfo{}, fmt.Errorf("hypervisor query failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return hypervisorInfo{}, fmt.Errorf("hypervisor query returned HTTP %d", resp.StatusCode)
	}

	// Cap the response body to 1 MiB to guard against unexpectedly large payloads.
	const maxBody = 1 << 20 // 1 MiB
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return hypervisorInfo{}, fmt.Errorf("cannot read hypervisor response: %w", err)
	}

	// The response is {"host": [{"resource": {...}}, {"registration": {...}}]}
	var raw struct {
		Host []map[string]json.RawMessage `json:"host"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return hypervisorInfo{}, fmt.Errorf("cannot parse hypervisor response: %w", err)
	}

	// Find the registration entry (the one with a non-empty "registration" key).
	type regBlock struct {
		ManagerType     string `json:"manager_type"`
		PrimaryHMCUUID  string `json:"primary_hmc_uuid"`
		UserID          string `json:"user_id"`
		AccessIP        string `json:"access_ip"`
		HostDisplayName string `json:"host_display_name"`
	}
	var reg *regBlock
	for _, entry := range raw.Host {
		regRaw, ok := entry["registration"]
		if !ok || string(regRaw) == "null" || string(regRaw) == "{}" {
			continue
		}
		var rb regBlock
		if err := json.Unmarshal(regRaw, &rb); err != nil {
			return hypervisorInfo{}, fmt.Errorf("cannot parse registration block: %w", err)
		}
		reg = &rb
		break
	}
	if reg == nil {
		return hypervisorInfo{}, fmt.Errorf("no registration block found for hypervisor %q", hypervisor)
	}
	if reg.ManagerType == "" {
		return hypervisorInfo{}, fmt.Errorf("manager_type empty for hypervisor %q", hypervisor)
	}
	if reg.HostDisplayName == "" {
		return hypervisorInfo{}, fmt.Errorf("host_display_name empty for hypervisor %q", hypervisor)
	}

	logDebug(debug, "manager_type=%s host_display_name=%s", reg.ManagerType, reg.HostDisplayName)

	return hypervisorInfo{
		ManagerType:     reg.ManagerType,
		PrimaryHMCUUID:  reg.PrimaryHMCUUID,
		UserID:          reg.UserID,
		AccessIP:        reg.AccessIP,
		HostDisplayName: reg.HostDisplayName,
	}, nil
}

// getHMCAccessIP queries /ibm-hmcs/<uuid> and returns the HMC's access IP.
func getHMCAccessIP(hc *http.Client, serverIP, projectID, token, hmcUUID string, debug bool) (string, error) {
	hmcURL := fmt.Sprintf("https://%s:8774/v2.1/%s/ibm-hmcs/%s", serverIP, projectID, hmcUUID)
	logDebug(debug, "GET %s", hmcURL)

	req, err := http.NewRequest(http.MethodGet, hmcURL, nil)
	if err != nil {
		return "", fmt.Errorf("cannot create HMC request: %w", err)
	}
	req.Header.Set("X-Auth-Token", token)

	resp, err := hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("HMC query failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("HMC query returned HTTP %d", resp.StatusCode)
	}

	var raw struct {
		HMC struct {
			Registration struct {
				AccessIP string `json:"access_ip"`
			} `json:"registration"`
		} `json:"hmc"`
	}
	// Cap the response body to 1 MiB to guard against unexpectedly large payloads.
	const maxBody = 1 << 20 // 1 MiB
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&raw); err != nil {
		return "", fmt.Errorf("cannot parse HMC response: %w", err)
	}
	if raw.HMC.Registration.AccessIP == "" {
		return "", fmt.Errorf("access_ip empty in HMC response for UUID %q", hmcUUID)
	}
	logDebug(debug, "HMC access_ip=%s", raw.HMC.Registration.AccessIP)
	return raw.HMC.Registration.AccessIP, nil
}

// consoleTarget holds the resolved SSH target and mkvterm arguments.
type consoleTarget struct {
	SSHHost         string // IP of HMC or PowerVM host
	SSHUser         string // "hscroot" (HMC) or value from registration.user_id (pvm)
	HostDisplayName string // -m argument to mkvterm
	InstanceName    string // -p argument to mkvterm
}

// shellQuote returns s wrapped in single quotes safe for use in a POSIX shell
// command string. Any single-quote characters within s are escaped by ending
// the quoted segment, inserting an escaped single quote, then resuming quoting.
// Example: "LTC10U11-Ranier" → "'LTC10U11-Ranier'"
//
//	O'Brien → 'O'\''Brien'
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ─── SSH password-based console capture ──────────────────────────────────────

// sshPasswords returns the list of passwords to try from the SSH_PASSWORDS
// environment variable (space-separated) or an empty slice if unset.
func sshPasswords() []string {
	raw := strings.TrimSpace(os.Getenv("SSH_PASSWORDS"))
	if raw == "" {
		return nil
	}
	return strings.Fields(raw)
}

// dialSSHWithPassword attempts to open an SSH connection to addr using
// password authentication, trying each password in turn until one succeeds.
// Returns an error if all passwords fail.
func dialSSHWithPassword(addr, user string, passwords []string, debug bool) (*ssh.Client, error) {
	if len(passwords) == 0 {
		return nil, errors.New("SSH_PASSWORDS is not set — set it to a space-separated list of passwords to try")
	}

	for _, pw := range passwords {
		logDebug(debug, "trying SSH password auth as %s@%s", user, addr)

		cfg := &ssh.ClientConfig{
			User: user,
			Auth: []ssh.AuthMethod{ssh.Password(pw)},
			// The HMC/PowerVM host key is not known in advance; we use
			// InsecureIgnoreHostKey because there is no out-of-band way to
			// obtain the HMC key. This is consistent with console.sh's
			// ssh -o StrictHostKeyChecking=no behaviour.
			HostKeyCallback: ssh.InsecureIgnoreHostKey(), //nolint:gosec // HMC key not known in advance
			Timeout:         15 * time.Second,
		}
		client, err := ssh.Dial("tcp", addr, cfg)
		if err == nil {
			logDebug(debug, "password accepted")
			return client, nil
		}
		logDebug(debug, "password attempt failed: %v", err)
	}
	return nil, fmt.Errorf("all %d SSH password(s) failed for %s@%s", len(passwords), user, addr)
}

// runMkvterm opens an SSH session on client, runs
//
//	mkvterm -m '<hostDisplayName>' -p '<instanceName>'
//
// streams all output to stdout for duration, then closes the session.
func runMkvterm(client *ssh.Client, hostDisplayName, instanceName string, duration time.Duration, nudge, debug bool) error {
	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("cannot open SSH session: %w", err)
	}
	// Explicit close with drain below; no defer to avoid double-close.

	// mkvterm calls tcgetattr on its stdin to configure raw-terminal mode, so
	// the SSH session must have a PTY allocated; without one the remote stdin
	// is a plain pipe and tcgetattr returns ENOTTY ("Inappropriate ioctl for
	// device").  We request a minimal xterm PTY with a large window so that
	// mkvterm output is not wrapped or truncated.
	modes := ssh.TerminalModes{
		ssh.ECHO:          0,     // disable echo — we are only capturing output
		ssh.TTY_OP_ISPEED: 38400, // input baud
		ssh.TTY_OP_OSPEED: 38400, // output baud
	}
	if err := session.RequestPty("xterm", 50, 220, modes); err != nil {
		session.Close()
		return fmt.Errorf("cannot request PTY: %w", err)
	}

	// Shell-quote the arguments so that names containing spaces, apostrophes,
	// or other shell-special characters are passed to mkvterm verbatim.
	remoteCmd := fmt.Sprintf("mkvterm -m %s -p %s", shellQuote(hostDisplayName), shellQuote(instanceName))
	logDebug(debug, "remote command: %s", remoteCmd)

	stdoutPipe, err := session.StdoutPipe()
	if err != nil {
		session.Close()
		return fmt.Errorf("cannot get stdout pipe: %w", err)
	}
	stderrPipe, err := session.StderrPipe()
	if err != nil {
		session.Close()
		return fmt.Errorf("cannot get stderr pipe: %w", err)
	}
	stdinPipe, err := session.StdinPipe()
	if err != nil {
		session.Close()
		return fmt.Errorf("cannot get stdin pipe: %w", err)
	}

	if err := session.Start(remoteCmd); err != nil {
		session.Close()
		return fmt.Errorf("cannot start mkvterm: %w", err)
	}

	// If --nudge is set, wait briefly for mkvterm to establish the vterm
	// connection, then send a carriage return.  This causes the console's
	// login prompt (or shell prompt) to re-display itself in the capture.
	if nudge {
		logDebug(debug, "nudge: waiting 2s then sending CR to console")
		go func() {
			time.Sleep(2 * time.Second)
			_, _ = stdinPipe.Write([]byte("\r"))
		}()
	}

	// Stream both pipes to stdout concurrently.
	done := make(chan struct{}, 2)
	go func() { io.Copy(os.Stdout, stdoutPipe); done <- struct{}{} }()  //nolint:errcheck
	go func() { io.Copy(os.Stdout, stderrPipe); done <- struct{}{} }()  //nolint:errcheck

	// Wait for the capture window, then stop the remote process.
	timer := time.NewTimer(duration)
	defer timer.Stop()
	<-timer.C
	logDebug(debug, "duration elapsed — closing session")

	// Best-effort SIGTERM; mkvterm may not forward signals.
	_ = session.Signal(ssh.SIGTERM)
	session.Close()

	// Drain remaining output.
	<-done
	<-done

	return nil
}

// ─── main ─────────────────────────────────────────────────────────────────────

func main() {
	args, err := parseArgs()
	if err != nil {
		logError("%s", err)
		os.Exit(1)
	}

	logInfo("GrabConsole starting")
	logDebug(args.Debug, "args: server=%q cloud=%q clusterDir=%q duration=%s",
		args.ServerArg, args.Cloud, args.ClusterDir, args.Duration)

	// ── 0. Pre-flight: SSH_PASSWORDS must be set before doing any API work ────
	passwords := sshPasswords()
	if len(passwords) == 0 {
		logError("SSH_PASSWORDS is not set — set it to a space-separated list of passwords, e.g.:")
		logError(`  export SSH_PASSWORDS="pass1 2word example3"`)
		os.Exit(1)
	}

	// ── 1. Resolve cloud + server name ───────────────────────────────────────
	var serverName string

	if isInfraNode(args.ServerArg) {
		if _, err := os.Stat(args.ClusterDir); errors.Is(err, os.ErrNotExist) {
			logError("cluster directory %q does not exist", args.ClusterDir)
			os.Exit(1)
		}
		infraID, metaCloud, err := readMetadata(args.ClusterDir)
		if err != nil {
			logError("failed to read metadata: %v", err)
			os.Exit(1)
		}
		logSuccess("metadata: infraID=%s cloud=%s", infraID, metaCloud)
		if args.Cloud == "" {
			args.Cloud = metaCloud
		}
		serverName = infraID + "-" + args.ServerArg
	} else {
		if args.Cloud == "" {
			logError("--cloud (or CLOUD env var) is required for custom server names")
			os.Exit(1)
		}
		serverName = args.ServerArg
		logWarning("%q is not a standard node name — using as-is", args.ServerArg)
	}
	logSuccess("target server: %s (cloud: %s)", serverName, args.Cloud)

	// ── 2. Parse clouds.yaml for PowerVC credentials ─────────────────────────
	auth, caCertFile, cloudsYAMLFile, err := readCloudsYAML(args.Cloud)
	if err != nil {
		logError("failed to read clouds.yaml: %v", err)
		os.Exit(1)
	}
	logDebug(args.Debug, "clouds.yaml resolved path: %s", cloudsYAMLFile)
	serverIP, err := powervcHost(auth.AuthURL)
	if err != nil {
		logError("failed to extract PowerVC host: %v", err)
		os.Exit(1)
	}
	logSuccess("PowerVC host: %s", serverIP)

	// ── 2b. Build HTTP client with CA cert (or insecure fallback) ────────────
	hc, err := newHTTPClient(caCertFile, args.Debug)
	if err != nil {
		logError("failed to build HTTP client: %v", err)
		os.Exit(1)
	}
	if caCertFile != "" {
		logSuccess("TLS: using CA cert %s", caCertFile)
	}

	// ── 3. Query OpenStack for hypervisor + instance name ────────────────────
	ctx := context.Background()
	srvDetails, err := getServerDetails(ctx, args.Cloud, serverName, cloudsYAMLFile, hc, args.Debug)
	if err != nil {
		logError("failed to query server: %v", err)
		os.Exit(1)
	}
	logSuccess("hypervisor: %s  instance: %s", srvDetails.HypervisorHostname, srvDetails.InstanceName)

	// ── 4. Obtain PowerVC token ───────────────────────────────────────────────
	token, err := getToken(hc, serverIP, auth.ProjectName, auth.Username, auth.Password, args.Debug)
	if err != nil {
		logError("failed to get PowerVC token: %v", err)
		os.Exit(1)
	}

	// ── 5. Query hypervisor registration ─────────────────────────────────────
	hvInfo, err := getHypervisorInfo(hc, serverIP, auth.ProjectID, token, srvDetails.HypervisorHostname, args.Debug)
	if err != nil {
		logError("failed to query hypervisor: %v", err)
		os.Exit(1)
	}
	logSuccess("manager type: %s  host: %s", hvInfo.ManagerType, hvInfo.HostDisplayName)

	// ── 6. Resolve SSH target (HMC or PowerVM) ────────────────────────────────
	var target consoleTarget
	target.HostDisplayName = hvInfo.HostDisplayName
	target.InstanceName = srvDetails.InstanceName

	switch hvInfo.ManagerType {
	case "hmc":
		if hvInfo.PrimaryHMCUUID == "" {
			logError("primary_hmc_uuid is empty for HMC-managed hypervisor")
			os.Exit(1)
		}
		hmcIP, err := getHMCAccessIP(hc, serverIP, auth.ProjectID, token, hvInfo.PrimaryHMCUUID, args.Debug)
		if err != nil {
			logError("failed to get HMC access IP: %v", err)
			os.Exit(1)
		}
		target.SSHHost = hmcIP
		target.SSHUser = "hscroot"
	case "pvm":
		if hvInfo.AccessIP == "" {
			logError("access_ip is empty for PowerVM-managed hypervisor")
			os.Exit(1)
		}
		target.SSHHost = hvInfo.AccessIP
		target.SSHUser = hvInfo.UserID
	default:
		logError("unknown manager type %q", hvInfo.ManagerType)
		os.Exit(1)
	}

	logSuccess("console target: %s@%s", target.SSHUser, target.SSHHost)

	// ── 7. Connect via SSH with password retry ────────────────────────────────
	addr := net.JoinHostPort(target.SSHHost, "22")
	logInfo("connecting to %s@%s (trying %d password(s))…", target.SSHUser, addr, len(passwords))

	sshClient, err := dialSSHWithPassword(addr, target.SSHUser, passwords, args.Debug)
	if err != nil {
		logError("SSH connection failed: %v", err)
		os.Exit(1)
	}
	defer sshClient.Close()
	logSuccess("SSH connection established to %s@%s", target.SSHUser, target.SSHHost)

	// ── 8 & 9. Run mkvterm and capture output ─────────────────────────────────
	remoteCmd := fmt.Sprintf("mkvterm -m %s -p %s",
		shellQuote(target.HostDisplayName), shellQuote(target.InstanceName))
	logInfo("running: %s  (capturing for %s)", remoteCmd, args.Duration)
	fmt.Println()
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	if err := runMkvterm(sshClient, target.HostDisplayName, target.InstanceName, args.Duration, args.Nudge, args.Debug); err != nil {
		logError("mkvterm failed: %v", err)
		os.Exit(1)
	}
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println()
	logSuccess("console capture complete")
}

// Made with Bob
