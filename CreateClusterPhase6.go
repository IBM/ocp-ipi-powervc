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

	"sigs.k8s.io/yaml"
)

// Note: This file uses the global 'log' variable declared in PowerVC-Tool.go

const (
//	rhcosImage = "hamzy2-419-96-20250402-0-ppc64le"
	rhcosImage = "hamzy3-rhcos-9.6.20250826-1-openstack-ppc64le"
)

var (
	errImageChanged = errors.New("image changed")
)

//
// Change the image used by the VMs.
//
func createClusterPhase6(directory string) error {
	var (
		err           error
	)

	err = processManifestsImage(directory)

	return err
}

func processManifestsImage(directory string) error {
	var (
		err error
	)

	err = processManifestDirectoryImage(fmt.Sprintf("%s/%s", directory, "openshift"))
	if err != nil {
		return err
	}

	err = processManifestDirectoryImage(fmt.Sprintf("%s/%s", directory, "cluster-api/machines"))

	return err
}

func processManifestDirectoryImage(directory string) error {
	var (
		entries  []os.DirEntry
		entry    os.DirEntry
		filename string
		err      error
	)

	log.Debugf("processManifestDirectoryImage: %s", directory)

	entries, err = os.ReadDir(directory)
	if err != nil {
		return err
	}

	for _, entry = range entries {
		if entry.IsDir() {
			continue
		}

		filename = fmt.Sprintf("%s/%s", directory, entry.Name())
		log.Debugf("processManifestDirectoryImage: %s", filename)

		err = changeImagesManifest(filename)
		if err != nil {
			return err
		}
	}

	return err
}

func changeImagesManifest(filename string) error {
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

	err = changeImages(jsonOld)
	log.Debugf("jsonOld = %+v", jsonOld)
	if err != nil && !errors.Is(err, errImageChanged) {
		return err
	}
	changed = errors.Is(err, errImageChanged)

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

func changeImages(node map[string]any) error {
	for k, v := range node {
		if k == "image" {
			log.Debugf("FOUND image! type: %T", v)
			_, ok := node[k].(map[string]any)
			log.Debugf("ok = %v", ok)
			if ok {
				delete(node, "image")
				node["image"] = map[string]any {
					"filter":  map[string]string{
						"name": rhcosImage,
					},
				}
				return errImageChanged
			} else {
				node[k] = rhcosImage
				return errImageChanged
			}
		}
		switch value := v.(type) {
		case map[string]any:
			log.Debugf("map[string]any: key = %s, value = %+v", k, value)

			if err := changeImages(value); err != nil {
				return err
			}
		case []interface{}:
			log.Debugf("[]interface{}: key = %s, value = %+v", k, value)
			if err := changeImagesArray(value); err != nil {
				return err
			}
		}
	}

	return nil
}

func changeImagesArray(node []interface{}) error {
	for _, value := range node {
		if mapChild, ok := value.(map[string]any); ok {
			if err := changeImages(mapChild); err != nil {
				return err
			}
		}
	}

	return nil
}
