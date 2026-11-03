; Установщик Windows (Inno Setup): один setup.exe из каталога dist, который
; собрали windeployqt и ldd (release.yml). Ставится без прав администратора
; (в %LocalAppData%\Programs), регистрирует ссылки mattermost:// на себя.
; Параметры: /DTag=v1.2.3 (имя файла) /DVersion=1.2.3 (версия в свойствах).

#ifndef Tag
  #define Tag "dev"
#endif
#ifndef Version
  #define Version "0.0.0"
#endif

[Setup]
AppId={{io.github.matterqute}}
AppName=Matterqute
AppVersion={#Version}
AppVerName=Matterqute {#Tag}
AppPublisher=Matterqute
AppPublisherURL=https://github.com/dmitry-novozhilov/matterqute
AppSupportURL=https://github.com/dmitry-novozhilov/matterqute/issues
DefaultDirName={autopf}\Matterqute
DefaultGroupName=Matterqute
DisableProgramGroupPage=yes
PrivilegesRequired=lowest
PrivilegesRequiredOverridesAllowed=dialog
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
OutputBaseFilename=matterqute-{#Tag}-windows-x86_64-setup
Compression=lzma2
SolidCompression=yes
WizardStyle=modern
UninstallDisplayIcon={app}\matterqute.exe
LicenseFile=..\..\LICENSE

[Languages]
Name: "en"; MessagesFile: "compiler:Default.isl"
Name: "ru"; MessagesFile: "compiler:Languages\Russian.isl"

[Tasks]
Name: "desktopicon"; Description: "{cm:CreateDesktopIcon}"; GroupDescription: "{cm:AdditionalIcons}"; Flags: unchecked

[Files]
Source: "..\..\dist\*"; DestDir: "{app}"; Flags: ignoreversion recursesubdirs createallsubdirs

[Icons]
Name: "{group}\Matterqute"; Filename: "{app}\matterqute.exe"
Name: "{autodesktop}\Matterqute"; Filename: "{app}\matterqute.exe"; Tasks: desktopicon

[Registry]
; Ссылки mattermost:// («Открыть в приложении» на странице Mattermost) — в это приложение.
Root: HKA; Subkey: "Software\Classes\mattermost"; ValueType: string; ValueName: ""; ValueData: "URL:Mattermost link"; Flags: uninsdeletekey
Root: HKA; Subkey: "Software\Classes\mattermost"; ValueType: string; ValueName: "URL Protocol"; ValueData: ""
Root: HKA; Subkey: "Software\Classes\mattermost\shell\open\command"; ValueType: string; ValueName: ""; ValueData: """{app}\matterqute.exe"" ""%1"""

[Run]
Filename: "{app}\matterqute.exe"; Description: "{cm:LaunchProgram,Matterqute}"; Flags: nowait postinstall skipifsilent
