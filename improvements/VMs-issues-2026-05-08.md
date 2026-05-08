# VMs.go Issues Review - 2026-05-08

## Scope

This document summarizes confirmed review findings for `VMs.go` based on code inspection of:
- `VMs.go`
- `OpenStack.go`
- `Utils.go`
- `Services.go`
- `RunnableObject.go`

Validation note:
- `go test ./...` passed successfully
- Findings below are code quality, robustness, and maintainability issues rather than compile or unit test failures

## Summary

`VMs.go` is functional and passes the current test suite indirectly through repository-wide testing, but it contains several issues worth addressing. The most important behavioral issue is loose cluster VM matching, which can cause unrelated virtual machines to appear in cluster status output.

## Findings

### 1. Loose cluster VM matching
**Severity:** High

**Location:**
- `VMs.go:228`

**Current code:**
```go
if !strings.HasPrefix(strings.ToLower(server.Name), strings.ToLower(infraID)) {
    log.Debugf("ClusterStatus: SKIPPING server = %s (not part of cluster)", server.Name)
    continue
}
```

**Issue:**
Cluster membership is determined by checking whether the server name starts with `infraID`. This is overly broad and can match unrelated VMs whose names happen to share the same prefix.

**Impact:**
- False positives in cluster status output
- Misleading operational visibility
- Incorrect SSH checks and hypervisor reporting for unrelated VMs

**Recommendation:**
Use a stricter match such as:
- `infraID + "-"` as the required prefix, or
- metadata/tags/attributes from OpenStack if available

**Suggested direction:**
```go
clusterPrefix := strings.ToLower(infraID) + "-"
if !strings.HasPrefix(strings.ToLower(server.Name), clusterPrefix) {
    continue
}
```

---

### 2. SSH checks can consume too much total runtime
**Severity:** Medium

**Locations:**
- `VMs.go:245-255`
- `Utils.go:385-414`

**Issue:**
`ClusterStatus()` calls:
```go
outb, err = keyscanServer(ctx, ipAddress, true)
```

The `keyscanServer()` helper retries using exponential backoff until the provided context expires. In `ClusterStatus()`, the same long-lived context is reused across all servers. Since VMs are processed serially, one unreachable IP can consume a large portion of the total timeout.

**Impact:**
- Slow status reporting
- Poor behavior during network failures
- Large clusters may take too long to evaluate if multiple nodes are unreachable

**Recommendation:**
Create a short per-host timeout context specifically for SSH probing.

**Suggested direction:**
```go
sshCtx, sshCancel := context.WithTimeout(ctx, 10*time.Second)
defer sshCancel()

outb, err = keyscanServer(sshCtx, ipAddress, true)
```

Note: in a loop, call `sshCancel()` explicitly after the probe instead of deferring repeatedly.

---

### 3. Redundant compute client creation
**Severity:** Low

**Locations:**
- `VMs.go:197`
- `VMs.go:203`
- `OpenStack.go:494-517`

**Issue:**
`ClusterStatus()` creates a compute client:

```go
connCompute, err = NewServiceClient(ctx, "compute", DefaultClientOpts(cloud))
```

It then uses that client for hypervisor queries, but server listing is done through:

```go
allServers, err = getAllServers(ctx, []string{cloud})
```

`getAllServers()` creates another compute client internally. This duplicates client setup and authentication work.

**Impact:**
- Unnecessary extra client creation
- Slight inefficiency
- Inconsistent client usage within the same method

**Recommendation:**
Use one consistent path:
- either reuse `connCompute` for both hypervisors and servers, or
- move both operations to common helpers with consistent client lifecycle behavior

---

### 4. Separator string duplication
**Severity:** Low

**Locations:**
- `VMs.go:217`
- `Utils.go:50`

**Issue:**
`VMs.go` prints a hardcoded separator string even though `Utils.go` already defines:

```go
separatorLine = "8<--------8<--------8<--------8<--------8<--------8<--------8<--------8<--------"
```

**Impact:**
- Duplication
- Drift risk if the separator changes in one place but not the other

**Recommendation:**
Reuse the shared constant:
```go
fmt.Println(separatorLine)
```

---

### 5. Constructor returns a meaningless error slot
**Severity:** Low

**Location:**
- `VMs.go:97-111`

**Issue:**
`innerNewVMs()` allocates:
```go
errs = make([]error, 1)
```

but does not populate any error. This means callers receive an error slice of length 1 containing `nil`.

**Impact:**
- Misleading constructor contract
- Unnecessary bookkeeping
- Makes the API appear to report per-object construction results when there is no actual error state

**Recommendation:**
Return either:
- `nil`, or
- an empty slice when there are no errors

Example:
```go
return vms, nil
```

## Non-Issues / Validation Notes

### Repository health
The repository passes:
```bash
go test ./...
```

This indicates the findings above do not currently manifest as compilation errors or failing tests.

### Review classification
These are primarily:
- correctness risk in filtering logic
- robustness/performance concerns in SSH probing
- maintainability issues in construction and formatting

## Priority Recommendation

Address in this order:

1. **Loose cluster VM matching**
   - highest risk of incorrect behavior

2. **SSH timeout behavior**
   - highest runtime/operational impact

3. **Redundant compute client creation**
   - cleanup and efficiency

4. **Constructor error slice cleanup**
   - API clarity

5. **Separator constant reuse**
   - minor cleanup

## Proposed Next Steps

1. Tighten the VM name filter to avoid false positives
2. Add a short timeout specifically for SSH probing
3. Refactor `ClusterStatus()` to avoid redundant compute client creation
4. Simplify `innerNewVMs()` error handling
5. Replace the duplicated separator literal with `separatorLine`

## Conclusion

Yes, there are issues in `VMs.go`, but they are not breaking build or tests. The most significant confirmed issue is the broad VM name prefix check, which can lead to incorrect cluster status reporting. The remaining issues are mainly around runtime efficiency, API clarity, and maintainability.