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

// (cd snippet1/; /bin/rm go.*; go mod init example/user/snippet1; go mod tidy; /bin/rm /tmp/test_file; for I in {1..10}; do echo "line $I" >> /tmp/test_file; echo "skip $I" >> /tmp/test_file; done; go run snippet1.go)
// (cd snippet1/; /bin/rm go.*; go mod init example/user/snippet1; go mod tidy; /bin/rm /tmp/test_file; for I in {1..1000}; do echo "Running $I" >> /tmp/test_file; echo "Error $I" >> /tmp/test_file; done; go run snippet1.go)
// (cd snippet1/; /bin/rm go.*)

package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

const (
	// defaultTimeout is the default timeout for operations
	defaultTimeout = 15 * time.Minute

	// separatorLine is the visual separator used in command output
	separatorLine = "8<--------8<--------8<--------8<--------8<--------8<--------8<--------8<--------"
)

func createCommand(ctx context.Context, acmdline []string) (*exec.Cmd, error) {
	if len(acmdline) == 0 {
		return nil, fmt.Errorf("command array cannot be empty")
	}

	if len(acmdline) == 1 {
		return exec.CommandContext(ctx, acmdline[0]), nil
	}

	return exec.CommandContext(ctx, acmdline[0], acmdline[1:]...), nil
}

func main() {
	log := &logrus.Logger{
		Out:   os.Stderr,
		Formatter: &logrus.TextFormatter{
			FullTimestamp:   true,
			TimestampFormat: "2006-01-02 15:04:05",
		},
		Level: logrus.DebugLevel,
	}

	ptrKubeConfig := flag.String("kubeconfig", "", "The KUBECONFIG file")
	flag.Parse()

//	cmdline1 := "cat /tmp/test_file"
	cmdline1 := "oc --request-timeout=5s get pods -A -o=wide"
//	cmdline2 := "grep line"
	cmdline2 := "sed -e /\\(Running\\|Completed\\)/d"

	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()

	log.Debugf("runTwoCommands: cmdline1 = %s", cmdline1)
	log.Debugf("runTwoCommands: cmdline2 = %s", cmdline2)

	// Split the space separated lines into arrays of strings
	acmdline1 := strings.Fields(cmdline1)
	acmdline2 := strings.Fields(cmdline2)

	// Create first command
	cmd1, err := createCommand(ctx, acmdline1)
	if err != nil {
		log.Errorf("failed to create first command: %v", err)
	}

	cmd1.Env = append(
		os.Environ(),
		fmt.Sprintf("KUBECONFIG=%s", *ptrKubeConfig),
	)

	// Create second command
	cmd2, err := createCommand(ctx, acmdline2)
	if err != nil {
		log.Errorf("failed to create second command: %v", err)
	}

	// Create pipe to connect commands
	readPipe, writePipe, err := os.Pipe()
	if err != nil {
		log.Errorf("failed to create pipe: %v", err)
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
		log.Errorf("failed to start first command: %v", err)
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
		log.Errorf("failed to run second command: %v", err)
	}

	// Wait for first command to complete
	if err := cmd1.Wait(); err != nil {
		log.Debugf("runTwoCommands: First command failed: %v", err)
		log.Errorf("first command failed: %v", err)
	}

	out := buffer.Bytes()

	fmt.Println(separatorLine)
	fmt.Printf("%s | %s\n", cmdline1, cmdline2)
	fmt.Println(string(out))

	log.Debugf("runTwoCommands: Pipeline completed successfully, output size: %d bytes", len(out))
}
