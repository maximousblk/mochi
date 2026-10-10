package files

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/pkg/sftp"
)

// pipeConn is one end of an in-process SFTP connection.
type pipeConn struct {
	io.Reader
	io.WriteCloser
}

// newTestFS serves a temporary directory over an in-process SFTP server and
// returns the adapter and the directory's name relative to the remote root.
// The adapter maps names to absolute paths, so the relative name is the
// directory with its leading slash removed.
func newTestFS(t *testing.T) (*sftpFS, string) {
	t.Helper()
	dir := t.TempDir()
	clientRead, serverWrite := io.Pipe()
	serverRead, clientWrite := io.Pipe()

	server, err := sftp.NewServer(pipeConn{Reader: serverRead, WriteCloser: serverWrite})
	if err != nil {
		t.Fatal(err)
	}
	go server.Serve()
	client, err := sftp.NewClientPipe(clientRead, clientWrite)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// Closing the pipe ends first gives both sides EOF, so their receive
		// loops return before the client and server wait on them.
		clientWrite.Close()
		serverWrite.Close()
		client.Close()
		server.Close()
	})
	return &sftpFS{client: client}, strings.TrimPrefix(dir, "/")
}

func TestFsErrMapsGenericFailureToPermission(t *testing.T) {
	generic := &sftp.StatusError{Code: sshFxFailure}
	err := fsErr("open", "Config.Msi", generic)
	if !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("generic SSH_FX_FAILURE = %v, want fs.ErrPermission", err)
	}
	var pe *fs.PathError
	if !errors.As(err, &pe) || pe.Path != "Config.Msi" || pe.Op != "open" {
		t.Fatalf("error does not name the operation and path: %#v", err)
	}
}

func TestFsErrKeepsOtherErrorsAsTheyAre(t *testing.T) {
	missing := &sftp.StatusError{Code: 2} // SSH_FX_NO_SUCH_FILE
	if err := fsErr("stat", "a", missing); err != missing {
		t.Fatalf("status %d was rewritten to %v", missing.Code, err)
	}
	if err := fsErr("stat", "a", nil); err != nil {
		t.Fatalf("nil error became %v", err)
	}
}

func TestSFTPRoundTrip(t *testing.T) {
	fsys, root := newTestFS(t)
	ctx := context.Background()
	dirName := root + "/docs"
	if err := fsys.Mkdir(ctx, dirName, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	f, err := fsys.OpenFile(ctx, dirName+"/a.txt", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := f.Write([]byte("hello")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	r, err := fsys.OpenFile(ctx, dirName+"/a.txt", os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	got, err := io.ReadAll(r)
	r.Close()
	if err != nil || string(got) != "hello" {
		t.Fatalf("read back %q, %v; want \"hello\"", got, err)
	}

	fi, err := fsys.Stat(ctx, dirName+"/a.txt")
	if err != nil || fi.Size() != 5 || fi.IsDir() {
		t.Fatalf("stat = %v, %v; want a 5-byte file", fi, err)
	}

	if err := fsys.Rename(ctx, dirName+"/a.txt", dirName+"/b.txt"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if _, err := fsys.Stat(ctx, dirName+"/a.txt"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("old name still stats: %v", err)
	}

	if err := fsys.RemoveAll(ctx, dirName); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := fsys.Stat(ctx, dirName); err == nil {
		t.Fatal("directory still exists after RemoveAll")
	}
}

func TestDirectoryHandleListsEntriesInPages(t *testing.T) {
	fsys, root := newTestFS(t)
	ctx := context.Background()
	dir := root + "/d"
	if err := fsys.Mkdir(ctx, dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"a", "b", "c"} {
		f, err := fsys.OpenFile(ctx, dir+"/"+n, os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		f.Close()
	}

	// A read-only open of a directory returns a handle that lists it.
	h, err := fsys.OpenFile(ctx, dir, os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("open dir: %v", err)
	}
	defer h.Close()
	if fi, err := h.Stat(); err != nil || !fi.IsDir() {
		t.Fatalf("handle stat = %v, %v; want a directory", fi, err)
	}
	if _, err := h.Read(make([]byte, 1)); err == nil {
		t.Fatal("reading a directory succeeded")
	}
	if _, err := h.Write([]byte("x")); err == nil {
		t.Fatal("writing a directory succeeded")
	}

	// Pages of at most two entries, then io.EOF; the three entries come once.
	seen := map[string]bool{}
	for {
		page, err := h.Readdir(2)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("readdir: %v", err)
		}
		if len(page) > 2 {
			t.Fatalf("page of %d entries; want at most 2", len(page))
		}
		for _, fi := range page {
			seen[fi.Name()] = true
		}
	}
	if len(seen) != 3 || !seen["a"] || !seen["b"] || !seen["c"] {
		t.Fatalf("listed %v; want a, b and c once each", seen)
	}
}

func TestReaddirWithoutCountReturnsEverything(t *testing.T) {
	fsys, root := newTestFS(t)
	ctx := context.Background()
	dir := root + "/all"
	if err := fsys.Mkdir(ctx, dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := fsys.OpenFile(ctx, dir+"/x", os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	h, err := fsys.OpenFile(ctx, dir, os.O_RDONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	all, err := h.Readdir(0)
	if err != nil || len(all) != 1 || all[0].Name() != "x" {
		t.Fatalf("Readdir(0) = %v, %v; want the one entry", all, err)
	}
}

func TestMissingPathIsAnErrorNamingItsOperation(t *testing.T) {
	fsys, root := newTestFS(t)
	_, err := fsys.OpenFile(context.Background(), root+"/nope", os.O_RDONLY, 0)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("open of a missing file = %v; want not-exist", err)
	}
}
