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

// Made with Bob
