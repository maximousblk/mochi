package app

import (
	"embed"
	"fmt"

	"github.com/egoist/mygo/ui"
	"github.com/klauspost/compress/zstd"
)

// Icons and fonts are embedded here, because go:embed only reaches files at or
// below this package's directory.

//go:embed resources/tray.png
var trayIcon []byte

//go:embed resources/ssh.svg
var sshIconData []byte

var sshIcon = ui.MustParseSVG(sshIconData)

//go:embed resources/webdav.svg
var webdavIconData []byte

var webdavIcon = ui.MustParseSVG(webdavIconData)

//go:embed resources/rustdesk.svg
var rustdeskIconData []byte

var rustdeskIcon = ui.MustParseSVG(rustdeskIconData)

//go:embed resources/send.svg
var sendIconData []byte

var textSenderIcon = ui.MustParseSVG(sendIconData)

//go:embed resources/trash.svg
var trashIconData []byte

var trashIcon = ui.MustParseSVG(trashIconData)

//go:embed resources/settings.svg
var settingsIconData []byte

var settingsIcon = ui.MustParseSVG(settingsIconData)

// The window controls that Mochi draws itself on Windows and Linux.

//go:embed resources/minimize.svg
var minimizeIconData []byte

var minimizeIcon = ui.MustParseSVG(minimizeIconData)

//go:embed resources/maximize.svg
var maximizeIconData []byte

var maximizeIcon = ui.MustParseSVG(maximizeIconData)

//go:embed resources/restore.svg
var restoreIconData []byte

var restoreIcon = ui.MustParseSVG(restoreIconData)

//go:embed resources/close.svg
var closeIconData []byte

var closeIcon = ui.MustParseSVG(closeIconData)

//go:embed resources/clipboard.svg
var clipboardIconData []byte

var clipboardIcon = ui.MustParseSVG(clipboardIconData)

//go:embed resources/folder.svg
var folderIconData []byte

var folderIcon = ui.MustParseSVG(folderIconData)

// The Ioskeley Mono fonts are embedded zstd-compressed, which keeps every glyph,
// weight and hint while taking a quarter of the room in the executable. They
// decompress to the upstream files byte for byte, in tens of milliseconds at
// startup, with a decoder Tailcat already links in. Recreate them from the
// upstream release's Normal/Hinted TTFs with: zstd --ultra -22.
//
//go:embed resources/fonts/*.ttf.zst
var fontFiles embed.FS

// fontNames are the weights of Ioskeley Mono that Mochi ships.
var fontNames = []string{"Regular", "Medium", "SemiBold", "Bold"}

func registerFonts() error {
	dec, err := zstd.NewReader(nil, zstd.WithDecoderConcurrency(1))
	if err != nil {
		return err
	}
	defer dec.Close()
	for _, name := range fontNames {
		path := "resources/fonts/IoskeleyMono-" + name + ".ttf.zst"
		packed, err := fontFiles.ReadFile(path)
		if err != nil {
			return err
		}
		face, err := dec.DecodeAll(packed, nil)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if err := ui.RegisterFont(face, "Ioskeley Mono"); err != nil {
			return err
		}
	}
	return nil
}
