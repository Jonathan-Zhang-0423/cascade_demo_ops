[CmdletBinding()]
param(
  [string]$EngineBaseUrl = "http://127.0.0.1:4317",
  [string]$TargetUrl = "http://127.0.0.1:5000/app",
  [string]$LocalRepoPath = "",
  [string]$TargetAudience = "local-product-e2e-acceptance",
  [string]$CredentialRefName = "unattended-e2e",
  [string]$ProjectIdea = "",
  [int]$TimeoutSec = 900,
  [switch]$UseStoredCredential,
  [switch]$NoRun
)

# Server-owned local dev/test orchestration only.
# This script never edits an App package, never changes App/Validation rules,
# and never sends credentials in the visible-browser prepare request.
$ErrorActionPreference = "Stop"

function Invoke-BridgeJson {
  param(
    [Parameter(Mandatory = $true)][ValidateSet("GET", "POST")][string]$Method,
    [Parameter(Mandatory = $true)][string]$Uri,
    [object]$Body
  )

  $params = @{
    Method = $Method
    Uri = $Uri
    TimeoutSec = $TimeoutSec
    ErrorAction = "Stop"
  }
  if ($null -ne $Body) {
    $params.ContentType = "application/json"
    $json = $Body | ConvertTo-Json -Depth 100 -Compress
    # Windows PowerShell may otherwise transcode non-ASCII JSON through the
    # system code page. Send exact UTF-8 bytes so Go's decoder sees one valid
    # JSON document and the App receives the original user text unchanged.
    $params.Body = [Text.Encoding]::UTF8.GetBytes($json)
  }
  $response = Invoke-RestMethod @params
  if ($null -ne $response.ok -and -not [bool]$response.ok) {
    $message = if ($response.error) { [string]$response.error } else { "Dev Bridge request failed" }
    throw $message
  }
  if ($null -ne $response.data) { return $response.data }
  return $response
}

function Write-Utf8NoBom {
  param([Parameter(Mandatory = $true)][string]$Path, [Parameter(Mandatory = $true)][string]$Text)
  $utf8 = New-Object System.Text.UTF8Encoding($false)
  [IO.File]::WriteAllText($Path, $Text, $utf8)
}

function Get-LoopbackTarget {
  param([string]$Value)
  $uri = [Uri]$Value
  if ($uri.Scheme -ne "http" -or $uri.UserInfo -or $uri.Query -or $uri.Fragment) {
    throw "TargetUrl must be a plain http loopback URL without credentials, query, or fragment"
  }
  if ($uri.Host -notin @("127.0.0.1", "localhost", "[::1]", "::1")) {
    throw "Unattended local acceptance accepts only loopback TargetUrl"
  }
  return $uri
}

function Get-Origin {
  param([Uri]$Uri)
  return "$($Uri.Scheme)://$($Uri.Host)"
}

function Decode-Utf8Base64 {
  param([Parameter(Mandatory = $true)][string]$Value)
  return [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($Value))
}

function Get-UniqueNodeIdsFromBlockingReasons {
  param([object[]]$Reasons)
  $seen = @{}
  foreach ($reason in @($Reasons)) {
    $text = ([string]$reason).Trim()
    if ($text -match "^(?<node>[^:]+):") {
      $node = $Matches.node.Trim()
      if ($node -and -not $seen.ContainsKey($node)) {
        $seen[$node] = $true
      }
    }
  }
  return @($seen.Keys | Sort-Object)
}

function Get-UnscopedBlockingReasonHashes {
  param([object[]]$Reasons)
  $seen = @{}
  $sha = [Security.Cryptography.SHA256]::Create()
  try {
    foreach ($reason in @($Reasons)) {
      $text = ([string]$reason).Trim()
      if (-not $text -or $text -match "^(?<node>[^:]+):") {
        continue
      }
      $bytes = [Text.Encoding]::UTF8.GetBytes($text)
      $hash = ([BitConverter]::ToString($sha.ComputeHash($bytes))).Replace("-", "").ToLowerInvariant()
      if (-not $seen.ContainsKey($hash)) {
        $seen[$hash] = $true
      }
    }
  } finally {
    $sha.Dispose()
  }
  return @($seen.Keys | Sort-Object)
}

$target = Get-LoopbackTarget $TargetUrl
$backupDirectory = Decode-Utf8Base64 "5aSH5Lu9"
if ([string]::IsNullOrWhiteSpace($LocalRepoPath)) {
  $LocalRepoPath = Join-Path "D:\" "$backupDirectory\Cascade-main"
}
if (-not (Test-Path -LiteralPath $LocalRepoPath -PathType Container)) {
  throw "LocalRepoPath does not exist: $LocalRepoPath"
}
$modelReadiness = Invoke-BridgeJson -Method GET -Uri "$EngineBaseUrl/v1/desktop/model-readiness"
if (-not [bool]$modelReadiness.ready) {
  $failedTasks = @($modelReadiness.diagnostics | Where-Object { -not [bool]$_.ok } | ForEach-Object { [string]$_.task })
  throw "Engine model readiness is false; formal App package generation and Chromium execution remain prohibited. Failed tasks: $($failedTasks -join ', ')"
}
$runtimeHealth = Invoke-BridgeJson -Method GET -Uri "$EngineBaseUrl/v1/desktop/runtime-health"
if (-not [bool]$runtimeHealth.node_runtime_configured -or -not [bool]$runtimeHealth.sidecars.'video-worker') {
  throw "Engine Node/video-worker runtime is not configured. Refusing to generate an App package from screenshot fallback evidence; restart the Engine Server with NODE_BINARY_PATH and NODE_WORKER_PATH."
}
$email = [string]$env:CASCADE_DEV_VISIBLE_LOGIN_EMAIL
$password = [string]$env:CASCADE_DEV_VISIBLE_LOGIN_PASSWORD
if ([string]::IsNullOrWhiteSpace($CredentialRefName) -or $CredentialRefName -match '[/\\:\x00\r\n]') {
  throw "CredentialRefName must be a simple local credential name"
}
if (-not $UseStoredCredential -and ([string]::IsNullOrWhiteSpace($email) -or [string]::IsNullOrEmpty($password))) {
  throw "Set CASCADE_DEV_VISIBLE_LOGIN_EMAIL and CASCADE_DEV_VISIBLE_LOGIN_PASSWORD, or use -UseStoredCredential after storing the local credential once"
}

$credentialRef = "credential://demo/$CredentialRefName"
if (-not $UseStoredCredential) {
  $stored = Invoke-BridgeJson -Method POST -Uri "$EngineBaseUrl/v1/desktop/demo-credential" -Body @{
    ref = $CredentialRefName
    username = $email
    password = $password
  }
  if (-not [bool]$stored.configured -or [string]$stored.secretRef -ne $credentialRef) {
    throw "Local credential vault did not return the expected opaque reference"
  }
  # Secret material is no longer needed by this process. The formal App
  # request below carries only the opaque credential reference.
  $email = ""
  $password = ""
}

$stamp = [DateTime]::UtcNow.ToString("yyyyMMdd-HHmmss")
$outputRoot = Join-Path (Join-Path (Get-Location) "artifacts\dev-test-only\unattended-app-server-e2e") $stamp
New-Item -ItemType Directory -Path $outputRoot -Force | Out-Null

$zhTargetPage = Decode-Utf8Base64 "55uu5qCH6aG16Z2i77ya"
$zhLocalSource = Decode-Utf8Base64 "5pys5Zyw5Lqn5ZOB56uv5Yiw56uv6aqM5pS2"
$zhOrder = Decode-Utf8Base64 "5b+F6aG75oyJ5Lul5LiL6aG65bqP5omn6KGM5bm25b2V5Yi25a2X5bmV6K+05piO77ya"
$zhStep1 = Decode-Utf8Base64 "MS4g54K55Ye74oCc5paw5bu66aG555uu4oCd44CC"
$zhStep2 = Decode-Utf8Base64 "Mi4g5Zyo4oCc5LuK5aSp5L2g5oOz5YGa5LuA5LmI77yf4oCd6L6T5YWl4oCc6LSq5ZCD6JuH5ri45oiP4oCd44CC"
$zhStep3 = Decode-Utf8Base64 "My4g54K55Ye74oCc5p6E5bu64oCd44CC"
$zhStep4 = Decode-Utf8Base64 "NC4g5Zyo5by55Ye655qE55So5oi36aG16Z2i6L6T5YWl4oCc5biu5oiR5p6E5bu65LiA5Liq6LSq5ZCD6JuH5ri45oiP77yM6KaB5rGC5Y+v5Lul6Ieq5a6a5LmJ55WM6Z2i6aKc6Imy77yM5bm25LiU5Y+v5Lul6YCJ5oup5LiJ56eN6Zq+5bqm5qih5byP4oCd77yM562J5b6F5p6E5bu65a6M5oiQ5ZCO5YGc5q2i5b2V5Yi244CC"
$zhForbidden = Decode-Utf8Base64 "56aB5q2i5L+u5pS55p2D6ZmQ44CB57uR5a6a5pSv5LuY5pa55byP44CB5a+85Ye65pWw5o2u44CB5aSN5Yi25a+G6ZKl5oiWIFRva2Vu77yb56aB5q2i6K+75Y+W5oiW5b2V5Yi25a+G56CB44CB6YKu566x44CB5omL5py65Y+344CBVG9rZW7jgIFDb29raWXjgIFBUEkgS2V544CC"
$zhSnakeGame = Decode-Utf8Base64 "6LSq5ZCD6JuH5ri45oiP"
$requestedProjectIdea = if ([string]::IsNullOrWhiteSpace($ProjectIdea)) { $zhSnakeGame } else { $ProjectIdea.Trim() }
if ([string]::IsNullOrWhiteSpace($ProjectIdea)) {
  $requirementBody = @(
    "$zhTargetPage$TargetUrl"
    "$zhLocalSource$LocalRepoPath"
    $zhOrder
    $zhStep1
    $zhStep2
    $zhStep3
    $zhStep4
    $zhForbidden
  ) -join "`n"
} else {
  $requirementBody = @(
    "Target page: $TargetUrl"
    "Local product source: $LocalRepoPath"
    "Execute this exact business flow and add a subtitle explanation for every step:"
    "1. Click New Project."
    "2. Fill the project-idea field with the exact value: $requestedProjectIdea"
    "3. Click Build."
    "4. Observe the real build page until a stable progress or completion state is visible; stop after at most ten minutes."
    "Never modify permissions, bind payment, export data, copy secrets, or expose credentials, tokens, cookies, or API keys."
  ) -join "`n"
}
$zhNewProject = Decode-Utf8Base64 "5paw5bu66aG555uu5YWl5Y+j"
$zhSnakeRequirement = Decode-Utf8Base64 "6LSq5ZCD6JuH5ri45oiP6ZyA5rGC"
$zhBuildButton = Decode-Utf8Base64 "5p6E5bu65oyJ6ZKu"
$zhBuildComplete = Decode-Utf8Base64 "5p6E5bu65a6M5oiQ54q25oCB"
$zhPassword = Decode-Utf8Base64 "5a+G56CB"
$zhEmail = Decode-Utf8Base64 "6YKu566x"
$zhPhone = Decode-Utf8Base64 "5omL5py65Y+3"

# Evidence-only input captured from the real Cascade modal in a prior local
# run. It is not an App package rewrite: the next App formal run receives this
# actual screenshot as an annotated source so it can bind the missing textarea
# and submit-button interactions to page evidence.
$evidenceScreenshotPath = Join-Path (Get-Location) "artifacts\dev-test-only\input-evidence\new-project-dialog-after.png"
$loginEvidenceScreenshotPath = Join-Path (Get-Location) "artifacts\dev-test-only\input-evidence\login-entry.png"
$webpageScreenshots = @()
if (Test-Path -LiteralPath $loginEvidenceScreenshotPath -PathType Leaf) {
  $loginEvidenceHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $loginEvidenceScreenshotPath).Hash.ToLowerInvariant()
  $loginEvidenceInfo = Get-Item -LiteralPath $loginEvidenceScreenshotPath
  $webpageScreenshots += @{
    id = "evidence_authentication_entry"
    url = ([Uri]$TargetUrl).GetLeftPart([System.UriPartial]::Authority) + "/login"
    title = "Cascade AI - Login"
    page_role = "authentication"
    artifact = @{
      id = "artifact_evidence_authentication_entry"
      kind = "webpage_screenshot"
      uri = ([Uri]$loginEvidenceScreenshotPath).AbsoluteUri
      mime_type = "image/png"
      sha256 = $loginEvidenceHash
      size_bytes = [int64]$loginEvidenceInfo.Length
    }
    sequence_id = "authentication_entry"
    step_hint = "Use the approved authentication entry route before entering the local credential reference."
    ocr_text = "登录 Cascade AI 邮箱登录 手机号登录 GitHub 账号登录 微信登录"
    vision_summary = "Real Cascade AI authentication entry page with email, phone, GitHub, and WeChat login options; no credential values are visible."
    annotations = @(
      @{ id = "annotation_authentication_email_entry"; kind = "click"; label = "Email login"; description = "Select the approved email authentication route."; selector_hint = "button[type='button']" }
    )
    metadata = @{
      observed_url = (([Uri]$TargetUrl).GetLeftPart([System.UriPartial]::Authority) + "/login")
      observed_route_template = "/login"
      observed_page_role = "authentication"
      observed_form_role = "authentication_entry"
      capture_scope = "server_test_input_only"
    }
  }
}
if (Test-Path -LiteralPath $evidenceScreenshotPath -PathType Leaf) {
  $evidenceHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $evidenceScreenshotPath).Hash.ToLowerInvariant()
  $evidenceInfo = Get-Item -LiteralPath $evidenceScreenshotPath
  $webpageScreenshots += @{
    id = "evidence_new_project_dialog_after"
    url = $TargetUrl
    title = "Cascade AI — Build Apps with AI"
    page_role = "new_project_dialog"
    artifact = @{
      id = "artifact_evidence_new_project_dialog_after"
      kind = "webpage_screenshot"
      uri = ([Uri]$evidenceScreenshotPath).AbsoluteUri
      mime_type = "image/png"
      sha256 = $evidenceHash
      size_bytes = [int64]$evidenceInfo.Length
    }
    sequence_id = "new_project_dialog"
    step_hint = "After opening New Project, fill the real project-idea textarea with the requested Snake game idea, then click Build."
    ocr_text = "What do you want to build? Build"
    vision_summary = "Real Cascade new-project dialog with a project-idea textarea and a Build button."
    annotations = @(
      @{
        id = "annotation_project_idea_input"
        kind = "fill"
        label = "Project idea input: $requestedProjectIdea"
        description = "Fill the project-idea textarea with the exact requested value: $requestedProjectIdea"
        selector_hint = "[data-testid='input-project-idea']"
      },
      @{
        id = "annotation_project_create_button"
        kind = "click"
        label = "Build"
        description = "Submit project creation and start the build."
        selector_hint = "[data-testid='button-create-project']"
      }
    )
  }
}

# This is the existing App formal orchestration path exposed by the local
# Dev Bridge. The request contains user intent only; no browser actions are
# authored by this script and no App package field is rewritten afterwards.
$userInput = @{
  mode = "desktop"
  product_url = $TargetUrl
  local_repo_path = $LocalRepoPath
  webpage_screenshots = $webpageScreenshots
  product_description = $requirementBody
  target_audience = $TargetAudience
  target_duration_sec = 600
  must_show = @($zhNewProject, $requestedProjectIdea, $zhBuildButton, $zhBuildComplete)
  must_not_show = @($zhPassword, $zhEmail, $zhPhone, "Token", "Cookie", "API Key")
  forbidden_pages = @("/billing", "/permissions", "/settings", "/admin", "/v1", "/aigc", "/execution-packages")
  forbidden_data = @("password", "email", "phone", "token", "cookie", "api key")
  # Do not add derived HTTPS origins to the raw App package. The App package
  # must carry only its actual visible product origin; Server derives the
  # recording origin from product_url during the local test waiver flow.
  allowed_domains = @("http://127.0.0.1:5000")
  requirement_documents = @(@{
    id = "unattended-e2e-requirements-$stamp"
    kind = "inline_markdown"
    title = "Local unattended E2E requirements"
    body = $requirementBody
    focus_areas = @("business_actions", "completion_observation", "subtitles", "redaction")
  })
}

$preparePath = "$EngineBaseUrl/v1/desktop/projects/auto/execution-package"
$appRequest = @{ user_input = $userInput; credential_ref = $credentialRef }
$appRequestJson = $appRequest | ConvertTo-Json -Depth 100 -Compress
$requestPath = Join-Path $outputRoot "app-formal-request.json"
Write-Utf8NoBom -Path $requestPath -Text $appRequestJson
$state = Invoke-BridgeJson -Method POST -Uri $preparePath -Body $appRequest
$projectId = [string]$state.project_id
if ([string]::IsNullOrWhiteSpace($projectId)) { throw "App formal package generation returned no project_id" }

# The local source is read-only and has already been supplied as part of the
# formal App request. When the product page lacks a matching deployment or
# repository identity signal, the App deliberately returns `unverified` rather
# than silently mixing page and source evidence. This unattended command is an
# explicit App-side decision point: confirm only that non-mismatch state, write
# the decision to the project audit trail, then regenerate the immutable package.
$sourceBinding = Invoke-BridgeJson -Method GET -Uri "$EngineBaseUrl/v1/desktop/projects/$([Uri]::EscapeDataString($projectId))/source-binding"
if ([string]$sourceBinding.status -eq "unverified") {
  if ([string]::IsNullOrWhiteSpace([string]$sourceBinding.assessment_hash)) {
    throw "App source binding is unverified without an assessment hash; refusing to produce a mixed-evidence package"
  }
  $confirmed = Invoke-BridgeJson -Method POST -Uri "$EngineBaseUrl/v1/desktop/projects/$([Uri]::EscapeDataString($projectId))/source-binding/decisions" -Body @{
    decision = "confirm_mixed"
    assessment_hash = [string]$sourceBinding.assessment_hash
    idempotency_key = "unattended-formal-source-binding-$stamp"
  }
  $projectId = [string]$confirmed.project_id
  if ([string]::IsNullOrWhiteSpace($projectId)) { throw "App source-binding confirmation returned no project_id" }
  $sourceBinding = Invoke-BridgeJson -Method GET -Uri "$EngineBaseUrl/v1/desktop/projects/$([Uri]::EscapeDataString($projectId))/source-binding"
}
if ([string]$sourceBinding.effective_mode -ne "mixed" -or ([string]$sourceBinding.status -ne "matched" -and [string]$sourceBinding.status -ne "confirmed")) {
  throw "App source binding remains ineligible for formal mixed-evidence package: status=$($sourceBinding.status), mode=$($sourceBinding.effective_mode)"
}

$buildPath = "$EngineBaseUrl/v1/desktop/projects/$([Uri]::EscapeDataString($projectId))/client-execution-package"
$build = Invoke-BridgeJson -Method POST -Uri $buildPath -Body @{ org_id = "org_desktop" }
$package = $build.package
if ($null -eq $package) { throw "App formal package export returned no package" }
$bundle = $package.executable_script_bundle
if ($null -eq $bundle) { throw "App package has no executable_script_bundle" }
if ([string]$bundle.script_manifest.runtime -ne "browser-agent-outline-v1") {
  throw "App package runtime is not browser-agent-outline-v1: $($bundle.script_manifest.runtime)"
}

$rawPath = Join-Path $outputRoot "client_execution_package.json"
Write-Utf8NoBom -Path $rawPath -Text ($package | ConvertTo-Json -Depth 100)
$manifest = [ordered]@{
  schema_version = "cascade.unattended_app_server_e2e_manifest.v1"
  generated_at = [DateTime]::UtcNow.ToString("o")
  app_formal_run = $true
  project_id = $projectId
  package_id = [string]$package.package_id
  raw_package_path = $rawPath
  app_formal_request_path = $requestPath
  package_digest_sha256 = [string]$build.package_digest_sha256
  bundle_hash_sha256 = [string]$bundle.reproducibility.bundle_hash_sha256
  plan_hash_sha256 = [string]$bundle.reproducibility.plan_hash_sha256
  target_url = [string]$package.recording_run_spec.base_url
  confidence_readiness = [string]$package.confidence_summary.readiness
  blocking_reasons = @($package.confidence_summary.blocking_reasons)
}

$packageTarget = Get-LoopbackTarget ([string]$package.recording_run_spec.base_url)
if ((Get-Origin $packageTarget) -ne (Get-Origin $target)) { throw "App package origin does not match requested local target" }
$nodeIds = @(Get-UniqueNodeIdsFromBlockingReasons @($package.confidence_summary.blocking_reasons))
$globalReasonHashes = @(Get-UnscopedBlockingReasonHashes @($package.confidence_summary.blocking_reasons))
$manifest.approved_node_ids = $nodeIds
$manifest.approved_blocking_reason_hashes = $globalReasonHashes
$auditPath = Join-Path $outputRoot "app-package-outline-audit.json"
$auditScript = Join-Path (Get-Location) "tests\server-e2e\scripts\audit-app-browser-agent-package.mjs"
if (-not (Test-Path -LiteralPath $auditScript -PathType Leaf)) {
  throw "App package audit script is missing: $auditScript"
}
$nodeCommand = Get-Command node -ErrorAction Stop
$auditOutput = @(& $nodeCommand.Source $auditScript $rawPath $requestedProjectIdea 2>&1)
$auditExitCode = $LASTEXITCODE
$auditText = ($auditOutput | ForEach-Object { [string]$_ }) -join "`n"
Write-Utf8NoBom -Path $auditPath -Text $auditText
$manifest.outline_audit_path = $auditPath
$manifest.ready_for_server_execution = ($auditExitCode -eq 0)
$manifestPath = Join-Path $outputRoot "app-package-manifest.json"
Write-Utf8NoBom -Path $manifestPath -Text ($manifest | ConvertTo-Json -Depth 20)

Write-Host "App formal package generated: $($package.package_id)"
Write-Host "Raw package: $rawPath"
Write-Host "Readiness: $($manifest.confidence_readiness)"
Write-Host "Blocking node count: $($nodeIds.Count)"
Write-Host "Outline audit: $auditPath"

if ($auditExitCode -ne 0) {
  throw "App formal package is not executable for the requested business flow. The raw package was preserved unchanged; inspect $auditPath"
}

if ($NoRun) {
  Write-Host "NoRun specified; package generation and identity checks completed without browser execution."
  exit 0
}
if ($nodeIds.Count -eq 0) {
  throw "No node-scoped App blocking reasons were returned; unattended test waiver cannot be issued automatically"
}
if ([string]$manifest.confidence_readiness -ne "blocked") {
  throw "Unattended waiver path requires App readiness=blocked; current value is $($manifest.confidence_readiness)"
}

$waiver = Invoke-BridgeJson -Method POST -Uri "$EngineBaseUrl/v1/desktop/app-package-test-waivers/raw-file" -Body @{
  package_file = $rawPath
  package_id = [string]$package.package_id
  expected_bundle_hash_sha256 = [string]$bundle.reproducibility.bundle_hash_sha256
  expected_plan_hash_sha256 = [string]$bundle.reproducibility.plan_hash_sha256
  approved_node_ids = $nodeIds
  approved_blocking_reason_hashes = $globalReasonHashes
  dev_test_ack = $true
}

$visible = Invoke-BridgeJson -Method POST -Uri "$EngineBaseUrl/v1/desktop/dev-visible-browser-agent/prepare" -Body @{
  target_url = $TargetUrl
  auto_login = $true
  credential_ref = $credentialRef
  dev_test_ack = $true
}
if ([string]$visible.status -ne "ready_for_approved_package") {
  throw "Visible browser did not reach the real local page: status=$($visible.status)"
}

$result = Invoke-BridgeJson -Method POST -Uri "$EngineBaseUrl/v1/desktop/app-package-test-waivers/$([Uri]::EscapeDataString([string]$waiver.waiver_id))/run" -Body @{
  session_id = [string]$visible.session_id
  dev_test_ack = $true
}
$resultPath = Join-Path $outputRoot "run-result.json"
Write-Utf8NoBom -Path $resultPath -Text ($result | ConvertTo-Json -Depth 100)
Write-Host "Acceptance status: $($result.result.status)"
Write-Host "Result summary: $resultPath"
Write-Host "The run is dev_test_only=true and formal_exchange=false; inspect the result package before claiming success."
