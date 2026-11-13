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

// OpenStack.go provides direct OpenStack (Glance) image access via gophercloud,
// replacing the previous approach of shelling out to the "openstack" CLI.
//
// The routines here are adapted from the main ocp-ipi-powervc tool's
// OpenStack.go.  Because this program is a separate Go module with its own
// colourised, config-driven logging (see config.logDebug), the functions are
// methods on *config and log through c.logDebug rather than a global logrus
// logger.
package main

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack"
	osconfig "github.com/gophercloud/gophercloud/v2/openstack/config"
	"github.com/gophercloud/gophercloud/v2/openstack/config/clouds"
	"github.com/gophercloud/gophercloud/v2/openstack/image/v2/images"
	"github.com/gophercloud/gophercloud/v2/pagination"

	"k8s.io/apimachinery/pkg/util/wait"
)

const (
	// Backoff configuration constants for retry logic.
	defaultBackoffDuration = 1 * time.Minute
	defaultBackoffFactor   = 1.1
	defaultBackoffSteps    = math.MaxInt32
)

// leftInContext returns the time remaining before ctx's deadline.  When ctx has
// no deadline it returns a very large duration so callers treat it as "no cap".
func leftInContext(ctx context.Context) time.Duration {
	deadline, ok := ctx.Deadline()
	if !ok {
		return math.MaxInt64
	}
	remaining := time.Until(deadline)
	if remaining < 0 {
		return 0
	}
	return remaining
}

// getUserAgent generates a Gophercloud UserAgent to help cloud operators
// disambiguate our requests.
func getUserAgent() gophercloud.UserAgent {
	ua := gophercloud.UserAgent{}
	ua.Prepend("upload-rhcos/1.0")
	return ua
}

// createDefaultBackoff creates a standard backoff configuration for retry logic.
func createDefaultBackoff(ctx context.Context) wait.Backoff {
	return wait.Backoff{
		Duration: defaultBackoffDuration,
		Factor:   defaultBackoffFactor,
		Cap:      leftInContext(ctx),
		Steps:    defaultBackoffSteps,
	}
}

// getServiceClient creates an OpenStack image service client with retry logic,
// using exponential backoff to handle transient failures.
//
// # Why we cannot use clientconfig.NewServiceClient directly
//
// clientconfig.NewServiceClient calls openstack.AuthenticatedClient internally,
// which discards the ClientOpts (including any custom HTTP client) before it
// makes the first TLS connection to the Keystone identity endpoint.  On a
// PowerVC with a self-signed or privately-signed CA certificate this produces:
//
//	tls: failed to verify certificate: x509: certificate signed by unknown authority
//
// The fix uses clouds.Parse (which builds a *tls.Config from the cacert field
// in clouds.yaml) and osconfig.NewProviderClient (which injects that TLS config
// into the transport before authentication), matching the pattern shown in the
// gophercloud documentation:
//
//	ao, _, tlsConfig, _ := clouds.Parse(clouds.WithCloudName(cloud))
//	providerClient, _   := osconfig.NewProviderClient(ctx, ao, osconfig.WithTLSConfig(tlsConfig))
func (c *config) getServiceClient(ctx context.Context, serviceType, cloud string) (client *gophercloud.ServiceClient, err error) {
	if serviceType == "" {
		return nil, fmt.Errorf("service type cannot be empty")
	}
	if cloud == "" {
		return nil, fmt.Errorf("cloud name cannot be empty")
	}

	// Resolve ~/.config/openstack/clouds.yaml explicitly so that clouds.Parse
	// always reads the user's file regardless of the current working directory.
	// Without this, gophercloud checks $CWD/clouds.yaml first, which can pick
	// up a stale or incomplete file when the binary is run from a checkout that
	// contains its own clouds.yaml.
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("cannot determine home directory: %w", err)
	}
	cloudsYAMLPath := filepath.Join(homeDir, ".config", "openstack", "clouds.yaml")

	// Parse clouds.yaml once up-front to validate the cloud name and retrieve
	// the TLS config (built from the cacert field, if present).
	ao, _, tlsCfg, err := clouds.Parse(
		clouds.WithCloudName(cloud),
		clouds.WithLocations(cloudsYAMLPath),
	)
	if err != nil {
		return nil, err
	}
	if tlsCfg == nil {
		c.logWarning("no cacert found in clouds.yaml for cloud %q — TLS certificate verification may fail", cloud)
	} else {
		c.logDebug("TLS: loaded CA cert from clouds.yaml for cloud %q", cloud)
	}

	backoff := createDefaultBackoff(ctx)

	err = wait.ExponentialBackoffWithContext(ctx, backoff, func(context.Context) (bool, error) {
		c.logDebug("getServiceClient: duration = %v, calling NewProviderClient(%s, %s)", leftInContext(ctx), serviceType, cloud)

		providerClient, err2 := osconfig.NewProviderClient(ctx, ao, osconfig.WithTLSConfig(tlsCfg))
		if err2 != nil {
			c.logDebug("getServiceClient: Error: NewProviderClient returns error %v", err2)
			if strings.Contains(err2.Error(), "authentication") {
				return false, err2
			}
			return false, nil
		}
		providerClient.UserAgent = getUserAgent()

		client, err2 = openstack.NewImageV2(providerClient, gophercloud.EndpointOpts{})
		if err2 != nil {
			c.logDebug("getServiceClient: Error: NewImageV2 returns error %v", err2)
			return false, nil
		}

		return true, nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to create %s service client for cloud %s: %w", serviceType, cloud, err)
	}

	return client, nil
}

// getAllImages retrieves all images from the specified OpenStack cloud, with
// pagination and exponential backoff retry on transient API failures.
func (c *config) getAllImages(ctx context.Context, cloudName string) (allImages []images.Image, err error) {
	if cloudName == "" {
		return nil, fmt.Errorf("cloud name cannot be empty")
	}

	var pager pagination.Page

	connImage, err := c.getServiceClient(ctx, "image", cloudName)
	if err != nil {
		return nil, fmt.Errorf("failed to get image service client: %w", err)
	}

	backoff := createDefaultBackoff(ctx)

	err = wait.ExponentialBackoffWithContext(ctx, backoff, func(context.Context) (bool, error) {
		var err2 error

		c.logDebug("getAllImages: duration = %v, calling images.List", leftInContext(ctx))
		pager, err2 = images.List(connImage, images.ListOpts{}).AllPages(ctx)
		if err2 != nil {
			c.logDebug("getAllImages: images.List returned error: %v", err2)
			return false, nil
		}

		allImages, err2 = images.ExtractImages(pager)
		if err2 != nil {
			c.logDebug("getAllImages: images.ExtractImages returned error: %v", err2)
			return false, nil
		}

		return true, nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list images: %w", err)
	}

	return allImages, nil
}

// findImage searches for an OpenStack image by name (or ID) and returns the
// matching image, or an error if none is found or the API call fails.
func (c *config) findImage(ctx context.Context, cloudName, name string) (foundImage images.Image, err error) {
	if cloudName == "" {
		return images.Image{}, fmt.Errorf("cloud name cannot be empty")
	}
	if name == "" {
		return images.Image{}, fmt.Errorf("image name cannot be empty")
	}

	allImages, err := c.getAllImages(ctx, cloudName)
	if err != nil {
		return images.Image{}, err
	}

	for _, image := range allImages {
		c.logDebug("findImage: checking image.Name = %s, image.ID = %s", image.Name, image.ID)

		if image.Name == name || image.ID == name {
			c.logDebug("findImage: found image %s with ID %s", image.Name, image.ID)
			return image, nil
		}
	}

	return images.Image{}, fmt.Errorf("could not find image named %s", name)
}
