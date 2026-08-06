param(
    [string]$EnvFile = (Join-Path $PSScriptRoot "..\.env"),
    [string]$PublicKeyPath = "C:\tmp\cascade-app-formal-e2e-20260804\ubuntu-e2e-ed25519.pub",
    [string]$PrivateKeyPath = "C:\tmp\cascade-app-formal-e2e-20260804\ubuntu-e2e-ed25519",
    [string]$KnownHostsPath = (Join-Path $PSScriptRoot "..\..\ssh-known-hosts"),
    [string]$HostOverride = ""
)

$ErrorActionPreference = "Stop"

function Read-DotEnv([string]$Path) {
    $result = @{}
    foreach ($line in Get-Content -LiteralPath $Path -Encoding UTF8) {
        if ($line -match '^\s*(server[ _-]?(?:address|username|password))\s*[:=]\s*(.*)$') {
            $normalized = ($matches[1] -replace '[ _-]', '').ToLowerInvariant()
            $key = switch ($normalized) {
                "serveraddress" { "SSH_HOST" }
                "serverusername" { "SSH_USER" }
                "serverpassword" { "SSH_PASSWORD" }
            }
            $value = $matches[2].Trim()
            if ($value.Length -ge 2) {
                $first = $value[0]
                $last = $value[$value.Length - 1]
                if (($first -eq '"' -and $last -eq '"') -or ($first -eq "'" -and $last -eq "'")) {
                    $value = $value.Substring(1, $value.Length - 2)
                }
            }
            $result[$key] = $value
            continue
        }
        if ($line -notmatch '^\s*(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*)$') { continue }
        $key = $matches[1]
        $value = $matches[2].Trim()
        if ($value.Length -ge 2) {
            $first = $value[0]
            $last = $value[$value.Length - 1]
            if (($first -eq '"' -and $last -eq '"') -or ($first -eq "'" -and $last -eq "'")) {
                $value = $value.Substring(1, $value.Length - 2)
            }
        }
        $result[$key] = $value
    }
    return $result
}

function Resolve-Password([hashtable]$Values) {
    if (-not [string]::IsNullOrWhiteSpace([string]$Values["SSH_PASSWORD"])) {
        return [string]$Values["SSH_PASSWORD"]
    }
    $reference = [string]$Values["SSH_PASSWORD_SECRET_REF"]
    if ($reference -match '^env:([A-Za-z_][A-Za-z0-9_]*)$') {
        return [Environment]::GetEnvironmentVariable($matches[1], "Process")
    }
    if ($reference -match '^[A-Za-z_][A-Za-z0-9_]*$' -and $Values.ContainsKey($reference)) {
        return [string]$Values[$reference]
    }
    return ""
}

$resolvedEnv = (Resolve-Path -LiteralPath $EnvFile).Path
$repoRoot = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot "..")).Path
git -C $repoRoot check-ignore -q -- $resolvedEnv
if ($LASTEXITCODE -ne 0) { throw "SSH bootstrap env file must be ignored by Git." }

$values = Read-DotEnv $resolvedEnv
$hostName = ([string]$values["SSH_HOST"]).Trim()
$HostOverride = $HostOverride.Trim()
if (-not [string]::IsNullOrWhiteSpace($HostOverride)) { $hostName = $HostOverride }
$userName = ([string]$values["SSH_USER"]).Trim()
$portText = ([string]$values["SSH_PORT"]).Trim()
$password = Resolve-Password $values

if ($hostName -notmatch '^[A-Za-z0-9.-]+$') { throw "SSH_HOST is missing or invalid." }
if ($userName -notmatch '^[A-Za-z_][A-Za-z0-9._-]*$') { throw "SSH_USER is missing or invalid." }
$port = 0
if (-not [int]::TryParse($portText, [ref]$port) -or $port -lt 1 -or $port -gt 65535) { throw "SSH_PORT is invalid." }
if ([string]::IsNullOrEmpty($password)) { throw "SSH_PASSWORD or its supported local secret reference is missing." }
if (-not (Test-Path -LiteralPath $PublicKeyPath) -or -not (Test-Path -LiteralPath $PrivateKeyPath)) { throw "Temporary SSH key pair is missing." }

$publicKey = (Get-Content -Raw -LiteralPath $PublicKeyPath -Encoding ASCII).Trim()
if ($publicKey -notmatch '^ssh-ed25519 [A-Za-z0-9+/=]+ [A-Za-z0-9._@-]+$') { throw "Temporary SSH public key is invalid." }

$askPass = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot "ssh-askpass.cmd")).Path
$env:CASCADE_SSH_BOOTSTRAP_PASSWORD = $password
$env:SSH_ASKPASS = $askPass
$env:SSH_ASKPASS_REQUIRE = "force"
$env:DISPLAY = "cascade-ssh-bootstrap"
$target = "${userName}@${hostName}"
$installCommand = "umask 077; mkdir -p ~/.ssh; touch ~/.ssh/authorized_keys; grep -qxF '$publicKey' ~/.ssh/authorized_keys || printf '%s\n' '$publicKey' >> ~/.ssh/authorized_keys; chmod 700 ~/.ssh; chmod 600 ~/.ssh/authorized_keys"

try {
    & ssh -p $port -o PreferredAuthentications=password -o PubkeyAuthentication=no -o NumberOfPasswordPrompts=1 -o ConnectTimeout=15 -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile=$KnownHostsPath $target $installCommand
    if ($LASTEXITCODE -ne 0) { throw "Password-authenticated SSH public-key bootstrap failed." }
} finally {
    $env:CASCADE_SSH_BOOTSTRAP_PASSWORD = $null
    $password = $null
}

& ssh -i $PrivateKeyPath -p $port -o BatchMode=yes -o IdentitiesOnly=yes -o ConnectTimeout=15 -o StrictHostKeyChecking=yes -o UserKnownHostsFile=$KnownHostsPath $target "printf '{\"key_auth\":true,\"sudo_nopass\":'; if sudo -n true 2>/dev/null; then printf true; else printf false; fi; printf ',\"arch\":\"'; uname -m; printf '\",\"os\":\"'; . /etc/os-release; printf '%s %s' \"`$ID\" \"`$VERSION_ID\"; printf '\"}\n'"
if ($LASTEXITCODE -ne 0) { throw "SSH key authentication verification failed." }
