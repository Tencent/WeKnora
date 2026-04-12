; WeKnora Lite Windows installer script (NSIS)
; Called from CI with -DVERSION=xxx -DINST_DIR=yyy -DOUTFILE=zzz

!include "MUI2.nsh"

Name "WeKnora Lite ${VERSION}"
OutFile "${OUTFILE}"
InstallDir "$PROGRAMFILES64\WeKnora Lite"
InstallDirRegKey HKLM "Software\WeKnora Lite" "Install_Dir"
RequestExecutionLevel admin
SetCompressor /SOLID lzma
Unicode true

; ── MUI pages ──
!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH

!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES

!insertmacro MUI_LANGUAGE "English"
!insertmacro MUI_LANGUAGE "SimpChinese"

; ── Install section ──
Section "Install"
  SetOutPath $INSTDIR

  ; Main executable
  File "${INST_DIR}\WeKnora Lite.exe"

  ; .env config
  File "${INST_DIR}\.env"

  ; Sub-directories
  SetOutPath $INSTDIR\config
  File /r "${INST_DIR}\config\*.*"

  SetOutPath $INSTDIR\migrations\sqlite
  File /r "${INST_DIR}\migrations\sqlite\*.*"

  SetOutPath $INSTDIR\web
  File /r "${INST_DIR}\web\*.*"

  SetOutPath $INSTDIR\jieba_dict
  File /r "${INST_DIR}\jieba_dict\*.*"

  ; Reset output path and write uninstaller
  SetOutPath $INSTDIR
  WriteUninstaller "$INSTDIR\Uninstall.exe"

  ; Registry entries
  WriteRegStr HKLM "Software\WeKnora Lite" "Install_Dir" "$INSTDIR"
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\WeKnora Lite" \
    "DisplayName" "WeKnora Lite"
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\WeKnora Lite" \
    "UninstallString" '"$INSTDIR\Uninstall.exe"'
  WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\WeKnora Lite" \
    "DisplayVersion" "${VERSION}"
  WriteRegDWORD HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\WeKnora Lite" \
    "NoModify" 1
  WriteRegDWORD HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\WeKnora Lite" \
    "NoRepair" 1

  ; Start Menu shortcuts
  CreateDirectory "$SMPROGRAMS\WeKnora Lite"
  CreateShortCut "$SMPROGRAMS\WeKnora Lite\WeKnora Lite.lnk" "$INSTDIR\WeKnora Lite.exe"
  CreateShortCut "$SMPROGRAMS\WeKnora Lite\Uninstall.lnk" "$INSTDIR\Uninstall.exe"

  ; Desktop shortcut
  CreateShortCut "$DESKTOP\WeKnora Lite.lnk" "$INSTDIR\WeKnora Lite.exe"
SectionEnd

; ── Uninstall section ──
Section "Uninstall"
  ; Remove files
  Delete "$INSTDIR\WeKnora Lite.exe"
  Delete "$INSTDIR\.env"
  Delete "$INSTDIR\Uninstall.exe"

  ; Remove directories
  RMDir /r "$INSTDIR\config"
  RMDir /r "$INSTDIR\migrations"
  RMDir /r "$INSTDIR\web"
  RMDir /r "$INSTDIR\jieba_dict"
  RMDir "$INSTDIR"

  ; Remove shortcuts
  Delete "$SMPROGRAMS\WeKnora Lite\WeKnora Lite.lnk"
  Delete "$SMPROGRAMS\WeKnora Lite\Uninstall.lnk"
  RMDir "$SMPROGRAMS\WeKnora Lite"
  Delete "$DESKTOP\WeKnora Lite.lnk"

  ; Remove registry
  DeleteRegKey HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\WeKnora Lite"
  DeleteRegKey HKLM "Software\WeKnora Lite"
SectionEnd
