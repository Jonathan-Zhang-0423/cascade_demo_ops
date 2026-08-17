# Server 视频质量与 Browser Agent 证据链开发计划 v1

状态：核心实现与回归测试通过，等待新鲜真实页面端到端验收
范围：仅 Server 与 video-worker；不修改 App 产包规则，不修改 Validation Agent 规则。

## 目标

1. Browser Agent 只录制产品网页内容视口，采集母版统一为 2560x1440、16:9、30fps。
2. WebM 保留为不可变浏览器证据原片；编辑交付统一为 MP4/H.264/AAC/yuv420p/CFR30。
3. 每个源画面最多经历一次有损视频编码；片段拼接和已合规交付归一化使用流复制。
4. 目标解析只在 App 批准的语义、selector 和 evidence 范围内增加冗余，不允许任意猜测目标。
5. Browser Agent 在动作前记录真实目标几何、视口、DPR、解析策略、时间戳及证据引用。
6. 编辑器只使用有证据的目标几何生成裁剪、缩放和圈选；缺失时报告 `missing_target_geometry`，不得生成固定坐标圈选。
7. 镜头计划必须记录素材、步骤、时间范围、模型 provider/model、真实调用状态、调用追踪和最终采用结果。
8. TOS 为模型提供短时有效的 HTTPS 预签名素材 URL；本地路径不能进入真实模型请求。
9. Doubao TTS 负责生成旁白候选音频，经过下载校验、媒体探测和审批后才可进入编辑时间线。

## 分批交付与验收

### 批次 1：采集与编码质量

- Browser Agent 强制使用 2560x1440 的网页内容视口。
- Server 在编辑前使用 FFprobe 校验原始 WebM；非 2560x1440 原片返回 `recording_evidence_resolution_mismatch`，禁止用后期放大冒充 2K 采集。
- 截图使用 `fullPage=false`，不录制桌面、浏览器外壳、地址栏或页面视口外的长页面。
- FFmpeg 片段保持同一媒体规格，concat 使用 stream copy。
- 已满足交付规格的合成 MP4 仅 remux/faststart，不再次有损编码。
- 使用 FFprobe 验证原片尺寸、最终编码、像素格式、帧率和音频。
- `DemoEditPlan.shots` 的有效时长总和必须与 `target_duration_ms` 在 5%/250ms 容差内一致；否则报告 `edit_plan_timeline_duration_mismatch`，Server 最终交付门禁拒绝该 MP4，禁止“镜头只有 35 秒但目标写 60 秒”的自洽短片被误报为满足。

### 批次 2：目标解析与几何证据

- 解析顺序覆盖 App 批准的 role/name、testid、label、CSS、text 和 selector alternatives。
- 精确语义失败时，只允许在同一 App evidence binding 内做规范化名称兼容和唯一目标修复。
- 持久化 CSS 像素坐标与 0..1 归一化坐标、视口、DPR、解析策略、动作时间戳和截图证据。
- 对敏感输入仅记录目标几何，不记录值、Cookie、Token 或页面 HTML。
- 已删除脱离 App evidence binding 的 Server 通用对话框/“构建”按钮猜测逻辑；目标修复仍限制在同阶段、同证据且唯一可见的候选范围内。

### 批次 3：证据驱动编辑

- 根据目标几何和安全边距计算构图，不使用通用固定 zoom/pan/highlight 坐标。
- 统一处理 crop、scale、pad 后的坐标变换。
- 缺少几何证据的镜头可保留，但不得绘制目标圈选。
- render manifest 记录每个覆盖层的证据来源和坐标变换结果。
- 已通过真实 FFmpeg 像素测试：无几何证据时不绘制圈选；有证据时，圈选在 zoom 后仍绑定正确目标。
- 预观察和正式动作执行都必须保存脱敏的 `target_resolution_attempts`；不能只记录预扫描策略而丢失真正点击/填写时的再次解析结果。

### 批次 4：模型素材与旁白

- 验证 TOS 上传、预签名 HTTPS GET、有效期和清理策略。
- 验证 Director 的 provider/model/real_call_made/provider_call 记录。
- Seedance 供应商视频先保留原件，再生成独立的标准化 MP4 派生文件；只有通过 FFmpeg 归一化、FFprobe 媒体门禁和候选审核的派生文件才可提出镜头补丁。
- Seedance 候选镜头补丁固定为 `AutoApply=false`，必须显式采用；模型被调用不等于输出被采用，最终审计分别记录两种状态。
- Seedance 下载或标准化失败按 `continue_without_generated_candidate` 降级，不阻断真实页面录屏和确定性成片，但必须写入可追溯告警。
- 对 Seedance 不接受的 WebM 证据母版，Server 生成独立的 5 秒 MP4 模型引用副本（1920x1080/H.264/yuv420p/CFR30），保留来源 artifact ID、SHA-256 和双探测结果；原始 WebM 不变。
- 本机旧版 FFmpeg 不支持 `-fps_mode cfr`，标准化器仅在识别到该特定错误时回退为 `-vsync cfr`，其他转码错误仍失败并记录。
- 已实现 OpenSpeech Token TTS 提交、轮询、受控下载、SHA-256、FFprobe 和脱敏候选结果；待填写语音服务凭据和音色后做真实短句验收。
- TTS 输出保持候选态，不自动写入正式时间线；确认并导入为 `narration_audio` 后，复用现有 `narrations[]` 时间线混音。

当前外部阻塞：本机尚未配置 `VOLC_TTS_APP_ID`、`VOLC_TTS_ACCESS_KEY`、`VOLC_TTS_RESOURCE_ID`、`VOLC_TTS_SPEAKER`。该项暂缓，不阻塞无旁白视频验收。

### 批次 5：端到端验收

- 使用 App 正式规则产出的不可变原包。
- Server 执行真实产品页面、采集 2K 原片、生成几何证据、编排镜头和旁白并输出 MP4。
- 交付录屏、截图、Trace、StepResults、stage events、镜头计划、模型调用审计、render manifest 和质量报告。
- 每项必须由实际产物或探测结果证明，fixture/mock/test waiver 不计为正式 App 到 Server 成功。
- 历史原片为 1440x900，只能用于 Server 回归，不能证明原生 2K；必须重新执行真实页面无人值守录制。

## 当前验证结果

- Server 核心包：`internal/app`、`internal/executor`、`internal/model`、`internal/orchestrator`、`internal/media` 全部通过。
- video-worker：TypeScript 构建通过，44/44 测试通过（包含 5 项真实 FFmpeg 静态镜头、圈选对齐和媒体测试）。
- TOS：私有上传、短时预签名 HTTPS GET、哈希校验和测试对象删除通过。
- 全功能历史素材回归：18 个镜头及 6 个历史阶段均进入渲染；最终 MP4 为 2560x1440/H.264/yuv420p/30fps，但历史源 WebM 仅 1440x900，因此不计入原生 2K 验收。
- 真实页面 Server 回归（`test_waiver_1786560452226268400`）：使用不可变正式 App 历史原包，自动登录后六阶段全部通过；原始 WebM 为原生 2560x1440，最终 MP4 为 2560x1440/H.264/yuv420p/AAC，14 张截图、Trace、StepResults、stage events、render manifest 和质量报告齐全。
- 上述回归属于本地测试豁免路径，`dev_test_only=true`、`formal_exchange=false`，证明 Server 真实页面执行和成片能力，不代表 Direct Exchange 生产传输验收。
- 本地真实素材已成功生成 5 秒 Seedance 引用 MP4（1920x1080/H.264/yuv420p/30fps）。真实 TOS/Seedance 出站回归尚未授权，因此不得声明 Seedance 已参与本轮最终视频。
- 最新正式 App 产包 `pkg_bundle_script_graph_1786559723307775100` 缺失“填写项目需求”动作，原包审计已阻止执行；这是 App Outline 完整性问题，Server 未修改或补写 App 包。
- 历史 App 包虽然六阶段完成，但最终观察主要停留在项目预览/Welcome 状态，说明“业务构建完成”的可观测判定偏弱，需要 App 侧增强成功条件与证据。

### 2026-08-13 真实页面回归

- 使用不可变正式 App 历史原包 `pkg_bundle_script_graph_1786481557655834800`，经同范围本地测试豁免执行；未修改 App 原包、App 产包规则或 Validation Agent 规则。
- 最终成功回归：`test_waiver_1786563531040170300`，六个阶段全部通过，真实页面进入项目路由并完成证据采集与渲染。
- 修复了 Worker 关闭会话时重建截图引用却丢失 `target_geometry` 的问题；目标几何现在按 artifact ID 精确恢复，未知或普通截图不会获得坐标。
- 修复了静态圈选镜头只校验“同一步”却未校验“同一张截图”的问题。三个动作镜头的 `source_artifact_id` 均与各自 `target_evidence_artifact_id` 完全相同；跨图圈选返回 `target_geometry_source_mismatch` 并停止绘制。
- 修复了静态镜头被视频时长裁剪器全部移除的问题；真实恢复类型 `screenshot` 与 `target_geometry_screenshot` 均有回归覆盖。
- 最终 MP4：`2560x1440`、H.264、yuv420p、30fps、AAC、30.145 秒；render manifest 实际应用 `caption`、`highlight_box`、`cursor_highlight`、`concat`，无跳过的圈选操作。
- 模型审计明确为 `invoked=false`、`suggestion_origin=deterministic_server_director`、`provider_output_adopted=false`。本轮未调用 Seedance，不能把该视频描述为 Seedance 编排结果。
- 该回归仍为 `dev_test_only=true`、`formal_exchange=false`，只证明 Server 真实页面执行、证据驱动编排和 2K MP4 交付，不代表生产 Direct Exchange 已验收。

### 2026-08-13 离线安全静态编辑回归

- 修复静态截图编排只使用每阶段单张截图、导致 60 秒交付意图被压缩为约 30 秒的问题：现在按阶段顺序选择不同的已批准、非敏感截图，并均匀分配目标时长；不重复使用同一截图，不用重复帧伪造业务过程。
- 修复同一阶段不同截图之间的目标几何继承：只有截图自身携带匹配 `target_geometry` 时才绘制圈选；`after`/`revalidate` 截图不会继承 `target` 截图的圈选。
- 新增 `video-worker/scripts/accept-safe-still-editor-local.mjs`，可对已有 `asset_timeline_catalog.json` 做离线 Server 编辑验收，不启动 Chromium、不调用 Browser Agent、不调用模型、不解除 raw recording 敏感标记。
- 使用真实页面回归 catalog 离线生成 9 个唯一安全截图镜头，编辑计划 60 秒，实际 MP4 60.208667 秒；FFprobe：2560x1440、H.264、yuv420p、30fps、AAC。模型审计为 `invoked=false`。
- 该结果证明 Server 静态素材编辑与质量门禁，不代表重新录制的正式 App→Server 端到端验收，也不代表 Seedance/TOS/TTS 已参与。

### 暂缓项

- Doubao TTS 缺少 `VOLC_TTS_APP_ID`、`VOLC_TTS_ACCESS_KEY`、`VOLC_TTS_RESOURCE_ID`、`VOLC_TTS_SPEAKER`，暂缓真实旁白验收；无旁白交付不受影响。
- 真实页面素材上传 TOS 并发送给 Seedance 属于外部数据出站，尚无本轮明确授权，暂缓真实 Seedance 候选素材验收。
- 最新正式 App 包缺少项目需求输入步骤，且历史包的“构建完成”可观测条件偏弱；继续记录为 App 侧问题，不在 Server 侧篡改包或放宽 Validation。
- 原包要求 60 秒、1920x1080，但本轮 Server 安全静态编排实际为约 30 秒、2560x1440。质量报告已保留该意图差异；后续需统一交付时长和 2K 输出协议口径。

## 外部信息止损规则

同一外部阻塞连续三轮仍无法通过时，记录失败命令、错误、影响、所需信息和恢复入口，然后继续不依赖该信息的批次。不得伪造外部调用或把 dry-run 标记为真实成功。
