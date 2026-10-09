package main

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"

	"github.com/pkg/sftp"
	"golang.org/x/net/webdav"
)

var errIsDirectory = errors.New("is a directory")

// sftpFS serves a remote tailcat file service as a webdav.FileSystem. WebDAV
// paths are slash-separated and rooted at the served directory.
type sftpFS struct {
	client *sftp.Client
}

func remotePath(name string) string { return path.Clean("/" + name) }

// sshFxFailure is the generic SFTP status code (SSH_FX_FAILURE).
const sshFxFailure = 4

// fsErr maps a generic SFTP failure to a permission error. Windows OpenSSH
// reports access denied, sharing violations (a file another process holds
// open), and similar per-file problems only as SSH_FX_FAILURE, which the sftp
// library does not map to fs.ErrPermission. webdav skips unreadable children
// only for permission errors, so without this one locked system file, such as
// Config.Msi or DumpStack.log.tmp, fails the whole folder listing with 500.
func fsErr(op, name string, err error) error {
	var se *sftp.StatusError
	if errors.As(err, &se) && se.Code == sshFxFailure {
		return &fs.PathError{Op: op, Path: name, Err: fs.ErrPermission}
	}
	return err
}

func (s *sftpFS) Mkdir(_ context.Context, name string, _ os.FileMode) error {
	return s.client.Mkdir(remotePath(name))
}

func (s *sftpFS) OpenFile(_ context.Context, name string, flag int, _ os.FileMode) (webdav.File, error) {
	p := remotePath(name)
	if flag&(os.O_WRONLY|os.O_RDWR|os.O_CREATE|os.O_TRUNC) == 0 {
		fi, err := s.client.Stat(p)
		if err != nil {
			return nil, fsErr("open", name, err)
		}
		if fi.IsDir() {
			// Directories are listed by name, so they need no open handle.
			return &sftpFile{client: s.client, name: p, dirInfo: fi}, nil
		}
	}
	f, err := s.client.OpenFile(p, flag)
	if err != nil {
		return nil, fsErr("open", name, err)
	}
	return &sftpFile{client: s.client, name: p, file: f}, nil
}

func (s *sftpFS) RemoveAll(_ context.Context, name string) error {
	return s.client.RemoveAll(remotePath(name))
}

func (s *sftpFS) Rename(_ context.Context, oldName, newName string) error {
	return s.client.Rename(remotePath(oldName), remotePath(newName))
}

func (s *sftpFS) Stat(_ context.Context, name string) (os.FileInfo, error) {
	fi, err := s.client.Stat(remotePath(name))
	return fi, fsErr("stat", name, err)
}

// sftpFile is an open file or directory on the remote. Exactly one of file
// and dirInfo is set: file for regular files, dirInfo for directories.
type sftpFile struct {
	client  *sftp.Client
	name    string
	file    *sftp.File
	dirInfo fs.FileInfo
	listing []fs.FileInfo
	listed  bool
}

func (f *sftpFile) Stat() (fs.FileInfo, error) {
	if f.file == nil {
		return f.dirInfo, nil
	}
	return f.file.Stat()
}

func (f *sftpFile) Read(p []byte) (int, error) {
	if f.file == nil {
		return 0, errIsDirectory
	}
	return f.file.Read(p)
}

func (f *sftpFile) Write(p []byte) (int, error) {
	if f.file == nil {
		return 0, errIsDirectory
	}
	return f.file.Write(p)
}

func (f *sftpFile) Seek(offset int64, whence int) (int64, error) {
	if f.file == nil {
		return 0, errIsDirectory
	}
	return f.file.Seek(offset, whence)
}

// Readdir follows os.File.Readdir: count > 0 returns at most count entries
// and io.EOF when none remain; count <= 0 returns all remaining entries.
func (f *sftpFile) Readdir(count int) ([]fs.FileInfo, error) {
	if f.file != nil || f.dirInfo == nil || !f.dirInfo.IsDir() {
		return nil, errors.New("not a directory")
	}
	if !f.listed {
		list, err := f.client.ReadDir(f.name)
		if err != nil {
			return nil, fsErr("readdir", f.name, err)
		}
		f.listing, f.listed = list, true
	}
	if count <= 0 {
		out := f.listing
		f.listing = nil
		return out, nil
	}
	if len(f.listing) == 0 {
		return nil, io.EOF
	}
	n := min(count, len(f.listing))
	out := f.listing[:n]
	f.listing = f.listing[n:]
	return out, nil
}

func (f *sftpFile) Close() error {
	if f.file == nil {
		return nil
	}
	return f.file.Close()
}
