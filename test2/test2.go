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
