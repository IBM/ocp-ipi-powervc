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

func runCommand(kubeconfig string, cmdline string) error {
	var (
		acmdline []string
		ctx      context.Context
		cancel   context.CancelFunc
		cmd      *exec.Cmd
		out      []byte
		err      error
	)

	ctx, cancel = context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()

	// Split the space separated line into an array of strings
	acmdline = strings.Fields(cmdline)

	if len(acmdline) == 0 {
		return fmt.Errorf("runCommand has empty command")
	} else if len(acmdline) == 1 {
		cmd = exec.CommandContext(ctx, acmdline[0])
	} else {
		cmd = exec.CommandContext(ctx, acmdline[0], acmdline[1:]...)
	}

	cmd.Env = append(
		os.Environ(),
		fmt.Sprintf("KUBECONFIG=%s", kubeconfig),
	)

	fmt.Println("8<--------8<--------8<--------8<--------8<--------8<--------8<--------8<--------")
	fmt.Println(cmdline)
	out, err = cmd.CombinedOutput()
	fmt.Println(string(out))

	return err
}

func runSplitCommand(acmdline []string) (err error) {
	var (
		out []byte
	)

	out, err = runSplitCommand2(acmdline)
	fmt.Println(string(out))

	return
}

func runSplitCommand2(acmdline []string) (out []byte, err error) {
	var (
		ctx    context.Context
		cancel context.CancelFunc
		cmd    *exec.Cmd
	)

	ctx, cancel = context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()

	if len(acmdline) == 0 {
		err = fmt.Errorf("runSplitCommand has empty command")
		return
	} else if len(acmdline) == 1 {
		cmd = exec.CommandContext(ctx, acmdline[0])
	} else {
		cmd = exec.CommandContext(ctx, acmdline[0], acmdline[1:]...)
	}

	fmt.Println("8<--------8<--------8<--------8<--------8<--------8<--------8<--------8<--------")
	fmt.Println(acmdline)
	out, err = cmd.CombinedOutput()
	return
}

func runSplitCommandNoErr(acmdline []string, silent bool) (out []byte, err error) {
	var (
		ctx    context.Context
		cancel context.CancelFunc
		cmd    *exec.Cmd
		stdout bytes.Buffer
	)

	ctx, cancel = context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()

	if len(acmdline) == 0 {
		err = fmt.Errorf("runSplitCommand has empty command")
		return
	} else if len(acmdline) == 1 {
		cmd = exec.CommandContext(ctx, acmdline[0])
	} else {
		cmd = exec.CommandContext(ctx, acmdline[0], acmdline[1:]...)
	}
	cmd.Stdout = &stdout // Capture stderr into a buffer

	if !silent {
		fmt.Println("8<--------8<--------8<--------8<--------8<--------8<--------8<--------8<--------")
		fmt.Println(acmdline)
	}
	err = cmd.Run()
	out = stdout.Bytes()
	return
}

func runTwoCommands(kubeconfig string, cmdline1 string, cmdline2 string) error {
	var (
		acmdline1 []string
		acmdline2 []string
		ctx       context.Context
		cancel    context.CancelFunc
		cmd1      *exec.Cmd
		cmd2      *exec.Cmd
		readPipe  *os.File
		writePipe *os.File
		buffer    bytes.Buffer
		out       []byte
		err       error
	)

	ctx, cancel = context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()

	log.Debugf("cmdline1 = %s", cmdline1)
	log.Debugf("cmdline2 = %s", cmdline2)

	// Split the space separated line into an array of strings
	acmdline1 = strings.Fields(cmdline1)
	acmdline2 = strings.Fields(cmdline2)

	if len(acmdline1) == 0 {
		return fmt.Errorf("runTwoCommands has empty command")
	} else if len(acmdline1) == 1 {
		cmd1 = exec.CommandContext(ctx, acmdline1[0])
	} else {
		cmd1 = exec.CommandContext(ctx, acmdline1[0], acmdline1[1:]...)
	}

	cmd1.Env = append(
		os.Environ(),
		fmt.Sprintf("KUBECONFIG=%s", kubeconfig),
	)

	if len(acmdline2) == 0 {
		return fmt.Errorf("runTwoCommands has empty command")
	} else if len(acmdline2) == 1 {
		cmd2 = exec.CommandContext(ctx, acmdline2[0])
	} else {
		cmd2 = exec.CommandContext(ctx, acmdline2[0], acmdline2[1:]...)
	}

	readPipe, writePipe, err = os.Pipe()
	if err != nil {
		return fmt.Errorf("Error returned from os.Pipe: %v", err)
	}

	defer readPipe.Close()

	cmd1.Stdout = writePipe

	err = cmd1.Start()
	if err != nil {
		return fmt.Errorf("Error returned from cmd1.Start: %v", err)
	}

	defer cmd1.Wait()

	writePipe.Close()

	cmd2.Stdin = readPipe
	cmd2.Stdout = &buffer
	cmd2.Stderr = &buffer

	cmd2.Run()

	out = buffer.Bytes()

	fmt.Println("8<--------8<--------8<--------8<--------8<--------8<--------8<--------8<--------")
	fmt.Printf("%s | %s\n", cmdline1, cmdline2)
	fmt.Println(string(out))

	return nil
}
