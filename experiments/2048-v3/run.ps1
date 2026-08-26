param(
  [string]$AppBaseURL = "http://127.0.0.1:4318",
  [string]$TargetURL = "https://cascadeai.cn/app",
  [string]$UserGoal = "构建一款适合产品演示的精致响应式 2048 网页游戏",
  [Parameter(Mandatory = $true)][string]$CredentialRef,
  [string]$AuthorizationRef = ("approval://experiment/2048-v3/" + [DateTimeOffset]::UtcNow.ToUnixTimeSeconds()),
  [string]$OutputDirectory = ""
)

$ErrorActionPreference = "Stop"
$idempotencyKey = "experiment-2048-v3-" + [Guid]::NewGuid().ToString("N")
$body = @{
  definition_ref = "2048-v3"
  user_goal = $UserGoal
  target_url = $TargetURL
  credential_ref = $CredentialRef
  authorization_ref = $AuthorizationRef
  idempotency_key = $idempotencyKey
  harness_profile = "adaptive-business-harness-v2"
} | ConvertTo-Json

$created = Invoke-RestMethod -Method Post -ContentType "application/json" -Uri ($AppBaseURL.TrimEnd("/") + "/v1/experiment-runs") -Body $body
if (-not $created.ok) { throw "DemoOps rejected the v3 experiment start request." }
$runID = $created.data.run_id
Write-Output ("experiment_run_id=" + $runID)

while ($true) {
  Start-Sleep -Seconds 5
  $view = Invoke-RestMethod -Method Get -Uri ($AppBaseURL.TrimEnd("/") + "/v1/experiment-runs/" + $runID)
  if (-not $view.ok) { throw "DemoOps could not project the v3 experiment run." }
  $run = $view.data
  Write-Output ((Get-Date -Format o) + " state=" + $run.state + " phase=" + $run.phase + " revision=" + $run.revision)
  if ($run.state -in @("succeeded", "failed", "canceled", "expired", "waiting_input", "waiting_external")) { break }
}

if ([string]::IsNullOrWhiteSpace($OutputDirectory)) {
  $OutputDirectory = Join-Path $PSScriptRoot "runs"
}
New-Item -ItemType Directory -Force -Path $OutputDirectory | Out-Null
$reportPath = Join-Path $OutputDirectory ($runID + ".json")
$run | ConvertTo-Json -Depth 30 | Set-Content -LiteralPath $reportPath -Encoding utf8
Write-Output ("run_snapshot=" + (Resolve-Path -LiteralPath $reportPath))

if ($run.state -eq "waiting_input" -and $run.phase -eq "awaiting_final_review") {
  Write-Output ("final_film_job_id=" + $run.final_film.job_id)
  Write-Output ("review_package_id=" + $run.final_film.package_id)
  Write-Output "The experiment is waiting for its single permitted human final review."
} elseif ($run.waiting) {
  Write-Output ("waiting_reason=" + $run.waiting.reason)
  Write-Output ("next_action=" + $run.waiting.next_action)
}
