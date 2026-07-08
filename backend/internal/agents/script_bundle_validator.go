package agents

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
	"time"

	"cascade-demoops/backend/internal/model"
)

type ScriptBundleValidator struct{}

func NewScriptBundleValidator() *ScriptBundleValidator { return &ScriptBundleValidator{} }

func (v *ScriptBundleValidator) ValidateBundle(bundle *model.ExecutableRecordingScriptBundle) model.ExecutableScriptValidation {
	findings := []model.AgentFinding{}
	if bundle == nil {
		return model.ExecutableScriptValidation{
			Valid:       false,
			Findings:    []model.AgentFinding{scriptValidationFinding("bundle_nil", model.FindingSeverityBlocking, "脚本包为空。")},
			ValidatedAt: time.Now().UTC(),
		}
	}
	source := bundle.PlaywrightScript.InlineSource
	if source == "" {
		findings = append(findings, scriptValidationFinding("script_missing", model.FindingSeverityBlocking, "可执行 TypeScript 脚本为空或未内联。"))
	}
	if bundle.PlanJSON == nil {
		findings = append(findings, scriptValidationFinding("plan_missing", model.FindingSeverityBlocking, "脚本包缺少 JSON 执行计划。"))
	}
	findings = append(findings, validateManifest(bundle)...)
	findings = append(findings, validateForbiddenScriptTokens(source, bundle.SecurityPolicy)...)
	findings = append(findings, validateAllowedContextUsage(source, bundle.SecurityPolicy)...)
	findings = append(findings, validateRawSecretPatterns(source)...)
	findings = append(findings, validatePlanScriptBinding(bundle, source)...)
	findings = append(findings, validateScriptDomainPolicy(bundle, source)...)
	valid := true
	for _, finding := range findings {
		if finding.Severity == model.FindingSeverityBlocking {
			valid = false
			break
		}
	}
	return model.ExecutableScriptValidation{Valid: valid, Findings: findings, ValidatedAt: time.Now().UTC()}
}

func validateManifest(bundle *model.ExecutableRecordingScriptBundle) []model.AgentFinding {
	findings := []model.AgentFinding{}
	manifest := bundle.ScriptManifest
	if manifest.EntryFunction != "runCascadeRecording" {
		findings = append(findings, scriptValidationFinding("entry_function_mismatch", model.FindingSeverityBlocking, "脚本入口函数必须是 runCascadeRecording。"))
	}
	if manifest.Language != "typescript" {
		findings = append(findings, scriptValidationFinding("language_mismatch", model.FindingSeverityBlocking, "脚本语言必须是 TypeScript。"))
	}
	if manifest.Runtime != "playwright-restricted-sandbox" {
		findings = append(findings, scriptValidationFinding("runtime_mismatch", model.FindingSeverityBlocking, "脚本运行时必须是受限 Playwright 沙箱。"))
	}
	if !strings.Contains(bundle.PlaywrightScript.InlineSource, "runCascadeRecording(ctx: CascadeRecordingContext): Promise<CascadeRecordingResult>") {
		findings = append(findings, scriptValidationFinding("entry_signature_missing", model.FindingSeverityBlocking, "脚本缺少固定导出函数签名。"))
	}
	if len(manifest.DependencyAllowlist) > 0 {
		findings = append(findings, scriptValidationFinding("dependency_allowlist_not_empty", model.FindingSeverityBlocking, "v1 可执行脚本不允许声明外部依赖。"))
	}
	return findings
}

func validateForbiddenScriptTokens(source string, policy model.ExecutableScriptSecurityPolicy) []model.AgentFinding {
	findings := []model.AgentFinding{}
	for _, token := range policy.ForbiddenIdentifiers {
		if token == "" {
			continue
		}
		if scriptHasIdentifier(source, token) {
			findings = append(findings, scriptValidationFinding("forbidden_identifier_"+safeID("", token), model.FindingSeverityBlocking, "脚本包含禁止标识符："+token))
		}
	}
	for _, token := range policy.ForbiddenImports {
		if token == "" {
			continue
		}
		if strings.Contains(source, "\""+token+"\"") || strings.Contains(source, "'"+token+"'") {
			findings = append(findings, scriptValidationFinding("forbidden_import_"+safeID("", token), model.FindingSeverityBlocking, "脚本包含禁止导入："+token))
		}
	}
	return findings
}

func validateAllowedContextUsage(source string, policy model.ExecutableScriptSecurityPolicy) []model.AgentFinding {
	findings := []model.AgentFinding{}
	allowedRoots := map[string]bool{}
	for _, api := range policy.AllowedContextAPIs {
		root := strings.TrimPrefix(strings.TrimSpace(api), "ctx.")
		if root != "" {
			allowedRoots[root] = true
		}
	}
	if len(allowedRoots) == 0 {
		for _, root := range []string{"page", "secrets", "capture", "assert", "log"} {
			allowedRoots[root] = true
		}
	}
	for _, match := range regexp.MustCompile(`ctx\.([A-Za-z_][A-Za-z0-9_]*)`).FindAllStringSubmatch(source, -1) {
		if len(match) < 2 {
			continue
		}
		if !allowedRoots[match[1]] {
			findings = append(findings, scriptValidationFinding("ctx_api_forbidden_"+match[1], model.FindingSeverityBlocking, "脚本调用了未授权的 ctx API："+match[1]))
		}
	}
	allowedPageMethods := map[string]bool{}
	for _, method := range policy.AllowedPageMethods {
		if method != "" {
			allowedPageMethods[method] = true
		}
	}
	if len(allowedPageMethods) == 0 {
		for _, method := range []string{"goto", "click", "fill", "selectOption", "setInputFiles", "waitForTimeout", "waitForLoadState", "locator"} {
			allowedPageMethods[method] = true
		}
	}
	for _, match := range regexp.MustCompile(`ctx\.page\.([A-Za-z_][A-Za-z0-9_]*)`).FindAllStringSubmatch(source, -1) {
		if len(match) < 2 {
			continue
		}
		if !allowedPageMethods[match[1]] {
			findings = append(findings, scriptValidationFinding("page_method_forbidden_"+match[1], model.FindingSeverityBlocking, "脚本调用了未授权的 ctx.page 方法："+match[1]))
		}
	}
	return findings
}

func validateRawSecretPatterns(source string) []model.AgentFinding {
	patterns := []struct {
		id      string
		pattern string
		summary string
	}{
		{"private_key_literal", `BEGIN\s+(RSA\s+|EC\s+|OPENSSH\s+)?PRIVATE\s+KEY`, "脚本中疑似出现私钥内容。"},
		{"bearer_token_literal", `(?i)bearer\s+[a-z0-9._\-]{12,}`, "脚本中疑似出现 Bearer token。"},
		{"api_key_assignment", `(?i)(password|passwd|api[_-]?key|private[_-]?key|token)\s*[:=]\s*["'][^"']{6,}["']`, "脚本中疑似出现明文密码、token 或 API key。"},
		{"openai_like_key", `sk-[A-Za-z0-9]{12,}`, "脚本中疑似出现 API secret key。"},
	}
	findings := []model.AgentFinding{}
	for _, item := range patterns {
		if regexp.MustCompile(item.pattern).FindStringIndex(source) != nil {
			findings = append(findings, scriptValidationFinding(item.id, model.FindingSeverityBlocking, item.summary))
		}
	}
	return findings
}

func validatePlanScriptBinding(bundle *model.ExecutableRecordingScriptBundle, source string) []model.AgentFinding {
	findings := []model.AgentFinding{}
	if bundle.PlanJSON == nil {
		return findings
	}
	planHash, err := bundle.PlanJSON.ComputeScriptHash()
	if err != nil {
		findings = append(findings, scriptValidationFinding("plan_hash_error", model.FindingSeverityBlocking, err.Error()))
	} else if bundle.Reproducibility.PlanHashSHA256 != "" && bundle.Reproducibility.PlanHashSHA256 != planHash {
		findings = append(findings, scriptValidationFinding("plan_hash_mismatch", model.FindingSeverityBlocking, "脚本包 plan hash 与 JSON 执行计划不一致。"))
	}
	scriptHash := model.SHA256Hex([]byte(source))
	if bundle.PlaywrightScript.SHA256 != "" && bundle.PlaywrightScript.SHA256 != scriptHash {
		findings = append(findings, scriptValidationFinding("script_source_hash_mismatch", model.FindingSeverityBlocking, "脚本 source hash 与内联 TypeScript 内容不一致。"))
	}
	if bundle.Reproducibility.ScriptHashSHA256 != "" && bundle.Reproducibility.ScriptHashSHA256 != scriptHash {
		findings = append(findings, scriptValidationFinding("script_hash_mismatch", model.FindingSeverityBlocking, "脚本包 script hash 与 TypeScript 内容不一致。"))
	}
	if bundle.Reproducibility.BundleHashSHA256 != "" {
		hash, err := bundle.ComputeBundleHash()
		if err != nil {
			findings = append(findings, scriptValidationFinding("bundle_hash_error", model.FindingSeverityBlocking, err.Error()))
		} else if hash != bundle.Reproducibility.BundleHashSHA256 {
			findings = append(findings, scriptValidationFinding("bundle_hash_mismatch", model.FindingSeverityBlocking, "脚本包 bundle hash 与当前内容不一致。"))
		}
	}
	seen := map[string]bool{}
	for _, step := range bundle.PlanJSON.Steps {
		if step.NodeID == "" {
			findings = append(findings, scriptValidationFinding("step_node_missing", model.FindingSeverityBlocking, "JSON 执行计划存在空 node_id。"))
			continue
		}
		if seen[step.NodeID] {
			findings = append(findings, scriptValidationFinding("step_node_duplicate_"+step.NodeID, model.FindingSeverityBlocking, "JSON 执行计划存在重复 node_id："+step.NodeID))
		}
		seen[step.NodeID] = true
		if !strings.Contains(source, `"`+step.NodeID+`"`) {
			findings = append(findings, scriptValidationFinding("step_node_missing_in_script_"+step.NodeID, model.FindingSeverityBlocking, "TypeScript 脚本缺少对应 graph node："+step.NodeID))
		}
	}
	for _, nodeID := range bundle.ScriptManifest.StepNodeIDs {
		if !seen[nodeID] {
			findings = append(findings, scriptValidationFinding("manifest_node_unknown_"+nodeID, model.FindingSeverityBlocking, "manifest 中存在 JSON 执行计划没有的 node_id："+nodeID))
		}
	}
	return findings
}

func validateScriptDomainPolicy(bundle *model.ExecutableRecordingScriptBundle, source string) []model.AgentFinding {
	findings := []model.AgentFinding{}
	allowed := bundle.SecurityPolicy.AllowedDomains
	if len(allowed) == 0 && bundle.PlanJSON != nil {
		allowed = bundle.PlanJSON.RecordingRunSpec.AllowedDomains
	}
	for _, literalURL := range extractGotoURLs(source) {
		parsed, err := url.Parse(literalURL)
		if err != nil || parsed.Host == "" {
			findings = append(findings, scriptValidationFinding("url_parse_failed", model.FindingSeverityBlocking, "脚本中存在无法解析的跳转 URL："+literalURL))
			continue
		}
		if !hostAllowed(parsed.Host, allowed) {
			findings = append(findings, scriptValidationFinding("url_domain_forbidden", model.FindingSeverityBlocking, "脚本跳转 URL 不在 allowed domains 内："+literalURL))
		}
		for _, forbidden := range bundle.SecurityPolicy.ForbiddenPages {
			if forbidden != "" && strings.Contains(parsed.Path, forbidden) {
				findings = append(findings, scriptValidationFinding("forbidden_page_navigation", model.FindingSeverityBlocking, "脚本尝试访问禁止页面："+literalURL))
			}
		}
	}
	return findings
}

func extractGotoURLs(source string) []string {
	matches := regexp.MustCompile(`ctx\.page\.goto\("([^"]+)"`).FindAllStringSubmatch(source, -1)
	urls := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) > 1 {
			urls = append(urls, match[1])
		}
	}
	return urls
}

func scriptHasIdentifier(source string, token string) bool {
	if token == "import" {
		return regexp.MustCompile(`(?m)^\s*import\s`).FindStringIndex(source) != nil
	}
	return regexp.MustCompile(`\b`+regexp.QuoteMeta(token)+`\b`).FindStringIndex(source) != nil
}

func hostAllowed(host string, allowed []string) bool {
	if len(allowed) == 0 {
		return false
	}
	host = strings.ToLower(host)
	for _, domain := range allowed {
		domain = strings.ToLower(strings.TrimSpace(domain))
		if domain == "" {
			continue
		}
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	return false
}

func scriptValidationFinding(id string, severity model.FindingSeverity, summary string) model.AgentFinding {
	id = strings.Trim(id, "_")
	if id == "" {
		id = "script_validation"
	}
	return model.AgentFinding{
		ID:       "script_validation_" + id,
		Kind:     "script_bundle_validation",
		Severity: severity,
		Summary:  summary,
	}
}

func ensureValidBundle(bundle *model.ExecutableRecordingScriptBundle) error {
	validation := NewScriptBundleValidator().ValidateBundle(bundle)
	if validation.Valid {
		return nil
	}
	summaries := make([]string, 0, len(validation.Findings))
	for _, finding := range validation.Findings {
		if finding.Severity == model.FindingSeverityBlocking {
			summaries = append(summaries, finding.Summary)
		}
	}
	if len(summaries) == 0 {
		return errors.New("script bundle validation failed")
	}
	return errors.New(strings.Join(summaries, "; "))
}
