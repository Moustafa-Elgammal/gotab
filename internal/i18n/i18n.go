package i18n

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
)

//go:embed en.json
var enJSON []byte

// catalog is the resolved lookup: the active locale's overrides layered over the
// built-in English. It is swapped whole by SetLocale/Load through an atomic
// pointer, so T reads it without a lock.
type catalog struct {
	locale   string
	base     map[string]string // always the embedded en.json
	override map[string]string // the active non-en locale, or nil
}

var active atomic.Pointer[catalog]

func init() {
	base := map[string]string{}
	if err := json.Unmarshal(enJSON, &base); err != nil {
		// en.json is a build artifact of this package; a parse failure is a bug
		// here, caught by any run, not a runtime condition a caller could face.
		panic("i18n: embedded en.json is invalid: " + err.Error())
	}
	active.Store(&catalog{locale: "en", base: base})
}

// T returns the string for key: the active locale's value if it has one, then the
// built-in English, then key itself. With args it is fmt.Sprintf'd, so catalog
// values carry %s/%d verbs. It never panics and takes no lock — SetLocale and
// Load are expected to run once at startup, before other goroutines exist (as in
// cmd/gotab's main).
func T(key string, args ...any) string {
	c := active.Load()
	v, ok := c.override[key]
	if !ok {
		if v, ok = c.base[key]; !ok {
			v = key
		}
	}
	if len(args) > 0 {
		return fmt.Sprintf(v, args...)
	}
	return v
}

// Locale reports the active locale tag ("en" when nothing else is set).
func Locale() string { return active.Load().locale }

// SetLocale sets the active locale tag and drops any override strings from a
// previous one. It normalizes the tag ("de_DE.UTF-8" -> "de", "pt_BR" -> "pt-BR").
// "en" or "" is the built-in catalog. It loads no files itself — call Load for
// that; the call order does not matter.
func SetLocale(tag string) {
	c := active.Load()
	active.Store(&catalog{locale: normalize(tag), base: c.base})
}

// Load merges <dir>/<active-locale>.lproj/gotab.json over the built-in strings.
// It is a no-op with a nil error when the locale is English, dir is empty, or the
// file is absent; it returns an error only when the file is present but
// unreadable or malformed. Call it after SetLocale.
func Load(dir string) error {
	c := active.Load()
	if c.locale == "en" || c.locale == "" || dir == "" {
		return nil
	}
	path := filepath.Join(dir, c.locale+".lproj", "gotab.json")
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	m := map[string]string{}
	if err := json.Unmarshal(b, &m); err != nil {
		return fmt.Errorf("i18n: %s: %w", path, err)
	}
	active.Store(&catalog{locale: c.locale, base: c.base, override: m})
	return nil
}

// normalize lowercases the language subtag, uppercases a region if present, and
// strips any encoding or modifier suffix: "DE" -> "de", "pt_BR" -> "pt-BR",
// "en_US.UTF-8" -> "en-US", "C" -> "c" (which matches no catalog, i.e. English).
func normalize(tag string) string {
	tag = strings.TrimSpace(tag)
	if i := strings.IndexAny(tag, ".@ "); i >= 0 {
		tag = tag[:i]
	}
	tag = strings.ReplaceAll(tag, "_", "-")
	lang, region, ok := strings.Cut(tag, "-")
	lang = strings.ToLower(lang)
	if !ok {
		return lang
	}
	return lang + "-" + strings.ToUpper(region)
}
