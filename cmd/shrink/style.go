package main

import "os"

// Minimal SGR palette: greyscale chrome with a few accents, each carrying
// meaning (cyan = progress, green = success, yellow = warning, red = error).
const (
	sgrReset  = "\x1b[0m"
	sgrDim    = "2"
	sgrBold   = "1"
	sgrRed    = "31"
	sgrGreen  = "32"
	sgrYellow = "33"
	sgrCyan   = "36"
)

// styler emits ANSI escapes only when its stream is an interactive
// terminal and colors aren't disabled, so pipes and logs stay clean.
type styler struct{ on bool }

func newStyler(f *os.File) styler {
	if os.Getenv("TERM") == "dumb" {
		return styler{}
	}
	if _, noColor := os.LookupEnv("NO_COLOR"); noColor {
		return styler{}
	}
	return styler{on: isTerminal(f)}
}

func (s styler) paint(code, text string) string {
	if !s.on {
		return text
	}
	return "\x1b[" + code + "m" + text + sgrReset
}

func (s styler) dim(t string) string    { return s.paint(sgrDim, t) }
func (s styler) bold(t string) string   { return s.paint(sgrBold, t) }
func (s styler) red(t string) string    { return s.paint(sgrRed, t) }
func (s styler) green(t string) string  { return s.paint(sgrGreen, t) }
func (s styler) yellow(t string) string { return s.paint(sgrYellow, t) }
func (s styler) cyan(t string) string   { return s.paint(sgrCyan, t) }

func (a *application) doneMark() string {
	if a.ui.on {
		return a.ui.green("✓")
	}
	return "done:"
}

func (a *application) errMark() string {
	if a.ui.on {
		return a.ui.red("✗")
	}
	return "error:"
}
