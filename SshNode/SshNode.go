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
//	--cloud <name>        OpenStack cloud from clouds.yaml (env: CLOUD)
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
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
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
  --cloud <name>       OpenStack cloud from clouds.yaml (env: CLOUD)
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

// ─── OpenStack ────────────────────────────────────────────────────────────────

// getServerIP queries OpenStack for the given server name and returns its first
// fixed IP address.
func getServerIP(ctx context.Context, cloud, serverName string, debug bool) (string, error) {
	logDebugf(debug, "querying OpenStack cloud=%q server=%q", cloud, serverName)

	opts := &clientconfig.ClientOpts{Cloud: cloud}
	providerClient, err := clientconfig.AuthenticatedClient(ctx, opts)
	if err != nil {
		return "", fmt.Errorf("OpenStack authentication failed: %w", err)
	}

	// Set a timeout on the provider client HTTP client to prevent
	// indefinite hangs on unresponsive OpenStack API endpoints.
	providerClient.HTTPClient.Timeout = 30 * time.Second

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

// runInteractiveSSH establishes an interactive SSH shell on the remote host,
// equivalent to `ssh -t core@<ip>`. It allocates a PTY, puts the local
// terminal in raw mode so that Ctrl-C and other control characters are
// forwarded to the remote, and mirrors local terminal resize events.
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

	// Wire up standard I/O.
	session.Stdin = os.Stdin
	session.Stdout = os.Stdout
	session.Stderr = os.Stderr

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
	// sessions (user types "exit", presses Ctrl-D, or is interrupted), so we
	// only surface genuine I/O / connection errors.
	if err := session.Wait(); err != nil {
		var exitErr *ssh.ExitError
		if errors.As(err, &exitErr) {
			return nil // normal remote exit
		}
		return fmt.Errorf("SSH session ended: %w", err)
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

		// metadata.json's cloud takes precedence unless the user explicitly set --cloud.
		if args.Cloud == "" {
			args.Cloud = metaCloud
		}
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

	// ── query OpenStack for the server IP ────────────────────────────────────
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	ip, err := getServerIP(ctx, args.Cloud, serverName, args.Debug)
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
