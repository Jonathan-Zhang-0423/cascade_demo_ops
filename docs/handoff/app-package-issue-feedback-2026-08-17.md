# App 产包问题反馈

## 范围

本反馈来自一次正式 App 产包流程，不使用 Server fixture、test waiver 或手工改写执行包。

- 产品地址：`http://127.0.0.1:5000/app`
- 本地源码：通过 App 的 `local_repository` 安全引用接入
- 登录：通过不透明的 `credential://demo/...` 本地凭据引用接入
- 正式项目：`proj_1786974890145206900`
- 包格式：`demoops.client_execution_package.v1`，内含 `browser-agent-outline-v1`

## 已成功环节

1. Assistant 会话完成了目标地址、源码引用、凭据引用、安全边界和业务步骤的正式配置。
2. App 本地分析完成需求读取、代码读取（30 个文件）、页面理解、项目智能理解、多模态理解、产品地图、页面交互验证、流程图和脚本打包。
3. 生成了 6 个业务 stage、9 个已验证交互和 8 个业务交互，草稿包包含登录、新建项目、输入、构建、进度观察和最终状态观察。
4. 目标地址、允许域、凭据引用、禁止控制面路径、敏感数据遮罩策略均进入了正式包。

## 阻断结论

正式 Client 包的 `confidence_summary.readiness` 为 `blocked`，唯一阻断原因为：

```text
关键需求未完整映射到 stage 和证据
```

需求覆盖率为 `0.857`，即 6/7。按照严格 App -> Server 验收规则，包不能提交给 Server 录制，也不能以任何 Server 旁路、fixture 或 waiver 代替正式成功。

## 问题一：字幕要求被错误归类为页面交互需求

用户要求“每一步增加字幕说明”。该要求被写入 `must_show`，随后被 Client 包置信度算法当作每一项都必须绑定网页 stage 和页面证据的需求。

它没有 `node_refs`，因此成为唯一未覆盖需求并使包阻断。字幕本质上是视频编辑输出约束，应由 Server 视频编辑器在每个已批准 stage 生成和验收，而不是要求 App 页面预扫描为它寻找 DOM 控件。

### 建议

1. 在 App 输入模型中将字幕、旁白、镜头样式、时长、画幅等区分为 `presentation/output constraint`，不要写入网页 `must_show`。
2. 置信度算法应分别评估：页面业务需求需要 stage + 页面证据；视频输出需求需要 stage 覆盖 + 编辑计划/渲染验收条件。
3. 包内应明确产出类似 `caption_policy` 或 `presentation_requirements`：`caption_required=true`、`caption_per_stage=true`、语言、文案来源和验收规则。

## 问题二：业务步骤被过度合并

本次生成的 stage 映射中：

- “点击构建”和“输入详细游戏需求”都落在“填写项目名称”阶段。
- “等待并确认游戏构建完成”落在登录/会话 stage，而非进度观察或最终观察 stage。

这会降低 Browser Agent 的执行目标精度，也会让最终视频无法严格证明每项用户要求。

### 建议

1. 必须为“点击构建”生成独立 `business_submit` stage。
2. 必须为详细需求弹窗生成独立 `business_input` stage，保留输入语义但不记录敏感内容。
3. 必须为“构建完成”生成 `observe_progress` / `final_observe` stage，并定义可观测完成条件，例如状态文本、结果页、预览或构建完成标识。
4. 每项 `must_show` 只映射到语义匹配的 stage，禁止将等待结果回填到登录 stage 以凑覆盖率。

## 问题三：页面预扫描没有覆盖动态业务页面

页面验证识别了工作台初始页和“新建项目”按钮，但未获得点击新建项目后的输入框、构建按钮、详细需求弹窗及构建完成状态的直接页面证据。

当前流程因此只能为这些 stage 保留候选 selector/运行时修复空间，无法在 App 侧建立完整的静态证据链。

### 建议

1. App 预扫描应采用受限状态机：登录后 -> 工作台 -> 新建项目 -> 输入页/弹窗 -> 提交 -> 进度/结果页。
2. 每次状态转移只允许已批准的非破坏性业务动作，并记录 URL、角色、可访问名称、`data-testid`、截图摘要及时间。
3. 对动态页面不可获得的 selector，应显式标记 `runtime_discovery_required`，并绑定业务语义、允许的修复边界和 Server 需要回传的验证证据。
4. 不要把项目列表容器、仪表盘容器或已有项目卡片误判为用户请求的核心业务动作。

## 问题四：自然语言结构化结果不稳定

首次模型配置草稿未填项目名，并把部分安全限制混入展示步骤。最终依赖现有 App 手动配置接口补齐了用户已明确提供的字段。

### 建议

1. 对“步骤”“安全限制”“登录凭据引用”“视频输出约束”使用不同的结构化字段和校验器。
2. 将模型输出限制为语义标签，而不是直接把长句拆分为 `must_show`。
3. 在配置确认前显示字段分类预览，并对“输出约束误入页面步骤”给出可见警告。
4. 对缺失项目名等非关键字段提供稳定默认命名规则，不应阻塞已完整的业务目标。

## 问题五：本地源码与运行页面的绑定为 page_only

本次 `source_binding` 为 `unverified/page_only`。代码读取已完成，但产品源码和本地运行页面没有形成可证明的部署来源匹配证据。

### 建议

1. 本地启动测试时，App 应记录受控启动进程、端口、工作目录摘要和构建版本摘要，并只把哈希写入包。
2. 若源码由本地安全引用启动了对应端口，可形成 `local_runtime_binding` 证据；若不能证明，应明确维持 `page_only`，不要声称源码与页面已绑定。
3. Server 只能消费该声明，不能自行将 `page_only` 升级为源码匹配。

## 给 Server 的契约要求

Server 不应修改 App 包、重写 hash 或把 fixture 当作 App 包。收到此类包时应：

1. 解析并回传每个阻断需求、其 `node_refs`、证据数量和建议责任方。
2. 对 `presentation/output constraint` 与页面交互需求分别报告，避免将字幕、旁白等误诊为 selector 缺失。
3. 在 `readiness=blocked` 时拒绝正式执行，但保留可追溯的预检报告。
4. 仅在 App 重新生成的未改写包 `readiness=ready` 后，启动真实 Browser Agent、录屏、编辑、验证和交付。

## 问题六：媒体交付策略需要客户端展示、确认并回传

Server 现已确定以下默认策略：

1. 最终交付固定为同一时间线的 `2560x1440` Master 与 `1920x1080` 分发版。
2. Seedance 是默认展示候选 Provider；候选仅可用于通过门禁的非事实展示镜头，不能替代业务步骤的真实录屏证据。
3. 用户未明确要求时，TTS 默认根据产品风格、目标受众和已批准业务 Stage 生成旁白候选。
4. TOS 默认以私有任务前缀保存所有任务内容 30 天，包括原始证据、派生素材、模型候选、最终视频和审计信息。

Server 已提供项目级接口：

```text
GET /v1/desktop/projects/{project_id}/media-delivery-policy
PUT /v1/desktop/projects/{project_id}/media-delivery-policy
```

`GET` 返回默认策略、双规格输出、默认旁白模式及 `client_feedback_required`。App 应在提交会触发真实模型/TOS 出站的任务前展示：

```text
本任务所有素材、结果与审计信息默认保存 30 天。
```

用户确认或调整保留天数后，App 通过 `PUT` 回传 `media_delivery_preferences`。默认值为：

```json
{
  "narration": {"mode": "auto_by_product_style", "client_specified": false},
  "tos_retention": {
    "mode": "standard_30d",
    "retention_days": 30,
    "scope": "all_task_artifacts",
    "client_disclosure_acknowledged": true
  }
}
```

App 不得传递模型 API Key、TOS AccessKey、预签名 URL 或 Provider endpoint。客户端仅负责呈现与确认策略；Provider、模型版本、凭据和实际 TOS 对象路径均由 Server 管理。

## 验收标准

修复后，同一测试输入重新生成的 App 包应满足：

- `recording_run_spec.base_url = http://127.0.0.1:5000`
- `browser-agent-outline-v1` 完整保留
- 每个网页业务步骤具有语义匹配的 stage 和证据，或显式 `runtime_discovery_required`
- 字幕要求进入视频输出契约，而非未映射的网页需求
- `confidence_summary.requirement_coverage = 1`
- `confidence_summary.readiness = ready`
- 之后才允许不改写地提交给 Server 做无人值守录制和 MP4 验收
