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
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"os"
	"os/exec"
	"path"
	"time"

	igntypes "github.com/coreos/ignition/v2/config/v3_2/types"

	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"

	"github.com/sirupsen/logrus"

	"k8s.io/utils/ptr"
)

func createRhcosCommand(createRhcosFlags *flag.FlagSet, args []string) error {
	var (
		out             io.Writer
		apiKey          string
		ptrCloud        *string
		ptrRhcosName    *string
		ptrFlavorName   *string
		ptrImageName    *string
		ptrNetworkName  *string
		ptrPasswdHash   *string
		ptrSshPublicKey *string
		ptrDomainName   *string
		ptrShouldDebug  *string
		ctx             context.Context
		cancel          context.CancelFunc
		userData        []byte
		foundServer     servers.Server
		err             error
	)

	fmt.Fprintf(os.Stderr, "Program version is %v, release = %v\n", version, release)

	// NOTE: This is optional
	apiKey = os.Getenv("IBMCLOUD_API_KEY")

	ptrCloud = createRhcosFlags.String("cloud", "", "The cloud to use in clouds.yaml")
	ptrRhcosName = createRhcosFlags.String("rhcosName", "", "The name of the bastion VM to use")
	ptrFlavorName = createRhcosFlags.String("flavorName", "", "The name of the flavor to use")
	ptrImageName = createRhcosFlags.String("imageName", "", "The name of the image to use")
	ptrNetworkName = createRhcosFlags.String("networkName", "", "The name of the network to use")
	ptrPasswdHash = createRhcosFlags.String("passwdHash", "", "The password hash of the core user")
	ptrSshPublicKey = createRhcosFlags.String("sshPublicKey", "", "The contents of the ssh public key to use")
	// NOTE: This is optional
	ptrDomainName = createRhcosFlags.String("domainName", "", "The DNS domain to use")
	ptrShouldDebug = createRhcosFlags.String("shouldDebug", "false", "Should output debug output")

	createRhcosFlags.Parse(args)

	if ptrCloud == nil || *ptrCloud == "" {
		return fmt.Errorf("Error: --cloud not specified")
	}
	if ptrRhcosName == nil || *ptrRhcosName == "" {
		return fmt.Errorf("Error: --bastionName not specified")
	}
	if ptrFlavorName == nil || *ptrFlavorName == "" {
		return fmt.Errorf("Error: --flavorName not specified")
	}
	if ptrImageName == nil || *ptrImageName == "" {
		return fmt.Errorf("Error: --imageName not specified")
	}
	if ptrNetworkName == nil || *ptrNetworkName == "" {
		return fmt.Errorf("Error: --networkName not specified")
	}
	if ptrSshPublicKey == nil || *ptrSshPublicKey == "" {
		return fmt.Errorf("Error: --sshPublicKey not specified")
	}
	if ptrPasswdHash == nil || *ptrPasswdHash == "" {
		return fmt.Errorf("Error: --passwdHash not specified")
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

	ctx, cancel = context.WithTimeout(context.TODO(), 15*time.Minute)
	defer cancel()

	userData, err = bootstrapIgnitionFile(*ptrPasswdHash, *ptrSshPublicKey)
	if err != nil {
		return err
	}

	foundServer, err = findServer(ctx, *ptrCloud, *ptrRhcosName)
//	log.Debugf("foundServer = %+v", foundServer)
	if err != nil {
		if strings.HasPrefix(err.Error(), "Could not find server named") {
			fmt.Printf("Could not find server %s, creating...\n", *ptrRhcosName)

			err = createServer(ctx,
				*ptrCloud,
				*ptrFlavorName,
				*ptrImageName,
				*ptrNetworkName,
				"",			// No ssh-key
				*ptrRhcosName,
				userData,
			)
			if err != nil {
				return err
			}

			fmt.Println("Done!")

			foundServer, err = findServer(ctx, *ptrCloud, *ptrRhcosName)
			if err != nil {
				return err
			}
		} else {
			return err
		}
	}

	err = setupRhcosServer(ctx, *ptrCloud, foundServer)
	if err != nil {
		return err
	}

	if apiKey != "" {
		err = dnsForServer(ctx, *ptrCloud, apiKey, *ptrRhcosName, *ptrDomainName)
		if err != nil {
			return err
		}
	} else {
		fmt.Println("Warning: IBMCLOUD_API_KEY not set.  Make sure DNS is supported via another way.")
	}

	return err
}

func setupRhcosServer(ctx context.Context, cloudName string, server servers.Server) error {
	var (
		ipAddress    string
		homeDir      string
		outb         []byte
		outs         string
		exitError    *exec.ExitError
		err          error
	)

	log.Debugf("setupRhcosServer: server = %+v", server)

	_, ipAddress, err = findIpAddress(server)
	if err != nil {
		return err
	}
	if ipAddress == "" {
		return fmt.Errorf("ip address is empty for server %s", server.Name)
	}

	log.Debugf("setupRhcosServer: ipAddress = %s", ipAddress)

	homeDir, err = os.UserHomeDir()
	if err != nil {
		return err
	}
	log.Debugf("setupRhcosServer: homeDir = %s", homeDir)

	outb, err = runSplitCommand2([]string{
		"ssh-keygen",
		"-H",
		"-F",
		ipAddress,
	})
	outs = strings.TrimSpace(string(outb))
	log.Debugf("setupRhcosServer: outs = \"%s\"", outs)
	if errors.As(err, &exitError) {
		log.Debugf("setupRhcosServer: exitError.ExitCode() = %+v\n", exitError.ExitCode())

		log.Debugf("setupRhcosServer: %v", exitError.ExitCode() == 1)
		if exitError.ExitCode() == 1 {

			outb, err = keyscanServer(ctx, ipAddress, false)
			if err != nil {
				return err
			}

			knownHosts := path.Join(homeDir, ".ssh/known_hosts")
			log.Debugf("setupRhcosServer: knownHosts = %s", knownHosts)

			fileKnownHosts, err := os.OpenFile(knownHosts, os.O_APPEND|os.O_RDWR, 0644)
			if err != nil {
				return err
			}

			fileKnownHosts.Write(outb)

			defer fileKnownHosts.Close()
		}
	}

	fmt.Printf("Setting up server %s...\n", server.Name)
	return nil
}

// Marshal is a helper function to use the marshaler function from "github.com/clarketm/json".
// It supports zero values of structs with the omittempty annotation.
// In effect this excludes empty pointer struct fields from the marshaled data,
// instead of inserting nil values into them.
// This is necessary for ignition configs to pass openAPI validation on fields
// that are not supposed to contain nil pointers, but e.g. strings.
// It can be used as a dropin replacement for "encoding/json".Marshal
func Marshal(input interface{}) ([]byte, error) {
	return json.Marshal(input)
}

func bootstrapIgnitionFile (passwdHash string, sshKey string) ([]byte, error) {
	var (
		byteData []byte
		strData  string
		err      error
	)

	byteData, err = Marshal(igntypes.Config{
		Ignition: igntypes.Ignition{
			Version: igntypes.MaxVersion.String(),
			Timeouts: igntypes.Timeouts{
				HTTPResponseHeaders: ptr.To(120),
			},
		},
		Passwd: igntypes.Passwd{
			Users: []igntypes.PasswdUser{
				igntypes.PasswdUser{
					Name:             "core",
					PasswordHash:      ptr.To(passwdHash),
					SSHAuthorizedKeys: []igntypes.SSHAuthorizedKey{
						igntypes.SSHAuthorizedKey(sshKey),
					},
				},
			},
		},
	})
	fmt.Printf("byteData = %v\n", byteData)

	if err != nil {
		return nil, fmt.Errorf("unable to encode the Ignition: %w", err)
	}

	strData = base64.StdEncoding.EncodeToString(byteData)
	fmt.Printf("strData = %v\n", strData)

	// Check the size of the base64-rendered ignition shim isn't to big for nova
	// https://docs.openstack.org/nova/latest/user/metadata.html#user-data
	if len(strData) > 65535 {
		return nil, fmt.Errorf("rendered bootstrap ignition shim exceeds the 64KB limit for nova user data")
	}

	return byteData, nil
}
