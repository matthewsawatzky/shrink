package main

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode"
)

func (a *application) printUpdateHeader() {
	if a.machine {
		return
	}
	current := normVersion(version)
	if version == "dev" {
		current = "dev (local build)"
	}
	fmt.Fprintf(a.stderr, "\n%s\n\n  Current   %s\n", a.ui.bold("shrink update"), a.ui.dim(current))
}

func (a *application) printUpdateSize(before, after int64) {
	delta := "size unchanged"
	if before > 0 && after != before {
		percent := (float64(after)/float64(before) - 1) * 100
		if after > before {
			delta = a.ui.yellow(fmt.Sprintf("+%.1f%% larger", percent))
		} else {
			delta = a.ui.green(fmt.Sprintf("%.1f%% smaller", -percent))
		}
	} else if before <= 0 {
		delta = "percentage unavailable"
	}
	fmt.Fprintf(a.stderr, "  Binary    %s -> %s  (%s)\n", a.humanSize(before), a.humanSize(after), delta)
}

type releaseHighlights struct {
	Added, Changed, Fixed []string
}

var releaseMarkdownLink = regexp.MustCompile(`!?\[([^\]]+)\]\([^)]*\)`)

// Release text is remote Markdown. Render a bounded plain-text summary, without
// passing terminal control characters or Markdown styling to the terminal.
func releaseSummaryText(text string) string {
	text = releaseMarkdownLink.ReplaceAllString(text, "$1")
	text = strings.NewReplacer("`", "", "**", "", "__", "").Replace(text)
	text = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf) {
			return ' '
		}
		return r
	}, text)
	text = strings.Join(strings.Fields(text), " ")
	runes := []rune(text)
	if len(runes) > 140 {
		text = strings.TrimSpace(string(runes[:137])) + "..."
	}
	return text
}

func summarizeRelease(body string) releaseHighlights {
	var highlights releaseHighlights
	var section *[]string
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			section = nil
			title := strings.ToLower(strings.TrimSpace(strings.Trim(trimmed, "# \t\r")))
			switch title {
			case "added":
				section = &highlights.Added
			case "changed":
				section = &highlights.Changed
			case "fixed":
				section = &highlights.Fixed
			}
			continue
		}
		// Skip nested lists; their parent already describes the change.
		if section == nil || len(*section) >= 3 || strings.HasPrefix(line, "\t") || len(line)-len(strings.TrimLeft(line, " ")) > 3 {
			continue
		}
		if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") || strings.HasPrefix(trimmed, "+ ") {
			if text := releaseSummaryText(trimmed[2:]); text != "" {
				*section = append(*section, text)
			}
		}
	}
	return highlights
}

func (a *application) printUpdateNotes(release ghRelease) {
	highlights := summarizeRelease(release.Body)
	fmt.Fprintf(a.stderr, "  %s\n", a.ui.bold("Release highlights"))
	count := 0
	for _, section := range []struct {
		name  string
		items []string
	}{{"Added", highlights.Added}, {"Changed", highlights.Changed}, {"Fixed", highlights.Fixed}} {
		if len(section.items) == 0 {
			continue
		}
		count += len(section.items)
		fmt.Fprintf(a.stderr, "\n  %s\n", a.ui.cyan(section.name))
		for _, item := range section.items {
			fmt.Fprintf(a.stderr, "    - %s\n", item)
		}
	}
	if count == 0 {
		fmt.Fprintln(a.stderr, "    No short summary available; see the full changelog below.")
	}
	fmt.Fprintf(a.stderr, "\n  Full changelog\n  https://github.com/%s/releases/tag/%s\n\n", updateRepo, url.PathEscape(release.TagName))
}
