; Mochi's Windows installer. It has one window: the install progress. It starts
; as soon as it opens and closes itself when the app is installed. There are no
; Next, directory or finish pages.
;
; The app goes to the current user's %LOCALAPPDATA%\Programs\Mochi, which needs
; no administrator rights, and the app can update itself there. A running
; Mochi is stopped first, and started again once the install is done.
;
; Built by packaging/windows/build.sh, which passes the version, the app
; executable and the output path as defines.

Unicode true
ManifestDPIAware true
SetCompressor /SOLID lzma
RequestExecutionLevel user
BrandingText " "

!ifndef APP_VERSION
  !error "APP_VERSION is not defined; build with packaging/windows/build.sh"
!endif
!ifndef APP_EXE
  !error "APP_EXE is not defined; build with packaging/windows/build.sh"
!endif
!ifndef OUT_FILE
  !error "OUT_FILE is not defined; build with packaging/windows/build.sh"
!endif

!define APP_NAME "Mochi"
!define APP_ID "dev.mochi.tailcat"
!define UNINSTALL_KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\${APP_ID}"

Name "${APP_NAME}"
OutFile "${OUT_FILE}"
InstallDir "$LOCALAPPDATA\Programs\${APP_NAME}"
ShowInstDetails nevershow
ShowUninstDetails nevershow
AutoCloseWindow true

!include "MUI2.nsh"
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "English"

Section "Install"
  ; Mochi's window hides on close, so a normal close leaves it running and
  ; locking its executable. Its state is in SQLite (WAL), which a forced stop
  ; leaves intact. /T also stops the processes it started.
  nsExec::Exec 'taskkill /F /T /IM "${APP_NAME}.exe"'
  Pop $0
  Sleep 1000

  SetOutPath "$INSTDIR"
  File "/oname=${APP_NAME}.exe" "${APP_EXE}"
  WriteUninstaller "$INSTDIR\Uninstall.exe"

  CreateDirectory "$SMPROGRAMS\${APP_NAME}"
  CreateShortCut "$SMPROGRAMS\${APP_NAME}\${APP_NAME}.lnk" "$INSTDIR\${APP_NAME}.exe"

  ; Listed in Windows Settings, uninstalled per user.
  WriteRegStr HKCU "${UNINSTALL_KEY}" "DisplayName" "${APP_NAME}"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "DisplayVersion" "${APP_VERSION}"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "DisplayIcon" "$INSTDIR\${APP_NAME}.exe"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "InstallLocation" "$INSTDIR"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "UninstallString" '"$INSTDIR\Uninstall.exe"'
  WriteRegStr HKCU "${UNINSTALL_KEY}" "QuietUninstallString" '"$INSTDIR\Uninstall.exe" /S'
  WriteRegDWORD HKCU "${UNINSTALL_KEY}" "NoModify" 1
  WriteRegDWORD HKCU "${UNINSTALL_KEY}" "NoRepair" 1

  ; Start the app now, as the user who ran the installer.
  Exec '"$INSTDIR\${APP_NAME}.exe"'
SectionEnd

Section "Uninstall"
  nsExec::Exec 'taskkill /F /T /IM "${APP_NAME}.exe"'
  Pop $0
  Sleep 1000

  Delete "$SMPROGRAMS\${APP_NAME}\${APP_NAME}.lnk"
  RMDir "$SMPROGRAMS\${APP_NAME}"
  DeleteRegKey HKCU "${UNINSTALL_KEY}"
  ; Last, so that the folder is gone once the uninstall is done.
  RMDir /r "$INSTDIR"
SectionEnd
