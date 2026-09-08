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
// P7.3 only. CGSCopyManagedDisplayForSpace names the display a Space lives on; SetCurrentSpace makes
// it current on that display; Show/HideSpaces drive the visual transition and are best-effort -- the
// pointer moves without them, some macOS versions just do not animate the reveal.
typedef CFStringRef (*gt_fn_display_for_space)(gt_cgs_connection, uint64_t);
typedef void (*gt_fn_set_current_space)(gt_cgs_connection, CFStringRef, uint64_t);
typedef void (*gt_fn_show_hide_spaces)(gt_cgs_connection, CFArrayRef);

static struct {
    bool loaded;
    gt_fn_main_connection main_connection;
    gt_fn_active_space active_space;
    gt_fn_menubar_display menubar_display;
    gt_fn_display_space display_space;
    gt_fn_spaces_for_windows spaces_for_windows;
    gt_fn_managed_display_spaces managed_display_spaces;
    gt_fn_display_for_space display_for_space;
    gt_fn_set_current_space set_current_space;
    gt_fn_show_hide_spaces show_spaces;
    gt_fn_show_hide_spaces hide_spaces;
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
        // P7.3 write path. Loaded here but not part of g_sl.loaded's minimum: the read queries above
        // must work for the rest of the package, the Space switch is an optional extra and
        // gt_space_switch_to_window checks its own symbols.
        g_sl.display_for_space = (gt_fn_display_for_space)
            sl_sym(h, "CGSCopyManagedDisplayForSpace", "SLSCopyManagedDisplayForSpace");
        g_sl.set_current_space = (gt_fn_set_current_space)
            sl_sym(h, "CGSManagedDisplaySetCurrentSpace", "SLSManagedDisplaySetCurrentSpace");
        g_sl.show_spaces = (gt_fn_show_hide_spaces)
            sl_sym(h, "CGSShowSpaces", "SLSShowSpaces");
        g_sl.hide_spaces = (gt_fn_show_hide_spaces)
            sl_sym(h, "CGSHideSpaces", "SLSHideSpaces");
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

// ---------------------------------------------------------------------------
// P7.3 -- the one write here, and a private-API bet (see this file's header comment)
// ---------------------------------------------------------------------------

// Builds a one-element CFArray holding an int64 CFNumber, or NULL. Caller releases.
static CFArrayRef one_number_array(uint64_t v) {
    CFNumberRef n = CFNumberCreate(NULL, kCFNumberSInt64Type, &v);
    if (!n) return NULL;
    CFArrayRef arr = CFArrayCreate(NULL, (const void *[]){ n }, 1, &kCFTypeArrayCallBacks);
    CFRelease(n);
    return arr;
}

// If window wid is on a Space other than the one the user is looking at, make that Space current so
// gt_window_raise can then resolve the window through Accessibility (kAXWindowsAttribute only lists
// the current Space -- D20/D46). Returns 1 only if gt_current_space() actually changed to the target;
// the caller retries the AX resolve on 1 and falls through to P7.1's app-only activation on 0.
//
// Every step is guarded: a return of 0 covers SkyLight absent, the private write symbols absent, the
// window's Space unknown or already current, or the switch not taking. Nothing here blocks -- the
// calls are WindowServer round trips, not waits -- so a wedged switch degrades rather than hangs.
//
// PRIVATE API and NOT VERIFIED: this machine has one Space (D24), so the cross-Space path is reasoned
// from how yabai and Hammerspoon drive CGSManagedDisplaySetCurrentSpace, not observed. A macOS
// release that drops these symbols turns this back into P7.1's behaviour. See docs/tasks/P7.3.md; a
// human confirms it under V6.2.
int gt_space_switch_to_window(uint32_t wid) {
    if (!sl_load()) return 0;
    gt_cgs_connection cid = g_sl.main_connection ? g_sl.main_connection() : 0;
    if (!cid || !g_sl.spaces_for_windows || !g_sl.set_current_space || !g_sl.display_for_space) {
        return 0;
    }

    uint64_t current = gt_current_space();
    if (current == 0) return 0;

    // Which Space is the window on? Ask for the full mask so a window on another Space still reports
    // it. If any reported Space is the current one the window is already visible -- nothing to do.
    uint64_t target = 0;
    int32_t w = (int32_t)wid;
    CFNumberRef num = CFNumberCreate(NULL, kCFNumberSInt32Type, &w);
    if (!num) return 0;
    CFArrayRef one = CFArrayCreate(NULL, (const void *[]){ num }, 1, &kCFTypeArrayCallBacks);
    CFRelease(num);
    if (!one) return 0;
    CFArrayRef spaces = g_sl.spaces_for_windows(cid, GT_SPACE_MASK_ALL, one);
    CFRelease(one);
    if (!spaces) return 0;
    CFIndex n = CFArrayGetCount(spaces);
    for (CFIndex i = 0; i < n; i++) {
        CFNumberRef s = (CFNumberRef)CFArrayGetValueAtIndex(spaces, i);
        if (!s || CFGetTypeID(s) != CFNumberGetTypeID()) continue;
        uint64_t v = 0;
        if (!CFNumberGetValue(s, kCFNumberSInt64Type, &v) || v == 0) continue;
        if (v == current) { target = 0; break; }  // already on-screen
        if (target == 0) target = v;
    }
    CFRelease(spaces);
    if (target == 0 || target == current) return 0;

    CFStringRef display = g_sl.display_for_space(cid, target);
    if (!display) return 0;

    // Show/Hide drive the reveal animation and are best-effort: without them the current-Space
    // pointer still moves, some macOS versions just cut rather than animate.
    if (g_sl.hide_spaces) {
        CFArrayRef arr = one_number_array(current);
        if (arr) { g_sl.hide_spaces(cid, arr); CFRelease(arr); }
    }
    g_sl.set_current_space(cid, display, target);
    if (g_sl.show_spaces) {
        CFArrayRef arr = one_number_array(target);
        if (arr) { g_sl.show_spaces(cid, arr); CFRelease(arr); }
    }
    CFRelease(display);

    // Confirm the pointer moved before telling the caller the retry is worth it.
    return gt_current_space() == target ? 1 : 0;
}
