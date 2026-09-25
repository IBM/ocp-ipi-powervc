package main

import (
	"encoding/json"
	"fmt"
)

func main() {
	var jsonStr = `{
        "Name": "test-server",
        "Addresses": [
		{
	                "network1": {
					"OS-EXT-IPS-MAC:mac_addr": "fa:16:3e:00:00:01",
                	                "addr":                    "192.168.1.10"
                	}
		},
		{
                	"network2": {
                        	        "OS-EXT-IPS-MAC:mac_addr": "fa:16:3e:00:00:02",
                                	"addr":                    "10.0.0.10"
                	}
		}
      ]
}`
	var data map[string]interface{}

	json.Unmarshal([]byte(jsonStr), &data)

	fmt.Printf("data = %+v\n", data)
}
