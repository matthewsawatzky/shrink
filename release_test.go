package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func releaseTool(t *testing.T, tool string, args ...string) (string, error) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX release tooling")
	}
	out, err := exec.Command("sh", append([]string{filepath.Join("scripts", tool)}, args...)...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func TestReleaseVersionPolicy(t *testing.T) {
	for _, c := range []struct{ bump, base, want string }{
		{"major", "v8.4.1", "v9.0.0"}, {"minor", "8.4.1", "v8.5.0"}, {"patch", "v8.4.1", "v8.4.2"},
		{"major", "v0.8.1", "v1.0.0"}, {"minor", "v0.8.1", "v0.9.0"}, {"patch", "v0.8.1", "v0.8.2"},
		{"patch", "v1.9.9", "v1.9.10"}, {"minor", "v1.9.9", "v1.10.0"},
	} {
		out, err := releaseTool(t, "next-version.sh", c.bump, c.base)
		if err != nil || out != c.want {
			t.Errorf("%s %s: %q %v; want %s", c.bump, c.base, out, err, c.want)
		}
	}
	for _, base := range []string{"v01.2.3", "1.2", "1.2.3-rc1", "garbage", "1.2.3.4"} {
		if out, err := releaseTool(t, "next-version.sh", "patch", base); err == nil {
			t.Errorf("accepted %q: %s", base, out)
		}
	}
	if out, err := releaseTool(t, "next-version.sh", "other", "v1.0.0"); err == nil {
		t.Errorf("accepted invalid bump: %s", out)
	}
}

func TestReleaseNotesAreExtractedFromMatchingSection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "CHANGELOG.md")
	changelog := "# Changelog\n\n## [Unreleased]\n\n### Added\n- Future feature\n\n## [1.2.3] - 2026-10-27\n\n### Fixed\n\n- The actual release fix.\n\n## [1.2.2] - 2026-10-26\n\n- Old release.\n"
	if err := os.WriteFile(path, []byte(changelog), 0644); err != nil {
		t.Fatal(err)
	}
	out, err := releaseTool(t, "release-notes.sh", "v1.2.3", path)
	if err != nil || out != "### Fixed\n\n- The actual release fix." {
		t.Fatalf("release notes: %q %v", out, err)
	}
}

func TestReleaseNotesRejectMissingDuplicateAndEmptyEntries(t *testing.T) {
	for _, body := range []string{
		"## [Unreleased]\n- Unreleased notes\n",
		"## [1.0.0] - 2026-10-27\n\n### Fixed\n",
		"## [1.0.0] - invalid-date\n- Fix\n",
		"## [1.0.0] - 2026-10-27\n- First\n## [1.0.0] - 2026-10-27\n- Duplicate\n",
	} {
		path := filepath.Join(t.TempDir(), "CHANGELOG.md")
		if err := os.WriteFile(path, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
		if out, err := releaseTool(t, "release-notes.sh", "v1.0.0", path); err == nil {
			t.Errorf("accepted unusable notes %q: %s", body, out)
		}
	}
}

// Publishing tests run the real shell helper with local artifacts and a fake gh.
// They never contact GitHub or modify this repository's tags/releases.
func publishFixture(t *testing.T) (string, string, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX release tooling")
	}
	root := t.TempDir()
	for _, dir := range []string{"scripts", "dist", "bin"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	script, err := os.ReadFile("scripts/publish-release.sh")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "scripts/publish-release.sh"), script, 0755); err != nil {
		t.Fatal(err)
	}
	var checksums strings.Builder
	for _, platform := range []string{"darwin-arm64", "darwin-amd64", "linux-arm64", "linux-amd64", "windows-arm64", "windows-amd64"} {
		name := "shrink-v1.2.3-" + platform + ".tar.gz"
		data := []byte("fixture " + platform)
		if err := os.WriteFile(filepath.Join(root, "dist", name), data, 0644); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&checksums, "%x  %s\n", sha256.Sum256(data), name)
	}
	if err := os.WriteFile(filepath.Join(root, "dist/shrink-v1.2.3-checksums.txt"), []byte(checksums.String()), 0644); err != nil {
		t.Fatal(err)
	}
	notes := filepath.Join(root, "notes.md")
	if err := os.WriteFile(notes, []byte("### Fixed\n- A release fix.\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gh := `#!/bin/sh
printf '%s\n' "$*" >> "$PUBLISH_TEST_LOG"
case "$1 $2" in
 "release view")
  case " $* " in
   *" isDraft "*)
    if [ "$PUBLISH_TEST_STATE" = missing ]; then exit 1; fi
    printf '%s\n' "$PUBLISH_TEST_STATE" ;;
   *) printf '%s\n' 'https://example.test/release/v1.2.3' ;;
  esac ;;
 "release upload"|"release create")
  if [ "${PUBLISH_TEST_FAIL_UPLOAD:-0}" = 1 ]; then exit 1; fi ;;
esac
`
	if err := os.WriteFile(filepath.Join(root, "bin/gh"), []byte(gh), 0755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(root, "gh.log")
	t.Setenv("PATH", filepath.Join(root, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PUBLISH_TEST_LOG", log)
	return root, notes, log
}

func runPublishFixture(root, notes string) (string, error) {
	cmd := exec.Command("sh", filepath.Join(root, "scripts/publish-release.sh"), "v1.2.3", notes)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestLocalPublisherUploadsBeforeMakingReleasePublic(t *testing.T) {
	for _, state := range []string{"missing", "true"} {
		t.Run(state, func(t *testing.T) {
			root, notes, log := publishFixture(t)
			t.Setenv("PUBLISH_TEST_STATE", state)
			out, err := runPublishFixture(root, notes)
			if err != nil {
				t.Fatalf("publish: %v %s", err, out)
			}
			calls, _ := os.ReadFile(log)
			upload := "release create"
			if state == "true" {
				upload = "release upload"
			}
			before, after := strings.Index(string(calls), upload), strings.Index(string(calls), "release edit")
			if before < 0 || after <= before || !strings.Contains(string(calls), "--draft=false") {
				t.Fatalf("publication order: %s", calls)
			}
			if state == "missing" && !strings.Contains(string(calls), "--draft --verify-tag") {
				t.Fatalf("release was not staged as a draft: %s", calls)
			}
		})
	}
}

func TestLocalPublisherDoesNotPublishAfterFailedUpload(t *testing.T) {
	root, notes, log := publishFixture(t)
	t.Setenv("PUBLISH_TEST_STATE", "true")
	t.Setenv("PUBLISH_TEST_FAIL_UPLOAD", "1")
	if out, err := runPublishFixture(root, notes); err == nil {
		t.Fatalf("upload failure ignored: %s", out)
	}
	calls, _ := os.ReadFile(log)
	if strings.Contains(string(calls), "release edit") {
		t.Fatalf("published an incomplete release: %s", calls)
	}
}

func TestLocalPublisherRejectsIncompleteCorruptOrPublishedRelease(t *testing.T) {
	for _, reason := range []string{"missing", "corrupt", "published"} {
		t.Run(reason, func(t *testing.T) {
			root, notes, log := publishFixture(t)
			t.Setenv("PUBLISH_TEST_STATE", "false")
			asset := filepath.Join(root, "dist/shrink-v1.2.3-linux-arm64.tar.gz")
			switch reason {
			case "missing":
				if err := os.Remove(asset); err != nil {
					t.Fatal(err)
				}
			case "corrupt":
				if err := os.WriteFile(asset, []byte("corrupted"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			if out, err := runPublishFixture(root, notes); err == nil {
				t.Fatalf("accepted %s release: %s", reason, out)
			}
			calls, _ := os.ReadFile(log)
			for _, mutation := range []string{"release create", "release upload", "release edit"} {
				if strings.Contains(string(calls), mutation) {
					t.Fatalf("mutated %s release: %s", reason, calls)
				}
			}
		})
	}
}
