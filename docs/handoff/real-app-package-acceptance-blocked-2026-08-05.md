# 真实 App 包验收阻塞报告（测试端 A）

日期：2026-08-05
报告人：万明达（Validation Agent）
主送：孟洋
抄送：徐一凯
依据文档：`App到Server成片双端独立验收说明-v1.md`、`Cascade被测产品测试包-2026-08-03/统一测试任务.md`
报告类型：**真实 App 包验收**（非固定包验收）
结论：**阻塞** —— 阻塞点在 App 执行包生成阶段，Validation Agent 与 Server 执行链路本轮未被触达

## 1. 结论摘要

测试端 A 已完成被测产品搭建、App 项目创建和执行包生成，但执行包**未能通过 App 侧自身的确信度门禁**（`readiness = blocked`），因此无法进入审批和正式 Exchange 上传，后续 Server 执行、录屏取证、MP4 渲染和 ACK 环节本轮均未执行。

按验收说明第 12 节责任定位表，本次首个失败阶段为 **App 输入收集 / Outline 生成**，建议责任归属 **App / Outline 生成侧**。

依据验收说明第 6.2 节"如缺少必填字段、目标控件证据或完成判定，应停止审批，保留 App 提示和生成日志。本轮不得手工改 JSON 绕过校验"，本报告不对执行包做任何手工修补，如实记录阻塞。

## 2. 环境表（测试端 A，已脱敏）

| 变量 | 测试端 A 实际值 |
|---|---|
| Git commit | `d2d10b5`（已 merge origin/main，含 PR #35 之前全部提交） |
| 操作系统 | macOS（darwin），非说明文档假设的 Windows PowerShell |
| `<APP_BASE_URL>` | `http://127.0.0.1:3000`（Vite dev server） |
| `<SERVER_BASE_URL>` | `http://127.0.0.1:4317`（dev bridge） |
| `<TARGET_PRODUCT_URL>` | `http://127.0.0.1:5100/app` |
| `<LOCAL_SOURCE_DIR>` | `/tmp/cascade_test_product` |
| `<ORG_ID>` | `org_desktop` |
| 数据库 | 本机 PostgreSQL 16（Homebrew），独立库 `cascade_test_product`、独立角色 `cascade_test` |
| 端口调整说明 | 被测产品未用文档建议的 5000（该端口被 macOS ControlCenter 占用），改用 **5100**；`.env` 的 `PORT`、`APP_BASE_URL` 与 App 中填写的目标 URL 已同步一致 |
| `CASCADE_ARK_MEDIA_MODE` | `dry_run`（未进入 real 模式） |
| 模型 Key | 使用测试包内自带 Key，实测有效（被测产品"构建"功能可正常调用模型返回结果） |

被测产品环境准备已按《Cascade被测产品测试包使用说明.md》第 6 节完成人工检查：页面可打开、可登录、可见"新建项目"、手动建项目后"构建"功能能正常调用模型、无遗留未完成构建任务。

补充说明：测试包不含现存账号，且产品四种注册方式（邮箱 / 手机号 / GitHub / 微信）在本地离线环境下均依赖未配置的第三方服务（`GitHub OAuth not configured`、短信服务缺失导致 `Invalid phone`），产品自身的用户名注册路由已主动关闭（返回 403，提示只允许 email/phone/GitHub 注册）。测试端 A 通过在本地空库中直接插入一条带 bcrypt 口令的测试账号记录完成登录，账号仅存在于本机测试库，口令未写入执行包、日志、字幕或模型上下文。

## 3. 已执行到的环节

| 环节 | 结果 |
|---|---|
| 被测 Cascade 产品搭建与启动 | 通过 |
| 被测产品登录与"构建"功能人工验证 | 通过 |
| App 项目创建（真实产品分析链路） | 通过 |
| App 生成 3 阶段执行方案 | 通过（`current_node = HumanApprove`, `status = awaiting_human_approval`） |
| 执行服务器连接（Exchange 握手） | 通过（`127.0.0.1:4317`，返回 `demoops.exchange_bootstrap.v1`） |
| 四项人工审批确认 | 通过（录制范围、上传内容、禁止策略、目标环境许可均已勾选） |
| Client Execution Package 构建 | 通过（产出包体与两个哈希） |
| **执行包确信度门禁** | **阻塞** |
| 本地审批通过 | 未执行（被上一步阻塞） |
| 正式 Exchange 上传 | 未执行 |
| Server 接收 / 路由 / 执行 | 未执行 |
| StepResult / 阶段事件 / ValidationReport | 未产生 |
| 录屏 / trace / MP4 / 编辑器素材 | 未产生 |
| App 下载校验 / 交付 ACK | 未执行 |

## 4. 可追溯标识

| 项目 | 值 |
|---|---|
| `project_id` | `proj_1785932366319105000`（API 创建）；`proj_1785933490652339000`（UI 创建，二次复现，见第 5.3 节） |
| `package_id` | `pkg_bundle_script_graph_1785932366615215000` |
| `approval_subject_digest_sha256` | `d16a84ac4d92c42642a68c311f276f0b781b76bf89057acec575019db6fa494f` |
| `package_digest_sha256` | `48a7b807c15a64014a5e85603698a3286ceda5ebe6ec93c0a827a90f0f25cb3f` |
| `confidence.assessment_hash` | `4d9f303568293e5bdb39c7d7d40d307659bcb2fe24d88137db40bbb4236e7d8b` |
| `confidence.algorithm_version` | `demoops.package_confidence.v1` |
| `build_status` | `draft` |
| `readiness` | `blocked` |
| exchange package ID | 无（未上传） |
| result package ID | 无（未执行） |

## 5. 阻塞原因与根因定位

### 5.1 门禁输出的三条阻塞原因

```text
business_stage_new_project_entry: 安全域、来源或非破坏性约束不完整
business_stage_start_agent_build: 安全域、来源或非破坏性约束不完整
关键需求未完整映射到 stage 和证据
```

确信度明细：

| 指标 | 值 |
|---|---|
| `overall_score` | 0.751 |
| `requirement_coverage` | **0** |
| `deterministic_validation_coverage` | 1 |
| `runtime_page_evidence_coverage` | 1 |
| `selector_quality` | 0.86 |
| `source_binding_status` | `unverified` |
| `source_binding_mode` | `page_only` |

分阶段得分：

| node_id | overall | business_intent | result_validation |
|---|---|---|---|
| `business_stage_new_project_entry` | 0.751 | 1 | 1 |
| `business_stage_start_agent_build` | 0.751 | 1 | 1 |
| `business_stage_final_observe` | 0.905 | 1 | 1 |

### 5.2 根因：click 步骤缺少 `non_destructive` 标记

判定逻辑位于 `backend/internal/model/package_confidence.go:348-359`：

```go
func confidenceSafetyScore(pkg *ClientExecutionPackage, step ScriptStep, stage StageApprovalStage) float64 {
	if len(pkg.RecordingRunSpec.AllowedDomains) == 0 || stage.TargetContract == nil || stage.TargetContract.SemanticID == "" {
		return 0
	}
	if step.Action.Type != GraphActionNavigate && step.Action.Type != GraphActionInspect && !step.NonDestructive {
		return 0
	}
	if pkg.SourceBindingSummary != nil && pkg.SourceBindingSummary.EffectiveMode == ProductSourceModeBlocked {
		return 0
	}
	return 1
}
```

对本次实际执行包逐条核验三个子条件：

| 子条件 | 实际值 | 是否满足 |
|---|---|---|
| `RecordingRunSpec.AllowedDomains` 非空 | `["127.0.0.1:5100"]` | 满足 |
| `stage.TargetContract.SemanticID` 非空 | 三个阶段分别为 `semantic_dbfb241fde8180e4` / `semantic_ffaf433347168880` / `semantic_375388e22849f625` | 满足 |
| `SourceBindingSummary.EffectiveMode` 不为 blocked | `page_only` | 满足 |
| **action 类型豁免或 `NonDestructive == true`** | 见下表 | **两个阶段不满足** |

各步骤 action 与 `non_destructive` 实测值：

| node_id | action type | `non_destructive` | safety score |
|---|---|---|---|
| `business_stage_new_project_entry` | `click` | **null** | 0 |
| `business_stage_start_agent_build` | `click` | **null** | 0 |
| `business_stage_final_observe` | `inspect` | `true` | 1（且因 action 类型即可豁免） |

**结论**：App 的 outline 生成器为 `click` 类型步骤输出的 `non_destructive` 字段为 null 而非 `true`。由于 `click` 既不属于 `GraphActionNavigate` 也不属于 `GraphActionInspect`，第 352 行的 `!step.NonDestructive` 成立，安全得分归零，进而触发"安全域、来源或非破坏性约束不完整"。第三个阶段是 `inspect`，靠 action 类型直接豁免，因此得分 0.905 未被阻塞——这一对比进一步印证根因就在 click 步骤的 `non_destructive` 标记缺失，而非安全域或来源绑定配置本身有问题。

`requirement_coverage = 0` 是第二条独立问题：本次演示目标（贪吃蛇、可自定义颜色、三种难度）未被映射到任何 stage 的需求覆盖统计中，触发 `package_confidence.go:161` 的"关键需求未完整映射到 stage 和证据"。

### 5.3 二次复现验证

在前端缺陷修复后（见《App 前端缺陷报告》），测试端 A 通过**完整 UI 流程**重新创建了一个项目 `proj_1785933490652339000`（项目名"贪吃蛇演示"），完成了 configuration 确认、项目来源选择、方案生成、执行服务器连接（`127.0.0.1:4317` 握手成功）和全部四项人工确认，界面推进到"审批上传"步骤。

该项目的确信度门禁输出与首次通过 API 创建的项目**完全一致**：

```text
readiness: blocked | overall_score: 0.751
requirement_coverage: 0 | selector_quality: 0.86

blocking_reasons:
  - business_stage_new_project_entry: 安全域、来源或非破坏性约束不完整
  - business_stage_start_agent_build: 安全域、来源或非破坏性约束不完整
  - 关键需求未完整映射到 stage 和证据
```

分阶段 `safety_source_score` 直接印证第 5.2 节的根因判断：

| node_id | action | `non_destructive` | `safety_source_score` | overall |
|---|---|---|---|---|
| `business_stage_new_project_entry` | `click` | `None` | **0** | 0.751 |
| `business_stage_start_agent_build` | `click` | `None` | **0** | 0.751 |
| `business_stage_final_observe` | `inspect` | `True` | **1** | 0.905 |

两个 `click` 阶段安全分为 0、`inspect` 阶段为 1 的对比，排除了安全域配置（`allowed_domains = ["127.0.0.1", "127.0.0.1:5100"]`，非空）和来源绑定（`source_binding = page_only`，非 blocked）作为原因的可能，确认根因唯一指向 `click` 步骤缺失 `non_destructive: true`。

结论：该阻塞可在 UI 流程与 API 流程下稳定复现，不依赖特定的项目创建路径。

### 5.4 UI 提示文案与后端门禁的区分

审批界面在"上传前必须完成人工审批"下方显示四条提示：

```text
业务动作缺少稳定且已验证的 selector，上传前应补充页面扫描、截图标注或 data-testid/role/name 证据。
当前执行图没有 click/fill/select/upload/api_call 等已验证真实业务动作，不能自动上传录制。
需要复核打码选择器和禁止访问数据。
需要确认仅上传代码结构摘要，不上传完整源码。
```

这四条属于前端的人工审批提示清单，与后端 `confidence_summary.blocking_reasons` 是两套独立检查。使"审批当前版本并上传"按钮不可点击的是后端 `readiness = blocked`；即便四项人工确认全部勾选、执行服务器已连接，按钮仍不可用。排查时需以后端 `blocking_reasons` 为准，前端文案不指向真正的阻塞点。

### 5.3 与统一测试任务的业务偏差（另一独立问题）

App 自动生成的方案为 **3 个阶段**：

```text
business_stage_new_project_entry  进入新建项目流程      click
business_stage_start_agent_build  启动 agent 实际构建   click
business_stage_final_observe      收束并观察最终状态    inspect
```

而《统一测试任务.md》要求 **5 步**：点"新建项目" → 输入"贪吃蛇游戏" → 点"构建" → 输入详细游戏需求 → 等待构建完成。

缺失"输入项目主题"和"补充游戏要求"两个 `fill` 类型的业务输入步骤，且五条中文字幕文案均未出现在方案中。这与 `requirement_coverage = 0` 相互印证：关键业务需求未进入 stage 计划。

本报告不对此做手工修补，原因同第 1 节所述（验收说明禁止手改 JSON 绕过校验）。

## 6. 责任定位与建议

按验收说明第 12 节：

| 失败表现 | 首要定位阶段 | 建议责任归属 |
|---|---|---|
| App 无法形成完整执行包 | App 输入收集 / Outline 生成 | App / Outline 生成侧 |

建议修复方向（供 App / Outline 生成侧参考，不代为实现）：

1. **outline 生成器为非破坏性的 `click` / `fill` 步骤显式写入 `non_destructive: true`**。当前仅 `inspect` 步骤带该标记，而 `confidenceSafetyScore` 只对 `navigate` 和 `inspect` 两种 action 类型豁免，其余类型强制要求该字段为 true。
2. **将用户演示目标中的关键需求映射进 stage 计划与证据引用**，使 `requirement_coverage` 达到 1。
3. **补齐统一测试任务要求的 5 个业务步骤及中文字幕**，特别是两个 `fill` 类型输入步骤。

## 7. Validation Agent 侧状态说明

本轮阻塞发生在执行包生成阶段，Validation Agent 的三个方法（`ValidateBeforeExecution` / `ValidateStageEvents` / `ValidatePostExecution`）**本轮未被调用**——执行包未通过门禁、未上传、Server 未执行，因此没有真实阶段事件和结果包可供验证。

按《Validation-Agent新架构对接任务说明》第 12 节推荐执行顺序，第 1–4 步（固定验收）已全部完成：

| 项 | 状态 | 提交 |
|---|---|---|
| OutcomeVerifier 三阶段实现（P0） | 已完成，已并入 main | PR #28 |
| 受限修复提案身份与策略门禁（P1） | 已完成，已并入 main | PR #30（`d22b6a8`） |
| 四个受控 outline 场景包与端到端固定验收（P2） | 已完成，已并入 main | PR #30（`2a5470a`） |
| 场景 6：跨域访问 / 进入禁止页面 | 已完成，待并入 main | `ccd78d0`、`d906d3f` |
| 场景 11：结果包身份与哈希不一致 | 已完成，待并入 main | `a223756` |

前三项已随 PR #28 / #30 并入 `origin/main`；场景 6 与场景 11 的提交目前位于 `feat/browser-agent-outcome-verifier-adapter` 分支，尚未合入 main。

`internal/orchestrator`、`internal/model`、`internal/media` 全套测试通过，`go build ./...` 通过。

第 5 步"使用 App 正式执行包进行一次真实端到端联调"需待本报告第 6 节的 App 侧问题修复后重试。依据同文档第 8 节"若 App 包本身不满足协议，应返回结构化问题，不得在 Validation Agent 内偷偷补写业务字段"，本轮不在 Validation Agent 内补写任何业务字段。

## 8. 双端结果表（测试端 A 列）

| 项目 | 测试端 A |
|---|---|
| Git commit | `d2d10b5` |
| App / Server / 目标产品地址 | `127.0.0.1:3000` / `127.0.0.1:4317` / `127.0.0.1:5100` |
| `org_id` | `org_desktop` |
| exchange package ID | 无（未上传） |
| `origin=app_formal_exchange` | 未达到 |
| 运行时为新路径 | 未达到 |
| StepResult 完整 | 未达到 |
| 严格证据完整 | 未达到 |
| MP4 可播放 | 未达到 |
| 内容来自真实目标页面 | 未达到 |
| 编辑器素材已落地 | 未达到 |
| App 下载并校验 | 未达到 |
| ACK 完成 | 未达到 |
| 总耗时 | 环境搭建约 3 小时；包生成至阻塞约 5 分钟 |
| 首个失败阶段 | **App Outline 生成 —— 执行包确信度门禁 `readiness=blocked`** |
| 错误码 / trace ID | `assessment_hash=4d9f3035…`；阻塞原因见第 5.1 节 |
| 产物目录 | 无最终产物；包体与确信度报告可由 `project_id` 复现 |
| 最终结论 | **阻塞** |

## 9. 复现步骤

```bash
# 1. 启动被测产品（PORT=5100，独立 PostgreSQL）
cd /tmp/cascade_test_product && npm run dev:start

# 2. 启动 App 与 dev bridge
cd cascade_demo_ops && pnpm dev:bridge      # 127.0.0.1:4317
cd cascade_demo_ops/frontend/web && npx vite --host 127.0.0.1 --port 3000 --strictPort

# 3. 创建项目（注：本轮因前端配置界面缺陷改用 API，详见前端缺陷报告）
curl -X POST http://127.0.0.1:4317/v1/desktop/projects \
  -H "Content-Type: application/json" -d '{
    "mode":"desktop",
    "product_url":"http://127.0.0.1:5100/app",
    "local_repo_path":"/tmp/cascade_test_product",
    "target_audience":"产品团队",
    "target_duration_sec":60,
    "product_description":"完整演示从创建项目到生成一个可自定义界面颜色、支持三种难度模式的贪吃蛇游戏，并生成带逐步中文说明字幕的演示视频。"
  }'

# 4. 构建执行包并观察确信度门禁
curl -X POST http://127.0.0.1:4317/v1/desktop/projects/<project_id>/client-execution-package \
  -H "Content-Type: application/json" -d '{"org_id":"org_desktop"}'
# 观察 package.confidence_summary.readiness 与 blocking_reasons
```
