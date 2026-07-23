#ifndef AppVersion
  #error AppVersion must be defined
#endif
#ifndef AppVersionNumeric
  #error AppVersionNumeric must be defined
#endif
#ifndef AppArch
  #error AppArch must be defined
#endif
#ifndef SourceDir
  #error SourceDir must be defined
#endif
#ifndef OutputDir
  #error OutputDir must be defined
#endif
#ifndef AppIdSuffix
  #define AppIdSuffix ""
#endif

#define AppName "codex-swarm"
#define AppPublisher "MTG-Thomas"
#define AppURL "https://github.com/MTG-Thomas/codex-swarm"
#define AppIdValue "MTG-Thomas.codex-swarm" + AppIdSuffix
#define SettingsKey "Software\MTG-Thomas\codex-swarm" + AppIdSuffix

#if AppArch == "amd64"
  #define AllowedArchitectures "x64compatible and not arm64"
  #define InstallArchitectures "x64compatible"
#elif AppArch == "arm64"
  #define AllowedArchitectures "arm64"
  #define InstallArchitectures "arm64"
#else
  #error AppArch must be amd64 or arm64
#endif

[Setup]
AppId={#AppIdValue}
AppName={#AppName}
AppVersion={#AppVersion}
AppPublisher={#AppPublisher}
AppPublisherURL={#AppURL}
AppSupportURL={#AppURL}/issues
AppUpdatesURL={#AppURL}/releases
DefaultDirName={localappdata}\Programs\codex-swarm
DefaultGroupName=codex-swarm
DisableProgramGroupPage=yes
PrivilegesRequired=lowest
ArchitecturesAllowed={#AllowedArchitectures}
ArchitecturesInstallIn64BitMode={#InstallArchitectures}
MinVersion=10.0.17763
OutputDir={#OutputDir}
OutputBaseFilename=codex-swarm-v{#AppVersion}-windows-{#AppArch}-setup
Compression=lzma2/max
SolidCompression=yes
WizardStyle=modern
SetupLogging=yes
CloseApplications=no
RestartApplications=no
ChangesEnvironment=yes
UsePreviousAppDir=yes
UsePreviousTasks=yes
UninstallDisplayIcon={app}\cs.exe
VersionInfoVersion={#AppVersionNumeric}
VersionInfoCompany={#AppPublisher}
VersionInfoDescription=codex-swarm installer
VersionInfoProductName={#AppName}
VersionInfoProductVersion={#AppVersionNumeric}

[Tasks]
Name: addtopath; Description: "Add codex-swarm to my PATH"; GroupDescription: "Command line integration:"; Flags: checkedonce

[Files]
Source: "{#SourceDir}\cs.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#SourceDir}\csd.exe"; DestDir: "{app}"; Flags: ignoreversion

[Code]
const
  EnvironmentKey = 'Environment';
  PathValueName = 'Path';
  ManagedPathValueName = 'PathManaged';
  SettingsKeyName = '{#SettingsKey}';

function NormalizePathEntry(Value: string): string;
begin
  Result := Trim(Value);
  if (Length(Result) >= 2) and (Result[1] = '"') and
     (Result[Length(Result)] = '"') then
  begin
    Delete(Result, Length(Result), 1);
    Delete(Result, 1, 1);
  end;
  while (Length(Result) > 3) and
        ((Result[Length(Result)] = '\') or (Result[Length(Result)] = '/')) do
    Delete(Result, Length(Result), 1);
end;

procedure EnumeratePathEntries(
  PathValue: string; var Entries: TArrayOfString);
var
  DelimiterPos: Integer;
  Entry: string;
begin
  SetArrayLength(Entries, 0);
  PathValue := PathValue + ';';
  while Length(PathValue) > 0 do
  begin
    DelimiterPos := Pos(';', PathValue);
    Entry := Copy(PathValue, 1, DelimiterPos - 1);
    Delete(PathValue, 1, DelimiterPos);
    SetArrayLength(Entries, GetArrayLength(Entries) + 1);
    Entries[GetArrayLength(Entries) - 1] := Entry;
  end;
end;

function PathContainsExact(PathValue, Target: string): Boolean;
var
  Entries: TArrayOfString;
  Index: Integer;
begin
  Result := False;
  Target := NormalizePathEntry(Target);
  EnumeratePathEntries(PathValue, Entries);
  for Index := 0 to GetArrayLength(Entries) - 1 do
  begin
    if CompareText(NormalizePathEntry(Entries[Index]), Target) = 0 then
    begin
      Result := True;
      Exit;
    end;
  end;
end;

function RemoveExactPath(PathValue, Target: string): string;
var
  Entries: TArrayOfString;
  Index: Integer;
begin
  Result := '';
  Target := NormalizePathEntry(Target);
  EnumeratePathEntries(PathValue, Entries);
  for Index := 0 to GetArrayLength(Entries) - 1 do
  begin
    if (Entries[Index] <> '') and
       (CompareText(NormalizePathEntry(Entries[Index]), Target) <> 0) then
    begin
      if Result <> '' then
        Result := Result + ';';
      Result := Result + Entries[Index];
    end;
  end;
end;

procedure WriteUserPath(Value: string);
begin
  if Value = '' then
    RegDeleteValue(HKEY_CURRENT_USER, EnvironmentKey, PathValueName)
  else if not RegWriteExpandStringValue(
    HKEY_CURRENT_USER, EnvironmentKey, PathValueName, Value) then
    RaiseException('Unable to update the current user PATH.');
end;

procedure ConfigurePath;
var
  CurrentPath: string;
  InstallPath: string;
  ManagedPath: Cardinal;
  HasManagedPath: Boolean;
begin
  InstallPath := ExpandConstant('{app}');
  if not RegQueryStringValue(
    HKEY_CURRENT_USER, EnvironmentKey, PathValueName, CurrentPath) then
    CurrentPath := '';
  HasManagedPath := RegQueryDWordValue(
    HKEY_CURRENT_USER, SettingsKeyName, ManagedPathValueName, ManagedPath);

  if WizardIsTaskSelected('addtopath') then
  begin
    if not PathContainsExact(CurrentPath, InstallPath) then
    begin
      if (CurrentPath <> '') and (CurrentPath[Length(CurrentPath)] <> ';') then
        CurrentPath := CurrentPath + ';';
      WriteUserPath(CurrentPath + InstallPath);
      RegWriteDWordValue(
        HKEY_CURRENT_USER, SettingsKeyName, ManagedPathValueName, 1);
    end
    else if not HasManagedPath then
      RegWriteDWordValue(
        HKEY_CURRENT_USER, SettingsKeyName, ManagedPathValueName, 0);
  end
  else
  begin
    if HasManagedPath and (ManagedPath = 1) then
      WriteUserPath(RemoveExactPath(CurrentPath, InstallPath));
    RegDeleteValue(
      HKEY_CURRENT_USER, SettingsKeyName, ManagedPathValueName);
  end;
end;

procedure RemoveManagedPath;
var
  CurrentPath: string;
  InstallPath: string;
  ManagedPath: Cardinal;
begin
  InstallPath := ExpandConstant('{app}');
  if RegQueryDWordValue(
    HKEY_CURRENT_USER, SettingsKeyName, ManagedPathValueName, ManagedPath) and
     (ManagedPath = 1) and
     RegQueryStringValue(
       HKEY_CURRENT_USER, EnvironmentKey, PathValueName, CurrentPath) then
    WriteUserPath(RemoveExactPath(CurrentPath, InstallPath));
  RegDeleteKeyIncludingSubkeys(HKEY_CURRENT_USER, SettingsKeyName);
end;

procedure CurStepChanged(CurStep: TSetupStep);
begin
  if CurStep = ssPostInstall then
    ConfigurePath;
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
begin
  if CurUninstallStep = usUninstall then
    RemoveManagedPath;
end;
