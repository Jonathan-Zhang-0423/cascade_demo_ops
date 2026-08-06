# Server 验收发现的 App 侧问题台账

> 用途：记录 Server 使用 App 正式执行包进行真实链路验收时发现的 App 侧问题，供提交 Server 更新和跨团队对接时反馈。
>
> 责任边界：本台账只记录、复现和提出修复建议。Server 开发不得修改 App 前端、App 产包规则或原始执行包字段。

## 1. 执行原则

1. 每次 Server 提交前检查本台账，并向项目负责人汇报新增、仍存在和已由 App 修复的问题。
2. 每个问题必须包含执行包身份、事实证据、业务影响、建议和状态；不能仅凭猜测归责。
3. Server 可以提供严格受限的本地测试兼容措施，但必须标记为 `dev_test_only`，不得伪装成正式 Exchange，也不得改变原包及其哈希。
4. Server 自身缺陷单独说明，不得登记成 App 侧问题。
5. 台账不得记录账号、密码、Cookie、Token、API Key 或其他敏感信息。

## 2. 本轮真实验收基线

- 项目：`test--Server-8-4`
- Project ID：`proj_1785839461694284200`
- Package ID：`pkg_bundle_script_graph_1785839662893353500`
- Runtime：`browser-agent-outline-v1`
- Bundle hash：`6ab44b83536d867cbfee2738ffa11199c65326e758eeb20fc870229a4a7201bc`
- Plan hash：`0efdcafa277f91b2ea90cd1b2340bd8b553c8acf55ca411786e73f993010fd29`
- 真实产品页面：`http://127.0.0.1:5000/app`
- 验收日期：2026-08-05

## 3. 已确认的 App 侧问题

### APP-001：同一业务目标的主 selector 与证据 selector 冲突

- 状态：待 App 修复
- 严重程度：阻断
- 阶段：`business_stage_new_project_entry`
- 事实：交互目标的 label、Evidence ID 和同阶段组件均指向“新建项目”按钮，但主交互 selector 为 `[data-testid="project-list"]`；同 Evidence ID 对应的已验证组件 selector 为 `[data-testid="button-new-project"]`。
- 影响：Server 按主交互执行时无法把项目列表容器解析成“新建项目”按钮，真实执行在第 2 阶段停止。
- 复现证据：
  - `test_waiver_1785921054259009600` 的阶段事件在第 2 阶段报告 `browser_agent_target_not_resolved`。
  - 真实截图：`.cascade-dev/artifacts/dev-test-only/visible-browser-agent/dev_visible_1785920873644793300/stage-002-business_stage_new_project_entry-before.png`。
- App 侧建议：产包时增加 `interaction.target → component → Evidence ID → selector` 一致性校验；出现冲突时阻止审批或选择与交互 Evidence ID 完全一致的目标。
- Server 当前处理：仅在运行时从同阶段、同 Evidence ID、原包已声明的 selector 中寻找候选，并要求真实页面唯一、可见且语义兼容；不修改原包。

### APP-002：目标允许名称混入测试标识，和真实无障碍名称不一致

- 状态：待 App 修复
- 严重程度：高
- 阶段：`business_stage_new_project_entry`
- 事实：App 包保存的名称包含 `新建项目 button-new-project`，真实页面按钮的无障碍名称为 `新建项目`（视觉文字为 `+ 新建项目`）。
- 影响：严格的 role/name 目标解析无法直接命中真实按钮，也增加 selector 修复后二次验证的复杂度。
- 复现证据：真实截图与第 2 阶段 `allowed_names`、扫描 Evidence summary 的对照结果。
- App 侧建议：`allowed_names` 保存真实无障碍名称；`data-testid` 只写入 `test_id/selector` 字段，不拼接到名称中。
- Server 当前处理：仅对与同一 App Evidence ID 绑定的 selector，在唯一可见、角色允许、禁止名称未命中的前提下，剥离完全相同的 test-id 后比较名称。

### APP-003：关键业务动作缺少明确的非破坏性声明

- 状态：待 App 修复
- 严重程度：阻断正式执行
- 阶段：
  - `business_stage_new_project_entry`
  - `business_stage_start_agent_build`
- 事实：Server 正式校验结果为“安全域、来源或非破坏性约束不完整”；本地测试 waiver 只能为上述两个已明确批准的节点临时补足 `non_destructive=true`。
- 影响：原包不能直接进入正式 Server 执行，必须保持阻断；当前只可在 development、loopback、`dev_test_ack=true` 条件下验收。
- 复现证据：以下 waiver 的 `blocked_reasons`：
  - `test_waiver_1785922634489737800`
  - `test_waiver_1785923042036287100`
- App 侧建议：产包前根据动作语义和审批结果明确写入 `non_destructive`，不能让 Server 猜测；构建动作如有副作用，应由 App 给出准确风险分类和审批要求。
- Server 当前处理：仅测试旁路对明确列出的节点生效；正式 Exchange 校验不放宽。

### APP-004：关键需求没有完整映射到 stage 与证据

- 状态：待 App 修复
- 严重程度：阻断正式执行及最终视频完整性
- 事实：App 包的阻断原因包含“关键需求未完整映射到 stage 和证据”。当前执行计划未完整表达用户要求的所有输入步骤、持续等待构建完成的可观测判定以及逐步字幕要求。
- 影响：即使前两个点击动作成功，Server 也不能擅自补写业务输入、完成条件或字幕语义；否则会超出 App 已审批业务事实。
- 复现证据：上述两次 waiver 的 `blocked_reasons`，以及包内仅有 5 个业务阶段的计划结构。
- App 侧建议：将用户要求逐项映射为可执行 stage、输入值或 `input_ref`、required validation、完成判定和叙事/字幕信息，并建立“用户要求 → stage → Evidence ID”覆盖率校验。
- Server 当前处理：不补写业务语义，只报告缺失并在测试旁路内运行包中已有步骤。

### APP-005：动作后的成功校验目标错误

- 状态：待 App 修复
- 严重程度：阻断
- 阶段：`business_stage_new_project_entry`
- 事实：计划要求点击后“新建项目表单、弹窗或创建流程可见”，但 required validation 实际检查 `[data-testid="button-new-project"]`，而不是代表成功结果的 `[data-testid="dialog-new-project"]` 或 `[data-testid="input-project-idea"]`。
- 真实证据：第五轮 `test_waiver_1785982787973282700` 已产生 `action_completed`；点击后截图明确显示“今天你想做什么？”弹窗已打开，但错误 validation 返回 `not_visible`，Validation 因此停止。
- App 侧建议：明确区分动作前目标和动作后结果目标；成功判据必须证明业务结果，不能机械复用被点击控件。

### APP-006：缺少新建项目需求输入阶段

- 状态：待 App 修复
- 严重程度：阻断
- 事实：执行包从打开弹窗直接跳到启动构建，没有向真实 `[data-testid="input-project-idea"]` 填入“贪吃蛇游戏”的 fill stage。
- 影响：真实 `[data-testid="button-create-project"]` 在输入为空时处于 disabled 状态，后续构建无法执行。
- App 侧建议：新增经用户审批的 business input stage，并使用 `value_equals` 验证输入内容；Server 不得补写用户业务输入。

### APP-007：构建按钮名称与真实控件不一致

- 状态：待 App 修复
- 严重程度：阻断
- 阶段：`business_stage_start_agent_build`
- 事实：包内目标为泛化文字“启动 agent 构建”，没有稳定 selector；真实控件为 `[data-testid="button-create-project"]`，页面文字为“构建！”。
- App 侧建议：对交互后弹窗继续只读扫描，或从源码结构摘要读取稳定 test-id；不得用模型概念名称冒充已验证控件。

### APP-008：构建启动后的 required validation 缺少真实结果目标

- 状态：待 App 修复
- 严重程度：阻断
- 事实：validation 声称要确认“构建进度、日志或项目详情”，但 target 仍是泛化的“启动 agent 构建”，没有具体 selector、test-id 或可靠路由结果。
- App 侧建议：使用动态项目路由、项目详情根节点、构建状态控件、日志面板或预览区域等真实证据作为 required validation。

### APP-009：动态项目路由被当作字面 URL

- 状态：待 App 修复
- 严重程度：阻断后续观察
- 阶段：`business_stage_observe_agent_progress`、`business_stage_final_observe`
- 事实：包内将 `http://127.0.0.1:5000/project/:id` 作为真实运行 URL 和 `url_matches` 预期值；真实产品会生成 `/project/{真实项目ID}`。
- App 侧建议：按协议表达受限路由模板或由前一步结果绑定 route parameter，不得把 `:id` 占位符当作字面地址。

### APP-010：源码绑定未完成，计划实际为 page-only

- 状态：待 App 修复
- 严重程度：高
- 事实：包评估为 `source_binding_status=unverified`、`source_binding_mode=page_only`，项目理解摘要为“模块边界：0 个”。用户虽提供本地源码目录，计划没有可靠使用源码中已有的稳定控件结构。
- App 侧建议：产包前确认源码读取并输出可审计的 route/component/test-id 摘要和来源哈希；绑定失败时禁止声称已融合源码证据。

### APP-011：模型理解失败后仍生成看似可执行的泛化阶段

- 状态：待 App 修复
- 严重程度：高
- 事实：项目状态记录 Project Intelligence fallback 为 `json_parse_failed`，Multimodal Understanding fallback 为 `timeout`；随后仍生成“启动 agent 构建”等缺少页面证据的泛化目标。
- App 侧建议：模型失败或结构化解析失败时 fail closed，只保留已有真实证据的步骤并明确列出缺口。

### APP-012：置信度评估没有发现动作—结果语义矛盾

- 状态：待 App 修复
- 严重程度：高
- 事实：第 2 阶段 `result_validation_score=1`，但 required validation 实际检查错误目标；`deterministic_validation_coverage=1` 只证明字段存在，没有证明校验语义正确。
- App 侧建议：置信度必须验证结果状态是否真正证明业务目标，并检查动作前目标与动作后结果目标的语义关系。

### APP-013：重新打包旧计划时缺少陈旧性提示

- 状态：待 App 评估
- 严重程度：中高
- 事实：外层 Package 在 8 月 6 日重建，但内部 Bundle/Plan 生成于 8 月 4 日且哈希不变；重新打包没有重新扫描源码、页面或生成计划。
- App 侧建议：明确展示计划生成时间、源码快照、页面扫描时间和 staleness；来源或需求变化后要求重新生成和审批。

### APP-014：执行包超过软预算

- 状态：待 App 优化
- 严重程度：低，非本轮失败原因
- 事实：当前包为 `123556 bytes`，超过 `96 KiB` 软预算。
- App 侧建议：对重复 Evidence、组件和文字采用引用或去重结构，同时保留必要审计证据。

## 4. 已确认但不属于 App 侧的问题

### SERVER-001：本地测试结果打包曾错误依赖正式 Exchange 上传许可

- 状态：Server 已修复并通过回归。
- 说明：本地测试结果现明确标记 `not_for_exchange_upload=true`，不会伪装成正式交付。

### SERVER-002：人工登录后 Runtime 曾重复执行登录填充动作

- 状态：Server 已修复并通过回归。
- 说明：仅在本地可见验收中使用人工会话检查点，不读取或注入密码。

### SERVER-003：selector 修复 proposal 曾使用错误协议字段

- 状态：Server 已修复并通过回归。
- 说明：现在按 App `repair_policy.editable_fields` 使用 `action.target.selector`，不扩大 App 授权范围。

### SERVER-004：修复候选发现与修复后二次验证使用了不一致的名称规则

- 状态：Server 已修复，并由第四轮真实链路证明二次验证通过。
- 说明：第四轮 `test_waiver_1785945064917379500` 已依次产生 `repair_applied`、`target_resolved` 和 `action_started`，其中 `target_resolved=true`、`actual=approved_evidence_css`；证明同一 App Evidence ID 绑定的“新建项目”候选已在真实页面通过二次验证。该轮在 Worker 真正点击前因 SERVER-005 停止，尚未产生 `action_completed`，也未进入后续素材采集和视频渲染。
- 自动验收结果：Server `internal/executor`、`internal/app` 完整回归通过；video-worker 19 项通过，2 项既有真实渲染测试按环境配置跳过。

### SERVER-005：测试 waiver 的非破坏性分类未传递到 Worker 运行副本

- 状态：Server 已修复，并由第五轮真实链路复验通过。
- 事实证据：第四轮 `test_waiver_1785945064917379500` 在“新建项目”阶段已产生 `target_resolved` 和 `action_started`，随后约 5 毫秒内直接 `stage_failed`，没有 `action_completed`。Server Policy Guard 当时仅在授权意图副本中临时补足 `non_destructive=true`，而传给 Worker 的 stage interaction 仍保留 App 原包中的 `false`；Worker 独立安全检查因此在点击前拒绝动作。
- 责任边界：App 原包缺少明确分类仍属于 APP-003；Server 本地测试旁路没有把已经签发的节点级分类安全地传给 Worker，属于 Server 自身缺陷，不能归责给 App。
- 修复方式：只在 development 本地 waiver 的内存运行副本中补足分类；同时绑定 package ID、Bundle hash、Plan hash、node ID、stage ID、interaction index、action type 和 semantic ID。任何不匹配、过期、重复或 destructive target 都拒绝。原 App 包、正式编译计划及协议哈希保持不变，正式 Exchange 路径继续拒绝缺少分类的动作。
- 审计：每次应用分类写入 `waiver_runtime_classifications_applied`，明确记录 `runtime_copy_only=true`、`original_package_unchanged=true`、`formal_runtime_unchanged=true`。
- 真实验收证据：第五轮 `test_waiver_1785982787973282700` 已产生 `waiver_runtime_classifications_applied`，并在真实页面产生 `action_completed`；证明 Worker 已收到运行副本分类并完成“新建项目”点击。后续因 APP-005 的错误结果校验停止。
- 自动验收结果：Server `internal/app` 完整回归通过；video-worker 19 项通过，2 项既有真实渲染测试按环境配置跳过。

## 5. 每次 Server 提交前的反馈清单

- [ ] 确认本次没有修改 `frontend/`、App 产包规则或原始执行包。
- [ ] 运行与本次 Server 修改相关的专项测试和完整回归。
- [ ] 更新本台账的问题状态、复现证据和新增问题。
- [ ] 汇报新增 App 问题、仍阻断问题、App 已修复问题和 Server 自身问题。
- [ ] 明确哪些兼容措施仅用于本地测试，哪些属于生产 Server 能力。
- [ ] 不提交 `.cascade-dev`、账号凭据、截图中的敏感信息或临时验收包。
