// Copyright 2026 IBM Corp
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

// (cd snippet2/; /bin/rm go.*; go mod init example/user/snippet2; go mod tidy; go run snippet2.go)
// (cd snippet2/; /bin/rm go.*)

package main

import (
	"fmt"
	"strings"
	"github.com/gophercloud/gophercloud/v2/openstack/config/clouds"
	"github.com/gophercloud/utils/openstack/clientconfig"
)

func main1() {
	// Load config from clouds.yaml
	opts := &clientconfig.ClientOpts{Cloud: "my-cloud-name"}
	_, err := clientconfig.NewServiceClient("compute", opts)
	// _, err := clientconfig.GetCloudConfig(opts)
	fmt.Printf("err = %v\n", err)
	if err != nil {
		// Standard error check for a missing cloud entry
		if strings.Contains(err.Error(), "Could not find cloud") {
			fmt.Println("The specified OpenStack cloud does not exist in your config.")
		}
	}
}

func main2() {
	const exampleClouds = `clouds:
  openstack:
    auth:
      auth_url: https://example.com:13000`

	ao, _, _, err := clouds.Parse(
//		clouds.WithCloudsYAML(strings.NewReader(exampleClouds)),
		clouds.WithCloudName("openstack"),
	)
	if err != nil {
		panic(err)
	}

	fmt.Println(ao.IdentityEndpoint)
}

func main() {
	main2()
}
