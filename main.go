// Command mochi runs the Mochi desktop app. Its subcommand, `issue`, signs
// certificates.
package main

import (
	"fmt"
	"os"

	"github.com/ashwin-y/mochi/app"
	"github.com/ashwin-y/mochi/cmd"
	"github.com/ashwin-y/mochi/internal/share"
)

func main() {
	args := os.Args[1:]
	for _, arg := range args {
		if arg == "--verbose" {
			share.Verbose = true
		}
	}
	var err error
	switch {
	case len(args) > 0 && args[0] == "issue":
		err = cmd.Issue(args[1:])
	default:
		err = app.Run(args)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "mochi:", err)
		os.Exit(1)
	}
}
