#define AppName "Cascade DemoOps"
#define AppVersion GetEnv("CASCADE_APP_VERSION")
#define PackageRoot AddBackslash(SourcePath) + "..\..\dist\package"
#define WebViewBootstrapper GetEnv("CASCADE_WEBVIEW2_BOOTSTRAPPER")
#define ReleaseChannel GetEnv("CASCADE_RELEASE_CHANNEL")

[Setup]
AppId={{F81B17C0-BC2D-4A73-8CF0-A1115E80862E}
AppName={#AppName}
AppVersion={#AppVersion}
DefaultDirName={localappdata}\Programs\CascadeDemoOps
DefaultGroupName={#AppName}
DisableProgramGroupPage=yes
PrivilegesRequired=lowest
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
OutputDir={#PackageRoot}\..\release
OutputBaseFilename=CascadeDemoOps-{#AppVersion}-windows-x64-setup
Compression=lzma2/ultra64
SolidCompression=yes
WizardStyle=modern
UninstallDisplayIcon={app}\cascade-demoops-desktop.exe
CloseApplications=yes
RestartApplications=no
#if ReleaseChannel != "internal"
SignTool=demoops
SignedUninstaller=yes
#endif

[Files]
Source: "{#PackageRoot}\*"; DestDir: "{app}"; Flags: ignoreversion recursesubdirs createallsubdirs
#if WebViewBootstrapper != ""
Source: "{#WebViewBootstrapper}"; DestDir: "{tmp}"; DestName: "MicrosoftEdgeWebview2Setup.exe"; Flags: deleteafterinstall
#endif

[Icons]
Name: "{group}\{#AppName}"; Filename: "{app}\cascade-demoops-desktop.exe"
Name: "{userdesktop}\{#AppName}"; Filename: "{app}\cascade-demoops-desktop.exe"; Tasks: desktopicon

[Tasks]
Name: "desktopicon"; Description: "创建桌面快捷方式"; GroupDescription: "快捷方式："; Flags: unchecked

[Run]
#if WebViewBootstrapper != ""
Filename: "{tmp}\MicrosoftEdgeWebview2Setup.exe"; Parameters: "/silent /install"; StatusMsg: "正在安装 Microsoft WebView2 Runtime..."; Check: not IsWebView2Installed
#endif
Filename: "{app}\cascade-demoops-desktop.exe"; Description: "启动 {#AppName}"; Flags: nowait postinstall skipifsilent

[Code]
function IsWebView2Installed: Boolean;
var
  Version: String;
begin
  Result :=
    RegQueryStringValue(HKCU, 'Software\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}', 'pv', Version) or
    RegQueryStringValue(HKLM, 'Software\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}', 'pv', Version) or
    RegQueryStringValue(HKLM64, 'Software\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}', 'pv', Version);
end;

function InitializeSetup: Boolean;
begin
  Result := True;
#if WebViewBootstrapper == ""
  if not IsWebView2Installed then
  begin
    MsgBox('Cascade DemoOps 需要 Microsoft Edge WebView2 Runtime。请先安装 WebView2，或使用包含 Evergreen Bootstrapper 的安装包。', mbError, MB_OK);
    Result := False;
  end;
#endif
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
begin
  { 用户数据位于 %APPDATA%\CascadeDemoOps，普通卸载始终保留。 }
end;

procedure CurStepChanged(CurStep: TSetupStep);
begin
  if CurStep = ssPostInstall then
    FileCopy(ExpandConstant('{srcexe}'), ExpandConstant('{app}\previous-installer.exe'), False);
end;
