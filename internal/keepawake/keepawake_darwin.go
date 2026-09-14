//go:build darwin

package keepawake

/*
#cgo LDFLAGS: -framework IOKit -framework CoreFoundation
#include <stdlib.h>
#include <IOKit/pwr_mgt/IOPMLib.h>
#include <CoreFoundation/CoreFoundation.h>

// ogcodeHoldAssertion creates a PreventUserIdleDisplaySleep assertion: the
// screen stays on and, since the system cannot idle-sleep while the display is
// up, idle system sleep is prevented too. Returns 0 on success and writes the
// assertion id to *outID; otherwise returns the non-zero IOReturn. The
// kIOReturnSuccess comparison stays in C so the Go side need not rely on cgo
// exposing that macro. IOPMAssertionID is a uint32_t, so unsigned int carries it.
static int ogcodeHoldAssertion(const char *name, unsigned int *outID) {
    CFStringRef cfName = CFStringCreateWithCString(kCFAllocatorDefault, name, kCFStringEncodingUTF8);
    if (cfName == NULL) {
        // A missing name would fail the create; fall back to a constant. The
        // CFRetain balances the CFRelease below so the constant is not freed.
        cfName = CFSTR("ogcode");
        CFRetain(cfName);
    }
    IOPMAssertionID id = 0;
    IOReturn r = IOPMAssertionCreateWithName(
        kIOPMAssertionTypePreventUserIdleDisplaySleep,
        kIOPMAssertionLevelOn,
        cfName,
        &id);
    CFRelease(cfName);
    if (r != kIOReturnSuccess) {
        return (int)r;
    }
    *outID = (unsigned int)id;
    return 0;
}

static void ogcodeReleaseAssertion(unsigned int id) {
    IOPMAssertionRelease((IOPMAssertionID)id);
}
*/
import "C"

import "unsafe"

func platformHold(reason string) (uintptr, bool) {
	cName := C.CString(reason)
	defer C.free(unsafe.Pointer(cName))
	var id C.uint
	if C.ogcodeHoldAssertion(cName, &id) != 0 {
		return 0, false
	}
	return uintptr(id), true
}

func platformRelease(handle uintptr) {
	C.ogcodeReleaseAssertion(C.uint(handle))
}
