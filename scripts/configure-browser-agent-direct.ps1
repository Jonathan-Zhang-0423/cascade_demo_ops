param(
    [string]$EnvFile = (Join-Path $PSScriptRoot "..\.env"),
    [string]$PrivateKeyPath = "C:\tmp\cascade-app-formal-e2e-20260804\ubuntu-e2e-ed25519",
    [string]$KnownHostsPath = (Join-Path $PSScriptRoot "..\..\ssh-known-hosts"),
    [string]$ControlURL = "https://app.cascadeai.cn:18443",
    [string]$HostOverride = "",
    [string]$UserOverride = "",
    [string]$DataRoot = ""
)

$ErrorActionPreference = "Stop"

function Read-ConnectionFields([string]$Path) {
    $result = @{}
    foreach ($line in Get-Content -LiteralPath $Path -Encoding UTF8) {
        if ($line -match '^\s*(server[ _-]?(?:address|username))\s*[:=]\s*(.*?)\s*$') {
            $normalized = ($matches[1] -replace '[ _-]', '').ToLowerInvariant()
            $key = if ($normalized -eq "serveraddress") { "SSH_HOST" } else { "SSH_USER" }
            $result[$key] = $matches[2].Trim().Trim('"').Trim("'")
            continue
        }
        if ($line -match '^\s*(SSH_HOST|SSH_USER|SSH_PORT)\s*=\s*(.*?)\s*$') {
            $result[$matches[1]] = $matches[2].Trim().Trim('"').Trim("'")
        }
    }
    return $result
}

$resolvedEnv = (Resolve-Path -LiteralPath $EnvFile).Path
$repoRoot = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot "..")).Path
git -C $repoRoot check-ignore -q -- $resolvedEnv
if ($LASTEXITCODE -ne 0) { throw "Connection env file must be ignored by Git." }

$values = Read-ConnectionFields $resolvedEnv
$sshHost = if ([string]::IsNullOrWhiteSpace($HostOverride)) { ([string]$values["SSH_HOST"]).Trim() } else { $HostOverride.Trim() }
$sshUser = if ([string]::IsNullOrWhiteSpace($UserOverride)) { ([string]$values["SSH_USER"]).Trim() } else { $UserOverride.Trim() }
$portText = ([string]$values["SSH_PORT"]).Trim()
if ([string]::IsNullOrWhiteSpace($portText)) { $portText = "22" }

if ($sshHost -notmatch '^[A-Za-z0-9][A-Za-z0-9.-]*$') { throw "SSH host is missing or invalid." }
if ($sshUser -notmatch '^[A-Za-z_][A-Za-z0-9._-]*$') { throw "SSH user is missing or invalid." }
$sshPort = 0
if (-not [int]::TryParse($portText, [ref]$sshPort) -or $sshPort -lt 1 -or $sshPort -gt 65535) { throw "SSH port is invalid." }
if (-not (Test-Path -LiteralPath $PrivateKeyPath -PathType Leaf)) { throw "SSH private key is unavailable." }
if (-not (Test-Path -LiteralPath $KnownHostsPath -PathType Leaf)) { throw "Strict known_hosts file is unavailable." }

$backendRoot = Join-Path $repoRoot "backend"
$arguments = @(
    "run", "./cmd/browser-agent-direct-configure",
    "--ssh-host", $sshHost,
    "--ssh-user", $sshUser,
    "--ssh-port", [string]$sshPort,
    "--ssh-private-key", (Resolve-Path -LiteralPath $PrivateKeyPath).Path,
    "--known-hosts", (Resolve-Path -LiteralPath $KnownHostsPath).Path,
    "--control-url", $ControlURL
)
if (-not [string]::IsNullOrWhiteSpace($DataRoot)) { $arguments += @("--data-root", $DataRoot) }

Push-Location $backendRoot
try {
    & go @arguments
    if ($LASTEXITCODE -ne 0) { throw "Browser Agent direct provisioning failed with a redacted error; inspect error_class only." }
} finally {
    Pop-Location
}
