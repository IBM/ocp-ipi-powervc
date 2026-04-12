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

package main

import (
//	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"
)

// TestWatchInstallationCommand_NilFlagSet tests that the function returns an error when flagSet is nil
func TestWatchInstallationCommand_NilFlagSet(t *testing.T) {
	err := watchInstallationCommand(nil, []string{})

	if err == nil {
		t.Fatal("Expected error for nil flag set, got nil")
	}

	expectedMsg := "flag set cannot be nil"
	if !strings.Contains(err.Error(), expectedMsg) {
		t.Errorf("Expected error message to contain %q, got: %v", expectedMsg, err)
	}
}

// TestWatchInstallationCommand_MissingRequiredFlags tests that the function returns errors for missing required flags
func TestWatchInstallationCommand_MissingRequiredFlags(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		errorMsg string
	}{
		{
			name:     "no flags provided",
			args:     []string{},
			errorMsg: "--cloud not specified",
		},
		{
			name:     "empty cloud",
			args:     []string{"--cloud", ""},
			errorMsg: "--cloud not specified",
		},
		{
			name:     "missing domainName",
			args:     []string{"--cloud", "mycloud"},
			errorMsg: "--domainName not specified",
		},
		{
			name: "empty domainName",
			args: []string{
				"--cloud", "mycloud",
				"--domainName", "",
			},
			errorMsg: "--domainName not specified",
		},
		{
			name: "missing bastionMetadata",
			args: []string{
				"--cloud", "mycloud",
				"--domainName", "example.com",
			},
			errorMsg: "--bastionMetadata not specified",
		},
		{
			name: "empty bastionMetadata",
			args: []string{
				"--cloud", "mycloud",
				"--domainName", "example.com",
				"--bastionMetadata", "",
			},
			errorMsg: "--bastionMetadata not specified",
		},
		{
			name: "missing bastionUsername",
			args: []string{
				"--cloud", "mycloud",
				"--domainName", "example.com",
				"--bastionMetadata", "/tmp/metadata",
			},
			errorMsg: "--bastionUsername not specified",
		},
		{
			name: "empty bastionUsername",
			args: []string{
				"--cloud", "mycloud",
				"--domainName", "example.com",
				"--bastionMetadata", "/tmp/metadata",
				"--bastionUsername", "",
			},
			errorMsg: "--bastionUsername not specified",
		},
		{
			name: "missing bastionRsa",
			args: []string{
				"--cloud", "mycloud",
				"--domainName", "example.com",
				"--bastionMetadata", "/tmp/metadata",
				"--bastionUsername", "core",
			},
			errorMsg: "--bastionRsa not specified",
		},
		{
			name: "empty bastionRsa",
			args: []string{
				"--cloud", "mycloud",
				"--domainName", "example.com",
				"--bastionMetadata", "/tmp/metadata",
				"--bastionUsername", "core",
				"--bastionRsa", "",
			},
			errorMsg: "--bastionRsa not specified",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flagSet := flag.NewFlagSet("watch-installation", flag.ContinueOnError)
			err := watchInstallationCommand(flagSet, tt.args)

			if err == nil {
				t.Fatal("Expected error for missing required flag, got nil")
			}

			if !strings.Contains(err.Error(), tt.errorMsg) {
				t.Errorf("Expected error message to contain %q, got: %v", tt.errorMsg, err)
			}
		})
	}
}

// TestWatchInstallationCommand_DHCPValidation tests DHCP configuration validation
func TestWatchInstallationCommand_DHCPValidation(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		errorMsg string
	}{
		{
			name: "enableDhcpd true but missing dhcpInterface",
			args: []string{
				"--cloud", "mycloud",
				"--domainName", "example.com",
				"--bastionMetadata", "/tmp/metadata",
				"--bastionUsername", "core",
				"--bastionRsa", "/tmp/key.rsa",
				"--enableDhcpd", "true",
			},
			errorMsg: "--dhcpInterface not specified",
		},
		{
			name: "enableDhcpd true but missing dhcpSubnet",
			args: []string{
				"--cloud", "mycloud",
				"--domainName", "example.com",
				"--bastionMetadata", "/tmp/metadata",
				"--bastionUsername", "core",
				"--bastionRsa", "/tmp/key.rsa",
				"--enableDhcpd", "true",
				"--dhcpInterface", "eth0",
			},
			errorMsg: "--dhcpSubnet not specified",
		},
		{
			name: "enableDhcpd true but missing dhcpNetmask",
			args: []string{
				"--cloud", "mycloud",
				"--domainName", "example.com",
				"--bastionMetadata", "/tmp/metadata",
				"--bastionUsername", "core",
				"--bastionRsa", "/tmp/key.rsa",
				"--enableDhcpd", "true",
				"--dhcpInterface", "eth0",
				"--dhcpSubnet", "192.168.1.0",
			},
			errorMsg: "--dhcpNetmask not specified",
		},
		{
			name: "enableDhcpd true but missing dhcpRouter",
			args: []string{
				"--cloud", "mycloud",
				"--domainName", "example.com",
				"--bastionMetadata", "/tmp/metadata",
				"--bastionUsername", "core",
				"--bastionRsa", "/tmp/key.rsa",
				"--enableDhcpd", "true",
				"--dhcpInterface", "eth0",
				"--dhcpSubnet", "192.168.1.0",
				"--dhcpNetmask", "255.255.255.0",
			},
			errorMsg: "--dhcpRouter not specified",
		},
		{
			name: "enableDhcpd true but missing dhcpDnsServers",
			args: []string{
				"--cloud", "mycloud",
				"--domainName", "example.com",
				"--bastionMetadata", "/tmp/metadata",
				"--bastionUsername", "core",
				"--bastionRsa", "/tmp/key.rsa",
				"--enableDhcpd", "true",
				"--dhcpInterface", "eth0",
				"--dhcpSubnet", "192.168.1.0",
				"--dhcpNetmask", "255.255.255.0",
				"--dhcpRouter", "192.168.1.1",
			},
			errorMsg: "--dhcpDnsServers not specified",
		},
		{
			name: "enableDhcpd true but missing dhcpServerId",
			args: []string{
				"--cloud", "mycloud",
				"--domainName", "example.com",
				"--bastionMetadata", "/tmp/metadata",
				"--bastionUsername", "core",
				"--bastionRsa", "/tmp/key.rsa",
				"--enableDhcpd", "true",
				"--dhcpInterface", "eth0",
				"--dhcpSubnet", "192.168.1.0",
				"--dhcpNetmask", "255.255.255.0",
				"--dhcpRouter", "192.168.1.1",
				"--dhcpDnsServers", "8.8.8.8",
			},
			errorMsg: "--dhcpServerId not specified",
		},
		{
			name: "invalid enableDhcpd value",
			args: []string{
				"--cloud", "mycloud",
				"--domainName", "example.com",
				"--bastionMetadata", "/tmp/metadata",
				"--bastionUsername", "core",
				"--bastionRsa", "/tmp/key.rsa",
				"--enableDhcpd", "invalid",
			},
			errorMsg: "must be 'true' or 'false'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flagSet := flag.NewFlagSet("watch-installation", flag.ContinueOnError)
			err := watchInstallationCommand(flagSet, tt.args)

			if err == nil {
				t.Fatal("Expected error for invalid DHCP configuration, got nil")
			}

			if !strings.Contains(err.Error(), tt.errorMsg) {
				t.Errorf("Expected error message to contain %q, got: %v", tt.errorMsg, err)
			}
		})
	}
}

// TestWatchInstallationCommand_InvalidDebugFlag tests invalid shouldDebug flag values
func TestWatchInstallationCommand_InvalidDebugFlag(t *testing.T) {
	tests := []struct {
		name       string
		debugValue string
	}{
		{
			name:       "invalid debug value",
			debugValue: "invalid",
		},
		{
			name:       "numeric debug value",
			debugValue: "3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flagSet := flag.NewFlagSet("watch-installation", flag.ContinueOnError)
			args := []string{
				"--cloud", "mycloud",
				"--domainName", "example.com",
				"--bastionMetadata", "/tmp/metadata",
				"--bastionUsername", "core",
				"--bastionRsa", "/tmp/key.rsa",
				"--shouldDebug", tt.debugValue,
			}

			err := watchInstallationCommand(flagSet, args)

			if err == nil {
				t.Fatal("Expected error for invalid debug flag, got nil")
			}

			expectedMsg := "must be 'true' or 'false'"
			if !strings.Contains(err.Error(), expectedMsg) {
				t.Errorf("Expected error message to contain %q, got: %v", expectedMsg, err)
			}
		})
	}
}

// TestStringArray_String tests the String method of stringArray type
func TestStringArray_String(t *testing.T) {
	tests := []struct {
		name     string
		array    stringArray
		expected string
	}{
		{
			name:     "empty array",
			array:    stringArray{},
			expected: "",
		},
		{
			name:     "single element",
			array:    stringArray{"value1"},
			expected: "value1",
		},
		{
			name:     "multiple elements",
			array:    stringArray{"value1", "value2", "value3"},
			expected: "value1,value2,value3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.array.String()
			if result != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, result)
			}
		})
	}
}

// TestStringArray_Set tests the Set method of stringArray type
func TestStringArray_Set(t *testing.T) {
	var arr stringArray

	err := arr.Set("value1")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if len(arr) != 1 || arr[0] != "value1" {
		t.Errorf("Expected [value1], got %v", arr)
	}

	err = arr.Set("value2")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if len(arr) != 2 || arr[1] != "value2" {
		t.Errorf("Expected [value1 value2], got %v", arr)
	}
}

// TestGatherBastionInformations tests the gatherBastionInformations function
func TestGatherBastionInformations(t *testing.T) {
	// Initialize logger for tests
	log = initLogger(false)
	
	// Create a temporary directory structure for testing
	tempDir := t.TempDir()

	// Create test metadata files
	cluster1Dir := filepath.Join(tempDir, "cluster1")
	cluster2Dir := filepath.Join(tempDir, "cluster2")
	cluster3Dir := filepath.Join(tempDir, "cluster3", "subdir")

	err := os.MkdirAll(cluster1Dir, 0755)
	if err != nil {
		t.Fatalf("Failed to create test directory: %v", err)
	}
	err = os.MkdirAll(cluster2Dir, 0755)
	if err != nil {
		t.Fatalf("Failed to create test directory: %v", err)
	}
	err = os.MkdirAll(cluster3Dir, 0755)
	if err != nil {
		t.Fatalf("Failed to create test directory: %v", err)
	}

	// Create metadata.json files
	metadata1 := filepath.Join(cluster1Dir, "metadata.json")
	metadata2 := filepath.Join(cluster2Dir, "metadata.json")
	metadata3 := filepath.Join(cluster3Dir, "metadata.json")

	err = os.WriteFile(metadata1, []byte(`{"clusterName":"cluster1"}`), 0644)
	if err != nil {
		t.Fatalf("Failed to create metadata file: %v", err)
	}
	err = os.WriteFile(metadata2, []byte(`{"clusterName":"cluster2"}`), 0644)
	if err != nil {
		t.Fatalf("Failed to create metadata file: %v", err)
	}
	err = os.WriteFile(metadata3, []byte(`{"clusterName":"cluster3"}`), 0644)
	if err != nil {
		t.Fatalf("Failed to create metadata file: %v", err)
	}

	// Test gathering bastion information
	bastionInfos, err := gatherBastionInformations(tempDir, "testuser", "/tmp/test.rsa")
	if err != nil {
		t.Fatalf("gatherBastionInformations failed: %v", err)
	}

	// Verify results
	if len(bastionInfos) != 3 {
		t.Errorf("Expected 3 bastion informations, got %d", len(bastionInfos))
	}

	for _, info := range bastionInfos {
		if info.Username != "testuser" {
			t.Errorf("Expected username 'testuser', got %q", info.Username)
		}
		if info.InstallerRsa != "/tmp/test.rsa" {
			t.Errorf("Expected InstallerRsa '/tmp/test.rsa', got %q", info.InstallerRsa)
		}
		if info.Valid {
			t.Error("Expected Valid to be false initially")
		}
	}
}

// TestGatherBastionInformations_EmptyDirectory tests gathering from an empty directory
func TestGatherBastionInformations_EmptyDirectory(t *testing.T) {
	// Initialize logger for tests
	log = initLogger(false)
	
	tempDir := t.TempDir()

	bastionInfos, err := gatherBastionInformations(tempDir, "testuser", "/tmp/test.rsa")
	if err != nil {
		t.Fatalf("gatherBastionInformations failed: %v", err)
	}

	if len(bastionInfos) != 0 {
		t.Errorf("Expected 0 bastion informations, got %d", len(bastionInfos))
	}
}

// TestGetMetadataClusterName tests the getMetadataClusterName function
func TestGetMetadataClusterName(t *testing.T) {
	// Initialize logger for tests
	log = initLogger(false)
	
	tempDir := t.TempDir()
	metadataFile := filepath.Join(tempDir, "metadata.json")

	metadata := MinimalMetadata{
		ClusterName: "test-cluster",
		ClusterID:   "cluster-id-123",
		InfraID:     "infra-id-456",
	}

	data, err := json.Marshal(metadata)
	if err != nil {
		t.Fatalf("Failed to marshal metadata: %v", err)
	}

	err = os.WriteFile(metadataFile, data, 0644)
	if err != nil {
		t.Fatalf("Failed to write metadata file: %v", err)
	}

	clusterName, infraID, err := getMetadataClusterName(metadataFile)
	if err != nil {
		t.Fatalf("getMetadataClusterName failed: %v", err)
	}

	if clusterName != "test-cluster" {
		t.Errorf("Expected cluster name 'test-cluster', got %q", clusterName)
	}

	if infraID != "infra-id-456" {
		t.Errorf("Expected infra ID 'infra-id-456', got %q", infraID)
	}
}

// TestGetMetadataClusterName_NonExistentFile tests reading from a non-existent file
func TestGetMetadataClusterName_NonExistentFile(t *testing.T) {
	// Initialize logger for tests
	log = initLogger(false)

	_, _, err := getMetadataClusterName("/nonexistent/metadata.json")
	if err == nil {
		t.Fatal("Expected error for non-existent file, got nil")
	}
}

// TestGetMetadataClusterName_InvalidJSON tests reading invalid JSON
func TestGetMetadataClusterName_InvalidJSON(t *testing.T) {
	// Initialize logger for tests
	log = initLogger(false)

	tempDir := t.TempDir()
	metadataFile := filepath.Join(tempDir, "metadata.json")

	err := os.WriteFile(metadataFile, []byte("invalid json"), 0644)
	if err != nil {
		t.Fatalf("Failed to write metadata file: %v", err)
	}

	_, _, err = getMetadataClusterName(metadataFile)
	if err == nil {
		t.Fatal("Expected error for invalid JSON, got nil")
	}
}

// TestGetServerSet tests the getServerSet function
func TestGetServerSet(t *testing.T) {
	tests := []struct {
		name           string
		servers        []servers.Server
		expectedCount  int
		expectedNames  []string
	}{
		{
			name:          "empty server list",
			servers:       []servers.Server{},
			expectedCount: 0,
		},
		{
			name: "servers with bootstrap, master, worker",
			servers: []servers.Server{
				{
					Name: "cluster-abc123-bootstrap-0",
					Addresses: map[string]interface{}{
						"network1": []interface{}{
							map[string]interface{}{
								"OS-EXT-IPS-MAC:mac_addr": "fa:16:3e:00:00:01",
								"addr":                    "192.168.1.10",
							},
						},
					},
				},
				{
					Name: "cluster-abc123-master-0",
					Addresses: map[string]interface{}{
						"network1": []interface{}{
							map[string]interface{}{
								"OS-EXT-IPS-MAC:mac_addr": "fa:16:3e:00:00:02",
								"addr":                    "192.168.1.11",
							},
						},
					},
				},
				{
					Name: "cluster-abc123-worker-0",
					Addresses: map[string]interface{}{
						"network1": []interface{}{
							map[string]interface{}{
								"OS-EXT-IPS-MAC:mac_addr": "fa:16:3e:00:00:03",
								"addr":                    "192.168.1.12",
							},
						},
					},
				},
			},
			expectedCount: 3,
			expectedNames: []string{
				"cluster-abc123-bootstrap-0",
				"cluster-abc123-master-0",
				"cluster-abc123-worker-0",
			},
		},
		{
			name: "servers with non-cluster nodes",
			servers: []servers.Server{
				{
					Name: "cluster-abc123-master-0",
					Addresses: map[string]interface{}{
						"network1": []interface{}{
							map[string]interface{}{
								"OS-EXT-IPS-MAC:mac_addr": "fa:16:3e:00:00:01",
								"addr":                    "192.168.1.10",
							},
						},
					},
				},
				{
					Name: "other-server",
					Addresses: map[string]interface{}{
						"network1": []interface{}{
							map[string]interface{}{
								"OS-EXT-IPS-MAC:mac_addr": "fa:16:3e:00:00:02",
								"addr":                    "192.168.1.11",
							},
						},
					},
				},
			},
			expectedCount: 1,
			expectedNames: []string{"cluster-abc123-master-0"},
		},
		{
			name: "server without IP address",
			servers: []servers.Server{
				{
					Name:      "cluster-abc123-master-0",
					Addresses: map[string]interface{}{},
				},
			},
			expectedCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			serverSet := getServerSet(tt.servers)

			if serverSet.Len() != tt.expectedCount {
				t.Errorf("Expected %d servers in set, got %d", tt.expectedCount, serverSet.Len())
			}

			for _, name := range tt.expectedNames {
				if !serverSet.Has(name) {
					t.Errorf("Expected server set to contain %q", name)
				}
			}
		})
	}
}

// TestFindIpAddress tests the findIpAddress function
func TestFindIpAddress(t *testing.T) {
	tests := []struct {
		name           string
		server         servers.Server
		expectedMAC    string
		expectedIP     string
		expectError    bool
	}{
		{
			name: "valid server with IP",
			server: servers.Server{
				Name: "test-server",
				Addresses: map[string]interface{}{
					"network1": []interface{}{
						map[string]interface{}{
							"OS-EXT-IPS-MAC:mac_addr": "fa:16:3e:b1:33:03",
							"addr":                    "10.20.182.169",
						},
					},
				},
			},
			expectedMAC: "fa:16:3e:b1:33:03",
			expectedIP:  "10.20.182.169",
			expectError: false,
		},
		{
			name: "server without addresses",
			server: servers.Server{
				Name:      "test-server",
				Addresses: map[string]interface{}{},
			},
			expectedMAC: "",
			expectedIP:  "",
			expectError: false,
		},
		{
			name: "server with multiple networks",
			server: servers.Server{
				Name: "test-server",
				Addresses: map[string]interface{}{
					"network1": []interface{}{
						map[string]interface{}{
							"OS-EXT-IPS-MAC:mac_addr": "fa:16:3e:00:00:01",
							"addr":                    "192.168.1.10",
						},
					},
					"network2": []interface{}{
						map[string]interface{}{
							"OS-EXT-IPS-MAC:mac_addr": "fa:16:3e:00:00:02",
							"addr":                    "10.0.0.10",
						},
					},
				},
			},
			expectedMAC: "fa:16:3e:00:00:01",
			expectedIP:  "192.168.1.10",
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			macAddr, ipAddress, err := findIpAddress(tt.server)

			if tt.expectError && err == nil {
				t.Fatal("Expected error, got nil")
			}

			if !tt.expectError && err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if macAddr != tt.expectedMAC {
				t.Errorf("Expected MAC address %q, got %q", tt.expectedMAC, macAddr)
			}

			if ipAddress != tt.expectedIP {
				t.Errorf("Expected IP address %q, got %q", tt.expectedIP, ipAddress)
			}
		})
	}
}

// TestGetClusterName tests the getClusterName function
func TestGetClusterName(t *testing.T) {
	tests := []struct {
		name        string
		servers     []servers.Server
		expected    string
	}{
		{
			name:     "empty server list",
			servers:  []servers.Server{},
			expected: "",
		},
		{
			name: "server with bootstrap",
			servers: []servers.Server{
				{Name: "mycluster-abc12-bootstrap-0"},
			},
			expected: "mycluster",
		},
		{
			name: "server with master",
			servers: []servers.Server{
				{Name: "mycluster-abc12-master-0"},
			},
			expected: "mycluster",
		},
		{
			name: "multiple servers",
			servers: []servers.Server{
				{Name: "other-server"},
				{Name: "mycluster-abc12-master-0"},
				{Name: "mycluster-abc12-worker-0"},
			},
			expected: "mycluster",
		},
		{
			name: "no matching servers",
			servers: []servers.Server{
				{Name: "other-server"},
				{Name: "another-server"},
			},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getClusterName(tt.servers)
			if result != tt.expected {
				t.Errorf("Expected cluster name %q, got %q", tt.expected, result)
			}
		})
	}
}

// TestHandleCheckAlive tests the handleCheckAlive function
func TestHandleCheckAlive(t *testing.T) {
	// Initialize logger for tests
	log = initLogger(false)
	
	tests := []struct {
		name        string
		data        string
		expectError bool
	}{
		{
			name:        "valid check-alive command",
			data:        `{"command":"check-alive"}`,
			expectError: false,
		},
		{
			name:        "invalid JSON",
			data:        `invalid json`,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errChan := make(chan error, 1)
			go handleCheckAlive(tt.data, errChan)
			err := <-errChan

			if tt.expectError && err == nil {
				t.Error("Expected error, got nil")
			}

			if !tt.expectError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
		})
	}
}

// TestHandleCreateMetadata tests the handleCreateMetadata function
func TestHandleCreateMetadata(t *testing.T) {
	// Initialize logger for tests
	log = initLogger(false)
	
	tempDir := t.TempDir()
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get current directory: %v", err)
	}
	defer os.Chdir(originalDir)

	err = os.Chdir(tempDir)
	if err != nil {
		t.Fatalf("Failed to change directory: %v", err)
	}

	tests := []struct {
		name         string
		data         string
		shouldCreate bool
		expectError  bool
	}{
		{
			name: "create metadata",
			data: `{
				"command": "create-metadata",
				"metadata": {
					"clusterName": "test-cluster",
					"clusterID": "cluster-123",
					"infraID": "infra-456"
				}
			}`,
			shouldCreate: true,
			expectError:  false,
		},
		{
			name:         "invalid JSON",
			data:         `invalid json`,
			shouldCreate: true,
			expectError:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errChan := make(chan error, 1)
			go handleCreateMetadata(tt.data, tt.shouldCreate, errChan)
			err := <-errChan

			if tt.expectError && err == nil {
				t.Error("Expected error, got nil")
			}

			if !tt.expectError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			if !tt.expectError && tt.shouldCreate {
				// Verify metadata file was created
				metadataPath := filepath.Join(tempDir, "infra-456", "metadata.json")
				if _, err := os.Stat(metadataPath); os.IsNotExist(err) {
					t.Error("Expected metadata file to be created")
				}
			}
		})
	}
}

// TestBastionInformation tests the bastionInformation struct
func TestBastionInformation(t *testing.T) {
	info := bastionInformation{
		Valid:        true,
		Metadata:     "/path/to/metadata.json",
		Username:     "core",
		InstallerRsa: "/path/to/key.rsa",
		ClusterName:  "test-cluster",
		InfraID:      "infra-123",
		IPAddress:    "192.168.1.100",
		NumVMs:       5,
	}

	if !info.Valid {
		t.Error("Expected Valid to be true")
	}

	if info.ClusterName != "test-cluster" {
		t.Errorf("Expected ClusterName 'test-cluster', got %q", info.ClusterName)
	}

	if info.NumVMs != 5 {
		t.Errorf("Expected NumVMs 5, got %d", info.NumVMs)
	}
}

// TestMinimalMetadata tests the MinimalMetadata struct
func TestMinimalMetadata(t *testing.T) {
	metadata := MinimalMetadata{
		ClusterName: "test-cluster",
		ClusterID:   "cluster-123",
		InfraID:     "infra-456",
	}

	data, err := json.Marshal(metadata)
	if err != nil {
		t.Fatalf("Failed to marshal metadata: %v", err)
	}

	var unmarshaled MinimalMetadata
	err = json.Unmarshal(data, &unmarshaled)
	if err != nil {
		t.Fatalf("Failed to unmarshal metadata: %v", err)
	}

	if unmarshaled.ClusterName != metadata.ClusterName {
		t.Errorf("Expected ClusterName %q, got %q", metadata.ClusterName, unmarshaled.ClusterName)
	}

	if unmarshaled.ClusterID != metadata.ClusterID {
		t.Errorf("Expected ClusterID %q, got %q", metadata.ClusterID, unmarshaled.ClusterID)
	}

	if unmarshaled.InfraID != metadata.InfraID {
		t.Errorf("Expected InfraID %q, got %q", metadata.InfraID, unmarshaled.InfraID)
	}
}

//// TestUpdateBastionInformations_EmptyList tests updateBastionInformations with empty list
//func TestUpdateBastionInformations_EmptyList(t *testing.T) {
//	// Initialize logger for tests
//	log = initLogger(false)
//
//	ctx := context.Background()
//	bastionInfos := []bastionInformation{}
//
//	// This should not fail with empty list
//	err := updateBastionInformations(ctx, "test-cloud", bastionInfos)
//	
//	// We expect this to fail because getAllServers will fail with invalid cloud
//	// but we're testing that empty list doesn't cause panic
//	if err == nil {
//		t.Log("Function completed without error (expected for empty list)")
//	}
//}

// TestGetServerSet_DifferenceOperations tests set difference operations
func TestGetServerSet_DifferenceOperations(t *testing.T) {
	servers1 := []servers.Server{
		{
			Name: "cluster-abc123-master-0",
			Addresses: map[string]interface{}{
				"network1": []interface{}{
					map[string]interface{}{
						"OS-EXT-IPS-MAC:mac_addr": "fa:16:3e:00:00:01",
						"addr":                    "192.168.1.10",
					},
				},
			},
		},
		{
			Name: "cluster-abc123-worker-0",
			Addresses: map[string]interface{}{
				"network1": []interface{}{
					map[string]interface{}{
						"OS-EXT-IPS-MAC:mac_addr": "fa:16:3e:00:00:02",
						"addr":                    "192.168.1.11",
					},
				},
			},
		},
	}

	servers2 := []servers.Server{
		{
			Name: "cluster-abc123-worker-0",
			Addresses: map[string]interface{}{
				"network1": []interface{}{
					map[string]interface{}{
						"OS-EXT-IPS-MAC:mac_addr": "fa:16:3e:00:00:02",
						"addr":                    "192.168.1.11",
					},
				},
			},
		},
		{
			Name: "cluster-abc123-worker-1",
			Addresses: map[string]interface{}{
				"network1": []interface{}{
					map[string]interface{}{
						"OS-EXT-IPS-MAC:mac_addr": "fa:16:3e:00:00:03",
						"addr":                    "192.168.1.12",
					},
				},
			},
		},
	}

	set1 := getServerSet(servers1)
	set2 := getServerSet(servers2)

	// Test added servers (in set2 but not in set1)
	added := set2.Difference(set1)
	if !added.Has("cluster-abc123-worker-1") {
		t.Error("Expected worker-1 to be in added set")
	}
	if added.Len() != 1 {
		t.Errorf("Expected 1 added server, got %d", added.Len())
	}

	// Test deleted servers (in set1 but not in set2)
	deleted := set1.Difference(set2)
	if !deleted.Has("cluster-abc123-master-0") {
		t.Error("Expected master-0 to be in deleted set")
	}
	if deleted.Len() != 1 {
		t.Errorf("Expected 1 deleted server, got %d", deleted.Len())
	}
}

// Made with Bob
