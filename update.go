package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// updateRepo is where releases come from: one place for the API base
// and download URLs.
const (
	updateRepo = "matthewsawatzky/shrink"
	updateAPI  = "https://api.github.com/repos/" + updateRepo
)

// ghRelease is the slice of the GitHub release API shrink update uses.
type ghRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

// machineUpdate is the -m document for a shrink update run.
type machineUpdate struct {
	Version     string `json:"version"`
	Updated     bool   `json:"updated"`
	UpdatedFrom string `json:"updated_from,omitempty"`
	UpdatedTo   string `json:"updated_to,omitempty"`
	Note        string `json:"note,omitempty"`
}

// downloadFile fetches url, capping the read so a bad URL can't stream
// forever (release tarballs are a couple of megabytes).
func downloadFile(url string) ([]byte, error) {
	client := &http.Client{Timeout: 60 * time.Second}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "shrink/"+version)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d for %s", resp.StatusCode, url)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 64<<20))
}

// parseRelease decodes a GitHub release document.
func parseRelease(body []byte) (ghRelease, error) {
	var r ghRelease
	if err := json.Unmarshal(body, &r); err != nil {
		return r, err
	}
	if r.TagName == "" {
		return r, fmt.Errorf("no tag in the release document")
	}
	return r, nil
}

// fetchLatestRelease asks GitHub for the newest published release.
func fetchLatestRelease() (ghRelease, error) {
	body, err := downloadFile(updateAPI + "/releases/latest")
	if err != nil {
		return ghRelease{}, err
	}
	return parseRelease(body)
}

// normVersion puts the v back on a version, so stamped builds and API
// tags compare cleanly.
func normVersion(v string) string {
	return "v" + strings.TrimPrefix(v, "v")
}

// sha256hex is the hex digest of b.
func sha256hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// checksumFor pulls the recorded digest for name out of a release
// checksums file.
func checksumFor(name, checksums string) string {
	for _, line := range strings.Split(checksums, "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && f[1] == name {
			return f[0]
		}
	}
	return ""
}

// assetURL finds an asset's download URL by name.
func assetURL(release ghRelease, name string) string {
	for _, a := range release.Assets {
		if a.Name == name {
			return a.BrowserDownloadURL
		}
	}
	return ""
}

// extractBinary pulls the shrink binary out of a release tarball.
func extractBinary(tarball []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(tarball))
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil, fmt.Errorf("no shrink binary in the tarball")
		}
		if err != nil {
			return nil, err
		}
		if hdr.Name == "shrink" || hdr.Name == "shrink.exe" {
			return io.ReadAll(tr)
		}
	}
}

// brewManaged reports whether path lives under a Homebrew prefix, in
// which case the package manager owns updates and shrink stays out of
// the way.
func brewManaged(path string) bool {
	brew, err := exec.LookPath("brew")
	if err != nil {
		return false
	}
	out, err := exec.Command(brew, "--prefix").Output()
	if err != nil {
		return false
	}
	prefix := strings.TrimSpace(string(out))
	if prefix == "" {
		return false
	}
	return strings.HasPrefix(path, prefix+string(os.PathSeparator)) || path == prefix
}

// runUpdate replaces the running binary with the newest release: check
// for one, verify its checksum, swap atomically. No-op when current,
// hands off to brew when Homebrew owns the install.
func runUpdate() {
	self, err := os.Executable()
	if err != nil {
		exitError("could not locate the running shrink: %v", err)
	}
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		self = resolved
	}

	if brewManaged(self) {
		note := "this shrink is managed by Homebrew - update it with: brew upgrade shrink"
		if machine {
			printJSON(machineUpdate{Version: version, Note: note})
		} else {
			fmt.Fprintf(os.Stderr, "%s %s\n", ui.yellow("!"), ui.yellow(note))
		}
		os.Exit(0)
	}

	release, err := fetchLatestRelease()
	if err != nil {
		exitError("could not check for updates: %v", err)
	}
	current := normVersion(version)
	latest := normVersion(release.TagName)
	if current == latest {
		if machine {
			printJSON(machineUpdate{Version: current})
		} else {
			fmt.Fprintf(os.Stderr, "%s shrink %s is up to date\n", doneMark(), current)
		}
		os.Exit(0)
	}
	if current == "vdev" && !overwrite {
		exitError("this is a local dev build - shrink update would replace it with the %s release; re-run with -o to continue", latest)
	}

	assetName := fmt.Sprintf("shrink-%s-%s-%s.tar.gz", latest, runtime.GOOS, runtime.GOARCH)
	url := assetURL(release, assetName)
	if url == "" {
		exitError("the %s release has no %s/%s build", latest, runtime.GOOS, runtime.GOARCH)
	}
	if dryRun {
		note := fmt.Sprintf("dry run: would update shrink %s -> %s", current, latest)
		if machine {
			printJSON(machineUpdate{Version: current, UpdatedTo: latest, Note: note})
		} else {
			fmt.Fprintf(os.Stderr, "%s %s\n", doneMark(), ui.dim(note))
		}
		os.Exit(0)
	}

	if !machine {
		fmt.Fprintf(os.Stderr, "%s\n", ui.cyan("updating "+current+" -> "+latest+"..."))
	}
	tarball, err := downloadFile(url)
	if err != nil {
		exitError("download failed: %v", err)
	}
	checksums, err := downloadFile(assetURL(release, "shrink-"+latest+"-checksums.txt"))
	if err != nil {
		exitError("could not fetch the release checksums: %v", err)
	}
	want := checksumFor(assetName, string(checksums))
	if want == "" {
		exitError("the release is missing a checksum for %s", assetName)
	}
	if got := sha256hex(tarball); got != want {
		exitError("checksum mismatch for %s (got %s) - the download may be corrupted, try again", assetName, got)
	}
	bin, err := extractBinary(tarball)
	if err != nil {
		exitError("could not unpack the release: %v", err)
	}

	// swap: write the new binary beside the old one, then two renames.
	// Moving the running binary out of the way first also works on
	// Windows, where a running exe cannot be deleted but can be moved.
	tmp := self + ".new"
	old := self + ".old"
	if err := os.WriteFile(tmp, bin, 0o755); err != nil {
		exitError("could not write %s: %v", tmp, err)
	}
	if err := os.Rename(self, old); err != nil {
		os.Remove(tmp)
		exitError("could not move the running binary out of the way: %v", err)
	}
	if err := os.Rename(tmp, self); err != nil {
		os.Rename(old, self) // put the running binary back
		os.Remove(tmp)
		exitError("could not put the new binary in place: %v", err)
	}
	if err := os.Remove(old); err != nil && !machine {
		fmt.Fprintf(os.Stderr, "  %s\n", ui.dim("could not remove the old binary "+old+" - delete it later: "+err.Error()))
	}

	// best effort: confirm the new binary answers
	if out, err := exec.Command(self, "-V").Output(); err == nil {
		if got := strings.TrimSpace(string(out)); got != "shrink "+latest {
			fmt.Fprintf(os.Stderr, "  %s\n", ui.dim("note: the new binary reports "+got))
		}
	}

	if machine {
		printJSON(machineUpdate{Version: latest, Updated: true, UpdatedFrom: current, UpdatedTo: latest})
	} else {
		fmt.Fprintf(os.Stderr, "%s updated %s -> %s\n", doneMark(), ui.dim(current), ui.bold(latest))
	}
	os.Exit(0)
}
