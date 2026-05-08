# CmdCreateBastion.go - Context Handling Improvements

**Date**: 2026-05-08  
**Severity**: High  
**Category**: Concurrency & Resource Management  
**File**: CmdCreateBastion.go (1238 lines)

## Executive Summary

Critical issues identified with context propagation and cancellation handling in CmdCreateBastion.go. Multiple long-running SSH operations do not respect context timeouts, leading to potential resource leaks, hanging operations, and unpredictable behavior when timeouts expire.

## Issues Identified

### 🔴 Critical Issue #1: Context Not Propagated to SSH Operations

**Location**: Lines 367-402 (`setupHAProxyOnServer`)

**Problem**: The function receives a context parameter but fails to pass it to any of the SSH operations it orchestrates.

```go
func setupHAProxyOnServer(ctx context.Context, ipAddress, bastionRsa string) error {
    cfg := newSSHConfig(ipAddress, bastionRsa)
    
    // Step 1: Uses context ✓
    if err := addServerKnownHosts(ctx, ipAddress); err != nil {
        return fmt.Errorf("failed to add server to known_hosts: %w", err)
    }
    
    // Step 2: Uses context ✓
    if err := waitForSSHReady(ctx, cfg); err != nil {
        return fmt.Errorf("SSH not ready: %w", err)
    }
    
    // Steps 3-6: Context NOT passed ✗
    if err := ensureHAProxyInstalled(cfg); err != nil {
        return fmt.Errorf("failed to ensure HAProxy installation: %w", err)
    }
    
    if err := ensureHAProxyConfigPermissions(cfg); err != nil {
        return fmt.Errorf("failed to configure HAProxy permissions: %w", err)
    }
    
    if err := ensureHAProxySELinux(cfg); err != nil {
        return fmt.Errorf("failed to configure HAProxy SELinux: %w", err)
    }
    
    if err := enableAndStartHAProxy(cfg); err != nil {
        return fmt.Errorf("failed to start HAProxy service: %w", err)
    }
    
    return nil
}
```

**Impact**:
- Operations continue running after context timeout (default: 15 minutes)
- No way to cancel in-progress SSH operations
- Potential for hanging SSH connections
- Resource leaks on timeout

**Risk Level**: HIGH

---

### 🔴 Critical Issue #2: SSH Operation Functions Missing Context Parameter

**Affected Functions**:

#### HAProxy Installation (Lines 146-199)
```go
func isHAProxyInstalled(cfg *sshConfig) (bool, error)
func installHAProxy(cfg *sshConfig) error
func ensureHAProxyInstalled(cfg *sshConfig) error
```

**Operations**:
- `rpm -q haproxy` - Quick but can timeout on slow systems
- `dnf install -y haproxy` - **Can take 2-5 minutes** with slow network/mirrors
- No cancellation possible once started

#### File Permission Operations (Lines 207-253)
```go
func getFilePermissions(cfg *sshConfig, filePath string) (string, error)
func setFilePermissions(cfg *sshConfig, filePath, perms string) error
func ensureHAProxyConfigPermissions(cfg *sshConfig) error
```

**Operations**:
- `stat -c "%a" /path` - Usually fast
- `chmod 646 /path` - Usually fast
- Can accumulate delays with network latency

#### SELinux Operations (Lines 259-313)
```go
func getSELinuxBool(cfg *sshConfig, boolName string) (bool, error)
func setSELinuxBool(cfg *sshConfig, boolName string, value bool) error
func ensureHAProxySELinux(cfg *sshConfig) error
```

**Operations**:
- `getsebool haproxy_connect_any` - Usually fast
- `setsebool -P haproxy_connect_any=1` - **Can take 30-60 seconds** (persistent flag)
- No cancellation during SELinux policy update

#### Systemd Service Operations (Lines 319-353)
```go
func systemctlCommand(cfg *sshConfig, action, service string) error
func enableService(cfg *sshConfig, service string) error
func startService(cfg *sshConfig, service string) error
func enableAndStartHAProxy(cfg *sshConfig) error
```

**Operations**:
- `systemctl enable haproxy.service` - Usually fast
- `systemctl start haproxy.service` - **Can hang indefinitely** if service fails to start
- No timeout on service startup

**Risk Level**: HIGH

---

### 🔴 Critical Issue #3: Context Not Passed to Remote Setup

**Location**: Line 798 (`setupBastion`)

```go
func setupBastion(ctx context.Context, config *BastionConfig) error {
    if config.IsRemoteSetup() {
        fmt.Println("Setting up bastion remotely...")
        // sendCreateBastion doesn't receive ctx!
        if err := sendCreateBastion(config.ServerIP,
            config.Clouds[0],
            config.BastionName,
            config.DomainName,
        ); err != nil {
            return fmt.Errorf("remote setup failed: %w", err)
        }
        return nil
    }
    // ... local setup uses context
}
```

**Impact**:
- Remote setup operations ignore timeout
- No way to cancel remote operations
- Inconsistent behavior between local and remote setup

**Risk Level**: HIGH

---

### 🟡 Moderate Issue #4: No Context Checks Between Operations

**Location**: Lines 367-402 (`setupHAProxyOnServer`)

**Problem**: Six sequential operations with no context cancellation checks between them.

```go
func setupHAProxyOnServer(ctx context.Context, ipAddress, bastionRsa string) error {
    // Step 1
    if err := addServerKnownHosts(ctx, ipAddress); err != nil { ... }
    
    // No context check here!
    
    // Step 2
    if err := waitForSSHReady(ctx, cfg); err != nil { ... }
    
    // No context check here!
    
    // Steps 3-6 (don't even receive context)
    if err := ensureHAProxyInstalled(cfg); err != nil { ... }
    if err := ensureHAProxyConfigPermissions(cfg); err != nil { ... }
    if err := ensureHAProxySELinux(cfg); err != nil { ... }
    if err := enableAndStartHAProxy(cfg); err != nil { ... }
}
```

**Impact**:
- If context is cancelled after step 1, steps 2-6 still execute
- Wasted resources on cancelled operations
- Delayed error reporting

**Risk Level**: MODERATE

---

### 🟡 Moderate Issue #5: Cleanup Functions Don't Handle Context Cancellation

**Location**: Lines 868-884 (`createServer`)

```go
cleanupPort := func(createdPort *ports.Port) {
    if deleteErr := deleteNetworkPort(ctx, cloudName, createdPort); deleteErr != nil {
        log.Debugf("Warning: failed to cleanup port %s: %v", port.ID, deleteErr)
    }
}

cleanupServerAndPort := func(server *servers.Server, createdPort *ports.Port) {
    if deleteErr := deleteServer(ctx, cloudName, server); deleteErr != nil {
        // Context might be cancelled here
        log.Debugf("Warning: failed to cleanup server %v: %v", server.ID, deleteErr)
    }
    cleanupPort(createdPort)
}
```

**Problem**:
- Cleanup operations use the same context that might be cancelled
- If context is cancelled, cleanup might fail silently
- Resource leaks possible (orphaned servers/ports)

**Recommendation**: Use `context.Background()` or a fresh context with short timeout for cleanup operations.

**Risk Level**: MODERATE

---

## Root Cause Analysis

1. **Design Pattern Issue**: SSH operation functions were designed without context awareness
2. **Incremental Development**: Context was added to high-level functions but not propagated down
3. **Missing Abstraction**: No context-aware SSH execution wrapper
4. **Inconsistent API**: Some functions accept context, others don't

---

## Recommended Solutions

### Solution 1: Add Context to All SSH Operation Functions (Recommended)

**Priority**: HIGH  
**Effort**: Medium  
**Impact**: Comprehensive fix

#### Changes Required:

1. **Update function signatures** to accept context:

```go
// Before
func ensureHAProxyInstalled(cfg *sshConfig) error

// After
func ensureHAProxyInstalled(ctx context.Context, cfg *sshConfig) error
```

2. **Modify all affected functions**:

```go
// HAProxy Installation
func isHAProxyInstalled(ctx context.Context, cfg *sshConfig) (bool, error)
func installHAProxy(ctx context.Context, cfg *sshConfig) error
func ensureHAProxyInstalled(ctx context.Context, cfg *sshConfig) error

// File Permissions
func getFilePermissions(ctx context.Context, cfg *sshConfig, filePath string) (string, error)
func setFilePermissions(ctx context.Context, cfg *sshConfig, filePath, perms string) error
func ensureHAProxyConfigPermissions(ctx context.Context, cfg *sshConfig) error

// SELinux
func getSELinuxBool(ctx context.Context, cfg *sshConfig, boolName string) (bool, error)
func setSELinuxBool(ctx context.Context, cfg *sshConfig, boolName string, value bool) error
func ensureHAProxySELinux(ctx context.Context, cfg *sshConfig) error

// Systemd
func systemctlCommand(ctx context.Context, cfg *sshConfig, action, service string) error
func enableService(ctx context.Context, cfg *sshConfig, service string) error
func startService(ctx context.Context, cfg *sshConfig, service string) error
func enableAndStartHAProxy(ctx context.Context, cfg *sshConfig) error
```

3. **Update SSH execution functions**:

```go
// Before
func execSSHCommand(cfg *sshConfig, command []string) (string, error)

// After
func execSSHCommand(ctx context.Context, cfg *sshConfig, command []string) (string, error) {
    args := []string{
        "ssh",
        "-o", "BatchMode=yes",
        "-o", "ConnectTimeout=30",
        "-o", "StrictHostKeyChecking=no",
        "-i", cfg.KeyPath,
        fmt.Sprintf("%s@%s", cfg.User, cfg.Host),
    }
    args = append(args, command...)
    
    // Use CommandContext instead of Command
    cmd := exec.CommandContext(ctx, args[0], args[1:]...)
    outb, err := cmd.CombinedOutput()
    return strings.TrimSpace(string(outb)), err
}
```

4. **Update all call sites** in `setupHAProxyOnServer`:

```go
func setupHAProxyOnServer(ctx context.Context, ipAddress, bastionRsa string) error {
    cfg := newSSHConfig(ipAddress, bastionRsa)
    
    if err := addServerKnownHosts(ctx, ipAddress); err != nil {
        return fmt.Errorf("failed to add server to known_hosts: %w", err)
    }
    
    if err := waitForSSHReady(ctx, cfg); err != nil {
        return fmt.Errorf("SSH not ready: %w", err)
    }
    
    // Now all functions receive context
    if err := ensureHAProxyInstalled(ctx, cfg); err != nil {
        return fmt.Errorf("failed to ensure HAProxy installation: %w", err)
    }
    
    if err := ensureHAProxyConfigPermissions(ctx, cfg); err != nil {
        return fmt.Errorf("failed to configure HAProxy permissions: %w", err)
    }
    
    if err := ensureHAProxySELinux(ctx, cfg); err != nil {
        return fmt.Errorf("failed to configure HAProxy SELinux: %w", err)
    }
    
    if err := enableAndStartHAProxy(ctx, cfg); err != nil {
        return fmt.Errorf("failed to start HAProxy service: %w", err)
    }
    
    return nil
}
```

---

### Solution 2: Add Context Checks Between Operations

**Priority**: MEDIUM  
**Effort**: Low  
**Impact**: Faster failure detection

```go
func setupHAProxyOnServer(ctx context.Context, ipAddress, bastionRsa string) error {
    cfg := newSSHConfig(ipAddress, bastionRsa)
    
    // Check context before each major step
    if err := ctx.Err(); err != nil {
        return fmt.Errorf("context cancelled before adding to known_hosts: %w", err)
    }
    if err := addServerKnownHosts(ctx, ipAddress); err != nil {
        return fmt.Errorf("failed to add server to known_hosts: %w", err)
    }
    
    if err := ctx.Err(); err != nil {
        return fmt.Errorf("context cancelled before SSH ready check: %w", err)
    }
    if err := waitForSSHReady(ctx, cfg); err != nil {
        return fmt.Errorf("SSH not ready: %w", err)
    }
    
    if err := ctx.Err(); err != nil {
        return fmt.Errorf("context cancelled before HAProxy installation: %w", err)
    }
    if err := ensureHAProxyInstalled(ctx, cfg); err != nil {
        return fmt.Errorf("failed to ensure HAProxy installation: %w", err)
    }
    
    // ... continue for all steps
}
```

---

### Solution 3: Fix Remote Setup Context Handling

**Priority**: HIGH  
**Effort**: Low  
**Impact**: Consistency

```go
// Update sendCreateBastion signature
func sendCreateBastion(ctx context.Context, serverIP, cloudName, bastionName, domainName string) error {
    // Implementation should use ctx for HTTP requests
}

// Update call site
func setupBastion(ctx context.Context, config *BastionConfig) error {
    if config.IsRemoteSetup() {
        fmt.Println("Setting up bastion remotely...")
        if err := sendCreateBastion(ctx, config.ServerIP,
            config.Clouds[0],
            config.BastionName,
            config.DomainName,
        ); err != nil {
            return fmt.Errorf("remote setup failed: %w", err)
        }
        return nil
    }
    // ... local setup
}
```

---

### Solution 4: Improve Cleanup Context Handling

**Priority**: MEDIUM  
**Effort**: Low  
**Impact**: Better resource cleanup

```go
func createServer(ctx context.Context, cloudName, availabilityZone, flavorName, imageName, networkName, sshKeyName, bastionName string, userData []byte) error {
    // ... existing code ...
    
    // Create cleanup context with short timeout
    cleanupPort := func(createdPort *ports.Port) {
        cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
        defer cancel()
        
        if deleteErr := deleteNetworkPort(cleanupCtx, cloudName, createdPort); deleteErr != nil {
            log.Debugf("Warning: failed to cleanup port %s: %v", port.ID, deleteErr)
        }
    }
    
    cleanupServerAndPort := func(server *servers.Server, createdPort *ports.Port) {
        cleanupCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
        defer cancel()
        
        if deleteErr := deleteServer(cleanupCtx, cloudName, server); deleteErr != nil {
            if server == nil {
                log.Debugf("Warning: failed to cleanup nil server: %v", deleteErr)
            } else {
                log.Debugf("Warning: failed to cleanup server %v: %v", server.ID, deleteErr)
            }
        }
        cleanupPort(createdPort)
    }
    
    // ... rest of function
}
```

---

## Implementation Plan

### Phase 1: Critical Fixes (Week 1)
1. ✅ Update `execSSHCommand` and `execSSHSudoCommand` to accept context
2. ✅ Add context parameter to all SSH operation functions
3. ✅ Update `setupHAProxyOnServer` to pass context to all operations
4. ✅ Fix `sendCreateBastion` to accept and use context
5. ✅ Update all call sites

### Phase 2: Improvements (Week 2)
1. ✅ Add context checks between operations
2. ✅ Improve cleanup context handling
3. ✅ Add timeout configuration options
4. ✅ Add context cancellation tests

### Phase 3: Testing (Week 3)
1. ✅ Unit tests for context cancellation
2. ✅ Integration tests with timeout scenarios
3. ✅ Test cleanup behavior on cancellation
4. ✅ Performance testing with various timeouts

---

## Testing Strategy

### Unit Tests

```go
func TestEnsureHAProxyInstalled_ContextCancellation(t *testing.T) {
    ctx, cancel := context.WithCancel(context.Background())
    cancel() // Cancel immediately
    
    cfg := newSSHConfig("192.168.1.1", "/path/to/key")
    err := ensureHAProxyInstalled(ctx, cfg)
    
    if err == nil {
        t.Error("Expected error due to cancelled context")
    }
    if !errors.Is(err, context.Canceled) {
        t.Errorf("Expected context.Canceled error, got: %v", err)
    }
}

func TestSetupHAProxyOnServer_Timeout(t *testing.T) {
    ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
    defer cancel()
    
    // Mock slow SSH operations
    err := setupHAProxyOnServer(ctx, "192.168.1.1", "/path/to/key")
    
    if err == nil {
        t.Error("Expected timeout error")
    }
    if !errors.Is(err, context.DeadlineExceeded) {
        t.Errorf("Expected context.DeadlineExceeded, got: %v", err)
    }
}
```

### Integration Tests

```go
func TestCreateBastion_FullWorkflow_WithTimeout(t *testing.T) {
    // Test complete bastion creation with realistic timeout
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
    defer cancel()
    
    config := &BastionConfig{
        // ... configuration
    }
    
    err := createBastionCommand(ctx, config)
    
    // Verify proper handling of timeout
    // Verify cleanup on cancellation
    // Verify no resource leaks
}
```

---

## Risk Assessment

### Before Fixes
- **Severity**: HIGH
- **Likelihood**: HIGH (timeout easily exceeded in production)
- **Impact**: Resource leaks, hanging operations, unpredictable behavior

### After Fixes
- **Severity**: LOW
- **Likelihood**: LOW
- **Impact**: Graceful cancellation, proper cleanup, predictable behavior

---

## Performance Considerations

### Current Behavior
- Operations continue after timeout
- Multiple SSH connections may remain open
- Resources not released until process termination

### Expected Behavior After Fixes
- Operations cancelled within 1-2 seconds of timeout
- SSH connections properly closed
- Resources released immediately on cancellation

### Timeout Recommendations
- Default: 15 minutes (current)
- Minimum: 5 minutes (for slow networks)
- Maximum: 30 minutes (for very slow environments)
- Cleanup operations: 30-60 seconds

---

## Migration Guide

### For Developers

1. **Update function calls** to include context:
   ```go
   // Before
   err := ensureHAProxyInstalled(cfg)
   
   // After
   err := ensureHAProxyInstalled(ctx, cfg)
   ```

2. **Check for context errors** in long-running operations:
   ```go
   if err := ctx.Err(); err != nil {
       return fmt.Errorf("operation cancelled: %w", err)
   }
   ```

3. **Use context-aware command execution**:
   ```go
   // Before
   cmd := exec.Command("ssh", args...)
   
   // After
   cmd := exec.CommandContext(ctx, "ssh", args...)
   ```

### For Operators

1. **Monitor timeout occurrences** in logs
2. **Adjust timeout values** based on environment
3. **Review cleanup logs** for resource leaks
4. **Test cancellation behavior** in staging

---

## Related Files

- `CmdCreateBastion.go` - Main file requiring changes
- `Utils.go` - May need context-aware helper functions
- `Run.go` - Command execution utilities
- `ServerCommand.go` - Remote command execution
- `CmdCreateBastion_test.go` - Test file requiring updates

---

## References

- Go Context Package: https://pkg.go.dev/context
- Context Best Practices: https://go.dev/blog/context
- exec.CommandContext: https://pkg.go.dev/os/exec#CommandContext
- Timeout Patterns: https://go.dev/blog/context-and-structs

---

## Conclusion

The context handling issues in CmdCreateBastion.go represent a **high-priority production risk** that should be addressed before deployment in environments with strict timeout requirements. The recommended solutions are straightforward to implement and will significantly improve the reliability and predictability of the bastion creation process.

**Estimated Effort**: 2-3 weeks (including testing)  
**Priority**: HIGH  
**Complexity**: MEDIUM  
**Risk of Changes**: LOW (mostly additive changes)