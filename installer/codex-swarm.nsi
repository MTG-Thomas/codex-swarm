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
!include "StrFunc.nsh"
!include "WinMessages.nsh"
!include "WinVer.nsh"
!include "x64.nsh"

${Using:StrFunc} StrTok
${Using:StrFunc} UnStrTok

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
    ReadRegStr $1 HKCU "Environment" "Path"
    Push "$1"
    Push "$INSTDIR"
    Call un.RemoveExactPath
    Pop $1
    Call un.WriteUserPath
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
  ReadRegStr $0 HKCU "Environment" "Path"
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
    Push "$0"
    Push "$INSTDIR"
    Call PathContainsExact
    Pop $3
    ${If} $3 = 0
      StrLen $3 "$0"
      ${If} $3 > 0
        StrCpy $3 "$0" 1 -1
        ${If} $3 != ";"
          StrCpy $0 "$0;"
        ${EndIf}
      ${EndIf}
      StrCpy $0 "$0$INSTDIR"
      Call WriteUserPath
      WriteRegDWORD HKCU "${SETTINGS_KEY}" "PathManaged" 1
    ${ElseIf} $2 = 0
      WriteRegDWORD HKCU "${SETTINGS_KEY}" "PathManaged" 0
    ${EndIf}
  ${Else}
    ${If} $2 = 1
    ${AndIf} $1 = 1
      Push "$0"
      Push "$INSTDIR"
      Call RemoveExactPath
      Pop $0
      Call WriteUserPath
    ${EndIf}
    DeleteRegValue HKCU "${SETTINGS_KEY}" "PathManaged"
  ${EndIf}
FunctionEnd

Function WriteUserPath
  ClearErrors
  ${If} "$0" == ""
    DeleteRegValue HKCU "Environment" "Path"
  ${Else}
    WriteRegExpandStr HKCU "Environment" "Path" "$0"
  ${EndIf}
  ${If} ${Errors}
    MessageBox MB_ICONSTOP|MB_OK "Unable to update the current user's PATH."
    Abort
  ${EndIf}
  SendMessage ${HWND_BROADCAST} ${WM_SETTINGCHANGE} 0 "STR:Environment" /TIMEOUT=5000
FunctionEnd

Function un.WriteUserPath
  ClearErrors
  ${If} "$1" == ""
    DeleteRegValue HKCU "Environment" "Path"
  ${Else}
    WriteRegExpandStr HKCU "Environment" "Path" "$1"
  ${EndIf}
  ${If} ${Errors}
    MessageBox MB_ICONSTOP|MB_OK "Unable to update the current user's PATH."
    Abort
  ${EndIf}
  SendMessage ${HWND_BROADCAST} ${WM_SETTINGCHANGE} 0 "STR:Environment" /TIMEOUT=5000
FunctionEnd

!macro DefineNormalizePathEntry Prefix
Function ${Prefix}NormalizePathEntry
  Exch $0
  Push $1
  Push $2

  ${Do}
    StrCpy $1 "$0" 1
    ${If} "$1" == " "
    ${OrIf} "$1" == "$\t"
      StrCpy $0 "$0" "" 1
    ${Else}
      ${ExitDo}
    ${EndIf}
  ${Loop}

  ${Do}
    StrLen $2 "$0"
    ${If} $2 = 0
      ${ExitDo}
    ${EndIf}
    IntOp $2 $2 - 1
    StrCpy $1 "$0" 1 $2
    ${If} "$1" == " "
    ${OrIf} "$1" == "$\t"
      StrCpy $0 "$0" $2
    ${Else}
      ${ExitDo}
    ${EndIf}
  ${Loop}

  StrLen $2 "$0"
  ${If} $2 >= 2
    StrCpy $1 "$0" 1
    ${If} "$1" == '"'
      StrCpy $1 "$0" 1 -1
      ${If} "$1" == '"'
        IntOp $2 $2 - 2
        StrCpy $0 "$0" $2 1
      ${EndIf}
    ${EndIf}
  ${EndIf}

  ${Do}
    StrLen $2 "$0"
    ${If} $2 <= 3
      ${ExitDo}
    ${EndIf}
    StrCpy $1 "$0" 1 -1
    ${If} "$1" == "\"
    ${OrIf} "$1" == "/"
      IntOp $2 $2 - 1
      StrCpy $0 "$0" $2
    ${Else}
      ${ExitDo}
    ${EndIf}
  ${Loop}

  Pop $2
  Pop $1
  Exch $0
FunctionEnd
!macroend

!insertmacro DefineNormalizePathEntry ""
!insertmacro DefineNormalizePathEntry "un."

!macro DefinePathContainsExact Prefix TokenFunction
Function ${Prefix}PathContainsExact
  Exch $0
  Exch
  Exch $1
  Push $2
  Push $3
  Push $4

  Push "$0"
  Call ${Prefix}NormalizePathEntry
  Pop $0
  StrCpy $2 0
  StrCpy $4 0

  ${Do}
    ${${TokenFunction}} $3 "$1" ";" "$2" "1"
    ${If} "$3" == ""
      ${ExitDo}
    ${EndIf}
    Push "$3"
    Call ${Prefix}NormalizePathEntry
    Pop $3
    ${If} "$3" == "$0"
      StrCpy $4 1
      ${ExitDo}
    ${EndIf}
    IntOp $2 $2 + 1
  ${Loop}

  StrCpy $0 "$4"
  Pop $4
  Pop $3
  Pop $2
  Pop $1
  Exch $0
FunctionEnd
!macroend

!insertmacro DefinePathContainsExact "" StrTok

!macro DefineRemoveExactPath Prefix TokenFunction
Function ${Prefix}RemoveExactPath
  Exch $0
  Exch
  Exch $1
  Push $2
  Push $3
  Push $4
  Push $5

  Push "$0"
  Call ${Prefix}NormalizePathEntry
  Pop $0
  StrCpy $2 0
  StrCpy $5 ""

  ${Do}
    ${${TokenFunction}} $3 "$1" ";" "$2" "1"
    ${If} "$3" == ""
      ${ExitDo}
    ${EndIf}
    StrCpy $4 "$3"
    Push "$4"
    Call ${Prefix}NormalizePathEntry
    Pop $4
    ${If} "$4" != "$0"
      ${If} "$5" != ""
        StrCpy $5 "$5;"
      ${EndIf}
      StrCpy $5 "$5$3"
    ${EndIf}
    IntOp $2 $2 + 1
  ${Loop}

  StrCpy $0 "$5"
  Pop $5
  Pop $4
  Pop $3
  Pop $2
  Pop $1
  Exch $0
FunctionEnd
!macroend

!insertmacro DefineRemoveExactPath "" StrTok
!insertmacro DefineRemoveExactPath "un." UnStrTok
