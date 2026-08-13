package model

// validationCheckMeta holds the structured feedback fields for a known failure
// code. These are filled in by AnnotateValidationChecks so every check carries
// machine-readable impact, actionable suggestion, and a responsibility domain
// that helps developers route the issue to the correct team.
type validationCheckMeta struct {
	Impact               string
	Suggestion           string
	NextStep             string
	ResponsibilityDomain ValidationCheckDomain
}

// validationCheckMetaTable maps each stable failure code to its structured
// feedback. Codes not in the table are left unmodified by AnnotateValidationChecks.
var validationCheckMetaTable = map[string]validationCheckMeta{
	// ── pre_execution ──────────────────────────────────────────────────────────
	"MISSING_BUNDLE_HASH": {
		Impact:               "无法验证执行包完整性，阻止执行继续。",
		Suggestion:           "确认 App 产包流程在审批时写入 source_bundle_hash_sha256 字段。",
		NextStep:             "由 App 侧重新生成并审批执行包，确保哈希字段完整。",
		ResponsibilityDomain: ValidationCheckDomainApp,
	},
	"MISSING_POLICY_HASH": {
		Impact:               "无法验证生效策略一致性，阻止执行继续。",
		Suggestion:           "确认 App 产包流程在审批时写入 effective_policy_hash_sha256 字段。",
		NextStep:             "由 App 侧重新生成并审批执行包，确保策略哈希字段完整。",
		ResponsibilityDomain: ValidationCheckDomainApp,
	},
	"EMPTY_STAGE_APPROVAL_PLAN": {
		Impact:               "没有可执行的 stage，Server 无法启动 Browser Agent。",
		Suggestion:           "App 产包时至少应包含一个有效 stage；检查 StageApprovalPlan 与 ScriptOutline 是否正确生成。",
		NextStep:             "由 App 侧重新生成执行包，确认 stage_approval_plan.stages 非空。",
		ResponsibilityDomain: ValidationCheckDomainApp,
	},
	"MISSING_SCRIPT_OUTLINE": {
		Impact:               "缺少 ScriptOutline，Server 无法进行安全边界校验和阶段调度。",
		Suggestion:           "App 产包时必须生成完整 script_outline 字段。",
		NextStep:             "由 App 侧重新生成执行包，确认 script_outline 字段存在且完整。",
		ResponsibilityDomain: ValidationCheckDomainApp,
	},

	// ── runtime_stage ──────────────────────────────────────────────────────────
	"OUT_OF_ORDER_EVENTS": {
		Impact:               "阶段事件顺序异常，执行结果不可信，阻止继续。",
		Suggestion:           "检查 Browser Agent 或 Stage Orchestrator 的事件发送逻辑，确保 stage_started 先于 stage_completed/stage_failed。",
		NextStep:             "由 Server Runtime 侧排查事件发送顺序，修复后重新执行。",
		ResponsibilityDomain: ValidationCheckDomainServer,
	},
	"DUPLICATE_STAGE_STARTED": {
		Impact:               "同一阶段多次启动，可能导致重复执行和证据污染。",
		Suggestion:           "检查重试逻辑是否在前一次执行未终止时重新发送了 stage_started 事件。",
		NextStep:             "由 Server Runtime 侧排查重复启动根因，确保每个阶段只发送一次 stage_started。",
		ResponsibilityDomain: ValidationCheckDomainServer,
	},
	"MISSING_OUTCOME_OBSERVED": {
		Impact:               "阶段标记完成但无真实观察证据，结果不可信，阻止继续。",
		Suggestion:           "Browser Agent 在阶段完成前必须发送 outcome_observed 事件并携带真实浏览器观察结果。",
		NextStep:             "由 Server Browser Agent 侧排查 outcome_observed 事件缺失原因。",
		ResponsibilityDomain: ValidationCheckDomainServer,
	},
	"DERIVED_FROM_PLAN_EVIDENCE": {
		Impact:               "证据来自计划推导而非真实浏览器观察，不能作为业务结论的依据，阻止继续。",
		Suggestion:           "Browser Agent 必须基于真实页面状态产生 observation，不允许将计划预期值复制为观察结果。",
		NextStep:             "由 Server Browser Agent 侧修复，确保 observation.source 为 actual_browser_observation 或同等真实来源。",
		ResponsibilityDomain: ValidationCheckDomainServer,
	},
	"NO_OBSERVATION_EVIDENCE": {
		Impact:               "关键事件缺少 observation 字段，无法判断阶段真实结果，阻止继续。",
		Suggestion:           "outcome_observed 和 observation_collected 事件必须携带非空的 observation 对象。",
		NextStep:             "由 Server Browser Agent 侧排查 observation 写入逻辑。",
		ResponsibilityDomain: ValidationCheckDomainServer,
	},
	"REQUIRED_ASSERTION_FAILED": {
		Impact:               "必填业务断言未通过，业务目标未达成，执行停止。",
		Suggestion:           "检查目标页面是否出现了执行包声明的业务结果；若页面行为与预期不符，需由 App 重新定义成功判定或由产品修复页面行为。",
		NextStep:             "确认被测产品的实际行为是否符合执行包的业务成功定义；如不符，由 App 侧更新执行包。",
		ResponsibilityDomain: ValidationCheckDomainApp,
	},
	"STAGE_FAILED": {
		Impact:               "阶段执行失败，后续依赖阶段无法继续，执行停止。",
		Suggestion:           "查看 failure_diagnostic 中的 error.code 和截图/trace 定位具体原因（selector 未解析、导航失败、超时等）。",
		NextStep:             "根据 failure_diagnostic.error.code 和 blocked_reasons 定位责任方，修复后使用新执行包或 waiver 重试。",
		ResponsibilityDomain: ValidationCheckDomainServer,
	},
	"CROSS_DOMAIN_ACCESS": {
		Impact:               "Browser Agent 访问了执行包未批准的域名，安全边界被突破，执行停止。",
		Suggestion:           "检查执行包的 allowed_domains 配置是否覆盖了所有需要访问的目标域名；若页面有跨域跳转，需由 App 在审批时明确许可。",
		NextStep:             "由 App 侧更新 recording_run_spec.allowed_domains 或 security_policy.allowed_domains，覆盖实际访问的域名。",
		ResponsibilityDomain: ValidationCheckDomainApp,
	},
	"FORBIDDEN_PAGE_ACCESS": {
		Impact:               "Browser Agent 访问了执行包声明的禁止路径，安全边界被突破，执行停止。",
		Suggestion:           "检查 script_outline.allowed_exploration_scope.forbidden_path_prefixes 配置与实际导航路径是否匹配；若导航是合法业务流程，需由 App 调整禁止路径范围。",
		NextStep:             "由 App 侧重新审视 forbidden_path_prefixes 与业务流程的兼容性，更新执行包。",
		ResponsibilityDomain: ValidationCheckDomainApp,
	},

	// ── post_execution ─────────────────────────────────────────────────────────
	"MISSING_RESULT_PACKAGE": {
		Impact:               "结果包不存在，无法验证执行结论，禁止交付。",
		Suggestion:           "检查 Server 执行管线是否正常完成了 result_packaging 阶段；result_id 不能为空。",
		NextStep:             "由 Server Runtime 侧排查结果包生成逻辑。",
		ResponsibilityDomain: ValidationCheckDomainServer,
	},
	"RESULT_PACKAGE_MISMATCH": {
		Impact:               "结果包来源与本次批准运行不一致，证据链不可信，禁止交付。",
		Suggestion:           "不得将其他运行产生的结果包关联到本次执行；结果包的 source_package_id 必须与批准包 ID 完全一致。",
		NextStep:             "由 Server 重新执行该执行包，使用正确的原始包生成结果。",
		ResponsibilityDomain: ValidationCheckDomainServer,
	},
	"RESULT_HASH_MISMATCH": {
		Impact:               "结果包审计哈希与原始执行包哈希不一致，证据链不可信，禁止交付。",
		Suggestion:           "结果包生成后不得修改原始包或其哈希；若哈希不一致，说明执行链路中存在未授权修改。",
		NextStep:             "由 Server 重新执行，确保从原始未修改包启动，不得直接修改结果包哈希字段。",
		ResponsibilityDomain: ValidationCheckDomainServer,
	},
	"REQUIRED_STAGE_NOT_COMPLETED": {
		Impact:               "必需阶段未完成，业务目标未达成，执行结论无效。",
		Suggestion:           "检查该阶段是否因前序阶段失败而被跳过；若是，需先修复导致前序失败的根本原因。",
		NextStep:             "根据首个失败阶段的 failure_diagnostic 定位原因，修复后重新执行。",
		ResponsibilityDomain: ValidationCheckDomainServer,
	},
	"MISSING_EVIDENCE_REFS": {
		Impact:               "关键事件缺少证据引用，无法追溯原始产物，可追溯性受损。",
		Suggestion:           "outcome_observed 类事件应携带截图或 trace 的证据引用。",
		NextStep:             "由 Server Browser Agent 侧确保关键事件写入 evidence_refs。",
		ResponsibilityDomain: ValidationCheckDomainServer,
	},
	"MISSING_TRACE_ARTIFACT": {
		Impact:               "缺少 trace 产物，问题调试和现场还原能力受限。",
		Suggestion:           "检查 Browser Agent 是否正确启用了 Playwright trace 采集。",
		NextStep:             "由 Server Runtime 侧确认 trace 采集配置，下次执行补充产出。",
		ResponsibilityDomain: ValidationCheckDomainServer,
	},
	"MISSING_SCREENSHOTS": {
		Impact:               "缺少截图证据，人工复核和现场还原困难。",
		Suggestion:           "确认 Browser Agent 的截图采集点配置正确，执行包声明的阶段均应产出截图。",
		NextStep:             "由 Server Runtime 侧确认截图采集逻辑。",
		ResponsibilityDomain: ValidationCheckDomainServer,
	},
	"MISSING_MP4_VIDEO": {
		Impact:               "缺少最终 MP4，无法完成演示视频交付。",
		Suggestion:           "检查视频渲染管线（Video Worker + FFmpeg）是否正确完成；确认 CASCADE_FFMPEG_PATH 配置可用。",
		NextStep:             "由 Server Runtime 侧排查 FFmpeg 渲染步骤，确认 node 和 video-worker 版本匹配。",
		ResponsibilityDomain: ValidationCheckDomainEnvironment,
	},
	"MISSING_STAGE_EVENT_LOG": {
		Impact:               "缺少 stage_event_log，阶段时序无法重放，可追溯性受损。",
		Suggestion:           "确认 Browser Agent 事件日志写入器（BrowserAgentEventLog）正常落盘 JSONL 文件。",
		NextStep:             "由 Server Runtime 侧排查事件日志落盘逻辑。",
		ResponsibilityDomain: ValidationCheckDomainServer,
	},
	"STAGE_VALIDATION_FAILURE_THRESHOLD": {
		Impact:               "超过 50% 的阶段验证失败，整体质量不达标，建议重新理解产品后生成执行包。",
		Suggestion:           "当前执行包的阶段覆盖度或证据质量系统性不足，应由 App 侧重新扫描产品页面后产包。",
		NextStep:             "由 App 侧重新启动产品理解流程，生成更准确的执行包；不应在 Validation Agent 内降低阈值。",
		ResponsibilityDomain: ValidationCheckDomainApp,
	},
	"EVIDENCE_ARTIFACT_REFERENCE_BROKEN": {
		Impact:               "ValidationCheck 引用的 artifact 证据不存在，无法核验原始证据",
		Suggestion:           "检查 artifact 生成逻辑是否完整；确认 evidence_refs 的 artifact_id 引用正确",
		NextStep:             "审查 artifact 生成与引用的代码路径",
		ResponsibilityDomain: ValidationCheckDomainServer,
	},
}

// AnnotateValidationChecks fills in structured feedback fields (Impact,
// Suggestion, NextStep, ResponsibilityDomain) for each check whose Code is
// found in the metadata table. Checks with unknown codes or empty Code are
// left unmodified. The function operates in-place on the slice elements.
func AnnotateValidationChecks(checks []ValidationCheck) {
	for i := range checks {
		meta, ok := validationCheckMetaTable[checks[i].Code]
		if !ok {
			continue
		}
		checks[i].Impact = meta.Impact
		checks[i].Suggestion = meta.Suggestion
		checks[i].NextStep = meta.NextStep
		checks[i].ResponsibilityDomain = meta.ResponsibilityDomain
	}
}
