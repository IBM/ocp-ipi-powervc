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
	"testing"
)

func TestNewBastionConfig(t *testing.T) {
	config := NewBastionConfig()
	if !config.EnableHAProxy {
		t.Error("Expected EnableHAProxy to be true by default")
	}
}

func TestBastionConfigValidation(t *testing.T) {
	tests := []struct {
		name    string
		config  *BastionConfig
		wantErr bool
	}{
		{
			name: "valid config with HAProxy",
			config: &BastionConfig{
				Cloud:         "mycloud",
				BastionName:   "bastion-1",
				BastionRsa:    "/path/to/key",
				FlavorName:    "m1.small",
				ImageName:     "rhel-8",
				NetworkName:   "private",
				SshKeyName:    "mykey",
				EnableHAProxy: true,
			},
			wantErr: false,
		},
		{
			name: "valid config without HAProxy",
			config: &BastionConfig{
				Cloud:         "mycloud",
				BastionName:   "bastion-1",
				BastionRsa:    "/path/to/key",
				FlavorName:    "m1.small",
				ImageName:     "rhel-8",
				NetworkName:   "private",
				SshKeyName:    "mykey",
				EnableHAProxy: false,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
