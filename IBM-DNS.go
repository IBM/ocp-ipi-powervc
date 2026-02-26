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

	"github.com/IBM/platform-services-go-sdk/resourcecontrollerv2"
)

const (
	IBMDNSName = "IBM Domain Name Service"
)

type IBMDNS struct {
	services *Services

	//
	dnsSvc *dnssvcsv1.DnsSvcsV1

	//
	dnsRecordsSvc *dnsrecordsv1.DnsRecordsV1
}

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

func NewIBMDNSAlt(services *Services) ([]*IBMDNS, []error) {
	return innerNewIBMDNS(services)
}

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

func initIBMDNSService(services *Services) (*dnssvcsv1.DnsSvcsV1, *dnsrecordsv1.DnsRecordsV1, error) {
	var (
		authenticator       core.Authenticator
		dnsService          *dnssvcsv1.DnsSvcsV1
		globalOptions       *dnsrecordsv1.DnsRecordsV1Options
		controllerSvc       *resourcecontrollerv2.ResourceControllerV2
		listResourceOptions *resourcecontrollerv2.ListResourceInstancesOptions
		dnsRecordService    *dnsrecordsv1.DnsRecordsV1
		zonesService        *zonesv1.ZonesV1
		listZonesOptions    *zonesv1.ListZonesOptions
		listZonesResponse   *zonesv1.ListZonesResp
		zoneID              string
		err                 error
	)

	if services == nil {
		return nil, nil, nil
	}

	authenticator = &core.IamAuthenticator{
		ApiKey: services.GetApiKey(),
	}
	err = authenticator.Validate()
	if err != nil {
		return nil, nil, err
	}

	dnsService, err = dnssvcsv1.NewDnsSvcsV1(&dnssvcsv1.DnsSvcsV1Options{
		Authenticator: authenticator,
	})
	if err != nil {
		return nil, nil, err
	}

	authenticator = &core.IamAuthenticator{
		ApiKey: services.GetApiKey(),
	}
	err = authenticator.Validate()
	if err != nil {
		return nil, nil, err
	}

	controllerSvc = services.GetControllerSvc()

	listResourceOptions = controllerSvc.NewListResourceInstancesOptions()
	listResourceOptions.SetResourceID("75874a60-cb12-11e7-948e-37ac098eb1b9") // CIS service ID

	listResourceInstancesResponse, _, err := controllerSvc.ListResourceInstances(listResourceOptions)
	if err != nil {
		return nil, nil, err
	}

	for _, instance := range listResourceInstancesResponse.Resources {
		log.Debugf("initIBMDNSService: instance.CRN = %s", *instance.CRN)

		authenticator = &core.IamAuthenticator{
			ApiKey: services.GetApiKey(),
		}
		err = authenticator.Validate()
		if err != nil {
			return nil, nil, err
		}

		zonesService, err = zonesv1.NewZonesV1(&zonesv1.ZonesV1Options{
			Authenticator: authenticator,
			Crn:           instance.CRN,
		})
		if err != nil {
			return nil, nil, err
		}
		log.Debugf("initIBMDNSService: zonesService = %+v", zonesService)

		listZonesOptions = zonesService.NewListZonesOptions()

		listZonesResponse, _, err = zonesService.ListZones(listZonesOptions)
		if err != nil {
			return nil, nil, err
		}

		for _, zone := range listZonesResponse.Result {
			log.Debugf("initIBMDNSService: zone.Name = %s", *zone.Name)
			log.Debugf("initIBMDNSService: zone.ID   = %s", *zone.ID)

			if *zone.Name == services.GetBaseDomain() {
				zoneID = *zone.ID
			}
		}
	}
	log.Debugf("initIBMDNSService: zoneID = %s", zoneID)

	authenticator = &core.IamAuthenticator{
		ApiKey: services.GetApiKey(),
	}
	err = authenticator.Validate()
	if err != nil {
		return nil, nil, err
	}

	CRN := services.GetCISInstanceCRN()

	globalOptions = &dnsrecordsv1.DnsRecordsV1Options{
		Authenticator:  authenticator,
		Crn:            &CRN,
		ZoneIdentifier: &zoneID,
	}
	dnsRecordService, err = dnsrecordsv1.NewDnsRecordsV1(globalOptions)
	log.Debugf("initIBMDNSService: dnsRecordService = %+v", dnsRecordService)

	return dnsService, dnsRecordService, err
}

// listDNSRecords lists IBMDNS records for the cluster.
func (dns *IBMDNS) listIBMDNSRecords() ([]string, error) {
	var (
		metadata *Metadata
		ctx      context.Context
		cancel   context.CancelFunc
		result   []string
	)

	log.Debugf("listIBMDNSRecords: Listing IBMDNS records (%v)", dns.services)

	result = make([]string, 0, 3)

	if dns.services == nil {
		return result, nil
	}

	metadata = dns.services.GetMetadata()

	ctx, cancel = dns.services.GetContextWithTimeout()
	defer cancel()

	select {
	case <-ctx.Done():
		log.Debugf("listIBMDNSRecords: case <-ctx.Done()")
		return nil, ctx.Err() // we're cancelled, abort
	default:
	}

	var (
		foundOne       = false
		perPage  int64 = 20
		page     int64 = 1
		moreData       = true
	)

	dnsRecordsOptions := dns.dnsRecordsSvc.NewListAllDnsRecordsOptions()
	dnsRecordsOptions.PerPage = &perPage
	dnsRecordsOptions.Page = &page

	dnsMatcher, err := regexp.Compile(fmt.Sprintf(`.*\Q%s.%s\E$`, metadata.GetClusterName(), dns.services.GetBaseDomain()))
	if err != nil {
		return nil, fmt.Errorf("failed to build IBMDNS records matcher: %w", err)
	}

	for moreData {
		select {
		case <-ctx.Done():
			log.Debugf("listIBMDNSRecords: case <-ctx.Done()")
			return nil, ctx.Err() // we're cancelled, abort
		default:
		}

		dnsResources, detailedResponse, err := dns.dnsRecordsSvc.ListAllDnsRecordsWithContext(ctx, dnsRecordsOptions)
		if err != nil {
			return nil, fmt.Errorf("failed to list IBMDNS records: %w and the response is: %s", err, detailedResponse)
		}

		for _, record := range dnsResources.Result {
			// Match all of the cluster's IBMDNS records
			nameMatches := dnsMatcher.Match([]byte(*record.Name))
			contentMatches := dnsMatcher.Match([]byte(*record.Content))
			if nameMatches || contentMatches {
				foundOne = true
				log.Debugf("listIBMDNSRecords: FOUND: %v, %v", *record.ID, *record.Name)
				result = append(result, *record.Name)
			}
		}

		log.Debugf("listIBMDNSRecords: PerPage = %v, Page = %v, Count = %v", *dnsResources.ResultInfo.PerPage, *dnsResources.ResultInfo.Page, *dnsResources.ResultInfo.Count)

		moreData = *dnsResources.ResultInfo.PerPage == *dnsResources.ResultInfo.Count
		log.Debugf("listIBMDNSRecords: moreData = %v", moreData)

		page++
	}
	if !foundOne {
		log.Debugf("listIBMDNSRecords: NO matching IBMDNS against: %s", metadata.GetInfraID())
		for moreData {
			select {
			case <-ctx.Done():
				log.Debugf("listIBMDNSRecords: case <-ctx.Done()")
				return nil, ctx.Err() // we're cancelled, abort
			default:
			}

			dnsResources, detailedResponse, err := dns.dnsRecordsSvc.ListAllDnsRecordsWithContext(ctx, dnsRecordsOptions)
			if err != nil {
				return nil, fmt.Errorf("failed to list IBMDNS records: %w and the response is: %s", err, detailedResponse)
			}
			for _, record := range dnsResources.Result {
				log.Debugf("listIBMDNSRecords: FOUND: IBMDNS: %v, %v", *record.ID, *record.Name)
			}
			moreData = *dnsResources.ResultInfo.PerPage == *dnsResources.ResultInfo.Count
			page++
		}
	}

	return result, nil
}

func (dns *IBMDNS) Name() (string, error) {
	return IBMDNSName, nil
}

func (dns *IBMDNS) ObjectName() (string, error) {
	return IBMDNSName, nil
}

func (dns *IBMDNS) Run() error {
	// Nothing needs to be done here.
	return nil
}

func (dns *IBMDNS) ClusterStatus() {
	var (
		metadata *Metadata
		records  []string
		patterns = []string{"api-int", "api", "*.apps"}
		name     string
		found    bool
		err      error
	)

	fmt.Println("8<--------8<--------8<--------8<--------8<--------8<--------8<--------8<--------")

	if dns.services == nil {
		fmt.Printf("%s is NOTOK. It has not been initialized.\n", IBMDNSName)
		return
	}

	metadata = dns.services.GetMetadata()

	records, err = dns.listIBMDNSRecords()
	if err != nil {
		fmt.Printf("%s is NOTOK. Could not list IBMDNS records: %v\n", IBMDNSName, err)
		return
	}
	log.Debugf("Valid: records = %+v", records)

	if len(records) != 3 {
		fmt.Printf("%s is NOTOK. Expecting 3 IBMDNS records, found %d (%+v)\n", IBMDNSName, len(records), records)
		return
	}

	for _, pattern := range patterns {
		name = fmt.Sprintf("%s.%s.%s", pattern, metadata.GetClusterName(), dns.services.GetBaseDomain())
		log.Debugf("Valid: name = %s", name)

		found = false
		for _, record := range records {
			if record == name {
				found = true
			}
		}
		if !found {
			fmt.Printf("%s is NOTOK. Expecting IBMDNS record %s to exist\n", IBMDNSName, name)
			return
		}

		// @TODO maybe do a IBMDNS lookup on the name?
	}

	fmt.Printf("%s is OK.\n", IBMDNSName)
}

func (dns *IBMDNS) Priority() (int, error) {
	return -1, nil
}
