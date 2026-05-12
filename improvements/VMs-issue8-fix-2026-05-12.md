# VMs.go Issue #8 Fix - 2026-05-12

## Issue Description
**Issue #8: Magic String Constants**
- **Location:** Lines 239-240, 258, 263 (original)
- **Severity:** Low
- **Problem:** Inconsistent use of constants vs hardcoded strings for "N/A" values, and semantic confusion about what "N/A" means in different contexts.

## Root Cause
The code had several issues with "N/A" string usage:

1. **Semantic Confusion:** `sshStatusNA` constant was used for multiple purposes:
   - SSH status (intended purpose)
   - MAC address when not available (line 244)
   - IP address when not available (line 245)

2. **Hardcoded String:** Hypervisor name used hardcoded `"N/A"` instead of a constant (line 263)

3. **Inconsistency:** Mixed use of constants and hardcoded strings for the same display value

## Impact
- **Maintainability:** Harder to change display format consistently
- **Semantic Clarity:** Confusing to use SSH status constant for network addresses
- **Code Quality:** Inconsistent patterns make code harder to understand
- **Potential Bugs:** If SSH status format changes, it would incorrectly affect network status display

## Solution Implemented

### Created Semantic Constants
Introduced separate constants for different "N/A" meanings:

```go
const (
    // VMsName is the display name for the Virtual Machines service
    VMsName = "Virtual Machines"

    // Status constants for various "not available" scenarios
    statusNotAvailable = "N/A"

    // SSH status constants
    sshStatusNA    = statusNotAvailable
    sshStatusAlive = "ALIVE"
    sshStatusDead  = "DEAD"

    // Network status constants
    networkStatusNA = statusNotAvailable

    // Hypervisor status constants
    hypervisorStatusNA = statusNotAvailable
)
```

### Updated Code to Use Appropriate Constants

**1. Network Address Status (lines 244-245, 254):**
```go
// Before:
macAddress = sshStatusNA
ipAddress = sshStatusNA
if ipAddress != sshStatusNA {

// After:
macAddress = networkStatusNA
ipAddress = networkStatusNA
if ipAddress != networkStatusNA {
```

**2. Hypervisor Name Status (line 267):**
```go
// Before:
hypervisorName := "N/A"

// After:
hypervisorName := hypervisorStatusNA
```

## Benefits

### 1. Semantic Clarity
- `sshStatusNA` - Used only for SSH connection status
- `networkStatusNA` - Used for MAC/IP addresses
- `hypervisorStatusNA` - Used for hypervisor names
- Clear intent for each constant's purpose

### 2. Maintainability
- Single source of truth: `statusNotAvailable = "N/A"`
- Easy to change display format in one place
- All constants automatically update if base value changes

### 3. Consistency
- No more hardcoded strings
- Uniform approach across the file
- Easier to search and refactor

### 4. Type Safety
- Constants prevent typos
- IDE autocomplete helps developers
- Compiler catches undefined constants

### 5. Future Flexibility
- Can easily add different display formats per context
- Example: Could change hypervisor to show "Unknown" instead of "N/A"
- Won't affect SSH or network status displays

## Design Pattern
The solution uses a hierarchical constant pattern:
```
statusNotAvailable (base value)
    ↓
    ├── sshStatusNA (SSH context)
    ├── networkStatusNA (Network context)
    └── hypervisorStatusNA (Hypervisor context)
```

This allows:
- Semantic meaning at usage sites
- Centralized value management
- Easy context-specific customization if needed

## Testing Recommendations
1. **Display Test:** Verify all "N/A" values still display correctly
2. **Consistency Test:** Check that all contexts show "N/A" as expected
3. **Refactoring Test:** Try changing `statusNotAvailable` to verify all uses update
4. **Context Test:** Verify each constant is used in appropriate context

## Example Usage in Output
```
Server: test-cluster-master-0
  Status: ACTIVE
  Power: RUNNING
  MAC: 52:54:00:12:34:56
  IP: 192.168.1.100
  SSH: ALIVE
  Hypervisor: hypervisor-01

Server: test-cluster-worker-0
  Status: ACTIVE
  Power: RUNNING
  MAC: N/A          ← networkStatusNA
  IP: N/A           ← networkStatusNA
  SSH: N/A          ← sshStatusNA (because no IP)
  Hypervisor: N/A   ← hypervisorStatusNA
```

## Related Issues
This fix improves code quality and addresses:
- Issue #7: Inconsistent string comparison (related to string handling)
- Issue #12: Inconsistent error message format (related to consistency)

## Files Modified
- **VMs.go:**
  - Added `statusNotAvailable`, `networkStatusNA`, `hypervisorStatusNA` constants
  - Updated MAC/IP address assignments to use `networkStatusNA`
  - Updated IP address comparison to use `networkStatusNA`
  - Updated hypervisor name initialization to use `hypervisorStatusNA`

## Status
✅ **FIXED** - All "N/A" values now use semantic constants with clear intent