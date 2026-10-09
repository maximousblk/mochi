package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/ashwin-y/mochi/internal/admission"
	"github.com/ashwin-y/mochi/internal/connector/ssh"
	"github.com/ashwin-y/mochi/internal/state"
)

// SSHProxy implements `mochi connector-ssh-proxy <tailcat-address> <port>`, the
// ProxyCommand that the SSH launcher writes. It runs headlessly, with its
// diagnostics on stderr, and relays stdin and stdout to the host.
func SSHProxy(args []string) error {
	client, err := state.LoadClient()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	dialing := admission.Dialing{
		Key:   client.Identity.NodeKey(state.RoleClient),
		Certs: func() [][]byte { return client.Certs },
	}
	if err := ssh.Proxy(ctx, client.Hosts, args, os.Stdin, os.Stdout, dialing); err != nil {
		return fmt.Errorf("connector-ssh-proxy: %w", err)
	}
	return nil
}
