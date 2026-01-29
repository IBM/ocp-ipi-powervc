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
	"io/ioutil"
	"os"
	"strings"

	"github.com/sirupsen/logrus"
)

func watchCreateClusterCommand(watchCreateClusterFlags *flag.FlagSet, args []string) error {
	var (
		out                io.Writer
		apiKey             string
		ptrCloud           *string
		ptrMetadata        *string
		ptrKubeConfig      *string
		ptrBastionUsername *string
		ptrBastionRsa      *string
		ptrBaseDomain      *string
		ptrShouldDebug     *string
		metadata           *Metadata
		services           *Services
		robjsFuncs         []NewRunnableObjectsEntry
		robjsCluster       []RunnableObject
		robjObjectName     string
		err                error
	)

	fmt.Fprintf(os.Stderr, "Program version is %v, release = %v\n", version, release)

	ptrCloud = watchCreateClusterFlags.String("cloud", "", "The cloud to use in clouds.yaml")
	ptrMetadata = watchCreateClusterFlags.String("metadata", "", "The location of the metadata.json file")
	ptrKubeConfig = watchCreateClusterFlags.String("kubeconfig", "", "The KUBECONFIG file")
	ptrBastionUsername = watchCreateClusterFlags.String("bastionUsername", "", "The username of the bastion VM to use")
	ptrBastionRsa = watchCreateClusterFlags.String("bastionRsa", "", "The RSA filename for the bastion VM to use")
	ptrBaseDomain = watchCreateClusterFlags.String("baseDomain", "", "The DNS base name to use")
	ptrShouldDebug = watchCreateClusterFlags.String("shouldDebug", "false", "Should output debug output")

	watchCreateClusterFlags.Parse(args)

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

	// ibmcloud is optional
	apiKey = os.Getenv("IBMCLOUD_API_KEY")
	if len(apiKey) != 0 {
		// Before we do a lot of work, validate the apikey!
		_, err = InitBXService(apiKey)
		if err != nil {
			return err
		}
	}

	if ptrCloud == nil || *ptrCloud == "" {
		return fmt.Errorf("Error: --cloud not specified")
	}
	if *ptrMetadata == "" {
		return fmt.Errorf("Error: No metadata file location iset, use -metadata")
	}
	if ptrBastionUsername == nil || *ptrBastionUsername == "" {
		return fmt.Errorf("Error: --bastionUsername not specified")
	}
	if ptrBastionRsa == nil || *ptrBastionRsa == "" {
		return fmt.Errorf("Error: --bastionRsa not specified")
	}

	_, err = ioutil.ReadFile(*ptrMetadata)
	if err != nil {
		return fmt.Errorf("Error: Opening metadata file %s had %v", *ptrMetadata, err)
	}

	robjsFuncs = make([]NewRunnableObjectsEntry, 0)
	if *ptrKubeConfig != "" {
		robjsFuncs = append(robjsFuncs, NewRunnableObjectsEntry{NewOc, "OpenShift Cluster"})
	}
	robjsFuncs = append(robjsFuncs, NewRunnableObjectsEntry{NewVMs,          "Virtual Machines"})
	robjsFuncs = append(robjsFuncs, NewRunnableObjectsEntry{NewLoadBalancer, "Load Balancer"})
	if *ptrBaseDomain != "" {
		robjsFuncs = append(robjsFuncs, NewRunnableObjectsEntry{NewIBMDNS, "IBM Domain Name Service"})
	}

	metadata, err = NewMetadataFromCCMetadata(*ptrMetadata)
	if err != nil {
		return fmt.Errorf("Error: Could not read metadata from %s\n", *ptrMetadata)
	}
	log.Debugf("metadata = %+v", metadata)

	services, err = NewServices(metadata, apiKey, *ptrKubeConfig, *ptrCloud, *ptrBastionUsername, *ptrBastionRsa, *ptrBaseDomain)
	if err != nil {
		return fmt.Errorf("Error: Could not create a Services object (%s)!\n", err)
	}

	robjsCluster, err = initializeRunnableObjects(services, robjsFuncs)
	if err != nil {
		return err
	}

	// Sort the objects by their priority.
	robjsCluster = BubbleSort(robjsCluster)
	for _, robj := range robjsCluster {
		robjObjectName, _ = robj.ObjectName()
		log.Debugf("Sorted %s %+v", robjObjectName, robj)
	}
	fmt.Fprintf(os.Stderr, "Sorted the objects.\n")

	// Query the status of the objects.
	for _, robj := range robjsCluster {
		robj.ClusterStatus()
	}

	return nil
}
