# VMs.go Issue #6 Fix - 2026-05-12

## Issue Description
**Issue #6: SSH Check Ignores Context**
- **Location:** Lines 249-255 (VMs.go), keyscanServer function (Utils.go)
- **Severity:** Medium
- **Problem:** The `keyscanServer()` function was called with a context, but the underlying command execution created its own context, ignoring the passed context for cancellation and timeout control.

## Root Cause
The call chain had a context propagation break:
1. `ClusterStatus()` creates context with timeout
2. Passes context to `keyscanServer(ctx, ipAddress, true)`
3. `keyscanServer()` uses `wait.ExponentialBackoffWithContext(ctx, ...)` (good)
4. BUT: Inside the retry loop, it calls `runSplitCommandNoErr()` which creates its own context:
   ```go
   ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
   ```
5. This new context ignores the parent context passed from `ClusterStatus()`

## Impact
- **Context Cancellation Ignored:** SSH checks may continue even after parent context is cancelled
- **Timeout Not Respected:** SSH operations use their own timeout instead of respecting the parent timeout
- **Resource Leaks:** Goroutines may hang beyond intended timeout
- **Unresponsive Operations:** Cannot interrupt long-running SSH scans

## Solution Implemented

### 1. Created Context-Aware Command Function (Run.go)
Added new function `runSplitCommandNoErrWithContext` that accepts a context parameter:

```go
func runSplitCommandNoErrWithContext(ctx context.Context, acmdline []string, silent bool) ([]byte, error) {
    if len(acmdline) == 0 {
        return nil, fmt.Errorf("command array cannot be empty")
    }

    cmd, err := createCommand(ctx, acmdline)
    // ... rest of implementation
}
```

Modified existing `runSplitCommandNoErr` to use the new function:
```go
func runSplitCommandNoErr(acmdline []string, silent bool) ([]byte, error) {
    ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
    defer cancel()
    return runSplitCommandNoErrWithContext(ctx, acmdline, silent)
}
```

### 2. Updated keyscanServer to Use Context (Utils.go)
Modified the retry loop to pass the context to command execution:

**Before:**
```go
err := wait.ExponentialBackoffWithContext(ctx, backoff, func(context.Context) (bool, error) {
    outb, err := runSplitCommandNoErr([]string{"ssh-keyscan", ipAddress}, silent)
    // ...
})
```

**After:**
```go
err := wait.ExponentialBackoffWithContext(ctx, backoff, func(retryCtx context.Context) (bool, error) {
    outb, err := runSplitCommandNoErrWithContext(retryCtx, []string{"ssh-keyscan", ipAddress}, silent)
    // ...
})
```

## Benefits
1. **Proper Context Propagation:** Context flows from ClusterStatus → keyscanServer → command execution
2. **Respects Cancellation:** SSH operations can be cancelled when parent context is cancelled
3. **Timeout Control:** SSH operations respect the timeout set by the caller
4. **Resource Management:** No goroutine leaks from hanging SSH operations
5. **Backward Compatible:** Existing `runSplitCommandNoErr` calls still work with default timeout

## Context Flow After Fix
```
ClusterStatus()
  ↓ (creates context with timeout)
  ↓
keyscanServer(ctx, ...)
  ↓ (uses context in retry loop)
  ↓
wait.ExponentialBackoffWithContext(ctx, ...)
  ↓ (passes retry context)
  ↓
runSplitCommandNoErrWithContext(retryCtx, ...)
  ↓ (uses context for command)
  ↓
exec.CommandContext(ctx, ...)
  ✓ (respects context cancellation and timeout)
```

## Testing Recommendations
1. **Context Cancellation Test:**
   - Cancel context during SSH scan
   - Verify operation stops promptly
   
2. **Timeout Test:**
   - Set short timeout in parent context
   - Verify SSH scan respects timeout
   
3. **Retry Test:**
   - Test with unreachable host
   - Verify retries stop when context expires
   
4. **Success Path Test:**
   - Test with reachable SSH server
   - Verify normal operation still works

5. **Backward Compatibility Test:**
   - Verify existing `runSplitCommandNoErr` calls still work
   - Check that default timeout is still applied

## Related Issues
This fix addresses issue #6 and improves the overall context handling in the codebase:
- Issue #1: Context not passed properly to helper functions (partially related)
- Issue #4: Deferred cancel called after early returns (fixed separately)

## Files Modified
1. **Run.go:**
   - Added `runSplitCommandNoErrWithContext()` function
   - Modified `runSplitCommandNoErr()` to use new function
   
2. **Utils.go:**
   - Modified `keyscanServer()` to use `runSplitCommandNoErrWithContext()`
   - Changed retry loop to pass context to command execution

## Status
✅ **FIXED** - SSH operations now properly respect context cancellation and timeouts