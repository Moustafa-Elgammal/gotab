// Package prefs is the switcher's typed settings: the schema, its defaults, and the mapping to and
// from a flat key/value store. Pure Go — the store itself (the macOS CFPreferences domain for the
// bundle id) lives behind the Reader/Writer interfaces, which internal/platform/darwin implements.
//
// Invariants:
//
//   - The zero value is never used. Build a Prefs with Default(), then Load() over it — a missing or
//     malformed key falls back to the Default() value for that field, never to Go's zero.
//   - Keys are the exported field names verbatim ("MaxColumns", "ShowMinimized", …), so
//     `defaults write <bundle-id> MaxColumns 5` and `gotab -prefs MaxColumns=5` address the same key.
//   - Every field maps to exactly one of: bool, int, string, []string. No nested dictionaries — a
//     CFPreferences plist can hold them, but a flat schema is what keeps the Reader/Writer trivial.
//   - Derivation is one-way. LayoutOpts() and Rules() build core types from a Prefs; nothing builds a
//     Prefs from a core type. Runtime-only fields those core types carry (Screen, Scale, CurrentSpace)
//     are the caller's to fill after.
//
// Not every field is consumed yet: the Rules() fields and the hotkey chord are in the schema so it is
// defined once and P4.2's settings UI has all of it, but the event loop does not filter by Rules and
// the hotkey keycode is still fixed. Those wirings belong to later Phase 4 tasks; see docs/tasks/P4.1.md.
package prefs
