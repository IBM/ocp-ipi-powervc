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
	"time"
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
				Clouds:      []string{ "mycloud", },
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
				Clouds:      []string{ "mycloud", },
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
			name: "cloud empty",
			config: &BastionConfig{
				Clouds:      []string{ "", },
			},
			expectError: true,
			errorMsg:    "cloud: field is required",
		},
		{
			name: "extra cloud",
			config: &BastionConfig{
				Clouds:      []string{ "mycloud1", "mycloud2" },
			},
			expectError: true,
			errorMsg:    "cloud: only one cloud is allowed",
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
				Clouds:      []string{ "mycloud", },
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
				Clouds:      []string{ "mycloud", },
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
				Clouds:      []string{ "mycloud", },
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
				Clouds:      []string{ "mycloud", },
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
				Clouds:      []string{ "mycloud", },
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
				Clouds:      []string{ "mycloud", },
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
		Clouds:      []string{ "mycloud", },
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
			Clouds:        []string{ "mycloud", },
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
			Clouds:      []string{ "mycloud", },
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

func TestNewSSHConfig(t *testing.T) {
	cfg := newSSHConfig("192.168.1.100", "/path/to/key")

	if cfg.Host != "192.168.1.100" {
		t.Errorf("expected host 192.168.1.100, got %s", cfg.Host)
	}
	if cfg.User != sshUser {
		t.Errorf("expected user %s, got %s", sshUser, cfg.User)
	}
	if cfg.KeyPath != "/path/to/key" {
		t.Errorf("expected keyPath /path/to/key, got %s", cfg.KeyPath)
	}
	if cfg.MaxRetries != maxSSHRetries {
		t.Errorf("expected maxRetries %d, got %d", maxSSHRetries, cfg.MaxRetries)
	}
	if cfg.RetryDelay != sshRetryDelay {
		t.Errorf("expected retryDelay %v, got %v", sshRetryDelay, cfg.RetryDelay)
	}
}

func TestRemoveCommentLines(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "no comments",
			input:    "line1\nline2\nline3",
			expected: "line1\nline2\nline3",
		},
		{
			name:     "with comments",
			input:    "line1\n# comment\nline2\n# another comment\nline3",
			expected: "line1\nline2\nline3",
		},
		{
			name:     "empty lines",
			input:    "line1\n\nline2\n\n\nline3",
			expected: "line1\nline2\nline3",
		},
		{
			name:     "mixed comments and empty lines",
			input:    "line1\n# comment\n\nline2\n  # indented comment\n\nline3",
			expected: "line1\nline2\nline3",
		},
		{
			name:     "only comments",
			input:    "# comment1\n# comment2\n# comment3",
			expected: "",
		},
		{
			name:     "empty input",
			input:    "",
			expected: "",
		},
		{
			name:     "whitespace only lines",
			input:    "line1\n   \n\t\nline2",
			expected: "line1\nline2",
		},
		{
			name:     "comment at start of line with spaces",
			input:    "line1\n  # comment with leading spaces\nline2",
			expected: "line1\nline2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := removeCommentLines(tt.input)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestDNSRecord(t *testing.T) {
	record := dnsRecord{
		recordType: "A",
		name:       "api.example.com",
		content:    "192.168.1.100",
	}

	if record.recordType != "A" {
		t.Errorf("expected recordType A, got %s", record.recordType)
	}
	if record.name != "api.example.com" {
		t.Errorf("expected name api.example.com, got %s", record.name)
	}
	if record.content != "192.168.1.100" {
		t.Errorf("expected content 192.168.1.100, got %s", record.content)
	}
}

func TestCleanupBastionIPFile(t *testing.T) {
	// Test when file doesn't exist
	t.Run("file does not exist", func(t *testing.T) {
		tempDir := t.TempDir()
		testFile := filepath.Join(tempDir, "test-bastion-ip")
		
		// File doesn't exist - should not error
		err := os.Remove(testFile)
		if err != nil && !os.IsNotExist(err) {
			t.Errorf("unexpected error: %v", err)
		}
	})

	// Test when file exists
	t.Run("file exists", func(t *testing.T) {
		tempDir := t.TempDir()
		testFile := filepath.Join(tempDir, "test-bastion-ip")
		
		// Create file
		if err := os.WriteFile(testFile, []byte("192.168.1.100"), 0644); err != nil {
			t.Fatalf("failed to create test file: %v", err)
		}

		// Remove file
		err := os.Remove(testFile)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}

		// Verify file is gone
		if _, err := os.Stat(testFile); !os.IsNotExist(err) {
			t.Error("expected file to be removed")
		}
	})
}

func TestAppendToFile(t *testing.T) {
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "test-append")

	// Create initial file
	initialContent := []byte("initial content\n")
	if err := os.WriteFile(testFile, initialContent, 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Test appending data
	appendData := []byte("appended content\n")
	if err := appendToFile(testFile, appendData); err != nil {
		t.Fatalf("appendToFile failed: %v", err)
	}

	// Verify content
	content, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("failed to read file: %v", err)
	}

	expected := string(initialContent) + string(appendData)
	if string(content) != expected {
		t.Errorf("expected %q, got %q", expected, string(content))
	}
}

func TestAppendToFile_NonExistentFile(t *testing.T) {
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "nonexistent")

	err := appendToFile(testFile, []byte("test"))
	if err == nil {
		t.Error("expected error for nonexistent file, got nil")
	}
}

func TestSSHConfig_Struct(t *testing.T) {
	retryDelay := 10 * time.Second
	cfg := &sshConfig{
		Host:       "192.168.1.100",
		User:       "testuser",
		KeyPath:    "/path/to/key",
		MaxRetries: 5,
		RetryDelay: retryDelay,
	}

	if cfg.Host != "192.168.1.100" {
		t.Errorf("expected host 192.168.1.100, got %s", cfg.Host)
	}
	if cfg.User != "testuser" {
		t.Errorf("expected user testuser, got %s", cfg.User)
	}
	if cfg.KeyPath != "/path/to/key" {
		t.Errorf("expected keyPath /path/to/key, got %s", cfg.KeyPath)
	}
	if cfg.MaxRetries != 5 {
		t.Errorf("expected maxRetries 5, got %d", cfg.MaxRetries)
	}
	if cfg.RetryDelay != retryDelay {
		t.Errorf("expected retryDelay %v, got %v", retryDelay, cfg.RetryDelay)
	}
}

func TestBastionConfig_String_NoRSA(t *testing.T) {
	config := &BastionConfig{
		Clouds:        []string{ "mycloud", },
		BastionName:   "bastion-1",
		FlavorName:    "m1.small",
		ImageName:     "rhel-8",
		NetworkName:   "private",
		SshKeyName:    "mykey",
		DomainName:    "example.com",
		EnableHAProxy: true,
		ServerIP:      "192.168.1.100",
		ShouldDebug:   false,
	}

	result := config.String()
	if !strings.Contains(result, "RSA=<not set>") {
		t.Errorf("expected RSA=<not set> in output, got %q", result)
	}
	if strings.Contains(result, "<redacted>") {
		t.Error("did not expect <redacted> when BastionRsa is empty")
	}
}

func TestBastionConfig_ValidationCaching(t *testing.T) {
	tempKeyDir := t.TempDir()
	validKeyPath := filepath.Join(tempKeyDir, "id_rsa")
	if err := os.WriteFile(validKeyPath, []byte("test-private-key"), 0600); err != nil {
		t.Fatalf("failed to create test key: %v", err)
	}

	config := &BastionConfig{
		Clouds:      []string{ "mycloud", },
		BastionName: "bastion-1",
		BastionRsa:  validKeyPath,
		FlavorName:  "m1.small",
		ImageName:   "rhel-8",
		NetworkName: "private",
		SshKeyName:  "mykey",
	}

	// First validation
	if err := config.Validate(); err != nil {
		t.Fatalf("first validation failed: %v", err)
	}

	if !config.validated {
		t.Error("expected validated flag to be true")
	}

	// Second validation should use cache
	if err := config.Validate(); err != nil {
		t.Fatalf("cached validation failed: %v", err)
	}
}

func TestBastionConfig_MultipleValidationErrors(t *testing.T) {
	config := &BastionConfig{
		// Missing all required fields
	}

	err := config.Validate()
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}

	errMsg := err.Error()
	// Should contain multiple error messages
	if !strings.Contains(errMsg, "cloud: field is required") {
		t.Error("expected cloud error in validation message")
	}
	if !strings.Contains(errMsg, "bastionName: field is required") {
		t.Error("expected bastionName error in validation message")
	}
	if !strings.Contains(errMsg, "setup mode") {
		t.Error("expected setup mode error in validation message")
	}
}

func TestBastionConfig_EdgeCases(t *testing.T) {
	t.Run("bastion name with valid special characters", func(t *testing.T) {
		config := &BastionConfig{
			Clouds:      []string{ "mycloud", },
			BastionName: "bastion-1_test",
			ServerIP:    "192.168.1.100",
			FlavorName:  "m1.small",
			ImageName:   "rhel-8",
			NetworkName: "private",
			SshKeyName:  "mykey",
		}

		if err := config.Validate(); err != nil {
			t.Errorf("expected valid name with hyphens and underscores, got error: %v", err)
		}
	})

	t.Run("bastion name with invalid characters", func(t *testing.T) {
		invalidNames := []string{
			"bastion@1",
			"bastion.1",
			"bastion 1",
			"bastion#1",
			"bastion$1",
		}

		for _, name := range invalidNames {
			config := &BastionConfig{
				Clouds:      []string{ "mycloud", },
				BastionName: name,
				ServerIP:    "192.168.1.100",
				FlavorName:  "m1.small",
				ImageName:   "rhel-8",
				NetworkName: "private",
				SshKeyName:  "mykey",
			}

			err := config.Validate()
			if err == nil {
				t.Errorf("expected error for invalid name %q, got nil", name)
			}
			if !strings.Contains(err.Error(), "invalid characters") {
				t.Errorf("expected invalid characters error for %q, got %v", name, err)
			}
		}
	})

	t.Run("various IP address formats", func(t *testing.T) {
		validIPs := []string{
			"192.168.1.100",
			"10.0.0.1",
			"172.16.0.1",
			"::1",
			"2001:db8::1",
		}

		for _, ip := range validIPs {
			config := &BastionConfig{
				Clouds:      []string{ "mycloud", },
				BastionName: "bastion-1",
				ServerIP:    ip,
				FlavorName:  "m1.small",
				ImageName:   "rhel-8",
				NetworkName: "private",
				SshKeyName:  "mykey",
			}

			if err := config.Validate(); err != nil {
				t.Errorf("expected valid IP %q, got error: %v", ip, err)
			}
		}
	})
}

// Made with Bob
