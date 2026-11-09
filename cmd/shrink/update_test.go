package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNormVersion(t *testing.T) {
	cases := []struct{ in, want string }{
		{"v0.5.0", "v0.5.0"},
		{"0.5.0", "v0.5.0"},
		{"dev", "vdev"},
		{"", "v"},
	}
	for _, c := range cases {
		if got := normVersion(c.in); got != c.want {
			t.Errorf("normVersion(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestReleaseHighlights(t *testing.T) {
	body := "### Added\r\n- **First** with `code`\n- [Second](https://example.com)\n    - nested detail\n- Third\n- Fourth\n### Changed\n* Different behavior\n### Compatibility notes\n- Not a change summary\n### Fixed\n+ Correct output\n"
	got := summarizeRelease(body)
	if strings.Join(got.Added, "|") != "First with code|Second|Third" || strings.Join(got.Changed, "|") != "Different behavior" || strings.Join(got.Fixed, "|") != "Correct output" {
		t.Fatalf("unexpected highlights: %+v", got)
	}
	if text := releaseSummaryText("bad\x1b[31m\r\u202etext"); strings.ContainsAny(text, "\x1b\r\u202e") {
		t.Fatalf("terminal controls retained: %q", text)
	}
	if text := releaseSummaryText(strings.Repeat("界", 200)); len([]rune(text)) != 140 || !strings.HasSuffix(text, "...") {
		t.Fatalf("long summary not bounded: %q", text)
	}
}

func TestUpdateSizeComparison(t *testing.T) {
	for _, c := range []struct {
		before, after int64
		want          string
	}{{2048, 3072, "+50.0% larger"}, {2048, 1024, "50.0% smaller"}, {2048, 2048, "size unchanged"}, {0, 1024, "percentage unavailable"}} {
		var stderr bytes.Buffer
		a := newApplication(context.Background(), io.Discard, &stderr)
		a.printUpdateSize(c.before, c.after)
		if !strings.Contains(stderr.String(), c.want) || strings.Contains(stderr.String(), "\x1b") {
			t.Fatalf("incorrect plain size comparison: %q", stderr.String())
		}
	}
}

type updateRoundTripper func(*http.Request) (*http.Response, error)

func (f updateRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func updateTestExit(fn func()) (code int) {
	defer func() {
		if value := recover(); value != nil {
			if status, ok := value.(exitCode); ok {
				code = int(status)
			} else {
				panic(value)
			}
		}
	}()
	fn()
	return
}

func TestUpdatePresentationAndSafeInstall(t *testing.T) {
	for _, mode := range []string{"update", "current", "dry-run", "bad-checksum", "machine"} {
		t.Run(mode, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			a := newApplication(context.Background(), &stdout, &stderr)
			a.overwrite = true // A development build needs explicit replacement.
			a.machine, a.dryRun = mode == "machine", mode == "dry-run"
			latest := "v9.0.0"
			if mode == "current" {
				latest = normVersion(version)
			}
			asset := fmt.Sprintf("shrink-%s-%s-%s.tar.gz", latest, runtime.GOOS, runtime.GOARCH)
			binary := []byte("#!/bin/sh\nprintf 'shrink v9.0.0\\n'\n")
			binary = append(binary, bytes.Repeat([]byte("\n"), 1024-len(binary))...)
			var archive bytes.Buffer
			gz := gzip.NewWriter(&archive)
			tw := tar.NewWriter(gz)
			if err := tw.WriteHeader(&tar.Header{Name: "shrink", Mode: 0755, Size: int64(len(binary))}); err != nil {
				t.Fatal(err)
			}
			if _, err := tw.Write(binary); err != nil {
				t.Fatal(err)
			}
			if err := tw.Close(); err != nil {
				t.Fatal(err)
			}
			if err := gz.Close(); err != nil {
				t.Fatal(err)
			}
			notes := "### Added\n- Persistent queue\n### Changed\n- Shared CPU budget\n### Fixed\n- Cancellation cleanup\n"
			release, err := json.Marshal(map[string]any{"tag_name": latest, "body": notes, "assets": []map[string]string{
				{"name": asset, "browser_download_url": "https://example.invalid/archive"},
				{"name": "shrink-" + latest + "-checksums.txt", "browser_download_url": "https://example.invalid/checksums"},
			}})
			if err != nil {
				t.Fatal(err)
			}
			checksum := sha256hex(archive.Bytes())
			if mode == "bad-checksum" {
				checksum = strings.Repeat("0", 64)
			}
			responses := map[string][]byte{
				updateAPI + "/releases/latest":      release,
				"https://example.invalid/archive":   archive.Bytes(),
				"https://example.invalid/checksums": []byte(checksum + "  " + asset + "\n"),
			}
			requests := 0
			a.updateHTTPClient = &http.Client{Transport: updateRoundTripper(func(r *http.Request) (*http.Response, error) {
				requests++
				body, ok := responses[r.URL.String()]
				if !ok {
					return nil, fmt.Errorf("unexpected URL: %s", r.URL)
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body)), Header: http.Header{}}, nil
			})}
			self := filepath.Join(t.TempDir(), "shrink")
			if runtime.GOOS == "windows" {
				self += ".exe"
			}
			original := bytes.Repeat([]byte("x"), 2048)
			if err := os.WriteFile(self, original, 0755); err != nil {
				t.Fatal(err)
			}
			code := updateTestExit(func() { a.printUpdateHeader(); a.runUpdateAt(self) })
			installed, err := os.ReadFile(self)
			if err != nil {
				t.Fatal(err)
			}
			text := stderr.String()
			if mode == "bad-checksum" {
				if code != 1 || !bytes.Equal(installed, original) || strings.Contains(text, "Update complete") || !strings.Contains(text, "checksum mismatch") {
					t.Fatalf("failed verification changed install or claimed success: code=%d %s", code, text)
				}
				return
			}
			if code != 0 {
				t.Fatalf("update failed: %d %s", code, text)
			}
			if mode == "current" || mode == "dry-run" {
				if requests != 1 || !bytes.Equal(installed, original) {
					t.Fatal("no-op downloaded an archive or changed installed binary")
				}
				want := "Already up to date."
				if mode == "dry-run" {
					want = "Dry run complete. No files changed."
				}
				if !strings.Contains(text, want) || strings.Contains(text, "Update complete.") {
					t.Fatal(text)
				}
				return
			}
			if !bytes.Equal(installed, binary) {
				t.Fatal("verified binary was not installed")
			}
			if mode == "machine" {
				var result machineUpdate
				if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || !result.Updated || result.UpdatedTo != latest || text != "" {
					t.Fatalf("machine output changed: %s %s %v", stdout.String(), text, err)
				}
				return
			}
			for _, want := range []string{"Current", "Latest    v9.0.0", "smaller", "Persistent queue", "Shared CPU budget", "Cancellation cleanup", "https://github.com/" + updateRepo + "/releases/tag/v9.0.0", "Update complete."} {
				if !strings.Contains(text, want) {
					t.Errorf("missing %q in output:\n%s", want, text)
				}
			}
			if strings.Index(text, "Binary") > strings.Index(text, "Release highlights") || strings.Index(text, "Full changelog") > strings.Index(text, "Update complete.") {
				t.Fatal("update receipt is out of order", text)
			}
		})
	}
}

func TestChecksumFor(t *testing.T) {
	sums := "aaa111 shrink-v1.0.0-darwin-arm64.tar.gz\n" +
		"bbb222 shrink-v1.0.0-linux-amd64.tar.gz\n"
	if got := checksumFor("shrink-v1.0.0-linux-amd64.tar.gz", sums); got != "bbb222" {
		t.Errorf("checksumFor = %q, want bbb222", got)
	}
	if got := checksumFor("shrink-v1.0.0-windows-amd64.tar.gz", sums); got != "" {
		t.Errorf("missing entries should give empty, got %q", got)
	}
}

func TestSHA256Hex(t *testing.T) {
	h := sha256hex([]byte("shrink"))
	if len(h) != 64 {
		t.Fatalf("sha256hex length = %d, want 64", len(h))
	}
	if sha256hex([]byte("shrink")) != h {
		t.Error("sha256hex should be deterministic")
	}
	if sha256hex([]byte("shrink2")) == h {
		t.Error("different inputs should hash differently")
	}
}

func TestParseRelease(t *testing.T) {
	body := []byte(`{
		"tag_name": "v0.9.9",
		"assets": [
			{"name": "shrink-v0.9.9-darwin-arm64.tar.gz", "browser_download_url": "https://example.com/a"},
			{"name": "shrink-v0.9.9-checksums.txt", "browser_download_url": "https://example.com/c"}
		]
	}`)
	r, err := parseRelease(body)
	if err != nil || r.TagName != "v0.9.9" {
		t.Fatalf("parseRelease = %+v, %v", r, err)
	}
	if got := assetURL(r, "shrink-v0.9.9-darwin-arm64.tar.gz"); got != "https://example.com/a" {
		t.Errorf("assetURL = %q", got)
	}
	if got := assetURL(r, "nope"); got != "" {
		t.Errorf("unknown asset should give empty, got %q", got)
	}
	if _, err := parseRelease([]byte(`{}`)); err == nil {
		t.Error("a document without a tag should fail")
	}
	if _, err := parseRelease([]byte(`not json`)); err == nil {
		t.Error("garbage should fail")
	}
}

func TestExtractBinary(t *testing.T) {
	// build a release-shaped tarball in memory: one file named shrink
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	content := []byte("#!/bin/sh\nfake binary\n")
	if err := tw.WriteHeader(&tar.Header{Name: "shrink", Mode: 0o755, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gz.Close()

	got, err := extractBinary(buf.Bytes())
	if err != nil || string(got) != string(content) {
		t.Errorf("extractBinary = %q, %v", got, err)
	}

	// a tarball without the binary fails
	var empty bytes.Buffer
	egz := gzip.NewWriter(&empty)
	etw := tar.NewWriter(egz)
	etw.Close()
	egz.Close()
	if _, err := extractBinary(empty.Bytes()); err == nil {
		t.Error("a tarball without the binary should fail")
	}
}
