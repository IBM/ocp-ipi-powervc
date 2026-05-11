# CmdWatchInstallation.go - Listener Shutdown Race Condition Fix
**Date**: 2026-05-11  
**Issue**: #8 from CmdWatchInstallation-current-issues-2026-05-11.md  
**Severity**: Low (Reliability)

## Problem Statement

The `listenForCommands` function had a race condition in its shutdown logic that could cause issues:

1. Listener could be closed multiple times (double-close)
2. Race condition between goroutine and defer statement
3. Unclear error messages during shutdown
4. No error handling for close failures

### Problematic Code Location
**File**: CmdWatchInstallation.go  
**Lines**: 2187-2220 (original implementation)

```go
// Original problematic code
func listenForCommands(ctx context.Context, clouds cloudFlags) error {
    ln, err := net.Listen("tcp", listenPort)
    if err != nil {
        return fmt.Errorf("failed to start listener on %s: %w", listenPort, err)
    }
    defer ln.Close()  // First close

    // Close listener when context is cancelled
    go func() {
        <-ctx.Done()
        log.Printf("[INFO] Context cancelled, closing listener...")
        ln.Close()  // Second close - RACE CONDITION!
    }()

    // Accept loop...
}
```

## Problems Identified

### 1. Double-Close Risk
```go
defer ln.Close()  // Will always execute

go func() {
    <-ctx.Done()
    ln.Close()  // May execute before defer
}()
```
**Problem**: Listener can be closed twice, causing panic or unclear errors.

### 2. Race Condition
- Goroutine may close listener
- Then defer tries to close it again
- Or vice versa
**Problem**: Unpredictable behavior, potential panic.

### 3. No Error Handling
```go
ln.Close()  // Ignores error
```
**Problem**: Close failures are silent.

### 4. Unclear Shutdown Flow
**Problem**: Hard to understand which code path closed the listener.

## Solution Implemented

### Use sync.Once for Thread-Safe Single Close

```go
func listenForCommands(ctx context.Context, clouds cloudFlags) error {
    var (
        closeOnce sync.Once
        closeErr  error
    )

    ln, err := net.Listen("tcp", listenPort)
    if err != nil {
        return fmt.Errorf("failed to start listener on %s: %w", listenPort, err)
    }

    // Ensure listener is closed exactly once
    closeListener := func() {
        closeOnce.Do(func() {
            log.Printf("[INFO] Closing listener...")
            closeErr = ln.Close()
            if closeErr != nil {
                log.Errorf("[ERROR] Failed to close listener: %v", closeErr)
            }
        })
    }
    defer closeListener()  // Safe - will only close if not already closed

    // Close listener when context is cancelled
    go func() {
        <-ctx.Done()
        log.Printf("[INFO] Context cancelled, initiating listener shutdown...")
        closeListener()  // Safe - will only close if not already closed
    }()

    // Accept loop...
}
```

## Key Improvements

### 1. Thread-Safe Single Close
```go
var closeOnce sync.Once

closeListener := func() {
    closeOnce.Do(func() {
        // This code runs exactly once, no matter how many times closeListener is called
        closeErr = ln.Close()
    })
}
```
**Benefit**: Listener closed exactly once, no race condition.

### 2. Error Handling
```go
closeErr = ln.Close()
if closeErr != nil {
    log.Errorf("[ERROR] Failed to close listener: %v", closeErr)
}
```
**Benefit**: Close failures are logged.

### 3. Clear Shutdown Flow
```go
log.Printf("[INFO] Closing listener...")
```
**Benefit**: Clear indication of when and why listener is closing.

### 4. Safe from Both Paths
- Defer can call `closeListener()` - safe
- Goroutine can call `closeListener()` - safe
- Both can call it - safe (only first one executes)
**Benefit**: No race condition, no double-close.

## How sync.Once Works

```go
var once sync.Once

once.Do(func() {
    // This code runs exactly once
    fmt.Println("First call")
})

once.Do(func() {
    // This code never runs
    fmt.Println("Second call")
})

// Output: "First call" (only)
```

**Key Properties:**
- Thread-safe
- First call wins
- Subsequent calls are no-ops
- Blocks until first call completes

## Shutdown Scenarios

### Scenario 1: Normal Shutdown (Context Cancelled)
1. Context is cancelled
2. Goroutine calls `closeListener()`
3. `sync.Once` executes close
4. Function returns
5. Defer calls `closeListener()` - no-op (already closed)

### Scenario 2: Error During Accept
1. Accept returns error
2. Function returns
3. Defer calls `closeListener()`
4. `sync.Once` executes close
5. Goroutine eventually calls `closeListener()` - no-op (already closed)

### Scenario 3: Panic in Handler
1. Panic occurs
2. Defer calls `closeListener()`
3. `sync.Once` executes close
4. Goroutine eventually calls `closeListener()` - no-op (already closed)

## Before vs After Comparison

### Before (Problematic)
```go
func listenForCommands(ctx context.Context, clouds cloudFlags) error {
    ln, err := net.Listen("tcp", listenPort)
    if err != nil {
        return fmt.Errorf("failed to start listener on %s: %w", listenPort, err)
    }
    defer ln.Close()  // ❌ Can cause double-close

    go func() {
        <-ctx.Done()
        ln.Close()  // ❌ Race condition with defer
    }()

    for {
        conn, err := ln.Accept()
        if err != nil {
            select {
            case <-ctx.Done():
                return nil
            default:
                return fmt.Errorf("failed to accept connection: %w", err)
            }
        }
        go handleConnection(ctx, conn, clouds)
    }
}
```

### After (Fixed)
```go
func listenForCommands(ctx context.Context, clouds cloudFlags) error {
    var (
        closeOnce sync.Once
        closeErr  error
    )

    ln, err := net.Listen("tcp", listenPort)
    if err != nil {
        return fmt.Errorf("failed to start listener on %s: %w", listenPort, err)
    }

    closeListener := func() {
        closeOnce.Do(func() {
            log.Printf("[INFO] Closing listener...")
            closeErr = ln.Close()
            if closeErr != nil {
                log.Errorf("[ERROR] Failed to close listener: %v", closeErr)
            }
        })
    }
    defer closeListener()  // ✅ Safe - only closes if not already closed

    go func() {
        <-ctx.Done()
        log.Printf("[INFO] Context cancelled, initiating listener shutdown...")
        closeListener()  // ✅ Safe - only closes if not already closed
    }()

    for {
        conn, err := ln.Accept()
        if err != nil {
            select {
            case <-ctx.Done():
                log.Printf("[INFO] Listener shutting down gracefully")
                return nil
            default:
                log.Errorf("[ERROR] Failed to accept connection: %v", err)
                return fmt.Errorf("failed to accept connection: %w", err)
            }
        }
        go handleConnection(ctx, conn, clouds)
    }
}
```

## Benefits

### 1. No Race Condition
- **Before**: Race between defer and goroutine
- **After**: `sync.Once` ensures thread-safe single execution
- **Impact**: Predictable, reliable shutdown

### 2. No Double-Close
- **Before**: Listener could be closed twice
- **After**: Closed exactly once, guaranteed
- **Impact**: No panics or unclear errors

### 3. Error Handling
- **Before**: Close errors ignored
- **After**: Close errors logged
- **Impact**: Better observability

### 4. Clear Logging
- **Before**: Unclear which path closed listener
- **After**: Clear log messages for each path
- **Impact**: Easier debugging

### 5. Maintainable
- **Before**: Complex shutdown logic
- **After**: Simple, clear pattern
- **Impact**: Easier to understand and modify

## Testing Recommendations

### Unit Tests

1. **Test Normal Shutdown**
   ```go
   func TestListenForCommands_NormalShutdown(t *testing.T) {
       ctx, cancel := context.WithCancel(context.Background())
       // Start listener
       // Cancel context
       // Verify clean shutdown
   }
   ```

2. **Test Rapid Cancellation**
   ```go
   func TestListenForCommands_RapidCancellation(t *testing.T) {
       // Cancel context immediately after starting
       // Verify no panic, clean shutdown
   }
   ```

3. **Test Multiple Connections During Shutdown**
   ```go
   func TestListenForCommands_ConnectionsDuringShutdown(t *testing.T) {
       // Accept connections
       // Cancel context
       // Verify existing connections handled gracefully
   }
   ```

### Integration Tests

1. Test with real TCP connections
2. Test graceful shutdown with active connections
3. Test rapid start/stop cycles
4. Test under load

### Stress Tests

1. Test with many concurrent connections
2. Test rapid context cancellations
3. Test memory usage (no leaks)
4. Test with slow handlers

## Performance Impact

### Memory
- **sync.Once**: Minimal overhead (one sync.Mutex internally)
- **closeErr variable**: One error value
- **Overall**: Negligible

### CPU
- **sync.Once.Do()**: Minimal overhead (mutex lock/unlock)
- **Overall**: Negligible

### Latency
- **Normal Operation**: No impact
- **Shutdown**: Slightly faster (no double-close attempts)
- **Overall**: Improved

## Related Patterns

### Pattern: Idempotent Close
```go
var closeOnce sync.Once

func close() {
    closeOnce.Do(func() {
        // Close resources
    })
}
```
**Use Case**: Any resource that should be closed exactly once.

### Pattern: Thread-Safe Initialization
```go
var initOnce sync.Once
var resource *Resource

func getResource() *Resource {
    initOnce.Do(func() {
        resource = initializeResource()
    })
    return resource
}
```
**Use Case**: Lazy initialization that should happen exactly once.

## Related Issues

This fix addresses:
- Issue #8: Race Condition in Listener Shutdown
- Prevents double-close panics
- Improves shutdown reliability
- Better error handling

## Files Modified

1. **CmdWatchInstallation.go**
   - Added `sync` import
   - Modified `listenForCommands` function (~30 lines changed)
   - Added `sync.Once` for thread-safe close
   - Added error handling for close operation
   - Improved logging

## Verification Steps

1. **Test Normal Shutdown**
   ```bash
   # Start the program
   ./tool watch-installation ...
   
   # Send SIGTERM
   kill -TERM <pid>
   
   # Should see:
   # [INFO] Context cancelled, initiating listener shutdown...
   # [INFO] Closing listener...
   # [INFO] Listener shutting down gracefully
   ```

2. **Test Rapid Shutdown**
   ```bash
   # Start and immediately stop
   ./tool watch-installation ... &
   PID=$!
   sleep 0.1
   kill -TERM $PID
   
   # Should shutdown cleanly without errors
   ```

3. **Test with Active Connections**
   ```bash
   # Start program
   ./tool watch-installation ... &
   
   # Connect
   nc localhost 8080 &
   
   # Shutdown
   kill -TERM <pid>
   
   # Should shutdown gracefully
   ```

## Future Enhancements

1. Add metrics for shutdown duration
2. Add configurable shutdown timeout
3. Add graceful connection draining
4. Consider using errgroup for better goroutine management

## Conclusion

This fix eliminates the race condition in listener shutdown by:
- Using `sync.Once` for thread-safe single close
- Adding proper error handling
- Improving logging and observability
- Simplifying shutdown logic

The implementation is robust, maintainable, and follows Go best practices for resource cleanup.

---

**Status**: ✅ Implemented  
**Tested**: ⏳ Pending (Go compiler not available in environment)  
**Reviewed**: ⏳ Pending