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
	"regexp"

	"github.com/IBM/go-sdk-core/v5/core"

	// https://raw.githubusercontent.com/IBM/networking-go-sdk/refs/heads/master/dnsrecordsv1/dns_records_v1.go
	"github.com/IBM/networking-go-sdk/dnsrecordsv1"
	//
	"github.com/IBM/networking-go-sdk/dnssvcsv1"
	// https://raw.githubusercontent.com/IBM/networking-go-sdk/refs/heads/master/zonesv1/zones_v1.go
	"github.com/IBM/networking-go-sdk/zonesv1"
)

// Note: This file uses the global 'log' variable declared in PowerVC-Tool.go

const (
	// IBMDNSName is the display name for IBM Domain Name Service
	IBMDNSName = "IBM Domain Name Service"
)

// IBMDNS manages IBM Cloud DNS services for OpenShift cluster deployment.
// It handles both DNS Services (dnssvcsv1) and DNS Records (dnsrecordsv1) operations.
type IBMDNS struct {
	services *Services

	// dnsSvc provides access to IBM Cloud DNS Services API
	dnsSvc *dnssvcsv1.DnsSvcsV1

	// dnsRecordsSvc provides access to IBM Cloud Internet Services DNS Records API
	dnsRecordsSvc *dnsrecordsv1.DnsRecordsV1
}

// NewIBMDNS creates a new IBMDNS instance and returns it as a RunnableObject.
// This is the primary constructor used by the framework.
//
// Parameters:
//   - services: Services instance containing configuration and API clients
//
// Returns:
//   - []RunnableObject: Array containing the IBMDNS instance as a RunnableObject
//   - []error: Array of errors encountered during initialization
func NewIBMDNS(services *Services) ([]RunnableObject, []error) {
	var (
		dns  []*IBMDNS
		errs []error
		ros  []RunnableObject
	)

	dns, errs = innerNewIBMDNS(services)

	ros = make([]RunnableObject, len(dns))
	// Go does not support type converting the entire array.
	// So we do it manually.
	for i, v := range dns {
		ros[i] = RunnableObject(v)
	}

	return ros, errs
}

// NewIBMDNSAlt creates a new IBMDNS instance and returns it directly.
// This is an alternative constructor that returns the concrete type.
//
// Parameters:
//   - services: Services instance containing configuration and API clients
//
// Returns:
//   - []*IBMDNS: Array containing the IBMDNS instance
//   - []error: Array of errors encountered during initialization
func NewIBMDNSAlt(services *Services) ([]*IBMDNS, []error) {
	return innerNewIBMDNS(services)
}

// innerNewIBMDNS is the internal constructor that initializes IBMDNS services.
// It creates and configures both DNS Services and DNS Records service clients.
//
// Parameters:
//   - services: Services instance containing configuration and API clients
//
// Returns:
//   - []*IBMDNS: Array containing the initialized IBMDNS instance
//   - []error: Array of errors encountered during initialization
func innerNewIBMDNS(services *Services) ([]*IBMDNS, []error) {
	var (
		dns           []*IBMDNS
		errs          []error
		dnsSvc        *dnssvcsv1.DnsSvcsV1
		dnsRecordsSvc *dnsrecordsv1.DnsRecordsV1
		err           error
	)

	dns = make([]*IBMDNS, 1)
	errs = make([]error, 1)

	dnsSvc, dnsRecordsSvc, err = initIBMDNSService(services)
	if err != nil {
		errs[0] = err
		return dns, errs
	}

	dns[0] = &IBMDNS{
		services:      services,
		dnsSvc:        dnsSvc,
		dnsRecordsSvc: dnsRecordsSvc,
	}

	return dns, errs
}

// createAuthenticator creates and validates an IAM authenticator for IBM Cloud services.
// This helper function eliminates code duplication across service initialization.
//
// Parameters:
//   - apiKey: IBM Cloud API key for authentication
//
// Returns:
//   - core.Authenticator: Validated IAM authenticator
//   - error: Any error encountered during creation or validation
func createAuthenticator(apiKey string) (core.Authenticator, error) {
	authenticator := &core.IamAuthenticator{
		ApiKey: apiKey,
	}
	if err := authenticator.Validate(); err != nil {
		return nil, fmt.Errorf("failed to validate authenticator: %w", err)
	}
	return authenticator, nil
}

// initIBMDNSService initializes IBM Cloud DNS services for the cluster.
// It sets up both DNS Services (dnssvcsv1) and DNS Records (dnsrecordsv1) clients,
// and discovers the appropriate DNS zone for the cluster's base domain.
//
// The function performs the following steps:
//  1. Creates DNS Services client
//  2. Lists CIS (Cloud Internet Services) instances
//  3. For each CIS instance, lists DNS zones
//  4. Finds the zone matching the cluster's base domain
//  5. Creates DNS Records client for the discovered zone
//
// Parameters:
//   - services: Services instance containing configuration and API clients
//
// Returns:
//   - *dnssvcsv1.DnsSvcsV1: DNS Services client
//   - *dnsrecordsv1.DnsRecordsV1: DNS Records client
//   - error: Any error encountered during initialization
//
// Reference: https://cloud.ibm.com/apidocs/dns-svcs
// Reference: https://cloud.ibm.com/apidocs/cis
func initIBMDNSService(services *Services) (*dnssvcsv1.DnsSvcsV1, *dnsrecordsv1.DnsRecordsV1, error) {
	if services == nil {
		return nil, nil, nil
	}

	apiKey := services.GetApiKey()
	if apiKey == "" {
		return nil, nil, fmt.Errorf("API key is required for DNS service initialization")
	}

	// Initialize DNS Services client
	dnsService, err := initDNSServicesClient(apiKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to initialize DNS Services client: %w", err)
	}

	// Find the DNS zone for the cluster's base domain
	zoneID, err := findDNSZoneID(services)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to find DNS zone: %w", err)
	}

	if zoneID == "" {
		return nil, nil, fmt.Errorf("no DNS zone found for base domain: %s", services.GetBaseDomain())
	}

	log.Debugf("initIBMDNSService: found zoneID = %s", zoneID)

	// Initialize DNS Records client
	dnsRecordService, err := initDNSRecordsClient(apiKey, services.GetCISInstanceCRN(), zoneID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to initialize DNS Records client: %w", err)
	}

	log.Debugf("initIBMDNSService: dnsRecordService = %+v", dnsRecordService)

	return dnsService, dnsRecordService, nil
}

// initDNSServicesClient creates and initializes an IBM Cloud DNS Services client.
//
// Parameters:
//   - apiKey: IBM Cloud API key for authentication
//
// Returns:
//   - *dnssvcsv1.DnsSvcsV1: Initialized DNS Services client
//   - error: Any error encountered during initialization
func initDNSServicesClient(apiKey string) (*dnssvcsv1.DnsSvcsV1, error) {
	authenticator, err := createAuthenticator(apiKey)
	if err != nil {
		return nil, err
	}

	dnsService, err := dnssvcsv1.NewDnsSvcsV1(&dnssvcsv1.DnsSvcsV1Options{
		Authenticator: authenticator,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create DNS Services client: %w", err)
	}

	return dnsService, nil
}

// initDNSRecordsClient creates and initializes an IBM Cloud DNS Records client.
//
// Parameters:
//   - apiKey: IBM Cloud API key for authentication
//   - crn: Cloud Resource Name of the CIS instance
//   - zoneID: DNS zone identifier
//
// Returns:
//   - *dnsrecordsv1.DnsRecordsV1: Initialized DNS Records client
//   - error: Any error encountered during initialization
func initDNSRecordsClient(apiKey, crn, zoneID string) (*dnsrecordsv1.DnsRecordsV1, error) {
	authenticator, err := createAuthenticator(apiKey)
	if err != nil {
		return nil, err
	}

	globalOptions := &dnsrecordsv1.DnsRecordsV1Options{
		Authenticator:  authenticator,
		Crn:            &crn,
		ZoneIdentifier: &zoneID,
	}

	dnsRecordService, err := dnsrecordsv1.NewDnsRecordsV1(globalOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to create DNS Records client: %w", err)
	}

	return dnsRecordService, nil
}

// findDNSZoneID discovers the DNS zone ID for the cluster's base domain.
// It searches through all CIS instances and their zones to find a match.
//
// Parameters:
//   - services: Services instance containing configuration and API clients
//
// Returns:
//   - string: DNS zone ID if found, empty string otherwise
//   - error: Any error encountered during the search
func findDNSZoneID(services *Services) (string, error) {
	controllerSvc := services.GetControllerSvc()
	if controllerSvc == nil {
		return "", fmt.Errorf("resource controller service is not initialized")
	}

	// List CIS instances
	listResourceOptions := controllerSvc.NewListResourceInstancesOptions()
	listResourceOptions.SetResourceID(cisServiceID)

	listResourceInstancesResponse, _, err := controllerSvc.ListResourceInstances(listResourceOptions)
	if err != nil {
		return "", fmt.Errorf("failed to list CIS instances: %w", err)
	}

	baseDomain := services.GetBaseDomain()
	apiKey := services.GetApiKey()

	// Search through CIS instances for the matching zone
	for _, instance := range listResourceInstancesResponse.Resources {
		log.Debugf("findDNSZoneID: checking instance.CRN = %s", *instance.CRN)

		zoneID, err := searchZonesInInstance(apiKey, instance.CRN, baseDomain)
		if err != nil {
			log.Debugf("findDNSZoneID: error searching zones in instance: %v", err)
			continue
		}

		if zoneID != "" {
			return zoneID, nil
		}
	}

	return "", nil
}

// searchZonesInInstance searches for a DNS zone matching the base domain in a CIS instance.
//
// Parameters:
//   - apiKey: IBM Cloud API key for authentication
//   - crn: Cloud Resource Name of the CIS instance
//   - baseDomain: Base domain to search for
//
// Returns:
//   - string: DNS zone ID if found, empty string otherwise
//   - error: Any error encountered during the search
func searchZonesInInstance(apiKey string, crn *string, baseDomain string) (string, error) {
	authenticator, err := createAuthenticator(apiKey)
	if err != nil {
		return "", err
	}

	zonesService, err := zonesv1.NewZonesV1(&zonesv1.ZonesV1Options{
		Authenticator: authenticator,
		Crn:           crn,
	})
	if err != nil {
		return "", fmt.Errorf("failed to create zones service: %w", err)
	}

	log.Debugf("searchZonesInInstance: zonesService = %+v", zonesService)

	listZonesOptions := zonesService.NewListZonesOptions()
	listZonesResponse, _, err := zonesService.ListZones(listZonesOptions)
	if err != nil {
		return "", fmt.Errorf("failed to list zones: %w", err)
	}

	for _, zone := range listZonesResponse.Result {
		log.Debugf("searchZonesInInstance: zone.Name = %s, zone.ID = %s", *zone.Name, *zone.ID)

		if *zone.Name == baseDomain {
			return *zone.ID, nil
		}
	}

	return "", nil
}

// listIBMDNSRecords lists DNS records for the cluster from IBM Cloud Internet Services.
// It searches for DNS records matching the cluster's domain pattern and returns their names.
//
// The function performs paginated queries to retrieve all DNS records and filters them
// based on the cluster name and base domain. It matches records where either the name
// or content matches the cluster's domain pattern.
//
// Returns:
//   - []string: List of DNS record names matching the cluster
//   - error: Any error encountered during the operation
func (dns *IBMDNS) listIBMDNSRecords() ([]string, error) {
	if dns == nil || dns.services == nil {
		return []string{}, nil
	}

	if dns.dnsRecordsSvc == nil {
		return nil, fmt.Errorf("DNS records service is not initialized")
	}

	metadata := dns.services.GetMetadata()
	if metadata == nil {
		return nil, fmt.Errorf("metadata is not available")
	}

	ctx, cancel := dns.services.GetContextWithTimeout()
	defer cancel()

	log.Debugf("listIBMDNSRecords: Listing DNS records for cluster %s", metadata.GetClusterName())

	// Build regex matcher for cluster DNS records
	dnsMatcher, err := regexp.Compile(fmt.Sprintf(`.*\Q%s.%s\E$`, metadata.GetClusterName(), dns.services.GetBaseDomain()))
	if err != nil {
		return nil, fmt.Errorf("failed to build DNS records matcher: %w", err)
	}

	result, err := dns.fetchMatchingDNSRecords(ctx, dnsMatcher)
	if err != nil {
		return nil, err
	}

	if len(result) == 0 {
		log.Debugf("listIBMDNSRecords: No matching DNS records found for cluster %s", metadata.GetClusterName())
		// Log all available records for debugging
		if err := dns.logAllDNSRecords(ctx); err != nil {
			log.Debugf("listIBMDNSRecords: Failed to log all DNS records: %v", err)
		}
	}

	return result, nil
}

// fetchMatchingDNSRecords retrieves DNS records matching the provided pattern.
// It handles pagination automatically and uses retry logic from IBMCloud.go.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - matcher: Compiled regex pattern to match DNS records
//
// Returns:
//   - []string: List of matching DNS record names
//   - error: Any error encountered during the operation
func (dns *IBMDNS) fetchMatchingDNSRecords(ctx context.Context, matcher *regexp.Regexp) ([]string, error) {
	var (
		result  = make([]string, 0, 3)
		perPage int64 = 20
		page    int64 = 1
	)

	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("context cancelled while fetching DNS records: %w", ctx.Err())
		default:
		}

		dnsRecordsOptions := dns.dnsRecordsSvc.NewListAllDnsRecordsOptions()
		dnsRecordsOptions.PerPage = &perPage
		dnsRecordsOptions.Page = &page

		// Use retry logic from IBMCloud.go
		dnsResources, detailedResponse, err := listAllDnsRecords(ctx, dns.dnsRecordsSvc, dnsRecordsOptions)
		if err != nil {
			return nil, fmt.Errorf("failed to list DNS records (page %d): %w, response: %v", page, err, detailedResponse)
		}

		// Process records on this page
		for _, record := range dnsResources.Result {
			if record.Name == nil || record.Content == nil {
				continue
			}

			nameMatches := matcher.Match([]byte(*record.Name))
			contentMatches := matcher.Match([]byte(*record.Content))

			if nameMatches || contentMatches {
				log.Debugf("listIBMDNSRecords: Found matching record: ID=%v, Name=%v", *record.ID, *record.Name)
				result = append(result, *record.Name)
			}
		}

		log.Debugf("listIBMDNSRecords: Page %d: PerPage=%v, Count=%v",
			page, *dnsResources.ResultInfo.PerPage, *dnsResources.ResultInfo.Count)

		// Check if there are more pages
		if *dnsResources.ResultInfo.PerPage != *dnsResources.ResultInfo.Count {
			break
		}

		page++
	}

	return result, nil
}

// logAllDNSRecords logs all available DNS records for debugging purposes.
// This is called when no matching records are found to help troubleshoot issues.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//
// Returns:
//   - error: Any error encountered during the operation
func (dns *IBMDNS) logAllDNSRecords(ctx context.Context) error {
	var (
		perPage int64 = 20
		page    int64 = 1
	)

	log.Debugf("logAllDNSRecords: Listing all DNS records for debugging")

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		dnsRecordsOptions := dns.dnsRecordsSvc.NewListAllDnsRecordsOptions()
		dnsRecordsOptions.PerPage = &perPage
		dnsRecordsOptions.Page = &page

		dnsResources, _, err := listAllDnsRecords(ctx, dns.dnsRecordsSvc, dnsRecordsOptions)
		if err != nil {
			return fmt.Errorf("failed to list DNS records for debugging: %w", err)
		}

		for _, record := range dnsResources.Result {
			if record.ID != nil && record.Name != nil {
				log.Debugf("logAllDNSRecords: Record: ID=%v, Name=%v", *record.ID, *record.Name)
			}
		}

		if *dnsResources.ResultInfo.PerPage != *dnsResources.ResultInfo.Count {
			break
		}

		page++
	}

	return nil
}

// Name returns the display name of the DNS service.
// This implements the RunnableObject interface.
//
// Returns:
//   - string: The service name (IBMDNSName)
//   - error: Always nil for this implementation
func (dns *IBMDNS) Name() (string, error) {
	return IBMDNSName, nil
}

// ObjectName returns the object name of the DNS service.
// This implements the RunnableObject interface.
//
// Returns:
//   - string: The service name (IBMDNSName)
//   - error: Always nil for this implementation
func (dns *IBMDNS) ObjectName() (string, error) {
	return IBMDNSName, nil
}

// Run executes the DNS service operations.
// This implements the RunnableObject interface.
// Currently, no operations are performed during the run phase.
//
// Returns:
//   - error: Always nil for this implementation
func (dns *IBMDNS) Run() error {
	// Nothing needs to be done here.
	return nil
}

// ClusterStatus validates the DNS configuration for the OpenShift cluster.
// It checks that all required DNS records exist for the cluster:
//   - api-int.<cluster>.<domain> - Internal API endpoint
//   - api.<cluster>.<domain> - External API endpoint
//   - *.apps.<cluster>.<domain> - Wildcard for application routes
//
// The function prints the validation status to stdout and logs details to the debug log.
// This implements the RunnableObject interface.
func (dns *IBMDNS) ClusterStatus() {
	fmt.Println("8<--------8<--------8<--------8<--------8<--------8<--------8<--------8<--------")

	if dns == nil || dns.services == nil {
		fmt.Printf("%s is NOTOK. It has not been initialized.\n", IBMDNSName)
		return
	}

	metadata := dns.services.GetMetadata()
	if metadata == nil {
		fmt.Printf("%s is NOTOK. Metadata is not available.\n", IBMDNSName)
		return
	}

	records, err := dns.listIBMDNSRecords()
	if err != nil {
		fmt.Printf("%s is NOTOK. Could not list DNS records: %v\n", IBMDNSName, err)
		return
	}
	log.Debugf("ClusterStatus: records = %+v", records)

	// Validate that exactly 3 DNS records exist
	const expectedRecordCount = 3
	if len(records) != expectedRecordCount {
		fmt.Printf("%s is NOTOK. Expecting %d DNS records, found %d (%+v)\n",
			IBMDNSName, expectedRecordCount, len(records), records)
		return
	}

	// Validate each required DNS record pattern
	patterns := []string{"api-int", "api", "*.apps"}
	for _, pattern := range patterns {
		name := fmt.Sprintf("%s.%s.%s", pattern, metadata.GetClusterName(), dns.services.GetBaseDomain())
		log.Debugf("ClusterStatus: checking for record: %s", name)

		found := false
		for _, record := range records {
			if record == name {
				found = true
				break
			}
		}

		if !found {
			fmt.Printf("%s is NOTOK. Expected DNS record %s does not exist\n", IBMDNSName, name)
			return
		}

		// TODO: Consider adding DNS lookup validation to verify record resolution
	}

	fmt.Printf("%s is OK.\n", IBMDNSName)
}

// Priority returns the execution priority for this service.
// This implements the RunnableObject interface.
// A priority of -1 indicates this service has no specific ordering requirement.
//
// Returns:
//   - int: Priority value (-1 for no specific priority)
//   - error: Always nil for this implementation
func (dns *IBMDNS) Priority() (int, error) {
	return -1, nil
}
