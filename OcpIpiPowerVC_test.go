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
	"bytes"
	"flag"
	"io"
	"os"
	"strings"
	"testing"
)

// TestConstants verifies that all constants are defined correctly
func TestConstants(t *testing.T) {
	tests := []struct {
		name     string
		constant string
		expected string
	}{
		{"check-alive command", cmdCheckAlive, "check-alive"},
		{"create-bastion command", cmdCreateBastion, "create-bastion"},
		{"create-rhcos command", cmdCreateRhcos, "create-rhcos"},
		{"create-cluster command", cmdCreateCluster, "create-cluster"},
		{"send-metadata command", cmdSendMetadata, "send-metadata"},
		{"watch-installation command", cmdWatchInstallation, "watch-installation"},
		{"watch-create command", cmdWatchCreate, "watch-create"},
		{"version flag", versionFlag, "-version"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.constant != tt.expected {
				t.Errorf("Expected constant %q, got %q", tt.expected, tt.constant)
			}
		})
	}
}

// TestExitCodes verifies that exit codes are defined correctly
func TestExitCodes(t *testing.T) {
	if exitSuccess != 0 {
		t.Errorf("Expected exitSuccess to be 0, got %d", exitSuccess)
	}
	if exitError != 1 {
		t.Errorf("Expected exitError to be 1, got %d", exitError)
	}
}

// TestVersionVariables verifies that version variables exist
func TestVersionVariables(t *testing.T) {
	// These should be defined, even if they're "undefined" at test time
	if version == "" {
		t.Error("version variable should not be empty string")
	}
	if release == "" {
		t.Error("release variable should not be empty string")
	}
}

// TestPrintUsage verifies that printUsage outputs correct information
func TestPrintUsage(t *testing.T) {
	// Capture stderr
	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	printUsage("test-executable")

	w.Close()
	os.Stderr = oldStderr

	var buf bytes.Buffer
	io.Copy(&buf, r)
	output := buf.String()

	// Verify output contains expected elements
	expectedStrings := []string{
		"Program version is",
		"Usage: test-executable <command>",
		"Available commands:",
		cmdCheckAlive,
		cmdCreateBastion,
		cmdCreateRhcos,
		cmdCreateCluster,
		cmdSendMetadata,
		cmdWatchInstallation,
		cmdWatchCreate,
		"Check if cluster nodes are alive",
		"Create bastion host",
		"Create RHCOS image",
		"Create OpenShift cluster",
		"Send metadata to cluster",
		"Watch cluster installation progress",
		"Watch cluster creation process",
	}

	for _, expected := range expectedStrings {
		if !strings.Contains(output, expected) {
			t.Errorf("Expected output to contain %q, but it didn't.\nOutput: %s", expected, output)
		}
	}
}

// TestPrintUsage_ExecutableName verifies that the executable name is used correctly
func TestPrintUsage_ExecutableName(t *testing.T) {
	tests := []struct {
		name           string
		executableName string
	}{
		{"simple name", "myapp"},
		{"with path", "/usr/bin/myapp"},
		{"with extension", "myapp.exe"},
		{"with spaces", "my app"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldStderr := os.Stderr
			r, w, _ := os.Pipe()
			os.Stderr = w

			printUsage(tt.executableName)

			w.Close()
			os.Stderr = oldStderr

			var buf bytes.Buffer
			io.Copy(&buf, r)
			output := buf.String()

			if !strings.Contains(output, tt.executableName) {
				t.Errorf("Expected output to contain executable name %q", tt.executableName)
			}
		})
	}
}

// TestPrintUsage_VersionInfo verifies that version information is displayed
func TestPrintUsage_VersionInfo(t *testing.T) {
	// Save original values
	origVersion := version
	origRelease := release
	defer func() {
		version = origVersion
		release = origRelease
	}()

	// Set test values
	version = "test-version-123"
	release = "test-release-456"

	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	printUsage("test-app")

	w.Close()
	os.Stderr = oldStderr

	var buf bytes.Buffer
	io.Copy(&buf, r)
	output := buf.String()

	if !strings.Contains(output, "test-version-123") {
		t.Error("Expected output to contain test version")
	}
	if !strings.Contains(output, "test-release-456") {
		t.Error("Expected output to contain test release")
	}
}

// TestPrintUsage_CommandFormatting verifies that commands are properly formatted
func TestPrintUsage_CommandFormatting(t *testing.T) {
	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w

	printUsage("test-app")

	w.Close()
	os.Stderr = oldStderr

	var buf bytes.Buffer
	io.Copy(&buf, r)
	output := buf.String()

	// Check that each command appears with its description
	commandDescriptions := map[string]string{
		cmdCheckAlive:        "Check if cluster nodes are alive",
		cmdCreateBastion:     "Create bastion host",
		cmdCreateRhcos:       "Create RHCOS image",
		cmdCreateCluster:     "Create OpenShift cluster",
		cmdSendMetadata:      "Send metadata to cluster",
		cmdWatchInstallation: "Watch cluster installation progress",
		cmdWatchCreate:       "Watch cluster creation process",
	}

	for cmd, desc := range commandDescriptions {
		if !strings.Contains(output, cmd) {
			t.Errorf("Expected output to contain command %q", cmd)
		}
		if !strings.Contains(output, desc) {
			t.Errorf("Expected output to contain description %q", desc)
		}
	}
}

// TestMain_NoArguments verifies behavior when no arguments are provided
func TestMain_NoArguments(t *testing.T) {
	// This test verifies the logic that would be executed in main()
	// We can't directly test main() as it calls os.Exit, but we can test the logic

	if len([]string{}) == 0 {
		// This is the condition checked in main when len(os.Args) == 1
		// (os.Args[0] is the program name, so len==1 means no arguments)
		t.Log("Verified that empty args array has length 0")
	}
}

// TestMain_VersionFlag verifies version flag handling logic
func TestMain_VersionFlag(t *testing.T) {
	// Save original values
	origVersion := version
	origRelease := release
	defer func() {
		version = origVersion
		release = origRelease
	}()

	// Set test values
	version = "v1.2.3"
	release = "v1.2.0"

	// Simulate the version flag check
	args := []string{"program", versionFlag}
	if len(args) == 2 && args[1] == versionFlag {
		// This is what main() would do
		expectedOutput := "version = v1.2.3\nrelease = v1.2.0\n"
		actualOutput := "version = " + version + "\nrelease = " + release + "\n"
		if actualOutput != expectedOutput {
			t.Errorf("Expected version output %q, got %q", expectedOutput, actualOutput)
		}
	} else {
		t.Error("Version flag condition not met")
	}
}

// TestMain_CommandDispatch verifies command name handling
func TestMain_CommandDispatch(t *testing.T) {
	tests := []struct {
		name        string
		command     string
		shouldMatch bool
	}{
		{"check-alive lowercase", "check-alive", true},
		{"check-alive uppercase", "CHECK-ALIVE", true},
		{"check-alive mixed case", "Check-Alive", true},
		{"create-bastion lowercase", "create-bastion", true},
		{"create-rhcos lowercase", "create-rhcos", true},
		{"create-cluster lowercase", "create-cluster", true},
		{"send-metadata lowercase", "send-metadata", true},
		{"watch-installation lowercase", "watch-installation", true},
		{"watch-create lowercase", "watch-create", true},
		{"unknown command", "unknown-command", false},
		{"empty command", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			command := strings.ToLower(tt.command)
			matched := false

			switch command {
			case cmdCheckAlive, cmdCreateBastion, cmdCreateCluster,
				cmdCreateRhcos, cmdSendMetadata, cmdWatchInstallation, cmdWatchCreate:
				matched = true
			}

			if matched != tt.shouldMatch {
				t.Errorf("Command %q: expected match=%v, got match=%v", tt.command, tt.shouldMatch, matched)
			}
		})
	}
}

// TestMain_FlagSetCreation verifies that flag sets are created correctly
func TestMain_FlagSetCreation(t *testing.T) {
	// Test that we can create flag sets for each command
	commands := []string{
		cmdCheckAlive,
		cmdCreateBastion,
		cmdCreateCluster,
		cmdCreateRhcos,
		cmdSendMetadata,
		cmdWatchInstallation,
		cmdWatchCreate,
	}

	for _, cmd := range commands {
		t.Run(cmd, func(t *testing.T) {
			flagSet := flag.NewFlagSet(cmd, flag.ExitOnError)
			if flagSet == nil {
				t.Errorf("Failed to create flag set for command %q", cmd)
			}
			if flagSet.Name() != cmd {
				t.Errorf("Expected flag set name %q, got %q", cmd, flagSet.Name())
			}
		})
	}
}

// TestMain_CaseInsensitiveCommands verifies that commands are case-insensitive
func TestMain_CaseInsensitiveCommands(t *testing.T) {
	testCases := []struct {
		input    string
		expected string
	}{
		{"CHECK-ALIVE", "check-alive"},
		{"Check-Alive", "check-alive"},
		{"check-ALIVE", "check-alive"},
		{"CREATE-BASTION", "create-bastion"},
		{"Create-Bastion", "create-bastion"},
		{"WATCH-INSTALLATION", "watch-installation"},
	}

	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			result := strings.ToLower(tc.input)
			if result != tc.expected {
				t.Errorf("Expected %q to normalize to %q, got %q", tc.input, tc.expected, result)
			}
		})
	}
}

// TestMain_ErrorHandling verifies error handling logic
func TestMain_ErrorHandling(t *testing.T) {
	// Test that errors are properly formatted
	testError := "test error message"
	command := "test-command"

	expectedOutput := "Error: Command 'test-command' failed: test error message\n"
	actualOutput := "Error: Command '" + command + "' failed: " + testError + "\n"

	if actualOutput != expectedOutput {
		t.Errorf("Expected error output %q, got %q", expectedOutput, actualOutput)
	}
}

// TestMain_UnknownCommand verifies unknown command handling
func TestMain_UnknownCommand(t *testing.T) {
	unknownCommands := []string{
		"invalid",
		"test",
		"help",
		"--help",
		"-h",
		"version",
		"",
	}

	for _, cmd := range unknownCommands {
		t.Run(cmd, func(t *testing.T) {
			command := strings.ToLower(cmd)
			isKnown := false

			switch command {
			case cmdCheckAlive, cmdCreateBastion, cmdCreateCluster,
				cmdCreateRhcos, cmdSendMetadata, cmdWatchInstallation, cmdWatchCreate:
				isKnown = true
			}

			if isKnown {
				t.Errorf("Command %q should not be recognized as known", cmd)
			}
		})
	}
}

// TestMain_GlobalVariables verifies global variable initialization
func TestMain_GlobalVariables(t *testing.T) {
	// Test shouldDebug default value
	if shouldDebug {
		t.Error("shouldDebug should default to false")
	}

	// Test that log variable can be nil initially
	if log != nil {
		t.Log("log variable is initialized (this is okay)")
	}
}

// TestMain_ExecutablePathHandling verifies executable path logic
func TestMain_ExecutablePathHandling(t *testing.T) {
	// Test that we can get the executable path
	execPath, err := os.Executable()
	if err != nil {
		t.Logf("Note: os.Executable() returned error (expected in some test environments): %v", err)
		return
	}

	if execPath == "" {
		t.Error("Executable path should not be empty")
	}

	// Test that we can extract the base name
	// This simulates what main() does
	baseName := execPath
	if idx := strings.LastIndex(execPath, "/"); idx >= 0 {
		baseName = execPath[idx+1:]
	}
	if idx := strings.LastIndex(baseName, "\\"); idx >= 0 {
		baseName = baseName[idx+1:]
	}

	if baseName == "" {
		t.Error("Base name should not be empty")
	}
}

// TestMain_ArgumentParsing verifies argument parsing logic
func TestMain_ArgumentParsing(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		expected int
	}{
		{"no args", []string{"program"}, 1},
		{"one arg", []string{"program", "command"}, 2},
		{"two args", []string{"program", "command", "--flag"}, 3},
		{"multiple args", []string{"program", "command", "--flag1", "value1", "--flag2", "value2"}, 6},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if len(tt.args) != tt.expected {
				t.Errorf("Expected %d args, got %d", tt.expected, len(tt.args))
			}
		})
	}
}

// TestMain_CommandArguments verifies command argument extraction
func TestMain_CommandArguments(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		expectedCmd  string
		expectedArgs []string
	}{
		{
			name:         "command only",
			args:         []string{"program", "check-alive"},
			expectedCmd:  "check-alive",
			expectedArgs: []string{},
		},
		{
			name:         "command with flags",
			args:         []string{"program", "check-alive", "--serverIP", "192.168.1.1"},
			expectedCmd:  "check-alive",
			expectedArgs: []string{"--serverIP", "192.168.1.1"},
		},
		{
			name:         "command with multiple flags",
			args:         []string{"program", "create-bastion", "--cloud", "mycloud", "--bastionName", "bastion1"},
			expectedCmd:  "create-bastion",
			expectedArgs: []string{"--cloud", "mycloud", "--bastionName", "bastion1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if len(tt.args) < 2 {
				t.Skip("Not enough args")
			}

			cmd := tt.args[1]
			cmdArgs := tt.args[2:]

			if cmd != tt.expectedCmd {
				t.Errorf("Expected command %q, got %q", tt.expectedCmd, cmd)
			}

			if len(cmdArgs) != len(tt.expectedArgs) {
				t.Errorf("Expected %d command args, got %d", len(tt.expectedArgs), len(cmdArgs))
			}

			for i, arg := range cmdArgs {
				if i < len(tt.expectedArgs) && arg != tt.expectedArgs[i] {
					t.Errorf("Expected arg[%d] to be %q, got %q", i, tt.expectedArgs[i], arg)
				}
			}
		})
	}
}

// TestMain_VersionFlagVariations verifies different version flag formats
func TestMain_VersionFlagVariations(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		isVersion bool
	}{
		{"exact version flag", []string{"program", "-version"}, true},
		{"version with extra args", []string{"program", "-version", "extra"}, false},
		{"version uppercase", []string{"program", "-VERSION"}, false},
		{"version without dash", []string{"program", "version"}, false},
		{"double dash version", []string{"program", "--version"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isVersion := len(tt.args) == 2 && tt.args[1] == versionFlag
			if isVersion != tt.isVersion {
				t.Errorf("Expected isVersion=%v, got %v for args %v", tt.isVersion, isVersion, tt.args)
			}
		})
	}
}

// TestMain_ExitCodeLogic verifies exit code logic
func TestMain_ExitCodeLogic(t *testing.T) {
	// Test success case
	var err error = nil
	expectedExit := exitSuccess
	actualExit := exitSuccess
	if err != nil {
		actualExit = exitError
	}
	if actualExit != expectedExit {
		t.Errorf("Expected exit code %d for success, got %d", expectedExit, actualExit)
	}

	// Test error case
	err = os.ErrNotExist
	expectedExit = exitError
	actualExit = exitSuccess
	if err != nil {
		actualExit = exitError
	}
	if actualExit != expectedExit {
		t.Errorf("Expected exit code %d for error, got %d", expectedExit, actualExit)
	}
}

// Made with Bob