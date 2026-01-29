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

// (/bin/rm go.*; go mod init example/user/PowerVS-Check; go mod tidy)
// (echo "vet:"; go vet || exit 1; echo "build:"; go build -ldflags="-X main.version=$(git describe --always --long --dirty) -X main.release=$(git describe --tags --abbrev=0)" -o PowerVS-Check-Create *.go || exit 1; echo "run:"; ./PowerVS-Check check-create -apiKey "..." -metadata metadata.json -shouldDebug true)

package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"
)

var (
	// Replaced with:
	//   -ldflags="-X main.version=$(git describe --always --long --dirty)"
	version = "undefined"
	release = "undefined"

	shouldDebug  = false
	shouldDelete = false

	log *logrus.Logger
)

func printUsage(executableName string) {
	fmt.Fprintf(os.Stderr, "Program version is %v, release = %v\n", version, release)

	fmt.Fprintf(os.Stderr, "Usage: %s [ "+
		"check-alive "+
		"| create-bastion "+
		"| create-rhcos "+
		"| create-cluster "+
		"| send-metadata "+
		"| watch-installation "+
		"| watch-create"+
		" ]\n", executableName)
}

func main() {
	var (
		executableName          string
		checkAliveFlags         *flag.FlagSet
		createBastionFlags      *flag.FlagSet
		createClusterFlags      *flag.FlagSet
		createRhcosFlags        *flag.FlagSet
		sendMetadataFlags       *flag.FlagSet
		watchInstallationFlags  *flag.FlagSet
		watchCreateClusterFlags *flag.FlagSet
		err                     error
	)

	executablePath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting executable path: %v\n", err)
		os.Exit(1)
	}

	executableName = filepath.Base(executablePath)

	if len(os.Args) == 1 {
		printUsage(executableName)
		os.Exit(1)
	} else if len(os.Args) == 2 && os.Args[1] == "-version" {
		fmt.Fprintf(os.Stderr, "version = %v\nrelease = %v\n", version, release)
		os.Exit(1)
	}

	checkAliveFlags = flag.NewFlagSet("check-alive", flag.ExitOnError)
	createBastionFlags = flag.NewFlagSet("create-bastion", flag.ExitOnError)
	createClusterFlags = flag.NewFlagSet("create-cluster", flag.ExitOnError)
	createRhcosFlags = flag.NewFlagSet("create-rhcos", flag.ExitOnError)
	sendMetadataFlags = flag.NewFlagSet("send-metadata", flag.ExitOnError)
	watchInstallationFlags = flag.NewFlagSet("watch-cluster", flag.ExitOnError)
	watchCreateClusterFlags = flag.NewFlagSet("watch-create", flag.ExitOnError)

	switch strings.ToLower(os.Args[1]) {
	case "check-alive":
		err = checkAliveCommand(checkAliveFlags, os.Args[2:])

	case "create-bastion":
		err = createBastionCommand(createBastionFlags, os.Args[2:])

	case "create-cluster":
		err = createClusterCommand(createClusterFlags, os.Args[2:])

	case "create-rhcos":
		err = createRhcosCommand(createRhcosFlags, os.Args[2:])

	case "send-metadata":
		err = sendMetadataCommand(sendMetadataFlags, os.Args[2:])

	case "watch-installation":
		err = watchInstallationCommand(watchInstallationFlags, os.Args[2:])

	case "watch-create":
		err = watchCreateClusterCommand(watchCreateClusterFlags, os.Args[2:])

	default:
		fmt.Fprintf(os.Stderr, "Error: Unknown command %s\n", os.Args[1])
		printUsage(executableName)
		os.Exit(1)
	}

	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
