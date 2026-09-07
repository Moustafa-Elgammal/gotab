package update

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
)

// manifest is the release feed document: the newest published build and where to
// get it. It is deliberately tiny — the release process hand-writes it (see
// resources/appcast/latest.json) and check only needs a version to compare and a
// URL to hand the user. Unknown fields decode to nothing, so the schema can grow
// without breaking an older client.
type manifest struct {
	Version  string `json:"version"`   // the advertised release, e.g. "0.2.0" or "v0.2.0"
	URL      string `json:"url"`       // where to download Version / read its notes
	Notes    string `json:"notes"`     // optional one-line human summary
	MinMacOS string `json:"min_macos"` // optional; carried for the future (a build needing a newer OS than the host should not be offered) — not yet enforced, see docs/tasks/P5.3.md
}

// decodeManifest reads exactly one JSON object from r (the caller has already
// capped how much r will yield) and checks the two fields check cannot proceed
// without. A blank version or url, or trailing data after the object, is a
// malformed feed and comes back as an error — never as a silent "no update".
func decodeManifest(r io.Reader) (manifest, error) {
	dec := json.NewDecoder(r)

	var m manifest
	if err := dec.Decode(&m); err != nil {
		return manifest{}, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return manifest{}, errors.New("update: manifest has trailing data after the object")
	}

	m.Version = strings.TrimSpace(m.Version)
	m.URL = strings.TrimSpace(m.URL)
	m.Notes = strings.TrimSpace(m.Notes)
	m.MinMacOS = strings.TrimSpace(m.MinMacOS)

	if m.Version == "" {
		return manifest{}, errors.New("update: manifest is missing \"version\"")
	}
	if m.URL == "" {
		return manifest{}, errors.New("update: manifest is missing \"url\"")
	}
	return m, nil
}
