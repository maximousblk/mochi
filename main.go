// Command mochi runs the Mochi desktop app. Its subcommands are the tools the
// app's launchers call: `issue` signs certificates, and `connector-ssh-proxy`
// is the ProxyCommand the SSH launcher writes.
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
	case len(args) > 0 && args[0] == "connector-ssh-proxy":
		err = cmd.SSHProxy(args[1:])
	default:
		err = app.Run(args)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "mochi:", err)
		os.Exit(1)
	}
}
