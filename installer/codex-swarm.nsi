!ifndef APP_VERSION
  !error "APP_VERSION must be defined"
!endif
!ifndef APP_VERSION_NUMERIC
  !error "APP_VERSION_NUMERIC must be defined"
!endif
!ifndef APP_ARCH
  !error "APP_ARCH must be defined"
!endif
!ifndef SOURCE_DIR
  !error "SOURCE_DIR must be defined"
!endif
!ifndef OUTPUT_DIR
  !error "OUTPUT_DIR must be defined"
!endif
!ifndef APP_ID_SUFFIX
  !define APP_ID_SUFFIX ""
!endif

!define APP_NAME "codex-swarm"
!define APP_PUBLISHER "MTG-Thomas"
!define APP_URL "https://github.com/MTG-Thomas/codex-swarm"
!define APP_ID "MTG-Thomas.codex-swarm${APP_ID_SUFFIX}"
!define SETTINGS_KEY "Software\MTG-Thomas\codex-swarm${APP_ID_SUFFIX}"
!define UNINSTALL_KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\${APP_ID}"

Unicode True
RequestExecutionLevel user
SetCompressor /SOLID lzma
SetCompressorDictSize 32

Name "${APP_NAME}"
Caption "${APP_NAME} ${APP_VERSION}"
OutFile "${OUTPUT_DIR}\codex-swarm-v${APP_VERSION}-windows-${APP_ARCH}-setup.exe"
InstallDir "$LOCALAPPDATA\Programs\codex-swarm"
InstallDirRegKey HKCU "${SETTINGS_KEY}" "InstallLocation"
BrandingText "${APP_NAME}"
ShowInstDetails show
ShowUninstDetails show

VIProductVersion "${APP_VERSION_NUMERIC}"
VIAddVersionKey /LANG=1033 "CompanyName" "${APP_PUBLISHER}"
VIAddVersionKey /LANG=1033 "FileDescription" "${APP_NAME} installer"
VIAddVersionKey /LANG=1033 "FileVersion" "${APP_VERSION}"
VIAddVersionKey /LANG=1033 "LegalCopyright" "${APP_PUBLISHER}"
VIAddVersionKey /LANG=1033 "ProductName" "${APP_NAME}"
VIAddVersionKey /LANG=1033 "ProductVersion" "${APP_VERSION}"

!include "LogicLib.nsh"
!include "MUI2.nsh"
!include "Sections.nsh"
!include "WinMessages.nsh"
!include "WinVer.nsh"
!include "x64.nsh"

!define MUI_ABORTWARNING
!define MUI_UNABORTWARNING
!define MUI_COMPONENTSPAGE_SMALLDESC

!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_COMPONENTS
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "English"

Section "!${APP_NAME} command-line tools" SEC_CORE
  SectionIn RO
  SetOutPath "$INSTDIR"
  File "/oname=cs.exe" "${SOURCE_DIR}\cs.exe"
  File "/oname=csd.exe" "${SOURCE_DIR}\csd.exe"
  WriteUninstaller "$INSTDIR\Uninstall.exe"

  WriteRegStr HKCU "${SETTINGS_KEY}" "InstallLocation" "$INSTDIR"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "DisplayName" "${APP_NAME}"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "DisplayVersion" "${APP_VERSION}"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "Publisher" "${APP_PUBLISHER}"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "URLInfoAbout" "${APP_URL}"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "HelpLink" "${APP_URL}/issues"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "InstallLocation" "$INSTDIR"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "DisplayIcon" "$INSTDIR\cs.exe"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "UninstallString" '"$INSTDIR\Uninstall.exe"'
  WriteRegStr HKCU "${UNINSTALL_KEY}" "QuietUninstallString" '"$INSTDIR\Uninstall.exe" /S'
  WriteRegDWORD HKCU "${UNINSTALL_KEY}" "NoModify" 1
  WriteRegDWORD HKCU "${UNINSTALL_KEY}" "NoRepair" 1
SectionEnd

Section "Add ${APP_NAME} to my PATH" SEC_PATH
SectionEnd

Section "-Configure PATH"
  Call ConfigurePath
SectionEnd

LangString DESC_SEC_CORE ${LANG_ENGLISH} "Install cs.exe and csd.exe for the current user."
LangString DESC_SEC_PATH ${LANG_ENGLISH} "Add the installation directory to the current user's PATH."
!insertmacro MUI_FUNCTION_DESCRIPTION_BEGIN
  !insertmacro MUI_DESCRIPTION_TEXT ${SEC_CORE} $(DESC_SEC_CORE)
  !insertmacro MUI_DESCRIPTION_TEXT ${SEC_PATH} $(DESC_SEC_PATH)
!insertmacro MUI_FUNCTION_DESCRIPTION_END

Section "Uninstall"
  ClearErrors
  ReadRegDWORD $0 HKCU "${SETTINGS_KEY}" "PathManaged"
  ${IfNot} ${Errors}
  ${AndIf} $0 = 1
    nsExec::ExecToStack '"$INSTDIR\cs.exe" __installer-path remove'
    Pop $0
    Pop $1
    ${If} $0 != 0
      MessageBox MB_ICONSTOP|MB_OK "Unable to remove ${APP_NAME} from the current user's PATH: $1"
      Abort
    ${EndIf}
    ${If} "$1" != "removed"
    ${AndIf} "$1" != "absent"
      MessageBox MB_ICONSTOP|MB_OK "Unexpected PATH helper result while uninstalling ${APP_NAME}: $1"
      Abort
    ${EndIf}
    SendMessage ${HWND_BROADCAST} ${WM_SETTINGCHANGE} 0 "STR:Environment" /TIMEOUT=5000
  ${EndIf}

  DeleteRegKey HKCU "${UNINSTALL_KEY}"
  DeleteRegKey HKCU "${SETTINGS_KEY}"
  Delete "$INSTDIR\cs.exe"
  Delete "$INSTDIR\csd.exe"
  Delete "$INSTDIR\Uninstall.exe"
  RMDir "$INSTDIR"
SectionEnd

Function .onInit
  SetRegView 64
  ${Unless} ${AtLeastWin10}
    MessageBox MB_ICONSTOP|MB_OK "${APP_NAME} requires Windows 10 or newer."
    Abort
  ${EndUnless}

  !if "${APP_ARCH}" == "amd64"
    ${IfNot} ${RunningX64}
    ${OrIf} ${IsNativeARM64}
      MessageBox MB_ICONSTOP|MB_OK "This installer requires an amd64 Windows system. Download the installer for your architecture."
      Abort
    ${EndIf}
  !else if "${APP_ARCH}" == "arm64"
    ${IfNot} ${IsNativeARM64}
      MessageBox MB_ICONSTOP|MB_OK "This installer requires an arm64 Windows system. Download the installer for your architecture."
      Abort
    ${EndIf}
  !else
    !error "APP_ARCH must be amd64 or arm64"
  !endif
FunctionEnd

Function un.onInit
  SetRegView 64
FunctionEnd

Function ConfigurePath
  ClearErrors
  ReadRegDWORD $1 HKCU "${SETTINGS_KEY}" "PathManaged"
  ${If} ${Errors}
    StrCpy $2 0
  ${Else}
    StrCpy $2 1
  ${EndIf}

  SectionGetFlags ${SEC_PATH} $3
  IntOp $3 $3 & ${SF_SELECTED}
  ${If} $3 <> 0
    nsExec::ExecToStack '"$INSTDIR\cs.exe" __installer-path add'
    Pop $0
    Pop $3
    ${If} $0 != 0
      MessageBox MB_ICONSTOP|MB_OK "Unable to add ${APP_NAME} to the current user's PATH: $3"
      Abort
    ${EndIf}
    ${If} "$3" == "added"
      WriteRegDWORD HKCU "${SETTINGS_KEY}" "PathManaged" 1
    ${ElseIf} "$3" == "present"
      ${If} $2 = 0
        WriteRegDWORD HKCU "${SETTINGS_KEY}" "PathManaged" 0
      ${EndIf}
    ${Else}
      MessageBox MB_ICONSTOP|MB_OK "Unexpected PATH helper result while installing ${APP_NAME}: $3"
      Abort
    ${EndIf}
    SendMessage ${HWND_BROADCAST} ${WM_SETTINGCHANGE} 0 "STR:Environment" /TIMEOUT=5000
  ${Else}
    ${If} $2 = 1
    ${AndIf} $1 = 1
      nsExec::ExecToStack '"$INSTDIR\cs.exe" __installer-path remove'
      Pop $0
      Pop $3
      ${If} $0 != 0
        MessageBox MB_ICONSTOP|MB_OK "Unable to remove ${APP_NAME} from the current user's PATH: $3"
        Abort
      ${EndIf}
      ${If} "$3" != "removed"
      ${AndIf} "$3" != "absent"
        MessageBox MB_ICONSTOP|MB_OK "Unexpected PATH helper result while configuring ${APP_NAME}: $3"
        Abort
      ${EndIf}
      SendMessage ${HWND_BROADCAST} ${WM_SETTINGCHANGE} 0 "STR:Environment" /TIMEOUT=5000
    ${EndIf}
    DeleteRegValue HKCU "${SETTINGS_KEY}" "PathManaged"
  ${EndIf}
FunctionEnd
