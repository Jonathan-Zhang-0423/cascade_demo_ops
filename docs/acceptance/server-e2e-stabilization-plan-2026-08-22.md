# Server 端到端稳定性开发计划（待逐项审阅）

状态：仅完成问题登记与证据归档，尚未执行本计划中的代码修改、App 产包或端到端测试。

原则：每一项先由产品/协议确认方案，再进入实现；不修改 App 代码，不修改 Validation Agent 规则，不把旁路结果标记为正式 App→Server 成功。

## P0-1：正式 App 原始包来源门禁

问题：历史运行中出现过 fixture、Dev HTTP 重构包、页面-only 包和正式包标记混用；`app_formal_run=true` 单独不足以证明生产来源。

证据：最近 Direct job 的 package metadata、source binding、package digest 和生产审计没有形成独立可核验的完整链。

候选方案：

- Server 入站只接受 App 生成的不可变包和 App 生产审计摘要；
- 强制校验 package/bundle/plan/policy/source digest；
- 将 `producer_attestation`、`source_snapshot_digest`、`approved_at` 纳入入站门禁；
- 缺任一证据时停在 `package_provenance_blocked`，不启动 Chromium。

待审阅决定：是否把“生产审计摘要 + 原始包哈希”作为 Direct v1 必填字段，而不是仅作为 Server 本地诊断字段。

### 与当前协议的对齐结论（待确认）

当前协议已经硬性要求或实际校验：

- Direct 传输层根据原始 package bytes 计算 transport SHA-256，并以 package ID、installation、package digest 做幂等绑定；
- Direct job 还要求非空的 source package digest。当前实现取 `reproducibility.package_hash_sha256`，因此该字段虽然模型标签是 optional，但对 Direct 实际执行已经是必需的；
- `approved_at`、`approval_id`、`approval_schema_version`、`plan_digest_sha256`、approval subject digest 和各 subject digests 必须一致；
- executable bundle 的 plan hash、bundle hash、markdown hash、graph hash 等已有校验；
- 存在源码派生执行证据时，`source_binding_summary` 必须存在、schema/hash 有效，不能是被阻断的 page-only 来源。

当前协议没有明确规定为 Direct 入站字段的内容：

- 独立的 `App production audit summary` 字段；
- 一个统一命名的 standalone `policy_hash_sha256` 入站字段；
- 所有场景都必须存在 `source_snapshot_digest`。

因此，在没有 App/协议版本升级前，不应把上述三项直接当成现行协议硬门禁并声称“协议要求”。可先作为 Server 侧诊断/准备度检查；若要升级为缺失即禁止 Chromium，必须先形成 App 与 Server 共同确认的新协议字段和版本。

## P0-2：凭据与自动登录契约

问题：最近运行使用 `credential://demo/unattended-e2e`，但实际填入邮箱为占位值 `xxx`，页面停在 `/login`。

候选方案：

- 保留 App 负责 grant，Server 只负责一次性 envelope 消费；
- 在 Worker 内存中增加非泄漏的凭据有效性门禁：空值、占位值、邮箱基本格式、过期时间、域名/操作范围；
- 登录前后增加独立诊断事件和登录门禁报告；
- 登录失败立即 fail-closed，不启动业务阶段和编辑器。

待审阅决定：是否允许 Server 对凭据做“格式/占位值”检查；该检查不读取或输出密码，不改变 App 的凭据授权规则。

## P0-3：Direct credential envelope 客户端链路

问题：`backend/cmd/directsmoke` 目前只上传 package，不上传 credential envelope，不能覆盖“凭据包→自动登录”正式回归。

候选方案：

- 扩展 Direct 测试客户端，严格按同一 lease/job/package digest 上传 package receipt 和 credential receipt；
- 凭据值仅从本机安全存储读取，禁止命令行参数、日志和文件落盘；
- 添加 envelope 绑定、过期、scope 和一次性消费的集成测试；
- 将该客户端标记为 `formal_app_direct`，与 fixture smoke 分离。

待审阅决定：是否新增独立 `directe2e` 命令，避免继续复用只支持无凭据 fixture 的 `directsmoke`。

## P0-4：登录失败首因与级联错误隔离

问题：StageEvents 能看到 `/login`，但自动登录 RPC 没有形成独立登录结果；运行器仍执行第一阶段观察，OutcomeVerifier 又累计下游缺失，掩盖了首因。

候选方案：

- 新增 `login_gate_started/succeeded/failed` 事件和脱敏 login diagnostic；
- 登录失败时只生成登录证据包，不执行后续 Stage；
- 结果报告以 `direct_auto_login_failed` 或更具体的登录码为根因，其余节点标为 `not_started`；
- OutcomeVerifier 对 `not_started` 不重复生成 MP4/编辑器等次生错误。

待审阅决定：是否采用“登录门禁失败即不进入业务 Stage”的严格 fail-fast 规则。

## P1-1：登录选择器与编码稳定性

问题：Browser Agent 源码中部分中文 `has-text` 选择器出现乱码，存在无法识别中文登录控件的风险。

候选方案：

- 优先使用 App 包中的 evidence-bound role/name/testid/CSS；
- Server 仅在同一证据范围内使用 Unicode-safe 备选；
- 禁止把乱码文本作为固定选择器提交；
- 增加中文登录页回归测试，覆盖方法选择、邮箱、密码、提交和成功路由。

待审阅决定：是否将“App evidence selector 优先、Server 文本猜测降级”写入 Browser Agent 运行契约。

## P1-2：登录契约的可追溯现场还原

问题：当前失败现场有截图和 URL，但缺少完整的“凭据契约→表单解析→提交→路由迁移”链条。

候选方案：

- 保存 selector ID、evidence ID、页面 URL/title、表单字段类型、提交结果和路由迁移；
- 对密码/邮箱值只保存掩码和哈希，不保存原文；
- 统一写入 `login-gate.json`，并在结果包中通过 artifact ref 关联。

待审阅决定：是否将 `login-gate.json` 设为正式结果包必备 artifact。

## P1-3：历史测试分类与唯一基线

状态：文档归档已完成，代码门禁尚未实现。

文件：

- `docs/acceptance/formal-app-server-e2e-baseline-v1.md`
- `docs/acceptance/test-history-index-2026-08-22.md`

后续可选实现：在结果包中强制写入 `classification`，防止 fixture、waiver、人工审核和正式 Direct 结果混淆。

## P2：通过登录门禁后的完整链路回归

前置条件：P0-1 至 P0-4 审阅通过并实现；App 侧生成一份真实、不可变、有效 credential grant 的原始包。

执行范围：

- Direct package receipt + credential receipt；
- `/login → /app` 自动登录门禁；
- 业务 Stage、录屏、Trace、截图、OutcomeVerifier；
- 编辑器、Seedance 候选、测试 TTS、双规格 MP4；
- checksum、verified marker、结果 ACK。

在 P2 前禁止把任何旧包或旁路包作为正式回归输入。

## 审阅顺序

建议按以下顺序逐项确认：

1. P0-1：是否接受正式包来源门禁；
2. P0-2：是否接受 Server 侧非泄漏凭据有效性检查；
3. P0-3：是否新增支持 credential envelope 的 Direct 测试客户端；
4. P0-4：是否采用登录失败即停止后续 Stage；
5. P1-1/P1-2：确认登录选择器和诊断证据格式；
6. 全部确认后，再安排代码实现与 App 原始包回归。
