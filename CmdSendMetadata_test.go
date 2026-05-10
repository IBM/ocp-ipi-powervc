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

// TestSendMetadataCommand_NilFlagSet tests that the function returns an error when flagSet is nil
func TestSendMetadataCommand_NilFlagSet(t *testing.T) {
	err := sendMetadataCommand(nil, []string{})

	if err == nil {
		t.Fatal("Expected error for nil flag set, got nil")
	}

	expectedMsg := "flag set cannot be nil"
	if !strings.Contains(err.Error(), expectedMsg) {
		t.Errorf("Expected error message to contain %q, got: %v", expectedMsg, err)
	}
}

// TestSendMetadataCommand_MutualExclusivity tests that create and delete flags are mutually exclusive
func TestSendMetadataCommand_MutualExclusivity(t *testing.T) {
	// Create a valid metadata test file
	tmpFile := createValidMetadataFile(t, "test-metadata.json")
	defer os.Remove(tmpFile)

	tests := []struct {
		name        string
		args        []string
		expectError bool
		errorMsg    string
	}{
		{
			name: "both create and delete specified",
			args: []string{
				"--createMetadata", tmpFile,
				"--deleteMetadata", tmpFile,
				"--serverIP", "192.168.1.100",
				"--timeout", "1s",
			},
			expectError: true,
			errorMsg:    "cannot specify both --createMetadata and --deleteMetadata",
		},
		{
			name:        "neither create nor delete specified",
			args:        []string{"--serverIP", "192.168.1.100"},
			expectError: true,
			errorMsg:    "required flag --createMetadata or --deleteMetadata must be specified",
		},
		{
			name: "only create specified",
			args: []string{
				"--createMetadata", tmpFile,
				"--serverIP", "192.168.1.100",
				"--timeout", "1s",
			},
			expectError: true, // Will fail at connection stage
			errorMsg:    "create failed during metadata transmission: operation cancelled during retry backoff: context deadline exceeded",
		},
		{
			name: "only delete specified",
			args: []string{
				"--deleteMetadata", tmpFile,
				"--serverIP", "192.168.1.100",
				"--timeout", "1s",
			},
			expectError: true, // Will fail at connection stage
			errorMsg:    "delete failed during metadata transmission: operation cancelled during retry backoff: context deadline exceeded",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flagSet := flag.NewFlagSet("send-metadata", flag.ContinueOnError)
			err := sendMetadataCommand(flagSet, tt.args)

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

// TestSendMetadataCommand_MissingServerIP tests that serverIP is required
func TestSendMetadataCommand_MissingServerIP(t *testing.T) {
	tmpFile := createValidMetadataFile(t, "test-metadata.json")
	defer os.Remove(tmpFile)

	tests := []struct {
		name string
		args []string
	}{
		{
			name: "create without serverIP",
			args: []string{"--createMetadata", tmpFile},
		},
		{
			name: "delete without serverIP",
			args: []string{"--deleteMetadata", tmpFile},
		},
		{
			name: "empty serverIP with create",
			args: []string{
				"--createMetadata", tmpFile,
				"--serverIP", "",
			},
		},
		{
			name: "whitespace serverIP with delete",
			args: []string{
				"--deleteMetadata", tmpFile,
				"--serverIP", "   ",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flagSet := flag.NewFlagSet("send-metadata", flag.ContinueOnError)
			err := sendMetadataCommand(flagSet, tt.args)

			if err == nil {
				t.Fatal("Expected error for missing/empty serverIP, got nil")
			}

			expectedMsg := "required flag --serverIP not specified"
			if !strings.Contains(err.Error(), expectedMsg) {
				t.Errorf("Expected error message to contain %q, got: %v", expectedMsg, err)
			}
		})
	}
}

// TestSendMetadataCommand_InvalidServerIP tests that invalid IP addresses are rejected
func TestSendMetadataCommand_InvalidServerIP(t *testing.T) {
	tmpFile := createValidMetadataFile(t, "test-metadata.json")
	defer os.Remove(tmpFile)

	tests := []struct {
		name     string
		serverIP string
	}{
		{
			name:     "invalid IP format",
			serverIP: "999.999.999.999",
		},
		{
			name:     "malformed IP",
			serverIP: "192.168.1",
		},
		{
			name:     "invalid characters",
			serverIP: "192.168.1.abc",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flagSet := flag.NewFlagSet("send-metadata", flag.ContinueOnError)
			args := []string{
				"--createMetadata", tmpFile,
				"--serverIP", tt.serverIP,
			}
			err := sendMetadataCommand(flagSet, args)

			if err == nil {
				t.Fatalf("Expected error for invalid serverIP %q, got nil", tt.serverIP)
			}

			expectedMsg := "invalid IP address or hostname"
			if !strings.Contains(err.Error(), expectedMsg) {
				t.Errorf("Expected error message to contain %q, got: %v", expectedMsg, err)
			}
		})
	}
}

// TestSendMetadataCommand_FileValidation tests that metadata file must exist
func TestSendMetadataCommand_FileValidation(t *testing.T) {
	tests := []struct {
		name        string
		setupFile   func(t *testing.T) string
		cleanupFile func(string)
		expectError bool
		errorMsg    string
	}{
		{
			name: "non-existent file",
			setupFile: func(t *testing.T) string {
				return filepath.Join(os.TempDir(), "non-existent-file-12345.json")
			},
			cleanupFile: func(s string) {},
			expectError: true,
			errorMsg:    "create failed during file validation: file does not exist",
		},
		{
			name: "valid file",
			setupFile: func(t *testing.T) string {
				return createValidMetadataFile(t, "valid-metadata.json")
			},
			cleanupFile: func(s string) { os.Remove(s) },
			expectError: true, // Will fail at connection stage
			errorMsg:    "create failed during metadata transmission: operation cancelled during retry backoff: context deadline exceeded",
		},
		{
			name: "empty filename",
			setupFile: func(t *testing.T) string {
				return ""
			},
			cleanupFile: func(s string) {},
			expectError: true,
			errorMsg:    "required flag --createMetadata or --deleteMetadata must be specified",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filePath := tt.setupFile(t)
			defer tt.cleanupFile(filePath)

			flagSet := flag.NewFlagSet("send-metadata", flag.ContinueOnError)
			args := []string{
				"--createMetadata", filePath,
				"--timeout", "1s",
				"--serverIP", "192.168.1.100",
			}
			err := sendMetadataCommand(flagSet, args)

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

// TestSendMetadataCommand_InvalidDebugFlag tests that invalid debug flag values are rejected
func TestSendMetadataCommand_InvalidDebugFlag(t *testing.T) {
	tmpFile := createValidMetadataFile(t, "test-metadata.json")
	defer os.Remove(tmpFile)

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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flagSet := flag.NewFlagSet("send-metadata", flag.ContinueOnError)
			args := []string{
				"--createMetadata", tmpFile,
				"--serverIP", "192.168.1.100",
				"--timeout", "1s",
				"--shouldDebug", tt.debugValue,
			}
			err := sendMetadataCommand(flagSet, args)

			if err == nil {
				t.Fatalf("Expected error for invalid debug flag %q, got nil", tt.debugValue)
			}

			if !strings.Contains(err.Error(), "shouldDebug") {
				t.Errorf("Expected error message to mention shouldDebug flag, got: %v", err)
			}
		})
	}
}

// TestSendMetadataCommand_ValidDebugFlags tests that valid debug flag values are accepted
func TestSendMetadataCommand_ValidDebugFlags(t *testing.T) {
	tmpFile := createValidMetadataFile(t, "test-metadata.json")
	defer os.Remove(tmpFile)

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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flagSet := flag.NewFlagSet("send-metadata", flag.ContinueOnError)
			args := []string{
				"--createMetadata", tmpFile,
				"--serverIP", "192.168.1.100",
				"--timeout", "1s",
				"--shouldDebug", tt.debugValue,
			}

			err := sendMetadataCommand(flagSet, args)

			// Should fail at connection stage, not at flag parsing
			if err != nil && strings.Contains(err.Error(), "must be 'true' or 'false'") {
				t.Errorf("Debug flag %q should be valid but got parsing error: %v", tt.debugValue, err)
			}
		})
	}
}

// TestSendMetadataCommand_CreateOperation tests create metadata operation
func TestSendMetadataCommand_CreateOperation(t *testing.T) {
	tmpFile := createValidMetadataFile(t, "create-metadata.json")
	defer os.Remove(tmpFile)

	flagSet := flag.NewFlagSet("send-metadata", flag.ContinueOnError)
	args := []string{
		"--createMetadata", tmpFile,
		"--timeout", "1s",
		"--serverIP", "192.168.1.100",
	}

	err := sendMetadataCommand(flagSet, args)

	// Should fail at connection stage (expected)
	if err == nil {
		t.Fatal("Expected error (connection failure), got nil")
	}

	// Should not fail at validation
	if strings.Contains(err.Error(), "metadata file validation failed") {
		t.Errorf("Should not fail at file validation, got: %v", err)
	}
	if strings.Contains(err.Error(), "invalid server IP") {
		t.Errorf("Should not fail at IP validation, got: %v", err)
	}
}

// TestSendMetadataCommand_DeleteOperation tests delete metadata operation
func TestSendMetadataCommand_DeleteOperation(t *testing.T) {
	tmpFile := createValidMetadataFile(t, "delete-metadata.json")
	defer os.Remove(tmpFile)

	flagSet := flag.NewFlagSet("send-metadata", flag.ContinueOnError)
	args := []string{
		"--deleteMetadata", tmpFile,
		"--timeout", "1s",
		"--serverIP", "192.168.1.100",
	}

	err := sendMetadataCommand(flagSet, args)

	// Should fail at connection stage (expected)
	if err == nil {
		t.Fatal("Expected error (connection failure), got nil")
	}

	// Should not fail at validation
	if strings.Contains(err.Error(), "metadata file validation failed") {
		t.Errorf("Should not fail at file validation, got: %v", err)
	}
	if strings.Contains(err.Error(), "invalid server IP") {
		t.Errorf("Should not fail at IP validation, got: %v", err)
	}
}

// TestSendMetadataCommand_ErrorPrefix tests that errors have the correct prefix
func TestSendMetadataCommand_ErrorPrefix(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{
			name: "missing operation",
			args: []string{"--serverIP", "192.168.1.100"},
		},
		{
			name: "invalid serverIP",
			args: []string{
				"--createMetadata", "test.json",
				"--serverIP", "999.999.999.999",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flagSet := flag.NewFlagSet("send-metadata", flag.ContinueOnError)
			err := sendMetadataCommand(flagSet, tt.args)

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

// TestSendMetadataCommand_Constants tests that constants are defined correctly
func TestSendMetadataCommand_Constants(t *testing.T) {
	// Test flag name constants
	if flagSendCreateMetadata == "" {
		t.Error("flagSendCreateMetadata constant should not be empty")
	}
	if flagSendDeleteMetadata == "" {
		t.Error("flagSendDeleteMetadata constant should not be empty")
	}
	if flagSendServerIP == "" {
		t.Error("flagSendServerIP constant should not be empty")
	}
	if flagSendShouldDebug == "" {
		t.Error("flagSendShouldDebug constant should not be empty")
	}

	// Test default value constants
	if defaultSendCreateMetadata != "" {
		t.Errorf("defaultSendCreateMetadata should be empty string, got: %q", defaultSendCreateMetadata)
	}
	if defaultSendDeleteMetadata != "" {
		t.Errorf("defaultSendDeleteMetadata should be empty string, got: %q", defaultSendDeleteMetadata)
	}
	if defaultSendServerIP != "" {
		t.Errorf("defaultSendServerIP should be empty string, got: %q", defaultSendServerIP)
	}
	if defaultSendShouldDebug != "false" {
		t.Errorf("defaultSendShouldDebug should be 'false', got: %q", defaultSendShouldDebug)
	}

	// Test operation name constants
	if operationCreate != "create" {
		t.Errorf("operationCreate should be 'create', got: %q", operationCreate)
	}
	if operationDelete != "delete" {
		t.Errorf("operationDelete should be 'delete', got: %q", operationDelete)
	}
	if operationCreated != "created" {
		t.Errorf("operationCreated should be 'created', got: %q", operationCreated)
	}
	if operationDeleted != "deleted" {
		t.Errorf("operationDeleted should be 'deleted', got: %q", operationDeleted)
	}

	// Test error prefix
	if errPrefixSend == "" {
		t.Error("errPrefixSend constant should not be empty")
	}
}

// TestSendMetadataCommand_FlagDefaults tests that default values are set correctly
func TestSendMetadataCommand_FlagDefaults(t *testing.T) {
	flagSet := flag.NewFlagSet("send-metadata", flag.ContinueOnError)

	// Define flags without parsing
	createMetadata := flagSet.String(flagSendCreateMetadata, defaultSendCreateMetadata, usageSendCreateMetadata)
	deleteMetadata := flagSet.String(flagSendDeleteMetadata, defaultSendDeleteMetadata, usageSendDeleteMetadata)
	serverIP := flagSet.String(flagSendServerIP, defaultSendServerIP, usageSendServerIP)
	shouldDebug := flagSet.String(flagSendShouldDebug, defaultSendShouldDebug, usageSendShouldDebug)

	// Check defaults before parsing
	if *createMetadata != "" {
		t.Errorf("Default createMetadata should be empty, got: %q", *createMetadata)
	}
	if *deleteMetadata != "" {
		t.Errorf("Default deleteMetadata should be empty, got: %q", *deleteMetadata)
	}
	if *serverIP != "" {
		t.Errorf("Default serverIP should be empty, got: %q", *serverIP)
	}
	if *shouldDebug != "false" {
		t.Errorf("Default shouldDebug should be 'false', got: %q", *shouldDebug)
	}
}

// TestSendMetadataCommand_WhitespaceHandling tests that whitespace is properly trimmed
func TestSendMetadataCommand_WhitespaceHandling(t *testing.T) {
	tmpFile := createValidMetadataFile(t, "test-metadata.json")
	defer os.Remove(tmpFile)

	tests := []struct {
		name     string
		args     []string
		shouldOK bool // true if should pass validation
	}{
		{
			name: "whitespace in createMetadata",
			args: []string{
				"--createMetadata", "  " + tmpFile + "  ",
				"--timeout", "1s",
				"--serverIP", "192.168.1.100",
			},
			shouldOK: true,
		},
		{
			name: "whitespace in serverIP",
			args: []string{
				"--createMetadata", tmpFile,
				"--timeout", "1s",
				"--serverIP", "  192.168.1.100  ",
			},
			shouldOK: true,
		},
		{
			name: "only whitespace in createMetadata",
			args: []string{
				"--createMetadata", "   ",
				"--timeout", "1s",
				"--serverIP", "192.168.1.100",
			},
			shouldOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flagSet := flag.NewFlagSet("send-metadata", flag.ContinueOnError)
			err := sendMetadataCommand(flagSet, tt.args)

			if tt.shouldOK {
				// Should fail at connection, not validation
				if err != nil && (strings.Contains(err.Error(), "metadata file validation failed") ||
					strings.Contains(err.Error(), "invalid server IP") ||
					strings.Contains(err.Error(), "required flag")) {
					t.Errorf("Should pass validation but got: %v", err)
				}
			} else {
				// Should fail at validation
				if err == nil {
					t.Error("Expected validation error, got nil")
				}
			}
		})
	}
}

// TestSendMetadataCommand_ValidIPv4Addresses tests various valid IPv4 formats
func TestSendMetadataCommand_ValidIPv4Addresses(t *testing.T) {
	tmpFile := createValidMetadataFile(t, "test-metadata.json")
	defer os.Remove(tmpFile)

	tests := []struct {
		name     string
		serverIP string
	}{
		{name: "standard IPv4", serverIP: "192.168.1.100"},
		{name: "localhost", serverIP: "127.0.0.1"},
		{name: "zero address", serverIP: "0.0.0.0"},
		{name: "broadcast", serverIP: "255.255.255.255"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flagSet := flag.NewFlagSet("send-metadata", flag.ContinueOnError)
			args := []string{
				"--createMetadata", tmpFile,
				"--serverIP", tt.serverIP,
				"--timeout", "1s",
			}
			err := sendMetadataCommand(flagSet, args)

			// Should fail at connection stage, not validation
			if err != nil && strings.Contains(err.Error(), "invalid server IP") {
				t.Errorf("IP %q should be valid but got validation error: %v", tt.serverIP, err)
			}
		})
	}
}

// TestSendMetadataCommand_ValidIPv6Addresses tests various valid IPv6 formats
func TestSendMetadataCommand_ValidIPv6Addresses(t *testing.T) {
	tmpFile := createValidMetadataFile(t, "test-metadata.json")
	defer os.Remove(tmpFile)

	tests := []struct {
		name     string
		serverIP string
	}{
		{name: "full IPv6", serverIP: "2001:0db8:85a3:0000:0000:8a2e:0370:7334"},
		{name: "compressed IPv6", serverIP: "2001:db8:85a3::8a2e:370:7334"},
		{name: "localhost IPv6", serverIP: "::1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flagSet := flag.NewFlagSet("send-metadata", flag.ContinueOnError)
			args := []string{
				"--createMetadata", tmpFile,
				"--serverIP", tt.serverIP,
				"--timeout", "1s",
			}
			err := sendMetadataCommand(flagSet, args)

			// Should fail at connection stage, not validation
			if err != nil && strings.Contains(err.Error(), "invalid server IP") {
				t.Errorf("IP %q should be valid but got validation error: %v", tt.serverIP, err)
			}
		})
	}
}

// TestSendMetadataCommand_MultipleInvocations tests that the function can be called multiple times
func TestSendMetadataCommand_MultipleInvocations(t *testing.T) {
	// First invocation
	flagSet1 := flag.NewFlagSet("send-metadata-1", flag.ContinueOnError)
	err1 := sendMetadataCommand(flagSet1, []string{})
	if err1 == nil {
		t.Error("First invocation: expected error for missing flags")
	}

	// Second invocation
	flagSet2 := flag.NewFlagSet("send-metadata-2", flag.ContinueOnError)
	err2 := sendMetadataCommand(flagSet2, []string{})
	if err2 == nil {
		t.Error("Second invocation: expected error for missing flags")
	}

	// Both should have similar errors
	if err1.Error() != err2.Error() {
		t.Errorf("Multiple invocations should produce consistent errors.\nFirst: %v\nSecond: %v", err1, err2)
	}
}

// Helper function to create a temporary test file
func createTempTestFile(t *testing.T, name, content string) string {
	t.Helper()

	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, name)

	err := os.WriteFile(filePath, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	return filePath
}

// Helper function to create a valid metadata test file with required fields
func createValidMetadataFile(t *testing.T, name string) string {
	t.Helper()

	validMetadata := `{
  "clusterName": "test-cluster",
  "clusterID": "12345678-1234-1234-1234-123456789012",
  "infraID": "test-cluster-abcde",
  "openstack": {
    "cloud": "test-cloud",
    "identifier": {
      "openshiftClusterID": "test-cluster-id"
    }
  }
}`

	return createTempTestFile(t, name, validMetadata)
}

// Made with Bob

// TestSendMetadataCommand_InvalidMetadataContent tests that invalid metadata content is rejected
func TestSendMetadataCommand_InvalidMetadataContent(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		errorMsg string
	}{
		{
			name:     "missing clusterName",
			content:  `{"infraID": "test-infra", "openstack": {"cloud": "test"}}`,
			errorMsg: "required field 'clusterName' is missing or empty",
		},
		{
			name:     "missing infraID",
			content:  `{"clusterName": "test-cluster", "openstack": {"cloud": "test"}}`,
			errorMsg: "required field 'infraID' is missing or empty",
		},
		{
			name:     "missing platform metadata",
			content:  `{"clusterName": "test-cluster", "infraID": "test-infra"}`,
			errorMsg: "metadata must contain either 'openstack' or 'powervc' platform configuration",
		},
		{
			name:     "invalid JSON",
			content:  `{"clusterName": "test-cluster", "infraID": "test-infra"`,
			errorMsg: "invalid JSON format",
		},
		{
			name:     "empty clusterName",
			content:  `{"clusterName": "", "infraID": "test-infra", "openstack": {"cloud": "test"}}`,
			errorMsg: "required field 'clusterName' is missing or empty",
		},
		{
			name:     "empty infraID",
			content:  `{"clusterName": "test-cluster", "infraID": "", "openstack": {"cloud": "test"}}`,
			errorMsg: "required field 'infraID' is missing or empty",
		},
		{
			name:     "whitespace-only clusterName",
			content:  `{"clusterName": "   ", "infraID": "test-infra", "openstack": {"cloud": "test"}}`,
			errorMsg: "required field 'clusterName' is missing or empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpFile := createTempTestFile(t, "invalid-metadata.json", tt.content)
			defer os.Remove(tmpFile)

			flagSet := flag.NewFlagSet("send-metadata", flag.ContinueOnError)
			args := []string{
				"--createMetadata", tmpFile,
				"--serverIP", "192.168.1.100",
				"--timeout", "1s",
			}

			err := sendMetadataCommand(flagSet, args)

			if err == nil {
				t.Fatal("Expected error for invalid metadata content, got nil")
			}

			if !strings.Contains(err.Error(), tt.errorMsg) {
				t.Errorf("Expected error to contain %q, got: %v", tt.errorMsg, err)
			}

			// Verify it failed at content validation, not connection
			if strings.Contains(err.Error(), "failed to connect to server") {
				t.Errorf("Should fail at content validation, not connection. Got: %v", err)
			}
		})
	}
}

// TestSendMetadataCommand_ValidMetadataContent tests that valid metadata content is accepted
func TestSendMetadataCommand_ValidMetadataContent(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{
			name: "valid OpenStack metadata",
			content: `{
				"clusterName": "test-cluster",
				"infraID": "test-infra",
				"openstack": {
					"cloud": "test-cloud",
					"identifier": {"openshiftClusterID": "test-id"}
				}
			}`,
		},
		{
			name: "valid PowerVC metadata",
			content: `{
				"clusterName": "test-cluster",
				"infraID": "test-infra",
				"powervc": {
					"cloud": "test-cloud",
					"identifier": {"openshiftClusterID": "test-id"}
				}
			}`,
		},
		{
			name: "both OpenStack and PowerVC metadata",
			content: `{
				"clusterName": "test-cluster",
				"infraID": "test-infra",
				"openstack": {
					"cloud": "openstack-cloud",
					"identifier": {"openshiftClusterID": "test-id"}
				},
				"powervc": {
					"cloud": "powervc-cloud",
					"identifier": {"openshiftClusterID": "test-id"}
				}
			}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpFile := createTempTestFile(t, "valid-metadata.json", tt.content)
			defer os.Remove(tmpFile)

			flagSet := flag.NewFlagSet("send-metadata", flag.ContinueOnError)
			args := []string{
				"--createMetadata", tmpFile,
				"--serverIP", "192.168.1.100",
				"--timeout", "1s",
			}

			err := sendMetadataCommand(flagSet, args)

			// Should fail at connection stage, not content validation
			if err != nil && strings.Contains(err.Error(), "metadata content validation") {
				t.Errorf("Should not fail at content validation for valid metadata. Got: %v", err)
			}
		})
	}
}


// TestSendMetadataCommand_TimeoutFlag tests the timeout flag functionality
func TestSendMetadataCommand_TimeoutFlag(t *testing.T) {
	tmpFile := createValidMetadataFile(t, "test-metadata.json")
	defer os.Remove(tmpFile)

	tests := []struct {
		name        string
		timeout     string
		expectError bool
		errorMsg    string
	}{
		{
			name:        "valid timeout - 5m",
			timeout:     "5m",
			expectError: true, // Will fail at connection, not timeout parsing
			errorMsg:    "connection to server",
		},
		{
			name:        "valid timeout - 10m",
			timeout:     "10m",
			expectError: true,
			errorMsg:    "connection to server",
		},
		{
			name:        "valid timeout - 30s",
			timeout:     "30s",
			expectError: true,
			errorMsg:    "connection to server",
		},
		{
			name:        "valid timeout - 1h",
			timeout:     "1h",
			expectError: true,
			errorMsg:    "connection to server",
		},
		{
			name:        "invalid timeout format",
			timeout:     "invalid",
			expectError: true,
			errorMsg:    "timeout parsing",
		},
		{
			name:        "negative timeout",
			timeout:     "-5m",
			expectError: true,
			errorMsg:    "timeout must be positive",
		},
		{
			name:        "zero timeout",
			timeout:     "0s",
			expectError: true,
			errorMsg:    "timeout must be positive",
		},
		{
			name:        "empty timeout uses default",
			timeout:     "",
			expectError: true,
			errorMsg:    "connection to server",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flagSet := flag.NewFlagSet("send-metadata", flag.ContinueOnError)
			args := []string{
				"--createMetadata", tmpFile,
				"--serverIP", "192.168.1.100",
			}
			if tt.timeout != "" {
				args = append(args, "--timeout", tt.timeout)
			}

			err := sendMetadataCommand(flagSet, args)

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

// TestSendMetadataCommand_TimeoutWithDifferentOperations tests timeout with create and delete
func TestSendMetadataCommand_TimeoutWithDifferentOperations(t *testing.T) {
	tmpFile := createValidMetadataFile(t, "test-metadata.json")
	defer os.Remove(tmpFile)

	tests := []struct {
		name      string
		operation string
		timeout   string
	}{
		{
			name:      "create with custom timeout",
			operation: "createMetadata",
			timeout:   "2m",
		},
		{
			name:      "delete with custom timeout",
			operation: "deleteMetadata",
			timeout:   "3m",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flagSet := flag.NewFlagSet("send-metadata", flag.ContinueOnError)
			args := []string{
				"--" + tt.operation, tmpFile,
				"--serverIP", "192.168.1.100",
				"--timeout", "1s",
			}

			err := sendMetadataCommand(flagSet, args)

			// Should fail at connection, not timeout parsing
			if err == nil {
				t.Fatal("Expected connection error, got nil")
			}
			if strings.Contains(err.Error(), "timeout parsing") {
				t.Errorf("Should not fail at timeout parsing. Got: %v", err)
			}
		})
	}
}

// TestSendMetadataCommand_ValidHostnames tests that valid hostnames are accepted by the validation
func TestSendMetadataCommand_ValidHostnames(t *testing.T) {
	// Create a valid metadata test file
	tmpFile := createValidMetadataFile(t, "test-metadata.json")
	defer os.Remove(tmpFile)

	tests := []struct {
		name     string
		hostname string
	}{
		{name: "localhost", hostname: "localhost"},
		{name: "FQDN", hostname: "server.example.com"},
		{name: "subdomain", hostname: "api.cluster.example.com"},
		{name: "multi-level subdomain", hostname: "api.prod.cluster.example.com"},
		{name: "hostname with hyphen", hostname: "my-server.example.com"},
		{name: "short hostname", hostname: "server"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flagSet := flag.NewFlagSet("send-metadata", flag.ContinueOnError)
			args := []string{
				"--createMetadata", tmpFile,
				"--serverIP", tt.hostname,
			}

			err := sendMetadataCommand(flagSet, args)

			// Should fail at connection, not validation
			if err != nil && strings.Contains(err.Error(), "invalid server IP") {
				t.Errorf("Hostname %q should be valid but got validation error: %v",
					tt.hostname, err)
			}

			// We expect connection errors since these are not real servers
			// but we should NOT get validation errors
			if err != nil && !strings.Contains(err.Error(), "metadata transmission") {
				t.Logf("Got expected connection error for hostname %q: %v", tt.hostname, err)
			}
		})
	}
}

// TestSendMetadataCommand_InvalidHostnames tests that invalid hostnames are rejected
func TestSendMetadataCommand_InvalidHostnames(t *testing.T) {
	// Create a valid metadata test file
	tmpFile := createValidMetadataFile(t, "test-metadata.json")
	defer os.Remove(tmpFile)

	tests := []struct {
		name     string
		hostname string
		errorMsg string
	}{
		{
			name:     "empty hostname",
			hostname: "",
			errorMsg: "required flag --serverIP not specified",
		},
		{
			name:     "hostname with spaces",
			hostname: "server name.com",
			errorMsg: "invalid IP address or hostname",
		},
		{
			name:     "hostname with invalid characters",
			hostname: "server@example.com",
			errorMsg: "invalid IP address or hostname",
		},
		{
			name:     "hostname starting with hyphen",
			hostname: "-server.example.com",
			errorMsg: "invalid IP address or hostname",
		},
		{
			name:     "hostname ending with hyphen",
			hostname: "server-.example.com",
			errorMsg: "invalid IP address or hostname",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flagSet := flag.NewFlagSet("send-metadata", flag.ContinueOnError)
			args := []string{
				"--createMetadata", tmpFile,
				"--serverIP", tt.hostname,
			}

			err := sendMetadataCommand(flagSet, args)

			if err == nil {
				t.Fatalf("Expected error for invalid hostname %q, got nil", tt.hostname)
			}

			if !strings.Contains(err.Error(), tt.errorMsg) {
				t.Errorf("Expected error to contain %q, got: %v", tt.errorMsg, err)
			}
		})
	}
}
