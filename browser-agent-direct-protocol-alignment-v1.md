# Browser Agent Direct API v1 协议对齐与架构说明

> 本文件以项目根目录下以下三个协议文件为唯一基准：
>
> 1. `browser-agent-direct-api-v1.md`
> 2. `browser-agent-direct-worklog-2026-08-08.md`
> 3. `browser-agent-outcome-verifier-rules-v1.md`
>
> 本文件只统一 App、Gateway、Worker、Browser Agent 和 OutcomeVerifier 的对接口径，不修改 App 产包规则，也不修改 Validation Agent 的职责或验证规则。

## 1. 结论摘要

当前设计的主链路是正确的，但正式闭环仍有三个必须补齐的 Server 条件：

- Direct 包来源、installation、lease、人工批准 digest 必须形成不可替换的绑定；
- credential envelope 必须安全地传递到 Browser Agent 的登录执行接口；
- 结果包必须以完整的 stage evidence、StepResults、trace、录屏、MP4 和 checksum 作为正式成功门禁。

因此，当前状态应定义为：

```text
Direct 传输与加密基础能力：已实现
Browser Agent 受限执行设计：方向正确，局部运行已验证
正式 App → Server → Browser Agent → MP4 闭环：尚未完成正式验收
```

## 2. 正式拓扑

```text
App 本地规划与人工批准
        │ browser-agent-outline-v1
        ▼
TLS Gateway :18443
        │ lease 分配
        │ installation 独占 data port :24000-24031
        ▼
Browser Agent Gateway
        │ 仅本机调用
        ▼
Loopback Worker 127.0.0.1:18444
        │
        ▼
Server Browser Agent / Chromium
        │
        ├─ OutcomeVerifier
        ├─ StageEvents / StepResults / ValidationReports
        ├─ 截图 / Trace / 原始录屏
        └─ Renderer → WebM / MP4
        │
        ▼
加密结果与分块 artifact
        │ App 校验 SHA-256
        ▼
verified marker → ACK → 编辑器交接 → lease release
```

### 组件职责

| 组件 | 负责内容 | 明确不负责 |
|---|---|---|
| App | 业务意图、stage 顺序、输入语义、成功条件、安全边界、批准和原包 digest | 不把密码、Token、Cookie 或完整源码放入包；不让 Server 猜业务动作 |
| TLS Gateway | bootstrap 鉴权、lease 分配/释放、Direct 加密消息、重放保护、job 状态 | 不执行页面操作；不改变用户意图 |
| Installation data port | 每个 installation 的独占数据通道 | 不作为公共业务 HTTP 入口 |
| Loopback Worker | claim 已批准包、一次性消费凭据、调用 Browser Agent、上传 artifact、提交结果 | 不修改 stage 顺序、路由、安全边界或 secret_ref |
| Browser Agent | 在批准范围内定位控件、等待、有限 selector repair、操作、录制和采集证据 | 不创建未批准 selector，不扩展业务动作，不越过 allowed domains |
| OutcomeVerifier | 消费脱敏 DTO、阶段事件和结果包，判断是否继续或停止并报告 | 不读取 Playwright/page，不修改 selector、route 或脚本 |
| Renderer | 根据已采集的真实证据组织素材并生成 WebM/MP4 | 不补造缺失的业务证据 |

## 3. Direct API v1 对接约束

### 3.1 Lease

`POST /v1/direct/leases` 必须使用：

- bootstrap token；
- installation ID；
- installation Ed25519 公钥签名；
- timestamp；
- 一次性 nonce。

成功后 Gateway 返回 lease、独占数据端口、lease token 和过期时间。lease token 只能用于内部通信，不应展示给终端用户。

### 3.2 数据消息

所有 Direct 数据请求必须携带：

- `X-Cascade-Timestamp`；
- `X-Cascade-Nonce`；
- `X-Cascade-Body-SHA256`；
- `X-Cascade-Signature`。

包、凭据、结果和 artifact chunk 使用 `HKDF-SHA256 + AES-256-GCM`。明文业务 payload 不应出现在 App 到 Gateway 的 HTTP body 中。

### 3.3 包上传

`POST /v1/direct/packages` 只接受 `browser-agent-outline-v1`，上传前必须确认：

1. App 已完成批准；
2. 上传内容与批准时的 bundle/plan/stage digest 一致；
3. installation、lease、producer install ID 和 package ID 一致；
4. 包通过 Server-owned package validation；
5. 不包含明文密码、Token、Cookie、完整源码、完整 DOM 或未脱敏敏感截图。

### 3.4 凭据

`POST /v1/direct/jobs/{job_id}/credentials` 只接受一次性加密 `credential_envelope`。封套必须绑定：

```text
job_id / package_id / package_digest / grant_id / secret_ref
installation_id / lease_id / allowed_domains / allowed_operations / expires_at
```

Gateway 只在内存保存，Worker consume 后立即清理。正式实现还必须把 consume 后的安全凭据引用传入 Browser Agent 的登录接口，并禁止把原始凭据写入日志、截图、trace 或结果包。

### 3.5 结果和 artifact

结果必须满足：

- `result_id`、`job_id`、`package_id`、source digest 一致；
- ValidationReports 的 source package 一致；
- 所有 artifact 的 SHA-256 和 size 与上传内容一致；
- 每个 chunk 不超过 4 MiB；
- App 下载后重新计算总 SHA-256 和字节数；
- 未生成 `.verified.sha256` marker 前，不得 ACK、审核或交给编辑器。

## 4. Worker 启动拓扑是否需要和同事对齐

需要，而且这是运行协议的一部分，不只是运维细节。

必须统一以下口径：

1. 谁启动 TLS Gateway；
2. 谁启动 loopback Worker；
3. 谁启动 Worker scheduler；
4. Worker 使用哪个独立 token；
5. Worker 监听地址是否始终为 `127.0.0.1:18444`；
6. Gateway 只负责排队，还是内嵌 scheduler；
7. Gateway/Worker 重启后，queued job 如何恢复；
8. lease 过期、端口耗尽、Worker 崩溃时的状态和重试规则。

推荐的最小运行方式：

```text
directgateway
  ├─ TLS control plane :18443
  ├─ installation data ports :24000-24031
  └─ persistent job/lease state

directworker
  ├─ loopback API 127.0.0.1:18444
  ├─ worker token
  ├─ claim-next/scheduler
  └─ Browser Agent + Renderer
```

因此，需要和同事对齐的是“启动责任、端口、token、状态恢复和结果提交时序”，不是让同事修改 Browser Agent 业务规则。

## 5. Browser Agent 当前设计评估

### 5.1 应保留的设计

当前 `Outline + 受限自适应 Browser Agent` 方向正确：

```text
App 定义业务事实
→ Server 编译受限运行计划
→ Browser Agent 在批准候选中定位和修复
→ OutcomeVerifier 根据真实观察判定
→ Renderer 只处理已验证素材
```

不建议回退到旧的“App 生成完整 Playwright 脚本、Server 逐字执行”路径。旧路径无法可靠应对登录后状态变化、动态路由、selector 变化和可追溯证据要求。

### 5.2 当前设计仍存在的问题

#### A. selector 质量和真实页面证据混为一谈

仅有 `test_id` 或 selector 字段，不代表该 selector 在真实页面存在。需要区分：

- 静态 selector 质量；
- App 证据来源质量；
- Server 运行时解析结果。

#### B. 动作目标和结果目标可能复用

点击“新建项目”后，验证目标不能仍然是“新建项目”按钮。必须验证弹窗、输入框或下一阶段真实状态。

#### C. fallback 仍可能生成“看似可执行”的计划

页面预扫描失败、模型 JSON 解析失败或 source binding 为 `page_only` 时，不能继续生成高可信业务计划。应 fail-closed，要求重新生成或人工补充证据。

#### D. 用户需求覆盖不足

必须逐项建立：

```text
用户需求
→ stage
→ action/input
→ required validation
→ evidence ID
```

缺少输入、等待、完成判定或字幕要求时，不能进入正式执行。

#### E. 动态路由语义不足

`/project/:id` 不能被当作字面 URL。必须由 App 声明“项目 ID 来自前一步结果”，Server 不得自行推断。

#### F. 结果门禁不够严格

有结果 JSON 或 artifact digest，不等于业务验收成功。正式成功必须同时具备完整 stage evidence、StepResults、StageEvents、ValidationReports、录屏、trace、MP4 和 checksum。

## 6. 建议的 Server 改进顺序

### P0：先补正式结果门禁

将 Direct 和 Legacy 的成功条件统一为：

```text
所有 required stage 完成
AND 每个 stage 有真实 observation
AND StepResults 完整
AND StageEventLog 存在
AND trace/截图/录屏存在
AND final MP4 存在且可校验
AND artifact checksum 正确
AND source = app_formal_exchange
```

任何条件不满足，只能返回失败或 `not_ready_for_app_e2e_acceptance`。

### P1：补 Direct 来源与批准绑定

把以下字段纳入不可变绑定：

- approval subject digest；
- producer install ID；
- installation public key；
- lease ID；
- package/bundle/plan hash；
- approval timestamp 和批准记录 ID。

### P1：完成凭据安全注入

增加明确的 Worker → Browser Agent credential broker 接口：

```text
consume credential envelope
→ 校验 job/package/lease/domain/operation
→ 只向隔离 Browser Context 注入
→ 输入动作完成后立即清理
→ 不进入日志、trace、截图和结果包
```

### P1：完善运行前语义检查

执行前拒绝以下包：

- action target 与 postcondition 相同且没有幂等声明；
- required validation 没有真实目标；
- selector 来源为未验证 fallback；
- 动态路由没有参数绑定；
- 用户需求未完整映射；
- 业务动作缺少安全分类。

### P2：统一启动和恢复机制

明确 Worker scheduler 的唯一责任方，并补充：

- claim-next；
- queued job 恢复；
- lease 过期处理；
- Worker 崩溃重试；
- Gateway 与 Worker 版本兼容性检查；
- 端口分配和释放审计。

## 7. 验收定义

### Server fixture 验收

可证明 Server 的回归能力，包括录屏、StepResults、trace、ValidationReports、MP4 和 ACK，但必须标记：

```text
origin=server_controlled_fixture
status=server_fixture_only
```

### 正式 App→Server 验收

只有同时满足以下条件才算通过：

```text
origin=app_formal_exchange
app_generated=true
formal_exchange=true
strict_evidence_complete=true
final_mp4_available=true
editor_materialized=true
status=ready_for_app_e2e_acceptance
```

test waiver、fixture、mock、局部点击和手工旁路都不能替代正式验收。

## 8. Worker 就绪与拓扑诊断

Server 现在提供脱敏的 Worker readiness 信号：

- `GET /v1/direct/health`：在 bootstrap 鉴权后返回 `worker_readiness`；
- `GET /v1/worker/readiness`：仅限 loopback + Worker token；
- `POST /v1/worker/heartbeat`：外置 Worker 定期上报 `external`，嵌入式 scheduler 上报 `embedded`。

readiness 只包含 ready、mode、heartbeat age、stale threshold 和 queued job count，不返回 Worker token、bootstrap token 或凭据内容。超过 5 秒没有心跳时，Gateway 报告 Worker 未就绪；这不改变 job 状态，也不伪造执行成功。

## 9. 当前结论

当前不需要推倒重做 Browser Agent，但必须把它从“能执行并产出部分证据”提升到“语义门禁严格、凭据闭环、结果证据完整、Worker 拓扑明确”的正式运行级别。

最小下一步是：

1. Server 先补 Direct 结果完整性门禁；
2. 补 Direct 来源/批准绑定；
3. 完成凭据到隔离 Browser Context 的安全注入；
4. 与 App/Worker 对接方统一启动拓扑和状态恢复协议；
5. 等 App 重新生成完整、真实证据绑定的正式包后，再进行正式端到端验收。
