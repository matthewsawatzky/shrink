package main

import (
	"fmt"
	"os"
	"strings"
)

// completionWords lists every target and flag a user can type, gathered
// from the same tables the CLI parses with, so completions can never
// drift from what shrink actually accepts.
func completionWords() []string {
	var words []string
	words = append(words, sortedKeys(resolutions)...)
	words = append(words, sortedKeys(videoFormats)...)
	words = append(words, sortedKeys(imageFormats)...)
	words = append(words, sortedKeys(audioFormats)...)
	words = append(words, sortedKeys(compressionLevels)...)
	words = append(words,
		"-v", "--verbose",
		"-r", "--recursive",
		"-j", "--jobs",
		"-f", "--fast",
		"-n", "--dry-run",
		"-o", "--overwrite",
		"-d", "--delete",
		"-m", "--machine",
		"-c", "--completions",
		"-V", "--version",
	)
	return words
}

// printCompletions writes a completion script for zsh or bash to stdout
// and exits; the output is meant to be sourced or dropped into a
// completion directory. Targets and flags always match what shrink
// accepts, because they come from the same tables.
func printCompletions(shell string) {
	words := completionWords()
	switch shell {
	case "zsh":
		fmt.Printf(`#compdef shrink
# zsh completion for shrink - drop this file on your fpath as _shrink
# (e.g. ~/.zfunc/_shrink, with "fpath=(~/.zfunc $fpath)" before compinit),
# or source it directly from your ~/.zshrc.
local -a targets
targets=(
	%s
)
_values -w 'shrink target' $targets
`, quotedWords(words, "\t"))
	case "bash":
		fmt.Printf(`# bash completion for shrink - source this file from your bashrc, or
# drop it in a completion directory (e.g. $(brew --prefix)/etc/bash_completion.d/).
_shrink() {
	local cur targets
	cur="${COMP_WORDS[COMP_CWORD]}"
	targets="%s"
	COMPREPLY=($(compgen -W "$targets" -- "$cur"))
}
complete -F _shrink shrink
`, strings.Join(words, " "))
	default:
		exitError("completions support zsh or bash, not %q", shell)
	}
	os.Exit(0)
}

// quotedWords renders words as single-quoted, one per line.
func quotedWords(words []string, indent string) string {
	q := make([]string, len(words))
	for i, w := range words {
		q[i] = "'" + w + "'"
	}
	return strings.Join(q, "\n"+indent)
}
