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
// and the 'cisServiceID' constant defined in Services.go

const (
	// IBMDNSName is the display name for IBM Domain Name Service
	IBMDNSName = "IBM Domain Name Service"

	// expectedDNSRecordCount is the number of DNS records expected for a valid cluster
	// - api-int.<cluster>.<domain> - Internal API endpoint
	// - api.<cluster>.<domain> - External API endpoint
	// - *.apps.<cluster>.<domain> - Wildcard for application routes
	expectedDNSRecordCount = 3

	// Pagination constants for DNS record queries
	defaultPerPage int64 = 20
	defaultPage    int64 = 1
)

// dnsRecordPattern represents a required DNS record pattern for cluster validation
type dnsRecordPattern struct {
	pattern     string
	description string
}

// requiredDNSPatterns defines the DNS records required for a valid OpenShift cluster
var requiredDNSPatterns = []dnsRecordPattern{
	{"api-int", "Internal API endpoint"},
	{"api", "External API endpoint"},
	{"*.apps", "Application routes wildcard"},
}

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
	if apiKey == "" {
		return nil, fmt.Errorf("API key cannot be empty")
	}
	if crn == "" {
		return nil, fmt.Errorf("CRN cannot be empty")
	}
	if zoneID == "" {
		return nil, fmt.Errorf("zone ID cannot be empty")
	}

	authenticator, err := createAuthenticator(apiKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create authenticator for DNS Records client: %w", err)
	}

	globalOptions := &dnsrecordsv1.DnsRecordsV1Options{
		Authenticator:  authenticator,
		Crn:            &crn,
		ZoneIdentifier: &zoneID,
	}

	dnsRecordService, err := dnsrecordsv1.NewDnsRecordsV1(globalOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to create DNS Records client for zone %s: %w", zoneID, err)
	}

	log.Debugf("initDNSRecordsClient: Successfully initialized DNS Records client for zone %s", zoneID)
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

	baseDomain := services.GetBaseDomain()
	if baseDomain == "" {
		return "", fmt.Errorf("base domain is empty, cannot search for DNS zone")
	}

	apiKey := services.GetApiKey()
	if apiKey == "" {
		return "", fmt.Errorf("API key is empty, cannot search for DNS zone")
	}

	log.Debugf("findDNSZoneID: Searching for DNS zone matching base domain: %s", baseDomain)

	// List CIS instances
	listResourceOptions := controllerSvc.NewListResourceInstancesOptions()
	listResourceOptions.SetResourceID(cisServiceID)

	listResourceInstancesResponse, _, err := controllerSvc.ListResourceInstances(listResourceOptions)
	if err != nil {
		return "", fmt.Errorf("failed to list CIS instances: %w", err)
	}

	if listResourceInstancesResponse == nil || len(listResourceInstancesResponse.Resources) == 0 {
		log.Debugf("findDNSZoneID: No CIS instances found")
		return "", nil
	}

	log.Debugf("findDNSZoneID: Found %d CIS instance(s) to search", len(listResourceInstancesResponse.Resources))

	// Search through CIS instances for the matching zone
	for i, instance := range listResourceInstancesResponse.Resources {
		if instance.CRN == nil {
			log.Debugf("findDNSZoneID: Skipping instance %d with nil CRN", i)
			continue
		}

		log.Debugf("findDNSZoneID: Checking instance %d/%d, CRN = %s",
			i+1, len(listResourceInstancesResponse.Resources), *instance.CRN)

		zoneID, err := searchZonesInInstance(apiKey, instance.CRN, baseDomain)
		if err != nil {
			log.Debugf("findDNSZoneID: Error searching zones in instance %s: %v", *instance.CRN, err)
			continue
		}

		if zoneID != "" {
			log.Debugf("findDNSZoneID: Found matching zone ID: %s in instance: %s", zoneID, *instance.CRN)
			return zoneID, nil
		}
	}

	log.Debugf("findDNSZoneID: No matching zone found for base domain: %s", baseDomain)
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
	if crn == nil {
		return "", fmt.Errorf("CRN cannot be nil")
	}
	if baseDomain == "" {
		return "", fmt.Errorf("base domain cannot be empty")
	}

	authenticator, err := createAuthenticator(apiKey)
	if err != nil {
		return "", fmt.Errorf("failed to create authenticator for zones service: %w", err)
	}

	zonesService, err := zonesv1.NewZonesV1(&zonesv1.ZonesV1Options{
		Authenticator: authenticator,
		Crn:           crn,
	})
	if err != nil {
		return "", fmt.Errorf("failed to create zones service for CRN %s: %w", *crn, err)
	}

	log.Debugf("searchZonesInInstance: Searching zones in CRN: %s for domain: %s", *crn, baseDomain)

	listZonesOptions := zonesService.NewListZonesOptions()
	listZonesResponse, _, err := zonesService.ListZones(listZonesOptions)
	if err != nil {
		return "", fmt.Errorf("failed to list zones in CRN %s: %w", *crn, err)
	}

	if listZonesResponse == nil || len(listZonesResponse.Result) == 0 {
		log.Debugf("searchZonesInInstance: No zones found in CRN: %s", *crn)
		return "", nil
	}

	log.Debugf("searchZonesInInstance: Found %d zone(s) in CRN: %s", len(listZonesResponse.Result), *crn)

	for i, zone := range listZonesResponse.Result {
		if zone.Name == nil || zone.ID == nil {
			log.Debugf("searchZonesInInstance: Skipping zone %d with nil Name or ID", i)
			continue
		}

		log.Debugf("searchZonesInInstance: Checking zone %d/%d: Name=%s, ID=%s",
			i+1, len(listZonesResponse.Result), *zone.Name, *zone.ID)

		if *zone.Name == baseDomain {
			log.Debugf("searchZonesInInstance: Found matching zone: %s (ID: %s)", *zone.Name, *zone.ID)
			return *zone.ID, nil
		}
	}

	log.Debugf("searchZonesInInstance: No matching zone found for domain: %s in CRN: %s", baseDomain, *crn)
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
//
// Example:
//   records, err := dns.listIBMDNSRecords()
//   if err != nil {
//       return fmt.Errorf("failed to list DNS records: %w", err)
//   }
//   // records contains: ["api.cluster.domain.com", "api-int.cluster.domain.com", "*.apps.cluster.domain.com"]
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

	clusterName := metadata.GetClusterName()
	if clusterName == "" {
		return nil, fmt.Errorf("cluster name is empty in metadata")
	}

	baseDomain := dns.services.GetBaseDomain()
	if baseDomain == "" {
		return nil, fmt.Errorf("base domain is empty in services configuration")
	}

	ctx, cancel := dns.services.GetContextWithTimeout()
	defer cancel()

	log.Debugf("listIBMDNSRecords: Listing DNS records for cluster %s.%s", clusterName, baseDomain)

	// Build regex matcher for cluster DNS records
	// Pattern matches: *.cluster.domain.com
	pattern := fmt.Sprintf(`.*\Q%s.%s\E$`, clusterName, baseDomain)
	dnsMatcher, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("failed to compile DNS records matcher pattern %q: %w", pattern, err)
	}

	result, err := dns.fetchMatchingDNSRecords(ctx, dnsMatcher)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch matching DNS records for cluster %s: %w", clusterName, err)
	}

	if len(result) == 0 {
		log.Debugf("listIBMDNSRecords: No matching DNS records found for cluster %s.%s", clusterName, baseDomain)
		// Log all available records for debugging
		if debugErr := dns.logAllDNSRecords(ctx); debugErr != nil {
			log.Debugf("listIBMDNSRecords: Failed to log all DNS records: %v", debugErr)
		}
	} else {
		log.Debugf("listIBMDNSRecords: Found %d matching DNS records", len(result))
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
	if matcher == nil {
		return nil, fmt.Errorf("matcher cannot be nil")
	}

	var (
		result  = make([]string, 0, expectedDNSRecordCount)
		perPage = defaultPerPage
		page    = defaultPage
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

		if dnsResources == nil || dnsResources.ResultInfo == nil {
			return nil, fmt.Errorf("received nil DNS resources or result info on page %d", page)
		}

		// Process records on this page
		recordsProcessed := 0
		for _, record := range dnsResources.Result {
			if record.Name == nil || record.Content == nil {
				log.Debugf("fetchMatchingDNSRecords: Skipping record with nil Name or Content on page %d", page)
				continue
			}

			nameMatches := matcher.Match([]byte(*record.Name))
			contentMatches := matcher.Match([]byte(*record.Content))

			if nameMatches || contentMatches {
				log.Debugf("fetchMatchingDNSRecords: Found matching record: ID=%v, Name=%v, Content=%v",
					*record.ID, *record.Name, *record.Content)
				result = append(result, *record.Name)
			}
			recordsProcessed++
		}

		log.Debugf("fetchMatchingDNSRecords: Page %d: Processed=%d, PerPage=%v, Count=%v",
			page, recordsProcessed, *dnsResources.ResultInfo.PerPage, *dnsResources.ResultInfo.Count)

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
		perPage      = defaultPerPage
		page         = defaultPage
		totalRecords int = 0
	)

	log.Debugf("logAllDNSRecords: Listing all DNS records for debugging")

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled while logging DNS records: %w", ctx.Err())
		default:
		}

		dnsRecordsOptions := dns.dnsRecordsSvc.NewListAllDnsRecordsOptions()
		dnsRecordsOptions.PerPage = &perPage
		dnsRecordsOptions.Page = &page

		dnsResources, _, err := listAllDnsRecords(ctx, dns.dnsRecordsSvc, dnsRecordsOptions)
		if err != nil {
			return fmt.Errorf("failed to list DNS records for debugging (page %d): %w", page, err)
		}

		if dnsResources == nil || dnsResources.ResultInfo == nil {
			return fmt.Errorf("received nil DNS resources or result info on page %d", page)
		}

		for _, record := range dnsResources.Result {
			if record.ID != nil && record.Name != nil {
				log.Debugf("logAllDNSRecords: Record: ID=%v, Name=%v, Type=%v",
					*record.ID, *record.Name, getRecordType(record))
				totalRecords++
			}
		}

		if *dnsResources.ResultInfo.PerPage != *dnsResources.ResultInfo.Count {
			break
		}

		page++
	}

	log.Debugf("logAllDNSRecords: Total records logged: %d", totalRecords)
	return nil
}

// getRecordType safely extracts the record type from a DNS record.
// Returns "unknown" if the type is nil.
func getRecordType(record dnsrecordsv1.DnsrecordDetails) string {
	if record.Type != nil {
		return *record.Type
	}
	return "unknown"
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
//
// Example output:
//   IBM Domain Name Service is OK.
//   IBM Domain Name Service is NOTOK. Expected DNS record api.cluster.domain.com does not exist
func (dns *IBMDNS) ClusterStatus() {
	fmt.Println("8<--------8<--------8<--------8<--------8<--------8<--------8<--------8<--------")

	if dns == nil || dns.services == nil {
		fmt.Printf("%s is NOTOK. It has not been initialized.\n", IBMDNSName)
		log.Debugf("ClusterStatus: DNS service or services is nil")
		return
	}

	metadata := dns.services.GetMetadata()
	if metadata == nil {
		fmt.Printf("%s is NOTOK. Metadata is not available.\n", IBMDNSName)
		log.Debugf("ClusterStatus: Metadata is nil")
		return
	}

	clusterName := metadata.GetClusterName()
	if clusterName == "" {
		fmt.Printf("%s is NOTOK. Cluster name is empty.\n", IBMDNSName)
		log.Debugf("ClusterStatus: Cluster name is empty")
		return
	}

	baseDomain := dns.services.GetBaseDomain()
	if baseDomain == "" {
		fmt.Printf("%s is NOTOK. Base domain is empty.\n", IBMDNSName)
		log.Debugf("ClusterStatus: Base domain is empty")
		return
	}

	records, err := dns.listIBMDNSRecords()
	if err != nil {
		fmt.Printf("%s is NOTOK. Could not list DNS records: %v\n", IBMDNSName, err)
		log.Debugf("ClusterStatus: Failed to list DNS records: %v", err)
		return
	}
	log.Debugf("ClusterStatus: Found %d DNS records: %+v", len(records), records)

	// Validate that exactly the expected number of DNS records exist
	if len(records) != expectedDNSRecordCount {
		fmt.Printf("%s is NOTOK. Expecting %d DNS records, found %d (%+v)\n",
			IBMDNSName, expectedDNSRecordCount, len(records), records)
		log.Debugf("ClusterStatus: Record count mismatch - expected %d, got %d",
			expectedDNSRecordCount, len(records))
		return
	}

	// Validate each required DNS record pattern
	for _, req := range requiredDNSPatterns {
		recordName := fmt.Sprintf("%s.%s.%s", req.pattern, clusterName, baseDomain)
		log.Debugf("ClusterStatus: Checking for %s record: %s", req.description, recordName)

		found := false
		for _, record := range records {
			if record == recordName {
				found = true
				log.Debugf("ClusterStatus: Found required record: %s", recordName)
				break
			}
		}

		if !found {
			fmt.Printf("%s is NOTOK. Expected DNS record %s (%s) does not exist\n",
				IBMDNSName, recordName, req.description)
			log.Debugf("ClusterStatus: Missing required record: %s (%s)", recordName, req.description)
			return
		}

		// TODO: Consider adding DNS lookup validation to verify record resolution
		// This would involve using net.LookupHost() or similar to verify the records
		// actually resolve to the expected IP addresses
	}

	fmt.Printf("%s is OK.\n", IBMDNSName)
	log.Debugf("ClusterStatus: All DNS records validated successfully for cluster %s.%s",
		clusterName, baseDomain)
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
