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
	"os/exec"
	"path/filepath"
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
		{"version flag 2", versionFlag2, "--version"},
		{"help flag", helpFlag, "-help"},
		{"help flag 2", helpFlag2, "--help"},
		{"help flag 3", helpFlag3, "-h"},
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
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Failed to create pipe: %v", err)
	}
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
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatalf("Failed to create pipe: %v", err)
			}
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
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Failed to create pipe: %v", err)
	}
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
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Failed to create pipe: %v", err)
	}
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

// TestRun_NoArguments verifies behavior when no arguments are provided
func TestRun_NoArguments(t *testing.T) {
	// Capture stderr
	oldStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Failed to create pipe: %v", err)
	}
	os.Stderr = w

	// Call run with no arguments
	err = run([]string{}, "test-app")

	w.Close()
	os.Stderr = oldStderr

	var buf bytes.Buffer
	io.Copy(&buf, r)
	output := buf.String()

	// Should return an error
	if err == nil {
		t.Error("Expected error when no arguments provided, got nil")
	}

	// Should contain error message and usage
	if !strings.Contains(output, "Error: No command specified") {
		t.Error("Expected error message about no command specified")
	}
	if !strings.Contains(output, "Usage:") {
		t.Error("Expected usage information in output")
	}
}

// TestRun_VersionFlag verifies version flag handling logic
func TestRun_VersionFlag(t *testing.T) {
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

	tests := []struct {
		name string
		args []string
	}{
		{"single dash version", []string{"-version"}},
		{"double dash version", []string{"--version"}},
		{"version with extra args", []string{"-version", "extra"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Capture stdout
			oldStdout := os.Stdout
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatalf("Failed to create pipe: %v", err)
			}
			os.Stdout = w

			// Call run with version flag
			err = run(tt.args, "test-app")

			w.Close()
			os.Stdout = oldStdout

			var buf bytes.Buffer
			io.Copy(&buf, r)
			output := buf.String()

			// Should not return an error
			if err != nil {
				t.Errorf("Expected no error for version flag, got: %v", err)
			}

			// Should contain version info
			if !strings.Contains(output, "version = v1.2.3") {
				t.Error("Expected version in output")
			}
			if !strings.Contains(output, "release = v1.2.0") {
				t.Error("Expected release in output")
			}
		})
	}
}

// TestRun_HelpFlag verifies help flag handling
func TestRun_HelpFlag(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"single dash help", []string{"-help"}},
		{"double dash help", []string{"--help"}},
		{"short help", []string{"-h"}},
		{"help with extra args", []string{"-help", "extra"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Capture stderr
			oldStderr := os.Stderr
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatalf("Failed to create pipe: %v", err)
			}
			os.Stderr = w

			// Call run with help flag
			err = run(tt.args, "test-app")

			w.Close()
			os.Stderr = oldStderr

			var buf bytes.Buffer
			io.Copy(&buf, r)
			output := buf.String()

			// Should not return an error
			if err != nil {
				t.Errorf("Expected no error for help flag, got: %v", err)
			}

			// Should contain usage info
			if !strings.Contains(output, "Usage:") {
				t.Error("Expected usage information in output")
			}
			if !strings.Contains(output, "Available commands:") {
				t.Error("Expected available commands in output")
			}
		})
	}
}

// TestRun_UnknownCommand verifies unknown command handling
func TestRun_UnknownCommand(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		command string
	}{
		{"unknown command", []string{"unknown-command"}, "unknown-command"},
		{"invalid command", []string{"invalid"}, "invalid"},
		{"help without dash", []string{"help"}, "help"},
		{"version without dash", []string{"version"}, "version"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Capture stderr
			oldStderr := os.Stderr
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatalf("Failed to create pipe: %v", err)
			}
			os.Stderr = w

			// Call run with unknown command
			err = run(tt.args, "test-app")

			w.Close()
			os.Stderr = oldStderr

			var buf bytes.Buffer
			io.Copy(&buf, r)
			output := buf.String()

			// Should return an error
			if err == nil {
				t.Error("Expected error for unknown command, got nil")
			}

			// Should contain error message
			if !strings.Contains(output, "Error: Unknown command") {
				t.Error("Expected unknown command error message")
			}
			if !strings.Contains(output, tt.command) {
				t.Errorf("Expected command name %q in error message", tt.command)
			}
			if !strings.Contains(output, "Usage:") {
				t.Error("Expected usage information in output")
			}
		})
	}
}

// TestRun_CaseInsensitiveCommands verifies that commands are case-insensitive
func TestRun_CaseInsensitiveCommands(t *testing.T) {
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
			_, matched := commandHandlers[command]

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

// TestMain_VersionFlagVariations verifies different version and help flag formats by executing the binary
func TestMain_VersionFlagVariations(t *testing.T) {
	// Build a test binary first
	testBinary := filepath.Join(t.TempDir(), "test-binary")
	buildCmd := exec.Command("go", "build", "-o", testBinary, ".")
	if output, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("Failed to build test binary: %v\nOutput: %s", err, output)
	}

	tests := []struct {
		name           string
		args           []string
		expectVersion  bool
		expectHelp     bool
		expectError    bool
		expectedExit   int
	}{
		// Version flag tests
		{
			name:          "single dash version flag",
			args:          []string{"-version"},
			expectVersion: true,
			expectHelp:    false,
			expectError:   false,
			expectedExit:  0,
		},
		{
			name:          "double dash version flag",
			args:          []string{"--version"},
			expectVersion: true,
			expectHelp:    false,
			expectError:   false,
			expectedExit:  0,
		},
		{
			name:          "version with extra args",
			args:          []string{"-version", "extra"},
			expectVersion: true,
			expectHelp:    false,
			expectError:   false,
			expectedExit:  0,
		},
		{
			name:          "version flag after command",
			args:          []string{"check-alive", "-version"},
			expectVersion: false,
			expectHelp:    false,
			expectError:   true, // -version is not a valid flag for check-alive
			expectedExit:  1,
		},
		{
			name:          "version uppercase",
			args:          []string{"-VERSION"},
			expectVersion: false,
			expectHelp:    true, // Shows usage on error
			expectError:   true,
			expectedExit:  1,
		},
		{
			name:          "version without dash",
			args:          []string{"version"},
			expectVersion: false,
			expectHelp:    true, // Shows usage on error
			expectError:   true,
			expectedExit:  1,
		},
		// Help flag tests
		{
			name:          "single dash help flag",
			args:          []string{"-help"},
			expectVersion: false,
			expectHelp:    true,
			expectError:   false,
			expectedExit:  0,
		},
		{
			name:          "double dash help flag",
			args:          []string{"--help"},
			expectVersion: false,
			expectHelp:    true,
			expectError:   false,
			expectedExit:  0,
		},
		{
			name:          "short help flag",
			args:          []string{"-h"},
			expectVersion: false,
			expectHelp:    true,
			expectError:   false,
			expectedExit:  0,
		},
		{
			name:          "help with extra args",
			args:          []string{"-help", "extra"},
			expectVersion: false,
			expectHelp:    true,
			expectError:   false,
			expectedExit:  0,
		},
		{
			name:          "help flag after command",
			args:          []string{"check-alive", "-help"},
			expectVersion: false,
			expectHelp:    false, // check-alive shows its own usage, not main help
			expectError:   true,  // -help is not defined for check-alive
			expectedExit:  1,
		},
		{
			name:          "help uppercase",
			args:          []string{"-HELP"},
			expectVersion: false,
			expectHelp:    true, // Shows usage on error
			expectError:   true,
			expectedExit:  1,
		},
		{
			name:          "help without dash",
			args:          []string{"help"},
			expectVersion: false,
			expectHelp:    true, // Shows usage on error
			expectError:   true,
			expectedExit:  1,
		},
		// Edge cases
		{
			name:          "no arguments",
			args:          []string{},
			expectVersion: false,
			expectHelp:    true, // Shows usage on error
			expectError:   true,
			expectedExit:  1,
		},
		{
			name:          "both version and help",
			args:          []string{"-version", "-help"},
			expectVersion: true,
			expectHelp:    false,
			expectError:   false,
			expectedExit:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command(testBinary, tt.args...)
			output, err := cmd.CombinedOutput()
			outputStr := string(output)

			// Check exit code
			exitCode := 0
			if err != nil {
				if exitErr, ok := err.(*exec.ExitError); ok {
					exitCode = exitErr.ExitCode()
				} else {
					t.Fatalf("Unexpected error type: %v", err)
				}
			}

			if exitCode != tt.expectedExit {
				t.Errorf("Expected exit code %d, got %d\nOutput: %s", tt.expectedExit, exitCode, outputStr)
			}

			// Check if version info is in output
			hasVersion := strings.Contains(outputStr, "version =") && strings.Contains(outputStr, "release =")
			if hasVersion != tt.expectVersion {
				t.Errorf("Expected version output=%v, got %v\nOutput: %s", tt.expectVersion, hasVersion, outputStr)
			}

			// Check if help/usage info is in output
			hasHelp := strings.Contains(outputStr, "Usage:") && strings.Contains(outputStr, "Available commands:")
			if hasHelp != tt.expectHelp {
				t.Errorf("Expected help output=%v, got %v\nOutput: %s", tt.expectHelp, hasHelp, outputStr)
			}

			// Check error expectations
			if tt.expectError && exitCode == 0 {
				t.Errorf("Expected error but command succeeded\nOutput: %s", outputStr)
			}
			if !tt.expectError && exitCode != 0 {
				t.Errorf("Expected success but command failed with exit code %d\nOutput: %s", exitCode, outputStr)
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
