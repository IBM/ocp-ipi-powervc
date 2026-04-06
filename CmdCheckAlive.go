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

// CmdCheckAlive.go implements the check-alive command for verifying server availability.
//
// The check-alive command sends a health check request to a specified server and waits
// for a response to confirm the server is alive and responding. This is useful for
// monitoring server health and verifying network connectivity.
//
// Command Usage:
//
//	ocp-ipi-powervc check-alive --serverIP <ip-address> [--shouldDebug <true|false>]
//
// Flags:
//
//	--serverIP (required): The IP address or hostname of the server to check
//	--shouldDebug (optional): Enable debug output (default: false)
//
// Examples:
//
//	# Check if server is alive
//	ocp-ipi-powervc check-alive --serverIP 192.168.1.100
//
//	# Check with debug output
//	ocp-ipi-powervc check-alive --serverIP 192.168.1.100 --shouldDebug true
//
//	# Check using hostname
//	ocp-ipi-powervc check-alive --serverIP server.example.com
//
// Exit Codes:
//
//	0: Server is alive and responding
//	1: Server is not responding or error occurred
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

const (
	// Flag names for check-alive command
	flagCheckAliveServerIP    = "serverIP"
	flagCheckAliveShouldDebug = "shouldDebug"

	// Default values for check-alive command flags
	defaultCheckAliveServerIP    = ""
	defaultCheckAliveShouldDebug = "false"

	// Usage messages for check-alive command flags
	usageCheckAliveServerIP    = "The IP address or hostname of the server to send the command to"
	usageCheckAliveShouldDebug = "Enable debug output (true/false)"

	// Error message prefix for check-alive command
	errPrefixCheckAlive = "[check-alive] "
)

// checkAliveCommand executes the check-alive command to verify server availability.
//
// This function performs a health check on a specified server by sending a check-alive
// command and waiting for a response. It validates all inputs, initializes logging based
// on the debug flag, and provides clear feedback on the server's status.
//
// Parameters:
//   - checkAliveFlags: FlagSet containing command-line flags for the check-alive command.
//     Must not be nil.
//   - args: Command-line arguments to parse. Can be empty but not nil.
//
// Returns:
//   - error: Any error encountered during execution, nil on success
//
// The function performs the following operations:
//  1. Validates input parameters (nil checks)
//  2. Displays program version information
//  3. Defines and parses command-line flags (serverIP, shouldDebug)
//  4. Validates required flags and server IP format
//  5. Initializes logger based on debug flag
//  6. Sends check-alive command to the specified server
//  7. Reports success or failure to the user
//
// Required Flags:
//   - serverIP: Must be a valid IP address (IPv4 or IPv6) or resolvable hostname
//
// Optional Flags:
//   - shouldDebug: Must be "true" or "false" (case-insensitive), defaults to "false"
//
// Example Usage:
//
//	flagSet := flag.NewFlagSet("check-alive", flag.ExitOnError)
//	err := checkAliveCommand(flagSet, []string{"--serverIP", "192.168.1.100"})
//	if err != nil {
//	    log.Fatalf("Check-alive failed: %v", err)
//	}
func checkAliveCommand(checkAliveFlags *flag.FlagSet, args []string) error {
	var (
		ptrServerIP    *string
		ptrShouldDebug *string
		err            error
	)

	// Validate input parameters
	if checkAliveFlags == nil {
		return fmt.Errorf("%sflag set cannot be nil", errPrefixCheckAlive)
	}

	fmt.Fprintf(os.Stderr, "Program version is %v, release = %v\n", version, release)

	// Define command-line flags
	ptrServerIP = checkAliveFlags.String(
		flagCheckAliveServerIP,
		defaultCheckAliveServerIP,
		usageCheckAliveServerIP,
	)
	ptrShouldDebug = checkAliveFlags.String(
		flagCheckAliveShouldDebug,
		defaultCheckAliveShouldDebug,
		usageCheckAliveShouldDebug,
	)

	// Parse flags
	if err = checkAliveFlags.Parse(args); err != nil {
		return fmt.Errorf("%sfailed to parse flags: %w", errPrefixCheckAlive, err)
	}

	// Validate required flags
	if ptrServerIP == nil || strings.TrimSpace(*ptrServerIP) == "" {
		return fmt.Errorf("%srequired flag --%s not specified", errPrefixCheckAlive, flagCheckAliveServerIP)
	}

	// Validate server IP format
	if err = validateServerIP(*ptrServerIP); err != nil {
		return fmt.Errorf("%sinvalid server IP: %w", errPrefixCheckAlive, err)
	}

	// Parse debug flag
	shouldDebug, err := parseBoolFlag(*ptrShouldDebug, flagCheckAliveShouldDebug)
	if err != nil {
		return fmt.Errorf("%s%w", errPrefixCheckAlive, err)
	}

	// Initialize logger (using utility function to avoid duplication)
	log = initLogger(shouldDebug)
	if shouldDebug {
		log.Debugf("Debug mode enabled")
	}

	// Log operation start
	log.Infof("Starting check-alive command")
	log.Infof("Program version: %v, release: %v", version, release)
	log.Infof("Validating required flags")
	log.Infof("Server IP: %s", *ptrServerIP)
	log.Infof("Debug mode: %v", shouldDebug)

	// Send check-alive command to server
	log.Infof("Sending check-alive command to server %s", *ptrServerIP)
	if err = sendCheckAlive(*ptrServerIP); err != nil {
		return fmt.Errorf("%scheck-alive command failed: %w", errPrefixCheckAlive, err)
	}

	// Log and report success
	log.Infof("Server %s is alive and responding", *ptrServerIP)
	fmt.Printf("[SUCCESS] Server %s is alive and responding (check-alive command completed successfully)\n", *ptrServerIP)

	return nil
}
