package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/charmbracelet/x/term"

	"github.com/Aepzie/vigil/internal/auth"
	"github.com/Aepzie/vigil/internal/sshclient"
)

const userUsage = `Usage:
  vigil user add NAME [--password-stdin] [--data DIR]
  vigil user passwd NAME [--password-stdin] [--data DIR]
  vigil user rm NAME [--data DIR]
  vigil user list [--data DIR]

Passwords are read from the terminal without echo and must be entered
twice. With --password-stdin the first line of standard input is used.
Changes apply to a running server immediately.
`

func cmdUser(args []string, std stdio) error {
	if len(args) == 0 || isHelp(args[0]) || args[0] == "help" {
		fmt.Fprint(std.out, userUsage)
		if len(args) == 0 {
			return errors.New("missing user command")
		}
		return nil
	}
	action, rest := args[0], args[1:]
	fs := newFlagSet("user "+action, "user "+action+" ...", std)
	fs.Usage = func() { fmt.Fprint(std.err, userUsage) }
	data := fs.String("data", "", dataDirHelp)
	fromStdin := fs.Bool("password-stdin", false, "read the password from the first line of stdin")
	positional, err := parseFlags(fs, rest)
	if err != nil {
		return handleParseErr(err)
	}
	dataDir, err := resolveDataDir(*data)
	if err != nil {
		return err
	}
	authn, err := auth.New(dataDir)
	if err != nil {
		return err
	}

	switch action {
	case "list", "ls":
		if err := expectArgs(fs, positional, 0); err != nil {
			return err
		}
		for _, name := range authn.Users() {
			fmt.Fprintln(std.out, name)
		}
		return nil
	case "add", "passwd", "rm", "remove", "del":
	default:
		fs.Usage()
		return fmt.Errorf("unknown user command %q", action)
	}

	if err := expectArgs(fs, positional, 1); err != nil {
		return err
	}
	name := positional[0]
	if err := auth.ValidateName(name); err != nil {
		return err
	}
	exists := slices.Contains(authn.Users(), name)

	switch action {
	case "rm", "remove", "del":
		if err := authn.RemoveUser(name); err != nil {
			return err
		}
		fmt.Fprintf(std.err, "removed user %s\n", name)
		if !authn.HasCredentials() {
			fmt.Fprintln(std.err, "warning: no users or authorized keys left; nobody can log in")
		}
		return nil
	case "add":
		if exists {
			return fmt.Errorf("user %s already exists (use `vigil user passwd %s`)", name, name)
		}
	case "passwd":
		if !exists {
			return fmt.Errorf("unknown user %s (use `vigil user add %s`)", name, name)
		}
	}

	password, err := readNewPassword(std, *fromStdin)
	if err != nil {
		return err
	}
	if err := authn.SetPassword(name, password); err != nil {
		return err
	}
	if action == "add" {
		fmt.Fprintf(std.err, "created user %s\n", name)
	} else {
		fmt.Fprintf(std.err, "changed password of %s\n", name)
	}
	return nil
}

// readNewPassword reads a password from the first line of stdin, or
// interactively without echo with confirmation.
func readNewPassword(std stdio, fromStdin bool) (string, error) {
	if fromStdin {
		line, err := bufio.NewReader(std.in).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", fmt.Errorf("read password: %w", err)
		}
		password := strings.TrimRight(line, "\r\n")
		return password, auth.ValidatePassword(password)
	}
	f, ok := std.in.(*os.File)
	if !ok || !term.IsTerminal(f.Fd()) {
		return "", errors.New("stdin is not a terminal; use --password-stdin")
	}
	prompt := func(label string) (string, error) {
		fmt.Fprint(std.err, label)
		raw, err := term.ReadPassword(f.Fd())
		fmt.Fprintln(std.err)
		if err != nil {
			return "", fmt.Errorf("read password: %w", err)
		}
		return string(raw), nil
	}
	password, err := prompt("New password: ")
	if err != nil {
		return "", err
	}
	if err := auth.ValidatePassword(password); err != nil {
		return "", err
	}
	again, err := prompt("Repeat password: ")
	if err != nil {
		return "", err
	}
	if again != password {
		return "", errors.New("passwords do not match")
	}
	return password, nil
}

func cmdPubkey(args []string, std stdio) error {
	fs := newFlagSet("pubkey", "pubkey [--data DIR]", std)
	data := fs.String("data", "", dataDirHelp)
	rest, err := parseFlags(fs, args)
	if err != nil {
		return handleParseErr(err)
	}
	if err := expectArgs(fs, rest, 0); err != nil {
		return err
	}
	dataDir, err := resolveDataDir(*data)
	if err != nil {
		return err
	}
	dialer, err := sshclient.NewDialer(dataDir)
	if err != nil {
		return fmt.Errorf("manager key: %w", err)
	}
	key := strings.TrimSpace(dialer.PublicKey())
	if key == "" {
		return errors.New("no manager key available")
	}
	_, err = fmt.Fprintln(std.out, key)
	return err
}
