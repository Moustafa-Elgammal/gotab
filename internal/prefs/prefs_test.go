package prefs

import (
	"reflect"
	"testing"
)

// These are the tests P4.1 deferred to Phase 6 (D16, D23): internal/prefs is pure Go, so the
// schema, the Load/Save round trip and the `-prefs Key=Value` parser are exactly the kind of
// boundary logic V6.6 collects here rather than leaving unverified.

// memStore is an in-memory Reader+Writer. The real store is CFPreferences (prefs.m); this exercises
// the pure Go on both sides of that interface without a plist.
type memStore struct {
	b      map[string]bool
	i      map[string]int
	s      map[string]string
	ss     map[string][]string
	synced int
}

func newMemStore() *memStore {
	return &memStore{
		b:  map[string]bool{},
		i:  map[string]int{},
		s:  map[string]string{},
		ss: map[string][]string{},
	}
}

func (m *memStore) Bool(k string) (bool, bool)        { v, ok := m.b[k]; return v, ok }
func (m *memStore) Int(k string) (int, bool)          { v, ok := m.i[k]; return v, ok }
func (m *memStore) String(k string) (string, bool)    { v, ok := m.s[k]; return v, ok }
func (m *memStore) Strings(k string) ([]string, bool) { v, ok := m.ss[k]; return v, ok }

func (m *memStore) SetBool(k string, v bool)        { m.b[k] = v }
func (m *memStore) SetInt(k string, v int)          { m.i[k] = v }
func (m *memStore) SetString(k, v string)           { m.s[k] = v }
func (m *memStore) SetStrings(k string, v []string) { m.ss[k] = v }
func (m *memStore) Sync() error                     { m.synced++; return nil }

// An empty store must yield exactly Default(): Load starts there and overrides nothing.
func TestLoadEmptyIsDefault(t *testing.T) {
	got := Load(newMemStore())
	if !reflect.DeepEqual(got, Default()) {
		t.Fatalf("Load(empty) = %+v, want Default() %+v", got, Default())
	}
}

// Save then Load is the identity. This is the contract every settings write depends on: what the
// window wrote is what the switcher reads back.
func TestSaveLoadRoundTrip(t *testing.T) {
	want := Prefs{
		ShowMinimized:      true,
		ShowHidden:         true,
		ShowOtherSpace:     false,
		ActiveAppOnly:      true,
		BlockedApps:        []string{"Finder", "loginwindow"},
		MaxColumns:         5,
		TileWidth:          220,
		TileHeight:         140,
		Appearance:         AppearanceDark,
		ThumbnailCacheSize: 128,
		HotkeyKeyCode:      50,
		HotkeyModifiers:    0x00100000,
	}
	st := newMemStore()
	if err := want.Save(st); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if st.synced != 1 {
		t.Errorf("Save flushed %d times, want 1", st.synced)
	}
	if got := Load(st); !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip:\n got %+v\nwant %+v", got, want)
	}
}

// Load rejects values the store holds but the schema forbids, falling back to the default rather
// than propagating a nonsense setting. A zero MaxColumns from a corrupt plist must not reach the
// layout engine.
func TestLoadRejectsOutOfRange(t *testing.T) {
	st := newMemStore()
	st.i["MaxColumns"] = 0         // must be > 0
	st.i["TileWidth"] = -10        // must be >= 0
	st.i["ThumbnailCacheSize"] = 0 // must be > 0
	st.i["HotkeyKeyCode"] = 0      // must be > 0
	st.i["HotkeyModifiers"] = 0    // a bare key would be swallowed session-wide
	st.s["Appearance"] = "chartreuse"

	got := Load(st)
	d := Default()
	if got.MaxColumns != d.MaxColumns {
		t.Errorf("MaxColumns = %d, want default %d", got.MaxColumns, d.MaxColumns)
	}
	if got.TileWidth != d.TileWidth {
		t.Errorf("TileWidth = %d, want default %d", got.TileWidth, d.TileWidth)
	}
	if got.ThumbnailCacheSize != d.ThumbnailCacheSize {
		t.Errorf("ThumbnailCacheSize = %d, want default %d", got.ThumbnailCacheSize, d.ThumbnailCacheSize)
	}
	if got.HotkeyKeyCode != d.HotkeyKeyCode || got.HotkeyModifiers != d.HotkeyModifiers {
		t.Errorf("hotkey = %d/%#x, want default %d/%#x",
			got.HotkeyKeyCode, got.HotkeyModifiers, d.HotkeyKeyCode, d.HotkeyModifiers)
	}
	if got.Appearance != d.Appearance {
		t.Errorf("Appearance = %q, want default %q", got.Appearance, d.Appearance)
	}
}

// A wrong-typed key is "absent", not an error: Load treats the type mismatch exactly as it treats a
// missing key.
func TestLoadIgnoresWrongType(t *testing.T) {
	st := newMemStore()
	st.s["MaxColumns"] = "seven" // schema wants an Int; a String here is not seen
	if got := Load(st).MaxColumns; got != Default().MaxColumns {
		t.Fatalf("MaxColumns = %d, want default %d", got, Default().MaxColumns)
	}
}

func TestSet(t *testing.T) {
	tests := []struct {
		name    string
		assign  string
		wantErr bool
		check   func(Prefs) bool
	}{
		{"bool true", "ShowMinimized=true", false, func(p Prefs) bool { return p.ShowMinimized }},
		{"bool on", "ShowHidden=on", false, func(p Prefs) bool { return p.ShowHidden }},
		{"bool 1", "ShowOtherSpace=1", false, func(p Prefs) bool { return p.ShowOtherSpace }},
		{"bool no", "ActiveAppOnly=no", false, func(p Prefs) bool { return !p.ActiveAppOnly }},
		{"bool spaced", "  ShowMinimized  =  false  ", false, func(p Prefs) bool { return !p.ShowMinimized }},
		{"bool garbage", "ShowMinimized=maybe", true, nil},
		{"int", "MaxColumns=9", false, func(p Prefs) bool { return p.MaxColumns == 9 }},
		{"int not a number", "MaxColumns=lots", true, nil},
		{"cache size", "ThumbnailCacheSize=200", false, func(p Prefs) bool { return p.ThumbnailCacheSize == 200 }},
		{"keycode positive", "HotkeyKeyCode=49", false, func(p Prefs) bool { return p.HotkeyKeyCode == 49 }},
		{"keycode zero rejected", "HotkeyKeyCode=0", true, nil},
		{"keycode negative rejected", "HotkeyKeyCode=-3", true, nil},
		{"modifiers zero rejected", "HotkeyModifiers=0", true, nil},
		{"modifiers set", "HotkeyModifiers=524288", false, func(p Prefs) bool { return p.HotkeyModifiers == 524288 }},
		{"blocked apps", "BlockedApps=Finder, Safari ,loginwindow", false, func(p Prefs) bool {
			return reflect.DeepEqual(p.BlockedApps, []string{"Finder", "Safari", "loginwindow"})
		}},
		{"blocked apps cleared", "BlockedApps=", false, func(p Prefs) bool { return p.BlockedApps == nil }},
		{"appearance dark", "Appearance=dark", false, func(p Prefs) bool { return p.Appearance == AppearanceDark }},
		{"appearance garbage", "Appearance=oled", true, nil},
		{"unknown key", "Wobble=3", true, nil},
		{"not an assignment", "ShowMinimized", true, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := Default()
			err := p.Set(tt.assign)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Set(%q) = nil error, want one", tt.assign)
				}
				// An error must change nothing.
				if !reflect.DeepEqual(p, Default()) {
					t.Fatalf("Set(%q) errored but mutated prefs to %+v", tt.assign, p)
				}
				return
			}
			if err != nil {
				t.Fatalf("Set(%q): %v", tt.assign, err)
			}
			if !tt.check(p) {
				t.Fatalf("Set(%q) did not take: %+v", tt.assign, p)
			}
		})
	}
}

// The two mappers feed core; a dropped or renamed field here is a filter or a layout that silently
// ignores a setting.
func TestMappersCarryEveryField(t *testing.T) {
	p := Prefs{
		ShowMinimized: true, ShowHidden: true, ShowOtherSpace: true, ActiveAppOnly: true,
		BlockedApps: []string{"X"},
		MaxColumns:  4, TileWidth: 200, TileHeight: 120,
	}
	r := p.Rules()
	if !r.ShowMinimized || !r.ShowHidden || !r.ShowOtherSpace || !r.ActiveAppOnly ||
		!reflect.DeepEqual(r.BlockedApps, []string{"X"}) {
		t.Errorf("Rules() dropped a field: %+v", r)
	}
	l := p.LayoutOpts()
	if l.MaxCols != 4 || l.TileW != 200 || l.TileH != 120 {
		t.Errorf("LayoutOpts() = %+v, want cols/w/h 4/200/120", l)
	}
}

// String is `gotab -prefs` output; it must mention every key so a user can see what they can set.
func TestStringListsEveryKey(t *testing.T) {
	s := Default().String()
	for _, k := range []string{
		"ShowMinimized", "ShowHidden", "ShowOtherSpace", "ActiveAppOnly", "BlockedApps",
		"MaxColumns", "TileWidth", "TileHeight", "Appearance", "ThumbnailCacheSize",
		"HotkeyKeyCode", "HotkeyModifiers",
	} {
		if !contains(s, k) {
			t.Errorf("String() omits %q:\n%s", k, s)
		}
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
