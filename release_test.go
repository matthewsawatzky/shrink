package main

import (
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
