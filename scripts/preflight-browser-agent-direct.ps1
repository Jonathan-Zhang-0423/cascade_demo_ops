param(
    [string]$ControlURL = "https://app.cascadeai.cn:18443",
    [int]$TimeoutMilliseconds = 10000
)

$ErrorActionPreference = "Stop"

function Test-TcpPort([string]$HostName, [int]$Port, [int]$TimeoutMs) {
    $client = [System.Net.Sockets.TcpClient]::new()
    try {
        $task = $client.ConnectAsync($HostName, $Port)
        return $task.Wait($TimeoutMs) -and $client.Connected
    } catch {
        return $false
    } finally {
        $client.Dispose()
    }
}

if ($TimeoutMilliseconds -lt 1000 -or $TimeoutMilliseconds -gt 60000) {
    throw "TimeoutMilliseconds must be between 1000 and 60000."
}
$uri = [Uri]::new($ControlURL.TrimEnd('/'))
if ($uri.Scheme -ne "https" -or [string]::IsNullOrWhiteSpace($uri.Host) -or $uri.Port -ne 18443 -or $uri.UserInfo -or $uri.Query -or $uri.Fragment) {
    throw "ControlURL must be an HTTPS URL on fixed port 18443 without credentials or query data."
}

$dns = $false
try { $dns = ([System.Net.Dns]::GetHostAddresses($uri.DnsSafeHost).Count -gt 0) } catch { $dns = $false }
$controlTCP = Test-TcpPort $uri.DnsSafeHost 18443 $TimeoutMilliseconds
$httpsStatus = $null
$tls = $false
$healthURL = "$($uri.AbsoluteUri.TrimEnd('/'))/v1/direct/health"
$curl = Get-Command curl.exe -ErrorAction SilentlyContinue
if ($null -ne $curl) {
    $curlOutput = & $curl.Source --noproxy "*" --tlsv1.3 --max-time ([Math]::Ceiling($TimeoutMilliseconds / 1000)) --silent --show-error --output NUL --write-out "%{http_code}|%{ssl_verify_result}" $healthURL 2>$null
    if ($LASTEXITCODE -eq 0) {
        $parts = ([string]$curlOutput).Trim().Split('|')
        if ($parts.Count -eq 2 -and $parts[1] -eq "0") {
            $tls = $true
            $parsedStatus = 0
            if ([int]::TryParse($parts[0], [ref]$parsedStatus)) { $httpsStatus = $parsedStatus }
        }
    }
} else {
    try {
        $handler = [System.Net.Http.HttpClientHandler]::new()
        $handler.UseProxy = $false
        $http = [System.Net.Http.HttpClient]::new($handler)
        $http.Timeout = [TimeSpan]::FromMilliseconds($TimeoutMilliseconds)
        $response = $http.GetAsync($healthURL).GetAwaiter().GetResult()
        $httpsStatus = [int]$response.StatusCode
        $response.Dispose()
        $http.Dispose()
        $tls = $true
    } catch {
        $httpsStatus = $null
    }
}

# A reachable existing HTTPS service with an unreachable new control port is a
# strong signal of a cloud security-group/upstream ACL block, not a TLS error.
$existingHTTPS = Test-TcpPort $uri.DnsSafeHost 443 $TimeoutMilliseconds
$upstreamLikelyBlocked = $existingHTTPS -and -not $controlTCP

[ordered]@{
    ok = $dns -and $controlTCP -and $tls
    control_url_host = $uri.Host
    control_port = 18443
    dns_resolves = $dns
    tcp_18443_reachable = $controlTCP
    tls_hostname_valid = $tls
    unauthenticated_health_status = $httpsStatus
    existing_https_443_reachable = $existingHTTPS
    cloud_security_group_likely_blocked = $upstreamLikelyBlocked
    data_port_range = "24000-24031 (verified after an active lease)"
    worker_port = "18444 loopback-only; never tested as public"
} | ConvertTo-Json -Compress
