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
	"encoding/json"
	"fmt"
	"os"

	configv1 "github.com/openshift/api/config/v1"
)

// Note: This file uses the global 'log' variable declared in PowerVC-Tool.go

// Metadata holds cluster metadata information loaded from the OpenShift installer's
// metadata.json file. It provides access to cluster identification, infrastructure
// details, and platform-specific configuration.
type Metadata struct {
	createMetadata CreateMetadata
}

// CreateMetadata contains the core cluster metadata fields from the OpenShift installer.
// This structure matches the format of the metadata.json file created during cluster installation.
type CreateMetadata struct {
	// ClusterName is the user-defined name of the OpenShift cluster
	ClusterName string `json:"clusterName"`

	// ClusterID is the unique identifier for the cluster
	ClusterID string `json:"clusterID"`

	// InfraID is the infrastructure identifier used as a prefix for resource names
	InfraID string `json:"infraID"`

	// OSClusterPlatformMetadata contains OpenStack-specific metadata
	OSClusterPlatformMetadata `json:",inline"`

	// PVClusterPlatformMetadata contains PowerVC-specific metadata
	PVClusterPlatformMetadata `json:",inline"`

	// FeatureSet specifies the OpenShift feature set enabled for the cluster
	FeatureSet configv1.FeatureSet `json:"featureSet"`

	// CustomFeatureSet contains custom feature gate configuration if applicable
	CustomFeatureSet *configv1.CustomFeatureGates `json:"customFeatureSet"`
}

// OSClusterPlatformMetadata contains OpenStack platform-specific metadata.
type OSClusterPlatformMetadata struct {
	// OpenStack contains OpenStack cloud configuration if the cluster is on OpenStack
	OpenStack *OpenStackSMetadata `json:"openstack,omitempty"`
}

// PVClusterPlatformMetadata contains PowerVC platform-specific metadata.
type PVClusterPlatformMetadata struct {
	// PowerVC contains PowerVC cloud configuration if the cluster is on PowerVC
	PowerVC *OpenStackSMetadata `json:"powervc,omitempty"`
}

// OpenStackIdentifier contains OpenStack-specific cluster identification.
type OpenStackIdentifier struct {
	// OpenshiftClusterID is the cluster identifier used in OpenStack
	OpenshiftClusterID string `json:"openshiftClusterID"`
}

// OpenStackSMetadata contains metadata for OpenStack-based platforms (OpenStack and PowerVC).
// PowerVC uses the same metadata structure as OpenStack since it's based on OpenStack.
type OpenStackSMetadata struct {
	// Cloud is the name of the cloud configuration from clouds.yaml
	Cloud string `json:"cloud"`

	// Identifier contains cluster identification information
	Identifier OpenStackIdentifier `json:"identifier"`
}

// NewMetadataFromCCMetadata loads cluster metadata from a JSON file.
// This function reads the metadata.json file created by the OpenShift installer
// and parses it into a Metadata structure.
//
// Parameters:
//   - filename: Path to the metadata.json file
//
// Returns:
//   - *Metadata: Parsed metadata structure
//   - error: Any error encountered during file reading or JSON parsing
//
// Example:
//   metadata, err := NewMetadataFromCCMetadata("./metadata.json")
//   if err != nil {
//       return fmt.Errorf("failed to load metadata: %w", err)
//   }
//   clusterName := metadata.GetClusterName()
func NewMetadataFromCCMetadata(filename string) (*Metadata, error) {
	if filename == "" {
		return nil, fmt.Errorf("filename cannot be empty")
	}

	log.Debugf("NewMetadataFromCCMetadata: Loading metadata from file: %s", filename)

	// Read the file content
	content, err := os.ReadFile(filename)
	if err != nil {
		log.Debugf("NewMetadataFromCCMetadata: Failed to read file %s: %v", filename, err)
		return nil, fmt.Errorf("failed to read metadata file %q: %w", filename, err)
	}

	log.Debugf("NewMetadataFromCCMetadata: Read %d bytes from file", len(content))
	log.Debugf("NewMetadataFromCCMetadata: content = %s", string(content))

	// Parse the JSON content
	var metadata Metadata
	if err := json.Unmarshal(content, &metadata.createMetadata); err != nil {
		log.Debugf("NewMetadataFromCCMetadata: Failed to unmarshal JSON: %v", err)
		return nil, fmt.Errorf("failed to parse metadata JSON from %q: %w", filename, err)
	}

	// Validate required fields
	if metadata.createMetadata.ClusterName == "" {
		log.Debugf("NewMetadataFromCCMetadata: Warning - ClusterName is empty")
	}
	if metadata.createMetadata.InfraID == "" {
		log.Debugf("NewMetadataFromCCMetadata: Warning - InfraID is empty")
	}

	log.Debugf("NewMetadataFromCCMetadata: Successfully loaded metadata")
	log.Debugf("NewMetadataFromCCMetadata: ClusterName=%s, InfraID=%s",
		metadata.createMetadata.ClusterName, metadata.createMetadata.InfraID)
	log.Debugf("NewMetadataFromCCMetadata: OpenStack=%+v", metadata.createMetadata.OpenStack)
	log.Debugf("NewMetadataFromCCMetadata: PowerVC=%+v", metadata.createMetadata.PowerVC)

	return &metadata, nil
}

// GetClusterName returns the name of the OpenShift cluster.
// Returns an empty string if the metadata is not initialized.
//
// Returns:
//   - string: The cluster name
func (m *Metadata) GetClusterName() string {
	if m == nil {
		log.Debugf("GetClusterName: Metadata is nil, returning empty string")
		return ""
	}
	return m.createMetadata.ClusterName
}

// GetClusterID returns the unique identifier of the OpenShift cluster.
// Returns an empty string if the metadata is not initialized.
//
// Returns:
//   - string: The cluster ID
func (m *Metadata) GetClusterID() string {
	if m == nil {
		log.Debugf("GetClusterID: Metadata is nil, returning empty string")
		return ""
	}
	return m.createMetadata.ClusterID
}

// GetInfraID returns the infrastructure identifier used as a prefix for resource names.
// Returns an empty string if the metadata is not initialized.
//
// Returns:
//   - string: The infrastructure ID
func (m *Metadata) GetInfraID() string {
	if m == nil {
		log.Debugf("GetInfraID: Metadata is nil, returning empty string")
		return ""
	}
	return m.createMetadata.InfraID
}

// GetCloud returns the cloud configuration name from either OpenStack or PowerVC metadata.
// It checks OpenStack first, then PowerVC. Returns an empty string if neither is configured.
//
// Returns:
//   - string: The cloud configuration name, or empty string if not found
func (m *Metadata) GetCloud() string {
	if m == nil {
		log.Debugf("GetCloud: Metadata is nil, returning empty string")
		return ""
	}

	if m.createMetadata.OpenStack != nil {
		log.Debugf("GetCloud: Using OpenStack cloud: %s", m.createMetadata.OpenStack.Cloud)
		return m.createMetadata.OpenStack.Cloud
	}

	if m.createMetadata.PowerVC != nil {
		log.Debugf("GetCloud: Using PowerVC cloud: %s", m.createMetadata.PowerVC.Cloud)
		return m.createMetadata.PowerVC.Cloud
	}

	log.Debugf("GetCloud: No cloud configuration found")
	return ""
}

// GetFeatureSet returns the OpenShift feature set enabled for the cluster.
// Returns an empty FeatureSet if the metadata is not initialized.
//
// Returns:
//   - configv1.FeatureSet: The feature set configuration
func (m *Metadata) GetFeatureSet() configv1.FeatureSet {
	if m == nil {
		log.Debugf("GetFeatureSet: Metadata is nil, returning empty FeatureSet")
		return ""
	}
	return m.createMetadata.FeatureSet
}

// GetCustomFeatureSet returns the custom feature gate configuration if applicable.
// Returns nil if no custom feature set is configured or if metadata is not initialized.
//
// Returns:
//   - *configv1.CustomFeatureGates: The custom feature gates, or nil if not configured
func (m *Metadata) GetCustomFeatureSet() *configv1.CustomFeatureGates {
	if m == nil {
		log.Debugf("GetCustomFeatureSet: Metadata is nil, returning nil")
		return nil
	}
	return m.createMetadata.CustomFeatureSet
}

// GetOpenshiftClusterID returns the OpenShift cluster ID from the platform metadata.
// It checks OpenStack first, then PowerVC. Returns an empty string if neither is configured.
//
// Returns:
//   - string: The OpenShift cluster ID, or empty string if not found
func (m *Metadata) GetOpenshiftClusterID() string {
	if m == nil {
		log.Debugf("GetOpenshiftClusterID: Metadata is nil, returning empty string")
		return ""
	}

	if m.createMetadata.OpenStack != nil {
		return m.createMetadata.OpenStack.Identifier.OpenshiftClusterID
	}

	if m.createMetadata.PowerVC != nil {
		return m.createMetadata.PowerVC.Identifier.OpenshiftClusterID
	}

	log.Debugf("GetOpenshiftClusterID: No platform metadata found")
	return ""
}

// IsOpenStack returns true if the cluster is running on OpenStack.
//
// Returns:
//   - bool: true if OpenStack metadata is present, false otherwise
func (m *Metadata) IsOpenStack() bool {
	if m == nil {
		return false
	}
	return m.createMetadata.OpenStack != nil
}

// IsPowerVC returns true if the cluster is running on PowerVC.
//
// Returns:
//   - bool: true if PowerVC metadata is present, false otherwise
func (m *Metadata) IsPowerVC() bool {
	if m == nil {
		return false
	}
	return m.createMetadata.PowerVC != nil
}
