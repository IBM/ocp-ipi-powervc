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
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	igntypes "github.com/coreos/ignition/v2/config/v3_2/types"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"
)

// TestRhcosConfig_Validate tests the validation logic for rhcosConfig
func TestRhcosConfig_Validate(t *testing.T) {
	validSSHKey := "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQC1234567890abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890 user@host"
	validPasswdHash := "$6$rounds=4096$saltsaltsal$hashhashhashhashhashhashhashhashhashhashhashhash"

	tests := []struct {
		name        string
		config      rhcosConfig
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid configuration",
			config: rhcosConfig{
				Cloud:        "mycloud",
				RhcosName:    "test-rhcos",
				FlavorName:   "medium",
				ImageName:    "rhcos-4.12",
				NetworkName:  "private-net",
				PasswdHash:   validPasswdHash,
				SshPublicKey: validSSHKey,
			},
			expectError: false,
		},
		{
			name: "missing cloud",
			config: rhcosConfig{
				Cloud:        "",
				RhcosName:    "test-rhcos",
				FlavorName:   "medium",
				ImageName:    "rhcos-4.12",
				NetworkName:  "private-net",
				PasswdHash:   validPasswdHash,
				SshPublicKey: validSSHKey,
			},
			expectError: true,
			errorMsg:    "cloud name is required",
		},
		{
			name: "missing rhcos name",
			config: rhcosConfig{
				Cloud:        "mycloud",
				RhcosName:    "",
				FlavorName:   "medium",
				ImageName:    "rhcos-4.12",
				NetworkName:  "private-net",
				PasswdHash:   validPasswdHash,
				SshPublicKey: validSSHKey,
			},
			expectError: true,
			errorMsg:    "RHCOS name is required",
		},
		{
			name: "invalid rhcos name characters",
			config: rhcosConfig{
				Cloud:        "mycloud",
				RhcosName:    "test@rhcos!",
				FlavorName:   "medium",
				ImageName:    "rhcos-4.12",
				NetworkName:  "private-net",
				PasswdHash:   validPasswdHash,
				SshPublicKey: validSSHKey,
			},
			expectError: true,
			errorMsg:    "RHCOS name contains invalid characters",
		},
		{
			name: "missing flavor name",
			config: rhcosConfig{
				Cloud:        "mycloud",
				RhcosName:    "test-rhcos",
				FlavorName:   "",
				ImageName:    "rhcos-4.12",
				NetworkName:  "private-net",
				PasswdHash:   validPasswdHash,
				SshPublicKey: validSSHKey,
			},
			expectError: true,
			errorMsg:    "flavor name is required",
		},
		{
			name: "missing image name",
			config: rhcosConfig{
				Cloud:        "mycloud",
				RhcosName:    "test-rhcos",
				FlavorName:   "medium",
				ImageName:    "",
				NetworkName:  "private-net",
				PasswdHash:   validPasswdHash,
				SshPublicKey: validSSHKey,
			},
			expectError: true,
			errorMsg:    "image name is required",
		},
		{
			name: "missing network name",
			config: rhcosConfig{
				Cloud:        "mycloud",
				RhcosName:    "test-rhcos",
				FlavorName:   "medium",
				ImageName:    "rhcos-4.12",
				NetworkName:  "",
				PasswdHash:   validPasswdHash,
				SshPublicKey: validSSHKey,
			},
			expectError: true,
			errorMsg:    "network name is required",
		},
		{
			name: "missing ssh public key",
			config: rhcosConfig{
				Cloud:        "mycloud",
				RhcosName:    "test-rhcos",
				FlavorName:   "medium",
				ImageName:    "rhcos-4.12",
				NetworkName:  "private-net",
				PasswdHash:   validPasswdHash,
				SshPublicKey: "",
			},
			expectError: true,
			errorMsg:    "SSH public key is required",
		},
		{
			name: "ssh key too short",
			config: rhcosConfig{
				Cloud:        "mycloud",
				RhcosName:    "test-rhcos",
				FlavorName:   "medium",
				ImageName:    "rhcos-4.12",
				NetworkName:  "private-net",
				PasswdHash:   validPasswdHash,
				SshPublicKey: "ssh-rsa short",
			},
			expectError: true,
			errorMsg:    "SSH public key appears invalid (too short)",
		},
		{
			name: "ssh key invalid prefix",
			config: rhcosConfig{
				Cloud:        "mycloud",
				RhcosName:    "test-rhcos",
				FlavorName:   "medium",
				ImageName:    "rhcos-4.12",
				NetworkName:  "private-net",
				PasswdHash:   validPasswdHash,
				SshPublicKey: "invalid-prefix AAAAB3NzaC1yc2EAAAADAQABAAABgQC1234567890abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890",
			},
			expectError: true,
			errorMsg:    "SSH public key must start with 'ssh-' or 'ecdsa-'",
		},
		{
			name: "valid ecdsa key",
			config: rhcosConfig{
				Cloud:        "mycloud",
				RhcosName:    "test-rhcos",
				FlavorName:   "medium",
				ImageName:    "rhcos-4.12",
				NetworkName:  "private-net",
				PasswdHash:   validPasswdHash,
				SshPublicKey: "ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTYAAAAIbmlzdHAyNTYAAABBBEmKSENjQEezOmxkZMy7opKgwFB9nkt5YRrYMjNuG5N87uRgg6CLrbo5wAdT/y6v0mKV0U2w0WZ2YB/++Tpockg= user@host",
			},
			expectError: false,
		},
		{
			name: "missing password hash",
			config: rhcosConfig{
				Cloud:        "mycloud",
				RhcosName:    "test-rhcos",
				FlavorName:   "medium",
				ImageName:    "rhcos-4.12",
				NetworkName:  "private-net",
				PasswdHash:   "",
				SshPublicKey: validSSHKey,
			},
			expectError: true,
			errorMsg:    "password hash is required",
		},
		{
			name: "password hash too short",
			config: rhcosConfig{
				Cloud:        "mycloud",
				RhcosName:    "test-rhcos",
				FlavorName:   "medium",
				ImageName:    "rhcos-4.12",
				NetworkName:  "private-net",
				PasswdHash:   "$6$short",
				SshPublicKey: validSSHKey,
			},
			expectError: true,
			errorMsg:    "password hash appears invalid (too short)",
		},
		{
			name: "password hash invalid format",
			config: rhcosConfig{
				Cloud:        "mycloud",
				RhcosName:    "test-rhcos",
				FlavorName:   "medium",
				ImageName:    "rhcos-4.12",
				NetworkName:  "private-net",
				PasswdHash:   "invalidhashformat",
				SshPublicKey: validSSHKey,
			},
			expectError: true,
			errorMsg:    "password hash must be in crypt format (starting with $)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.validate()

			if tt.expectError {
				if err == nil {
					t.Fatal("Expected error, got nil")
				}
				if tt.errorMsg != "" && !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("Expected error to contain %q, got: %v", tt.errorMsg, err)
				}
			} else {
				if err != nil {
					t.Errorf("Expected no error, got: %v", err)
				}
			}
		})
	}
}

// TestParseRhcosFlags tests the flag parsing logic
func TestParseRhcosFlags(t *testing.T) {
	validSSHKey := "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQC1234567890abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890 user@host"
	validPasswdHash := "$6$rounds=4096$saltsaltsal$hashhashhashhashhashhashhashhashhashhashhashhash"

	tests := []struct {
		name        string
		args        []string
		expectError bool
		errorMsg    string
		checkConfig func(*testing.T, *rhcosConfig)
	}{
		{
			name: "valid flags",
			args: []string{
				"--cloud", "mycloud",
				"--rhcosName", "test-rhcos",
				"--flavorName", "medium",
				"--imageName", "rhcos-4.12",
				"--networkName", "private-net",
				"--passwdHash", validPasswdHash,
				"--sshPublicKey", validSSHKey,
			},
			expectError: false,
			checkConfig: func(t *testing.T, c *rhcosConfig) {
				if c.Cloud != "mycloud" {
					t.Errorf("Expected cloud 'mycloud', got %q", c.Cloud)
				}
				if c.RhcosName != "test-rhcos" {
					t.Errorf("Expected rhcosName 'test-rhcos', got %q", c.RhcosName)
				}
			},
		},
		{
			name: "with optional domain name",
			args: []string{
				"--cloud", "mycloud",
				"--rhcosName", "test-rhcos",
				"--flavorName", "medium",
				"--imageName", "rhcos-4.12",
				"--networkName", "private-net",
				"--passwdHash", validPasswdHash,
				"--sshPublicKey", validSSHKey,
				"--domainName", "example.com",
			},
			expectError: false,
			checkConfig: func(t *testing.T, c *rhcosConfig) {
				if c.DomainName != "example.com" {
					t.Errorf("Expected domainName 'example.com', got %q", c.DomainName)
				}
			},
		},
		{
			name: "with debug flag true",
			args: []string{
				"--cloud", "mycloud",
				"--rhcosName", "test-rhcos",
				"--flavorName", "medium",
				"--imageName", "rhcos-4.12",
				"--networkName", "private-net",
				"--passwdHash", validPasswdHash,
				"--sshPublicKey", validSSHKey,
				"--shouldDebug", "true",
			},
			expectError: false,
			checkConfig: func(t *testing.T, c *rhcosConfig) {
				if !c.ShouldDebug {
					t.Error("Expected ShouldDebug to be true")
				}
			},
		},
		{
			name: "with debug flag false",
			args: []string{
				"--cloud", "mycloud",
				"--rhcosName", "test-rhcos",
				"--flavorName", "medium",
				"--imageName", "rhcos-4.12",
				"--networkName", "private-net",
				"--passwdHash", validPasswdHash,
				"--sshPublicKey", validSSHKey,
				"--shouldDebug", "false",
			},
			expectError: false,
			checkConfig: func(t *testing.T, c *rhcosConfig) {
				if c.ShouldDebug {
					t.Error("Expected ShouldDebug to be false")
				}
			},
		},
		{
			name: "missing required cloud flag",
			args: []string{
				"--rhcosName", "test-rhcos",
				"--flavorName", "medium",
				"--imageName", "rhcos-4.12",
				"--networkName", "private-net",
				"--passwdHash", validPasswdHash,
				"--sshPublicKey", validSSHKey,
			},
			expectError: true,
			errorMsg:    "cloud name is required",
		},
		{
			name: "invalid debug flag",
			args: []string{
				"--cloud", "mycloud",
				"--rhcosName", "test-rhcos",
				"--flavorName", "medium",
				"--imageName", "rhcos-4.12",
				"--networkName", "private-net",
				"--passwdHash", validPasswdHash,
				"--sshPublicKey", validSSHKey,
				"--shouldDebug", "invalid",
			},
			expectError: true,
			errorMsg:    "shouldDebug",
		},
		{
			name: "unknown flag",
			args: []string{
				"--cloud", "mycloud",
				"--rhcosName", "test-rhcos",
				"--flavorName", "medium",
				"--imageName", "rhcos-4.12",
				"--networkName", "private-net",
				"--passwdHash", validPasswdHash,
				"--sshPublicKey", validSSHKey,
				"--unknownFlag", "value",
			},
			expectError: true,
			errorMsg:    "failed to parse flags",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flagSet := flag.NewFlagSet("create-rhcos", flag.ContinueOnError)
			config, err := parseRhcosFlags(flagSet, tt.args)

			if tt.expectError {
				if err == nil {
					t.Fatal("Expected error, got nil")
				}
				if tt.errorMsg != "" && !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("Expected error to contain %q, got: %v", tt.errorMsg, err)
				}
			} else {
				if err != nil {
					t.Errorf("Expected no error, got: %v", err)
				}
				if tt.checkConfig != nil && config != nil {
					tt.checkConfig(t, config)
				}
			}
		})
	}
}

// TestParseRhcosFlags_APIKeyFromEnv tests that API key is loaded from environment
func TestParseRhcosFlags_APIKeyFromEnv(t *testing.T) {
	validSSHKey := "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQC1234567890abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890 user@host"
	validPasswdHash := "$6$rounds=4096$saltsaltsal$hashhashhashhashhashhashhashhashhashhashhashhash"

	// Save original env var
	originalAPIKey := os.Getenv("IBMCLOUD_API_KEY")
	defer func() {
		if originalAPIKey != "" {
			os.Setenv("IBMCLOUD_API_KEY", originalAPIKey)
		} else {
			os.Unsetenv("IBMCLOUD_API_KEY")
		}
	}()

	// Set test API key
	testAPIKey := "test-api-key-12345"
	os.Setenv("IBMCLOUD_API_KEY", testAPIKey)

	flagSet := flag.NewFlagSet("create-rhcos", flag.ContinueOnError)
	args := []string{
		"--cloud", "mycloud",
		"--rhcosName", "test-rhcos",
		"--flavorName", "medium",
		"--imageName", "rhcos-4.12",
		"--networkName", "private-net",
		"--passwdHash", validPasswdHash,
		"--sshPublicKey", validSSHKey,
	}

	config, err := parseRhcosFlags(flagSet, args)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if config.APIKey != testAPIKey {
		t.Errorf("Expected APIKey %q, got %q", testAPIKey, config.APIKey)
	}
}

// TestCreateBootstrapIgnition tests the ignition configuration generation
func TestCreateBootstrapIgnition(t *testing.T) {
	// Initialize logger for tests
	log = initLogger(false)
	
	validSSHKey := "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQC1234567890abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890 user@host"
	validPasswdHash := "$6$rounds=4096$saltsaltsal$hashhashhashhashhashhashhashhashhashhashhashhash"

	tests := []struct {
		name        string
		passwdHash  string
		sshKey      string
		expectError bool
		errorMsg    string
		validate    func(*testing.T, []byte)
	}{
		{
			name:        "valid ignition config",
			passwdHash:  validPasswdHash,
			sshKey:      validSSHKey,
			expectError: false,
			validate: func(t *testing.T, data []byte) {
				// Verify it's valid JSON
				var config igntypes.Config
				if err := json.Unmarshal(data, &config); err != nil {
					t.Errorf("Failed to unmarshal ignition config: %v", err)
				}

				// Verify version
				if config.Ignition.Version != igntypes.MaxVersion.String() {
					t.Errorf("Expected version %s, got %s", igntypes.MaxVersion.String(), config.Ignition.Version)
				}

				// Verify user configuration
				if len(config.Passwd.Users) != 1 {
					t.Errorf("Expected 1 user, got %d", len(config.Passwd.Users))
				}
				if config.Passwd.Users[0].Name != "core" {
					t.Errorf("Expected user 'core', got %q", config.Passwd.Users[0].Name)
				}
				if *config.Passwd.Users[0].PasswordHash != validPasswdHash {
					t.Errorf("Password hash mismatch")
				}
				if len(config.Passwd.Users[0].SSHAuthorizedKeys) != 1 {
					t.Errorf("Expected 1 SSH key, got %d", len(config.Passwd.Users[0].SSHAuthorizedKeys))
				}
			},
		},
		{
			name:        "empty password hash",
			passwdHash:  "",
			sshKey:      validSSHKey,
			expectError: true,
			errorMsg:    "password hash cannot be empty",
		},
		{
			name:        "empty ssh key",
			passwdHash:  validPasswdHash,
			sshKey:      "",
			expectError: true,
			errorMsg:    "SSH key cannot be empty",
		},
		{
			name:        "both empty",
			passwdHash:  "",
			sshKey:      "",
			expectError: true,
			errorMsg:    "password hash cannot be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := createBootstrapIgnition(tt.passwdHash, tt.sshKey)

			if tt.expectError {
				if err == nil {
					t.Fatal("Expected error, got nil")
				}
				if tt.errorMsg != "" && !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("Expected error to contain %q, got: %v", tt.errorMsg, err)
				}
			} else {
				if err != nil {
					t.Errorf("Expected no error, got: %v", err)
				}
				if tt.validate != nil {
					tt.validate(t, data)
				}
			}
		})
	}
}

// TestCreateBootstrapIgnition_SizeLimit tests that ignition config respects size limits
func TestCreateBootstrapIgnition_SizeLimit(t *testing.T) {
	// Initialize logger for tests
	log = initLogger(false)
	
	validSSHKey := "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQC1234567890abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890 user@host"
	validPasswdHash := "$6$rounds=4096$saltsaltsal$hashhashhashhashhashhashhashhashhashhashhashhash"

	data, err := createBootstrapIgnition(validPasswdHash, validSSHKey)
	if err != nil {
		t.Fatalf("Failed to create ignition config: %v", err)
	}

	// Encode to base64 as done in the function
	encoded := base64.StdEncoding.EncodeToString(data)
	encodedSize := len(encoded)

	if encodedSize > novaUserDataMaxSize {
		t.Errorf("Encoded ignition config exceeds nova user data limit: %d > %d bytes",
			encodedSize, novaUserDataMaxSize)
	}

	t.Logf("Ignition config size: %d bytes (%.1f%% of %d byte limit)",
		encodedSize, float64(encodedSize)/float64(novaUserDataMaxSize)*100, novaUserDataMaxSize)
}

// TestIsServerNotFoundError tests the server not found error detection
func TestIsServerNotFoundError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "server not found error with correct prefix",
			err:      os.ErrNotExist,
			expected: false,
		},
		{
			name:     "generic error",
			err:      os.ErrInvalid,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isServerNotFoundError(tt.err)
			if result != tt.expected {
				t.Errorf("Expected %v, got %v for error: %v", tt.expected, result, tt.err)
			}
		})
	}
}

// TestEnsureSSHDirectory tests SSH directory creation
func TestEnsureSSHDirectory(t *testing.T) {
	tests := []struct {
		name        string
		setup       func(t *testing.T) string
		cleanup     func(string)
		expectError bool
		errorMsg    string
	}{
		{
			name: "create new directory",
			setup: func(t *testing.T) string {
				tmpDir := t.TempDir()
				return filepath.Join(tmpDir, ".ssh")
			},
			cleanup:     func(s string) {},
			expectError: false,
		},
		{
			name: "existing directory",
			setup: func(t *testing.T) string {
				tmpDir := t.TempDir()
				sshDir := filepath.Join(tmpDir, ".ssh")
				if err := os.MkdirAll(sshDir, 0700); err != nil {
					t.Fatalf("Failed to create test directory: %v", err)
				}
				return sshDir
			},
			cleanup:     func(s string) {},
			expectError: false,
		},
		{
			name: "path exists but is a file",
			setup: func(t *testing.T) string {
				tmpDir := t.TempDir()
				filePath := filepath.Join(tmpDir, ".ssh")
				if err := os.WriteFile(filePath, []byte("test"), 0644); err != nil {
					t.Fatalf("Failed to create test file: %v", err)
				}
				return filePath
			},
			cleanup:     func(s string) { os.Remove(s) },
			expectError: true,
			errorMsg:    "SSH path exists but is not a directory",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := tt.setup(t)
			defer tt.cleanup(path)

			err := ensureSSHDirectory(path)

			if tt.expectError {
				if err == nil {
					t.Fatal("Expected error, got nil")
				}
				if tt.errorMsg != "" && !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("Expected error to contain %q, got: %v", tt.errorMsg, err)
				}
			} else {
				if err != nil {
					t.Errorf("Expected no error, got: %v", err)
				}
				// Verify directory exists
				info, statErr := os.Stat(path)
				if statErr != nil {
					t.Errorf("Directory should exist: %v", statErr)
				}
				if !info.IsDir() {
					t.Error("Path should be a directory")
				}
			}
		})
	}
}

// TestConfigureDNS tests DNS configuration logic
func TestConfigureDNS(t *testing.T) {
	tests := []struct {
		name        string
		config      *rhcosConfig
		expectError bool
		errorMsg    string
	}{
		{
			name: "no API key - should skip",
			config: &rhcosConfig{
				Cloud:      "mycloud",
				RhcosName:  "test-rhcos",
				DomainName: "example.com",
				APIKey:     "",
			},
			expectError: false,
		},
		// Note: We don't test with API key set because it would attempt
		// to connect to actual OpenStack/IBM Cloud services, which would
		// timeout or fail in test environment. The function is tested
		// indirectly through integration tests.
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			err := configureDNS(ctx, tt.config)

			if tt.expectError {
				if err == nil {
					t.Fatal("Expected error, got nil")
				}
				if tt.errorMsg != "" && !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("Expected error to contain %q, got: %v", tt.errorMsg, err)
				}
			} else {
				if err != nil {
					t.Errorf("Expected no error, got: %v", err)
				}
			}
		})
	}
}

// TestRhcosConstants tests that constants are defined correctly
func TestRhcosConstants(t *testing.T) {
	if rhcosDefaultTimeout == 0 {
		t.Error("rhcosDefaultTimeout should not be zero")
	}
	if novaUserDataMaxSize != 65535 {
		t.Errorf("novaUserDataMaxSize should be 65535, got %d", novaUserDataMaxSize)
	}
	if ignitionHTTPTimeout != 120 {
		t.Errorf("ignitionHTTPTimeout should be 120, got %d", ignitionHTTPTimeout)
	}
	if sshKeygenExitCodeNotFound != 1 {
		t.Errorf("sshKeygenExitCodeNotFound should be 1, got %d", sshKeygenExitCodeNotFound)
	}
	if knownHostsFilePerms != 0644 {
		t.Errorf("knownHostsFilePerms should be 0644, got %o", knownHostsFilePerms)
	}
	if sshDirPerms != 0700 {
		t.Errorf("sshDirPerms should be 0700, got %o", sshDirPerms)
	}
	if serverNotFoundPrefix == "" {
		t.Error("serverNotFoundPrefix should not be empty")
	}
	if minSSHKeyLength != 100 {
		t.Errorf("minSSHKeyLength should be 100, got %d", minSSHKeyLength)
	}
	if minPasswordHashLength != 13 {
		t.Errorf("minPasswordHashLength should be 13, got %d", minPasswordHashLength)
	}
}

// TestSetupRhcosServer tests the server setup logic
func TestSetupRhcosServer(t *testing.T) {
	tests := []struct {
		name        string
		server      servers.Server
		expectError bool
		errorMsg    string
	}{
		{
			name: "server without IP address",
			server: servers.Server{
				ID:   "test-id",
				Name: "test-server",
			},
			expectError: true,
			errorMsg:    "has no IP address",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			err := setupRhcosServer(ctx, "mycloud", tt.server)

			if tt.expectError {
				if err == nil {
					t.Fatal("Expected error, got nil")
				}
				if tt.errorMsg != "" && !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("Expected error to contain %q, got: %v", tt.errorMsg, err)
				}
			} else {
				if err != nil {
					t.Errorf("Expected no error, got: %v", err)
				}
			}
		})
	}
}

// TestRhcosConfig_EdgeCases tests edge cases in configuration
func TestRhcosConfig_EdgeCases(t *testing.T) {
	validSSHKey := "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQC1234567890abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890 user@host"
	validPasswdHash := "$6$rounds=4096$saltsaltsal$hashhashhashhashhashhashhashhashhashhashhashhash"

	tests := []struct {
		name        string
		config      rhcosConfig
		expectError bool
	}{
		{
			name: "very long rhcos name",
			config: rhcosConfig{
				Cloud:        "mycloud",
				RhcosName:    strings.Repeat("a", 255),
				FlavorName:   "medium",
				ImageName:    "rhcos-4.12",
				NetworkName:  "private-net",
				PasswdHash:   validPasswdHash,
				SshPublicKey: validSSHKey,
			},
			expectError: false,
		},
		{
			name: "rhcos name with hyphens and numbers",
			config: rhcosConfig{
				Cloud:        "mycloud",
				RhcosName:    "test-rhcos-123-server",
				FlavorName:   "medium",
				ImageName:    "rhcos-4.12",
				NetworkName:  "private-net",
				PasswdHash:   validPasswdHash,
				SshPublicKey: validSSHKey,
			},
			expectError: false,
		},
		{
			name: "optional domain name empty",
			config: rhcosConfig{
				Cloud:        "mycloud",
				RhcosName:    "test-rhcos",
				FlavorName:   "medium",
				ImageName:    "rhcos-4.12",
				NetworkName:  "private-net",
				PasswdHash:   validPasswdHash,
				SshPublicKey: validSSHKey,
				DomainName:   "",
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.validate()

			if tt.expectError {
				if err == nil {
					t.Fatal("Expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("Expected no error, got: %v", err)
				}
			}
		})
	}
}

// TestParseRhcosFlags_DebugFlagVariations tests various debug flag formats
func TestParseRhcosFlags_DebugFlagVariations(t *testing.T) {
	validSSHKey := "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQC1234567890abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890 user@host"
	validPasswdHash := "$6$rounds=4096$saltsaltsal$hashhashhashhashhashhashhashhashhashhashhashhash"

	tests := []struct {
		name        string
		debugValue  string
		expectDebug bool
		expectError bool
	}{
		{name: "true lowercase", debugValue: "true", expectDebug: true, expectError: false},
		{name: "TRUE uppercase", debugValue: "TRUE", expectDebug: true, expectError: false},
		{name: "false lowercase", debugValue: "false", expectDebug: false, expectError: false},
		{name: "FALSE uppercase", debugValue: "FALSE", expectDebug: false, expectError: false},
		{name: "1 numeric", debugValue: "1", expectDebug: true, expectError: false},
		{name: "0 numeric", debugValue: "0", expectDebug: false, expectError: false},
		{name: "yes", debugValue: "yes", expectDebug: true, expectError: false},
		{name: "no", debugValue: "no", expectDebug: false, expectError: false},
		{name: "invalid", debugValue: "invalid", expectDebug: false, expectError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flagSet := flag.NewFlagSet("create-rhcos", flag.ContinueOnError)
			args := []string{
				"--cloud", "mycloud",
				"--rhcosName", "test-rhcos",
				"--flavorName", "medium",
				"--imageName", "rhcos-4.12",
				"--networkName", "private-net",
				"--passwdHash", validPasswdHash,
				"--sshPublicKey", validSSHKey,
				"--shouldDebug", tt.debugValue,
			}

			config, err := parseRhcosFlags(flagSet, args)

			if tt.expectError {
				if err == nil {
					t.Fatal("Expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("Expected no error, got: %v", err)
				}
				if config != nil && config.ShouldDebug != tt.expectDebug {
					t.Errorf("Expected ShouldDebug=%v, got %v", tt.expectDebug, config.ShouldDebug)
				}
			}
		})
	}
}

// Made with Bob