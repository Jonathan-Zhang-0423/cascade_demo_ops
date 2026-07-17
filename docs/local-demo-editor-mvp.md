# Server Local Demo Editor MVP

> 状态：第一版纵向闭环已落地，真实成片仍需本机配置 FFmpeg 后验收。
> 范围：Server 本地素材编辑、预览和确定性导出，不依赖 Browser Agent 或双证据方案。

## 已实现

- `EditorSession` 文件化持久化和 revision 冲突保护；
- 本地视频素材探测：格式、时长、分辨率、帧率、编码、大小和 SHA-256；
- 素材目录与 `DemoEditPlan` 自动初始化；
- 片段裁剪、顺序调整、分割、删除和字幕编辑；
- Worker 侧独立 `probe_media`、`validate_edit_plan` JSON-RPC；
- 720p 快速预览和 1080p 最终导出参数；
- Go HTTP API、前端“视频编辑”入口和浏览器内视频读取；
- Seedance Provider 能力声明和候选素材安全边界。

## 本地流程

```text
新建 EditorSession
        ↓
输入 Server 本机视频路径
        ↓
ffprobe + SHA-256 素材探测
        ↓
生成 AssetTimelineCatalog 和默认片段
        ↓
前端调整入点、出点、顺序和字幕
        ↓
revision 保存 + DemoEditPlan 校验
        ↓
720p 预览或 1080p 最终 FFmpeg 渲染
        ↓
视频、Render Manifest、计划和验证报告
```

原始素材只读，编辑计划通过时间范围引用素材，不覆盖或重写源文件。
本地编辑器生成的计划使用 `source_authority=server_local_editor`；旧录制链路的 `customer_side_agent` 仍保持兼容，但不会冒充本地编辑来源。

## API

```text
GET  /v1/editor/sessions
POST /v1/editor/sessions
GET  /v1/editor/sessions/{session_id}
POST /v1/editor/sessions/{session_id}/assets
POST /v1/editor/sessions/{session_id}/plan
POST /v1/editor/sessions/{session_id}/validate
POST /v1/editor/sessions/{session_id}/preview
POST /v1/editor/sessions/{session_id}/render
GET  /v1/editor/sessions/{session_id}/media/preview
GET  /v1/editor/sessions/{session_id}/media/final
GET  /v1/editor/sessions/{session_id}/media/assets/{asset_id}
```

第一版的预览和最终渲染是同步调用。后台 Job、进度、取消和失败重试属于下一阶段。

## 运行条件

构建 Worker：

```powershell
pnpm build:worker
```

配置：

```dotenv
NODE_WORKER_PATH=../video-worker/dist/index.js
CASCADE_FFMPEG_PATH=ffmpeg
CASCADE_FFPROBE_PATH=ffprobe
```

如果 FFmpeg 没有加入 `PATH`，应填写 `ffmpeg.exe` 和 `ffprobe.exe` 的绝对路径。桌面安装包最终需要随应用携带这两个二进制文件。

启动本地 Bridge 和 Web：

```powershell
pnpm dev:bridge
pnpm dev:web
```

Web 运行时需要：

```dotenv
VITE_CASCADE_BRIDGE=local
```

## Seedance 扩展边界

编辑会话返回 Seedance 能力状态，但第一版不会从编辑器直接发起真实生成调用。后续接入时复用现有 Ark/Seedance 客户端、任务轮询、下载和 `CandidateAssetReview` 链路。

固定规则：

- Seedance 输出登记为 `generated_video_candidate`；
- `auto_include=false`；
- 必须完成人工审查和 Renderer 校验后才能加入时间线；
- 只能作为展示性素材；
- 不能绑定或代表真实业务步骤；
- Seedance 不可用时不影响本地确定性剪辑和导出。

## 下一阶段

1. 将同步渲染升级为可查询、可取消的后台 Render Job。
2. 增加桌面原生文件选择器，替代手工输入本机路径。
3. 增加撤销/重做和防抖自动保存。
4. 增加音频保留、音量控制和静音能力。
5. 完成 FFmpeg Windows 打包和字体一致性验证。
6. 将审查通过的 Seedance 候选素材接入素材区。
