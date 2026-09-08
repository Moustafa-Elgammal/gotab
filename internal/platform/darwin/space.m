// The SkyLight side of the platform layer. See space.h for the contract.
//
// Compiled without ARC, like shim.m, and it holds nothing ARC would manage anyway: CoreFoundation
// objects and function pointers.
#import <Foundation/Foundation.h>
#import <CoreGraphics/CoreGraphics.h>
#include <dlfcn.h>

#include "space.h"

// ---------------------------------------------------------------------------
// SkyLight, resolved at runtime
// ---------------------------------------------------------------------------

// dlopen/dlsym rather than linking the framework, and the reason is honesty rather than convenience.
//
// Linking SkyLight would need -F into /System/Library/PrivateFrameworks plus CGO_LDFLAGS_ALLOW, and
// it would make a missing symbol a LAUNCH failure: the whole switcher refuses to start because a
// private API moved. Resolved here, a missing symbol makes exactly one feature return "unknown",
// which is a state core.SpaceID already models. Spaces are worth degrading for; they are not worth
// the binary refusing to run.
//
// The symbols have carried the CGS prefix since well before Mission Control and SkyLight also exports
// SLS-prefixed aliases for all of them (both were present on this machine). CGS is the older and
// better-attested spelling and every window manager that works -- yabai, Hammerspoon, and others --
// uses it, so that is what is asked for first.
typedef int gt_cgs_connection;

typedef gt_cgs_connection (*gt_fn_main_connection)(void);
typedef uint64_t (*gt_fn_active_space)(gt_cgs_connection);
typedef CFStringRef (*gt_fn_menubar_display)(gt_cgs_connection);
typedef uint64_t (*gt_fn_display_space)(gt_cgs_connection, CFStringRef);
typedef CFArrayRef (*gt_fn_spaces_for_windows)(gt_cgs_connection, uint32_t, CFArrayRef);
typedef CFArrayRef (*gt_fn_managed_display_spaces)(gt_cgs_connection);

static struct {
    bool loaded;
    gt_fn_main_connection main_connection;
    gt_fn_active_space active_space;
    gt_fn_menubar_display menubar_display;
    gt_fn_display_space display_space;
    gt_fn_spaces_for_windows spaces_for_windows;
    gt_fn_managed_display_spaces managed_display_spaces;
} g_sl;

// Both spellings of one symbol. Returns NULL if neither is there, which every caller treats as
// "unknown" rather than as a reason to fail.
static void *sl_sym(void *handle, const char *cgs_name, const char *sls_name) {
    void *p = dlsym(handle, cgs_name);
    return p ? p : dlsym(handle, sls_name);
}

// Loads SkyLight once and reports whether the minimum -- a connection id -- is available. Individual
// callers still check the specific symbol they need, because the set is not all-or-nothing.
static bool sl_load(void) {
    static dispatch_once_t once;
    dispatch_once(&once, ^{
        // RTLD_LOCAL so these symbols do not join the global namespace and shadow anything.
        // No dlclose: the handle is process-lifetime by design, and unloading a framework AppKit may
        // also have loaded is a way to crash on exit for no benefit.
        void *h = dlopen("/System/Library/PrivateFrameworks/SkyLight.framework/SkyLight",
                         RTLD_LAZY | RTLD_LOCAL);
        if (!h) return;
        g_sl.main_connection = (gt_fn_main_connection)
            sl_sym(h, "CGSMainConnectionID", "SLSMainConnectionID");
        g_sl.active_space = (gt_fn_active_space)
            sl_sym(h, "CGSGetActiveSpace", "SLSGetActiveSpace");
        g_sl.menubar_display = (gt_fn_menubar_display)
            sl_sym(h, "CGSCopyActiveMenuBarDisplayIdentifier", "SLSCopyActiveMenuBarDisplayIdentifier");
        g_sl.display_space = (gt_fn_display_space)
            sl_sym(h, "CGSManagedDisplayGetCurrentSpace", "SLSManagedDisplayGetCurrentSpace");
        g_sl.spaces_for_windows = (gt_fn_spaces_for_windows)
            sl_sym(h, "CGSCopySpacesForWindows", "SLSCopySpacesForWindows");
        g_sl.managed_display_spaces = (gt_fn_managed_display_spaces)
            sl_sym(h, "CGSCopyManagedDisplaySpaces", "SLSCopyManagedDisplaySpaces");
        g_sl.loaded = g_sl.main_connection != NULL;
    });
    return g_sl.loaded;
}

// The WindowServer connection for this process, or 0 if there is none. 0 is a real state and not a
// bug: a process with no window server session -- a launchd daemon, a login-window context -- gets
// exactly this, and every caller must degrade rather than assume.
static gt_cgs_connection sl_connection(void) {
    if (!sl_load()) return 0;
    return g_sl.main_connection();
}

// The mask CGSCopySpacesForWindows takes: current | others | user. Asking for anything narrower makes
// the answer depend on where the user happens to be standing, which is the opposite of what a
// switcher wants -- a window on another Space must still report that Space.
enum { GT_SPACE_MASK_ALL = 7 };

// ---------------------------------------------------------------------------
// Queries
// ---------------------------------------------------------------------------

uint64_t gt_current_space(void) {
    gt_cgs_connection cid = sl_connection();
    if (!cid) return 0;

    // Per-display first. With Displays Have Separate Spaces there is no single current Space, and the
    // one that matters is the display holding the menubar, because that is where a summoned panel
    // appears. Measured agreeing with the connection-wide answer on a single-display machine.
    if (g_sl.menubar_display && g_sl.display_space) {
        CFStringRef display = g_sl.menubar_display(cid);
        if (display) {
            uint64_t space = g_sl.display_space(cid, display);
            CFRelease(display);
            if (space) return space;
        }
    }
    if (g_sl.active_space) return g_sl.active_space(cid);
    return 0;
}

gt_status gt_spaces_of(const uint32_t *ids, int32_t n, uint64_t *out) {
    if (n < 0) return GT_ERR_INTERNAL;
    if (n > 0 && (!ids || !out)) return GT_ERR_INTERNAL;

    // Cleared before anything can fail, so a caller that ignores the status still sees "unknown"
    // rather than whatever was in the buffer last time.
    for (int32_t i = 0; i < n; i++) out[i] = 0;
    if (n == 0) return GT_OK;

    gt_cgs_connection cid = sl_connection();
    if (!cid || !g_sl.spaces_for_windows) return GT_ERR_UNAVAILABLE;

    // Read once, outside the loop: it is another WindowServer round trip and it cannot change
    // meaningfully in the microseconds this takes.
    uint64_t current = gt_current_space();

    // One reused single-element array rather than one per window. The call takes a CFArray and the
    // allocation would otherwise be N of them for no gain.
    CFMutableArrayRef one = CFArrayCreateMutable(NULL, 1, &kCFTypeArrayCallBacks);
    if (!one) return GT_ERR_INTERNAL;

    for (int32_t i = 0; i < n; i++) {
        int32_t wid = (int32_t)ids[i];
        CFNumberRef num = CFNumberCreate(NULL, kCFNumberSInt32Type, &wid);
        if (!num) continue;
        CFArrayRemoveAllValues(one);
        CFArrayAppendValue(one, num);
        CFRelease(num);

        CFArrayRef spaces = g_sl.spaces_for_windows(cid, GT_SPACE_MASK_ALL, one);
        if (!spaces) continue;  // stays 0: unknown

        CFIndex count = CFArrayGetCount(spaces);
        for (CFIndex j = 0; j < count; j++) {
            CFNumberRef s = (CFNumberRef)CFArrayGetValueAtIndex(spaces, j);
            if (!s || CFGetTypeID(s) != CFNumberGetTypeID()) continue;
            uint64_t v = 0;
            if (!CFNumberGetValue(s, kCFNumberSInt64Type, &v) || v == 0) continue;
            // First answer wins, unless a later one is the Space the user is on -- a window assigned
            // to All Desktops is on every Space, and reporting one the user is not looking at would
            // make the switcher treat a visible window as off-Space.
            if (out[i] == 0 || v == current) out[i] = v;
            if (v == current) break;
        }
        CFRelease(spaces);
    }

    CFRelease(one);
    return GT_OK;
}

// Reads a CFNumber out of a CFDictionary, 0 if it is absent or not a number.
static uint64_t dict_uint64(CFDictionaryRef d, CFStringRef key) {
    CFNumberRef n = (CFNumberRef)CFDictionaryGetValue(d, key);
    if (!n || CFGetTypeID(n) != CFNumberGetTypeID()) return 0;
    uint64_t v = 0;
    return CFNumberGetValue(n, kCFNumberSInt64Type, &v) ? v : 0;
}

gt_status gt_space_list(uint64_t *buf, int32_t cap, int32_t *out_n, int32_t *out_total) {
    if (!buf || cap < 0 || !out_n || !out_total) return GT_ERR_INTERNAL;
    *out_n = 0;
    *out_total = 0;

    gt_cgs_connection cid = sl_connection();
    if (!cid || !g_sl.managed_display_spaces) return GT_ERR_UNAVAILABLE;

    CFArrayRef displays = g_sl.managed_display_spaces(cid);
    if (!displays) return GT_ERR_UNAVAILABLE;

    int32_t stored = 0, total = 0;
    CFIndex dcount = CFArrayGetCount(displays);
    for (CFIndex i = 0; i < dcount; i++) {
        CFDictionaryRef display = (CFDictionaryRef)CFArrayGetValueAtIndex(displays, i);
        if (!display || CFGetTypeID(display) != CFDictionaryGetTypeID()) continue;
        CFArrayRef spaces = (CFArrayRef)CFDictionaryGetValue(display, CFSTR("Spaces"));
        if (!spaces || CFGetTypeID(spaces) != CFArrayGetTypeID()) continue;

        CFIndex scount = CFArrayGetCount(spaces);
        for (CFIndex j = 0; j < scount; j++) {
            CFDictionaryRef space = (CFDictionaryRef)CFArrayGetValueAtIndex(spaces, j);
            if (!space || CFGetTypeID(space) != CFDictionaryGetTypeID()) continue;
            uint64_t id64 = dict_uint64(space, CFSTR("id64"));
            if (id64 == 0) id64 = dict_uint64(space, CFSTR("ManagedSpaceID"));
            if (id64 == 0) continue;  // unnameable is not a Space this package will invent one for

            total++;
            if (stored >= cap) continue;  // count it, drop it; the caller grows and retries
            buf[stored++] = id64;
        }
    }

    CFRelease(displays);
    *out_n = stored;
    *out_total = total;
    return GT_OK;
}
