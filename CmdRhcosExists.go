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

// CmdRhcosExists.go implements the rhcos-exists command for verifying RHCOS image availability.
//
// The rhcos-exists command checks whether a specified RHCOS (Red Hat CoreOS) image exists
// in an OpenStack cloud environment by querying the Glance image service. This is a critical
// pre-flight check before attempting cluster installation, as it validates that the required
// boot image is available in the target cloud.
//
// Command Usage:
//
//	ocp-ipi-powervc rhcos-exists --cloud <cloud-name> --imageName <image-name> [--shouldDebug <true|false>]
//
// Flags:
//
//	--cloud (required): The OpenStack cloud name from clouds.yaml (exactly one required)
//	--imageName (required): The name of the RHCOS image to search for
//	--shouldDebug (optional): Enable verbose debug logging (default: false)
//
// Examples:
//
//	# Check if a specific RHCOS image exists
//	ocp-ipi-powervc rhcos-exists --cloud mycloud --imageName rhcos-4.12.0
//
//	# Check with debug output enabled
//	ocp-ipi-powervc rhcos-exists --cloud mycloud --imageName rhcos-4.12.0 --shouldDebug true
//
//	# Check for a nightly build image
//	ocp-ipi-powervc rhcos-exists --cloud powervc --imageName rhcos-4.13.0-nightly-2023-01-15
//
// Behavior:
//
//	If the image is found:
//	  - Prints "Found image <name>" to stdout
//	  - Returns exit code 0
//
//	If the image is not found:
//	  - Prints error message to stdout
//	  - Lists all available images with their IDs and names
//	  - Returns exit code 1
//
// Use Cases:
//
//	- Pre-flight validation before OpenShift cluster installation
//	- Verifying image availability across different OpenStack environments
//	- Troubleshooting image naming issues and typos
//	- Discovering available RHCOS images in a cloud
//	- Automation scripts that need to verify prerequisites
//
// Exit Codes:
//
//	0: Image exists and was found successfully
//	1: Image not found, validation error, or other failure
//
// Notes:
//
//	- The command uses a 2-minute timeout for OpenStack API operations
//	- Requires valid OpenStack credentials in clouds.yaml
//	- Image names are case-sensitive and must match exactly
//	- The cloud name must correspond to an entry in clouds.yaml
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"
)

const (
	// flagRhcosExistsCloud is the command-line flag name for specifying the OpenStack cloud
	flagRhcosExistsCloud = "cloud"

	// flagRhcosExistsImageName is the command-line flag name for specifying the image name to search for
	flagRhcosExistsImageName = "imageName"

	// flagRhcosExistsShouldDebug is the command-line flag name for enabling debug output
	flagRhcosExistsShouldDebug = "shouldDebug"

	// defaultRhcosExistsImageName is the default value for the imageName flag (empty, must be provided by user)
	defaultRhcosExistsImageName = ""

	// defaultRhcosExistsShouldDebug is the default value for the debug flag (disabled by default)
	defaultRhcosExistsShouldDebug = "false"

	// usageRhcosExistsCloud is the help text displayed for the cloud flag
	usageRhcosExistsCloud = "Cloud name to use in clouds.yaml"

	// usageRhcosExistsImageName is the help text displayed for the imageName flag
	usageRhcosExistsImageName = "The name of the image"

	// usageRhcosExistsShouldDebug is the help text displayed for the shouldDebug flag
	usageRhcosExistsShouldDebug = "Enable debug output (true/false)"

	// errPrefixRhcosExists is the error message prefix for all rhcos-exists command errors
	errPrefixRhcosExists = "[rhcos-exists] "
)

// rhcosExistsCommand is the entry point for the rhcos-exists command.
// It wraps innerRhcosExistsCommand to provide consistent error handling and usage display.
//
// Parameters:
//   - rhcosExistsFlags: The flag set containing command-line flags for this command
//   - args: The command-line arguments to parse
//
// Returns:
//   - error: Any error encountered during command execution
//
// This function prints errors to stdout and displays usage information on failure.
func rhcosExistsCommand(rhcosExistsFlags *flag.FlagSet, args []string) error {
	err := innerRhcosExistsCommand(rhcosExistsFlags, args)
	if err != nil {
		// Print the full error with stack trace if available
		fmt.Printf("%+v\n", err)
		// Display usage information to help the user correct the error
		if rhcosExistsFlags != nil {
			rhcosExistsFlags.Usage()
		}
	}
	return err
}

// innerRhcosExistsCommand implements the core logic for the rhcos-exists command.
// It checks whether a specified RHCOS (Red Hat CoreOS) image exists in an OpenStack cloud.
//
// The command performs the following operations:
//  1. Validates input parameters (flags and args)
//  2. Parses command-line flags (cloud name, image name, debug flag)
//  3. Validates required fields (cloud and imageName must be provided)
//  4. Initializes logging based on debug mode
//  5. Searches for the specified image in the OpenStack cloud
//  6. If found, reports success; if not found, lists all available images
//
// Parameters:
//   - rhcosExistsFlags: The flag set containing command-line flags for this command
//   - args: The command-line arguments to parse
//
// Returns:
//   - error: nil if the image exists, or an error describing the failure
//
// Required flags:
//   - cloud: The OpenStack cloud name from clouds.yaml (exactly one required)
//   - imageName: The name of the RHCOS image to search for
//
// Optional flags:
//   - shouldDebug: Enable verbose debug logging (default: false)
func innerRhcosExistsCommand(rhcosExistsFlags *flag.FlagSet, args []string) error {
	// Validate input parameters to prevent nil pointer dereferences
	if rhcosExistsFlags == nil {
		return fmt.Errorf("%sflag set cannot be nil", errPrefixRhcosExists)
	}
	if args == nil {
		return fmt.Errorf("%sargs cannot be nil", errPrefixRhcosExists)
	}

	// Display version information for debugging and support purposes
	fmt.Fprintf(os.Stderr, "Program version is %v, release = %v\n", version, release)

	// Define command-line flags
	// cloudFlags is a custom type that allows multiple -cloud flags but we validate to allow only one
	var clouds cloudFlags // OpenStack cloud name from clouds.yaml (required, only 1 entry allowed)

	rhcosExistsFlags.Var(&clouds, flagRhcosExistsCloud, usageRhcosExistsCloud)
	imageName := rhcosExistsFlags.String(flagRhcosExistsImageName, defaultRhcosExistsImageName, usageRhcosExistsImageName)
	debugFlag := rhcosExistsFlags.String(flagRhcosExistsShouldDebug, defaultRhcosExistsShouldDebug, usageRhcosExistsShouldDebug)

	// Parse the command-line arguments
	if err := rhcosExistsFlags.Parse(args); err != nil {
		return fmt.Errorf("%sfailed to parse flags: %w", errPrefixRhcosExists, err)
	}

	// Validate required fields - cloud name must be provided and exactly one
	if len(clouds) == 0 {
		return fmt.Errorf("cloud: field is required")
	}
	if len(clouds) > 1 {
		return fmt.Errorf("cloud: only one cloud is allowed")
	}
	// Check for empty string even if one cloud was provided
	if len(clouds) == 1 && clouds[0] == "" {
		return fmt.Errorf("cloud: field is required")
	}

	// Validate that image name is provided
	if *imageName == "" {
		return fmt.Errorf("imageName: field is required")
	}

	// Parse and validate the debug flag
	shouldDebug, err := parseBoolFlag(*debugFlag, flagRhcosExistsShouldDebug)
	if err != nil {
		return fmt.Errorf("%s%w", errPrefixRhcosExists, err)
	}

	// Initialize the logger with the appropriate debug level
	log = initLogger(shouldDebug)
	log.Infof("Starting rhcos-exists command")
	log.Infof("Debug mode: %v", shouldDebug)

	// Create a context with timeout to prevent hanging on OpenStack API calls
	// 2 minutes should be sufficient for image lookup operations
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Attempt to find the specified image in the OpenStack cloud
	_, err = findImage(ctx, clouds[0], *imageName)
	if err == nil {
		// Image found - report success
		fmt.Printf("Found image %s\n", *imageName)
	} else {
		// Image not found - provide helpful feedback by listing all available images
		fmt.Printf("Error: Could not find image named %s\n", *imageName)
		fmt.Println("These images exist:")

		// Retrieve and display all images to help the user identify the correct name
		allImages, err2 := getAllImages(ctx, clouds[0])
		if err2 == nil {
			for _, image := range allImages {
				// Display both ID and name for each image
				fmt.Printf("%s %s\n", image.ID, image.Name)
			}
		}

		fmt.Println("")

		// Return the original error indicating the image was not found
		return fmt.Errorf("failed to find image %q: %w", *imageName, err)
	}

	return nil
}
