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
	// Create a temporary test file
	tmpFile := createTempTestFile(t, "test-metadata.json", `{"test": "data"}`)
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
			},
			expectError: true, // Will fail at connection stage
			errorMsg:    "send metadata command failed",
		},
		{
			name: "only delete specified",
			args: []string{
				"--deleteMetadata", tmpFile,
				"--serverIP", "192.168.1.100",
			},
			expectError: true, // Will fail at connection stage
			errorMsg:    "send metadata command failed",
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
	tmpFile := createTempTestFile(t, "test-metadata.json", `{"test": "data"}`)
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
	tmpFile := createTempTestFile(t, "test-metadata.json", `{"test": "data"}`)
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
			
			expectedMsg := "invalid server IP"
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
				return "/tmp/non-existent-file-12345.json"
			},
			cleanupFile: func(s string) {},
			expectError: true,
			errorMsg:    "metadata file validation failed",
		},
		{
			name: "valid file",
			setupFile: func(t *testing.T) string {
				return createTempTestFile(t, "valid-metadata.json", `{"test": "data"}`)
			},
			cleanupFile: func(s string) { os.Remove(s) },
			expectError: true, // Will fail at connection stage
			errorMsg:    "send metadata command failed",
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
	tmpFile := createTempTestFile(t, "test-metadata.json", `{"test": "data"}`)
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
	tmpFile := createTempTestFile(t, "test-metadata.json", `{"test": "data"}`)
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
	tmpFile := createTempTestFile(t, "create-metadata.json", `{"test": "create"}`)
	defer os.Remove(tmpFile)
	
	flagSet := flag.NewFlagSet("send-metadata", flag.ContinueOnError)
	args := []string{
		"--createMetadata", tmpFile,
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
	tmpFile := createTempTestFile(t, "delete-metadata.json", `{"test": "delete"}`)
	defer os.Remove(tmpFile)
	
	flagSet := flag.NewFlagSet("send-metadata", flag.ContinueOnError)
	args := []string{
		"--deleteMetadata", tmpFile,
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
	tmpFile := createTempTestFile(t, "test-metadata.json", `{"test": "data"}`)
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
				"--serverIP", "192.168.1.100",
			},
			shouldOK: true,
		},
		{
			name: "whitespace in serverIP",
			args: []string{
				"--createMetadata", tmpFile,
				"--serverIP", "  192.168.1.100  ",
			},
			shouldOK: true,
		},
		{
			name: "only whitespace in createMetadata",
			args: []string{
				"--createMetadata", "   ",
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
	tmpFile := createTempTestFile(t, "test-metadata.json", `{"test": "data"}`)
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
	tmpFile := createTempTestFile(t, "test-metadata.json", `{"test": "data"}`)
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

// Made with Bob
