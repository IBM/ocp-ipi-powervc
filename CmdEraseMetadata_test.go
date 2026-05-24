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

// TestEraseMetadataCommand_NilFlagSet tests that the function returns an error when flagSet is nil
func TestEraseMetadataCommand_NilFlagSet(t *testing.T) {
	err := eraseMetadataCommand(nil, []string{})

	if err == nil {
		t.Fatal("Expected error for nil flag set, got nil")
	}

	expectedMsg := "flag set cannot be nil"
	if !strings.Contains(err.Error(), expectedMsg) {
		t.Errorf("Expected error message to contain %q, got: %v", expectedMsg, err)
	}
}

// TestEraseMetadataCommand_MissingPattern tests that pattern is required
func TestEraseMetadataCommand_MissingPattern(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{
			name: "no pattern specified",
			args: []string{"--serverIP", "192.168.1.100"},
		},
		{
			name: "empty pattern",
			args: []string{
				"--pattern", "",
				"--serverIP", "192.168.1.100",
			},
		},
		{
			name: "whitespace pattern",
			args: []string{
				"--pattern", "   ",
				"--serverIP", "192.168.1.100",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flagSet := flag.NewFlagSet("erase-metadata", flag.ContinueOnError)
			err := eraseMetadataCommand(flagSet, tt.args)

			if err == nil {
				t.Fatal("Expected error for missing/empty pattern, got nil")
			}

			expectedMsg := "required flag --pattern must be specified"
			if !strings.Contains(err.Error(), expectedMsg) {
				t.Errorf("Expected error message to contain %q, got: %v", expectedMsg, err)
			}
		})
	}
}

// TestEraseMetadataCommand_MissingServerIP tests that serverIP is required
func TestEraseMetadataCommand_MissingServerIP(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{
			name: "no serverIP specified",
			args: []string{"--pattern", "test-*"},
		},
		{
			name: "empty serverIP",
			args: []string{
				"--pattern", "test-*",
				"--serverIP", "",
			},
		},
		{
			name: "whitespace serverIP",
			args: []string{
				"--pattern", "test-*",
				"--serverIP", "   ",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flagSet := flag.NewFlagSet("erase-metadata", flag.ContinueOnError)
			err := eraseMetadataCommand(flagSet, tt.args)

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

// TestEraseMetadataCommand_InvalidServerIP tests that invalid IP addresses are rejected
func TestEraseMetadataCommand_InvalidServerIP(t *testing.T) {
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
			flagSet := flag.NewFlagSet("erase-metadata", flag.ContinueOnError)
			args := []string{
				"--pattern", "test-*",
				"--serverIP", tt.serverIP,
			}
			err := eraseMetadataCommand(flagSet, args)

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

// TestEraseMetadataCommand_ValidPatterns tests that various valid patterns are accepted
func TestEraseMetadataCommand_ValidPatterns(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
	}{
		{name: "wildcard pattern", pattern: "test-*"},
		{name: "prefix pattern", pattern: "staging-*"},
		{name: "specific name", pattern: "my-cluster"},
		{name: "pattern with numbers", pattern: "cluster-2024-*"},
		{name: "pattern with hyphens", pattern: "dev-env-*"},
		{name: "pattern with underscores", pattern: "test_cluster_*"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flagSet := flag.NewFlagSet("erase-metadata", flag.ContinueOnError)
			args := []string{
				"--pattern", tt.pattern,
				"--serverIP", "192.168.1.100",
				"--timeout", "1s",
			}

			err := eraseMetadataCommand(flagSet, args)

			// Should fail at connection stage, not pattern validation
			if err != nil && strings.Contains(err.Error(), "pattern validation") {
				t.Errorf("Pattern %q should be valid but got validation error: %v",
					tt.pattern, err)
			}

			// We expect connection/transmission errors since server is not real
			// but we should NOT get pattern validation errors
			if err != nil && !strings.Contains(err.Error(), "metadata erasure") {
				t.Logf("Got expected connection error for pattern %q: %v", tt.pattern, err)
			}
		})
	}
}

// TestEraseMetadataCommand_DebugFlag tests that debug flag is properly parsed
func TestEraseMetadataCommand_DebugFlag(t *testing.T) {
	tests := []struct {
		name        string
		debugValue  string
		expectError bool
		errorMsg    string
	}{
		{
			name:        "debug true",
			debugValue:  "true",
			expectError: true, // Will fail at connection
			errorMsg:    "metadata erasure",
		},
		{
			name:        "debug false",
			debugValue:  "false",
			expectError: true, // Will fail at connection
			errorMsg:    "metadata erasure",
		},
		{
			name:        "debug invalid",
			debugValue:  "invalid",
			expectError: true,
			errorMsg:    "shouldDebug must be a boolean value",
		},
		{
			name:        "debug yes",
			debugValue:  "yes",
			expectError: true, // Will fail at connection (yes is valid)
			errorMsg:    "metadata erasure",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flagSet := flag.NewFlagSet("erase-metadata", flag.ContinueOnError)
			args := []string{
				"--pattern", "test-*",
				"--serverIP", "192.168.1.100",
				"--shouldDebug", tt.debugValue,
				"--timeout", "1s",
			}

			err := eraseMetadataCommand(flagSet, args)

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

// TestEraseMetadataCommand_TimeoutFlag tests that timeout flag is properly parsed
func TestEraseMetadataCommand_TimeoutFlag(t *testing.T) {
	tests := []struct {
		name         string
		timeoutValue string
		expectError  bool
		errorMsg     string
	}{
		{
			name:         "valid timeout 5m",
			timeoutValue: "5m",
			expectError:  true, // Will fail at connection
			errorMsg:     "metadata erasure",
		},
		{
			name:         "invalid timeout format",
			timeoutValue: "invalid",
			expectError:  true,
			errorMsg:     "invalid timeout value",
		},
		{
			name:         "negative timeout",
			timeoutValue: "-5m",
			expectError:  true,
			errorMsg:     "timeout must be positive",
		},
		{
			name:         "zero timeout",
			timeoutValue: "0s",
			expectError:  true,
			errorMsg:     "timeout must be positive",
		},
		{
			name:         "empty timeout uses default",
			timeoutValue: "",
			expectError:  true,
			errorMsg:     "failed to connect to server",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flagSet := flag.NewFlagSet("erase-metadata", flag.ContinueOnError)
			args := []string{
				"--pattern", "test-*",
				"--serverIP", "192.168.1.100",
			}

			if tt.timeoutValue != "" {
				args = append(args, "--timeout", tt.timeoutValue)
			}

			err := eraseMetadataCommand(flagSet, args)

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

// TestEraseMetadataCommand_ValidHostnames tests that valid hostnames are accepted by the validation
func TestEraseMetadataCommand_ValidHostnames(t *testing.T) {
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
			flagSet := flag.NewFlagSet("erase-metadata", flag.ContinueOnError)
			args := []string{
				"--pattern", "test-*",
				"--serverIP", tt.hostname,
				"--timeout", "1s",
			}

			err := eraseMetadataCommand(flagSet, args)

			// Should fail at connection, not validation
			if err != nil && strings.Contains(err.Error(), "invalid server IP") {
				t.Errorf("Hostname %q should be valid but got validation error: %v",
					tt.hostname, err)
			}

			// We expect connection errors since these are not real servers
			// but we should NOT get validation errors
			if err != nil && !strings.Contains(err.Error(), "metadata erasure") {
				t.Logf("Got expected connection error for hostname %q: %v", tt.hostname, err)
			}
		})
	}
}

// TestEraseMetadataCommand_InvalidHostnames tests that invalid hostnames are rejected
func TestEraseMetadataCommand_InvalidHostnames(t *testing.T) {
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
			flagSet := flag.NewFlagSet("erase-metadata", flag.ContinueOnError)
			args := []string{
				"--pattern", "test-*",
				"--serverIP", tt.hostname,
			}

			err := eraseMetadataCommand(flagSet, args)

			if err == nil {
				t.Fatalf("Expected error for invalid hostname %q, got nil", tt.hostname)
			}

			if !strings.Contains(err.Error(), tt.errorMsg) {
				t.Errorf("Expected error to contain %q, got: %v", tt.errorMsg, err)
			}
		})
	}
}

// TestEraseMetadataCommand_ValidIPAddresses tests that valid IP addresses are accepted
func TestEraseMetadataCommand_ValidIPAddresses(t *testing.T) {
	tests := []struct {
		name string
		ip   string
	}{
		{name: "IPv4 private", ip: "192.168.1.100"},
		{name: "IPv4 localhost", ip: "127.0.0.1"},
		{name: "IPv4 public", ip: "8.8.8.8"},
		{name: "IPv6 localhost", ip: "::1"},
		{name: "IPv6 full", ip: "2001:0db8:85a3:0000:0000:8a2e:0370:7334"},
		{name: "IPv6 compressed", ip: "2001:db8::1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flagSet := flag.NewFlagSet("erase-metadata", flag.ContinueOnError)
			args := []string{
				"--pattern", "test-*",
				"--serverIP", tt.ip,
				"--timeout", "1s",
			}

			err := eraseMetadataCommand(flagSet, args)

			// Should fail at connection, not validation
			if err != nil && strings.Contains(err.Error(), "invalid IP address") {
				t.Errorf("IP %q should be valid but got validation error: %v", tt.ip, err)
			}

			// We expect connection errors since these are not real servers
			if err != nil && !strings.Contains(err.Error(), "metadata erasure") {
				t.Logf("Got expected connection error for IP %q: %v", tt.ip, err)
			}
		})
	}
}

// TestEraseMetadataCommand_AllFlagsCombined tests that all flags work together correctly
func TestEraseMetadataCommand_AllFlagsCombined(t *testing.T) {
	flagSet := flag.NewFlagSet("erase-metadata", flag.ContinueOnError)
	args := []string{
		"--pattern", "test-cluster-*",
		"--serverIP", "192.168.1.100",
		"--shouldDebug", "true",
		"--timeout", "2s",
	}

	err := eraseMetadataCommand(flagSet, args)

	// Should fail at connection stage with all flags properly parsed
	if err == nil {
		t.Fatal("Expected connection error, got nil")
	}

	// Should not fail at parsing or validation
	if strings.Contains(err.Error(), "flag parsing") ||
		strings.Contains(err.Error(), "flag validation") ||
		strings.Contains(err.Error(), "pattern validation") ||
		strings.Contains(err.Error(), "server IP validation") {
		t.Errorf("Should not fail at parsing/validation stage. Got: %v", err)
	}

	// Should fail at metadata erasure (connection)
	if !strings.Contains(err.Error(), "metadata erasure") {
		t.Errorf("Expected to fail at metadata erasure stage. Got: %v", err)
	}
}

// Made with Bob
