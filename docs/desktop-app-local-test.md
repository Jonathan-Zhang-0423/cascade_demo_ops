# 桌面端 App 本地稳定试用方法

这套流程用于在没有真实云端、没有真实客户凭据、没有 Wails 正式壳的情况下，稳定体验桌面端 v1 的核心闭环：

```text
输入材料 -> 本地理解 -> 方案审批 -> 执行包审批 -> 模拟云端录制 -> 失败诊断 -> 修复重审 -> 成品验收
```

前端支持两种 bridge：

- `mock`：默认模式，只体验 UI，不调用 Go 后端。
- `local`：真实调用本机 Go Dev Bridge，跑本地理解到执行包生成链路。

## 1. 先跑一次稳定性检查

在仓库根目录执行：

```powershell
pnpm --filter @cascade/web test
pnpm --filter @cascade/web build
pnpm --filter @cascade/video-worker build
cd backend
go test ./...
cd ..
```

通过标准：

- web 单测全部通过。
- web build 能生成 `frontend/web/dist`。
- video-worker TypeScript build 通过。
- Go 后端测试通过。

## 2. 启动本地 App UI

### Mock UI 快速体验

在仓库根目录执行：

```powershell
pnpm --filter @cascade/web dev
```

打开命令输出里的本地地址，通常是：

```text
http://127.0.0.1:3000/
```

前端演示端口固定为 `3000`。如果端口被占用，Vite 会直接失败，请先释放该端口后重启。

### 真实本地执行包生成

打开两个终端。

终端 1 启动 Go Dev Bridge：

```powershell
pnpm dev:bridge
```

默认监听：

```text
http://127.0.0.1:4317
```

终端 2 启动 React，并切到 local bridge：

```powershell
$env:VITE_CASCADE_BRIDGE="local"
pnpm --filter @cascade/web dev
```

浏览器只打开：

```text
http://127.0.0.1:3000/
```

此模式下前端通过 Vite proxy 调用 `127.0.0.1:4317`，点击 `生成执行包` 会真实调用 Go agent 链路，生成 `ExecutionScriptDocument` 和 `ExecutableRecordingScriptBundle`。云端上传、录制、失败诊断和成品资产仍是后续阶段。

如需绕过 proxy 调试，也可以显式设置：

```powershell
$env:VITE_CASCADE_BRIDGE_URL="http://127.0.0.1:4317"
```

### 真实模型 API 实测

在仓库根目录创建本地 `.env` 或 `.env.local`，只放本机密钥，禁止提交：

```dotenv
CASCADE_LLM_MODE=real
KIMI_API_KEY=...
GLM_API_KEY=...
MINIMAX_API_KEY=...
SEEDANCE_API_KEY=...
DEEPSEEK_API_KEY=...
```

`CASCADE_LLM_MODE` 支持：

- `deterministic`：完全不用真实模型，适合 CI。
- `auto`：有 key 时调用模型，失败时降级 deterministic。
- `real`：真实模型失败就报错，适合你实测 API 接入质量。

默认模型分工：

- Kimi：需求理解、产品地图、执行图、中文审批文档润色。
- GLM：只读取代码结构摘要，不接收完整源码。
- MiniMax M3：页面/截图多模态理解。
- Seedance：视频操作阶段预留，本阶段不真实生成视频。

## 3. 固定试用脚本

按下面顺序操作，可以覆盖 v1 最重要的产品路径。

1. 进入 `项目`
   - 确认首屏不是 landing page，而是项目工作台。
   - 顶部应显示项目名、场景、目标受众、产品 URL、当前状态。
   - 左侧主导航应包含：`项目`、`新建演示`、`执行包`、`成品资产`、`设置`。

2. 切换 8 个阶段
   - 在项目页顶部依次点击：`基础设置`、`输入材料`、`产品理解`、`方案审批`、`执行包审批`、`云端录制`、`脚本修复`、`成品验收`。
   - 每个阶段都应展示中文业务可读内容。
   - 右侧检查器应持续显示安全策略、选中节点和云端状态。

3. 审阅执行方案
   - 回到 `方案审批`。
   - 点击步骤表里的不同节点，右侧检查器应同步变化。
   - 勾选或取消 `截图`、`聚焦`，表格状态应立即更新。

4. 生成执行包
   - 点击顶部 `生成执行包`。
   - App 会跳转到 `执行包`。
   - 页面应展示三栏：`思路文档`、`执行计划 JSON`、`可执行 TS 脚本`。
   - 审批事实区应显示 `Plan Hash`、`Script Hash`、`Bundle Hash`、允许域名、打码规则。
   - local bridge 模式下，三栏内容来自 Go 后端真实生成结果。

5. 验证上传阻塞
   - 在审批清单未完成时，`上传云端` 应为不可点击。
   - 依次勾选：执行方案、云端 IP 白名单、不上传完整源码、凭据授权、打码规则。
   - 勾完后 `上传云端` 应变为可点击。

6. 模拟云端录制
   - 点击 `上传云端`。
   - 当前阶段应进入 `云端录制`，状态显示录制中。
   - 回到 `执行包`，点击 `模拟完成`，App 应进入 `成品资产`，进度为 100%。
   - local bridge 模式下这些按钮会标记为后续接入，不触发真实云端流程。

7. 模拟失败诊断和修复闭环
   - 再点击顶部 `生成执行包` 回到执行包页。
   - 点击 `模拟失败诊断`。
   - 页面应显示失败节点、错误信息、当前页面、打码状态、脱敏截图/trace artifact refs、修复建议。
   - 点击 `生成修复包`。
   - App 必须回到 `执行包审批`，审批清单被重置，思路文档中应出现 `本次修复说明`。
   - 修复包未重新审批前，不允许上传。

8. 验收成品资产
   - 点击 `成品资产`。
   - 确认视频占位、步骤文档、checksum、来源追踪可见。
   - 点击 `批准成品` 后，资产状态应从待验收变为已批准。

## 4. 重点观察点

- 所有 UI 文案应为中文。
- 前端演示只使用 `http://127.0.0.1:3000/`。
- 审批页默认展示思路文档，JSON 和 TS 脚本作为技术审计项。
- 凭据只显示授权范围和 `secret_ref` 语义，不展示原始密码、token、API key。
- 执行包明确展示“不上传完整源码”。
- 失败诊断明确展示“已脱敏”，截图和 trace 都是敏感加密 artifact。
- 修复后的脚本包必须重新审批，不能自动上传。

## 5. 已知边界

- mock bridge 不会真实访问客户产品页。
- local bridge 会真实调用本机 Go agents；`CASCADE_LLM_MODE=real` 时会调用已配置的 Kimi/GLM/MiniMax。
- 当前不会真实上传云端，也不会生成真实视频。
- Wails 壳和真实云端 API 接入后，应复用同一套 DTO 和页面流程。
