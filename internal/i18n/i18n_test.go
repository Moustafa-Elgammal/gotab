package i18n

import (
	"os"
	"path/filepath"
	"testing"
)

// The tests P5.1 deferred (D16, D23). internal/i18n is pure Go; the tag normaliser and the
// base/override/key fallback are pure string logic and belong under V6.6.

// resetLocale puts the package back to the built-in English catalog. The active catalog is process
// global (SetLocale/Load are documented as startup-only), so every test that touches it restores it.
func resetLocale(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { SetLocale("en") })
}

func TestNormalize(t *testing.T) {
	tests := []struct{ in, want string }{
		{"de", "de"},
		{"DE", "de"},
		{"de_DE", "de-DE"},
		{"de_DE.UTF-8", "de-DE"},
		{"pt_BR", "pt-BR"},
		{"en-us", "en-US"},
		{"en_US.UTF-8", "en-US"},
		{"fr_FR@euro", "fr-FR"},
		{"  de  ", "de"},
		{"C", "c"},
		{"", ""},
		{"POSIX", "posix"},
	}
	for _, tt := range tests {
		if got := normalize(tt.in); got != tt.want {
			t.Errorf("normalize(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// The default catalog is English and every key in en.json resolves to a non-key string.
func TestDefaultCatalogIsEnglish(t *testing.T) {
	resetLocale(t)
	if Locale() != "en" {
		t.Fatalf("Locale() = %q, want en", Locale())
	}
	if got := T("update.upToDate", "1.0.0"); got != "gotab 1.0.0: up to date." {
		t.Fatalf("T(update.upToDate) = %q", got)
	}
}

// An unknown key returns itself — never a panic, never an empty string.
func TestUnknownKeyReturnsKey(t *testing.T) {
	resetLocale(t)
	const k = "no.such.key"
	if got := T(k); got != k {
		t.Fatalf("T(%q) = %q, want the key back", k, got)
	}
}

// args → fmt.Sprintf against the catalog value's verbs.
func TestFormatArgs(t *testing.T) {
	resetLocale(t)
	got := T("update.available", "0.3.0", "0.2.0", "https://example/r")
	want := "gotab: 0.3.0 is available (you have 0.2.0) — https://example/r"
	if got != want {
		t.Fatalf("T(update.available, …) = %q, want %q", got, want)
	}
}

// SetLocale + Load layers a <locale>.lproj/gotab.json overlay over English: an overridden key takes
// the overlay value, a key the overlay omits falls through to English.
func TestLoadOverlay(t *testing.T) {
	resetLocale(t)
	dir := t.TempDir()
	writeOverlay(t, dir, "de", `{"perm.alert.quit": "Beenden"}`)

	SetLocale("de_DE.UTF-8")
	if Locale() != "de-DE" {
		t.Fatalf("Locale() = %q after SetLocale, want de-DE", Locale())
	}
	// Load resolves <active-locale>.lproj/gotab.json; drop to the bare "de" the overlay is filed
	// under. normalize is covered above — this is the file-lookup half.
	SetLocale("de")
	if err := Load(dir); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := T("perm.alert.quit"); got != "Beenden" {
		t.Errorf("overridden key: T(perm.alert.quit) = %q, want Beenden", got)
	}
	if got := T("perm.alert.openSettings"); got != "Open System Settings" {
		t.Errorf("fall-through key: T(perm.alert.openSettings) = %q, want the English", got)
	}
}

// Load is a no-op with a nil error when the locale is English or the overlay file is absent.
func TestLoadNoopPaths(t *testing.T) {
	resetLocale(t)

	if err := Load(t.TempDir()); err != nil {
		t.Errorf("Load(dir) with locale=en: %v, want nil no-op", err)
	}
	SetLocale("de")
	if err := Load(t.TempDir()); err != nil { // no de.lproj/gotab.json in there
		t.Errorf("Load(dir) with missing overlay: %v, want nil no-op", err)
	}
	if got := T("perm.alert.quit"); got != "Quit" {
		t.Errorf("after a no-op Load the catalog is still English, got T(perm.alert.quit) = %q", got)
	}
}

// A present-but-malformed overlay is the one case Load reports as an error.
func TestLoadMalformedOverlay(t *testing.T) {
	resetLocale(t)
	dir := t.TempDir()
	writeOverlay(t, dir, "de", `{"perm.alert.quit": `) // truncated JSON

	SetLocale("de")
	if err := Load(dir); err == nil {
		t.Fatal("Load(malformed overlay) = nil, want an error")
	}
}

func writeOverlay(t *testing.T, dir, locale, body string) {
	t.Helper()
	sub := filepath.Join(dir, locale+".lproj")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "gotab.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
