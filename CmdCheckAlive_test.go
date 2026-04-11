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
	"strings"
	"testing"
)

// TestCheckAliveCommand_NilFlagSet tests that the function returns an error when flagSet is nil
func TestCheckAliveCommand_NilFlagSet(t *testing.T) {
	err := checkAliveCommand(nil, []string{})
	
	if err == nil {
		t.Fatal("Expected error for nil flag set, got nil")
	}
	
	expectedMsg := "flag set cannot be nil"
	if !strings.Contains(err.Error(), expectedMsg) {
		t.Errorf("Expected error message to contain %q, got: %v", expectedMsg, err)
	}
}

// TestCheckAliveCommand_MissingServerIP tests that the function returns an error when serverIP is not provided
func TestCheckAliveCommand_MissingServerIP(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{
			name: "no flags provided",
			args: []string{},
		},
		{
			name: "empty serverIP",
			args: []string{"--serverIP", ""},
		},
		{
			name: "whitespace serverIP",
			args: []string{"--serverIP", "   "},
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flagSet := flag.NewFlagSet("check-alive", flag.ContinueOnError)
			err := checkAliveCommand(flagSet, tt.args)
			
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

// TestCheckAliveCommand_InvalidServerIP tests that the function returns an error for invalid IP addresses
func TestCheckAliveCommand_InvalidServerIP(t *testing.T) {
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
		{
			name:     "special characters",
			serverIP: "192.168.1.1!@#",
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flagSet := flag.NewFlagSet("check-alive", flag.ContinueOnError)
			args := []string{"--serverIP", tt.serverIP}
			err := checkAliveCommand(flagSet, args)
			
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

// TestCheckAliveCommand_InvalidDebugFlag tests that the function returns an error for invalid debug flag values
func TestCheckAliveCommand_InvalidDebugFlag(t *testing.T) {
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
			flagSet := flag.NewFlagSet("check-alive", flag.ContinueOnError)
			args := []string{
				"--serverIP", "192.168.1.100",
				"--shouldDebug", tt.debugValue,
			}
			err := checkAliveCommand(flagSet, args)
			
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

// TestCheckAliveCommand_ValidDebugFlags tests that valid debug flag values are accepted
func TestCheckAliveCommand_ValidDebugFlags(t *testing.T) {
	tests := []struct {
		name       string
		debugValue string
		expected   bool
	}{
		{
			name:       "true lowercase",
			debugValue: "true",
			expected:   true,
		},
		{
			name:       "false lowercase",
			debugValue: "false",
			expected:   false,
		},
		{
			name:       "TRUE uppercase",
			debugValue: "TRUE",
			expected:   true,
		},
		{
			name:       "FALSE uppercase",
			debugValue: "FALSE",
			expected:   false,
		},
		{
			name:       "1 numeric",
			debugValue: "1",
			expected:   true,
		},
		{
			name:       "0 numeric",
			debugValue: "0",
			expected:   false,
		},
		{
			name:       "yes",
			debugValue: "yes",
			expected:   true,
		},
		{
			name:       "no",
			debugValue: "no",
			expected:   false,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Note: This test will fail when trying to connect to the server
			// We're only testing that the debug flag parsing works correctly
			flagSet := flag.NewFlagSet("check-alive", flag.ContinueOnError)
			args := []string{
				"--serverIP", "192.168.1.100",
				"--shouldDebug", tt.debugValue,
			}
			
			// We expect this to fail at the sendCheckAlive stage, not at flag parsing
			err := checkAliveCommand(flagSet, args)
			
			// The error should be about connection, not about invalid flag
			if err != nil && strings.Contains(err.Error(), "must be 'true' or 'false'") {
				t.Errorf("Debug flag %q should be valid but got parsing error: %v", tt.debugValue, err)
			}
		})
	}
}

// TestCheckAliveCommand_FlagParsing tests that flags are parsed correctly
func TestCheckAliveCommand_FlagParsing(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid IPv4 address",
			args: []string{"--serverIP", "192.168.1.100"},
			// Will fail at connection stage, not parsing
			expectError: true,
			errorMsg:    "check-alive command failed",
		},
		{
			name: "valid IPv4 with debug",
			args: []string{
				"--serverIP", "192.168.1.100",
				"--shouldDebug", "true",
			},
			expectError: true,
			errorMsg:    "check-alive command failed",
		},
		{
			name: "localhost",
			args: []string{"--serverIP", "127.0.0.1"},
			expectError: true,
			errorMsg:    "check-alive command failed",
		},
		{
			name:        "unknown flag",
			args:        []string{"--serverIP", "192.168.1.100", "--unknown", "value"},
			expectError: true,
			errorMsg:    "failed to parse flags",
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flagSet := flag.NewFlagSet("check-alive", flag.ContinueOnError)
			err := checkAliveCommand(flagSet, tt.args)
			
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

// TestCheckAliveCommand_ErrorPrefix tests that errors have the correct prefix
func TestCheckAliveCommand_ErrorPrefix(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{
			name: "missing serverIP",
			args: []string{},
		},
		{
			name: "invalid serverIP",
			args: []string{"--serverIP", "999.999.999.999"},
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flagSet := flag.NewFlagSet("check-alive", flag.ContinueOnError)
			err := checkAliveCommand(flagSet, tt.args)
			
			if err == nil {
				t.Fatal("Expected error, got nil")
			}
			
			expectedPrefix := "[check-alive]"
			if !strings.Contains(err.Error(), expectedPrefix) {
				t.Errorf("Expected error to contain prefix %q, got: %v", expectedPrefix, err)
			}
		})
	}
}

// TestCheckAliveCommand_ValidIPv4Addresses tests various valid IPv4 formats
func TestCheckAliveCommand_ValidIPv4Addresses(t *testing.T) {
	tests := []struct {
		name     string
		serverIP string
	}{
		{
			name:     "standard IPv4",
			serverIP: "192.168.1.100",
		},
		{
			name:     "localhost",
			serverIP: "127.0.0.1",
		},
		{
			name:     "zero address",
			serverIP: "0.0.0.0",
		},
		{
			name:     "broadcast",
			serverIP: "255.255.255.255",
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flagSet := flag.NewFlagSet("check-alive", flag.ContinueOnError)
			args := []string{"--serverIP", tt.serverIP}
			err := checkAliveCommand(flagSet, args)
			
			// Should fail at connection stage, not validation
			if err != nil && strings.Contains(err.Error(), "invalid server IP") {
				t.Errorf("IP %q should be valid but got validation error: %v", tt.serverIP, err)
			}
		})
	}
}

// TestCheckAliveCommand_ValidIPv6Addresses tests various valid IPv6 formats
func TestCheckAliveCommand_ValidIPv6Addresses(t *testing.T) {
	tests := []struct {
		name     string
		serverIP string
	}{
		{
			name:     "full IPv6",
			serverIP: "2001:0db8:85a3:0000:0000:8a2e:0370:7334",
		},
		{
			name:     "compressed IPv6",
			serverIP: "2001:db8:85a3::8a2e:370:7334",
		},
		{
			name:     "localhost IPv6",
			serverIP: "::1",
		},
		{
			name:     "IPv6 loopback",
			serverIP: "0:0:0:0:0:0:0:1",
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flagSet := flag.NewFlagSet("check-alive", flag.ContinueOnError)
			args := []string{"--serverIP", tt.serverIP}
			err := checkAliveCommand(flagSet, args)
			
			// Should fail at connection stage, not validation
			if err != nil && strings.Contains(err.Error(), "invalid server IP") {
				t.Errorf("IP %q should be valid but got validation error: %v", tt.serverIP, err)
			}
		})
	}
}

// TestCheckAliveCommand_EdgeCases tests edge cases and boundary conditions
func TestCheckAliveCommand_EdgeCases(t *testing.T) {
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
			errorMsg:    "required flag --serverIP not specified",
		},
		{
			name:        "only debug flag",
			args:        []string{"--shouldDebug", "true"},
			expectError: true,
			errorMsg:    "required flag --serverIP not specified",
		},
		{
			name: "serverIP with spaces",
			args: []string{"--serverIP", "  192.168.1.100  "},
			// Should trim and accept
			expectError: true,
			errorMsg:    "check-alive command failed", // Fails at connection, not validation
		},
		{
			name:        "duplicate serverIP flags",
			args:        []string{"--serverIP", "192.168.1.100", "--serverIP", "192.168.1.101"},
			expectError: true,
			// Last value wins in flag parsing
			errorMsg: "check-alive command failed",
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flagSet := flag.NewFlagSet("check-alive", flag.ContinueOnError)
			err := checkAliveCommand(flagSet, tt.args)
			
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

// TestCheckAliveCommand_Constants tests that constants are used correctly
func TestCheckAliveCommand_Constants(t *testing.T) {
	// Test that the constants are defined and accessible
	if flagCheckAliveServerIP == "" {
		t.Error("flagCheckAliveServerIP constant should not be empty")
	}
	if flagCheckAliveShouldDebug == "" {
		t.Error("flagCheckAliveShouldDebug constant should not be empty")
	}
	if defaultCheckAliveServerIP != "" {
		t.Error("defaultCheckAliveServerIP should be empty string")
	}
	if defaultCheckAliveShouldDebug != "false" {
		t.Errorf("defaultCheckAliveShouldDebug should be 'false', got: %q", defaultCheckAliveShouldDebug)
	}
	if errPrefixCheckAlive == "" {
		t.Error("errPrefixCheckAlive constant should not be empty")
	}
}

// TestCheckAliveCommand_FlagDefaults tests that default values are set correctly
func TestCheckAliveCommand_FlagDefaults(t *testing.T) {
	flagSet := flag.NewFlagSet("check-alive", flag.ContinueOnError)
	
	// Define flags without parsing
	serverIP := flagSet.String(flagCheckAliveServerIP, defaultCheckAliveServerIP, usageCheckAliveServerIP)
	shouldDebug := flagSet.String(flagCheckAliveShouldDebug, defaultCheckAliveShouldDebug, usageCheckAliveShouldDebug)
	
	// Check defaults before parsing
	if *serverIP != "" {
		t.Errorf("Default serverIP should be empty, got: %q", *serverIP)
	}
	if *shouldDebug != "false" {
		t.Errorf("Default shouldDebug should be 'false', got: %q", *shouldDebug)
	}
}

// TestCheckAliveCommand_MultipleInvocations tests that the function can be called multiple times
func TestCheckAliveCommand_MultipleInvocations(t *testing.T) {
	// First invocation
	flagSet1 := flag.NewFlagSet("check-alive-1", flag.ContinueOnError)
	err1 := checkAliveCommand(flagSet1, []string{})
	if err1 == nil {
		t.Error("First invocation: expected error for missing serverIP")
	}
	
	// Second invocation
	flagSet2 := flag.NewFlagSet("check-alive-2", flag.ContinueOnError)
	err2 := checkAliveCommand(flagSet2, []string{})
	if err2 == nil {
		t.Error("Second invocation: expected error for missing serverIP")
	}
	
	// Both should have similar errors
	if err1.Error() != err2.Error() {
		t.Errorf("Multiple invocations should produce consistent errors.\nFirst: %v\nSecond: %v", err1, err2)
	}
}

// Made with Bob
