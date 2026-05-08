# IBMCloud.go Documentation

## Overview

`IBMCloud.go` provides a set of wrapper functions for IBM Cloud SDK operations with built-in exponential backoff retry logic. These functions abstract the complexity of retry handling and provide a consistent interface for interacting with various IBM Cloud services.

## Table of Contents

1. [Purpose](#purpose)
2. [Architecture](#architecture)
3. [Functions](#functions)
4. [Usage Examples](#usage-examples)
5. [Error Handling](#error-handling)
6. [Best Practices](#best-practices)
7. [Dependencies](#dependencies)
8. [Related Files](#related-files)

---

## Purpose

The primary purposes of this file are:

1. **Retry Logic**: Automatically retry IBM Cloud API calls on transient failures
2. **Consistency**: Provide a uniform interface for all IBM Cloud operations
3. **Reliability**: Handle network issues and temporary service unavailability
4. **Observability**: Log retry attempts for debugging and monitoring

---

## Architecture

### Design Pattern

The file uses a **wrapper pattern** combined with **generic programming**:

```
┌─────────────────────────────────────────────────────────┐
│                    Caller Code                          │
└────────────────────┬────────────────────────────────────┘
                     │
                     ▼
┌─────────────────────────────────────────────────────────┐
│              IBMCloud Wrapper Function                  │
│  - Validates inputs (service client)                    │
│  - Calls retryWithBackoff                               │
└────────────────────┬────────────────────────────────────┘
                     │
                     ▼
┌─────────────────────────────────────────────────────────┐
│         retryWithBackoff (from Utils.go)                │
│  - Implements exponential backoff                       │
│  - Logs retry attempts                                  │
│  - Respects context cancellation                        │
└────────────────────┬────────────────────────────────────┘
                     │
                     ▼
┌─────────────────────────────────────────────────────────┐
│              IBM Cloud SDK Function                     │
│  - Actual API call to IBM Cloud                         │
└─────────────────────────────────────────────────────────┘
```

### Retry Configuration

All functions use the default retry configuration from `Utils.go`:
- **Initial Duration**: 15 seconds
- **Backoff Factor**: 1.1x
- **Maximum Steps**: 100 retries
- **Cap**: Respects context timeout (max 30 minutes)

---

## Functions

### 1. listResourceInstances

Retrieves a list of resource instances from IBM Cloud Resource Controller.

**Signature**:
```go
func listResourceInstances(
    ctx context.Context,
    controllerSvc *resourcecontrollerv2.ResourceControllerV2,
    listResourceOptions *resourcecontrollerv2.ListResourceInstancesOptions,
) (*resourcecontrollerv2.ResourceInstancesList, *core.DetailedResponse, error)
```

**Parameters**:
- `ctx`: Context for cancellation and timeout control
- `controllerSvc`: IBM Cloud Resource Controller service client (must not be nil)
- `listResourceOptions`: Options for filtering and pagination

**Returns**:
- `ResourceInstancesList`: List of resource instances
- `DetailedResponse`: HTTP response details
- `error`: Any error encountered

**API Reference**: [List Resource Instances](https://cloud.ibm.com/apidocs/resource-controller/resource-controller#list-resource-instances)

**Example**:
```go
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
defer cancel()

options := &resourcecontrollerv2.ListResourceInstancesOptions{
    Name: core.StringPtr("my-instance"),
}

instances, response, err := listResourceInstances(ctx, controllerSvc, options)
if err != nil {
    log.Fatalf("Failed to list instances: %v", err)
}
```

---

### 2. listCatalogEntries

Retrieves catalog entries from IBM Cloud Global Catalog.

**Signature**:
```go
func listCatalogEntries(
    ctx context.Context,
    gcv1 *globalcatalogv1.GlobalCatalogV1,
    listCatalogEntriesOpt *globalcatalogv1.ListCatalogEntriesOptions,
) (*globalcatalogv1.EntrySearchResult, *core.DetailedResponse, error)
```

**Parameters**:
- `ctx`: Context for cancellation and timeout control
- `gcv1`: IBM Cloud Global Catalog service client (must not be nil)
- `listCatalogEntriesOpt`: Options for filtering catalog entries

**Returns**:
- `EntrySearchResult`: Search results containing catalog entries
- `DetailedResponse`: HTTP response details
- `error`: Any error encountered

**API Reference**: [List Catalog Entries](https://cloud.ibm.com/apidocs/resource-catalog/global-catalog#list-catalog-entries)

**Example**:
```go
ctx := context.Background()

options := &globalcatalogv1.ListCatalogEntriesOptions{
    Q: core.StringPtr("kind:service"),
}

entries, response, err := listCatalogEntries(ctx, gcv1, options)
if err != nil {
    log.Fatalf("Failed to list catalog entries: %v", err)
}
```

---

### 3. getChildObjects

Retrieves child objects from IBM Cloud Global Catalog.

**Signature**:
```go
func getChildObjects(
    ctx context.Context,
    gcv1 *globalcatalogv1.GlobalCatalogV1,
    getChildOpt *globalcatalogv1.GetChildObjectsOptions,
) (*globalcatalogv1.EntrySearchResult, *core.DetailedResponse, error)
```

**Parameters**:
- `ctx`: Context for cancellation and timeout control
- `gcv1`: IBM Cloud Global Catalog service client (must not be nil)
- `getChildOpt`: Options for retrieving child objects

**Returns**:
- `EntrySearchResult`: Search results containing child objects
- `DetailedResponse`: HTTP response details
- `error`: Any error encountered

**API Reference**: [Get Child Catalog Entries](https://cloud.ibm.com/apidocs/resource-catalog/global-catalog#get-child-catalog-entries)

**Note**: This function is exported (capitalized) for use by other packages.

---

### 4. listZones

Retrieves DNS zones from IBM Cloud Internet Services.

**Signature**:
```go
func listZones(
    ctx context.Context,
    zv1 *zonesv1.ZonesV1,
    listOpts *zonesv1.ListZonesOptions,
) (*zonesv1.ListZonesResp, *core.DetailedResponse, error)
```

**Parameters**:
- `ctx`: Context for cancellation and timeout control
- `zv1`: IBM Cloud Zones service client (must not be nil)
- `listOpts`: Options for listing zones

**Returns**:
- `ListZonesResp`: List of DNS zones
- `DetailedResponse`: HTTP response details
- `error`: Any error encountered

**API Reference**: [List All Zones](https://cloud.ibm.com/apidocs/cis#list-all-zones)

**Example**:
```go
ctx := context.Background()

options := &zonesv1.ListZonesOptions{}

zones, response, err := listZones(ctx, zv1, options)
if err != nil {
    log.Fatalf("Failed to list zones: %v", err)
}

for _, zone := range zones.Result {
    fmt.Printf("Zone: %s (ID: %s)\n", *zone.Name, *zone.ID)
}
```

---

### 5. listAllDnsRecords

Retrieves all DNS records from IBM Cloud Internet Services.

**Signature**:
```go
func listAllDnsRecords(
    ctx context.Context,
    dnsService *dnsrecordsv1.DnsRecordsV1,
    listOpts *dnsrecordsv1.ListAllDnsRecordsOptions,
) (*dnsrecordsv1.ListDnsrecordsResp, *core.DetailedResponse, error)
```

**Parameters**:
- `ctx`: Context for cancellation and timeout control
- `dnsService`: IBM Cloud DNS Records service client (must not be nil)
- `listOpts`: Options for listing DNS records

**Returns**:
- `ListDnsrecordsResp`: List of DNS records
- `DetailedResponse`: HTTP response details
- `error`: Any error encountered

**API Reference**: [List All DNS Records](https://cloud.ibm.com/apidocs/cis#list-all-dns-records)

**Example**:
```go
ctx := context.Background()

options := &dnsrecordsv1.ListAllDnsRecordsOptions{
    Type: core.StringPtr("A"),
}

records, response, err := listAllDnsRecords(ctx, dnsService, options)
if err != nil {
    log.Fatalf("Failed to list DNS records: %v", err)
}
```

---

### 6. deleteDnsRecord

Deletes a DNS record from IBM Cloud Internet Services.

**Signature**:
```go
func deleteDnsRecord(
    ctx context.Context,
    dnsService *dnsrecordsv1.DnsRecordsV1,
    deleteOpts *dnsrecordsv1.DeleteDnsRecordOptions,
) (*dnsrecordsv1.DeleteDnsrecordResp, *core.DetailedResponse, error)
```

**Parameters**:
- `ctx`: Context for cancellation and timeout control
- `dnsService`: IBM Cloud DNS Records service client (must not be nil)
- `deleteOpts`: Options specifying which DNS record to delete

**Returns**:
- `DeleteDnsrecordResp`: Deletion response
- `DetailedResponse`: HTTP response details
- `error`: Any error encountered

**API Reference**: [Delete DNS Record](https://cloud.ibm.com/apidocs/cis#delete-dns-record)

**Example**:
```go
ctx := context.Background()

options := &dnsrecordsv1.DeleteDnsRecordOptions{
    DnsrecordIdentifier: core.StringPtr("record-id-123"),
}

result, response, err := deleteDnsRecord(ctx, dnsService, options)
if err != nil {
    log.Fatalf("Failed to delete DNS record: %v", err)
}
```

---

### 7. createDnsRecord

Creates a new DNS record in IBM Cloud Internet Services.

**Signature**:
```go
func createDnsRecord(
    ctx context.Context,
    dnsService *dnsrecordsv1.DnsRecordsV1,
    createOpts *dnsrecordsv1.CreateDnsRecordOptions,
) (*dnsrecordsv1.DnsrecordResp, *core.DetailedResponse, error)
```

**Parameters**:
- `ctx`: Context for cancellation and timeout control
- `dnsService`: IBM Cloud DNS Records service client (must not be nil)
- `createOpts`: Options specifying the DNS record to create

**Returns**:
- `DnsrecordResp`: Created DNS record details
- `DetailedResponse`: HTTP response details
- `error`: Any error encountered

**API Reference**: [Create DNS Record](https://cloud.ibm.com/apidocs/cis#create-dns-record)

**Example**:
```go
ctx := context.Background()

options := &dnsrecordsv1.CreateDnsRecordOptions{
    Type:    core.StringPtr("A"),
    Name:    core.StringPtr("api.example.com"),
    Content: core.StringPtr("192.168.1.100"),
    TTL:     core.Int64Ptr(3600),
}

record, response, err := createDnsRecord(ctx, dnsService, options)
if err != nil {
    log.Fatalf("Failed to create DNS record: %v", err)
}
```

---

## Usage Examples

### Complete Example: Managing DNS Records

```go
package main

import (
    "context"
    "fmt"
    "log"
    "time"

    "github.com/IBM/go-sdk-core/v5/core"
    "github.com/IBM/networking-go-sdk/dnsrecordsv1"
    "github.com/IBM/networking-go-sdk/zonesv1"
)

func main() {
    // Initialize authenticator
    authenticator := &core.IamAuthenticator{
        ApiKey: "your-api-key",
    }

    // Create DNS service client
    dnsService, err := dnsrecordsv1.NewDnsRecordsV1(&dnsrecordsv1.DnsRecordsV1Options{
        Authenticator: authenticator,
        Crn:          core.StringPtr("your-crn"),
        ZoneIdentifier: core.StringPtr("your-zone-id"),
    })
    if err != nil {
        log.Fatalf("Failed to create DNS service: %v", err)
    }

    // Create context with timeout
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
    defer cancel()

    // List existing DNS records
    listOpts := &dnsrecordsv1.ListAllDnsRecordsOptions{
        Type: core.StringPtr("A"),
    }
    
    records, _, err := listAllDnsRecords(ctx, dnsService, listOpts)
    if err != nil {
        log.Fatalf("Failed to list DNS records: %v", err)
    }

    fmt.Printf("Found %d DNS records\n", len(records.Result))

    // Create a new DNS record
    createOpts := &dnsrecordsv1.CreateDnsRecordOptions{
        Type:    core.StringPtr("A"),
        Name:    core.StringPtr("api.example.com"),
        Content: core.StringPtr("192.168.1.100"),
        TTL:     core.Int64Ptr(3600),
    }

    newRecord, _, err := createDnsRecord(ctx, dnsService, createOpts)
    if err != nil {
        log.Fatalf("Failed to create DNS record: %v", err)
    }

    fmt.Printf("Created DNS record: %s -> %s\n", *newRecord.Result.Name, *newRecord.Result.Content)

    // Delete the DNS record
    deleteOpts := &dnsrecordsv1.DeleteDnsRecordOptions{
        DnsrecordIdentifier: newRecord.Result.ID,
    }

    _, _, err = deleteDnsRecord(ctx, dnsService, deleteOpts)
    if err != nil {
        log.Fatalf("Failed to delete DNS record: %v", err)
    }

    fmt.Println("DNS record deleted successfully")
}
```

---

## Error Handling

### Common Error Scenarios

1. **Nil Service Client**:
```go
instances, _, err := listResourceInstances(ctx, nil, options)
// Error: "ListResourceInstances failed: controllerSvc cannot be nil"
```

2. **Context Cancellation**:
```go
ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
cancel() // Cancel immediately

instances, _, err := listResourceInstances(ctx, controllerSvc, options)
// Error: Context cancelled during retry
```

3. **API Errors**:
```go
// Invalid credentials
instances, _, err := listResourceInstances(ctx, controllerSvc, options)
// Error: "ListResourceInstances failed: authentication failed"
```

### Error Handling Best Practices

```go
instances, response, err := listResourceInstances(ctx, controllerSvc, options)
if err != nil {
    // Check if it's a context error
    if ctx.Err() != nil {
        log.Printf("Operation cancelled: %v", ctx.Err())
        return
    }

    // Check HTTP status code
    if response != nil {
        log.Printf("HTTP Status: %d", response.StatusCode)
    }

    // Log the error
    log.Printf("Failed to list instances: %v", err)
    return
}

// Success - process results
for _, instance := range instances.Resources {
    fmt.Printf("Instance: %s\n", *instance.Name)
}
```

---

## Best Practices

### 1. Always Use Context with Timeout

```go
// Good
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
defer cancel()

instances, _, err := listResourceInstances(ctx, controllerSvc, options)
```

```go
// Bad - no timeout
ctx := context.Background()
instances, _, err := listResourceInstances(ctx, controllerSvc, options)
```

### 2. Check for Nil Before Calling

```go
// Good
if controllerSvc == nil {
    return fmt.Errorf("service client not initialized")
}

instances, _, err := listResourceInstances(ctx, controllerSvc, options)
```

### 3. Handle Response Details

```go
instances, response, err := listResourceInstances(ctx, controllerSvc, options)
if err != nil {
    if response != nil {
        log.Printf("HTTP Status: %d, Headers: %v", response.StatusCode, response.Headers)
    }
    return err
}
```

### 4. Use Appropriate Timeouts

```go
// For quick operations
ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
defer cancel()

// For long-running operations
ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
defer cancel()
```

### 5. Log Retry Attempts

The functions automatically log retry attempts at DEBUG level. Enable debug logging:

```go
log := initLogger(true) // Enable debug mode
```

---

## Dependencies

### Required Packages

```go
import (
    "context"
    "fmt"

    "github.com/IBM/go-sdk-core/v5/core"
    "github.com/IBM/networking-go-sdk/dnsrecordsv1"
    "github.com/IBM/networking-go-sdk/zonesv1"
    "github.com/IBM/platform-services-go-sdk/globalcatalogv1"
    "github.com/IBM/platform-services-go-sdk/resourcecontrollerv2"
)
```

### External Dependencies

- **Utils.go**: Provides `retryWithBackoff` function and `leftInContext` helper
- **OcpIpiPowerVC.go**: Provides global `log` variable
- **CmdCreateBastion.go**: Provides `leftInContext` function (also in Utils.go)

---

## Related Files

- **Utils.go**: Contains the `retryWithBackoff` generic function
- **IBM-DNS.go**: Uses these functions for DNS management
- **Services.go**: May use these functions for service discovery
- **improvements/IBMCloud-issues-2026-05-08.md**: Current issues and improvement suggestions
- **improvements/IBMCloud-code-improvement-plan.md**: Original improvement plan

---

## Performance Considerations

### Retry Behavior

- **Initial Delay**: 15 seconds
- **Backoff Factor**: 1.1x (increases by 10% each retry)
- **Maximum Retries**: 100 attempts
- **Maximum Duration**: Respects context timeout (capped at 30 minutes)

### Example Retry Timeline

```
Attempt 1: 0s
Attempt 2: 15s
Attempt 3: 31.5s (15 * 1.1)
Attempt 4: 49.65s (31.5 * 1.1)
Attempt 5: 69.615s (49.65 * 1.1)
...
```

### Optimization Tips

1. **Use appropriate timeouts**: Don't set unnecessarily long timeouts
2. **Monitor retry counts**: High retry counts may indicate service issues
3. **Consider caching**: Cache results when appropriate to reduce API calls
4. **Batch operations**: Use batch APIs when available instead of individual calls

---

## Troubleshooting

### Issue: Operations timing out

**Solution**: Increase context timeout or check network connectivity
```go
ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
defer cancel()
```

### Issue: Too many retries

**Solution**: Check IBM Cloud service status and credentials
```go
// Enable debug logging to see retry attempts
log := initLogger(true)
```

### Issue: Nil pointer errors

**Solution**: Ensure service clients are properly initialized
```go
if controllerSvc == nil {
    return fmt.Errorf("service client not initialized")
}
```

---

## Future Enhancements

1. **Metrics Collection**: Add metrics for monitoring retry counts and success rates
2. **Circuit Breaker**: Implement circuit breaker pattern for repeated failures
3. **Custom Retry Policies**: Allow per-operation retry configuration
4. **Rate Limiting**: Add rate limiting to prevent API throttling
5. **Caching**: Implement response caching for frequently accessed data

---

## Conclusion

`IBMCloud.go` provides a robust, reliable interface for IBM Cloud operations with built-in retry logic. By following the best practices outlined in this document, you can effectively use these functions to build resilient applications that gracefully handle transient failures.

For issues or improvements, see `improvements/IBMCloud-issues-2026-05-08.md`.