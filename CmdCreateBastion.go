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

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"math"
	"net"
	"strings"
	"os"
	"os/exec"
	"path"
	"time"

	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/flavors"
	"github.com/gophercloud/gophercloud/v2/openstack/image/v2/images"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/networks"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/ports"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/keypairs"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"

	"github.com/IBM/networking-go-sdk/dnsrecordsv1"

	"k8s.io/apimachinery/pkg/util/wait"

	"k8s.io/utils/ptr"
)

const (
	bastionIpFilename     = "/tmp/bastionIp"
	defaultAvailZone      = "s1022"
	maxSSHRetries         = 10
	sshRetryDelay         = 15 * time.Second
	haproxyConfigPerms    = "646"
	haproxyConfigPath     = "/etc/haproxy/haproxy.cfg"
	haproxySelinuxSetting = "haproxy_connect_any"
)

var (
	enableHAProxy     = true
)

// BastionConfig holds all configuration for bastion creation
type BastionConfig struct {
	Cloud        string
	BastionName  string
	BastionRsa   string
	FlavorName   string
	ImageName    string
	NetworkName  string
	SshKeyName   string
	DomainName   string
	EnableHAP    bool
	ServerIP     string
	ShouldDebug  bool
}

// Validate checks if the configuration is valid
func (c *BastionConfig) Validate() error {
	// Required fields
	if c.Cloud == "" {
		return fmt.Errorf("cloud name is required")
	}
	if c.BastionName == "" {
		return fmt.Errorf("bastion name is required")
	}

	// Validate bastion name format (alphanumeric, hyphens, underscores)
	if !isValidResourceName(c.BastionName) {
		return fmt.Errorf("bastion name contains invalid characters: %s", c.BastionName)
	}

	// Mutual exclusivity check
	if c.BastionRsa == "" && c.ServerIP == "" {
		return fmt.Errorf("either bastion RSA key or server IP must be specified")
	}
	if c.BastionRsa != "" && c.ServerIP != "" {
		return fmt.Errorf("bastion RSA key and server IP are mutually exclusive")
	}

	// Validate file paths
	if c.BastionRsa != "" {
		if _, err := os.Stat(c.BastionRsa); err != nil {
			return fmt.Errorf("bastion RSA key file not found: %w", err)
		}
	}

	// Validate IP address format
	if c.ServerIP != "" {
		if net.ParseIP(c.ServerIP) == nil {
			return fmt.Errorf("invalid server IP address: %s", c.ServerIP)
		}
	}

	// Required OpenStack resources
	if c.FlavorName == "" {
		return fmt.Errorf("flavor name is required")
	}
	if c.ImageName == "" {
		return fmt.Errorf("image name is required")
	}
	if c.NetworkName == "" {
		return fmt.Errorf("network name is required")
	}
	if c.SshKeyName == "" {
		return fmt.Errorf("SSH key name is required")
	}

	return nil
}

// parseBastionFlags extracts and validates flags into a BastionConfig
func parseBastionFlags(flags *flag.FlagSet, args []string) (*BastionConfig, error) {
	config := &BastionConfig{}

	// Define flags
	cloud := flags.String("cloud", "", "The cloud to use in clouds.yaml")
	bastionName := flags.String("bastionName", "", "The name of the bastion VM")
	bastionRsa := flags.String("bastionRsa", "", "The RSA filename for the bastion VM")
	flavorName := flags.String("flavorName", "", "The name of the flavor")
	imageName := flags.String("imageName", "", "The name of the image")
	networkName := flags.String("networkName", "", "The name of the network")
	sshKeyName := flags.String("sshKeyName", "", "The name of the SSH keypair")
	domainName := flags.String("domainName", "", "The DNS domain (optional)")
	enableHAP := flags.String("enableHAProxy", "false", "Enable HA Proxy daemon")
	serverIP := flags.String("serverIP", "", "The IP address of the server")
	shouldDebug := flags.String("shouldDebug", "false", "Enable debug output")

	// Parse flags
	if err := flags.Parse(args); err != nil {
		return nil, fmt.Errorf("failed to parse flags: %w", err)
	}

	// Populate config
	config.Cloud = *cloud
	config.BastionName = *bastionName
	config.BastionRsa = *bastionRsa
	config.FlavorName = *flavorName
	config.ImageName = *imageName
	config.NetworkName = *networkName
	config.SshKeyName = *sshKeyName
	config.DomainName = *domainName
	config.ServerIP = *serverIP

	// Parse boolean flags
	var err error
	config.EnableHAP, err = parseBoolFlag(*enableHAP, "enableHAProxy")
	if err != nil {
		return nil, err
	}

	config.ShouldDebug, err = parseBoolFlag(*shouldDebug, "shouldDebug")
	if err != nil {
		return nil, err
	}

	// Validate configuration
	if err := config.Validate(); err != nil {
		return nil, err
	}

	return config, nil
}

func createBastionCommand(createBastionFlags *flag.FlagSet, args []string) error {
	// Print version info
	fmt.Fprintf(os.Stderr, "Program version is %v, release = %v\n", version, release)

	// Parse and validate configuration
	config, err := parseBastionFlags(createBastionFlags, args)
	if err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}

	// Initialize logger
	log = initLogger(config.ShouldDebug)

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()

	// Clean up previous bastion IP file
	if err := cleanupBastionIPFile(); err != nil {
		return fmt.Errorf("failed to cleanup bastion IP file: %w", err)
	}

	// Ensure server exists
	if err := ensureServerExists(ctx, config); err != nil {
		return fmt.Errorf("failed to ensure server exists: %w", err)
	}

	// Setup bastion server
	if err := setupBastion(ctx, config); err != nil {
		return fmt.Errorf("failed to setup bastion: %w", err)
	}

	// Write bastion IP to file
	if err := writeBastionIP(ctx, config.Cloud, config.BastionName); err != nil {
		return fmt.Errorf("failed to write bastion IP: %w", err)
	}

	return nil
}

// cleanupBastionIPFile removes the bastion IP file if it exists
func cleanupBastionIPFile() error {
	err := os.Remove(bastionIpFilename)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove bastion IP file: %w", err)
	}
	return nil
}

// ensureServerExists checks if server exists and creates it if needed
func ensureServerExists(ctx context.Context, config *BastionConfig) error {
	_, err := findServer(ctx, config.Cloud, config.BastionName)
	if err != nil {
		if errors.Is(err, ErrServerNotFound) {
			fmt.Printf("Server %s not found, creating...\n", config.BastionName)

			err = createServer(ctx,
				config.Cloud,
				config.FlavorName,
				config.ImageName,
				config.NetworkName,
				config.SshKeyName,
				config.BastionName,
				nil,
			)
			if err != nil {
				return fmt.Errorf("failed to create server: %w", err)
			}

			fmt.Println("Server created successfully!")
		} else {
			return fmt.Errorf("failed to find server: %w", err)
		}
	}

	// Verify server exists
	_, err = findServer(ctx, config.Cloud, config.BastionName)
	if err != nil {
		return fmt.Errorf("server verification failed: %w", err)
	}

	return nil
}

// setupBastion configures the bastion server either remotely or locally
func setupBastion(ctx context.Context, config *BastionConfig) error {
	if config.ServerIP != "" {
		fmt.Println("Setting up bastion remotely...")
		return sendCreateBastion(config.ServerIP, config.Cloud, config.BastionName, config.DomainName)
	}

	fmt.Println("Setting up bastion locally...")
	return setupBastionServer(ctx, config.Cloud, config.BastionName, config.DomainName, config.BastionRsa)
}

func createServer(ctx context.Context, cloudName string, flavorName string, imageName string, networkName string, sshKeyName string, bastionName string, userData []byte) error {
	var (
		flavor           flavors.Flavor
		image            images.Image
		network          networks.Network
		sshKeyPair       keypairs.KeyPair
		builder          ports.CreateOptsBuilder
		portCreateOpts   ports.CreateOpts
		portList         []servers.Network
		serverCreateOpts servers.CreateOptsBuilder
		newServer        *servers.Server
		err              error
	)

	flavor, err = findFlavor(ctx, cloudName, flavorName)
	if err != nil {
		return err
	}
	log.Debugf("createServer: flavor = %+v", flavor)

	image, err = findImage(ctx, cloudName, imageName)
	if err != nil {
		return err
	}
	log.Debugf("createServer: image = %+v", image)

	network, err = findNetwork(ctx, cloudName, networkName)
	if err != nil {
		return err
	}
	log.Debugf("createServer: network = %+v", network)

	if sshKeyName != "" {
		sshKeyPair, err = findKeyPair(ctx, cloudName, sshKeyName)
		if err != nil {
			return err
		}
		log.Debugf("createServer: sshKeyPair = %+v", sshKeyPair)
	}

	connNetwork, err := NewServiceClient(ctx, "network", DefaultClientOpts(cloudName))
	if err != nil {
		return err
	}
	fmt.Printf("createServer: connNetwork = %+v\n", connNetwork)

	portCreateOpts = ports.CreateOpts{
		Name:                  fmt.Sprintf("%s-port", bastionName),
		NetworkID:		network.ID,
		Description:           "hamzy test",
		AdminStateUp:          nil,
		MACAddress:            ptr.Deref(nil, ""),
		AllowedAddressPairs:   nil,
		ValueSpecs:            nil,
		PropagateUplinkStatus: nil,
	}

	builder = portCreateOpts
	log.Debugf("createServer: builder = %+v\n", builder)

	port, err := ports.Create(ctx, connNetwork, builder).Extract()
	if err != nil {
		return err
	}
	log.Debugf("createServer: port = %+v\n", port)
	log.Debugf("createServer: port.ID = %v\n", port.ID)

	connCompute, err := NewServiceClient(ctx, "compute", DefaultClientOpts(cloudName))
	if err != nil {
		return err
	}
	fmt.Printf("createServer: connCompute = %+v\n", connCompute)

	portList = []servers.Network{
		{ Port: port.ID, },
	}

	serverCreateOpts = servers.CreateOpts{
		AvailabilityZone: defaultAvailZone,
		FlavorRef:        flavor.ID,
		ImageRef:         image.ID,
		Name:             bastionName,
		Networks:         portList,
		UserData:         userData,
		// Additional properties are not allowed ('tags' was unexpected)
//		Tags:             tags[:],
//              KeyName:          "",
//
//		Metadata:         instanceSpec.Metadata,
//		ConfigDrive:      &instanceSpec.ConfigDrive,
//		BlockDevice:      blockDevices,
	}
	log.Debugf("createServer: serverCreateOpts = %+v\n", serverCreateOpts)

	if sshKeyName != "" {
		newServer, err = servers.Create(ctx,
			connCompute,
			keypairs.CreateOptsExt{
				CreateOptsBuilder: serverCreateOpts,
				KeyName:           sshKeyPair.Name,
			},
			nil).Extract()
	} else {
		newServer, err = servers.Create(ctx, connCompute, serverCreateOpts, nil).Extract()
	}
	if err != nil {
		return err
	}
	log.Debugf("createServer: newServer = %+v\n", newServer)

	err = waitForServer(ctx, cloudName, bastionName)
	log.Debugf("createServer: waitForServer = %v\n", err)
	if err != nil {
		return err
	}

	return err
}

func addServerKnownHosts(ctx context.Context, ipAddress string) error {
	var (
		homeDir    string
		knownHosts string
		outb       []byte
		outs       string
		err        error
	)

	homeDir, err = os.UserHomeDir()
	if err != nil {
		return err
	}
	log.Debugf("addServerKnownHosts: homeDir = %s", homeDir)

	knownHosts = path.Join(homeDir, ".ssh/known_hosts")
	log.Debugf("addServerKnownHosts: knownHosts = %s", knownHosts)

	// Remove ipAddress from known_hosts
	outb, err = runSplitCommand2([]string{
		"ssh-keygen",
		"-f",
		knownHosts,
		"-R",
		ipAddress,
	})
	outs = strings.TrimSpace(string(outb))
	log.Debugf("addServerKnownHosts: outs = \"%s\"", outs)

	outb, err = keyscanServer(ctx, ipAddress, false)
	if err != nil {
		return err
	}

	fileKnownHosts, err := os.OpenFile(knownHosts, os.O_APPEND|os.O_RDWR, 0644)
	if err != nil {
		return err
	}

	defer fileKnownHosts.Close()

	n, err := fileKnownHosts.Write(outb)
	if err != nil {
		return err
	}

	if n != len(outb) {
		return fmt.Errorf("Could not write entire data to known_hosts")
	}

	return nil
}

func setupBastionServer(ctx context.Context, cloudName string, serverName string, domainName string, bastionRsa string) error {
	var (
		server       servers.Server
		ipAddress    string
		outb         []byte
		outs         string
		exitError    *exec.ExitError
		apiKey       string
		err          error
	)

	server, err = findServer(ctx, cloudName, serverName)
	log.Debugf("setupBastionServer: server = %+v", server)
	if err != nil {
		return err
	}

	_, ipAddress, err = findIpAddress(server)
	if err != nil {
		return err
	}
	if ipAddress == "" {
		return fmt.Errorf("ip address is empty for server %s", server.Name)
	}

	log.Debugf("setupBastionServer: ipAddress = %s", ipAddress)
	log.Debugf("setupBastionServer: bastionRsa = %s", bastionRsa)

	fmt.Printf("Setting up server %s...\n", server.Name)

	if enableHAProxy {
		err = addServerKnownHosts(ctx, ipAddress)
		if err != nil {
			return err
		}

		for i := 0; i < maxSSHRetries; i++ {
			outb, err = runSplitCommand2([]string{
				"ssh",
				"-i",
				bastionRsa,
				fmt.Sprintf("cloud-user@%s", ipAddress),
				"echo",
				"ready",
			})
			outs = strings.TrimSpace(string(outb))
			log.Debugf("setupBastionServer: outs = \"%s\"", outs)
			if outs == "ready" {
				break
			} else if strings.Contains(outs, "Permission denied") {
				return fmt.Errorf("Error: ssh publickey Permission denied")
			}
			time.Sleep(sshRetryDelay)
		}
		if outs != "ready" {
			return fmt.Errorf("Error: HAProxy not ready in time")
		}

		outb, err = runSplitCommand2([]string{
			"ssh",
			"-i",
			bastionRsa,
			fmt.Sprintf("cloud-user@%s", ipAddress),
			"rpm",
			"-q",
			"haproxy",
		})
		outs = strings.TrimSpace(string(outb))
		log.Debugf("setupBastionServer: outs = \"%s\"", outs)
		if errors.As(err, &exitError) {
			log.Debugf("setupBastionServer: exitError.ExitCode() = %+v\n", exitError.ExitCode())

			if exitError.ExitCode() == 1 && outs == "package haproxy is not installed" {
				outb, err = runSplitCommand2([]string{
					"ssh",
					"-i",
					bastionRsa,
					fmt.Sprintf("cloud-user@%s", ipAddress),
					"sudo",
					"dnf",
					"install",
					"-y",
					"haproxy",
				})
				outs = strings.TrimSpace(string(outb))
				log.Debugf("setupBastionServer: outs = %s", outs)
				log.Debugf("setupBastionServer: err = %+v", err)
			}
		} else if err != nil {
			log.Debugf("setupBastionServer: err = %+v", err)
			return err
		}

		outb, err = runSplitCommand2([]string{
			"ssh",
			"-i",
			bastionRsa,
			fmt.Sprintf("cloud-user@%s", ipAddress),
			"sudo",
			"stat",
			"-c",
			"%a",
			haproxyConfigPath,
		})
		outs = strings.TrimSpace(string(outb))
		log.Debugf("setupBastionServer: outb = \"%s\"", outs)
		if err != nil {
			log.Debugf("setupBastionServer: err = %+v", err)
			return err
		}
		if outs != haproxyConfigPerms {
			outb, err = runSplitCommand2([]string{
				"ssh",
				"-i",
				bastionRsa,
				fmt.Sprintf("cloud-user@%s", ipAddress),
				"sudo",
				"chmod",
				haproxyConfigPerms,
				haproxyConfigPath,
			})
			outs = strings.TrimSpace(string(outb))
			log.Debugf("setupBastionServer: outb = \"%s\"", outs)
			if err != nil {
				log.Debugf("setupBastionServer: err = %+v", err)
				return err
			}
		}

		outb, err = runSplitCommand2([]string{
			"ssh",
			"-i",
			bastionRsa,
			fmt.Sprintf("cloud-user@%s", ipAddress),
			"sudo",
			"getsebool",
			haproxySelinuxSetting,
		})
		outs = strings.TrimSpace(string(outb))
		log.Debugf("setupBastionServer: outb = \"%s\"", outs)
		if err != nil {
			log.Debugf("setupBastionServer: err = %+v", err)
			return err
		}
		if outs != "haproxy_connect_any --> on" {
			outb, err = runSplitCommand2([]string{
				"ssh",
				"-i",
				bastionRsa,
				fmt.Sprintf("cloud-user@%s", ipAddress),
				"sudo",
				"setsebool",
				"-P",
				"haproxy_connect_any=1",
			})
			outs = strings.TrimSpace(string(outb))
			log.Debugf("setupBastionServer: outb = \"%s\"", outs)
			if err != nil {
				log.Debugf("setupBastionServer: err = %+v", err)
				return err
			}
		}

		outb, err = runSplitCommand2([]string{
			"ssh",
			"-i",
			bastionRsa,
			fmt.Sprintf("cloud-user@%s", ipAddress),
			"sudo",
			"systemctl",
			"enable",
			"haproxy.service",
		})
		outs = strings.TrimSpace(string(outb))
		log.Debugf("setupBastionServer: outb = \"%s\"", outs)
		if err != nil {
			log.Debugf("setupBastionServer: err = %+v", err)
			return err
		}

		outb, err = runSplitCommand2([]string{
			"ssh",
			"-i",
			bastionRsa,
			fmt.Sprintf("cloud-user@%s", ipAddress),
			"sudo",
			"systemctl",
			"start",
			"haproxy.service",
		})
		outs = strings.TrimSpace(string(outb))
		log.Debugf("setupBastionServer: outb = \"%s\"", outs)
		if err != nil {
			log.Debugf("setupBastionServer: err = %+v", err)
			return err
		}
	}

	// NOTE: This is optional
	apiKey = os.Getenv("IBMCLOUD_API_KEY")

	if apiKey != "" {
		err = dnsForServer(ctx, cloudName, apiKey, serverName, domainName)
		if err != nil {
			return err
		}
	} else {
		fmt.Println("Warning: IBMCLOUD_API_KEY not set.  Make sure DNS is supported via another way.")
	}

	return err
}

func writeBastionIP(ctx context.Context, cloudName string, serverName string) error {
	var (
		server       servers.Server
		ipAddress    string
		err          error
	)

	server, err = findServer(ctx, cloudName, serverName)
	log.Debugf("writeBastionIP: server = %+v", server)
	if err != nil {
		return err
	}

	_, ipAddress, err = findIpAddress(server)
	if err != nil {
		return err
	}
	if ipAddress == "" {
		return fmt.Errorf("ip address is empty for server %s", server.Name)
	}

	log.Debugf("writeBastionIP: ipAddress = %s", ipAddress)

	fileBastionIp, err := os.OpenFile(bastionIpFilename, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return err
	}

	fileBastionIp.Write([]byte(ipAddress))

	defer fileBastionIp.Close()

	return nil
}

func writeBastionIPToFile(ipAddress string) error {
	file, err := os.OpenFile(bastionIpFilename, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("failed to open bastion IP file: %w", err)
	}
	defer file.Close()

	if _, err := file.WriteString(ipAddress); err != nil {
		return fmt.Errorf("failed to write bastion IP: %w", err)
	}

	return nil
}

func removeCommentLines(input string) string {
	var (
		inputLines  []string
		resultLines []string
	)

	log.Debugf("removeCommentLines: input = \"%s\"", input)

	inputLines = strings.Split(input, "\n")

	for _, line := range inputLines {
		if !strings.HasPrefix(line, "#") {
			resultLines = append(resultLines, line)
		}
	}

	log.Debugf("removeCommentLines: resultLines = \"%s\"", resultLines)

	return strings.Join(resultLines, "\n")
}

func keyscanServer(ctx context.Context, ipAddress string, silent bool) ([]byte, error) {
	var (
		outb []byte
		outs string
		err  error
	)

	backoff := wait.Backoff{
		Duration: 1 * time.Second,
		Factor:   1.1,
		Cap:      leftInContext(ctx),
		Steps:    math.MaxInt32,
	}

	err = wait.ExponentialBackoffWithContext(ctx, backoff, func(context.Context) (bool, error) {
		var (
			err2 error
		)

		outb, err2 = runSplitCommandNoErr([]string{
			"ssh-keyscan",
			ipAddress,
		},
			silent)
		outs = strings.TrimSpace(string(outb))
		log.Debugf("keyscanServer: outs = %s", outs)
		if err2 != nil {
			return false, nil
		}

		return true, nil
	})

	if err == nil {
		// Get rid of the comment lines generated by ssh-keyscan
		outLines := removeCommentLines(outs)
		outb = []byte(outLines)
	}

	return outb, err
}

func dnsForServer(ctx context.Context, cloudName string, apiKey string, bastionName string, domainName string) error {
	var (
		server       servers.Server
		ipAddress    string
		cisServiceID string
		crnstr       string
		zoneID       string
		dnsService   *dnsrecordsv1.DnsRecordsV1
		err          error
	)

	server, err = findServer(ctx, cloudName, bastionName)
	if err != nil {
		return err
	}
//	log.Debugf("server = %+v", server)

	_, ipAddress, err = findIpAddress(server)
	if err != nil {
		return err
	}
	if ipAddress == "" {
		return fmt.Errorf("ip address is empty for server %s", server.Name)
	}

	cisServiceID, _, err = getServiceInfo(ctx, apiKey, "internet-svcs", "")
	if err != nil {
		log.Errorf("getServiceInfo returns %v", err)
		return err
	}
	log.Debugf("dnsForServer: cisServiceID = %s", cisServiceID)

	crnstr, zoneID, err = getDomainCrn(ctx, apiKey, cisServiceID, domainName)
	log.Debugf("dnsForServer: crnstr = %s, zoneID = %s, err = %+v", crnstr, zoneID, err)
	if err != nil {
		log.Errorf("getDomainCrn returns %v", err)
		return err
	}

	dnsService, err = loadDnsServiceAPI(apiKey, crnstr, zoneID)
	if err != nil {
		log.Errorf("dnsForServer: loadDnsServiceAPI returns %v", err)
		return err
	}
	log.Debugf("dnsForServer: dnsService = %+v", dnsService)

	err = createOrDeletePublicDNSRecord(ctx,
		dnsrecordsv1.CreateDnsRecordOptions_Type_A,
		fmt.Sprintf("api.%s.%s", bastionName, domainName),
		ipAddress,
		true,
		dnsService)
	if err != nil {
		log.Errorf("dnsForServer: createOrDeletePublicDNSRecord(1) returns %v", err)
		return err
	}

	err = createOrDeletePublicDNSRecord(ctx,
		dnsrecordsv1.CreateDnsRecordOptions_Type_A,
		fmt.Sprintf("api-int.%s.%s", bastionName, domainName),
		ipAddress,
		true,
		dnsService)
	if err != nil {
		log.Errorf("dnsForServer: createOrDeletePublicDNSRecord(2) returns %v", err)
		return err
	}

	err = createOrDeletePublicDNSRecord(ctx,
		dnsrecordsv1.CreateDnsRecordOptions_Type_Cname,
		fmt.Sprintf("*.apps.%s.%s", bastionName, domainName),
		fmt.Sprintf("api.%s.%s", bastionName, domainName),
		true,
		dnsService)
	if err != nil {
		log.Errorf("dnsForServer: createOrDeletePublicDNSRecord(3) returns %v", err)
		return err
	}

	return nil
}

func leftInContext(ctx context.Context) time.Duration {
	deadline, ok := ctx.Deadline()
	if !ok {
		return math.MaxInt64
	}

	duration := time.Until(deadline)

	return duration
}
