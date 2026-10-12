// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

package sshserver

import (
	"errors"
	"io"
	"os"

	"github.com/pkg/sftp"
	ssh "github.com/tailscale/gliderssh"
)

// sftpSessionHandler serves the sftp subsystem over the whole filesystem, with
// the same access a shell session has. Relative paths resolve against the
// account's home directory, matching OpenSSH's sftp-server.
func (s *Server) sftpSessionHandler(sess ssh.Session) {
	var opts []sftp.ServerOption
	if home, err := os.UserHomeDir(); err == nil {
		opts = append(opts, sftp.WithServerWorkingDirectory(home))
	}
	srv, err := sftp.NewServer(sess, opts...)
	if err != nil {
		s.logf("sftp session: %v", err)
		sess.Exit(1)
		return
	}
	err = srv.Serve()
	if errors.Is(err, io.EOF) {
		err = nil
	}
	// Exit must come before Close: Close closes the session channel, and an
	// exit-status sent after that is lost, making scp report failure for
	// successful transfers. Close still runs afterwards to release any
	// handles a client left open.
	if err != nil {
		s.logf("sftp session: %v", err)
		sess.Exit(1)
	} else {
		sess.Exit(0)
	}
	srv.Close()
}
