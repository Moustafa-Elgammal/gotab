package update

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// maxManifestBytes caps the feed read. The manifest is a handful of short
// strings; anything past this is a misconfigured or hostile host, and either way
// not something to pull into memory. io.LimitReader enforces it.
const maxManifestBytes = 64 << 10

// check is the real implementation behind Check: GET FeedURL, decode the release
// manifest, and report whether it advertises a build newer than current.
//
// Nothing here is fatal:
//
//   - A current that is not a release number — the literal "dev", "git describe"
//     output like "g1a2b3c4", an empty string — returns Result{Current: current,
//     Available: false} and a nil error. There is nothing to compare, which is
//     not an error, and no request is made.
//   - A transport, status, size, decode or manifest-content failure returns a
//     non-nil error and a Result carrying only Current. Available is never true
//     on any error path, and no input causes a panic.
//
// check keeps no package-global state, so a later launch-time caller can run it
// on its own goroutine. The context bounds the request; the client also caps it
// at five seconds so a hung connection cannot pin the goroutine forever.
func check(ctx context.Context, current string) (Result, error) {
	cur, ok := parseVersion(current)
	if !ok {
		return Result{Current: current, Available: false}, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, FeedURL, nil)
	if err != nil {
		return Result{Current: current}, fmt.Errorf("update: build request for %s: %w", FeedURL, err)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return Result{Current: current}, fmt.Errorf("update: fetch %s: %w", FeedURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return Result{Current: current}, fmt.Errorf("update: %s: unexpected status %s", FeedURL, resp.Status)
	}

	// Read one byte past the cap: if that byte materialises, the body was over
	// the limit and the feed is not one to trust.
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxManifestBytes+1))
	if err != nil {
		return Result{Current: current}, fmt.Errorf("update: read %s: %w", FeedURL, err)
	}
	if len(data) > maxManifestBytes {
		return Result{Current: current}, fmt.Errorf("update: %s: manifest exceeds %d bytes", FeedURL, maxManifestBytes)
	}

	m, err := decodeManifest(bytes.NewReader(data))
	if err != nil {
		return Result{Current: current}, err
	}

	latest, ok := parseVersion(m.Version)
	if !ok {
		return Result{Current: current}, fmt.Errorf("update: manifest version %q is not a release number", m.Version)
	}

	// min_macos is decoded (manifest.go) but not yet compared against the host:
	// that needs the running macOS version, which would pull OS detection into
	// this pure-stdlib package, and Result has no field to carry "blocked by OS"
	// anyway. Deferred — see docs/tasks/P5.3.md.

	return Result{
		Current:   current,
		Latest:    m.Version,
		URL:       m.URL,
		Notes:     m.Notes,
		Available: compare(latest, cur) > 0,
	}, nil
}
