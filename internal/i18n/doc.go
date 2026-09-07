// Package i18n is GoTab's string catalog.
//
// It is pure Go — no cgo, no import of internal/platform — so it sits beside
// internal/prefs in the layer diagram (docs/ARCHITECTURE.md) and compiles and
// runs anywhere. T(key) returns the active locale's string, falling back to the
// built-in English and then to the key itself; it never panics on a missing key.
//
// Two facts shape the design:
//
//   - The built-in English lives in en.json, embedded at build time, and is the
//     single source of truth for the key set. A non-English locale is a partial
//     overlay: any key it omits shows in English.
//
//   - GoTab's user-facing strings are split across two runtimes. Go code calls
//     i18n.T and reads <tag>.lproj/gotab.json. Objective-C code in
//     internal/platform/darwin calls NSLocalizedString and reads the sibling
//     <tag>.lproj/Localizable.strings that AppKit resolves from the bundle. One
//     .lproj directory per locale, two files in it, kept in step by hand. This is
//     the scaffold (P5.1) — only the permissions onboarding strings are routed
//     through it so far; the CLI usage text and the settings window are a later
//     pass. macOS system-locale detection (CFLocaleCopyPreferredLanguages) needs
//     cgo the package deliberately avoids, so cmd/gotab selects the locale from
//     GOTAB_LOCALE / LANG for now — see D40.
//
// SetLocale and Load mutate the catalog through an atomic pointer and are meant
// to be called once, at startup, before other goroutines run. T then reads
// lock-free.
package i18n
