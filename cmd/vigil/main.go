// Command vigil is an SSH-served monitoring dashboard for a fleet of
// Linux servers.
//
// Run `vigil help` for the list of subcommands.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"runtime/debug"
	"strings"
)

// version is set at build time with -ldflags "-X main.version=v1.2.3".
var version = "dev"

const usage = `vigil - SSH-served server monitoring dashboard

Usage:
  vigil [serve] [flags]          run the SSH dashboard server (default)
  vigil local [--data DIR]       run the dashboard in this terminal
  vigil demo [--serve]           run with a synthetic fleet
  vigil render [flags]           print one demo frame (screenshots, docs)
  vigil user add|passwd|rm NAME  manage dashboard users
  vigil user list                list dashboard users
  vigil pubkey [--data DIR]      print the manager public key for targets
  vigil version                  print the version
  vigil help                     show this help

Run "vigil COMMAND -h" for the flags of a command.

The data directory defaults to $VIGIL_DATA, else /var/lib/vigil when
running as root on Linux, else the user config directory (e.g.
~/.config/vigil).
`

// stdio bundles the standard streams so commands can be tested.
type stdio struct {
	in  io.Reader
	out io.Writer
	err io.Writer
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "vigil:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	return dispatch(args, stdio{in: os.Stdin, out: os.Stdout, err: os.Stderr})
}

// dispatch runs the subcommand named by args[0]; flags without a
// subcommand go to serve.
func dispatch(args []string, std stdio) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") && !isHelp(args[0]) {
		return cmdServe(args, std)
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "serve":
		return cmdServe(rest, std)
	case "local":
		return cmdLocal(rest, std)
	case "demo":
		return cmdDemo(rest, std)
	case "render":
		return cmdRender(rest, std)
	case "user", "users":
		return cmdUser(rest, std)
	case "pubkey":
		return cmdPubkey(rest, std)
	case "version", "--version":
		fmt.Fprintln(std.out, "vigil", versionString())
		return nil
	case "help", "-h", "-help", "--help":
		fmt.Fprint(std.out, usage)
		return nil
	}
	fmt.Fprint(std.err, usage)
	return fmt.Errorf("unknown command %q", cmd)
}

func isHelp(arg string) bool {
	return arg == "-h" || arg == "-help" || arg == "--help" || arg == "--version"
}

// versionString returns the -ldflags version, else the module version
// recorded by `go install`, else "dev". Pseudo-versions stamped by local
// builds (v0.0.0-20261108035403-65ac643cd3c8+dirty) are shortened to
// "dev-65ac643" so they fit the dashboard header.
func versionString() string {
	if version != "dev" && version != "" {
		return version
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	return buildVersion(bi.Main.Version, bi.Settings)
}

var pseudoVersion = regexp.MustCompile(`(^|[.-])\d{14}-([0-9a-f]{12})(\+dirty)?$`)

// buildVersion derives a display version from build info.
func buildVersion(modVersion string, settings []debug.BuildSetting) string {
	if modVersion != "" && modVersion != "(devel)" && !pseudoVersion.MatchString(modVersion) {
		return modVersion
	}
	rev, dirty := "", false
	for _, s := range settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev == "" {
		if m := pseudoVersion.FindStringSubmatch(modVersion); m != nil {
			rev, dirty = m[2], m[3] != ""
		}
	}
	if rev == "" {
		return "dev"
	}
	v := "dev-" + rev[:min(7, len(rev))]
	if dirty {
		v += "+dirty"
	}
	return v
}

// newFlagSet creates a flag set that reports errors instead of exiting.
func newFlagSet(name, synopsis string, std stdio) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(std.err)
	fs.Usage = func() {
		fmt.Fprintf(std.err, "Usage: vigil %s\n\nFlags:\n", synopsis)
		fs.PrintDefaults()
	}
	return fs
}

// parseFlags parses args allowing flags and positional arguments to be
// interleaved ("user add bob --password-stdin"), and returns the
// positional arguments. A "--" argument ends flag parsing: everything
// after it is positional ("user add -- -bob").
func parseFlags(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		if endedByTerminator(fs, args[:len(args)-len(rest)]) {
			return append(positional, rest...), nil
		}
		if len(rest) == 0 {
			return positional, nil
		}
		positional = append(positional, rest[0])
		args = rest[1:]
	}
}

// endedByTerminator reports whether fs.Parse consumed a "--" terminator
// as the last of the consumed arguments. The flag package swallows it
// silently, so the consumed arguments are replayed: a "--" that is the
// value of a preceding flag ("--data --") is not a terminator.
func endedByTerminator(fs *flag.FlagSet, consumed []string) bool {
	for i := 0; i < len(consumed); i++ {
		arg := consumed[i]
		if arg == "--" {
			return true
		}
		name := strings.TrimPrefix(strings.TrimPrefix(arg, "-"), "-")
		if strings.Contains(name, "=") {
			continue
		}
		if f := fs.Lookup(name); f != nil && !isBoolFlag(f) {
			i++
		}
	}
	return false
}

func isBoolFlag(f *flag.Flag) bool {
	b, ok := f.Value.(interface{ IsBoolFlag() bool })
	return ok && b.IsBoolFlag()
}

// expectArgs checks the number of positional arguments.
func expectArgs(fs *flag.FlagSet, args []string, n int) error {
	if len(args) == n {
		return nil
	}
	fs.Usage()
	if len(args) < n {
		return errors.New("missing argument")
	}
	return fmt.Errorf("unexpected argument %q", args[n])
}

// handleParseErr turns a help request into a clean exit.
func handleParseErr(err error) error {
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	return err
}
