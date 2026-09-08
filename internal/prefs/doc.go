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
// Every field is wired end to end: P4.2 (D35) made the hotkey chord live from the settings recorder
// through to the tap, and P4.4 (D37) put the Rules() fields through core.Filter on every rescan. The
// one exception is Rules().ActiveAppOnly, still inert because the loop does not capture the frontmost
// pid on Summon yet.
package prefs
