param(
    [Parameter(Mandatory = $true)]
    [string]$ProjectId,
    [string]$BridgeBaseUrl = "http://127.0.0.1:4317",
    [string]$OrgId = "org_desktop",
    [string]$OutputRoot = "test-runs"
)

$ErrorActionPreference = "Stop"

function Write-Utf8Json {
    param([string]$Path, [object]$Value, [int]$Depth = 100)
    $json = $Value | ConvertTo-Json -Depth $Depth
    [System.IO.File]::WriteAllText($Path, $json + [Environment]::NewLine, [System.Text.UTF8Encoding]::new($false))
}

function Get-PropertyValue {
    param([object]$Value, [string]$Name)
    if ($null -eq $Value) { return $null }
    $property = $Value.PSObject.Properties[$Name]
    if ($null -eq $property) { return $null }
    return $property.Value
}

$timestamp = Get-Date -Format "yyyyMMdd-HHmmss"
$outputDirectory = Join-Path $OutputRoot "app-tetris-uploadable-package-$timestamp"
$resolvedOutputRoot = [System.IO.Path]::GetFullPath((Join-Path (Get-Location) $OutputRoot))
$resolvedOutputDirectory = [System.IO.Path]::GetFullPath((Join-Path (Get-Location) $outputDirectory))
if (-not $resolvedOutputDirectory.StartsWith($resolvedOutputRoot + [System.IO.Path]::DirectorySeparatorChar, [System.StringComparison]::OrdinalIgnoreCase)) {
    throw "Refusing to export outside the requested output root."
}

$requestBody = @{ org_id = $OrgId } | ConvertTo-Json -Compress
$uri = "$($BridgeBaseUrl.TrimEnd('/'))/v1/desktop/projects/$([Uri]::EscapeDataString($ProjectId))/client-execution-package"
$webResponse = Invoke-WebRequest -Method Post -Uri $uri -ContentType "application/json" -Body $requestBody -TimeoutSec 120
$rawResponse = $webResponse.Content
$response = $rawResponse | ConvertFrom-Json -Depth 100
if (-not $response.ok -or $null -eq $response.data -or $null -eq $response.data.package) {
    throw "The App did not return a valid client execution package."
}

$build = $response.data
$package = $build.package
$confidence = $package.confidence_summary
$runtime = Get-PropertyValue $package.executable_script_bundle "runtime"
if ([string]::IsNullOrWhiteSpace($runtime)) {
    $outline = Get-PropertyValue $package.executable_script_bundle "script_outline"
    $runtime = Get-PropertyValue $outline "runtime"
}
$stageCount = @($package.executable_script_bundle.plan_json.steps).Count
$graphDigest = $package.reproducibility.graph_hash_sha256
$bundleReproducibility = $package.executable_script_bundle.reproducibility
$bundleDigest = $bundleReproducibility.bundle_hash_sha256
$policyDigest = $bundleReproducibility.browser_agent_contract_hash_sha256

$sensitivePatterns = @(
    @{ id = "plaintext_test_email"; regex = "(?i)yikai[.]xu@cascadeai[.]cn" },
    @{ id = "plaintext_test_password"; regex = "(?<![0-9])000000(?![0-9])" },
    @{ id = "product_source_path"; regex = "(?i)C:[\\\\/]Users[\\\\/]CascadeAI[\\\\/]Desktop[\\\\/]CascadeAI[\\\\/]Cascade" },
    @{ id = "generic_secret_assignment"; regex = '(?i)"(password|passwd|api[_-]?key|authorization)"\s*:\s*"(?!<redacted>|credential://)[^"]+"' }
)
$scanFindings = @()
foreach ($pattern in $sensitivePatterns) {
    $matchCount = [regex]::Matches($rawResponse, $pattern.regex).Count
    if ($matchCount -gt 0) {
        $scanFindings += [ordered]@{ id = $pattern.id; count = $matchCount }
    }
}
if ($scanFindings.Count -gt 0) {
    throw "Secret/path leak scan blocked export: $($scanFindings.id -join ', ')"
}

New-Item -ItemType Directory -Path $resolvedOutputDirectory | Out-Null
[System.IO.File]::WriteAllText((Join-Path $resolvedOutputDirectory "client_execution_package.json"), (($package | ConvertTo-Json -Depth 100) + [Environment]::NewLine), [System.Text.UTF8Encoding]::new($false))
[System.IO.File]::WriteAllText((Join-Path $resolvedOutputDirectory "local_build_response.json"), ($rawResponse.TrimEnd() + [Environment]::NewLine), [System.Text.UTF8Encoding]::new($false))

$validation = [ordered]@{
    schema_version = "demoops.local-formal-package-validation.v1"
    generated_at = (Get-Date).ToUniversalTime().ToString("o")
    validation_scope = "local_app_package_build_only"
    product_project_id = $ProjectId
    package_id = $package.package_id
    runtime = $runtime
    stage_count = $stageCount
    build_status = $build.build_status
    package_readiness = $confidence.readiness
    allowed_to_upload = [bool]$package.safety_report.allowed_to_upload
    approval_present = -not [string]::IsNullOrWhiteSpace($package.safety_report.human_approval.approval_id)
    local_preflight_passed = @($package.safety_report.policy_findings | Where-Object { $_.severity -eq "blocker" }).Count -eq 0
    blocking_reasons = @($confidence.blocking_reasons)
    warnings = @($confidence.warnings)
    package_size = $build.size_report
    claims = [ordered]@{
        package_generated = $true
        package_approved = $false
        cloud_uploaded = $false
        browser_agent_executed = $false
        product_outcome_verified = $false
    }
}
Write-Utf8Json (Join-Path $resolvedOutputDirectory "local-validation-report.json") $validation
Write-Utf8Json (Join-Path $resolvedOutputDirectory "confidence-report.json") $confidence

$graphNodes = @{}
foreach ($node in @($package.workflow_graph.nodes)) { $graphNodes[$node.id] = $node }
$approvalStages = @{}
foreach ($stage in @($package.executable_script_bundle.stage_approval_plan.stages)) { $approvalStages[$stage.node_id] = $stage }
$outlineStages = @{}
foreach ($stage in @($package.executable_script_bundle.script_outline.stages)) { $outlineStages[$stage.node_id] = $stage }
$contractStages = @()
$contractMismatches = @()
foreach ($step in @($package.executable_script_bundle.plan_json.steps)) {
    $node = $graphNodes[$step.node_id]
    $approvalStage = $approvalStages[$step.node_id]
    $outlineStage = $outlineStages[$step.node_id]
    $outlineValues = @($outlineStage.interactions | ForEach-Object { [bool]$_.non_destructive })
    $outlineMatches = $outlineValues.Count -gt 0 -and @($outlineValues | Where-Object { $_ -ne [bool]$step.non_destructive }).Count -eq 0
    $consistent = $null -ne $node -and $null -ne $approvalStage -and $null -ne $outlineStage -and
        [bool]$node.metadata.non_destructive -eq [bool]$step.non_destructive -and
        [bool]$approvalStage.interaction.non_destructive -eq [bool]$step.non_destructive -and $outlineMatches
    if (-not $consistent) { $contractMismatches += $step.node_id }
    $contractStages += [ordered]@{
        node_id = $step.node_id
        graph_non_destructive = [bool]$node.metadata.non_destructive
        plan_non_destructive = [bool]$step.non_destructive
        approval_non_destructive = [bool]$approvalStage.interaction.non_destructive
        outline_non_destructive = $outlineValues
        runtime_adaptive = [bool]$step.runtime_adaptive
        consistent = $consistent
    }
}
$contractValidation = [ordered]@{
    runtime = $runtime
    stage_count = $stageCount
    all_layers_present = $stageCount -eq $approvalStages.Count -and $stageCount -eq $outlineStages.Count
    non_destructive_consensus = $contractMismatches.Count -eq 0
    mismatches = $contractMismatches
    stages = $contractStages
}
Write-Utf8Json (Join-Path $resolvedOutputDirectory "execution-contract-validation.json") $contractValidation

$hashes = [ordered]@{
    graph_digest_sha256 = $graphDigest
    bundle_digest_sha256 = $bundleDigest
    browser_agent_policy_digest_sha256 = $policyDigest
    plan_digest_sha256 = $bundleReproducibility.plan_hash_sha256
    stage_approval_plan_digest_sha256 = $bundleReproducibility.stage_plan_hash_sha256
    outline_digest_sha256 = $bundleReproducibility.outline_hash_sha256
    prompt_policy_digest_sha256 = $bundleReproducibility.prompt_policy_hash_sha256
    approval_subject_digest_sha256 = $build.approval_subject_digest_sha256
    package_digest_sha256 = $build.package_digest_sha256
    confidence_assessment_hash = $confidence.assessment_hash
}
Write-Utf8Json (Join-Path $resolvedOutputDirectory "formal-package-hashes.json") $hashes

$runtimeHealth = Invoke-RestMethod -Uri "$($BridgeBaseUrl.TrimEnd('/'))/v1/desktop/runtime-health" -TimeoutSec 10
$exchange = $runtimeHealth.data.cloud_exchange
$gate = [ordered]@{
    checked_at = (Get-Date).ToUniversalTime().ToString("o")
    llm_mode = $runtimeHealth.data.llm_mode
    exchange_discovered = [bool]$exchange.exchange_discovered
    installation_paired = [bool]$exchange.installation_paired
    session_valid = [bool]$exchange.session_valid
    base_url_host = $exchange.base_url_host
    base_url_path = $exchange.base_url_path
    auth_mode = $exchange.auth_mode
    release_gate = if ($exchange.installation_paired -and $exchange.session_valid) { "available" } else { "blocked_cloud_auth_unavailable" }
}
Write-Utf8Json (Join-Path $resolvedOutputDirectory "exchange-gate-sanitized.json") $gate

$scan = [ordered]@{
    scanned_at = (Get-Date).ToUniversalTime().ToString("o")
    status = "passed"
    files_scanned = @("client_execution_package.json", "local_build_response.json")
    checks = @($sensitivePatterns | ForEach-Object { $_.id })
    finding_count = 0
    plaintext_credentials_found = $false
    opaque_secret_refs_allowed = $true
}
Write-Utf8Json (Join-Path $resolvedOutputDirectory "secret-leak-scan.json") $scan

$readmeTemplate = @'
# CascadeAI Tetris formal package handoff

This directory contains the exact package generated by the running App Bridge for project `{0}`.

- Scope: local package generation and local package preflight only.
- Runtime contract: `{1}`.
- Approval: not granted; the export does not create or alter approval state.
- Upload: not attempted.
- Browser Agent execution: not attempted.
- Product result: not claimed or fabricated.
- Cloud gate: `{2}` because installation pairing/session is not currently valid.
- Credential handling: only opaque credential references are permitted; plaintext test credentials and local product-source paths were scanned and not found.

`client_execution_package.json` is the formal package payload. `local_build_response.json` is the complete App build response. Review `local-validation-report.json`, `execution-contract-validation.json`, `confidence-report.json`, and `formal-package-hashes.json` before approval.
'@
$readme = $readmeTemplate -f $ProjectId, $runtime, $gate.release_gate
[System.IO.File]::WriteAllText((Join-Path $resolvedOutputDirectory "README.md"), ($readme.Trim() + [Environment]::NewLine), [System.Text.UTF8Encoding]::new($false))

$checksumFiles = Get-ChildItem -LiteralPath $resolvedOutputDirectory -File | Where-Object { $_.Name -ne "CHECKSUMS.sha256" } | Sort-Object Name
$checksumLines = foreach ($file in $checksumFiles) {
    $hash = (Get-FileHash -Algorithm SHA256 -LiteralPath $file.FullName).Hash.ToLowerInvariant()
    "$hash  $($file.Name)"
}
[System.IO.File]::WriteAllText((Join-Path $resolvedOutputDirectory "CHECKSUMS.sha256"), (($checksumLines -join "`n") + "`n"), [System.Text.UTF8Encoding]::new($false))

[ordered]@{
    output_directory = $resolvedOutputDirectory
    package_id = $package.package_id
    package_digest_sha256 = $build.package_digest_sha256
    readiness = $confidence.readiness
    local_preflight_passed = $validation.local_preflight_passed
    release_gate = $gate.release_gate
} | ConvertTo-Json -Depth 5
