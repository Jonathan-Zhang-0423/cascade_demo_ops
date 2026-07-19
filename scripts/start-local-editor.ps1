param([switch]$Wait)

$ErrorActionPreference = "Stop"

$repoRoot = Split-Path -Parent $PSScriptRoot
$runtimeRoot = Join-Path $repoRoot ".cascade-dev"
$logRoot = Join-Path $runtimeRoot "logs"
$runRoot = Join-Path $runtimeRoot "run"
$goCacheRoot = Join-Path $repoRoot ".gocache"
$goTempRoot = Join-Path $repoRoot ".gotmp"

New-Item -ItemType Directory -Force -Path $logRoot, $runRoot, $goCacheRoot, $goTempRoot | Out-Null

$ffmpegPath = if ($env:CASCADE_FFMPEG_PATH) { $env:CASCADE_FFMPEG_PATH } elseif (Test-Path -LiteralPath "D:\ffmpeg\bin\ffmpeg.exe") { "D:\ffmpeg\bin\ffmpeg.exe" } else { "ffmpeg" }
$ffprobePath = if ($env:CASCADE_FFPROBE_PATH) { $env:CASCADE_FFPROBE_PATH } elseif (Test-Path -LiteralPath "D:\ffmpeg\bin\ffprobe.exe") { "D:\ffmpeg\bin\ffprobe.exe" } else { "ffprobe" }

$workerEntry = Join-Path $repoRoot "video-worker\dist\index.js"
$workerTSC = Join-Path $repoRoot "node_modules\.bin\tsc.cmd"
& $workerTSC -p (Join-Path $repoRoot "video-worker\tsconfig.json")
$bridgeEntry = Join-Path $runtimeRoot "bin\cascade-editor-bridge.exe"
New-Item -ItemType Directory -Force -Path (Split-Path -Parent $bridgeEntry) | Out-Null
$env:GOCACHE = $goCacheRoot
$env:GOTMPDIR = $goTempRoot
& go -C (Join-Path $repoRoot "backend") build -o $bridgeEntry ./cmd/devserver
if ($LASTEXITCODE -ne 0) { throw "Cascade Go Bridge build failed." }

function Get-ListeningProcessId {
  param([int]$Port)

  foreach ($line in (& netstat -ano -p tcp)) {
    if ($line -match "^\s*TCP\s+\S+:$Port\s+\S+\s+LISTENING\s+(\d+)\s*$") {
      return [int]$Matches[1]
    }
  }
  return $null
}

function Wait-HttpEndpoint {
  param(
    [string]$Name,
    [string]$Uri,
    [int]$TimeoutSeconds = 20
  )

  $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
  do {
    try {
      $response = Invoke-WebRequest -UseBasicParsing -Uri $Uri -TimeoutSec 2
      if ($response.StatusCode -ge 200 -and $response.StatusCode -lt 400) {
        return
      }
    } catch {
      Start-Sleep -Milliseconds 300
    }
  } while ((Get-Date) -lt $deadline)

  throw "$Name did not become ready at $Uri within $TimeoutSeconds seconds."
}

function Start-LocalProcess {
  param(
    [string]$Name,
    [string]$FilePath,
    [string[]]$Arguments,
    [string]$WorkingDirectory,
    [string]$PidPath,
    [string]$StdoutPath,
    [string]$StderrPath,
    [int]$Port
  )
  $listenerPID = Get-ListeningProcessId -Port $Port
  if ($listenerPID) {
    Set-Content -LiteralPath $PidPath -Value $listenerPID -Encoding ASCII
    Write-Host "$Name already listening on port $Port with PID $listenerPID"
    return
  }
  if (Test-Path -LiteralPath $PidPath) {
    $existingPID = Get-Content -LiteralPath $PidPath -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($existingPID -and (Get-Process -Id $existingPID -ErrorAction SilentlyContinue)) {
      Write-Host "$Name already running with PID $existingPID"
      return
    }
  }
  $process = Start-Process -FilePath $FilePath -ArgumentList $Arguments -WorkingDirectory $WorkingDirectory -WindowStyle Hidden -RedirectStandardOutput $StdoutPath -RedirectStandardError $StderrPath -PassThru
  Set-Content -LiteralPath $PidPath -Value $process.Id -Encoding ASCII
  Write-Host "$Name started with PID $($process.Id)"
}

$env:NODE_WORKER_PATH = $workerEntry
$env:CASCADE_FFMPEG_PATH = $ffmpegPath
$env:CASCADE_FFPROBE_PATH = $ffprobePath
Start-LocalProcess -Name "Cascade Go Bridge" -FilePath $bridgeEntry -Arguments @("--addr", "127.0.0.1:4317") -WorkingDirectory (Join-Path $repoRoot "backend") -PidPath (Join-Path $runRoot "editor-bridge.pid") -StdoutPath (Join-Path $logRoot "editor-bridge.out.log") -StderrPath (Join-Path $logRoot "editor-bridge.err.log") -Port 4317

Wait-HttpEndpoint -Name "Cascade Go Bridge" -Uri "http://127.0.0.1:4317/v1/desktop/runtime-health"
$bridgePID = Get-ListeningProcessId -Port 4317
if (-not $bridgePID) {
  throw "Cascade Go Bridge passed its health check but no listener PID was found on port 4317."
}
Set-Content -LiteralPath (Join-Path $runRoot "editor-bridge.pid") -Value $bridgePID -Encoding ASCII

$env:VITE_CASCADE_BRIDGE = "local"
$env:VITE_CASCADE_BRIDGE_URL = ""
$viteEntry = Join-Path $repoRoot "frontend\web\node_modules\vite\bin\vite.js"
Start-LocalProcess -Name "Cascade Editor UI" -FilePath "node" -Arguments @($viteEntry, "--host", "127.0.0.1", "--port", "3000", "--strictPort") -WorkingDirectory (Join-Path $repoRoot "frontend\web") -PidPath (Join-Path $runRoot "editor-web.pid") -StdoutPath (Join-Path $logRoot "editor-web.out.log") -StderrPath (Join-Path $logRoot "editor-web.err.log") -Port 3000

Wait-HttpEndpoint -Name "Cascade Editor UI" -Uri "http://127.0.0.1:3000"
$webPID = Get-ListeningProcessId -Port 3000
if (-not $webPID) {
  throw "Cascade Editor UI passed its health check but no listener PID was found on port 3000."
}
Set-Content -LiteralPath (Join-Path $runRoot "editor-web.pid") -Value $webPID -Encoding ASCII

Write-Host "Editor UI:  http://127.0.0.1:3000"
Write-Host "Go Bridge:  http://127.0.0.1:4317"
Write-Host "FFmpeg:     $ffmpegPath"
Write-Host "FFprobe:    $ffprobePath"

if ($Wait) {
  Write-Host "Supervising local editor services. Press Ctrl+C to exit."
  while ($true) {
    if (-not (Get-ListeningProcessId -Port 3000) -or -not (Get-ListeningProcessId -Port 4317)) {
      throw "A local editor service stopped while supervision was active."
    }
    Start-Sleep -Seconds 2
  }
}
