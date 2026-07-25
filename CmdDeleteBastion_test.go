// Copyright 2025 IBM Corp
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
	"errors"
	"flag"
	"strings"
	"testing"
)

// TestDeleteConfig_Validate tests the validation logic for deleteConfig.
func TestDeleteConfig_Validate(t *testing.T) {
	tests := []struct {
		name      string
		config    deleteConfig
		wantErr   bool
		errField  string
		errSubstr string
	}{
		{
			name: "valid config",
			config: deleteConfig{
				Clouds:      cloudFlags{"mycloud"},
				BastionName: "my-bastion",
			},
			wantErr: false,
		},
		{
			name: "empty clouds slice",
			config: deleteConfig{
				Clouds:      cloudFlags{},
				BastionName: "my-bastion",
			},
			wantErr:  true,
			errField: "Cloud",
		},
		{
			name: "blank cloud entry",
			config: deleteConfig{
				Clouds:      cloudFlags{""},
				BastionName: "my-bastion",
			},
			wantErr:  true,
			errField: "Cloud",
		},
		{
			name: "missing bastion name",
			config: deleteConfig{
				Clouds:      cloudFlags{"mycloud"},
				BastionName: "",
			},
			wantErr:  true,
			errField: "BastionName",
		},
		{
			name: "both fields missing",
			config: deleteConfig{
				Clouds:      cloudFlags{},
				BastionName: "",
			},
			wantErr:  true,
			errField: "Cloud",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.validate()

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				var ve *ValidationError
				if errors.As(err, &ve) {
					if ve.Field != tt.errField {
						t.Errorf("expected Field=%q, got %q", tt.errField, ve.Field)
					}
				} else {
					t.Errorf("expected *ValidationError, got %T: %v", err, err)
				}
				if tt.errSubstr != "" && !strings.Contains(err.Error(), tt.errSubstr) {
					t.Errorf("expected error to contain %q, got: %v", tt.errSubstr, err)
				}
			} else {
				if err != nil {
					t.Errorf("expected no error, got: %v", err)
				}
			}
		})
	}
}

// TestParseDeleteBastionFlags tests flag parsing and config construction.
func TestParseDeleteBastionFlags(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		expectError bool
		errorMsg    string
		checkConfig func(*testing.T, *deleteConfig)
	}{
		{
			name: "valid minimal flags",
			args: []string{
				"--cloud", "mycloud",
				"--bastionName", "my-bastion",
			},
			checkConfig: func(t *testing.T, c *deleteConfig) {
				if len(c.Clouds) != 1 || c.Clouds[0] != "mycloud" {
					t.Errorf("expected Clouds=[mycloud], got %v", c.Clouds)
				}
				if c.BastionName != "my-bastion" {
					t.Errorf("expected BastionName=my-bastion, got %q", c.BastionName)
				}
				if c.ShouldDebug {
					t.Error("expected ShouldDebug=false by default")
				}
			},
		},
		{
			name: "shouldDebug true",
			args: []string{
				"--cloud", "mycloud",
				"--bastionName", "my-bastion",
				"--shouldDebug", "true",
			},
			checkConfig: func(t *testing.T, c *deleteConfig) {
				if !c.ShouldDebug {
					t.Error("expected ShouldDebug=true")
				}
			},
		},
		{
			name: "shouldDebug yes",
			args: []string{
				"--cloud", "mycloud",
				"--bastionName", "my-bastion",
				"--shouldDebug", "yes",
			},
			checkConfig: func(t *testing.T, c *deleteConfig) {
				if !c.ShouldDebug {
					t.Error("expected ShouldDebug=true for 'yes'")
				}
			},
		},
		{
			name: "missing cloud",
			args: []string{
				"--bastionName", "my-bastion",
			},
			expectError: true,
			errorMsg:    "Cloud",
		},
		{
			name: "missing bastion name",
			args: []string{
				"--cloud", "mycloud",
			},
			expectError: true,
			errorMsg:    "BastionName",
		},
		{
			name:        "no flags at all",
			args:        []string{},
			expectError: true,
			errorMsg:    "Cloud",
		},
		{
			name: "invalid shouldDebug value",
			args: []string{
				"--cloud", "mycloud",
				"--bastionName", "my-bastion",
				"--shouldDebug", "maybe",
			},
			expectError: true,
			errorMsg:    "shouldDebug must be a boolean value",
		},
		{
			name: "invalid cloud name rejected by cloudFlags",
			args: []string{
				"--cloud", "bad cloud!",
				"--bastionName", "my-bastion",
			},
			expectError: true,
			errorMsg:    "failed to parse flags",
		},
		{
			name: "unknown flag",
			args: []string{
				"--cloud", "mycloud",
				"--bastionName", "my-bastion",
				"--unknownFlag", "value",
			},
			expectError: true,
			errorMsg:    "failed to parse flags",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flagSet := flag.NewFlagSet("delete-bastion", flag.ContinueOnError)
			config, err := parseDeleteBastionFlags(flagSet, tt.args)

			if tt.expectError {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errorMsg != "" && !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("expected error containing %q, got: %v", tt.errorMsg, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("expected no error, got: %v", err)
			}
			if tt.checkConfig != nil {
				tt.checkConfig(t, config)
			}
		})
	}
}

// TestDeleteBastionCommand_MissingFlags verifies that deleteBastionCommand returns
// an error (and does not panic) when required flags are absent.
func TestDeleteBastionCommand_MissingFlags(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		errorMsg string
	}{
		{
			name:     "no flags",
			args:     []string{},
			errorMsg: "Cloud",
		},
		{
			name:     "missing bastion name",
			args:     []string{"--cloud", "mycloud"},
			errorMsg: "BastionName",
		},
		{
			name:     "missing cloud",
			args:     []string{"--bastionName", "my-bastion"},
			errorMsg: "Cloud",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flagSet := flag.NewFlagSet("delete-bastion", flag.ContinueOnError)
			err := deleteBastionCommand(flagSet, tt.args)

			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.errorMsg) {
				t.Errorf("expected error containing %q, got: %v", tt.errorMsg, err)
			}
		})
	}
}

// Made with IBM Bob
