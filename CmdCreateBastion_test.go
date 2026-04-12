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
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewBastionConfig(t *testing.T) {
	config := NewBastionConfig()

	if !config.EnableHAProxy {
		t.Error("Expected EnableHAProxy to be true by default")
	}
	if config.ShouldDebug {
		t.Error("Expected ShouldDebug to be false by default")
	}
	if config.validated {
		t.Error("Expected validated to be false by default")
	}
}

func TestNewBastionConfigWithDefaults(t *testing.T) {
	config := NewBastionConfigWithDefaults(false, true)

	if config.EnableHAProxy {
		t.Error("Expected EnableHAProxy to be false")
	}
	if !config.ShouldDebug {
		t.Error("Expected ShouldDebug to be true")
	}
}

func TestBastionConfigValidate(t *testing.T) {
	tempKeyDir := t.TempDir()
	validKeyPath := filepath.Join(tempKeyDir, "id_rsa")
	if err := os.WriteFile(validKeyPath, []byte("test-private-key"), 0600); err != nil {
		t.Fatalf("failed to create test key: %v", err)
	}

	tests := []struct {
		name        string
		config      *BastionConfig
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid local setup",
			config: &BastionConfig{
				Cloud:       "mycloud",
				BastionName: "bastion-1",
				BastionRsa:  validKeyPath,
				FlavorName:  "m1.small",
				ImageName:   "rhel-8",
				NetworkName: "private",
				SshKeyName:  "mykey",
			},
			expectError: false,
		},
		{
			name: "valid remote setup",
			config: &BastionConfig{
				Cloud:       "mycloud",
				BastionName: "bastion_1",
				ServerIP:    "192.168.122.10",
				FlavorName:  "m1.small",
				ImageName:   "rhel-8",
				NetworkName: "private",
				SshKeyName:  "mykey",
			},
			expectError: false,
		},
		{
			name: "missing required fields and setup mode",
			config: &BastionConfig{
				EnableHAProxy: true,
			},
			expectError: true,
			errorMsg:    "cloud: field is required",
		},
		{
			name: "invalid bastion name",
			config: &BastionConfig{
				Cloud:       "mycloud",
				BastionName: "bastion@1",
				ServerIP:    "192.168.122.10",
				FlavorName:  "m1.small",
				ImageName:   "rhel-8",
				NetworkName: "private",
				SshKeyName:  "mykey",
			},
			expectError: true,
			errorMsg:    "bastionName: contains invalid characters",
		},
		{
			name: "missing both setup modes",
			config: &BastionConfig{
				Cloud:       "mycloud",
				BastionName: "bastion-1",
				FlavorName:  "m1.small",
				ImageName:   "rhel-8",
				NetworkName: "private",
				SshKeyName:  "mykey",
			},
			expectError: true,
			errorMsg:    "either bastionRsa (local) or serverIP (remote) must be specified",
		},
		{
			name: "both setup modes specified",
			config: &BastionConfig{
				Cloud:       "mycloud",
				BastionName: "bastion-1",
				BastionRsa:  validKeyPath,
				ServerIP:    "192.168.122.10",
				FlavorName:  "m1.small",
				ImageName:   "rhel-8",
				NetworkName: "private",
				SshKeyName:  "mykey",
			},
			expectError: true,
			errorMsg:    "bastionRsa and serverIP are mutually exclusive",
		},
		{
			name: "missing bastion rsa file",
			config: &BastionConfig{
				Cloud:       "mycloud",
				BastionName: "bastion-1",
				BastionRsa:  filepath.Join(tempKeyDir, "missing-key"),
				FlavorName:  "m1.small",
				ImageName:   "rhel-8",
				NetworkName: "private",
				SshKeyName:  "mykey",
			},
			expectError: true,
			errorMsg:    "bastionRsa: file not found",
		},
		{
			name: "invalid server ip",
			config: &BastionConfig{
				Cloud:       "mycloud",
				BastionName: "bastion-1",
				ServerIP:    "not-an-ip",
				FlavorName:  "m1.small",
				ImageName:   "rhel-8",
				NetworkName: "private",
				SshKeyName:  "mykey",
			},
			expectError: true,
			errorMsg:    "serverIP: invalid IP address format",
		},
		{
			name: "missing openstack fields",
			config: &BastionConfig{
				Cloud:       "mycloud",
				BastionName: "bastion-1",
				ServerIP:    "192.168.122.10",
			},
			expectError: true,
			errorMsg:    "flavorName: field is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()

			if tt.expectError {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errorMsg != "" && !strings.Contains(err.Error(), tt.errorMsg) {
					t.Fatalf("expected error containing %q, got %v", tt.errorMsg, err)
				}
			} else if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		})
	}
}

func TestBastionConfigValidate_CachesSuccess(t *testing.T) {
	tempKeyDir := t.TempDir()
	validKeyPath := filepath.Join(tempKeyDir, "id_rsa")
	if err := os.WriteFile(validKeyPath, []byte("test-private-key"), 0600); err != nil {
		t.Fatalf("failed to create test key: %v", err)
	}

	config := &BastionConfig{
		Cloud:       "mycloud",
		BastionName: "bastion-1",
		BastionRsa:  validKeyPath,
		FlavorName:  "m1.small",
		ImageName:   "rhel-8",
		NetworkName: "private",
		SshKeyName:  "mykey",
	}

	if err := config.Validate(); err != nil {
		t.Fatalf("first validation failed: %v", err)
	}
	if !config.validated {
		t.Fatal("expected config to be marked validated")
	}

	if err := os.Remove(validKeyPath); err != nil {
		t.Fatalf("failed to remove key after validation: %v", err)
	}

	if err := config.Validate(); err != nil {
		t.Fatalf("expected cached validation success, got %v", err)
	}
}

func TestBastionConfigHelpers(t *testing.T) {
	originalAPIKey := os.Getenv("IBMCLOUD_API_KEY")
	defer func() {
		if originalAPIKey == "" {
			os.Unsetenv("IBMCLOUD_API_KEY")
			return
		}
		os.Setenv("IBMCLOUD_API_KEY", originalAPIKey)
	}()

	t.Run("local setup and string redaction", func(t *testing.T) {
		config := &BastionConfig{
			Cloud:         "mycloud",
			BastionName:   "bastion-1",
			BastionRsa:    "/secret/id_rsa",
			FlavorName:    "m1.small",
			ImageName:     "rhel-8",
			NetworkName:   "private",
			SshKeyName:    "mykey",
			DomainName:    "example.com",
			EnableHAProxy: true,
			ShouldDebug:   true,
		}

		if !config.IsLocalSetup() {
			t.Error("expected local setup")
		}
		if config.IsRemoteSetup() {
			t.Error("did not expect remote setup")
		}

		rendered := config.String()
		if strings.Contains(rendered, "/secret/id_rsa") {
			t.Error("expected rsa path to be redacted in String output")
		}
		if !strings.Contains(rendered, "RSA=<redacted>") {
			t.Errorf("expected redacted rsa marker, got %q", rendered)
		}
	})

	t.Run("remote setup and dns config", func(t *testing.T) {
		config := &BastionConfig{
			Cloud:       "mycloud",
			BastionName: "bastion-1",
			ServerIP:    "192.168.122.10",
			FlavorName:  "m1.small",
			ImageName:   "rhel-8",
			NetworkName: "private",
			SshKeyName:  "mykey",
			DomainName:  "example.com",
		}

		if config.IsLocalSetup() {
			t.Error("did not expect local setup")
		}
		if !config.IsRemoteSetup() {
			t.Error("expected remote setup")
		}

		os.Unsetenv("IBMCLOUD_API_KEY")
		if config.HasDNSConfig() {
			t.Error("did not expect dns config without api key")
		}

		os.Setenv("IBMCLOUD_API_KEY", "test-api-key")
		if !config.HasDNSConfig() {
			t.Error("expected dns config with domain and api key")
		}
	})
}

func TestParseBastionFlags(t *testing.T) {
	tempKeyDir := t.TempDir()
	validKeyPath := filepath.Join(tempKeyDir, "id_rsa")
	if err := os.WriteFile(validKeyPath, []byte("test-private-key"), 0600); err != nil {
		t.Fatalf("failed to create test key: %v", err)
	}

	tests := []struct {
		name        string
		args        []string
		expectError bool
		errorMsg    string
		checkConfig func(*testing.T, *BastionConfig)
	}{
		{
			name: "valid local flags with defaults",
			args: []string{
				"--cloud", "mycloud",
				"--bastionName", "bastion-1",
				"--bastionRsa", validKeyPath,
				"--flavorName", "m1.small",
				"--imageName", "rhel-8",
				"--networkName", "private",
				"--sshKeyName", "mykey",
			},
			checkConfig: func(t *testing.T, config *BastionConfig) {
				if !config.EnableHAProxy {
					t.Error("expected EnableHAProxy default true")
				}
				if config.ShouldDebug {
					t.Error("expected ShouldDebug default false")
				}
				if !config.IsLocalSetup() {
					t.Error("expected local setup")
				}
			},
		},
		{
			name: "valid remote flags with optional values",
			args: []string{
				"--cloud", "mycloud",
				"--bastionName", "bastion-1",
				"--serverIP", "192.168.122.10",
				"--flavorName", "m1.small",
				"--imageName", "rhel-8",
				"--networkName", "private",
				"--sshKeyName", "mykey",
				"--domainName", "example.com",
				"--enableHAProxy", "no",
				"--shouldDebug", "yes",
			},
			checkConfig: func(t *testing.T, config *BastionConfig) {
				if config.EnableHAProxy {
					t.Error("expected EnableHAProxy false")
				}
				if !config.ShouldDebug {
					t.Error("expected ShouldDebug true")
				}
				if config.DomainName != "example.com" {
					t.Errorf("expected domainName example.com, got %q", config.DomainName)
				}
				if !config.IsRemoteSetup() {
					t.Error("expected remote setup")
				}
			},
		},
		{
			name: "invalid boolean flag",
			args: []string{
				"--cloud", "mycloud",
				"--bastionName", "bastion-1",
				"--serverIP", "192.168.122.10",
				"--flavorName", "m1.small",
				"--imageName", "rhel-8",
				"--networkName", "private",
				"--sshKeyName", "mykey",
				"--enableHAProxy", "invalid",
			},
			expectError: true,
			errorMsg:    "enableHAProxy must be 'true' or 'false'",
		},
		{
			name: "unknown flag",
			args: []string{
				"--cloud", "mycloud",
				"--bastionName", "bastion-1",
				"--serverIP", "192.168.122.10",
				"--flavorName", "m1.small",
				"--imageName", "rhel-8",
				"--networkName", "private",
				"--sshKeyName", "mykey",
				"--unknownFlag", "value",
			},
			expectError: true,
			errorMsg:    "failed to parse flags",
		},
		{
			name: "validation error from parsed flags",
			args: []string{
				"--cloud", "mycloud",
				"--bastionName", "bastion-1",
				"--serverIP", "invalid-ip",
				"--flavorName", "m1.small",
				"--imageName", "rhel-8",
				"--networkName", "private",
				"--sshKeyName", "mykey",
			},
			expectError: true,
			errorMsg:    "serverIP: invalid IP address format",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flagSet := flag.NewFlagSet("create-bastion", flag.ContinueOnError)
			config, err := parseBastionFlags(flagSet, tt.args)

			if tt.expectError {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errorMsg != "" && !strings.Contains(err.Error(), tt.errorMsg) {
					t.Fatalf("expected error containing %q, got %v", tt.errorMsg, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if tt.checkConfig != nil {
				tt.checkConfig(t, config)
			}
		})
	}
}

// Made with Bob
