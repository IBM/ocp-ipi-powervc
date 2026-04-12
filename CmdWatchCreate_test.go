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

// TestWatchCreateClusterCommand_NilFlagSet tests that the function returns an error when flagSet is nil
func TestWatchCreateClusterCommand_NilFlagSet(t *testing.T) {
	err := watchCreateClusterCommand(nil, []string{})

	if err == nil {
		t.Fatal("Expected error for nil flag set, got nil")
	}

	expectedMsg := "flag set cannot be nil"
	if !strings.Contains(err.Error(), expectedMsg) {
		t.Errorf("Expected error message to contain %q, got: %v", expectedMsg, err)
	}
}

// TestWatchCreateClusterCommand_MissingRequiredFlags tests that the function returns errors for missing required flags
func TestWatchCreateClusterCommand_MissingRequiredFlags(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		errorMsg string
	}{
		{
			name:     "no flags provided",
			args:     []string{},
			errorMsg: "cloud name is required",
		},
		{
			name:     "empty cloud",
			args:     []string{"--cloud", ""},
			errorMsg: "cloud name is required",
		},
		{
			name:     "missing metadata",
			args:     []string{"--cloud", "mycloud"},
			errorMsg: "metadata file location is required",
		},
		{
			name: "empty metadata",
			args: []string{
				"--cloud", "mycloud",
				"--metadata", "",
			},
			errorMsg: "metadata file location is required",
		},
		{
			name: "missing bastionUsername",
			args: []string{
				"--cloud", "mycloud",
				"--metadata", "/tmp/metadata.json",
			},
			errorMsg: "bastion username is required",
		},
		{
			name: "empty bastionUsername",
			args: []string{
				"--cloud", "mycloud",
				"--metadata", "/tmp/metadata.json",
				"--bastionUsername", "",
			},
			errorMsg: "bastion username is required",
		},
		{
			name: "missing bastionRsa",
			args: []string{
				"--cloud", "mycloud",
				"--metadata", "/tmp/metadata.json",
				"--bastionUsername", "core",
			},
			errorMsg: "bastion RSA key is required",
		},
		{
			name: "empty bastionRsa",
			args: []string{
				"--cloud", "mycloud",
				"--metadata", "/tmp/metadata.json",
				"--bastionUsername", "core",
				"--bastionRsa", "",
			},
			errorMsg: "bastion RSA key is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flagSet := flag.NewFlagSet("watch-create", flag.ContinueOnError)
			err := watchCreateClusterCommand(flagSet, tt.args)

			if err == nil {
				t.Fatal("Expected error for missing required flag, got nil")
			}

			if !strings.Contains(err.Error(), tt.errorMsg) {
				t.Errorf("Expected error message to contain %q, got: %v", tt.errorMsg, err)
			}
		})
	}
}

// TestWatchCreateClusterCommand_InvalidDebugFlag tests that the function returns an error for invalid debug flag values
func TestWatchCreateClusterCommand_InvalidDebugFlag(t *testing.T) {
	tempDir := t.TempDir()
	metadataPath := filepath.Join(tempDir, "metadata.json")
	rsaPath := filepath.Join(tempDir, "id_rsa")

	// Create test files
	if err := os.WriteFile(metadataPath, []byte(`{"infraID":"test"}`), 0644); err != nil {
		t.Fatalf("Failed to create test metadata file: %v", err)
	}
	if err := os.WriteFile(rsaPath, []byte("test-key"), 0600); err != nil {
		t.Fatalf("Failed to create test RSA file: %v", err)
	}

	tests := []struct {
		name       string
		debugValue string
	}{
		{
			name:       "invalid value",
			debugValue: "invalid",
		},
		{
			name:       "numeric value",
			debugValue: "2",
		},
		{
			name:       "mixed case invalid",
			debugValue: "TRUE1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flagSet := flag.NewFlagSet("watch-create", flag.ContinueOnError)
			args := []string{
				"--cloud", "mycloud",
				"--metadata", metadataPath,
				"--bastionUsername", "core",
				"--bastionRsa", rsaPath,
				"--shouldDebug", tt.debugValue,
			}
			err := watchCreateClusterCommand(flagSet, args)

			if err == nil {
				t.Fatalf("Expected error for invalid debug flag %q, got nil", tt.debugValue)
			}

			// The error should mention the flag name
			if !strings.Contains(err.Error(), "shouldDebug") {
				t.Errorf("Expected error message to mention shouldDebug flag, got: %v", err)
			}
		})
	}
}

// TestWatchCreateClusterCommand_ValidDebugFlags tests that valid debug flag values are accepted
func TestWatchCreateClusterCommand_ValidDebugFlags(t *testing.T) {
	tempDir := t.TempDir()
	metadataPath := filepath.Join(tempDir, "metadata.json")
	rsaPath := filepath.Join(tempDir, "id_rsa")

	// Create test files
	if err := os.WriteFile(metadataPath, []byte(`{"infraID":"test"}`), 0644); err != nil {
		t.Fatalf("Failed to create test metadata file: %v", err)
	}
	if err := os.WriteFile(rsaPath, []byte("test-key"), 0600); err != nil {
		t.Fatalf("Failed to create test RSA file: %v", err)
	}

	tests := []struct {
		name       string
		debugValue string
	}{
		{name: "true lowercase", debugValue: "true"},
		{name: "false lowercase", debugValue: "false"},
		{name: "TRUE uppercase", debugValue: "TRUE"},
		{name: "FALSE uppercase", debugValue: "FALSE"},
		{name: "1 numeric", debugValue: "1"},
		{name: "0 numeric", debugValue: "0"},
		{name: "yes", debugValue: "yes"},
		{name: "no", debugValue: "no"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flagSet := flag.NewFlagSet("watch-create", flag.ContinueOnError)
			args := []string{
				"--cloud", "mycloud",
				"--metadata", metadataPath,
				"--bastionUsername", "core",
				"--bastionRsa", rsaPath,
				"--shouldDebug", tt.debugValue,
			}

			// We expect this to fail at a later stage (metadata parsing or services creation),
			// not at flag parsing
			err := watchCreateClusterCommand(flagSet, args)

			// The error should NOT be about invalid flag
			if err != nil && strings.Contains(err.Error(), "must be 'true' or 'false'") {
				t.Errorf("Debug flag %q should be valid but got parsing error: %v", tt.debugValue, err)
			}
		})
	}
}

// TestWatchCreateClusterCommand_MetadataFileValidation tests metadata file validation
func TestWatchCreateClusterCommand_MetadataFileValidation(t *testing.T) {
	tempDir := t.TempDir()
	rsaPath := filepath.Join(tempDir, "id_rsa")

	// Create test RSA file
	if err := os.WriteFile(rsaPath, []byte("test-key"), 0600); err != nil {
		t.Fatalf("Failed to create test RSA file: %v", err)
	}

	tests := []struct {
		name         string
		metadataPath string
		errorMsg     string
	}{
		{
			name:         "non-existent file",
			metadataPath: filepath.Join(tempDir, "nonexistent.json"),
			errorMsg:     "failed to read metadata file",
		},
		{
			name:         "directory instead of file",
			metadataPath: tempDir,
			errorMsg:     "failed to read metadata file",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flagSet := flag.NewFlagSet("watch-create", flag.ContinueOnError)
			args := []string{
				"--cloud", "mycloud",
				"--metadata", tt.metadataPath,
				"--bastionUsername", "core",
				"--bastionRsa", rsaPath,
			}
			err := watchCreateClusterCommand(flagSet, args)

			if err == nil {
				t.Fatal("Expected error for invalid metadata file, got nil")
			}

			if !strings.Contains(err.Error(), tt.errorMsg) {
				t.Errorf("Expected error message to contain %q, got: %v", tt.errorMsg, err)
			}
		})
	}
}

// TestWatchCreateClusterCommand_OptionalFlags tests optional flag handling
func TestWatchCreateClusterCommand_OptionalFlags(t *testing.T) {
	tempDir := t.TempDir()
	metadataPath := filepath.Join(tempDir, "metadata.json")
	rsaPath := filepath.Join(tempDir, "id_rsa")
	kubeconfigPath := filepath.Join(tempDir, "kubeconfig")

	// Create test files
	if err := os.WriteFile(metadataPath, []byte(`{"infraID":"test"}`), 0644); err != nil {
		t.Fatalf("Failed to create test metadata file: %v", err)
	}
	if err := os.WriteFile(rsaPath, []byte("test-key"), 0600); err != nil {
		t.Fatalf("Failed to create test RSA file: %v", err)
	}
	if err := os.WriteFile(kubeconfigPath, []byte("test-kubeconfig"), 0644); err != nil {
		t.Fatalf("Failed to create test kubeconfig file: %v", err)
	}

	tests := []struct {
		name string
		args []string
	}{
		{
			name: "with kubeconfig",
			args: []string{
				"--cloud", "mycloud",
				"--metadata", metadataPath,
				"--bastionUsername", "core",
				"--bastionRsa", rsaPath,
				"--kubeconfig", kubeconfigPath,
			},
		},
		{
			name: "with baseDomain",
			args: []string{
				"--cloud", "mycloud",
				"--metadata", metadataPath,
				"--bastionUsername", "core",
				"--bastionRsa", rsaPath,
				"--baseDomain", "example.com",
			},
		},
		{
			name: "with all optional flags",
			args: []string{
				"--cloud", "mycloud",
				"--metadata", metadataPath,
				"--bastionUsername", "core",
				"--bastionRsa", rsaPath,
				"--kubeconfig", kubeconfigPath,
				"--baseDomain", "example.com",
				"--shouldDebug", "true",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flagSet := flag.NewFlagSet("watch-create", flag.ContinueOnError)
			err := watchCreateClusterCommand(flagSet, tt.args)

			// We expect this to fail at a later stage (services creation or status query),
			// not at flag parsing or validation
			if err != nil && (strings.Contains(err.Error(), "required") ||
				strings.Contains(err.Error(), "must be 'true' or 'false'")) {
				t.Errorf("Optional flags should be accepted, got validation error: %v", err)
			}
		})
	}
}

// TestWatchCreateClusterCommand_FlagParsing tests that flags are parsed correctly
func TestWatchCreateClusterCommand_FlagParsing(t *testing.T) {
	tempDir := t.TempDir()
	metadataPath := filepath.Join(tempDir, "metadata.json")
	rsaPath := filepath.Join(tempDir, "id_rsa")

	// Create test files
	if err := os.WriteFile(metadataPath, []byte(`{"infraID":"test"}`), 0644); err != nil {
		t.Fatalf("Failed to create test metadata file: %v", err)
	}
	if err := os.WriteFile(rsaPath, []byte("test-key"), 0600); err != nil {
		t.Fatalf("Failed to create test RSA file: %v", err)
	}

	tests := []struct {
		name        string
		args        []string
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid minimal flags",
			args: []string{
				"--cloud", "mycloud",
				"--metadata", metadataPath,
				"--bastionUsername", "core",
				"--bastionRsa", rsaPath,
			},
			expectError: false, // Function completes successfully even if services fail
			errorMsg:    "",
		},
		{
			name: "unknown flag",
			args: []string{
				"--cloud", "mycloud",
				"--metadata", metadataPath,
				"--bastionUsername", "core",
				"--bastionRsa", rsaPath,
				"--unknownFlag", "value",
			},
			expectError: true,
			errorMsg:    "failed to parse flags",
		},
		{
			name: "duplicate flags",
			args: []string{
				"--cloud", "mycloud",
				"--cloud", "anothercloud",
				"--metadata", metadataPath,
				"--bastionUsername", "core",
				"--bastionRsa", rsaPath,
			},
			expectError: false, // Last value wins, function completes
			errorMsg:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flagSet := flag.NewFlagSet("watch-create", flag.ContinueOnError)
			err := watchCreateClusterCommand(flagSet, tt.args)

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

// TestWatchCreateClusterCommand_ErrorPrefix tests that errors have the correct prefix
func TestWatchCreateClusterCommand_ErrorPrefix(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{
			name: "missing cloud",
			args: []string{},
		},
		{
			name: "missing metadata",
			args: []string{"--cloud", "mycloud"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flagSet := flag.NewFlagSet("watch-create", flag.ContinueOnError)
			err := watchCreateClusterCommand(flagSet, tt.args)

			if err == nil {
				t.Fatal("Expected error, got nil")
			}

			expectedPrefix := "Error:"
			if !strings.Contains(err.Error(), expectedPrefix) {
				t.Errorf("Expected error to contain prefix %q, got: %v", expectedPrefix, err)
			}
		})
	}
}

// TestWatchCreateClusterCommand_Constants tests that constants are used correctly
func TestWatchCreateClusterCommand_Constants(t *testing.T) {
	// Test that the constants are defined and accessible
	if flagWatchCreateCloud == "" {
		t.Error("flagWatchCreateCloud constant should not be empty")
	}
	if flagWatchCreateMetadata == "" {
		t.Error("flagWatchCreateMetadata constant should not be empty")
	}
	if flagWatchCreateKubeConfig == "" {
		t.Error("flagWatchCreateKubeConfig constant should not be empty")
	}
	if flagWatchCreateBastionUsername == "" {
		t.Error("flagWatchCreateBastionUsername constant should not be empty")
	}
	if flagWatchCreateBastionRsa == "" {
		t.Error("flagWatchCreateBastionRsa constant should not be empty")
	}
	if flagWatchCreateBaseDomain == "" {
		t.Error("flagWatchCreateBaseDomain constant should not be empty")
	}
	if flagWatchCreateShouldDebug == "" {
		t.Error("flagWatchCreateShouldDebug constant should not be empty")
	}

	// Test default values
	if defaultWatchCreateCloud != "" {
		t.Error("defaultWatchCreateCloud should be empty string")
	}
	if defaultWatchCreateMetadata != "" {
		t.Error("defaultWatchCreateMetadata should be empty string")
	}
	if defaultWatchCreateKubeConfig != "" {
		t.Error("defaultWatchCreateKubeConfig should be empty string")
	}
	if defaultWatchCreateBastionUsername != "" {
		t.Error("defaultWatchCreateBastionUsername should be empty string")
	}
	if defaultWatchCreateBastionRsa != "" {
		t.Error("defaultWatchCreateBastionRsa should be empty string")
	}
	if defaultWatchCreateBaseDomain != "" {
		t.Error("defaultWatchCreateBaseDomain should be empty string")
	}
	if defaultWatchCreateShouldDebug != "false" {
		t.Errorf("defaultWatchCreateShouldDebug should be 'false', got: %q", defaultWatchCreateShouldDebug)
	}

	// Test error prefix
	if errPrefixWatchCreate == "" {
		t.Error("errPrefixWatchCreate constant should not be empty")
	}

	// Test environment variable name
	if envIBMCloudAPIKey == "" {
		t.Error("envIBMCloudAPIKey constant should not be empty")
	}

	// Test component names
	if componentOpenShift == "" {
		t.Error("componentOpenShift constant should not be empty")
	}
	if componentVMs == "" {
		t.Error("componentVMs constant should not be empty")
	}
	if componentLB == "" {
		t.Error("componentLB constant should not be empty")
	}
	if componentDNS == "" {
		t.Error("componentDNS constant should not be empty")
	}
}

// TestWatchCreateClusterCommand_FlagDefaults tests that default values are set correctly
func TestWatchCreateClusterCommand_FlagDefaults(t *testing.T) {
	flagSet := flag.NewFlagSet("watch-create", flag.ContinueOnError)

	// Define flags without parsing
	cloud := flagSet.String(flagWatchCreateCloud, defaultWatchCreateCloud, usageWatchCreateCloud)
	metadata := flagSet.String(flagWatchCreateMetadata, defaultWatchCreateMetadata, usageWatchCreateMetadata)
	kubeconfig := flagSet.String(flagWatchCreateKubeConfig, defaultWatchCreateKubeConfig, usageWatchCreateKubeConfig)
	bastionUsername := flagSet.String(flagWatchCreateBastionUsername, defaultWatchCreateBastionUsername, usageWatchCreateBastionUsername)
	bastionRsa := flagSet.String(flagWatchCreateBastionRsa, defaultWatchCreateBastionRsa, usageWatchCreateBastionRsa)
	baseDomain := flagSet.String(flagWatchCreateBaseDomain, defaultWatchCreateBaseDomain, usageWatchCreateBaseDomain)
	shouldDebug := flagSet.String(flagWatchCreateShouldDebug, defaultWatchCreateShouldDebug, usageWatchCreateShouldDebug)

	// Check defaults before parsing
	if *cloud != "" {
		t.Errorf("Default cloud should be empty, got: %q", *cloud)
	}
	if *metadata != "" {
		t.Errorf("Default metadata should be empty, got: %q", *metadata)
	}
	if *kubeconfig != "" {
		t.Errorf("Default kubeconfig should be empty, got: %q", *kubeconfig)
	}
	if *bastionUsername != "" {
		t.Errorf("Default bastionUsername should be empty, got: %q", *bastionUsername)
	}
	if *bastionRsa != "" {
		t.Errorf("Default bastionRsa should be empty, got: %q", *bastionRsa)
	}
	if *baseDomain != "" {
		t.Errorf("Default baseDomain should be empty, got: %q", *baseDomain)
	}
	if *shouldDebug != "false" {
		t.Errorf("Default shouldDebug should be 'false', got: %q", *shouldDebug)
	}
}

// TestWatchCreateClusterCommand_MultipleInvocations tests that the function can be called multiple times
func TestWatchCreateClusterCommand_MultipleInvocations(t *testing.T) {
	// First invocation
	flagSet1 := flag.NewFlagSet("watch-create-1", flag.ContinueOnError)
	err1 := watchCreateClusterCommand(flagSet1, []string{})
	if err1 == nil {
		t.Error("First invocation: expected error for missing cloud")
	}

	// Second invocation
	flagSet2 := flag.NewFlagSet("watch-create-2", flag.ContinueOnError)
	err2 := watchCreateClusterCommand(flagSet2, []string{})
	if err2 == nil {
		t.Error("Second invocation: expected error for missing cloud")
	}

	// Both should have similar errors
	if err1.Error() != err2.Error() {
		t.Errorf("Multiple invocations should produce consistent errors.\nFirst: %v\nSecond: %v", err1, err2)
	}
}

// TestWatchCreateClusterCommand_EdgeCases tests edge cases and boundary conditions
func TestWatchCreateClusterCommand_EdgeCases(t *testing.T) {
	tempDir := t.TempDir()
	metadataPath := filepath.Join(tempDir, "metadata.json")
	rsaPath := filepath.Join(tempDir, "id_rsa")

	// Create test files
	if err := os.WriteFile(metadataPath, []byte(`{"infraID":"test"}`), 0644); err != nil {
		t.Fatalf("Failed to create test metadata file: %v", err)
	}
	if err := os.WriteFile(rsaPath, []byte("test-key"), 0600); err != nil {
		t.Fatalf("Failed to create test RSA file: %v", err)
	}

	tests := []struct {
		name        string
		args        []string
		expectError bool
		errorMsg    string
	}{
		{
			name:        "empty args",
			args:        []string{},
			expectError: true,
			errorMsg:    "cloud name is required",
		},
		{
			name:        "only debug flag",
			args:        []string{"--shouldDebug", "true"},
			expectError: true,
			errorMsg:    "cloud name is required",
		},
		{
			name: "cloud with spaces",
			args: []string{
				"--cloud", "  mycloud  ",
				"--metadata", metadataPath,
				"--bastionUsername", "core",
				"--bastionRsa", rsaPath,
			},
			expectError: false, // Function completes, spaces are preserved in cloud name
			errorMsg:    "",
		},
		{
			name: "whitespace cloud",
			args: []string{
				"--cloud", "   ",
				"--metadata", metadataPath,
				"--bastionUsername", "core",
				"--bastionRsa", rsaPath,
			},
			expectError: false, // Function completes, whitespace is preserved
			errorMsg:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flagSet := flag.NewFlagSet("watch-create", flag.ContinueOnError)
			err := watchCreateClusterCommand(flagSet, tt.args)

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

// TestWatchCreateClusterCommand_IBMCloudAPIKey tests IBM Cloud API key handling
func TestWatchCreateClusterCommand_IBMCloudAPIKey(t *testing.T) {
	tempDir := t.TempDir()
	metadataPath := filepath.Join(tempDir, "metadata.json")
	rsaPath := filepath.Join(tempDir, "id_rsa")

	// Create test files
	if err := os.WriteFile(metadataPath, []byte(`{"infraID":"test"}`), 0644); err != nil {
		t.Fatalf("Failed to create test metadata file: %v", err)
	}
	if err := os.WriteFile(rsaPath, []byte("test-key"), 0600); err != nil {
		t.Fatalf("Failed to create test RSA file: %v", err)
	}

	// Save original API key
	originalAPIKey := os.Getenv(envIBMCloudAPIKey)
	defer func() {
		if originalAPIKey == "" {
			os.Unsetenv(envIBMCloudAPIKey)
		} else {
			os.Setenv(envIBMCloudAPIKey, originalAPIKey)
		}
	}()

	tests := []struct {
		name   string
		apiKey string
	}{
		{
			name:   "no API key",
			apiKey: "",
		},
		{
			name:   "invalid API key",
			apiKey: "invalid-api-key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.apiKey == "" {
				os.Unsetenv(envIBMCloudAPIKey)
			} else {
				os.Setenv(envIBMCloudAPIKey, tt.apiKey)
			}

			flagSet := flag.NewFlagSet("watch-create", flag.ContinueOnError)
			args := []string{
				"--cloud", "mycloud",
				"--metadata", metadataPath,
				"--bastionUsername", "core",
				"--bastionRsa", rsaPath,
			}

			err := watchCreateClusterCommand(flagSet, args)

			// We expect this to fail at some stage, but not necessarily at API key validation
			// if no API key is provided (it's optional)
			if tt.apiKey == "" && err != nil && strings.Contains(err.Error(), "IBM Cloud") {
				t.Errorf("No API key should be optional, but got IBM Cloud error: %v", err)
			}
		})
	}
}

// Made with Bob