# Server Local Demo Editor MVP

> 状态：第一版纵向闭环、真实 FFmpeg 成片、结果包导入和后台渲染均已验收。
> 范围：Server 本地素材编辑、预览和确定性导出，不依赖 Browser Agent 或双证据方案。

## 已实现

- `EditorSession` 文件化持久化和 revision 冲突保护；
- 本地视频素材探测：格式、时长、分辨率、帧率、编码、大小和 SHA-256；
- 素材目录与 `DemoEditPlan` 自动初始化；
- 片段裁剪、顺序调整、分割、删除和字幕编辑；
- Worker 侧独立 `probe_media`、`validate_edit_plan` JSON-RPC；
- 720p 快速预览和 1080p 最终导出参数；
- Go HTTP API、前端“视频编辑”入口和浏览器内视频读取；
- `RecordingResultPackage` 到 `EditorSession` 的导入，保留步骤顺序、时间范围、来源和默认字幕；
- 后台预览/导出任务、阶段进度、轮询和取消，取消会终止 Node/FFmpeg 进程树；
- 浏览器文件选择和 Server 管理目录流式上传；
- 最多 50 个编辑计划快照的撤销/重做，以及 900ms 防抖自动保存；
- 源音轨保留、0-200% 全局音量和静音模式；音频轨支持按播放头分段、分段静音/音量、恢复和合并边界，无音轨素材会补兼容静音轨；
- 源素材预览实时应用最终输出时间线上的分段静音和 0-100% 音量；101-200% 在浏览器中限制为 100%，最终 FFmpeg 导出仍应用声明的实际增益；
- Worker 通过 `analyze_audio` 将源音轨解码为 1 kHz 单声道 PCM，按默认 100 ms 桶计算 peak/RMS dBFS、静音区间和可审查候选；时间线按 shot 的裁剪、重排和重复映射波形，不拉伸整段源音频；
- Seedance Provider 能力声明和候选素材安全边界。
- OpenCut 风格的本地编辑工作台：素材区、源素材/渲染预览切换、片段属性、业务证据轨、视频/字幕/音频轨和导出校验抽屉；
- 导入弹窗统一承载本机上传、Server 路径、`RecordingResultPackage` 和空项目创建，不再让路径表单常驻主画面；
- 最终导出前同时执行前端业务约束检查和 Worker `DemoEditPlan` 校验；必需步骤遗漏/乱序、生成候选冒充业务步骤、未实现的像素操作均阻止导出。
- 视频轨支持拖动两侧把手裁剪、拖动片段排序、拖动播放头定位、30 FPS 帧吸附和步骤/素材边界优先吸附；拖动期间不触发自动保存，松手只形成一个撤销快照；
- `DemoEditPlan` 可编译为只读帧级 `PresentationComposition`，作为后续 Remotion Player 预览的唯一输入，当前不会反向替代业务协议或 FFmpeg 最终渲染器。

## 本地流程

```text
新建 EditorSession
        ↓
输入 Server 本机视频路径，或导入 RecordingResultPackage
        ↓
ffprobe + SHA-256 素材探测
        ↓
生成 AssetTimelineCatalog 和默认片段
        ↓
前端调整入点、出点、顺序、字幕和分段音频
        ↓
revision 保存 + DemoEditPlan 校验
        ↓
业务证据完整性、步骤顺序和 Renderer 能力检查
        ↓
后台执行 720p 预览或 1080p 最终 FFmpeg 渲染
        ↓
视频、Render Manifest、计划和验证报告
```

原始素材只读，编辑计划通过时间范围引用素材，不覆盖或重写源文件。
本地编辑器生成的计划使用 `source_authority=server_local_editor`；旧录制链路的 `customer_side_agent` 仍保持兼容，但不会冒充本地编辑来源。
自动保存继续使用 `expected_revision` 乐观锁；保存期间发生的新编辑不会被旧请求返回值覆盖。预览或导出前必须先保存当前草稿，避免渲染旧 revision。

编辑器页面继续运行在 `http://127.0.0.1:3000/`：用户点击左侧“视频编辑”后进入全高本地工作台。视频轨按最终输出时间连续排列，片段仍通过 `source_time_range_ms` 引用只读源素材；点击时间线会将输出时间映射回对应素材时间。结果包项目额外显示业务步骤证据轨；必需步骤的最后一个证据片段不能直接删除，前移/后移也不能改变必需步骤顺序。

时间线裁剪把手只修改片段的源入点/出点，拖动片段主体只修改展示顺序。所有拖动在 Pointer Up 时才写入一条历史记录，按 `Escape` 可放弃当前拖动，避免连续 Pointer Move 产生大量 revision。显式素材边界和步骤边界在阈值内优先于普通帧吸附，否则统一吸附到最终输出 FPS 的最近帧。

对于结果包项目，导出校验要求每个必需步骤的 `[start_ms, end_ms]` 被一个或多个同 `source_step_id` 片段连续完整覆盖。当前协议只提供步骤级粗粒度区间时，编辑器只执行这一保守检查，不推测点击关键帧或自行生成更高精度证据。

`PresentationComposition` 使用最终输出的 FPS、宽度和高度，把毫秒计划转换为视频、音频、字幕和标注序列，保留 `source_artifact_id`、`source_step_id` 和源帧范围。它是 Remotion 风格的预览中间表示，但当前最终成片仍由 FFmpeg Worker 负责，避免两套最终渲染语义。

页面只开放当前确定性 Renderer 已实现的裁剪、拼接、字幕和音频能力。模型中预留但尚未烧录到像素的 `zoom_pan`、高亮和模糊等操作会在导出校验中作为阻断项，而不是显示一个实际无效的编辑按钮。

`edit_plan.audio` 使用 `mode=source|mute` 和 `volume_percent=0..200` 声明全局默认值。`split_points_ms` 声明最终输出时间线上的音频编辑边界；`segment_settings` 以稀疏覆盖方式声明某个完整音频段的 `start_ms`、`end_ms`、`mode` 和 `volume_percent`。旧计划未声明时按 `source/100` 处理。

删除音频段等价于将该段设置为静音，不缩短视频时间线，因此不会造成音画错位。分割已有设置的音频段时，左右两段继承原设置；合并边界时以前一段设置为准。校验器要求覆盖范围严格对齐 `split_points_ms` 或时间线首尾、互不重叠、按时间递增且音量处于 0-200%。FFmpeg 按最终输出时间把这些设置映射到每个源视频片段，统一输出 48 kHz 双声道 AAC；无源音轨或全局静音时补兼容静音轨。

“分析音频”只产生客观信号数据和自动静音候选，不自动修改 `DemoEditPlan`。候选的“信号置信度”由静音阈值与区间平均 RMS 的差值确定，只表示该区间接近数字静音的程度，不表示业务步骤正确性。用户点击“静音”后才会在候选首尾增加 `split_points_ms` 并写入 `segment_settings`；点击“忽略”只影响当前页面，不改计划。仅凭音频删除停顿会改变视频时长并可能损害业务证据，因此当前不执行这类自动动作。

## 步骤截图展示片段（第一版）

结果包中已安全登记的步骤截图会以 `kind=step_screenshot`、`metadata.presentation_only=true` 存入素材区。用户可预览截图，再明确点击“加入时间线”；系统不会自动插入，也不会把截图视为业务动作完成的证据。

加入后对应的 `DemoEditShot` 使用：

```json
{
  "presentation_kind": "still",
  "output_duration_ms": 1500
}
```

静态片段仅允许引用非敏感、`include_in_demo=true` 的本地 PNG/JPEG 步骤截图，展示时长范围为 250–15000ms。它可以在视频轨排序、删除和添加字幕，并可拖动片段右边缘或填写属性面板调整展示时长；第一版不支持分割、源入/出点裁剪或自动插入。截图尾部改变时，位于其后的音频编辑边界会同步平移，位于截图内部的边界保持在原输出位置。`source_step_id` 只用于追溯截图来源；业务步骤覆盖与必需步骤顺序校验会忽略该静态片段。

FFmpeg 渲染时将图片循环为目标分辨率和 FPS 的视频帧，同时补入 48kHz 立体声静音音轨，再与录屏片段 concat。因此静态画面可安全插入有声或静音的成片时间线。

## API

```text
GET  /v1/editor/sessions
POST /v1/editor/sessions
POST /v1/editor/sessions/from-result-package
GET  /v1/editor/sessions/{session_id}
POST /v1/editor/sessions/{session_id}/assets
POST /v1/editor/sessions/{session_id}/uploads
POST /v1/editor/sessions/{session_id}/plan
POST /v1/editor/sessions/{session_id}/validate
POST /v1/editor/sessions/{session_id}/preview
POST /v1/editor/sessions/{session_id}/preview/cancel
POST /v1/editor/sessions/{session_id}/render
POST /v1/editor/sessions/{session_id}/render/cancel
GET  /v1/editor/sessions/{session_id}/media/preview
GET  /v1/editor/sessions/{session_id}/media/final
GET  /v1/editor/sessions/{session_id}/media/assets/{asset_id}
```

预览和最终渲染均为后台任务。启动接口立即返回 `job_id`、`phase` 和当前状态，前端轮询会话；取消通过 Go context 终止 Node worker 及其 FFmpeg 子进程。当前 JSON-RPC 尚未返回 FFmpeg 帧级事件，因此进度只表达排队、校验、渲染和完成阶段，不伪造逐帧百分比。

结果包中的 `raw_recording` 如果是远程或加密 URI，调用方必须同时提供已下载、已解密的 Server 本机路径。编辑器不会假设远程 URI 可直接读取。

`/uploads` 接收 multipart 字段 `file`，支持 `mp4`、`webm`、`mov`、`m4v`，单文件上限 8 GiB。文件流式写入会话的 Server 管理目录，使用随机文件名和原子重命名；成功后复用 SHA-256、FFmpeg/FFprobe 探测和时间线导入链路，失败时清理临时文件。

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

如果开发机只有 `ffmpeg.exe`，素材导入会回退到 FFmpeg 的输入元数据解析；最终安装包仍建议同时携带 `ffprobe.exe`，以获得更稳定、结构化的媒体校验结果。

一键启动本地 Bridge 和 Web：

```powershell
powershell -ExecutionPolicy Bypass -File scripts/start-local-editor.ps1
```

固定入口为 `http://127.0.0.1:3000`，Bridge 为 `http://127.0.0.1:4317`。停止命令为 `scripts/stop-local-editor.ps1`。在需要前台监督子进程的终端环境中可追加 `-Wait`。

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

1. Windows 应用控制解除后重建 Bridge，验收 `segment_settings` 保存/重载。
2. 完成 FFmpeg Windows 打包和字体一致性验证。
4. 将审查通过的 Seedance 候选素材接入素材区。
