// Command vigil is an SSH-served monitoring dashboard for a fleet of
// Linux servers.
package main

import (
	"fmt"
	"os"
)

var version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "vigil:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	return nil
}
