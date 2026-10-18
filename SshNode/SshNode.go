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

// SshNode connects to an OpenShift cluster node via SSH, replacing
// scripts/ssh.sh.
//
// It resolves the target node's IP address by querying OpenStack directly via
// the gophercloud API and parses metadata.json with the standard library. By
// default it prints a ready-to-run shell command that opens an interactive
// SSH session as the `core` user with StrictHostKeyChecking=no. With --execute
// it establishes the SSH connection directly — no external `ssh`, `ssh-agent`,
// `jq`, `mktemp`, or `openstack` binary is required.
//
// # Usage
//
//	SshNode [OPTIONS] <server-name>
//
// # Arguments
//
//	server-name   Infrastructure node: bootstrap, master-N, worker-N
//	              (prefixed with infraID from metadata.json)
//	              Custom server: any other name (requires --cloud)
//
// # Options
//
//	--execute           Execute the SSH connection directly instead of printing it
//	--cloud <name>        OpenStack cloud from clouds.yaml (env: CLOUD); only used for
//	                      custom server names — metadata.json wins for cluster nodes
//	--cluster-dir <dir>   Directory with metadata.json (env: CLUSTER_DIR, default: test)
//	--ssh-key <path>      Private key path (default: ~/.ssh/id_installer_rsa)
//	--debug               Enable debug output (env: DEBUG=true)
//	-h, --help            Show this help message
//
// # Environment Variables
//
//	CLOUD       OpenStack cloud name (overridden by --cloud)
//	CLUSTER_DIR Cluster directory    (overridden by --cluster-dir)
//	DEBUG       Set to "true" to enable debug output
//
// # Design summary
//
// The following table maps each shell-script dependency to its Go replacement:
//
//	External program          Replaced by
//	────────────────────────  ─────────────────────────────────────────────
//	jq                        encoding/json  (stdlib)
//	mktemp                    Not needed — no temp files required
//	openstack CLI             github.com/gophercloud/gophercloud/v2
//	ssh                       golang.org/x/crypto/ssh
//	StrictHostKeyChecking=no  ssh.InsecureIgnoreHostKey()
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"
	"github.com/gophercloud/utils/v2/openstack/clientconfig"
	"golang.org/x/crypto/ssh"
	"golang.org/x/term"
	"gopkg.in/yaml.v3"
)

// ─── ANSI colour helpers ──────────────────────────────────────────────────────

const (
	colorRed    = "\033[0;31m"
	colorGreen  = "\033[0;32m"
	colorYellow = "\033[1;33m"
	colorBlue   = "\033[0;34m"
	colorReset  = "\033[0m"
)

func logInfo(msg string)    { fmt.Printf("%s[INFO]%s %s\n", colorBlue, colorReset, msg) }
func logSuccess(msg string) { fmt.Printf("%s[SUCCESS]%s %s\n", colorGreen, colorReset, msg) }
func logWarning(msg string) { fmt.Printf("%s[WARNING]%s %s\n", colorYellow, colorReset, msg) }
func logError(msg string)   { fmt.Fprintf(os.Stderr, "%s[ERROR]%s %s\n", colorRed, colorReset, msg) }

func logInfof(format string, args ...any) {
	fmt.Printf("%s[INFO]%s "+format+"\n", append([]any{colorBlue, colorReset}, args...)...)
}
func logSuccessf(format string, args ...any) {
	fmt.Printf("%s[SUCCESS]%s "+format+"\n", append([]any{colorGreen, colorReset}, args...)...)
}
func logWarningf(format string, args ...any) {
	fmt.Printf("%s[WARNING]%s "+format+"\n", append([]any{colorYellow, colorReset}, args...)...)
}
func logErrorf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "%s[ERROR]%s "+format+"\n", append([]any{colorRed, colorReset}, args...)...)
}
func logDebug(debug bool, msg string) {
	if debug {
		fmt.Printf("%s[DEBUG]%s %s\n", colorYellow, colorReset, msg)
	}
}
func logDebugf(debug bool, format string, args ...any) {
	if debug {
		fmt.Printf("%s[DEBUG]%s "+format+"\n", append([]any{colorYellow, colorReset}, args...)...)
	}
}

// ─── CLI arguments ────────────────────────────────────────────────────────────

// Args holds all command-line arguments after parsing.
type Args struct {
	ServerArg  string // raw server argument (bootstrap, master-0, worker-1, or custom)
	Execute    bool   // --execute: run SSH directly instead of printing the command
	Cloud      string // OpenStack cloud name from clouds.yaml (env: CLOUD)
	ClusterDir string // directory containing metadata.json (env: CLUSTER_DIR, default: test)
	SSHKey     string // path to private SSH key (default: ~/.ssh/id_installer_rsa)
	Debug      bool   // verbose debug output (env: DEBUG=true)
}

func showUsage() {
	fmt.Fprintf(os.Stderr, `Usage: SshNode [OPTIONS] <server-name>

Connect to an OpenShift cluster node via SSH.

Arguments:
  server-name   Infrastructure node: bootstrap, master-N, worker-N
                Custom server: any other name (requires --cloud)

Options:
  --execute           Execute the SSH connection directly instead of printing it
  --cloud <name>       OpenStack cloud from clouds.yaml (env: CLOUD; only used for
                       custom server names, metadata.json wins for cluster nodes)
  --cluster-dir <dir>  Directory with metadata.json     (env: CLUSTER_DIR, default: test)
  --ssh-key <path>     Private key path                 (default: ~/.ssh/id_installer_rsa)
  --debug              Enable debug output              (env: DEBUG=true)
  -h, --help           Show this message

Environment Variables:
  CLOUD       OpenStack cloud name (overridden by --cloud)
  CLUSTER_DIR Cluster directory    (overridden by --cluster-dir)
  DEBUG       Set to "true" to enable debug output

Examples:
  # Print SSH command for bootstrap node
  SshNode bootstrap

  # Connect directly to master node
  SshNode --execute master-0

  # Connect to worker node
  SshNode --execute worker-0

  # Connect to custom server
  CLOUD=mycloud SshNode --execute my-custom-server

  # Debug mode
  DEBUG=true SshNode bootstrap
`)
}

// parseArgs parses os.Args and returns a populated Args struct.
func parseArgs() (*Args, error) {
	fs := flag.NewFlagSet("SshNode", flag.ContinueOnError)
	fs.Usage = showUsage

	home, err := os.UserHomeDir()
	if err != nil {
		home = os.Getenv("HOME")
	}
	defaultSSHKey := filepath.Join(home, ".ssh", "id_installer_rsa")
	defaultClusterDir := "test"
	if v := os.Getenv("CLUSTER_DIR"); v != "" {
		defaultClusterDir = v
	}
	defaultCloud := os.Getenv("CLOUD")
	defaultDebug := strings.EqualFold(os.Getenv("DEBUG"), "true")

	execute := fs.Bool("execute", false, "Execute SSH connection directly instead of printing the command")
	cloud := fs.String("cloud", defaultCloud, "OpenStack cloud name (env: CLOUD)")
	clusterDir := fs.String("cluster-dir", defaultClusterDir, "Cluster directory with metadata.json (env: CLUSTER_DIR, default: test)")
	sshKey := fs.String("ssh-key", defaultSSHKey, "Private key path (default: ~/.ssh/id_installer_rsa)")
	debug := fs.Bool("debug", defaultDebug, "Enable debug output (env: DEBUG=true)")
	help := fs.Bool("help", false, "Show usage")

	// Support -h as an alias for --help before flag parsing strips it.
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
		Execute:    *execute,
		Cloud:      *cloud,
		ClusterDir: *clusterDir,
		SSHKey:     *sshKey,
		Debug:      *debug,
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

// ─── TLS / clouds.yaml helpers ───────────────────────────────────────────────

// cloudsYAML mirrors the minimal structure of ~/.config/openstack/clouds.yaml
// that we need: just the cacert field per cloud entry.
type cloudsYAML struct {
	Clouds map[string]struct {
		CACert string `yaml:"cacert"`
	} `yaml:"clouds"`
}

// readCACert returns the cacert path configured for the named cloud in
// ~/.config/openstack/clouds.yaml, or an empty string when the field is absent.
// Errors reading or parsing the file are returned so the caller can decide
// whether to abort or fall back.
func readCACert(cloud string) (string, error) {
	u, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	path := filepath.Join(u.HomeDir, ".config", "openstack", "clouds.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("cannot read %s: %w", path, err)
	}
	var cy cloudsYAML
	if err := yaml.Unmarshal(data, &cy); err != nil {
		return "", fmt.Errorf("cannot parse %s: %w", path, err)
	}
	entry, ok := cy.Clouds[cloud]
	if !ok {
		return "", fmt.Errorf("cloud %q not found in %s", cloud, path)
	}
	return entry.CACert, nil
}

// newHTTPClient returns an *http.Client whose TLS transport either trusts the
// CA certificate at caCertFile, or falls back to InsecureSkipVerify when
// caCertFile is empty (matching the behaviour of the original curl --insecure
// calls and keeping self-signed PowerVC deployments working).
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
		logDebugf(debug, "TLS: loaded CA cert pool from %s", caCertFile)
	} else {
		logWarning("no cacert found in clouds.yaml — falling back to InsecureSkipVerify")
		tlsCfg.InsecureSkipVerify = true //nolint:gosec // no CA cert available; mirrors original curl --insecure
	}
	return &http.Client{
		Transport: &http.Transport{TLSClientConfig: tlsCfg},
		Timeout:   30 * time.Second,
	}, nil
}

// ─── OpenStack ────────────────────────────────────────────────────────────────

// getServerIP queries OpenStack for the given server name and returns its first
// fixed IP address.
//
// # Why clientconfig.AuthenticatedClient is NOT used here
//
// clientconfig.AuthenticatedClient silently drops the HTTPClient field from
// ClientOpts before calling openstack.AuthenticatedClient, so any custom TLS
// transport (e.g. a private CA certificate) is never applied to the Keystone
// GET, causing:
//
//	tls: failed to verify certificate: x509: certificate signed by unknown authority
//
// Instead we follow the same pattern used by GrabConsole: call
// clientconfig.AuthOptions to build the auth parameters, create the provider
// client with openstack.NewClient (no I/O yet), inject the custom HTTP client,
// then call openstack.Authenticate so that all TLS connections use our transport.
func getServerIP(ctx context.Context, cloud, serverName string, hc *http.Client, debug bool) (string, error) {
	logDebugf(debug, "querying OpenStack cloud=%q server=%q", cloud, serverName)

	opts := &clientconfig.ClientOpts{Cloud: cloud}
	ao, err := clientconfig.AuthOptions(opts)
	if err != nil {
		return "", fmt.Errorf("failed to build OpenStack auth options: %w", err)
	}

	providerClient, err := openstack.NewClient(ao.IdentityEndpoint)
	if err != nil {
		return "", fmt.Errorf("failed to create OpenStack provider client: %w", err)
	}
	providerClient.HTTPClient = *hc // inject before any TLS connection is made

	if err := openstack.Authenticate(ctx, providerClient, *ao); err != nil {
		return "", fmt.Errorf("OpenStack authentication failed: %w", err)
	}

	computeClient, err := openstack.NewComputeV2(providerClient, gophercloud.EndpointOpts{})
	if err != nil {
		return "", fmt.Errorf("failed to create compute client: %w", err)
	}

	// List servers and find the one matching serverName.
	listOpts := servers.ListOpts{Name: serverName}
	allPages, err := servers.List(computeClient, listOpts).AllPages(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to list servers: %w", err)
	}

	allServers, err := servers.ExtractServers(allPages)
	if err != nil {
		return "", fmt.Errorf("failed to extract server list: %w", err)
	}

	// Find exact match (ListOpts.Name is a substring/regex filter on the API side).
	var target *servers.Server
	for i := range allServers {
		if allServers[i].Name == serverName {
			target = &allServers[i]
			break
		}
	}
	if target == nil {
		return "", fmt.Errorf("server %q not found in cloud %q", serverName, cloud)
	}

	logDebugf(debug, "found server ID=%s", target.ID)

	// Extract the first fixed IP from the addresses map.
	for _, addrList := range target.Addresses {
		list, ok := addrList.([]interface{})
		if !ok {
			continue
		}
		for _, entry := range list {
			addrMap, ok := entry.(map[string]interface{})
			if !ok {
				continue
			}
			if ip, ok := addrMap["addr"].(string); ok && ip != "" {
				logDebugf(debug, "resolved IP=%s", ip)
				return ip, nil
			}
		}
	}

	return "", fmt.Errorf("no IP address found for server %q", serverName)
}

// ─── SSH key validation & client construction ─────────────────────────────────

// checkSSHKeyPerms warns when the private key file does not have secure
// permissions (0600 or 0400), mirroring scripts/ssh.sh.
func checkSSHKeyPerms(path string) {
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	mode := info.Mode().Perm()
	if mode != 0600 && mode != 0400 {
		logWarningf("SSH key has insecure permissions %04o", mode)
		logWarningf("Consider running: chmod 600 %s", path)
	}
}

// buildSSHClient creates an *ssh.Client authenticated with the given private
// key, equivalent to `ssh-agent ssh -t -o StrictHostKeyChecking=no -i <sshKey> core@<ip>`.
func buildSSHClient(ip, sshKeyPath string, debug bool) (*ssh.Client, error) {
	keyBytes, err := os.ReadFile(sshKeyPath)
	if err != nil {
		return nil, fmt.Errorf("cannot read SSH key %s: %w", sshKeyPath, err)
	}

	signer, err := ssh.ParsePrivateKey(keyBytes)
	if err != nil {
		return nil, fmt.Errorf("cannot parse SSH key %s: %w", sshKeyPath, err)
	}

	cfg := &ssh.ClientConfig{
		User:            "core",
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), //nolint:gosec // equivalent to StrictHostKeyChecking=no
		Timeout:         15 * time.Second,
	}

	addr := net.JoinHostPort(ip, "22")
	logDebugf(debug, "dialing SSH %s", addr)
	client, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		return nil, fmt.Errorf("SSH dial to %s failed: %w", addr, err)
	}
	logSuccessf("SSH connection established to core@%s", ip)
	return client, nil
}

// ─── SSH command generation & execution ───────────────────────────────────────

// generateSSHCommand builds the shell command string in non-execute (print-only) mode.
// Equivalent to: ssh-agent ssh -t -o StrictHostKeyChecking=no -i "<sshKey>" core@<ip>
func generateSSHCommand(ip, sshKey string) string {
	return fmt.Sprintf("ssh-agent ssh -t -o StrictHostKeyChecking=no -i %q core@%s", sshKey, ip)
}

// escapeReader wraps an io.Reader and intercepts the SSH escape sequence
// "~." (tilde-dot) when it appears after a newline (or at the start of
// input), mirroring the behaviour of the openssh client.  On detection it
// closes the done channel so that the caller can tear down the session, and
// returns io.EOF to the copy loop so it stops gracefully.
//
// The recognised patterns are:
//
//	\r~.   — most common in raw-mode terminals (CR before tilde)
//	\n~.   — newline before tilde (e.g. piped input)
//	~.     — at the very start of the stream (afterNewline initialised true)
//
// A single "~" at a line start is held back (not forwarded) until the next
// byte arrives so we can decide whether to suppress it.  If the next byte is
// anything other than ".", the held tilde is forwarded first, followed by the
// non-dot byte on the next Read call.
type escapeReader struct {
	r            io.Reader
	done         chan struct{}
	afterNewline bool // true when the previous byte was \r, \n, or start-of-stream
	hasHeld      bool // true when heldByte contains a pending byte to emit
	heldByte     byte // byte to emit on the next Read before reading more input
}

func newEscapeReader(r io.Reader, done chan struct{}) *escapeReader {
	return &escapeReader{r: r, done: done, afterNewline: true}
}

// Read implements io.Reader.  It reads one byte at a time so that escape
// detection stays simple without a separate state machine buffer.
func (e *escapeReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	// Emit any byte held from a previous call first.
	if e.hasHeld {
		p[0] = e.heldByte
		e.hasHeld = false
		e.afterNewline = (e.heldByte == '\r' || e.heldByte == '\n')
		return 1, nil
	}

	var b [1]byte
	_, err := e.r.Read(b[:])
	if err != nil {
		return 0, err
	}
	ch := b[0]

	if e.afterNewline && ch == '~' {
		// Read one more byte to decide: escape or literal tilde?
		_, err := e.r.Read(b[:])
		if err != nil {
			// Stream ended while holding tilde — emit it with the error.
			p[0] = '~'
			e.afterNewline = false
			return 1, err
		}
		next := b[0]
		if next == '.' {
			// Escape sequence detected — signal the caller and stop.
			select {
			case <-e.done:
			default:
				close(e.done)
			}
			return 0, io.EOF
		}
		// Not an escape: emit the tilde now and hold `next` for the following Read.
		p[0] = '~'
		e.afterNewline = false
		e.hasHeld = true
		e.heldByte = next
		return 1, nil
	}

	e.afterNewline = (ch == '\r' || ch == '\n')
	p[0] = ch
	return 1, nil
}

// runInteractiveSSH establishes an interactive SSH shell on the remote host,
// equivalent to `ssh -t core@<ip>`. It allocates a PTY, puts the local
// terminal in raw mode so that Ctrl-C and other control characters are
// forwarded to the remote, and mirrors local terminal resize events.
//
// The SSH escape sequence "~." (typed after a newline) disconnects the
// session locally, matching the behaviour of the openssh client.
func runInteractiveSSH(client *ssh.Client, debug bool) error {
	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("cannot open SSH session: %w", err)
	}
	defer session.Close()

	fd := int(os.Stdin.Fd())

	// --execute mode requires an interactive terminal for raw-mode I/O.
	if !term.IsTerminal(fd) {
		return fmt.Errorf("stdin is not a terminal — --execute requires an interactive terminal")
	}

	// Determine terminal dimensions for PTY allocation.
	w, h, err := term.GetSize(fd)
	if err != nil {
		logDebugf(debug, "cannot get terminal size: %v \u2014 using default", err)
		w, h = 220, 80
	}

	// Allocate a PTY (equivalent to `ssh -t`).
	modes := ssh.TerminalModes{
		ssh.ECHO:          1, // enable echo
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}
	if err := session.RequestPty("xterm", h, w, modes); err != nil {
		return fmt.Errorf("cannot request PTY: %w", err)
	}

	// Put the local terminal in raw mode so control characters (Ctrl-C,
	// Ctrl-Z, etc.) are forwarded byte-for-byte to the remote shell.
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return fmt.Errorf("failed to switch terminal to raw mode: %w", err)
	}
	defer term.Restore(fd, oldState)

	// escapeDone is closed by escapeReader when "~." is detected.
	escapeDone := make(chan struct{})

	// Wire up standard I/O.  Stdin goes through the escape-sequence filter;
	// stdout and stderr are forwarded directly.
	stdinPR, stdinPW := io.Pipe()
	session.Stdin = stdinPR
	session.Stdout = os.Stdout
	session.Stderr = os.Stderr

	er := newEscapeReader(os.Stdin, escapeDone)

	// Copy filtered stdin into the session pipe.
	var stdinWg sync.WaitGroup
	stdinWg.Add(1)
	go func() {
		defer stdinWg.Done()
		defer stdinPW.Close()
		io.Copy(stdinPW, er) //nolint:errcheck // EOF / escape closes the pipe
	}()

	// If "~." is detected, close the session so session.Wait() unblocks.
	go func() {
		<-escapeDone
		session.Close()
	}()

	// Forward terminal-resize events (SIGWINCH) until the session ends.
	sigWinch := make(chan os.Signal, 1)
	signal.Notify(sigWinch, syscall.SIGWINCH)
	defer signal.Stop(sigWinch)
	winchDone := make(chan struct{})
	var winchWg sync.WaitGroup
	winchWg.Add(1)
	go func() {
		defer winchWg.Done()
		for {
			select {
			case <-sigWinch:
				w, h, err := term.GetSize(fd)
				if err == nil {
					_ = session.WindowChange(h, w)
				}
			case <-winchDone:
				return
			}
		}
	}()

	// Close winchDone only after the goroutine has exited to avoid a race
	// between channel close and a concurrent send on sigWinch.
	defer func() { close(winchDone); winchWg.Wait() }()

	logDebug(debug, "starting remote shell")
	if err := session.Shell(); err != nil {
		return fmt.Errorf("cannot start remote shell: %w", err)
	}

	// Wait for the remote shell to exit. ExitError is expected for interactive
	// sessions (user types "exit", presses Ctrl-D, ~., or is interrupted), so
	// we only surface genuine I/O / connection errors.
	waitErr := session.Wait()
	stdinWg.Wait() // drain the stdin goroutine before restoring the terminal

	// Check whether the session ended due to the escape sequence.
	select {
	case <-escapeDone:
		logDebug(debug, "session closed via escape sequence (~.)")
		return nil
	default:
	}

	if waitErr != nil {
		var exitErr *ssh.ExitError
		if errors.As(waitErr, &exitErr) {
			return nil // normal remote exit
		}
		return fmt.Errorf("SSH session ended: %w", waitErr)
	}
	return nil
}

// ─── main ─────────────────────────────────────────────────────────────────────

func main() {
	args, err := parseArgs()
	if err != nil {
		logError(err.Error())
		os.Exit(1)
	}

	logInfo("SshNode starting")
	logDebugf(args.Debug, "args: server=%q execute=%v cloud=%q clusterDir=%q sshKey=%q",
		args.ServerArg, args.Execute, args.Cloud, args.ClusterDir, args.SSHKey)

	// ── check required programs ──────────────────────────────────────────────
	// Unlike ssh.sh, all dependencies (jq, openstack CLI, ssh) are built into
	// this Go binary — no external programs needed.
	logInfo("Checking required programs...")
	logSuccess("All required programs are available (built into SshNode binary)")
	fmt.Println()

	// ── resolve cloud + server name ──────────────────────────────────────────
	var serverName string

	if isInfraNode(args.ServerArg) {
		// Need metadata.json to expand <infraID>-<role>.
		if _, err := os.Stat(args.ClusterDir); errors.Is(err, os.ErrNotExist) {
			logErrorf("cluster directory %q does not exist", args.ClusterDir)
			os.Exit(1)
		}
		infraID, metaCloud, err := readMetadata(args.ClusterDir)
		if err != nil {
			logErrorf("failed to read metadata: %v", err)
			os.Exit(1)
		}
		logSuccessf("metadata: infraID=%s cloud=%s", infraID, metaCloud)

		// The cluster's metadata.json is authoritative for the cloud; it
		// overrides --cloud / the CLOUD environment variable.
		if args.Cloud != "" && args.Cloud != metaCloud {
			logWarningf("ignoring cloud %q \u2014 using %q from %s/metadata.json", args.Cloud, metaCloud, args.ClusterDir)
		}
		args.Cloud = metaCloud
		serverName = infraID + "-" + args.ServerArg
	} else {
		// Custom server name — cloud must be supplied.
		if args.Cloud == "" {
			logError("--cloud (or CLOUD env var) is required for custom server names")
			os.Exit(1)
		}
		serverName = args.ServerArg
		logWarningf("%q is not a standard node name \u2014 using as-is", args.ServerArg)
	}

	logSuccessf("target server: %s (cloud: %s)", serverName, args.Cloud)

	// ── build TLS-aware HTTP client ──────────────────────────────────────────
	caCert, err := readCACert(args.Cloud)
	if err != nil {
		logDebugf(args.Debug, "could not read cacert from clouds.yaml: %v — proceeding with InsecureSkipVerify fallback", err)
		caCert = ""
	}
	hc, err := newHTTPClient(caCert, args.Debug)
	if err != nil {
		logErrorf("failed to build HTTP client: %v", err)
		os.Exit(1)
	}

	// ── query OpenStack for the server IP ────────────────────────────────────
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	ip, err := getServerIP(ctx, args.Cloud, serverName, hc, args.Debug)
	if err != nil {
		logErrorf("failed to get server IP: %v", err)
		os.Exit(1)
	}
	logSuccessf("server IP: %s", ip)

	// ── validate SSH key ─────────────────────────────────────────────────────
	if _, err := os.Stat(args.SSHKey); errors.Is(err, os.ErrNotExist) {
		logErrorf("SSH key not found: %s", args.SSHKey)
		os.Exit(1)
	}
	checkSSHKeyPerms(args.SSHKey)
	logSuccessf("SSH key: %s", args.SSHKey)

	// ── connect or generate command ──────────────────────────────────────────
	fmt.Println()
	logSuccess("SSH connection details retrieved successfully")
	fmt.Println()

	if args.Execute {
		// --execute mode: establish the SSH connection directly via Go's
		// SSH library (no external `ssh` binary required).
		logInfof("Executing SSH connection to %s (%s)...", serverName, ip)
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Println()

		sshClient, err := buildSSHClient(ip, args.SSHKey, args.Debug)
		if err != nil {
			logErrorf("SSH connection failed: %v", err)
			os.Exit(1)
		}
		defer sshClient.Close()

		if err := runInteractiveSSH(sshClient, args.Debug); err != nil {
			logErrorf("SSH session failed: %v", err)
			os.Exit(1)
		}
		fmt.Println()
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Println()
		logSuccess("SSH session ended")
	} else {
		// Default mode: print the shell command so the user can review and run it.
		logInfof("To connect to %s (%s), run the following command:", serverName, ip)
		fmt.Println()
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

		cmd := generateSSHCommand(ip, args.SSHKey)
		logDebugf(args.Debug, "command: %s", cmd)

		fmt.Println(cmd)
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Println()
		logInfo("Or run with --execute flag to connect directly:")
		fmt.Printf("  SshNode --execute %s\n", args.ServerArg)
	}
}

// Made with Bob
