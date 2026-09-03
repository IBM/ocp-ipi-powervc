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
	"strings"
	"time"

	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack/config/clouds"
	"github.com/gophercloud/gophercloud/v2/openstack/image/v2/images"
	"github.com/gophercloud/gophercloud/v2/pagination"
	"github.com/gophercloud/utils/v2/openstack/clientconfig"

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

// defaultClientOpts generates default client opts based on cloud name.
func defaultClientOpts(cloudName string) *clientconfig.ClientOpts {
	opts := new(clientconfig.ClientOpts)
	opts.Cloud = cloudName
	// Explicitly disable reading auth data from env variables by setting an
	// invalid EnvPrefix, so clouds.yaml alone is enough to authenticate.
	opts.EnvPrefix = "NO_ENV_VARIABLES_"
	return opts
}

// newServiceClient wraps Gophercloud's NewServiceClient and consistently sets a
// user-agent.
func newServiceClient(ctx context.Context, service string, opts *clientconfig.ClientOpts) (*gophercloud.ServiceClient, error) {
	client, err := clientconfig.NewServiceClient(ctx, service, opts)
	if err != nil {
		return nil, err
	}
	client.UserAgent = getUserAgent()
	return client, nil
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

// getServiceClient creates an OpenStack service client with retry logic, using
// exponential backoff to handle transient failures.
func (c *config) getServiceClient(ctx context.Context, serviceType, cloud string) (client *gophercloud.ServiceClient, err error) {
	if serviceType == "" {
		return nil, fmt.Errorf("service type cannot be empty")
	}
	if cloud == "" {
		return nil, fmt.Errorf("cloud name cannot be empty")
	}

	// Test for the existence of the cloud name in clouds.yaml.
	if _, _, _, err = clouds.Parse(clouds.WithCloudName(cloud)); err != nil {
		return nil, err
	}

	backoff := createDefaultBackoff(ctx)

	err = wait.ExponentialBackoffWithContext(ctx, backoff, func(context.Context) (bool, error) {
		var err2 error

		c.logDebug("getServiceClient: duration = %v, calling NewServiceClient(%s, %s)", leftInContext(ctx), serviceType, cloud)
		client, err2 = newServiceClient(ctx, serviceType, defaultClientOpts(cloud))
		if err2 != nil {
			c.logDebug("getServiceClient: Error: NewServiceClient returns error %v", err2)
			// Stop retrying on authentication errors.
			if strings.Contains(err2.Error(), "authentication") {
				return false, err2
			}
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
