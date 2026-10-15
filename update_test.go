package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
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
