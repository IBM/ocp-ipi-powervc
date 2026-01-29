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
	"io"
	"os"
	"strings"

	"github.com/sirupsen/logrus"
)

func checkAliveCommand(checkAliveFlags *flag.FlagSet, args []string) error {
	var (
		out            io.Writer
		ptrServerIP    *string
		ptrShouldDebug *string
		err            error
	)

	fmt.Fprintf(os.Stderr, "Program version is %v, release = %v\n", version, release)

	ptrServerIP = checkAliveFlags.String("serverIP", "", "The IP address of the server to send the command to")
	ptrShouldDebug = checkAliveFlags.String("shouldDebug", "false", "Should output debug output")

	checkAliveFlags.Parse(args)

	if ptrServerIP == nil || *ptrServerIP == "" {
		return fmt.Errorf("Error: --serverIP not specified")
	}

	switch strings.ToLower(*ptrShouldDebug) {
	case "true":
		shouldDebug = true
	case "false":
		shouldDebug = false
	default:
		return fmt.Errorf("Error: shouldDebug is not true/false (%s)\n", *ptrShouldDebug)
	}

	if shouldDebug {
		out = os.Stderr
	} else {
		out = io.Discard
	}
	log = &logrus.Logger{
		Out:       out,
		Formatter: new(logrus.TextFormatter),
		Level:     logrus.DebugLevel,
	}

	err = sendCheckAlive(*ptrServerIP)

	return err
}
