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

	"github.com/IBM/go-sdk-core/v5/core"
	"github.com/IBM/networking-go-sdk/dnsrecordsv1"
	"github.com/IBM/networking-go-sdk/zonesv1"
	"github.com/IBM/platform-services-go-sdk/globalcatalogv1"
	"github.com/IBM/platform-services-go-sdk/resourcecontrollerv2"
)

// Note: This file uses the global 'log' variable declared in PowerVC-Tool.go
// and the 'leftInContext' function defined in CmdCreateBastion.go
// and the 'retryWithBackoff' function defined in Utils.go

// listResourceInstances retrieves a list of resource instances from IBM Cloud.
// It automatically retries on transient failures using exponential backoff.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - controllerSvc: IBM Cloud Resource Controller service client
//   - listResourceOptions: Options for filtering and pagination
//
// Returns:
//   - *resourcecontrollerv2.ResourceInstancesList: List of resource instances
//   - *core.DetailedResponse: HTTP response details
//   - error: Any error encountered during the operation
//
// Reference: https://cloud.ibm.com/apidocs/resource-controller/resource-controller#list-resource-instances
// SDK Reference: https://github.com/IBM/platform-services-go-sdk/blob/main/resourcecontrollerv2/resource_controller_v2.go#L5008
func listResourceInstances(
	ctx context.Context,
	controllerSvc *resourcecontrollerv2.ResourceControllerV2,
	listResourceOptions *resourcecontrollerv2.ListResourceInstancesOptions,
) (*resourcecontrollerv2.ResourceInstancesList, *core.DetailedResponse, error) {
	if controllerSvc == nil {
		return nil, nil, fmt.Errorf("ListResourceInstances failed: controllerSvc cannot be nil")
	}
	return retryWithBackoff(ctx, func(ctx context.Context) (*resourcecontrollerv2.ResourceInstancesList, *core.DetailedResponse, error) {
		return controllerSvc.ListResourceInstancesWithContext(ctx, listResourceOptions)
	}, "ListResourceInstances")
}

// listCatalogEntries retrieves catalog entries from IBM Cloud Global Catalog.
// It automatically retries on transient failures using exponential backoff.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - gcv1: IBM Cloud Global Catalog service client
//   - listCatalogEntriesOpt: Options for filtering catalog entries
//
// Returns:
//   - *globalcatalogv1.EntrySearchResult: Search results containing catalog entries
//   - *core.DetailedResponse: HTTP response details
//   - error: Any error encountered during the operation
//
// Reference: https://cloud.ibm.com/apidocs/resource-catalog/global-catalog#list-catalog-entries
// SDK Reference: https://github.com/IBM/platform-services-go-sdk/blob/main/globalcatalogv1/global_catalog_v1.go
func listCatalogEntries(
	ctx context.Context,
	gcv1 *globalcatalogv1.GlobalCatalogV1,
	listCatalogEntriesOpt *globalcatalogv1.ListCatalogEntriesOptions,
) (*globalcatalogv1.EntrySearchResult, *core.DetailedResponse, error) {
	if gcv1 == nil {
		return nil, nil, fmt.Errorf("ListCatalogEntries failed: gcv1 cannot be nil")
	}
	return retryWithBackoff(ctx, func(ctx context.Context) (*globalcatalogv1.EntrySearchResult, *core.DetailedResponse, error) {
		return gcv1.ListCatalogEntriesWithContext(ctx, listCatalogEntriesOpt)
	}, "ListCatalogEntries")
}

// GetChildObjects retrieves child objects from IBM Cloud Global Catalog.
// It automatically retries on transient failures using exponential backoff.
// This function is exported for use by other packages.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - gcv1: IBM Cloud Global Catalog service client
//   - getChildOpt: Options for retrieving child objects
//
// Returns:
//   - *globalcatalogv1.EntrySearchResult: Search results containing child objects
//   - *core.DetailedResponse: HTTP response details
//   - error: Any error encountered during the operation
//
// Reference: https://cloud.ibm.com/apidocs/resource-catalog/global-catalog#get-child-catalog-entries
// SDK Reference: https://github.com/IBM/platform-services-go-sdk/blob/main/globalcatalogv1/global_catalog_v1.go
func GetChildObjects(
	ctx context.Context,
	gcv1 *globalcatalogv1.GlobalCatalogV1,
	getChildOpt *globalcatalogv1.GetChildObjectsOptions,
) (*globalcatalogv1.EntrySearchResult, *core.DetailedResponse, error) {
	if gcv1 == nil {
		return nil, nil, fmt.Errorf("GetChildObjects failed: gcv1 cannot be nil")
	}
	return retryWithBackoff(ctx, func(ctx context.Context) (*globalcatalogv1.EntrySearchResult, *core.DetailedResponse, error) {
		return gcv1.GetChildObjectsWithContext(ctx, getChildOpt)
	}, "GetChildObjects")
}

// listZones retrieves DNS zones from IBM Cloud Internet Services.
// It automatically retries on transient failures using exponential backoff.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - zv1: IBM Cloud Zones service client
//   - listOpts: Options for listing zones
//
// Returns:
//   - *zonesv1.ListZonesResp: List of DNS zones
//   - *core.DetailedResponse: HTTP response details
//   - error: Any error encountered during the operation
//
// Reference: https://cloud.ibm.com/apidocs/cis#list-all-zones
// SDK Reference: https://github.com/IBM/networking-go-sdk/blob/master/zonesv1/zones_v1.go
func listZones(
	ctx context.Context,
	zv1 *zonesv1.ZonesV1,
	listOpts *zonesv1.ListZonesOptions,
) (*zonesv1.ListZonesResp, *core.DetailedResponse, error) {
	if zv1 == nil {
		return nil, nil, fmt.Errorf("ListZones failed: zv1 cannot be nil")
	}
	return retryWithBackoff(ctx, func(ctx context.Context) (*zonesv1.ListZonesResp, *core.DetailedResponse, error) {
		return zv1.ListZonesWithContext(ctx, listOpts)
	}, "ListZones")
}

// listAllDnsRecords retrieves all DNS records from IBM Cloud Internet Services.
// It automatically retries on transient failures using exponential backoff.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - dnsService: IBM Cloud DNS Records service client
//   - listOpts: Options for listing DNS records
//
// Returns:
//   - *dnsrecordsv1.ListDnsrecordsResp: List of DNS records
//   - *core.DetailedResponse: HTTP response details
//   - error: Any error encountered during the operation
//
// Reference: https://cloud.ibm.com/apidocs/cis#list-all-dns-records
// SDK Reference: https://github.com/IBM/networking-go-sdk/blob/master/dnsrecordsv1/dns_records_v1.go
func listAllDnsRecords(
	ctx context.Context,
	dnsService *dnsrecordsv1.DnsRecordsV1,
	listOpts *dnsrecordsv1.ListAllDnsRecordsOptions,
) (*dnsrecordsv1.ListDnsrecordsResp, *core.DetailedResponse, error) {
	if dnsService == nil {
		return nil, nil, fmt.Errorf("ListAllDnsRecords failed: dnsService cannot be nil")
	}
	return retryWithBackoff(ctx, func(ctx context.Context) (*dnsrecordsv1.ListDnsrecordsResp, *core.DetailedResponse, error) {
		return dnsService.ListAllDnsRecordsWithContext(ctx, listOpts)
	}, "ListAllDnsRecords")
}

// deleteDnsRecord deletes a DNS record from IBM Cloud Internet Services.
// It automatically retries on transient failures using exponential backoff.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - dnsService: IBM Cloud DNS Records service client
//   - deleteOpts: Options specifying which DNS record to delete
//
// Returns:
//   - *dnsrecordsv1.DeleteDnsrecordResp: Deletion response
//   - *core.DetailedResponse: HTTP response details
//   - error: Any error encountered during the operation
//
// Reference: https://cloud.ibm.com/apidocs/cis#delete-dns-record
// SDK Reference: https://github.com/IBM/networking-go-sdk/blob/master/dnsrecordsv1/dns_records_v1.go
func deleteDnsRecord(
	ctx context.Context,
	dnsService *dnsrecordsv1.DnsRecordsV1,
	deleteOpts *dnsrecordsv1.DeleteDnsRecordOptions,
) (*dnsrecordsv1.DeleteDnsrecordResp, *core.DetailedResponse, error) {
	if dnsService == nil {
		return nil, nil, fmt.Errorf("DeleteDnsRecord failed: dnsService cannot be nil")
	}
	return retryWithBackoff(ctx, func(ctx context.Context) (*dnsrecordsv1.DeleteDnsrecordResp, *core.DetailedResponse, error) {
		return dnsService.DeleteDnsRecordWithContext(ctx, deleteOpts)
	}, "DeleteDnsRecord")
}

// createDnsRecord creates a new DNS record in IBM Cloud Internet Services.
// It automatically retries on transient failures using exponential backoff.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - dnsService: IBM Cloud DNS Records service client
//   - createOpts: Options specifying the DNS record to create
//
// Returns:
//   - *dnsrecordsv1.DnsrecordResp: Created DNS record details
//   - *core.DetailedResponse: HTTP response details
//   - error: Any error encountered during the operation
//
// Reference: https://cloud.ibm.com/apidocs/cis#create-dns-record
// SDK Reference: https://github.com/IBM/networking-go-sdk/blob/master/dnsrecordsv1/dns_records_v1.go
func createDnsRecord(
	ctx context.Context,
	dnsService *dnsrecordsv1.DnsRecordsV1,
	createOpts *dnsrecordsv1.CreateDnsRecordOptions,
) (*dnsrecordsv1.DnsrecordResp, *core.DetailedResponse, error) {
	if dnsService == nil {
		return nil, nil, fmt.Errorf("CreateDnsRecord failed: dnsService cannot be nil")
	}
	return retryWithBackoff(ctx, func(ctx context.Context) (*dnsrecordsv1.DnsrecordResp, *core.DetailedResponse, error) {
		return dnsService.CreateDnsRecordWithContext(ctx, createOpts)
	}, "CreateDnsRecord")
}
