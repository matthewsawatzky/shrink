package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type cliRequest struct {
	args          []string
	flags         map[string]bool
	recursive     bool
	jobs          int
	completions   string
	help, version bool
}

type commandSpec struct {
	name, usage, flags string
	run                func(cliRequest)
}

// One registration supplies dispatch, command help and completion vocabulary.
func (a *application) commands() []commandSpec {
	return []commandSpec{
		{"folder", "folder <folder> [target...]", "vmfnodrj", func(c cliRequest) { a.runMedia(c.args, c.recursive, c.jobs, true) }},
		{"info", "info <file>...", "vm", func(c cliRequest) { a.runInfo(c.args) }},
		{"doctor", "doctor", "m", func(c cliRequest) { a.noArguments("doctor", c.args); a.runDoctor() }},
		{"make", "make [target...]", "vmno", func(c cliRequest) { a.runMake(c.args) }},
		{"update", "update", "mno", func(c cliRequest) { a.noArguments("update", c.args); a.runUpdate() }},
	}
}

func (a *application) noArguments(name string, args []string) {
	if len(args) > 0 {
		a.exitError("%s takes no arguments - just: shrink %s", name, name)
	}
}

func (a *application) parseCLI(args []string) (cliRequest, error) {
	c := cliRequest{jobs: 2, flags: map[string]bool{}}
	// Determine the output mode before reporting any parsing error.
	for _, arg := range args {
		if arg == "--" {
			break
		}
		if arg == "-m" || arg == "--machine" {
			a.machine = true
		}
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			c.args = append(c.args, args[i+1:]...)
			break
		}
		flag := ""
		switch arg {
		case "-h", "--help":
			c.help = true
		case "-V", "--version":
			c.version = true
		case "-v", "--verbose":
			a.verbose = true
			flag = "v"
		case "-m", "--machine":
			a.machine = true
			flag = "m"
		case "-r", "--recursive":
			c.recursive = true
			flag = "r"
		case "-f", "--fast":
			a.fast = true
			flag = "f"
		case "-n", "--dry-run":
			a.dryRun = true
			flag = "n"
		case "-o", "--overwrite":
			a.overwrite = true
			flag = "o"
		case "-d", "--delete":
			a.deleteAfter = true
			flag = "d"
		case "-j", "--jobs", "-c", "--completions":
			i++
			if i >= len(args) {
				return c, fmt.Errorf("%s needs a value", arg)
			}
			if arg == "-c" || arg == "--completions" {
				c.completions = args[i]
			} else {
				n, err := strconv.Atoi(args[i])
				if err != nil || n < 1 {
					return c, fmt.Errorf("jobs needs a number of 1 or more, got %q", args[i])
				}
				c.jobs = n
				flag = "j"
			}
		default:
			if strings.HasPrefix(arg, "-") {
				return c, fmt.Errorf("unknown flag: %s (use -- before a filename starting with -)", arg)
			}
			c.args = append(c.args, arg)
		}
		if flag != "" {
			c.flags[flag] = true
		}
	}
	return c, nil
}

func (a *application) validateFlags(c cliRequest, name, allowed string) {
	for _, flag := range sortedKeys(c.flags) {
		if !strings.Contains(allowed, flag) {
			a.exitError("-%s does not apply to %s", flag, name)
		}
	}
}

func (a *application) runArgs(args []string) {
	c, err := a.parseCLI(args)
	if err != nil {
		a.exitError("%v", err)
	}
	if c.help {
		a.usage()
		return
	}
	if c.version {
		fmt.Fprintln(a.stdout, "shrink "+version)
		return
	}
	if c.completions != "" {
		a.printCompletions(c.completions)
		return
	}
	if len(c.args) == 0 {
		a.usage()
		exitCLI(1)
	}
	for _, spec := range a.commands() {
		if c.args[0] == spec.name {
			a.validateFlags(c, spec.name, spec.flags)
			c.args = c.args[1:]
			spec.run(c)
			return
		}
	}
	allowed := "vmfnod"
	if stat, err := os.Stat(c.args[0]); err == nil && stat.IsDir() {
		allowed += "rj"
	}
	a.validateFlags(c, "file conversion", allowed)
	a.runMedia(c.args, c.recursive, c.jobs, false)
}
