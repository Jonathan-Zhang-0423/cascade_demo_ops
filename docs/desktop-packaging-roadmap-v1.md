# App 端封装打包路线图 v1

本文档是 `packaging/desktop-app-installer` 分支的研究基线。目标是把当前 App 端从“前端 dev server + Go dev bridge”逐步封装成真实可下载、可安装、可离线启动的桌面软件。服务器联通能力保留接口与配置入口，但本阶段不要求完整实现生产联通、自动配对或云端执行闭环。

## 目标

用户最终应下载一个安装包，安装后双击启动 Cascade DemoOps App：

- 内置 React 前端资源，不需要 `pnpm dev`。
- 内置 Go desktop runtime，不需要手动启动 `cmd/devserver`。
- 内置或可定位 Node/video-worker runtime，不要求普通用户安装 Node。
- 本地数据写入用户数据目录，不写入安装目录。
- 能完成 App 端需求理解、项目路径选择、三合一执行包生成与本地审批。
- 服务器联通只暴露配置、健康检查和协议接口预留；没有配置云端时 App 仍能本地生成执行包。

## 当前代码基线

已有能力：

- `backend/cmd/desktop`：可初始化 `DesktopBridge`，但目前只输出 JSON ready 信息，还不是窗口应用。
- `backend/internal/app/DesktopBridge`：提供 UI 可调用的 JSON-safe 方法，覆盖项目创建、执行包、runtime config、exchange 等服务入口。
- `frontend/web`：React UI，可通过 `VITE_CASCADE_BRIDGE=local` 调本地 dev bridge。
- `video-worker`：Node sidecar，JSON-RPC stdio 协议，构建产物为 `video-worker/dist/index.js`，Windows package baseline 会携带 build-time `node.exe`。
- `scripts/package-desktop.mjs`：已能打出资源目录 `dist/package`，包含 Go desktop binary、web dist、video-worker dist 和 `desktop-runtime.json`。
- `internal/config.AppRuntimeConfig`：已有 `dev / desktop / cloud` profile、用户数据目录、resource manifest 和 sidecar 路径解析。

主要缺口：

- 缺桌面壳：尚未接入 Wails/Tauri/Electron 这类窗口宿主。
- 缺安装器：尚未生成 `.exe/.msi/.dmg` 等用户可安装包。
- Node runtime 已进入 Windows package baseline；ffmpeg/ffprobe 策略仍待补齐。
- 缺原生文件夹选择器：当前仍偏文本路径输入。
- 缺系统安全存储：模型 key、云端 session、installation private key 仍需要后续接 OS keychain。
- 缺自动更新、签名、公证、崩溃日志和发布渠道。

## 推荐技术路线

v1 推荐继续走 Wails：

- Go 服务已经是核心 runtime，Wails 能直接绑定 Go 方法给前端。
- React UI 可复用现有 `frontend/web`。
- 安装包体积小于 Electron。
- 桌面能力如文件夹选择器、系统托盘、安全存储可在 Go/Wails 层逐步补齐。

备选：

- Tauri：Rust 生态，不贴合当前 Go 后端。
- Electron：最快接壳，但会引入 Node/Chromium 大包，和 Go bridge 双 runtime 更重。

因此本分支按 Wails-first 规划，但先不把 Wails 作为硬依赖引入，避免打断当前可测试链路。

## 分阶段计划

### Phase 0：可下载资源包

目标：生成一个 zip 资源包，技术用户解压后能运行。

交付物：

```text
dist/package/
  cascade-demoops-desktop.exe
  package-manifest.json
  resources/
    desktop-runtime.json
    web/
    sidecars/video-worker/dist/index.js

dist/release/
  CascadeDemoOps-<version>-windows-x64.zip
  CascadeDemoOps-<version>-windows-x64.zip.sha256
  CascadeDemoOps-<version>-windows-x64.manifest.json
```

验收：

- `pnpm package:desktop` 成功。
- `CASCADE_PROFILE=desktop dist/package/cascade-demoops-desktop.exe` 能加载 `desktop-runtime.json` 并初始化 `DesktopBridge`。
- release zip 可解压，checksum 可校验。
- `package-manifest.json` 列出入口、资源 manifest、文件 SHA-256 和服务器联通预留接口。
- `RuntimeConfig` 不泄露本地路径、API key 或 token。

当前状态：资源目录、portable zip、checksum 和 package manifest 已作为基线能力推进；仍需要补自动 smoke。

### Phase 1：桌面壳 MVP

目标：用户双击启动一个桌面窗口，而不是打开命令行和 Vite。

当前过渡实现：

- `cmd/desktop` 默认启动本地 Desktop Host，服务 `resources/web` 和同源 `/v1/desktop` / `/v1/editor` bridge API。
- `--check` 模式保留给 package smoke，初始化 bridge 后输出 JSON 并退出。
- `pnpm package:desktop` 编译前端时默认使用 `VITE_CASCADE_BRIDGE=local`，packaged UI 不再落回 mock bridge。

建议实现：

- 新增 Wails app 入口。
- 前端构建产物嵌入或放在 resources/web。
- Wails 绑定 `DesktopBridge` 方法。
- 前端 bridge 增加 `wails` backend adapter，和现有 `mock/local` 并存。
- 桌面 profile 默认 `APP_MODE=desktop`、`DATABASE_DIALECT=sqlite`。

验收：

- 双击打开窗口。
- 不启动 Vite，不启动 devserver。
- UI 能调用 `RuntimeConfig()`。
- 能本地生成三合一包。

### Phase 2：Windows 安装器

目标：生成可分发安装器。

当前过渡实现：

- `cmd/desktop-installer` 生成 Windows 自解压 setup exe，内嵌 portable zip payload。
- setup 会校验 payload SHA-256，安装到 per-user 目录，写入 `install-manifest.json`，并生成卸载脚本。
- `pnpm package:desktop:installer` 输出 `CascadeDemoOps-<version>-windows-x64-bootstrap.exe`、checksum 和 manifest。
- `pnpm smoke:desktop-installer` 会真实静默安装到 smoke 目录，验证 installed exe 的 `--check`、bundled Node 执行 video-worker JSON-RPC `health`、本地 Desktop Host 首页和 runtime-health。

建议实现：

- 用 Wails/NSIS/Inno 替换当前 setup baseline 或在其基础上补安装 UI。
- 产物包含 Go exe、web resources、video-worker、Node runtime、ffmpeg/ffprobe。
- 生成 installer 或 portable zip 两种形态：
  - `CascadeDemoOps-Setup-x.y.z.exe`
  - `CascadeDemoOps-portable-x.y.z.zip`
- 安装目录只读；用户数据进入 `%APPDATA%/CascadeDemoOps`。

验收：

- 当前 baseline：安装、启动和 video-worker Node runtime 不需要 Go、Node、pnpm；ffmpeg/ffprobe 仍需要后续随包分发。
- 首次启动能创建 data/artifacts/cache/logs。
- 卸载不误删用户项目数据，除非用户明确选择清理。

### Phase 3：密钥、配置和服务器接口预留

目标：保留服务器联通能力，但不把它做成本阶段硬依赖。

需要预留：

- Cloud Exchange base URL。
- installation identity metadata。
- server capability discovery 状态。
- result encryption key metadata。
- dev 明文通道标记。

本阶段不要求：

- 完整生产 KMS。
- 真实 app installation register/session。
- 自动更新服务器。
- 云端录制必须成功。

验收：

- 未配置服务器时 UI 显示“本地生成可用，服务器未连接”。
- 已配置 dev server 时能走现有 exchange lifecycle。
- 配置内容不进入三合一包明文，不进日志。

### Phase 4：发布工程

目标：具备团队内测和公开发布所需的工程能力。

待实现：

- 版本号和 build metadata 注入。
- Windows code signing。
- macOS notarization。
- 自动更新通道。
- crash/log bundle 导出。
- installer smoke。
- release artifact checksum。

## 资源清单规范

`resources/desktop-runtime.json` 是桌面包资源发现入口：

```json
{
  "app": "Cascade DemoOps",
  "resource_contract_version": 1,
  "sidecars": {
    "video-worker": "sidecars/video-worker/dist/index.js"
  },
  "runtimes": {
    "node": "runtimes/node/node.exe",
    "ffmpeg": "runtimes/ffmpeg/ffmpeg.exe",
    "ffprobe": "runtimes/ffmpeg/ffprobe.exe"
  },
  "web": "web"
}
```

当前 Windows package manifest 已写入 `node` runtime，Go runtime 会优先读取 manifest，再回退环境变量或系统命令。后续应继续扩展 `ffmpeg` / `ffprobe` runtime key。

## 本地数据目录

安装目录不得存储用户数据。默认用户数据目录：

- Windows：`%APPDATA%/CascadeDemoOps`
- macOS：`~/Library/Application Support/CascadeDemoOps`
- Linux：`~/.config/CascadeDemoOps`

目录结构：

```text
CascadeDemoOps/
  cascade_demoops.db
  orchestrator_state/
  artifacts/
  cache/
  logs/
  secrets/          # 后续仅存加密 metadata，不存 raw secret
```

## Bridge 分层

前端 bridge 推荐保留三种模式：

- `mock`：纯 UI 预览。
- `local`：浏览器 dev 模式，通过 HTTP 调 `127.0.0.1:4317`。
- `wails`：真实桌面模式，通过 Wails binding 调 Go `DesktopBridge`。

`wails` 模式不应依赖 devserver HTTP 端口。这样能避免端口占用、防火墙弹窗和本地代理干扰。

## 服务器接口预留

桌面端应只预留这些抽象，不把云端作为本阶段 blocker：

- `ExchangeCapabilityResolver`
- `ExchangeIdentityStore`
- `ExchangeSessionManager`
- `CloudLifecycleClient`

当前可继续使用 dev bearer token 兼容层；生产路径后续升级为 app installation session + envelope signature。无论哪种方式，App 端三合一包协议保持 `browser-agent-outline-v1`。

## 打包验收命令

当前应保持以下命令可用：

```powershell
pnpm --filter @cascade/web build
pnpm --filter @cascade/video-worker build
pnpm build:desktop:win
pnpm package:desktop
pnpm smoke:desktop-package
pnpm package:desktop:installer
pnpm smoke:desktop-installer
cd backend
go test ./...
```

后续新增：

```powershell
pnpm package:desktop:signed
```

## 下一步开发建议

1. 增加 `backend/cmd/desktop` 的 smoke test，验证 desktop profile 能读取 `dist/package/resources/desktop-runtime.json`。
2. 扩展 package smoke，覆盖 Desktop Host 首页和 runtime-health。
3. 扩展 installer smoke，覆盖卸载脚本、Start Menu launcher 和更深的 sidecar/FFmpeg 降级诊断。
4. 扩展 `DesktopResourceManifest` 支持 `ffmpeg` / `ffprobe` runtime key。
5. 增加前端 `wails` bridge adapter 的类型边界，但先用 mock binding 测。
6. 引入 Wails app skeleton，绑定 `DesktopBridge.RuntimeConfig()` 作为第一条真实链路。
7. 实现原生文件夹选择器和安全存储的接口占位。
8. 增加签名和自动更新前的 release smoke。

## 不做事项

本阶段不做：

- 完整云端自动配对。
- 生产 KMS/result encryption。
- 自动更新。
- 代码签名。
- 多平台安装器全量矩阵。
- 把 server browser agent 打进本地 App。

这些事项保留架构接口，等桌面壳和资源包稳定后再推进。
