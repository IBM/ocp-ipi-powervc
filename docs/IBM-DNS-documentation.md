# IBM-DNS.go Documentation

**Version:** 1.0  
**Date:** 2026-05-08  
**File:** IBM-DNS.go

---

## Table of Contents

1. [Overview](#overview)
2. [Architecture](#architecture)
3. [Key Components](#key-components)
4. [API Reference](#api-reference)
5. [Usage Examples](#usage-examples)
6. [Configuration](#configuration)
7. [Error Handling](#error-handling)
8. [Troubleshooting](#troubleshooting)
9. [Best Practices](#best-practices)

---

## Overview

### Purpose

The `IBM-DNS.go` module manages IBM Cloud DNS services for OpenShift cluster deployments. It handles DNS record validation, zone discovery, and cluster status verification through IBM Cloud Internet Services (CIS).

### Key Features

- ✅ Automatic DNS zone discovery across CIS instances
- ✅ DNS record validation for OpenShift clusters
- ✅ Support for both DNS Services and DNS Records APIs
- ✅ Paginated record retrieval
- ✅ Comprehensive error handling and logging
- ✅ Context-aware operations with timeout support

### Dependencies

```go
import (
    "github.com/IBM/go-sdk-core/v5/core"
    "github.com/IBM/networking-go-sdk/dnsrecordsv1"
    "github.com/IBM/networking-go-sdk/dnssvcsv1"
    "github.com/IBM/networking-go-sdk/zonesv1"
)
```

---

## Architecture

### Component Diagram

```
┌─────────────────────────────────────────────────────────────┐
│                         IBMDNS                              │
├─────────────────────────────────────────────────────────────┤
│  - services: *Services                                      │
│  - dnsSvc: *dnssvcsv1.DnsSvcsV1                            │
│  - dnsRecordsSvc: *dnsrecordsv1.DnsRecordsV1               │
└─────────────────────────────────────────────────────────────┘
                          │
                          │ uses
                          ▼
┌─────────────────────────────────────────────────────────────┐
│                    IBM Cloud APIs                           │
├─────────────────────────────────────────────────────────────┤
│  • DNS Services API (dnssvcsv1)                            │
│  • DNS Records API (dnsrecordsv1)                          │
│  • Zones API (zonesv1)                                     │
│  • Resource Controller API (via Services)                  │
└─────────────────────────────────────────────────────────────┘
```

### Data Flow

```
1. Initialize IBMDNS
   ↓
2. Create DNS Services Client
   ↓
3. Find DNS Zone ID
   ├─→ List CIS Instances
   ├─→ For each instance, list zones
   └─→ Match zone to base domain
   ↓
4. Create DNS Records Client
   ↓
5. Validate Cluster DNS Records
   ├─→ List all DNS records (paginated)
   ├─→ Filter by cluster pattern
   └─→ Verify required records exist
```

---

## Key Components

### Constants

```go
const (
    // IBMDNSName is the display name for IBM Domain Name Service
    IBMDNSName = "IBM Domain Name Service"

    // expectedDNSRecordCount is the number of DNS records expected
    expectedDNSRecordCount = 3

    // Pagination constants
    defaultPerPage int64 = 20
    defaultPage    int64 = 1
)
```

### Required DNS Records

Every OpenShift cluster requires exactly 3 DNS records:

| Pattern | Description | Example |
|---------|-------------|---------|
| `api-int.<cluster>.<domain>` | Internal API endpoint | `api-int.mycluster.example.com` |
| `api.<cluster>.<domain>` | External API endpoint | `api.mycluster.example.com` |
| `*.apps.<cluster>.<domain>` | Application routes wildcard | `*.apps.mycluster.example.com` |

### IBMDNS Structure

```go
type IBMDNS struct {
    services      *Services                      // Configuration and shared services
    dnsSvc        *dnssvcsv1.DnsSvcsV1          // DNS Services API client
    dnsRecordsSvc *dnsrecordsv1.DnsRecordsV1    // DNS Records API client
}
```

---

## API Reference

### Constructors

#### NewIBMDNS

Creates a new IBMDNS instance and returns it as a RunnableObject.

```go
func NewIBMDNS(services *Services) ([]RunnableObject, []error)
```

**Parameters:**
- `services`: Services instance containing configuration and API clients

**Returns:**
- `[]RunnableObject`: Array containing the IBMDNS instance
- `[]error`: Array of errors encountered during initialization

**Example:**
```go
services := &Services{...}
dnsObjects, errs := NewIBMDNS(services)
if len(errs) > 0 && errs[0] != nil {
    log.Fatalf("Failed to create DNS service: %v", errs[0])
}
dns := dnsObjects[0].(*IBMDNS)
```

#### NewIBMDNSAlt

Alternative constructor that returns the concrete type directly.

```go
func NewIBMDNSAlt(services *Services) ([]*IBMDNS, []error)
```

**Parameters:**
- `services`: Services instance containing configuration and API clients

**Returns:**
- `[]*IBMDNS`: Array containing the IBMDNS instance
- `[]error`: Array of errors encountered during initialization

**Example:**
```go
dnsInstances, errs := NewIBMDNSAlt(services)
if len(errs) > 0 && errs[0] != nil {
    log.Fatalf("Failed to create DNS service: %v", errs[0])
}
dns := dnsInstances[0]
```

---

### Core Methods

#### ClusterStatus

Validates the DNS configuration for the OpenShift cluster.

```go
func (dns *IBMDNS) ClusterStatus() error
```

**Returns:**
- `error`: nil if all DNS records are valid, error otherwise

**Validation Steps:**
1. Checks that services and metadata are initialized
2. Lists all DNS records for the cluster
3. Verifies exactly 3 DNS records exist
4. Validates each required DNS pattern exists

**Example Output:**
```
8<--------8<--------8<--------8<--------8<--------8<--------8<--------8<--------
IBM Domain Name Service is OK.
```

Or on failure:
```
8<--------8<--------8<--------8<--------8<--------8<--------8<--------8<--------
IBM Domain Name Service is NOTOK. Expected DNS record api.cluster.domain.com does not exist
```

**Example:**
```go
if err := dns.ClusterStatus(); err != nil {
    log.Errorf("DNS validation failed: %v", err)
}
```

#### listIBMDNSRecords

Lists DNS records for the cluster from IBM Cloud Internet Services.

```go
func (dns *IBMDNS) listIBMDNSRecords() ([]string, error)
```

**Returns:**
- `[]string`: List of DNS record names matching the cluster
- `error`: Any error encountered during the operation

**Behavior:**
- Performs paginated queries to retrieve all DNS records
- Filters records based on cluster name and base domain
- Matches records where name or content matches the cluster pattern

**Example:**
```go
records, err := dns.listIBMDNSRecords()
if err != nil {
    return fmt.Errorf("failed to list DNS records: %w", err)
}
// records: ["api.cluster.domain.com", "api-int.cluster.domain.com", "*.apps.cluster.domain.com"]
```

---

### RunnableObject Interface Implementation

#### Name

Returns the display name of the DNS service.

```go
func (dns *IBMDNS) Name() (string, error)
```

**Returns:**
- `string`: "IBM Domain Name Service"
- `error`: Always nil

#### ObjectName

Returns the object name of the DNS service.

```go
func (dns *IBMDNS) ObjectName() (string, error)
```

**Returns:**
- `string`: "IBM Domain Name Service"
- `error`: Always nil

#### Run

Executes the DNS service operations.

```go
func (dns *IBMDNS) Run() error
```

**Returns:**
- `error`: Always nil (no operations performed during run phase)

#### Priority

Returns the execution priority for this service.

```go
func (dns *IBMDNS) Priority() (int, error)
```

**Returns:**
- `int`: -1 (no specific priority)
- `error`: Always nil

---

### Internal Functions

#### initIBMDNSService

Initializes IBM Cloud DNS services for the cluster.

```go
func initIBMDNSService(services *Services) (*dnssvcsv1.DnsSvcsV1, *dnsrecordsv1.DnsRecordsV1, error)
```

**Process:**
1. Creates DNS Services client
2. Discovers DNS zone for the cluster's base domain
3. Creates DNS Records client for the discovered zone

**Returns:**
- `*dnssvcsv1.DnsSvcsV1`: DNS Services client
- `*dnsrecordsv1.DnsRecordsV1`: DNS Records client
- `error`: Any error encountered

#### findDNSZoneID

Discovers the DNS zone ID for the cluster's base domain.

```go
func findDNSZoneID(services *Services) (string, error)
```

**Process:**
1. Lists all CIS instances
2. For each instance, lists DNS zones
3. Finds zone matching the base domain

**Returns:**
- `string`: DNS zone ID if found, empty string otherwise
- `error`: Any error encountered

**Note:** ⚠️ Currently has a bug where it doesn't store the discovered CIS instance CRN.

#### searchZonesInInstance

Searches for a DNS zone matching the base domain in a CIS instance.

```go
func searchZonesInInstance(apiKey string, crn *string, baseDomain string) (string, error)
```

**Parameters:**
- `apiKey`: IBM Cloud API key
- `crn`: Cloud Resource Name of the CIS instance
- `baseDomain`: Base domain to search for

**Returns:**
- `string`: DNS zone ID if found, empty string otherwise
- `error`: Any error encountered

---

## Usage Examples

### Basic Initialization

```go
package main

import (
    "log"
)

func main() {
    // Create services instance with configuration
    services := &Services{
        apiKey:     "your-api-key",
        baseDomain: "example.com",
        // ... other configuration
    }

    // Initialize DNS service
    dnsObjects, errs := NewIBMDNS(services)
    if len(errs) > 0 && errs[0] != nil {
        log.Fatalf("Failed to initialize DNS service: %v", errs[0])
    }

    dns := dnsObjects[0].(*IBMDNS)

    // Validate cluster DNS
    if err := dns.ClusterStatus(); err != nil {
        log.Fatalf("DNS validation failed: %v", err)
    }

    log.Println("DNS validation successful!")
}
```

### Listing DNS Records

```go
func listClusterDNS(dns *IBMDNS) error {
    records, err := dns.listIBMDNSRecords()
    if err != nil {
        return fmt.Errorf("failed to list DNS records: %w", err)
    }

    fmt.Printf("Found %d DNS records:\n", len(records))
    for i, record := range records {
        fmt.Printf("%d. %s\n", i+1, record)
    }

    return nil
}
```

### Integration with RunnableObject Framework

```go
func runDNSValidation(services *Services) error {
    // Create DNS service
    dnsObjects, errs := NewIBMDNS(services)
    if len(errs) > 0 && errs[0] != nil {
        return fmt.Errorf("initialization failed: %w", errs[0])
    }

    dns := dnsObjects[0]

    // Get service name
    name, _ := dns.Name()
    fmt.Printf("Running %s...\n", name)

    // Execute run phase (no-op for DNS)
    if err := dns.Run(); err != nil {
        return fmt.Errorf("run failed: %w", err)
    }

    // Validate cluster status
    if err := dns.ClusterStatus(); err != nil {
        return fmt.Errorf("cluster status check failed: %w", err)
    }

    return nil
}
```

---

## Configuration

### Required Environment Variables

The DNS service requires the following configuration through the Services object:

| Variable | Type | Description | Example |
|----------|------|-------------|---------|
| API Key | string | IBM Cloud API key | `your-api-key-here` |
| Base Domain | string | Cluster base domain | `example.com` |
| Cluster Name | string | OpenShift cluster name | `mycluster` |
| CIS Instance CRN | string | Cloud Resource Name of CIS instance | `crn:v1:bluemix:...` |

### Services Object Requirements

```go
type Services struct {
    // Must implement:
    GetApiKey() string
    GetBaseDomain() string
    GetCISInstanceCRN() string
    GetMetadata() *Metadata
    GetControllerSvc() *resourcecontrollerv2.ResourceControllerV2
    GetContextWithTimeout() (context.Context, context.CancelFunc)
}
```

### Metadata Requirements

```go
type Metadata struct {
    // Must implement:
    GetClusterName() string
}
```

---

## Error Handling

### Error Types

The DNS service returns errors in the following categories:

#### Initialization Errors

```go
// API key missing
"API key is required for DNS service initialization"

// Zone not found
"no DNS zone found for base domain: example.com"

// Service creation failed
"failed to create DNS Services client: <error>"
```

#### Validation Errors

```go
// Wrong number of records
"Expecting 3 DNS records, found 2 ([api.cluster.domain.com, api-int.cluster.domain.com])"

// Missing required record
"Expected DNS record api.cluster.domain.com (External API endpoint) does not exist"
```

#### API Errors

```go
// List operation failed
"failed to list DNS records (page 1): <error>, response: <response>"

// Zone search failed
"failed to list zones in CRN <crn>: <error>"
```

### Error Handling Best Practices

```go
// Always check initialization errors
dnsObjects, errs := NewIBMDNS(services)
if len(errs) > 0 && errs[0] != nil {
    // Log the error with context
    log.Errorf("DNS initialization failed: %v", errs[0])
    
    // Decide whether to continue or abort
    return fmt.Errorf("cannot proceed without DNS service: %w", errs[0])
}

// Handle validation errors gracefully
if err := dns.ClusterStatus(); err != nil {
    // Log for debugging
    log.Debugf("DNS validation details: %v", err)
    
    // Provide user-friendly message
    fmt.Println("DNS configuration is incomplete. Please check your DNS records.")
    
    // Return wrapped error for upstream handling
    return fmt.Errorf("DNS validation failed: %w", err)
}
```

---

## Troubleshooting

### Common Issues

#### Issue 1: "No DNS zone found for base domain"

**Symptoms:**
```
failed to find DNS zone: no DNS zone found for base domain: example.com
```

**Causes:**
- Base domain doesn't exist in any CIS instance
- API key lacks permissions to list CIS instances
- CIS instance is in a different account

**Solutions:**
1. Verify base domain exists in IBM Cloud DNS:
   ```bash
   ibmcloud cis zones --instance <instance-name>
   ```

2. Check API key permissions:
   ```bash
   ibmcloud iam api-key-get <api-key-name>
   ```

3. Ensure CIS instance is accessible:
   ```bash
   ibmcloud resource service-instances --service-name internet-svcs
   ```

#### Issue 2: "Expecting 3 DNS records, found X"

**Symptoms:**
```
IBM Domain Name Service is NOTOK. Expecting 3 DNS records, found 1
```

**Causes:**
- Cluster installation incomplete
- DNS records not yet propagated
- Wrong cluster name or base domain

**Solutions:**
1. Wait for cluster installation to complete
2. Check OpenShift installer logs:
   ```bash
   tail -f .openshift_install.log
   ```

3. Verify DNS records manually:
   ```bash
   dig api.mycluster.example.com
   dig api-int.mycluster.example.com
   dig test.apps.mycluster.example.com
   ```

#### Issue 3: "Context cancelled while fetching DNS records"

**Symptoms:**
```
context cancelled while fetching DNS records: context deadline exceeded
```

**Causes:**
- Operation timeout too short
- Network latency issues
- Large number of DNS records

**Solutions:**
1. Increase timeout in Services configuration
2. Check network connectivity to IBM Cloud
3. Review debug logs for slow API calls

### Debug Logging

Enable debug logging to troubleshoot issues:

```go
// Set log level to debug
log.SetLevel(log.DebugLevel)

// Run DNS validation
dns.ClusterStatus()

// Check logs for detailed information:
// - "findDNSZoneID: Searching for DNS zone matching base domain: example.com"
// - "searchZonesInInstance: Found 5 zone(s) in CRN: crn:..."
// - "fetchMatchingDNSRecords: Found matching record: ID=abc123, Name=api.cluster.domain.com"
```

### Diagnostic Commands

```bash
# List all CIS instances
ibmcloud resource service-instances --service-name internet-svcs

# List zones in a CIS instance
ibmcloud cis zones --instance <instance-name>

# List DNS records in a zone
ibmcloud cis dns-records <zone-id> --instance <instance-name>

# Test DNS resolution
dig @8.8.8.8 api.mycluster.example.com
nslookup api-int.mycluster.example.com
host *.apps.mycluster.example.com
```

---

## Best Practices

### 1. Initialization

✅ **DO:**
```go
// Always check for initialization errors
dnsObjects, errs := NewIBMDNS(services)
if len(errs) > 0 && errs[0] != nil {
    return fmt.Errorf("DNS initialization failed: %w", errs[0])
}
```

❌ **DON'T:**
```go
// Don't ignore initialization errors
dnsObjects, _ := NewIBMDNS(services)
dns := dnsObjects[0] // May be nil!
```

### 2. Error Handling

✅ **DO:**
```go
// Wrap errors with context
if err := dns.ClusterStatus(); err != nil {
    return fmt.Errorf("cluster %s DNS validation failed: %w", clusterName, err)
}
```

❌ **DON'T:**
```go
// Don't lose error context
if err := dns.ClusterStatus(); err != nil {
    return err // Lost context about what failed
}
```

### 3. Logging

✅ **DO:**
```go
// Use appropriate log levels
log.Debugf("Checking DNS record: %s", recordName)
log.Infof("DNS validation successful for cluster %s", clusterName)
log.Errorf("DNS validation failed: %v", err)
```

❌ **DON'T:**
```go
// Don't use fmt.Println for logging
fmt.Println("Checking DNS record:", recordName)
```

### 4. Resource Management

✅ **DO:**
```go
// Use context with timeout
ctx, cancel := services.GetContextWithTimeout()
defer cancel()

result, err := dns.fetchMatchingDNSRecords(ctx, matcher)
```

❌ **DON'T:**
```go
// Don't use context.Background() for long operations
ctx := context.Background()
result, err := dns.fetchMatchingDNSRecords(ctx, matcher)
```

### 5. Validation

✅ **DO:**
```go
// Validate inputs early
if services == nil {
    return nil, fmt.Errorf("services cannot be nil")
}
if apiKey == "" {
    return nil, fmt.Errorf("API key cannot be empty")
}
```

❌ **DON'T:**
```go
// Don't assume inputs are valid
authenticator := &core.IamAuthenticator{
    ApiKey: apiKey, // May be empty!
}
```

---

## Performance Considerations

### Pagination

The DNS service uses pagination to efficiently retrieve large numbers of DNS records:

```go
const (
    defaultPerPage int64 = 20  // Records per page
    defaultPage    int64 = 1   // Starting page
)
```

**Performance Tips:**
- Default page size (20) is optimal for most use cases
- Larger page sizes may cause API timeouts
- Smaller page sizes increase API call overhead

### Caching

Currently, the DNS service does not cache results. Consider implementing caching for:
- DNS zone ID lookups (rarely change)
- CIS instance lists (rarely change)
- DNS record lists (change during cluster lifecycle)

**Example Caching Strategy:**
```go
type DNSCache struct {
    zoneID    string
    records   []string
    timestamp time.Time
    ttl       time.Duration
}

func (c *DNSCache) IsValid() bool {
    return time.Since(c.timestamp) < c.ttl
}
```

### API Rate Limiting

IBM Cloud APIs have rate limits. Best practices:
- Use pagination to reduce API calls
- Implement exponential backoff for retries
- Cache results when appropriate
- Batch operations when possible

---

## Security Considerations

### API Key Management

✅ **DO:**
- Store API keys in environment variables or secure vaults
- Use IAM service IDs with minimal required permissions
- Rotate API keys regularly
- Never log API keys

❌ **DON'T:**
- Hardcode API keys in source code
- Commit API keys to version control
- Share API keys across environments
- Use personal API keys for production

### Permissions Required

The DNS service requires the following IAM permissions:

| Service | Permission | Reason |
|---------|-----------|--------|
| Internet Services | Viewer | List zones and DNS records |
| Resource Controller | Viewer | List CIS instances |
| DNS Services | Viewer | Access DNS Services API |

**Minimal IAM Policy:**
```json
{
  "roles": [
    {
      "role_id": "crn:v1:bluemix:public:iam::::role:Viewer"
    }
  ],
  "resources": [
    {
      "service_name": "internet-svcs"
    },
    {
      "service_name": "dns-svcs"
    }
  ]
}
```

---

## Related Documentation

- [IBM Cloud DNS Services API](https://cloud.ibm.com/apidocs/dns-svcs)
- [IBM Cloud Internet Services API](https://cloud.ibm.com/apidocs/cis)
- [OpenShift DNS Requirements](https://docs.openshift.com/container-platform/latest/installing/installing_platform_agnostic/installing-platform-agnostic.html#installation-dns-user-infra_installing-platform-agnostic)
- [Services.go Documentation](./Services-documentation.md)
- [RunnableObject Interface](./RunnableObject-documentation.md)

---

## Changelog

### Version 1.0 (2026-05-08)
- Initial documentation
- Identified critical issues with CIS CRN tracking
- Documented pagination logic bug
- Added comprehensive API reference
- Included troubleshooting guide

---

## Contributing

When modifying IBM-DNS.go:

1. **Update Tests:** Add/update unit tests for any changes
2. **Update Documentation:** Keep this document in sync with code changes
3. **Follow Conventions:** Maintain existing code style and patterns
4. **Add Logging:** Include debug logging for troubleshooting
5. **Handle Errors:** Wrap errors with context using `fmt.Errorf` with `%w`
6. **Validate Inputs:** Check for nil pointers and empty strings
7. **Update Changelog:** Document changes in this file

---

## License

Copyright 2025 IBM Corp

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.