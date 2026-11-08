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
// recorded by `go install`, else "dev".
func versionString() string {
	if version != "dev" && version != "" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "dev"
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
// positional arguments.
func parseFlags(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			return positional, nil
		}
		if args[0] == "--" {
			return append(positional, args[1:]...), nil
		}
		positional = append(positional, args[0])
		args = args[1:]
	}
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
