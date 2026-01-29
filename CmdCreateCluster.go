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

func createClusterCommand(createClusterFlags *flag.FlagSet, args []string) error {
	var (
		out            io.Writer
		ptrDirectory   *string
		ptrShouldDebug *string
		functions      = []func(string) error{
			createClusterPhase1,
			createClusterPhase2,
			createClusterPhase3,
			createClusterPhase4,
			createClusterPhase5,
			createClusterPhase6,
			createClusterPhase7,
//			createClusterPhase8,
		}
		err            error
	)

	fmt.Fprintf(os.Stderr, "Program version is %v, release = %v\n", version, release)

	ptrDirectory = createClusterFlags.String("directory", "", "The location of the installation directory")
	ptrShouldDebug = createClusterFlags.String("shouldDebug", "false", "Should output debug output")

	createClusterFlags.Parse(args)

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

	if *ptrDirectory == "" {
		return fmt.Errorf("Error: No directory key set, use -directory")
	}

	for _, function := range functions {
		err = function(*ptrDirectory)
		if err != nil {
			return err
		}
	}

	return nil
}
