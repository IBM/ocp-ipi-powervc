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
	"strings"
	"os"

	"github.com/sirupsen/logrus"
)

func sendMetadataCommand(sendMetadataFlags *flag.FlagSet, args []string) error {
	var (
		out                  io.Writer
		ptrCreateMetadata    *string
		ptrDeleteMetadata    *string
		ptrServerIP          *string
		ptrShouldDebug       *string
		shouldCreateMetadata bool
		shouldDeleteMetadata bool
		metadataFile         string
		err                  error
	)

	fmt.Fprintf(os.Stderr, "Program version is %v, release = %v\n", version, release)

	ptrCreateMetadata = sendMetadataFlags.String("createMetadata", "", "Create the metadata of this file")
	ptrDeleteMetadata = sendMetadataFlags.String("deleteMetadata", "", "Delete the metadata of this file")
	ptrServerIP = sendMetadataFlags.String("serverIP", "", "The IP address of the server to send the command to")
	ptrShouldDebug = sendMetadataFlags.String("shouldDebug", "false", "Should output debug output")

	sendMetadataFlags.Parse(args)

	if ptrCreateMetadata != nil && *ptrCreateMetadata != "" {
		shouldCreateMetadata = true
		metadataFile = *ptrCreateMetadata
	}
	if ptrDeleteMetadata != nil && *ptrDeleteMetadata != "" {
		shouldDeleteMetadata = true
		metadataFile = *ptrDeleteMetadata
	}

	if !shouldCreateMetadata && !shouldDeleteMetadata {
		return fmt.Errorf("Error: Either --createMetadata or --deleteMetadata should be specified")
	}
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

	err = sendMetadata(metadataFile, *ptrServerIP, shouldCreateMetadata)

	return err
}
