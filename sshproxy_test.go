package main

import (
	"context"
	"io"
	"net"
	"strings"
	"testing"
)

func TestSSHConnectorCommandValidation(t *testing.T) {
	connector := NewSSHConfigConnector("", nil)
	if got := connector.CLICommand(); got != "connector-ssh-proxy" {
		t.Fatalf("CLI command = %q", got)
	}
	if err := connector.RunCommand(context.Background(), nil, strings.NewReader(""), io.Discard, Dialing{}); err == nil || !strings.Contains(err.Error(), "usage:") {
		t.Fatalf("missing arguments error = %v", err)
	}
	if err := connector.RunCommand(context.Background(), []string{"not-a-tailcat-address", "22"}, strings.NewReader(""), io.Discard, Dialing{}); err == nil || !strings.Contains(err.Error(), "invalid Tailcat address") {
		t.Fatalf("invalid address error = %v", err)
	}
}

func TestCanonicalAddressRestoresTailcatCaseFoldedByOpenSSH(t *testing.T) {
	connector := NewSSHConfigConnector("", nil)
	want := Host{Name: "lab", Address: "tcAbCdEf_123-XY"}
	connector.SetHosts([]Host{want})
	if got := connector.canonicalAddress(strings.ToLower(want.Address)); got != want.Address {
		t.Fatalf("canonical address = %q, want original case %q", got, want.Address)
	}
}

func TestProxyStreamsPropagatesHalfCloseAndResponse(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	remoteResult := make(chan error, 1)
	go func() {
		serverConn, err := listener.Accept()
		if err != nil {
			remoteResult <- err
			return
		}
		defer serverConn.Close()
		request, err := io.ReadAll(serverConn)
		if err != nil {
			remoteResult <- err
			return
		}
		_, err = serverConn.Write([]byte(strings.ToUpper(string(request))))
		remoteResult <- err
	}()

	clientConn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer clientConn.Close()
	var response strings.Builder
	if err := proxyStreams(clientConn, strings.NewReader("hello"), &response); err != nil {
		t.Fatalf("proxy streams: %v", err)
	}
	if err := <-remoteResult; err != nil {
		t.Fatalf("remote echo: %v", err)
	}
	if got := response.String(); got != "HELLO" {
		t.Fatalf("response = %q, want HELLO", got)
	}
}
