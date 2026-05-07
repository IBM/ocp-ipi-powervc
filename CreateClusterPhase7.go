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
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"sigs.k8s.io/yaml"
)

// Note: This file uses the global 'log' variable declared in PowerVC-Tool.go

var (
	errAddedLoadBalancer = errors.New("added LoadBalancer configuration")
)

//
// Adds a LoadBalancer block with enabled=false to the cloud provider config if missing.
//
func createClusterPhase7(directory string) error {
	var (
		filename = filepath.Join(directory, "manifests", "cloud-provider-config.yaml")
		err      error
	)

	fmt.Println("8<--------8<--------8<--------8<--------8<--------8<--------8<--------8<--------")

	err = processCloudProviderConfig(filename)

	return err
}

func processCloudProviderConfig(filename string) error {
	var (
		abyteYamlOld []byte
		abyteJsonOld []byte
		jsonOld      map[string]any
		changed      = false
		abyteJsonNew []byte
		abyteYamlNew []byte
		err          error
	)

	abyteYamlOld, err = os.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("Error reading YAML file: %v", err)
	}

	abyteJsonOld, err = yaml.YAMLToJSON(abyteYamlOld)
	if err != nil {
		return fmt.Errorf("Error: could not convert yaml to json: %v", err)
	}
//	log.Debugf("abyteJsonOld = %+v", string(abyteJsonOld))

	err = json.Unmarshal(abyteJsonOld, &jsonOld)
	if err != nil {
		return fmt.Errorf("Error: could not unmarshal the json: %v", err)
	}
//	log.Debugf("jsonOld = %+v", jsonOld)

	err = changeCloudProviderConfig(jsonOld)
//	log.Debugf("jsonOld = %+v", jsonOld)
	if err != nil {
		if !errors.Is(err, errAddedLoadBalancer) {
			return err
		}
		log.Debugf("Found errAddedLoadBalancer")
		changed = true
	}

	if !changed {
		return nil
	}

	abyteJsonNew, err = json.Marshal(jsonOld)
	if err != nil {
		return err
	}

	abyteYamlNew, err = yaml.JSONToYAML(abyteJsonNew)
	if err != nil {
		return err
	}

	err = os.WriteFile(filename, abyteYamlNew, 0644)
	if err != nil {
		return err
	}

	return err
}

func changeCloudProviderConfig(jsonOld map[string]any) error {
	var (
		ok      bool
		dataMap map[string]any
		config  string
	)

//	log.Debugf("jsonOld = %+v", jsonOld)

	v, ok := jsonOld["data"]
	if !ok {
		return fmt.Errorf("Could not find data in cloud provider config")
	}

	dataMap, ok = v.(map[string]any)
	if !ok {
		return fmt.Errorf("Could not convert data to map[string]any")
	}

	v, ok = dataMap["config"]
	if !ok {
		return fmt.Errorf("Could not find config in dataMap")
	}

	config, ok = v.(string)
	if !ok {
		return fmt.Errorf("Could not convert config to string")
	}
	log.Debugf("config = %+v", config)

	for _, line := range strings.Split(config, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "[LoadBalancer]" {
			return nil
		}
	}

	// Ensure there's a newline before the new section
	if !strings.HasSuffix(config, "\n") {
		config = config + "\n"
	}
	config = config + "[LoadBalancer]\nenabled = false\n"

	dataMap["config"] = config

	return errAddedLoadBalancer
}
