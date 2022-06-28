#include "environment.iss"

#define MyAppName "trzsz"
#define MyAppVersion GetEnv("TRZSZ_VERSION")
#define MyAppPublisher "Lonny Wong"
#define MyAppURL "https://trzsz.github.io/"

[Setup]
AppId={#MyAppName}-7c99-4ee4-b3fa-0e763c55ea33
AppName={#MyAppName}
AppVersion={#MyAppVersion}
AppVerName={#MyAppName} {#MyAppVersion}
AppPublisher={#MyAppPublisher}
AppPublisherURL={#MyAppURL}
AppSupportURL={#MyAppURL}
AppUpdatesURL={#MyAppURL}
DefaultDirName={autopf}\{#MyAppName}
DisableDirPage=yes
DefaultGroupName={#MyAppName}
DisableProgramGroupPage=yes
OutputBaseFilename=trzsz_{#MyAppVersion}_windows_setup_aarch64
Compression=lzma
SolidCompression=yes
WizardStyle=modern
ChangesEnvironment=true
ArchitecturesAllowed=arm64
ArchitecturesInstallIn64BitMode=arm64

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

[Files]
Source: "{#MyAppName}_{#MyAppVersion}_windows_aarch64/trz.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#MyAppName}_{#MyAppVersion}_windows_aarch64/tsz.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#MyAppName}_{#MyAppVersion}_windows_aarch64/trzsz.exe"; DestDir: "{app}"; Flags: ignoreversion

[Code]
procedure CurStepChanged(CurStep: TSetupStep);
begin
    if CurStep = ssPostInstall
     then EnvAddPath(ExpandConstant('{app}'));
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
begin
    if CurUninstallStep = usPostUninstall
    then EnvRemovePath(ExpandConstant('{app}'));
end;
