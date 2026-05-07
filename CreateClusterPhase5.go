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
	"io/ioutil"
	"os"
	"path/filepath"

	"sigs.k8s.io/yaml"
)

// Note: This file uses the global 'log' variable declared in PowerVC-Tool.go

var (
	errSecurityGroupDeleted = errors.New("security group deleted")
)

//
// Remove the security groups
//
func createClusterPhase5(directory string) error {
	var (
		err           error
	)

	err = runSplitCommand([]string{
		"openshift-install",
		"create",
		"manifests",
		"--dir",
		directory,
	})
	if err != nil {
		return err
	}

	err = processManifestsSecurityGroups(directory)

	return err
}

func processManifestsSecurityGroups(directory string) error {
	var (
		err error
	)

	err = processManifestDirectorySecurityGroups(fmt.Sprintf("%s/%s", directory, "openshift"))
	if err != nil {
		return err
	}

	err = processManifestDirectorySecurityGroups(fmt.Sprintf("%s/%s", directory, "cluster-api/machines"))

	return err
}

func processManifestDirectorySecurityGroups(directory string) error {
	var (
		entries  []os.DirEntry
		entry    os.DirEntry
		filename string
		err      error
	)

	log.Debugf("processManifestDirectorySecurityGroups: %s", directory)

	entries, err = os.ReadDir(directory)
	if err != nil {
		return err
	}

	for _, entry = range entries {
		if entry.IsDir() {
			continue
		}

		filename = filepath.Join(directory, entry.Name())
		log.Debugf("processManifestDirectorySecurityGroups: %s", filename)

		err = removeSecurityGroupsManifest(filename)
		if err != nil {
			return err
		}
	}

	return err
}

func removeSecurityGroupsManifest(filename string) error {
	var (
		abyteYamlOld []byte
		abyteJsonOld []byte
		jsonOld      map[string]any
		changed      = false
		abyteJsonNew []byte
		abyteYamlNew []byte
		err          error
	)

	abyteYamlOld, err = ioutil.ReadFile(filename)
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
	log.Debugf("jsonOld = %+v", jsonOld)

	err = removeSecurityGroups(jsonOld)
	log.Debugf("jsonOld = %+v", jsonOld)
	if err != nil {
		if !errors.Is(err, errSecurityGroupDeleted) {
			return err
		}
		log.Debugf("Found errSecurityGroupDeleted")
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

func removeSecurityGroups(node map[string]any) error {
	for k, v := range node {
		if k == "securityGroups" {
			log.Debugf("FOUND securityGroups! type: %T", v)
			delete(node, "securityGroups")
			return errSecurityGroupDeleted
		}
		switch value := v.(type) {
		case map[string]any:
			log.Debugf("map[string]any: key = %s, value = %+v", k, value)

			if err := removeSecurityGroups(value); err != nil {
				return err
			}
		case []interface{}:
			log.Debugf("[]interface{}: key = %s, value = %+v", k, value)
			if err := removeSecurityGroupsArray(value); err != nil {
				return err
			}
		}
	}

	return nil
}

func removeSecurityGroups2(node map[string]any) error {
	for k, v := range node {
		switch value := v.(type) {
		case map[string]any:
			log.Debugf("key = %s, value = %+v", k, value)
			if k == "securityGroups" {
				log.Debugf("FOUND securityGroups!")
				delete(value, "securityGroups")
				return nil
			}

			if err := removeSecurityGroups(value); err != nil {
				return err
			}
		case []interface{}:
			log.Debugf("key = %s, value = %+v", k, value)

			if err := removeSecurityGroupsArray(value); err != nil {
				return err
			}
		}
	}

	return nil
}

func removeSecurityGroupsArray(node []interface{}) error {
	for _, value := range node {
		if mapChild, ok := value.(map[string]any); ok {
			if err := removeSecurityGroups(mapChild); err != nil {
				return err
			}
		}
	}

	return nil
}
