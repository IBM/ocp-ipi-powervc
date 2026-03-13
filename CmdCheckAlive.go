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
	"flag"
	"fmt"
	"os"
	"strings"
)

func checkAliveCommand(checkAliveFlags *flag.FlagSet, args []string) error {
	var (
		ptrServerIP    *string
		ptrShouldDebug *string
		err            error
	)

	fmt.Fprintf(os.Stderr, "Program version is %v, release = %v\n", version, release)

	// Define command-line flags
	ptrServerIP = checkAliveFlags.String("serverIP", "", "The IP address of the server to send the command to")
	ptrShouldDebug = checkAliveFlags.String("shouldDebug", "false", "Enable debug output (true/false)")

	// Parse flags
	if err = checkAliveFlags.Parse(args); err != nil {
		return fmt.Errorf("failed to parse flags: %w", err)
	}

	// Validate required flags
	if ptrServerIP == nil || strings.TrimSpace(*ptrServerIP) == "" {
		return fmt.Errorf("required flag --serverIP not specified")
	}

	// Validate server IP format
	if err = validateServerIP(*ptrServerIP); err != nil {
		return fmt.Errorf("invalid server IP: %w", err)
	}

	// Parse debug flag
	shouldDebug, err = parseBoolFlag(*ptrShouldDebug, "shouldDebug")
	if err != nil {
		return err
	}

	// Initialize logger (using utility function to avoid duplication)
	log = initLogger(shouldDebug)

	// Send check-alive command to server
	if err = sendCheckAlive(*ptrServerIP); err != nil {
		return fmt.Errorf("check-alive command failed: %w", err)
	}

	fmt.Printf("Server %s is alive and responding\n", *ptrServerIP)

	return nil
}
