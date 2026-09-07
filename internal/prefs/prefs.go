package prefs

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Moustafa-Elgammal/gotab/internal/core"
)

// Appearance is how the panel picks Light vs Dark.
type Appearance string

const (
	AppearanceSystem Appearance = "system" // follow the OS, and restyle when it flips (P3.4's watcher)
	AppearanceLight  Appearance = "light"
	AppearanceDark   Appearance = "dark"
)

// Alternate is the CGEventFlags mask for the Option key — the default hotkey modifier. Named here so
// the default is readable rather than a magic 0x80000; internal/platform/darwin has the same constant
// from the SDK and this must agree with it.
const modifierAlternate = 0x00080000

// Prefs is the whole settings surface. See doc.go: keys are the field names, the zero value is not
// used, and some fields are schema-only until a later Phase 4 task wires them.
type Prefs struct {
	// Filter — core.Rules. NOT consumed by the event loop yet (it shows every window); carried so the
	// schema is complete. Wiring the filter is a later task (docs/tasks/P4.1.md).
	ShowMinimized  bool
	ShowHidden     bool
	ShowOtherSpace bool
	ActiveAppOnly  bool
	BlockedApps    []string

	// Layout — feeds core.LayoutOpts. 0 means "let the layout engine pick".
	MaxColumns int
	TileWidth  int
	TileHeight int

	// Appearance — wired: cmd/gotab passes it to darwin.SetAppearance before ApplyTheme.
	Appearance Appearance

	// Thumbnails — wired: the prefetcher's cache bound.
	ThumbnailCacheSize int

	// Hotkey — the chord that summons the switcher. HotkeyKeyCode is a hardware keycode (48 is Tab),
	// HotkeyModifiers a CGEventFlags mask. Schema-only for now: the tap's keycode is still fixed
	// (docs/tasks/P4.1.md). Shift always means "backwards" regardless of the base chord.
	HotkeyKeyCode   int
	HotkeyModifiers int
}

// Default returns the settings a fresh install runs with: every filter off (show everything switchable
// on the current Space), the layout engine's own sizing, a HUD that follows the system appearance, a
// 64-thumbnail cache, and ⌥⇥ for the hotkey.
func Default() Prefs {
	return Prefs{
		MaxColumns:         7,
		Appearance:         AppearanceSystem,
		ThumbnailCacheSize: 64,
		HotkeyKeyCode:      48, // Tab
		HotkeyModifiers:    modifierAlternate,
	}
}

// Reader reads scalar values from the backing store. A false second return means the key is absent or
// the wrong type — Load treats both as "use the default".
type Reader interface {
	Bool(key string) (bool, bool)
	Int(key string) (int, bool)
	String(key string) (string, bool)
	Strings(key string) ([]string, bool)
}

// Writer persists scalar values. Sync flushes them to disk (CFPreferencesAppSynchronize).
type Writer interface {
	SetBool(key string, v bool)
	SetInt(key string, v int)
	SetString(key, v string)
	SetStrings(key string, v []string)
	Sync() error
}

// Load starts from Default() and overrides each field whose key is present and well-typed in r.
func Load(r Reader) Prefs {
	p := Default()
	if v, ok := r.Bool("ShowMinimized"); ok {
		p.ShowMinimized = v
	}
	if v, ok := r.Bool("ShowHidden"); ok {
		p.ShowHidden = v
	}
	if v, ok := r.Bool("ShowOtherSpace"); ok {
		p.ShowOtherSpace = v
	}
	if v, ok := r.Bool("ActiveAppOnly"); ok {
		p.ActiveAppOnly = v
	}
	if v, ok := r.Strings("BlockedApps"); ok {
		p.BlockedApps = v
	}
	if v, ok := r.Int("MaxColumns"); ok && v > 0 {
		p.MaxColumns = v
	}
	if v, ok := r.Int("TileWidth"); ok && v >= 0 {
		p.TileWidth = v
	}
	if v, ok := r.Int("TileHeight"); ok && v >= 0 {
		p.TileHeight = v
	}
	if v, ok := r.String("Appearance"); ok {
		switch Appearance(v) {
		case AppearanceLight, AppearanceDark, AppearanceSystem:
			p.Appearance = Appearance(v)
		}
	}
	if v, ok := r.Int("ThumbnailCacheSize"); ok && v > 0 {
		p.ThumbnailCacheSize = v
	}
	if v, ok := r.Int("HotkeyKeyCode"); ok && v > 0 {
		p.HotkeyKeyCode = v
	}
	if v, ok := r.Int("HotkeyModifiers"); ok && v != 0 {
		p.HotkeyModifiers = v
	}
	return p
}

// Save writes every field to w and flushes. It writes the whole struct, not a diff: the store is the
// record, and a field left at its default is still worth persisting so a later default change does not
// silently move a user's setting.
func (p Prefs) Save(w Writer) error {
	w.SetBool("ShowMinimized", p.ShowMinimized)
	w.SetBool("ShowHidden", p.ShowHidden)
	w.SetBool("ShowOtherSpace", p.ShowOtherSpace)
	w.SetBool("ActiveAppOnly", p.ActiveAppOnly)
	w.SetStrings("BlockedApps", p.BlockedApps)
	w.SetInt("MaxColumns", p.MaxColumns)
	w.SetInt("TileWidth", p.TileWidth)
	w.SetInt("TileHeight", p.TileHeight)
	w.SetString("Appearance", string(p.Appearance))
	w.SetInt("ThumbnailCacheSize", p.ThumbnailCacheSize)
	w.SetInt("HotkeyKeyCode", p.HotkeyKeyCode)
	w.SetInt("HotkeyModifiers", p.HotkeyModifiers)
	return w.Sync()
}

// Set applies one "Key=Value" assignment in place, for the `gotab -prefs Key=Value` CLI. A bool takes
// true/false/1/0, an int a base-10 number, []string a comma-separated list, Appearance one of the
// three names. An unknown key or an unparseable value is an error and nothing changes.
func (p *Prefs) Set(assignment string) error {
	k, v, ok := strings.Cut(assignment, "=")
	if !ok {
		return fmt.Errorf("not a Key=Value assignment: %q", assignment)
	}
	k, v = strings.TrimSpace(k), strings.TrimSpace(v)

	parseBool := func() (bool, error) {
		switch strings.ToLower(v) {
		case "true", "1", "yes", "on":
			return true, nil
		case "false", "0", "no", "off":
			return false, nil
		}
		return false, fmt.Errorf("%s: want a boolean, got %q", k, v)
	}
	parseInt := func() (int, error) {
		n, err := strconv.Atoi(v)
		if err != nil {
			return 0, fmt.Errorf("%s: want an integer, got %q", k, v)
		}
		return n, nil
	}

	switch k {
	case "ShowMinimized", "ShowHidden", "ShowOtherSpace", "ActiveAppOnly":
		b, err := parseBool()
		if err != nil {
			return err
		}
		switch k {
		case "ShowMinimized":
			p.ShowMinimized = b
		case "ShowHidden":
			p.ShowHidden = b
		case "ShowOtherSpace":
			p.ShowOtherSpace = b
		case "ActiveAppOnly":
			p.ActiveAppOnly = b
		}
	case "BlockedApps":
		if v == "" {
			p.BlockedApps = nil
			return nil
		}
		parts := strings.Split(v, ",")
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		p.BlockedApps = parts
	case "MaxColumns", "TileWidth", "TileHeight", "ThumbnailCacheSize", "HotkeyKeyCode", "HotkeyModifiers":
		n, err := parseInt()
		if err != nil {
			return err
		}
		switch k {
		case "MaxColumns":
			p.MaxColumns = n
		case "TileWidth":
			p.TileWidth = n
		case "TileHeight":
			p.TileHeight = n
		case "ThumbnailCacheSize":
			p.ThumbnailCacheSize = n
		case "HotkeyKeyCode":
			if n <= 0 {
				return fmt.Errorf("HotkeyKeyCode: want a positive keycode, got %d", n)
			}
			p.HotkeyKeyCode = n
		case "HotkeyModifiers":
			if n == 0 {
				return fmt.Errorf("HotkeyModifiers: need at least one modifier bit — a bare key would be swallowed everywhere")
			}
			p.HotkeyModifiers = n
		}
	case "Appearance":
		switch Appearance(v) {
		case AppearanceSystem, AppearanceLight, AppearanceDark:
			p.Appearance = Appearance(v)
		default:
			return fmt.Errorf("Appearance: want system|light|dark, got %q", v)
		}
	default:
		return fmt.Errorf("unknown pref %q", k)
	}
	return nil
}

// LayoutOpts fills the pref-derived fields of a core.LayoutOpts. The caller sets Screen and Scale from
// the display it is about to show on.
func (p Prefs) LayoutOpts() core.LayoutOpts {
	return core.LayoutOpts{
		TileW:   p.TileWidth,
		TileH:   p.TileHeight,
		MaxCols: p.MaxColumns,
	}
}

// Rules fills the pref-derived fields of a core.Rules. The caller sets ActiveApp and CurrentSpace from
// the live state. NOTE: the event loop does not consult Rules yet — see doc.go.
func (p Prefs) Rules() core.Rules {
	return core.Rules{
		ShowMinimized:  p.ShowMinimized,
		ShowHidden:     p.ShowHidden,
		ShowOtherSpace: p.ShowOtherSpace,
		ActiveAppOnly:  p.ActiveAppOnly,
		BlockedApps:    p.BlockedApps,
	}
}

// String renders the effective settings as "Key = value" lines, for `gotab -prefs`.
func (p Prefs) String() string {
	var b strings.Builder
	line := func(k string, v any) { fmt.Fprintf(&b, "  %-20s %v\n", k, v) }
	line("ShowMinimized", p.ShowMinimized)
	line("ShowHidden", p.ShowHidden)
	line("ShowOtherSpace", p.ShowOtherSpace)
	line("ActiveAppOnly", p.ActiveAppOnly)
	blocked := "(none)"
	if len(p.BlockedApps) > 0 {
		blocked = strings.Join(p.BlockedApps, ", ")
	}
	line("BlockedApps", blocked)
	line("MaxColumns", p.MaxColumns)
	line("TileWidth", p.TileWidth)
	line("TileHeight", p.TileHeight)
	line("Appearance", p.Appearance)
	line("ThumbnailCacheSize", p.ThumbnailCacheSize)
	line("HotkeyKeyCode", p.HotkeyKeyCode)
	line("HotkeyModifiers", fmt.Sprintf("%#x", p.HotkeyModifiers))
	return b.String()
}
