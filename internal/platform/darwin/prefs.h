// Typed key I/O over the application's CFPreferences domain — the plist at
// ~/Library/Preferences/<CFBundleIdentifier>.plist, reached through the cfprefsd cache so it
// interoperates with `defaults read` / `defaults write`. Kept free of Objective-C so cgo can include
// it directly.
//
// Go (internal/prefs) owns the schema; this is only get/set for a flat dict whose values are bool,
// signed integer, string, or string array. The domain is kCFPreferencesCurrentApplication — the
// bundle id when running from the built .app, and the running binary's id under `go run` (so a dev
// build's settings land in their own domain, not the real one; acceptable, it is the same identity
// rule TCC uses).
#ifndef GOTAB_DARWIN_PREFS_H
#define GOTAB_DARWIN_PREFS_H

#include <stdint.h>

// Getters: return 1 and fill *out when the key is present AND the stored type matches; 0 when the key
// is absent; -1 when it is present as a different type (a hand-edited plist). A bool reads back from a
// number and vice versa — `defaults write -bool` and a literal 1 should behave the same.
int32_t gt_prefs_get_bool(const char *key, int32_t *out);
int32_t gt_prefs_get_i64(const char *key, int64_t *out);

// String: up to cap-1 bytes plus a NUL into buf; *out_len is the untruncated UTF-8 byte length, so a
// caller can detect truncation and retry with a bigger buffer.
int32_t gt_prefs_get_str(const char *key, char *buf, int32_t cap, int32_t *out_len);

// String array: the elements joined by '\n' into buf (same truncation contract as gt_prefs_get_str),
// with the element count in *out_count. A stored element containing '\n' is not representable and the
// join is lossy for it — window-server application names never contain one.
int32_t gt_prefs_get_strs(const char *key, char *buf, int32_t cap, int32_t *out_count, int32_t *out_len);

void gt_prefs_set_bool(const char *key, int32_t v);
void gt_prefs_set_i64(const char *key, int64_t v);
void gt_prefs_set_str(const char *key, const char *v);
// joined is the elements separated by '\n' (count of them); an empty string clears the key.
void gt_prefs_set_strs(const char *key, const char *joined, int32_t count);

// Flushes pending writes to the plist. 1 on success, 0 on failure.
int32_t gt_prefs_sync(void);

#endif
