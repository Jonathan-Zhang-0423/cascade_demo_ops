$ErrorActionPreference = "Stop"

$repoRoot = Split-Path -Parent $PSScriptRoot
$runRoot = Join-Path $repoRoot ".cascade-dev\run"

function Get-ListeningProcessId {
  param([int]$Port)

  foreach ($line in (& netstat -ano -p tcp)) {
    if ($line -match "^\s*TCP\s+\S+:$Port\s+\S+\s+LISTENING\s+(\d+)\s*$") {
      return [int]$Matches[1]
    }
  }
  return $null
}

foreach ($service in @(
  @{ Name = "editor-web"; Port = 3000 },
  @{ Name = "editor-bridge"; Port = 4317 }
)) {
  $name = $service.Name
  $pidPath = Join-Path $runRoot "$name.pid"
  # Resolve the current listener instead of trusting a stale PID that Windows may have reused.
  $processID = Get-ListeningProcessId -Port $service.Port
  $process = if ($processID) { Get-Process -Id $processID -ErrorAction SilentlyContinue } else { $null }
  if ($process) {
    Stop-Process -Id $process.Id -Force
    Write-Host "$name stopped (PID $($process.Id))"
  }
  Remove-Item -LiteralPath $pidPath -Force -ErrorAction SilentlyContinue
}
