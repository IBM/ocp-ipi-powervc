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
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

const (
	defaultTimeout        = 15 * time.Minute
)

var (
	ErrServerNotFound = errors.New("server not found")
	ErrInvalidConfig  = errors.New("invalid configuration")
)

// initLogger creates a configured logger based on debug flag
func initLogger(debug bool) *logrus.Logger {
	var out io.Writer
	if debug {
		out = os.Stderr
	} else {
		out = io.Discard
	}
	
	return &logrus.Logger{
		Out:       out,
		Formatter: new(logrus.TextFormatter),
		Level:     logrus.DebugLevel,
	}
}

// parseBoolFlag converts a string flag value to boolean
// Returns an error if the value is not "true" or "false"
func parseBoolFlag(value, flagName string) (bool, error) {
	switch strings.ToLower(value) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("%s must be 'true' or 'false', got: %s", flagName, value)
	}
}

func isValidResourceName(name string) bool {
	// Allow alphanumeric, hyphens, underscores
	matched, _ := regexp.MatchString(`^[a-zA-Z0-9_-]+$`, name)
	return matched
}

// validateServerIP validates that the provided IP address is in a valid format.
// Supports both IPv4 and IPv6 addresses.
func validateServerIP(ip string) error {
	// Try to parse as IP address
	if net.ParseIP(ip) == nil {
		// If not a valid IP, check if it's a valid hostname
		if _, err := net.LookupHost(ip); err != nil {
			return fmt.Errorf("invalid IP address or hostname: %s", ip)
		}
	}

	return nil
}

// validateFileExists checks if a file exists and is readable
func validateFileExists(filePath string) error {
	if filePath == "" {
		return fmt.Errorf("file path cannot be empty")
	}

	info, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("file does not exist: %s", filePath)
		}
		return fmt.Errorf("cannot access file %s: %w", filePath, err)
	}

	if info.IsDir() {
		return fmt.Errorf("path is a directory, not a file: %s", filePath)
	}

	// Check if file is readable
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("file is not readable: %s", filePath)
	}
	file.Close()

	return nil
}
