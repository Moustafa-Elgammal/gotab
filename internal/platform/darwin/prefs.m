// CFPreferences-backed settings I/O. Compiled without ARC like the rest of this package; every
// CF...Copy... result is released.
//
// CFPreferencesCopyAppValue / CFPreferencesSetAppValue rather than the four-argument form: the
// two-argument current-application domain is what `defaults read` / `defaults write <bundle-id>`
// address, so a value set here shows up there and vice versa.
#import <CoreFoundation/CoreFoundation.h>
#include <string.h>
#include "prefs.h"

static CFStringRef key_cf(const char *key) {
    return CFStringCreateWithCString(NULL, key, kCFStringEncodingUTF8);
}

// UTF-8 bytes of s into buf (cap-1 + NUL); *out_len is the untruncated length.
static void copy_cfstring(CFStringRef s, char *buf, int32_t cap, int32_t *out_len) {
    CFRange all = CFRangeMake(0, CFStringGetLength(s));
    CFIndex full = 0;
    CFStringGetBytes(s, all, kCFStringEncodingUTF8, 0, false, NULL, 0, &full);
    if (out_len) *out_len = (int32_t)full;
    if (!buf || cap <= 0) return;
    CFIndex used = 0;
    CFStringGetBytes(s, all, kCFStringEncodingUTF8, 0, false, (UInt8 *)buf, cap - 1, &used);
    buf[used] = 0;
}

int32_t gt_prefs_get_bool(const char *key, int32_t *out) {
    CFStringRef k = key_cf(key);
    CFPropertyListRef v = CFPreferencesCopyAppValue(k, kCFPreferencesCurrentApplication);
    CFRelease(k);
    if (!v) return 0;
    int32_t rc = -1;
    CFTypeID t = CFGetTypeID(v);
    if (t == CFBooleanGetTypeID()) {
        *out = CFBooleanGetValue((CFBooleanRef)v) ? 1 : 0;
        rc = 1;
    } else if (t == CFNumberGetTypeID()) {
        int64_t n = 0;
        CFNumberGetValue((CFNumberRef)v, kCFNumberSInt64Type, &n);
        *out = n != 0 ? 1 : 0;
        rc = 1;
    }
    CFRelease(v);
    return rc;
}

int32_t gt_prefs_get_i64(const char *key, int64_t *out) {
    CFStringRef k = key_cf(key);
    CFPropertyListRef v = CFPreferencesCopyAppValue(k, kCFPreferencesCurrentApplication);
    CFRelease(k);
    if (!v) return 0;
    int32_t rc = -1;
    CFTypeID t = CFGetTypeID(v);
    if (t == CFNumberGetTypeID()) {
        CFNumberGetValue((CFNumberRef)v, kCFNumberSInt64Type, out);
        rc = 1;
    } else if (t == CFBooleanGetTypeID()) {
        *out = CFBooleanGetValue((CFBooleanRef)v) ? 1 : 0;
        rc = 1;
    }
    CFRelease(v);
    return rc;
}

int32_t gt_prefs_get_str(const char *key, char *buf, int32_t cap, int32_t *out_len) {
    CFStringRef k = key_cf(key);
    CFPropertyListRef v = CFPreferencesCopyAppValue(k, kCFPreferencesCurrentApplication);
    CFRelease(k);
    if (!v) return 0;
    if (CFGetTypeID(v) != CFStringGetTypeID()) {
        CFRelease(v);
        return -1;
    }
    copy_cfstring((CFStringRef)v, buf, cap, out_len);
    CFRelease(v);
    return 1;
}

int32_t gt_prefs_get_strs(const char *key, char *buf, int32_t cap, int32_t *out_count, int32_t *out_len) {
    CFStringRef k = key_cf(key);
    CFPropertyListRef v = CFPreferencesCopyAppValue(k, kCFPreferencesCurrentApplication);
    CFRelease(k);
    if (!v) return 0;
    if (CFGetTypeID(v) != CFArrayGetTypeID()) {
        CFRelease(v);
        return -1;
    }
    CFArrayRef arr = (CFArrayRef)v;
    CFIndex n = CFArrayGetCount(arr);
    CFMutableStringRef joined = CFStringCreateMutable(NULL, 0);
    CFIndex kept = 0;
    for (CFIndex i = 0; i < n; i++) {
        CFTypeRef e = CFArrayGetValueAtIndex(arr, i);
        if (!e || CFGetTypeID(e) != CFStringGetTypeID()) continue;
        if (kept > 0) CFStringAppendCString(joined, "\n", kCFStringEncodingUTF8);
        CFStringAppend(joined, (CFStringRef)e);
        kept++;
    }
    copy_cfstring(joined, buf, cap, out_len);
    if (out_count) *out_count = (int32_t)kept;
    CFRelease(joined);
    CFRelease(v);
    return 1;
}

void gt_prefs_set_bool(const char *key, int32_t v) {
    CFStringRef k = key_cf(key);
    CFPreferencesSetAppValue(k, v ? kCFBooleanTrue : kCFBooleanFalse, kCFPreferencesCurrentApplication);
    CFRelease(k);
}

void gt_prefs_set_i64(const char *key, int64_t v) {
    CFStringRef k = key_cf(key);
    CFNumberRef n = CFNumberCreate(NULL, kCFNumberSInt64Type, &v);
    CFPreferencesSetAppValue(k, n, kCFPreferencesCurrentApplication);
    CFRelease(n);
    CFRelease(k);
}

void gt_prefs_set_str(const char *key, const char *v) {
    CFStringRef k = key_cf(key);
    CFStringRef s = CFStringCreateWithCString(NULL, v ? v : "", kCFStringEncodingUTF8);
    CFPreferencesSetAppValue(k, s, kCFPreferencesCurrentApplication);
    CFRelease(s);
    CFRelease(k);
}

void gt_prefs_set_strs(const char *key, const char *joined, int32_t count) {
    CFStringRef k = key_cf(key);
    if (count <= 0 || !joined || !*joined) {
        // An empty array, stored as such rather than removed, so Save() is a full record.
        CFArrayRef empty = CFArrayCreate(NULL, NULL, 0, &kCFTypeArrayCallBacks);
        CFPreferencesSetAppValue(k, empty, kCFPreferencesCurrentApplication);
        CFRelease(empty);
        CFRelease(k);
        return;
    }
    CFStringRef all = CFStringCreateWithCString(NULL, joined, kCFStringEncodingUTF8);
    CFArrayRef parts = CFStringCreateArrayBySeparatingStrings(NULL, all, CFSTR("\n"));
    CFPreferencesSetAppValue(k, parts, kCFPreferencesCurrentApplication);
    CFRelease(parts);
    CFRelease(all);
    CFRelease(k);
}

int32_t gt_prefs_sync(void) {
    return CFPreferencesAppSynchronize(kCFPreferencesCurrentApplication) ? 1 : 0;
}
