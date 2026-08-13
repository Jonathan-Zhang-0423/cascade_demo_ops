# Browser Agent Direct API v1：App 侧协议待确认事项

> 用途：确认 App 与 Server 在正式 Direct API v1 链路中的未决细节。
>
> 基准文件：
>
> - `browser-agent-direct-api-v1.md`
> - `browser-agent-direct-worklog-2026-08-08.md`
> - `browser-agent-outcome-verifier-rules-v1.md`
>
> 本文件不要求修改 Validation Agent 规则，也不要求 App 了解或控制 Server 内部 Browser Agent/Worker 实现。

## 1. 已明确、不需要重复讨论的内容

- 正式路径使用 `cascade.browser_agent_direct.v1`，不再把 Legacy Exchange 作为生产回退路径。
- App 只上传经过人工批准且 digest 一致的 `browser-agent-outline-v1` 包。
- App 负责用户意图、stage 顺序、输入语义、成功条件和安全边界。
- Server 不得修改业务意图、stage 顺序、输入内容、allowed domains、forbidden pages、secret ref 或 destructive 标记。
- selector repair 只能在同一 stage、同一 Evidence ID、同一业务目标和 App 批准候选范围内进行。
- 密码、Token、Cookie、API Key 和完整源码不得写入执行包、日志、截图、trace 或结果包。
- OutcomeVerifier 只消费脱敏 DTO、阶段事件和结果包，不读取 Playwright/page，也不修改脚本或 App 包。
- fixture、mock、test waiver 只能作为 Server 准备度证据，不能作为正式 App→Server 验收成功证据。

## 2. P0：正式 App 包与人工批准

### 2.1 批准主体 digest

请确认 App 在人工批准时冻结哪些对象，以及 Server 应校验哪些 digest：

- 整个 `ClientExecutionPackage`；
- `plan_json`；
- `stage_approval_plan`；
- `script_outline`；
- `browser_agent_contract`；
- `agent_prompt_policy`；
- `approval_markdown`。

Server 建议：批准后生成统一 `approval_subject_digest`，同时保留各子对象 hash；上传时必须全部一致。

### 2.2 批准记录字段

请确认正式包是否增加或固定以下批准元数据：

```text
approval_id
approval_subject_digest
approved_at
approved_by_installation_id
approval_schema_version
```

Server 建议：这些字段必须由 App 本地批准流程生成，Server 不接受包内简单的 `approved=true` 作为来源证明。

### 2.3 计划过期规则

请确认以下内容变化后，App 是否强制重新生成和重新批准执行包：

- 产品 URL；
- 本地源码快照；
- 用户需求；
- credential ref；
- allowed/forbidden 范围；
- 页面扫描证据；
- App 或协议版本。

Server 建议：任何影响业务动作、目标、路由或安全边界的变化都使原批准失效。

## 3. P0：installation 与包来源绑定

请确认：

1. `installation_id` 是否固定由 installation Ed25519 公钥确定性计算；
2. package producer 的 install ID 是否必须与 lease installation ID 相同；
3. 人工批准记录是否也绑定同一个 installation ID；
4. App 重装或密钥轮换后，旧项目和旧批准包如何处理；
5. bootstrap token 的签发、轮换和吊销由哪个系统负责。

Server 建议：installation、lease、producer、approval 和 package 五者必须一致，否则返回 `unverified_origin`，不得标记为正式 App 产包。

## 4. P0：包上传后的启动语义

请确认 App 的预期行为：

- 上传成功后 Server 自动进入 queued/running；还是
- App 需要显式调用 start/run；还是
- App 需要先上传 credential envelope 或完成人工登录 checkpoint 后，Server 才开始执行。

Server 建议使用以下状态机：

```text
package_received
→ awaiting_credentials | awaiting_manual_login | queued
→ worker_claimed
→ running
→ validating
→ rendering
→ completed | failed | canceled | expired
```

App 不应直接启动或访问 loopback Worker。

## 5. P0：登录和凭据模式

请确认正式产品支持哪些模式：

### 模式 A：一次性 credential envelope

- App 上传加密 credential envelope；
- 只包含 opaque `secret_ref` 和受控密文；
- 必须绑定 job/package/digest/installation/lease/domain/operation/expiry；
- Worker 一次性消费；
- 不得写入磁盘、日志和结果。

### 模式 B：人工登录 checkpoint

- Server 打开隔离 Chromium；
- App 展示“等待人工登录”；
- 用户只在 Chromium 中输入凭据；
- App/Server 通过不含凭据的 checkpoint 信号继续任务。

需要确认：

1. 正式生产是否同时支持 A/B；
2. 哪种模式是默认模式；
3. 登录失败、MFA、验证码、账号锁定如何反馈；
4. 人工登录等待多久后过期；
5. App 如何判断当前页面已经登录完成。

Server 建议：首个正式验收优先使用人工登录 checkpoint；credential broker 完成安全审计后再启用自动注入。

## 6. P0：stage、用户需求和证据覆盖

请确认 App 在正式产包前是否强制校验：

```text
每条用户需求
→ workflow requirement
→ stage
→ action/input
→ required validation
→ Evidence ID
```

以下缺口是否必须阻止批准：

- 缺少用户要求的 fill/input stage；
- 缺少等待业务完成的 stage；
- 缺少业务完成判定；
- 缺少字幕/叙事要求；
- required validation 没有真实目标；
- stage 只来自模型推测，没有页面或源码证据。

Server 建议：任何关键需求没有形成完整链路时，App 应 fail-closed，不生成可上传正式包。

## 7. P0：selector 和目标合同

请确认 selector 候选必须携带的来源信息：

```text
evidence_id
source_kind: source_scan | page_scan | approved_manual_annotation
source_digest
selector_kind
selector_value
observed_role
observed_accessible_name
observed_at
```

还需确认：

1. `data-testid` 只能进入 test_id/selector，不能拼入 accessible name；
2. action target、component 和 Evidence ID 的 selector 必须一致；
3. 页面预扫描或源码绑定失败时，是否禁止生成泛化 selector；
4. Server 是否只能使用 App 明确批准的 selector alternatives。

Server 建议：以上四项全部作为正式包硬门禁。

## 8. P0：动作结果和动态路由

### 8.1 动作前目标与动作后结果

请确认 App 是否强制区分：

- `action_target`：被点击、填写或提交的控件；
- `success_target`：动作完成后出现的页面、弹窗、状态或结果控件。

Server 建议：click/fill/submit 的 required validation 不得只复用 action target，除非明确声明为幂等状态检查。

### 8.2 动态路由

请确认动态路由采用哪种表达：

```text
/project/{project_id}
project_id_from=previous_stage.result.project_id
```

或使用结构化 route template/binding。不能把 `/project/:id` 当作字面 URL 交给 Server。

## 9. P0：业务完成判定

对于长时间任务，例如代码生成或项目构建，请确认 App 如何表达完成条件：

- 页面状态控件；
- 构建日志状态；
- 预览区域可用；
- 成功提示；
- 路由或项目状态变化；
- 多个条件组合；
- 最大等待时间。

Server 建议：使用结构化条件组合，不允许只写“等待构建完成”自然语言。超时后必须返回最后 URL、页面标题、截图、trace、已观察状态和失败阶段。

## 10. P1：App 状态显示与错误反馈

请确认 App 页面需要展示的最小状态：

- 当前协议版本；
- installation/lease 是否正常，不显示 token；
- package ID、job ID；
- 当前 stage、进度和等待原因；
- `awaiting_credentials` / `awaiting_manual_login`；
- blocking error code；
- 可执行的下一步；
- 是否允许取消或重新生成包；
- 是否需要重新人工批准。

建议 App 不展示 Worker 内部 token、端口实现、堆栈或原始 envelope，只展示稳定的业务状态和脱敏诊断。

## 11. P1：结果、下载、ACK 与编辑器交接

请确认正式成功需要哪些 artifact：

- StageEventLog；
- StepResults；
- ValidationReports；
- Replay Manifest；
- 关键截图；
- Browser trace；
- 原始录屏；
- 最终 MP4；
- checksum/size；
- 编辑器素材清单。

Server 建议：上述关键产物缺失时不得显示“端到端验收成功”。App 下载全部 chunk 并生成 `.verified.sha256` marker 后，才能 ACK 和进入编辑器。

需要确认 ACK 后：

1. Gateway 可以立即清理哪些数据；
2. App 是否保留原始录屏、trace 和 Replay Manifest；
3. lease 是自动释放还是由 App 显式释放；
4. 编辑器交接失败时是否允许重新下载结果。

## 12. P1：断线、重启和幂等性

请确认：

- App 重启后如何恢复 lease/job；
- Gateway 重启后是否要求重新申请 lease；
- 含凭据任务是否必须重新上传 credential envelope；
- package upload 使用哪个 idempotency key；
- 重复上传同一 digest 是返回原 job，还是创建新 job；
- canceled/expired job 是否允许恢复；
- artifact chunk 是否支持断点续传。

Server 建议：包上传、状态读取和 artifact 下载应支持幂等恢复；凭据 envelope 不允许恢复，必须重新上传。

## 13. P1：协议版本兼容

请确认 App 如何处理：

- Gateway 不支持当前 Direct 协议版本；
- package schema 版本不兼容；
- OutcomeVerifier 规则版本变化；
- Gateway 与 Worker 版本不一致；
- 必填字段新增。

Server 建议：health/lease receipt 返回明确的 supported versions 和 capability flags；不允许生产环境静默回退到 Legacy Exchange。

## 14. Worker 拓扑对 App 的边界

建议确认以下边界：

- App 只访问 TLS Gateway 和 lease 返回的数据端口；
- App 不访问 `127.0.0.1:18444`；
- App 不持有 Worker token；
- App 不负责启动 Worker 或 scheduler；
- Worker 不可用时，App 只接收稳定状态，例如 `queued`、`worker_unavailable` 或 `retry_scheduled`；
- Worker 的部署、调度、崩溃恢复属于 Server 内部运行协议。

## 15. 三份最新协议之间需要共同确认的口径

### 15.1 正式成功结果缺少 MP4/trace/截图/StageEventLog 时的严重级别

当前文件存在以下口径差异：

- Direct API 要求结果与 trace、stage、业务验证和 artifact 一致；
- Direct 工作日志把真实 Chromium、截图、视频、trace、下载和编辑器交接列为正式闭环条件；
- OutcomeVerifier adapter 当前将 `MISSING_TRACE_ARTIFACT`、`MISSING_SCREENSHOTS`、`MISSING_MP4_VIDEO`、`MISSING_STAGE_EVENT_LOG` 定义为 warning。

建议按结果类型区分：

| 结果类型 | 缺少 MP4 | 缺少 trace/StageEventLog | 建议处理 |
|---|---:|---:|---|
| 正式 completed 成功结果 | blocking | blocking | 不得进入 ACK/编辑器，也不得标记正式验收成功 |
| 正式 failed 失败诊断结果 | 允许无 MP4 | 至少应有可追溯诊断；缺失时 blocking 或 infrastructure failure | 返回失败包，不伪造成片 |
| fixture/test-only | 可为 warning | 可按测试目标决定 | 只能标记 fixture/test-only |

请确认 App 是否同意正式 completed 结果采用上述强门禁。

### 15.2 Evidence refs 缺失的严重级别

OutcomeVerifier 基础规则把 `evidence_refs_missing` 定义为 blocking，但 adapter 表中 `MISSING_EVIDENCE_REFS` 为 warning。建议统一为：

- required stage 的 `outcome_observed` 缺少 evidence refs：blocking；
- optional stage 或附加观察缺少 evidence refs：warning；
- App UI 应展示具体缺失 stage，而不是统一显示“生成失败”。

### 15.3 `reunderstanding_required` 的触发和承接方

当前规则把 `reunderstanding_required` 预留给外部 Validation Agent adapter，同时又规定 stage 验证失败超过阈值时转为该决策。需要确认：

1. App 收到该状态后是自动重新理解，还是要求用户重新批准；
2. 原 job 是终止、暂停还是可恢复；
3. 新计划是否必须生成新的 package ID 和 approval digest；
4. Server/OutcomeVerifier 只提供问题，不得自行重写业务计划。

Server 建议：原 job 终止；App 根据结构化问题重新产包并重新批准，创建新 package/job。

## 16. 建议回复模板

请对以下项目逐项确认或修改：

```text
1. approval_subject_digest 和批准记录字段：
2. 计划失效/重新批准条件：
3. installation、lease、producer 和 approval 的绑定规则：
4. 包上传后自动执行还是显式 start：
5. credential envelope 与人工登录的支持范围：
6. stage/用户需求/Evidence 覆盖硬门禁：
7. selector 来源和 alternatives 规则：
8. action_target 与 success_target 的表达：
9. 动态 route parameter 的绑定格式：
10. 长任务完成条件和最大等待时间：
11. App 状态和错误展示字段：
12. 正式成功必须包含的结果产物：
13. ACK、lease release 和结果保留策略：
14. 断线、重启、重复上传和幂等规则：
15. Direct 协议和规则版本兼容策略：
16. 正式成功结果缺少 MP4/trace/StageEventLog 的严重级别：
17. required Evidence refs 缺失的严重级别：
18. reunderstanding_required 的 App 承接流程：
```
