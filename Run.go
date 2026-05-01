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
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Note: This file uses the global 'log' variable declared in PowerVC-Tool.go
// and the 'defaultTimeout' constant defined elsewhere in the codebase
// and the separatorLine constant defined in Utils.go

// createCommand creates an exec.Cmd from a command line array with context.
// This helper function eliminates code duplication across run functions.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - acmdline: Array of command and arguments
//
// Returns:
//   - *exec.Cmd: Configured command ready to execute
//   - error: Error if command array is empty
func createCommand(ctx context.Context, acmdline []string) (*exec.Cmd, error) {
	if len(acmdline) == 0 {
		return nil, fmt.Errorf("command array cannot be empty")
	}

	if len(acmdline) == 1 {
		return exec.CommandContext(ctx, acmdline[0]), nil
	}

	return exec.CommandContext(ctx, acmdline[0], acmdline[1:]...), nil
}

// runCommand executes a shell command with KUBECONFIG environment variable set.
// It prints the command and its output to stdout.
//
// Parameters:
//   - kubeconfig: Path to the kubeconfig file
//   - cmdline: Space-separated command line string
//
// Returns:
//   - error: Any error encountered during command execution
//
// Example:
//   err := runCommand("/path/to/kubeconfig", "kubectl get nodes")
func runCommand(kubeconfig string, cmdline string) error {
	if kubeconfig == "" {
		return fmt.Errorf("kubeconfig path cannot be empty")
	}
	if cmdline == "" {
		return fmt.Errorf("command line cannot be empty")
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()

	// Split the space separated line into an array of strings
	acmdline := strings.Fields(cmdline)

	cmd, err := createCommand(ctx, acmdline)
	if err != nil {
		return fmt.Errorf("failed to create command: %w", err)
	}

	cmd.Env = append(
		os.Environ(),
		fmt.Sprintf("KUBECONFIG=%s", kubeconfig),
	)

	log.Debugf("runCommand: Executing command: %s", cmdline)
	log.Debugf("runCommand: KUBECONFIG=%s", kubeconfig)

	fmt.Println(separatorLine)
	fmt.Println(cmdline)

	out, err := cmd.CombinedOutput()
	fmt.Println(string(out))

	if err != nil {
		log.Debugf("runCommand: Command failed: %v", err)
		return fmt.Errorf("command execution failed: %w", err)
	}

	log.Debugf("runCommand: Command completed successfully")
	return nil
}

// runSplitCommand executes a command from an array of arguments and prints the output.
// This is a convenience wrapper around runSplitCommand2.
//
// Parameters:
//   - acmdline: Array containing command and arguments
//
// Returns:
//   - error: Any error encountered during command execution
//
// Example:
//   err := runSplitCommand([]string{"kubectl", "get", "nodes"})
func runSplitCommand(acmdline []string) error {
	if len(acmdline) == 0 {
		return fmt.Errorf("command array cannot be empty")
	}

	log.Debugf("runSplitCommand: Executing command: %v", acmdline)

	out, err := runSplitCommand2(acmdline)
	fmt.Println(string(out))

	if err != nil {
		log.Debugf("runSplitCommand: Command failed: %v", err)
		return fmt.Errorf("command execution failed: %w", err)
	}

	log.Debugf("runSplitCommand: Command completed successfully")
	return nil
}

// runSplitCommand2 executes a command from an array of arguments and returns the output.
// This function provides more control by returning the output bytes directly.
//
// Parameters:
//   - acmdline: Array containing command and arguments
//
// Returns:
//   - []byte: Combined stdout and stderr output
//   - error: Any error encountered during command execution
//
// Example:
//   out, err := runSplitCommand2([]string{"kubectl", "version", "--client"})
func runSplitCommand2(acmdline []string) ([]byte, error) {
	if len(acmdline) == 0 {
		return nil, fmt.Errorf("command array cannot be empty")
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()

	cmd, err := createCommand(ctx, acmdline)
	if err != nil {
		return nil, fmt.Errorf("failed to create command: %w", err)
	}

	log.Debugf("runSplitCommand2: Executing command: %v", acmdline)

	fmt.Println(separatorLine)
	fmt.Println(acmdline)

	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Debugf("runSplitCommand2: Command failed: %v", err)
		return out, fmt.Errorf("command execution failed: %w", err)
	}

	log.Debugf("runSplitCommand2: Command completed successfully, output size: %d bytes", len(out))
	return out, nil
}

// runSplitCommandNoErr executes a command and captures only stdout (not stderr).
// This function is useful when you want to suppress error output or handle it separately.
//
// Parameters:
//   - acmdline: Array containing command and arguments
//   - silent: If true, suppresses the command echo to stdout
//
// Returns:
//   - []byte: Stdout output from the command
//   - error: Any error encountered during command execution
//
// Example:
//   out, err := runSplitCommandNoErr([]string{"kubectl", "get", "pods"}, false)
//   if err != nil {
//       log.Printf("Command failed: %v", err)
//   }
func runSplitCommandNoErr(acmdline []string, silent bool) ([]byte, error) {
	if len(acmdline) == 0 {
		return nil, fmt.Errorf("command array cannot be empty")
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()

	cmd, err := createCommand(ctx, acmdline)
	if err != nil {
		return nil, fmt.Errorf("failed to create command: %w", err)
	}

	var stdout bytes.Buffer
	cmd.Stdout = &stdout // Capture stdout into a buffer (stderr is not captured)

	if !silent {
		log.Debugf("runSplitCommandNoErr: Executing command: %v", acmdline)
		fmt.Println(separatorLine)
		fmt.Println(acmdline)
	} else {
		log.Debugf("runSplitCommandNoErr: Executing command (silent): %v", acmdline)
	}

	err = cmd.Run()
	out := stdout.Bytes()

	if err != nil {
		log.Debugf("runSplitCommandNoErr: Command failed: %v, output size: %d bytes", err, len(out))
		return out, fmt.Errorf("command execution failed: %w", err)
	}

	log.Debugf("runSplitCommandNoErr: Command completed successfully, output size: %d bytes", len(out))
	return out, nil
}

// runTwoCommands executes two commands in a pipeline (cmd1 | cmd2) with KUBECONFIG set.
// The stdout of the first command is piped to the stdin of the second command.
//
// Parameters:
//   - kubeconfig: Path to the kubeconfig file (applied to first command)
//   - cmdline1: Space-separated command line string for the first command
//   - cmdline2: Space-separated command line string for the second command
//
// Returns:
//   - error: Any error encountered during command execution
//
// Example:
//   err := runTwoCommands("/path/to/kubeconfig", "kubectl get pods -o json", "jq .items[].metadata.name")
func runTwoCommands(kubeconfig string, cmdline1 string, cmdline2 string) error {
	if kubeconfig == "" {
		return fmt.Errorf("kubeconfig path cannot be empty")
	}
	if cmdline1 == "" {
		return fmt.Errorf("first command line cannot be empty")
	}
	if cmdline2 == "" {
		return fmt.Errorf("second command line cannot be empty")
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()

	log.Debugf("runTwoCommands: cmdline1 = %s", cmdline1)
	log.Debugf("runTwoCommands: cmdline2 = %s", cmdline2)
	log.Debugf("runTwoCommands: KUBECONFIG=%s", kubeconfig)

	// Split the space separated lines into arrays of strings
	acmdline1 := strings.Fields(cmdline1)
	acmdline2 := strings.Fields(cmdline2)

	// Create first command
	cmd1, err := createCommand(ctx, acmdline1)
	if err != nil {
		return fmt.Errorf("failed to create first command: %w", err)
	}

	cmd1.Env = append(
		os.Environ(),
		fmt.Sprintf("KUBECONFIG=%s", kubeconfig),
	)

	// Create second command
	cmd2, err := createCommand(ctx, acmdline2)
	if err != nil {
		return fmt.Errorf("failed to create second command: %w", err)
	}

	// Create pipe to connect commands
	readPipe, writePipe, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("failed to create pipe: %w", err)
	}
	defer readPipe.Close()

	// Connect first command's stdout to pipe
	cmd1.Stdin  = os.Stdin
	cmd1.Stdout = writePipe
	cmd1.Stderr = os.Stderr

	// Start first command
	log.Debugf("runTwoCommands: Starting first command: %v", acmdline1)
	if err := cmd1.Start(); err != nil {
		writePipe.Close()
		return fmt.Errorf("failed to start first command: %w", err)
	}

	// Close write end of pipe after starting cmd1
	writePipe.Close()

	// Connect second command's stdin to pipe and capture output
	var buffer bytes.Buffer
	cmd2.Stdin = readPipe
	cmd2.Stdout = &buffer
	cmd2.Stderr = &buffer

	// Run second command
	log.Debugf("runTwoCommands: Running second command: %v", acmdline2)
	if err := cmd2.Run(); err != nil {
		cmd1.Wait() // Wait for first command to finish
		return fmt.Errorf("failed to run second command: %w", err)
	}

	// Wait for first command to complete
	if err := cmd1.Wait(); err != nil {
		log.Debugf("runTwoCommands: First command failed: %v", err)
		return fmt.Errorf("first command failed: %w", err)
	}

	out := buffer.Bytes()

	fmt.Println(separatorLine)
	fmt.Printf("%s | %s\n", cmdline1, cmdline2)
	fmt.Println(string(out))

	log.Debugf("runTwoCommands: Pipeline completed successfully, output size: %d bytes", len(out))
	return nil
}
