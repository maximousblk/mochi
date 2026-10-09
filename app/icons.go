package app

import (
	_ "embed"

	"github.com/egoist/mygo/ui"
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

//go:embed resources/clipboard.svg
var clipboardIconData []byte

var clipboardIcon = ui.MustParseSVG(clipboardIconData)

//go:embed resources/folder.svg
var folderIconData []byte

var folderIcon = ui.MustParseSVG(folderIconData)

//go:embed resources/fonts/IoskeleyMono-Regular.ttf
var ioskeleyRegular []byte

//go:embed resources/fonts/IoskeleyMono-Medium.ttf
var ioskeleyMedium []byte

//go:embed resources/fonts/IoskeleyMono-SemiBold.ttf
var ioskeleySemiBold []byte

//go:embed resources/fonts/IoskeleyMono-Bold.ttf
var ioskeleyBold []byte

func registerFonts() error {
	for _, face := range [][]byte{ioskeleyRegular, ioskeleyMedium, ioskeleySemiBold, ioskeleyBold} {
		if err := ui.RegisterFont(face, "Ioskeley Mono"); err != nil {
			return err
		}
	}
	return nil
}
