package ssh

import (
	"context"
	"io"
	"net"
	"strings"
	"testing"

	"github.com/ashwin-y/mochi/internal/admission"
	"github.com/ashwin-y/mochi/internal/state"
)

func TestProxyCommandValidation(t *testing.T) {
	ctx := context.Background()
	if err := Proxy(ctx, nil, nil, strings.NewReader(""), io.Discard, admission.Dialing{}); err == nil || !strings.Contains(err.Error(), "usage:") {
		t.Fatalf("missing arguments error = %v", err)
	}
	if err := Proxy(ctx, nil, []string{"not-a-tailcat-address", "22"}, strings.NewReader(""), io.Discard, admission.Dialing{}); err == nil || !strings.Contains(err.Error(), "invalid Tailcat address") {
		t.Fatalf("invalid address error = %v", err)
	}
}

func TestCanonicalAddressRestoresTailcatCaseFoldedByOpenSSH(t *testing.T) {
	want := state.Host{Name: "lab", Address: "tcAbCdEf_123-XY"}
	if got := canonicalAddress([]state.Host{want}, strings.ToLower(want.Address)); got != want.Address {
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
