package update

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// check's HTTP behaviour, which P5.3 only exercised against an ad-hoc `python3 -m http.server`
// (D41). V6.6 gives it committed coverage; V6.11 is the separate check against the live feed.

// serveManifest points FeedURL at a test server for the duration of one test.
func serveManifest(t *testing.T, h http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	prev := FeedURL
	FeedURL = srv.URL
	t.Cleanup(func() { FeedURL = prev })
}

func manifestJSON(version string) string {
	return fmt.Sprintf(`{"version": %q, "url": "https://example/r/%s", "notes": "line"}`, version, version)
}

func TestCheckAvailable(t *testing.T) {
	serveManifest(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, manifestJSON("0.3.0"))
	})
	res, err := check(context.Background(), "0.2.0")
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if !res.Available || res.Latest != "0.3.0" || res.Current != "0.2.0" {
		t.Fatalf("check(0.2.0 vs 0.3.0) = %+v, want Available with Latest 0.3.0", res)
	}
	if res.URL != "https://example/r/0.3.0" || res.Notes != "line" {
		t.Fatalf("check dropped url/notes: %+v", res)
	}
}

func TestCheckUpToDate(t *testing.T) {
	serveManifest(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, manifestJSON("0.2.0"))
	})
	for _, cur := range []string{"0.2.0", "0.9.0", "v0.2.0"} {
		res, err := check(context.Background(), cur)
		if err != nil {
			t.Fatalf("check(%q): %v", cur, err)
		}
		if res.Available {
			t.Errorf("check(%q vs 0.2.0).Available = true, want false", cur)
		}
	}
}

// A development build never hits the network: there is nothing to compare.
func TestCheckDevMakesNoRequest(t *testing.T) {
	serveManifest(t, func(http.ResponseWriter, *http.Request) {
		t.Error("check made a request for a dev build")
	})
	res, err := check(context.Background(), "dev")
	if err != nil {
		t.Fatalf("check(dev): %v", err)
	}
	if res.Available {
		t.Errorf("check(dev).Available = true")
	}
}

func TestCheckErrorPaths(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{"non-200", func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "nope", http.StatusNotFound)
		}},
		{"malformed manifest", func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, `{"version":`)
		}},
		{"version not a release", func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, `{"version": "nightly", "url": "https://example/r"}`)
		}},
		{"oversized body", func(w http.ResponseWriter, _ *http.Request) {
			// > maxManifestBytes of valid-prefix JSON: check must refuse it, not buffer it.
			fmt.Fprintf(w, `{"version": "0.3.0", "url": "https://example/r", "notes": %q}`,
				strings.Repeat("x", maxManifestBytes+1))
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			serveManifest(t, tt.handler)
			res, err := check(context.Background(), "0.2.0")
			if err == nil {
				t.Fatalf("check(%s) = nil error, want one", tt.name)
			}
			if res.Available {
				t.Errorf("check(%s).Available = true on an error path", tt.name)
			}
			if res.Current != "0.2.0" {
				t.Errorf("check(%s) lost Current: %+v", tt.name, res)
			}
		})
	}
}

// An unreachable host is an error, never a panic and never Available.
func TestCheckUnreachable(t *testing.T) {
	prev := FeedURL
	FeedURL = "http://127.0.0.1:0/nope"
	t.Cleanup(func() { FeedURL = prev })

	res, err := check(context.Background(), "0.2.0")
	if err == nil {
		t.Fatal("check(unreachable) = nil error, want one")
	}
	if res.Available {
		t.Error("check(unreachable).Available = true")
	}
}
