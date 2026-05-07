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
	"fmt"
	"os"
	"path/filepath"

	"sigs.k8s.io/yaml"
)

var (
	testSubnet = false
)

// Note: This file uses the global 'log' variable declared in PowerVC-Tool.go

// process an unmarshalled JSON array structure by finding every map element.
func replacePlatformArray(node []interface{}) error {
	for _, value := range node {
		if mapChild, ok := value.(map[string]any); ok {
			if err := replacePlatformMap(mapChild); err != nil {
				return err
			}
		}
	}

	return nil
}

// process an unmarshalled JSON map structure by finding every platform element, and replacing the powervc
// child element with an openstack element.
func replacePlatformMap(node map[string]any) error {
	for k, v := range node {
		switch value := v.(type) {
		case map[string]any:
			if k == "platform" {
				nodePowerVC, ok := value["powervc"]
				if ok {
					value["openstack"] = nodePowerVC
					delete(value, "powervc")
				} else {
					return fmt.Errorf("could not convert powervc in the json")
				}

				continue
			}

			if err := replacePlatformMap(value); err != nil {
				return err
			}
		case []interface{}:
			if err := replacePlatformArray(value); err != nil {
				return err
			}
		}
	}

	return nil
}

//
// Replace powervc platform with openstack platform
//
func createClusterPhase2(directory string) error {
	var (
		abyteYamlOld []byte
		abyteJsonOld []byte
		jsonOld      map[string]any
		abyteJsonNew []byte
		abyteYamlNew []byte
		err          error
	)

	abyteYamlOld, err = os.ReadFile(filepath.Join(directory, "install-config.yaml"))
	if err != nil {
		return fmt.Errorf("Error reading YAML file: %v", err)
	}

	abyteJsonOld, err = yaml.YAMLToJSON(abyteYamlOld)
	if err != nil {
		return fmt.Errorf("Error: could not convert yaml to json: %v", err)
	}
	log.Debugf("abyteJsonOld read successfully (%d bytes)", len(abyteJsonOld))

	err = json.Unmarshal(abyteJsonOld, &jsonOld)
	if err != nil {
		return fmt.Errorf("Error: could not unmarshal the json: %v", err)
	}

	err = replacePlatformMap(jsonOld)
	if err != nil {
		return fmt.Errorf("Error: could not replacePlatformMap the json: %v", err)
	}
	log.Debugf("replacePlatformMap completed successfully")

	abyteJsonNew, err = json.Marshal(jsonOld)
	if err != nil {
		return err
	}

	abyteYamlNew, err = yaml.JSONToYAML(abyteJsonNew)
	if err != nil {
		return err
	}

	err = os.WriteFile(filepath.Join(directory, "install-config.yaml"), abyteYamlNew, 0600)
	if err != nil {
		return err
	}

if testSubnet {
	err = runSplitCommand([]string{
		"sed",
		"-i",
		"s,subnet: null,subnet:,",
		fmt.Sprintf("%s/%s", directory, "install-config.yaml"),
	})
	if err != nil {
		return err
	}
}

	return nil
}
