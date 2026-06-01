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
	"fmt"
	"strings"
	"testing"
)

// TestValidateCloudName tests the validateCloudName function with various inputs
func TestValidateCloudName(t *testing.T) {
	tests := []struct {
		name      string
		cloudName string
		wantError bool
		errorMsg  string
	}{
		// Valid cloud names
		{
			name:      "simple alphanumeric",
			cloudName: "mycloud",
			wantError: false,
		},
		{
			name:      "with underscore",
			cloudName: "my_cloud",
			wantError: false,
		},
		{
			name:      "with dash",
			cloudName: "my-cloud",
			wantError: false,
		},
		{
			name:      "with period",
			cloudName: "my.cloud",
			wantError: false,
		},
		{
			name:      "mixed valid characters",
			cloudName: "my_cloud-01.prod",
			wantError: false,
		},
		{
			name:      "numeric only",
			cloudName: "123456",
			wantError: false,
		},
		{
			name:      "uppercase",
			cloudName: "MYCLOUD",
			wantError: false,
		},
		{
			name:      "mixed case",
			cloudName: "MyCloud",
			wantError: false,
		},
		{
			name:      "domain style",
			cloudName: "cloud.example.com",
			wantError: false,
		},
		{
			name:      "max length (253 chars)",
			cloudName: strings.Repeat("a", 253),
			wantError: false,
		},

		// Invalid cloud names
		{
			name:      "empty string",
			cloudName: "",
			wantError: true,
			errorMsg:  "cloud name cannot be empty",
		},
		{
			name:      "too long (254 chars)",
			cloudName: strings.Repeat("a", 254),
			wantError: true,
			errorMsg:  "cloud name too long",
		},
		{
			name:      "path traversal with slashes",
			cloudName: "../etc/passwd",
			wantError: true,
			errorMsg:  "invalid cloud name format",
		},
		{
			name:      "double dots",
			cloudName: "my..cloud",
			wantError: true,
			errorMsg:  "cloud name contains path traversal sequence",
		},
		{
			name:      "double slashes",
			cloudName: "my//cloud",
			wantError: true,
			errorMsg:  "invalid cloud name format",
		},
		{
			name:      "at symbol",
			cloudName: "my@cloud",
			wantError: true,
			errorMsg:  "invalid cloud name format",
		},
		{
			name:      "hash symbol",
			cloudName: "my#cloud",
			wantError: true,
			errorMsg:  "invalid cloud name format",
		},
		{
			name:      "semicolon",
			cloudName: "cloud;ls",
			wantError: true,
			errorMsg:  "invalid cloud name format",
		},
		{
			name:      "pipe symbol",
			cloudName: "cloud|ls",
			wantError: true,
			errorMsg:  "invalid cloud name format",
		},
		{
			name:      "ampersand",
			cloudName: "cloud&test",
			wantError: true,
			errorMsg:  "invalid cloud name format",
		},
		{
			name:      "dollar sign",
			cloudName: "cloud$test",
			wantError: true,
			errorMsg:  "invalid cloud name format",
		},
		{
			name:      "backtick",
			cloudName: "cloud`test",
			wantError: true,
			errorMsg:  "invalid cloud name format",
		},
		{
			name:      "space",
			cloudName: "my cloud",
			wantError: true,
			errorMsg:  "invalid cloud name format",
		},
		{
			name:      "tab",
			cloudName: "my\tcloud",
			wantError: true,
			errorMsg:  "invalid cloud name format",
		},
		{
			name:      "newline",
			cloudName: "my\ncloud",
			wantError: true,
			errorMsg:  "invalid cloud name format",
		},
		{
			name:      "starting with period",
			cloudName: ".mycloud",
			wantError: true,
			errorMsg:  "cloud name cannot start or end with period or dash",
		},
		{
			name:      "ending with period",
			cloudName: "mycloud.",
			wantError: true,
			errorMsg:  "cloud name cannot start or end with period or dash",
		},
		{
			name:      "starting with dash",
			cloudName: "-mycloud",
			wantError: true,
			errorMsg:  "cloud name cannot start or end with period or dash",
		},
		{
			name:      "ending with dash",
			cloudName: "mycloud-",
			wantError: true,
			errorMsg:  "cloud name cannot start or end with period or dash",
		},
		{
			name:      "parentheses",
			cloudName: "my(cloud)",
			wantError: true,
			errorMsg:  "invalid cloud name format",
		},
		{
			name:      "brackets",
			cloudName: "my[cloud]",
			wantError: true,
			errorMsg:  "invalid cloud name format",
		},
		{
			name:      "braces",
			cloudName: "my{cloud}",
			wantError: true,
			errorMsg:  "invalid cloud name format",
		},
		{
			name:      "quotes",
			cloudName: "my\"cloud\"",
			wantError: true,
			errorMsg:  "invalid cloud name format",
		},
		{
			name:      "single quotes",
			cloudName: "my'cloud'",
			wantError: true,
			errorMsg:  "invalid cloud name format",
		},
		{
			name:      "backslash",
			cloudName: "my\\cloud",
			wantError: true,
			errorMsg:  "invalid cloud name format",
		},
		{
			name:      "forward slash",
			cloudName: "my/cloud",
			wantError: true,
			errorMsg:  "invalid cloud name format",
		},
		{
			name:      "question mark",
			cloudName: "my?cloud",
			wantError: true,
			errorMsg:  "invalid cloud name format",
		},
		{
			name:      "asterisk",
			cloudName: "my*cloud",
			wantError: true,
			errorMsg:  "invalid cloud name format",
		},
		{
			name:      "less than",
			cloudName: "my<cloud",
			wantError: true,
			errorMsg:  "invalid cloud name format",
		},
		{
			name:      "greater than",
			cloudName: "my>cloud",
			wantError: true,
			errorMsg:  "invalid cloud name format",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateCloudName(tt.cloudName)

			if tt.wantError {
				if err == nil {
					t.Errorf("validateCloudName(%q) expected error, got nil", tt.cloudName)
					return
				}
				if tt.errorMsg != "" && !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("validateCloudName(%q) error = %v, want error containing %q", tt.cloudName, err, tt.errorMsg)
				}
			} else {
				if err != nil {
					t.Errorf("validateCloudName(%q) unexpected error: %v", tt.cloudName, err)
				}
			}
		})
	}
}

// TestCloudFlags_Set tests the cloudFlags.Set method with validation
func TestCloudFlags_Set(t *testing.T) {
	tests := []struct {
		name      string
		cloudName string
		wantError bool
		errorMsg  string
	}{
		{
			name:      "valid cloud name",
			cloudName: "mycloud",
			wantError: false,
		},
		{
			name:      "invalid cloud name with special chars",
			cloudName: "my@cloud",
			wantError: true,
			errorMsg:  "invalid cloud name format",
		},
		{
			name:      "empty cloud name",
			cloudName: "",
			wantError: true,
			errorMsg:  "cloud name cannot be empty",
		},
		{
			name:      "path traversal attempt",
			cloudName: "../etc/passwd",
			wantError: true,
			errorMsg:  "invalid cloud name format",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var clouds cloudFlags
			err := clouds.Set(tt.cloudName)

			if tt.wantError {
				if err == nil {
					t.Errorf("cloudFlags.Set(%q) expected error, got nil", tt.cloudName)
					return
				}
				if tt.errorMsg != "" && !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("cloudFlags.Set(%q) error = %v, want error containing %q", tt.cloudName, err, tt.errorMsg)
				}
			} else {
				if err != nil {
					t.Errorf("cloudFlags.Set(%q) unexpected error: %v", tt.cloudName, err)
				}
				if len(clouds) != 1 || clouds[0] != tt.cloudName {
					t.Errorf("cloudFlags.Set(%q) clouds = %v, want [%q]", tt.cloudName, clouds, tt.cloudName)
				}
			}
		})
	}
}

// TestCloudFlags_SetMultiple tests setting multiple cloud names
func TestCloudFlags_SetMultiple(t *testing.T) {
	var clouds cloudFlags

	// Add valid cloud names
	validNames := []string{"cloud1", "cloud2", "cloud3"}
	for _, name := range validNames {
		if err := clouds.Set(name); err != nil {
			t.Errorf("cloudFlags.Set(%q) unexpected error: %v", name, err)
		}
	}

	if len(clouds) != len(validNames) {
		t.Errorf("Expected %d clouds, got %d", len(validNames), len(clouds))
	}

	for i, name := range validNames {
		if clouds[i] != name {
			t.Errorf("clouds[%d] = %q, want %q", i, clouds[i], name)
		}
	}

	// Try to add invalid cloud name
	if err := clouds.Set("invalid@cloud"); err == nil {
		t.Error("Expected error for invalid cloud name, got nil")
	}

	// Verify invalid cloud was not added
	if len(clouds) != len(validNames) {
		t.Errorf("Invalid cloud was added, expected %d clouds, got %d", len(validNames), len(clouds))
	}
}

// TestCloudFlags_String tests the String method
func TestCloudFlags_String(t *testing.T) {
	tests := []struct {
		name   string
		clouds cloudFlags
		want   string
	}{
		{
			name:   "empty",
			clouds: cloudFlags{},
			want:   "",
		},
		{
			name:   "single cloud",
			clouds: cloudFlags{"cloud1"},
			want:   "cloud1",
		},
		{
			name:   "multiple clouds",
			clouds: cloudFlags{"cloud1", "cloud2", "cloud3"},
			want:   "cloud1,cloud2,cloud3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.clouds.String()
			if got != tt.want {
				t.Errorf("cloudFlags.String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractNetmask(t *testing.T) {
	tests := []struct {
		name           string
		ipWithNetmask  string
		expectedResult string
	}{
		{
			name:           "IPv4 with /24 netmask",
			ipWithNetmask:  "192.168.1.10/24",
			expectedResult: "24",
		},
		{
			name:           "IPv4 with /16 netmask",
			ipWithNetmask:  "10.0.0.1/16",
			expectedResult: "16",
		},
		{
			name:           "IPv4 with /32 netmask",
			ipWithNetmask:  "172.16.0.1/32",
			expectedResult: "32",
		},
		{
			name:           "IPv4 with /8 netmask",
			ipWithNetmask:  "10.0.0.0/8",
			expectedResult: "8",
		},
		{
			name:           "IPv4 without netmask",
			ipWithNetmask:  "192.168.1.10",
			expectedResult: "",
		},
		{
			name:           "IPv6 with /64 netmask",
			ipWithNetmask:  "2001:db8::1/64",
			expectedResult: "64",
		},
		{
			name:           "IPv6 with /128 netmask",
			ipWithNetmask:  "fe80::1/128",
			expectedResult: "128",
		},
		{
			name:           "IPv6 without netmask",
			ipWithNetmask:  "2001:db8::1",
			expectedResult: "",
		},
		{
			name:           "Empty string",
			ipWithNetmask:  "",
			expectedResult: "",
		},
		{
			name:           "Just a slash",
			ipWithNetmask:  "/",
			expectedResult: "",
		},
		{
			name:           "Slash at the end",
			ipWithNetmask:  "192.168.1.1/",
			expectedResult: "",
		},
		{
			name:           "Multiple slashes (invalid but handled)",
			ipWithNetmask:  "192.168.1.1/24/32",
			expectedResult: "24/32",
		},
		{
			name:           "Netmask with leading zeros",
			ipWithNetmask:  "10.0.0.1/08",
			expectedResult: "08",
		},
		{
			name:           "Private network class C",
			ipWithNetmask:  "192.168.0.1/24",
			expectedResult: "24",
		},
		{
			name:           "Private network class B",
			ipWithNetmask:  "172.16.0.1/16",
			expectedResult: "16",
		},
		{
			name:           "Private network class A",
			ipWithNetmask:  "10.0.0.1/8",
			expectedResult: "8",
		},
		{
			name:           "Subnet with /28",
			ipWithNetmask:  "192.168.1.1/28",
			expectedResult: "28",
		},
		{
			name:           "Single host /32",
			ipWithNetmask:  "203.0.113.1/32",
			expectedResult: "32",
		},
		{
			name:           "Link-local IPv6",
			ipWithNetmask:  "fe80::1/64",
			expectedResult: "64",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractNetmask(tt.ipWithNetmask)
			if result != tt.expectedResult {
				t.Errorf("extractNetmask(%q) = %q, want %q",
					tt.ipWithNetmask, result, tt.expectedResult)
			}
		})
	}
}

// TestBuildResolvConf tests the buildResolvConf function with various inputs
func TestBuildResolvConf(t *testing.T) {
	tests := []struct {
		name        string
		nameservers []string
		expected    string
	}{
		{
			name:        "empty array",
			nameservers: []string{},
			expected:    "",
		},
		{
			name:        "nil array",
			nameservers: nil,
			expected:    "",
		},
		{
			name:        "single nameserver",
			nameservers: []string{"8.8.8.8"},
			expected:    "nameserver 8.8.8.8\n",
		},
		{
			name:        "two nameservers",
			nameservers: []string{"8.8.8.8", "8.8.4.4"},
			expected:    "nameserver 8.8.8.8\nnameserver 8.8.4.4\n",
		},
		{
			name:        "three nameservers",
			nameservers: []string{"8.8.8.8", "8.8.4.4", "1.1.1.1"},
			expected:    "nameserver 8.8.8.8\nnameserver 8.8.4.4\nnameserver 1.1.1.1\n",
		},
		{
			name:        "private network nameservers",
			nameservers: []string{"192.168.1.1", "10.0.0.1"},
			expected:    "nameserver 192.168.1.1\nnameserver 10.0.0.1\n",
		},
		{
			name:        "IPv6 nameservers",
			nameservers: []string{"2001:4860:4860::8888", "2001:4860:4860::8844"},
			expected:    "nameserver 2001:4860:4860::8888\nnameserver 2001:4860:4860::8844\n",
		},
		{
			name:        "mixed IPv4 and IPv6",
			nameservers: []string{"8.8.8.8", "2001:4860:4860::8888"},
			expected:    "nameserver 8.8.8.8\nnameserver 2001:4860:4860::8888\n",
		},
		{
			name:        "hostname nameservers",
			nameservers: []string{"dns1.example.com", "dns2.example.com"},
			expected:    "nameserver dns1.example.com\nnameserver dns2.example.com\n",
		},
		{
			name:        "empty string in array",
			nameservers: []string{"", "8.8.8.8", ""},
			expected:    "nameserver \nnameserver 8.8.8.8\nnameserver \n",
		},
		{
			name:        "nameservers with CIDR notation",
			nameservers: []string{"192.168.1.1/24", "10.0.0.1/16"},
			expected:    "nameserver 192.168.1.1/24\nnameserver 10.0.0.1/16\n",
		},
		{
			name:        "long list of nameservers",
			nameservers: []string{"1.1.1.1", "8.8.8.8", "8.8.4.4", "9.9.9.9", "149.112.112.112"},
			expected:    "nameserver 1.1.1.1\nnameserver 8.8.8.8\nnameserver 8.8.4.4\nnameserver 9.9.9.9\nnameserver 149.112.112.112\n",
		},
		{
			name:        "localhost nameserver",
			nameservers: []string{"127.0.0.1"},
			expected:    "nameserver 127.0.0.1\n",
		},
		{
			name:        "link-local IPv6",
			nameservers: []string{"fe80::1"},
			expected:    "nameserver fe80::1\n",
		},
		{
			name:        "nameservers with ports (non-standard)",
			nameservers: []string{"8.8.8.8:53", "1.1.1.1:5353"},
			expected:    "nameserver 8.8.8.8:53\nnameserver 1.1.1.1:5353\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildResolvConf(tt.nameservers)
			if result != tt.expected {
				t.Errorf("buildResolvConf() = %q, want %q", result, tt.expected)
			}
		})
	}
}

// TestBuildResolvConfLength tests that the output has correct number of lines
func TestBuildResolvConfLength(t *testing.T) {
	tests := []struct {
		name          string
		nameservers   []string
		expectedLines int
	}{
		{
			name:          "empty array produces no lines",
			nameservers:   []string{},
			expectedLines: 0,
		},
		{
			name:          "single nameserver produces one line",
			nameservers:   []string{"8.8.8.8"},
			expectedLines: 1,
		},
		{
			name:          "five nameservers produce five lines",
			nameservers:   []string{"1.1.1.1", "8.8.8.8", "8.8.4.4", "9.9.9.9", "149.112.112.112"},
			expectedLines: 5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildResolvConf(tt.nameservers)
			if result == "" && tt.expectedLines == 0 {
				return // Empty result is correct for empty input
			}

			// Trim trailing newline before splitting to get actual line count
			result = strings.TrimSuffix(result, "\n")
			lines := strings.Split(result, "\n")
			if len(lines) != tt.expectedLines {
				t.Errorf("buildResolvConf() produced %d lines, want %d", len(lines), tt.expectedLines)
			}
		})
	}
}

// TestBuildResolvConfFormat tests that each line has correct format
func TestBuildResolvConfFormat(t *testing.T) {
	nameservers := []string{"8.8.8.8", "8.8.4.4", "1.1.1.1"}
	result := buildResolvConf(nameservers)

	// Trim trailing newline before splitting to avoid empty last element
	result = strings.TrimSuffix(result, "\n")
	lines := strings.Split(result, "\n")

	if len(lines) != len(nameservers) {
		t.Errorf("Expected %d lines, got %d", len(nameservers), len(lines))
	}

	for i, line := range lines {
		expectedPrefix := "nameserver "

		if !strings.HasPrefix(line, expectedPrefix) {
			t.Errorf("Line %d does not start with 'nameserver '. Got: %q", i+1, line)
		}

		// Verify the nameserver value is correct
		expectedLine := fmt.Sprintf("nameserver %s", nameservers[i])
		if line != expectedLine {
			t.Errorf("Line %d format incorrect. Got: %q, want: %q", i+1, line, expectedLine)
		}
	}
}

// Made with Bob
