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
	"fmt"
	"os"
	"time"

	"github.com/gophercloud/gophercloud/v2/openstack/objectstorage/v1/objects"
)

//
// Upload the bootstrap igniton file to Swift.
//
func createClusterPhase4(directory string) error {
	var (
		metadata      *Metadata
		cloud         string
		filename      string
		containerName string
		objectName    string
		ctx           context.Context
		cancel        context.CancelFunc
		err           error
	)

	metadata, err = NewMetadataFromCCMetadata(fmt.Sprintf("%s/%s", directory, "metadata.json"))
	log.Debugf("metadata = %+v", metadata)
	if err != nil {
		return err
	}

	cloud = metadata.GetCloud()
	log.Debugf("cloud = %s", cloud)

	filename = fmt.Sprintf("%s/%s", directory, "bootstrap.ign")
	log.Debugf("filename = %s", filename)

	containerName = fmt.Sprintf("%s-ignition", metadata.GetInfraID())
	log.Debugf("containerName = %s", containerName)
	objectName = containerName

	ctx, cancel = context.WithTimeout(context.TODO(), 15*time.Minute)
	defer cancel()

	conn, err := NewServiceClient(ctx, "object-store", DefaultClientOpts(cloud))
	if err != nil {
		return err
	}
	fmt.Printf("conn = %+v\n", conn)

	f, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer f.Close()

	// Content            io.Reader
	// Metadata           map[string]string
	// NoETag             bool
	// CacheControl       string `h:"Cache-Control"`
	// ContentDisposition string `h:"Content-Disposition"`
	// ContentEncoding    string `h:"Content-Encoding"`
	// ContentLength      int64  `h:"Content-Length"`
	// ContentType        string `h:"Content-Type"`
	// CopyFrom           string `h:"X-Copy-From"`
	// DeleteAfter        int64  `h:"X-Delete-After"`
	// DeleteAt           int64  `h:"X-Delete-At"`
	// DetectContentType  string `h:"X-Detect-Content-Type"`
	// ETag               string `h:"ETag"`
	// IfNoneMatch        string `h:"If-None-Match"`
	// ObjectManifest     string `h:"X-Object-Manifest"`
	// TransferEncoding   string `h:"Transfer-Encoding"`
	// Expires            string `q:"expires"`
	// MultipartManifest  string `q:"multipart-manifest"`
	// Signature          string `q:"signature"`
	//
	// Content:     strings.NewReader(content),
	// ContentType: "text/plain",
	//
	header, err := objects.Create(ctx,
		conn,
		containerName,
		objectName,
		objects.CreateOpts{
			Content: f,
		}).Extract()
	fmt.Printf("header = %+v\n", header)
	fmt.Printf("err = %+v\n", err)
	if err != nil {
		return err
	}

	return err
}
