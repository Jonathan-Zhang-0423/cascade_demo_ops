# 本地旁路联调测试报告（aug_6 测试交付包）

日期：2026-08-06
报告人：万明达（Validation Agent）
主送：孟洋
抄送：徐一凯
依据：`aug_6_测试交付包/test-docs/本地旁路联调工作说明.md`、`server-local-bypass/启动说明.md`、`dev-visible-browser-agent-acceptance.md`
测试类型：**development / loopback 本地旁路**（`dev_test_only=true`，非正式 Exchange 验收）
Server commit：`ed6f6aa`（含 `d02d83b feat: harden server acceptance and media execution`）

## 1. 结论摘要

回答"能否跑出 Server 侧的产出视频"：

| 问题 | 结论 |
|---|---|
| **Server 渲染管线能否产出视频** | **能** —— fixture 包跑通完整链路，产出可播放 MP4 |
| **真实贪吃蛇任务包能否跑出视频** | **不能** —— 首次进入真实执行，但第 1 阶段目标解析失败即停止 |
| 阻塞归属 | App 产包侧（对应台账 APP-001 / APP-011） |

本轮的实质进展：借助 `d02d83b` 新增的 test waiver 机制，真实 App 包**首次突破 Server intake 进入真实浏览器执行**，并产出了脱敏诊断、阶段事件 JSONL 和真实截图。此前该包连 intake 都无法通过。

## 2. 环境

| 项 | 值 |
|---|---|
| 操作系统 | macOS（darwin），非交付说明假设的 Windows |
| Server | 本仓库源码 `go run ./cmd/devserver --addr 127.0.0.1:4317`（`cascade-devserver-current.exe` 为 Windows 二进制，本机不可执行；按 `dev-exchange-http-test-channel.md` 的 Server Smoke 章节改用源码启动，效果等价） |
| 旁路环境变量 | `CASCADE_DEV_EXCHANGE_HTTP=1`、`CASCADE_DEV_EXCHANGE_TOKEN=<local>`、`CASCADE_EXCHANGE_AUTO_RUN=1`、`CASCADE_DEV_LOCAL_SERVER_ACCEPTANCE=1` |
| 被测 Cascade 产品 | `http://127.0.0.1:5100/app`（5000 被 macOS ControlCenter 占用，按说明改端口并同步 `.env`） |
| video-worker | 已替换为交付包内 Aug 6 15:05 版本 |
| Node / Chromium / FFmpeg | node v24.18.0；Playwright chromium-1228；ffmpeg、ffprobe 均在 PATH |
| 媒体模式 | `dry_run` |
| 测试账号 | 接收方在本机空库自建，口令仅在隔离可见浏览器窗口手动输入，未进入执行包、日志、字幕或模型上下文 |

## 3. 测试一：Server 渲染管线（fixture 包）

命令：

```bash
go run ./cmd/devsmoke \
  --base-url http://127.0.0.1:4317 \
  --token <local-token> \
  --package-file-fixture \
  --sample-output-dir /tmp/cascade-server-smoke-<ts> \
  --timeout 300s
```

结果：

```json
{
  "ok": true,
  "status": "completed",
  "result": {
    "pass_rate": 1,
    "demo_video_count": 1,
    "raw_recording_count": 1,
    "trace_count": 1
  }
}
```

| 产物 | 路径 | 大小 |
|---|---|---|
| MP4 | `.cascade-dev/artifacts/exchange/xpkg_1786017639513601000_4/render/source_reference.mp4` | 288 KB |
| 原始录屏 | 同目录 `recording/page@e5162cfa….webm` | 186 KB |

结果包状态 `acked`，交付确认完成。

**结论：Server 侧 Browser Agent 执行、录屏取证、StepResults、Validation Reports、FFmpeg 渲染、结果包与 ACK 全链路正常。**

## 4. 测试二：真实贪吃蛇任务包（test waiver 路径）

### 4.1 waiver 签发

原包在 App 侧确信度门禁为 `readiness = blocked`，Server intake 拒绝上传（错误码 `client_execution_package_invalid`，原因 `package confidence assessment is blocked`）。按交付说明第 4 节，改用 `d02d83b` 新增的节点级 test waiver。

请求覆盖三个节点时被正确拒绝：

```text
node business_stage_final_observe has no missing non-destructive classification to waive
```

`final_observe` 的 action 是 `inspect`，本就被 `confidenceSafetyScore` 豁免，无需 waiver。改为只申请两个 `click` 节点后签发成功：

| 项 | 值 |
|---|---|
| `waiver_id` | `test_waiver_1786050844694253000` |
| `package_id` | `pkg_bundle_script_graph_1785933490880486000` |
| `bundle_hash` | `2e96309596eed577440f977706d0d83faa838110a552150f6f9fa9fc54b0c051` |
| `plan_hash` | `4459a29cb7a6ab370cf883eb6326ce63f903f916ba23a8cb3240745fbf5465df` |
| `dev_test_only` / `not_for_exchange_upload` | `true` / `true` |
| `formal_exchange` | `false` |
| `allowed_nodes` | `business_stage_new_project_entry`（click）、`business_stage_start_agent_build`（click） |
| 原包摘要 | `original_package_digest` 保持不变，原包与哈希未被修改 |

waiver 完整保留了原始阻塞原因：

```text
business_stage_new_project_entry: 安全域、来源或非破坏性约束不完整
business_stage_start_agent_build: 安全域、来源或非破坏性约束不完整
关键需求未完整映射到 stage 和证据
```

waiver 机制设计评价：只放行真正缺失声明的节点、拒绝无需豁免的节点、不改原包哈希、完整留痕阻塞原因——符合交付说明第 4 节的全部约束。

### 4.2 可见浏览器登录接力

```text
POST /v1/desktop/dev-visible-browser-agent/prepare   → status = awaiting_manual_login
（验收人员在隔离 Chromium 窗口手动登录）
POST /v1/desktop/dev-visible-browser-agent/<id>/continue → status = ready_for_approved_package
```

Session `dev_visible_1786050820197985000`，目标 `http://127.0.0.1:5100/app`，页面标题 `Cascade AI — Build Apps with AI`。Server 未读取也未注入口令。

### 4.3 执行结果

```text
POST /v1/desktop/app-package-test-waivers/test_waiver_1786050844694253000/run
```

响应：

```text
status: failed
message: 真实页面执行已停止并保留脱敏诊断；未生成可交付视频。
result_id: result_pkg_bundle_script_graph_1785933490880486000
validation_count: 1
```

阶段事件（`.cascade-dev/artifacts/dev-test-only/app-package-waiver/test_waiver_1786050844694253000/execution/browser-agent-stage-events.jsonl`）：

```text
seq 1  stage_started           stage_step_01_business_stage_new_project_entry
seq 2  observation_collected   FAILED assertion: target_resolved
seq 3  stage_failed            FAILED assertion: target_resolved
```

失败断言全文：

```text
browser_agent_target_not_resolved: business_stage_new_project_entry;
strategies=role:button+approved_name,role:link+approved_name,component_label,
interaction_label,component_css,interaction_css
```

六种解析策略全部未命中，执行在第 1 阶段正确停止，未产出视频。

## 5. 根因：App 生成的 selector 在真实页面上不存在

### 5.1 事实对照

| 来源 | selector |
|---|---|
| App 执行包主 selector | `[data-testid='new-project']` |
| App 执行包备选 selector | `[data-testid='create-project']` |
| **被测产品真实 DOM** | **`data-testid="button-new-project"`** |

真实值出自被测产品源码 `frontend/web/src/pages/dashboard.tsx:1042-1052`：

```tsx
<Button
  onClick={() => setShowNewDialog(true)}
  size="sm"
  className="..."
  data-testid="button-new-project"
>
  <Plus className="w-4 h-4" />
  <span className="hidden sm:inline">{t("dashboard.newProject")}</span>
</Button>
```

两个候选 selector 与真实值均不匹配。

### 5.2 App 自述的产生原因

执行包中该目标的 `source` 字段为 `runtime_adaptive_intent_fallback`，其 evidence 摘要写明：

```text
页面预扫描不可用，按用户显式需求编译运行时语义动作；不使用全局 selector 池补动作。
```

即 App 在页面预扫描失败的情况下，**根据需求文本推测 selector**，未与真实 DOM 校验，随后仍产出了看起来可执行的执行包。

### 5.3 目标控件确实存在

阶段截图 `stage-001-business_stage_new_project_entry-before.png` 显示"新建项目"按钮清晰可见于页面右上角，与"我的项目"标题同行。**不是页面缺少控件，而是 App 提供的定位方式错误。**

### 5.4 与既有台账的对应

对应 `docs/app-side-issues-found-by-server-acceptance.md`：

- **APP-001**（主 selector 与证据 selector 冲突）：本次是更强的形式——两个候选 selector 在真实页面上**都不存在**，且有产品源码真值对照。
- **APP-011**（模型理解失败后仍生成看似可执行的泛化阶段）：本次 `runtime_adaptive_intent_fallback` 与 evidence 摘要是该问题的直接自证。

建议将本节作为 APP-001 的补充证据，或作为 APP-011 的具体实例登记。

## 6. Validation Agent 侧表现

本轮 Validation Agent 判定正确：

- 正确判定 `status = failed`，未将未完成执行误判为成功；
- 产出 `validation_count = 1` 的验证报告；
- 保留脱敏失败诊断与真实截图证据；
- 阶段事件 JSONL 完整记录三条事件与失败断言全文，可追溯。

按《Validation-Agent 新架构对接任务说明》第 8 节"若 App 包本身不满足协议，应返回结构化问题，不得在 Validation Agent 内偷偷补写业务字段"，本轮未在 Validation Agent 内补写任何业务字段，也未替 App 修正 selector（交付说明第 4 节亦禁止"新增 App 没有声明的业务动作"）。

### 交付物状态（对照任务说明 §9）

| # | 交付物 | 状态 |
|---|---|---|
| 1 | OutcomeVerifier 三阶段实现 | 完成（已并入 main，PR #28） |
| 2 | 验证规则、错误码及严重级别说明 | 完成，本轮补齐场景 6/11 四个新错误码与修复提案规则（`docs/browser-agent-outcome-verifier-rules-v1.md`） |
| 3 | `RuntimeRepairProposal` 映射规则 | 完成（PR #30，`d22b6a8`） |
| 4 | 正常与异常自动化测试 | 完成，`internal/orchestrator` 全绿 |
| 5 | 固定验收包 fixtures | 完成（PR #30，`2a5470a`，四个受控 outline 场景包） |
| 6 | 可追溯固定包验收报告 | 完成（`docs/handoff/` 下四份） |
| 7 | Server 接入初始化方法与最小调用示例 | 完成（`Service.SetBrowserAgentOutcomeVerifier`，`service.go:175/190`） |

§6 十二个固定验收场景的错误码已全部实现并登记入规则文档。第 1–4 步固定验收完成；第 5 步真实端到端联调受本报告第 5 节的 App 侧问题阻塞。

## 7. 复现步骤

```bash
# 1. 启动被测产品（PORT=5100，独立 PostgreSQL）
cd /tmp/cascade_test_product && npm run dev:start

# 2. 启动 Server 旁路
cd cascade_demo_ops/backend
CASCADE_DEV_EXCHANGE_HTTP=1 \
CASCADE_DEV_EXCHANGE_TOKEN=<local-token> \
CASCADE_EXCHANGE_AUTO_RUN=1 \
CASCADE_DEV_LOCAL_SERVER_ACCEPTANCE=1 \
go run ./cmd/devserver --addr 127.0.0.1:4317

# 3. 验证 Server 渲染管线
go run ./cmd/devsmoke --base-url http://127.0.0.1:4317 \
  --token <local-token> --package-file-fixture --timeout 300s

# 4. 为真实包签发 waiver（仅两个 click 节点）
curl -X POST http://127.0.0.1:4317/v1/desktop/app-package-test-waivers \
  -H "Content-Type: application/json" -d '{
    "project_id":"proj_1785933490652339000",
    "package_id":"pkg_bundle_script_graph_1785933490880486000",
    "expected_bundle_hash_sha256":"2e96309596eed577440f977706d0d83faa838110a552150f6f9fa9fc54b0c051",
    "expected_plan_hash_sha256":"4459a29cb7a6ab370cf883eb6326ce63f903f916ba23a8cb3240745fbf5465df",
    "approved_node_ids":["business_stage_new_project_entry","business_stage_start_agent_build"],
    "dev_test_ack":true}'

# 5. 打开可见浏览器并人工登录，然后接力
curl -X POST http://127.0.0.1:4317/v1/desktop/dev-visible-browser-agent/prepare \
  -H "Content-Type: application/json" \
  -d '{"target_url":"http://127.0.0.1:5100/app","dev_test_ack":true}'
# （在隔离窗口手动登录）
curl -X POST http://127.0.0.1:4317/v1/desktop/dev-visible-browser-agent/<session_id>/continue \
  -H "Content-Type: application/json" -d '{}'

# 6. 执行 waiver
curl -X POST http://127.0.0.1:4317/v1/desktop/app-package-test-waivers/<waiver_id>/run \
  -H "Content-Type: application/json" \
  -d '{"session_id":"<session_id>","dev_test_ack":true}'
```

注意：waiver 与 visible session 均为短时有效（约 15–20 分钟），登录接力需在 session 过期前完成。

## 8. 产物索引

| 产物 | 路径 |
|---|---|
| fixture 包 MP4 | `.cascade-dev/artifacts/exchange/xpkg_1786017639513601000_4/render/source_reference.mp4` |
| fixture 包录屏 | `.cascade-dev/artifacts/exchange/xpkg_1786017639513601000_4/recording/page@e5162cfa….webm` |
| 真实包阶段事件 | `.cascade-dev/artifacts/dev-test-only/app-package-waiver/test_waiver_1786050844694253000/execution/browser-agent-stage-events.jsonl` |
| 真实包阶段截图 | `.cascade-dev/artifacts/dev-test-only/visible-browser-agent/dev_visible_1786050820197985000/stage-001-business_stage_new_project_entry-before.png` |

所有产物标记 `dev_test_only`，不得作为正式 Exchange 验收证据。
