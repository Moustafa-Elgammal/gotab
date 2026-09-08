package update

import (
	"strings"
	"testing"
)

// decodeManifest is the feed parser P5.3 deferred. It must reject a malformed feed rather than
// let it read as "no update" (manifest.go).

func TestDecodeManifestValid(t *testing.T) {
	const body = `{
		"version": "  0.3.0 ",
		"url": " https://example/r ",
		"notes": " new stuff ",
		"min_macos": "12.0",
		"unknown_future_field": 42
	}`
	m, err := decodeManifest(strings.NewReader(body))
	if err != nil {
		t.Fatalf("decodeManifest: %v", err)
	}
	if m.Version != "0.3.0" || m.URL != "https://example/r" || m.Notes != "new stuff" || m.MinMacOS != "12.0" {
		t.Fatalf("fields not trimmed/decoded: %+v", m)
	}
}

func TestDecodeManifestRejects(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"missing version", `{"url": "https://example/r"}`},
		{"blank version", `{"version": "   ", "url": "https://example/r"}`},
		{"missing url", `{"version": "0.3.0"}`},
		{"blank url", `{"version": "0.3.0", "url": ""}`},
		{"trailing data", `{"version": "0.3.0", "url": "https://example/r"} {"and": "more"}`},
		{"not json", `not json at all`},
		{"truncated", `{"version": "0.3.0"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := decodeManifest(strings.NewReader(tt.body)); err == nil {
				t.Fatalf("decodeManifest(%s) = nil error, want one", tt.name)
			}
		})
	}
}
