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
- 默认模型候选路由为 Seedance；任务在预算获得客户端确认且能力门禁通过后自动发起候选生成。自动发起不等于自动改变业务事实或跳过候选采用门禁。
- 对 Seedance 不接受的 WebM 证据母版，Server 生成独立的 5 秒 MP4 模型引用副本（1920x1080/H.264/yuv420p/CFR30），保留来源 artifact ID、SHA-256 和双探测结果；原始 WebM 不变。
- 本机旧版 FFmpeg 不支持 `-fps_mode cfr`，标准化器仅在识别到该特定错误时回退为 `-vsync cfr`，其他转码错误仍失败并记录。
- 已实现 OpenSpeech Token TTS 提交、轮询、受控下载、SHA-256、FFprobe 和脱敏候选结果；待填写语音服务凭据和音色后做真实短句验收。
- TTS 输出保持候选态，不自动写入正式时间线；确认并导入为 `narration_audio` 后，复用现有 `narrations[]` 时间线混音。

当前外部阻塞：本机尚未配置 `VOLC_TTS_APP_ID`、`VOLC_TTS_ACCESS_KEY`、`VOLC_TTS_RESOURCE_ID`、`VOLC_TTS_SPEAKER`。该项暂缓，不阻塞无旁白视频验收。

### 2026-08-18 已审核媒体输入与模型编排方案（待实现）

以下方案已经完成产品侧审核，进入 Server 后续开发计划；本节描述目标设计，不代表当前代码已经全部实现。

#### 原始录屏与网页全屏证据

- Browser Agent 采集浏览器页面内容视口，不录制桌面、浏览器外壳、地址栏或其他应用窗口。
- 原始证据母版固定保留完整产品页面内容视口，目标规格为 `2560x1440/16:9`；模型和编辑器不得覆盖或回写原始证据。
- 页面本身存在居中或最大宽度布局时，原始证据仍如实保留；后续通过经验证的构图派生镜头改善有效业务区域占比，不得以拉伸页面冒充网页全屏。
- 建立四层资产：`raw_recording.webm`、无编辑的 `recording_reference.mp4`、`model_reference_segments/` 和最终 `final_demo.mp4`，禁止把已经带缩放或覆盖层的成片重新标记为模型参考母版。

#### 输入模型前的合法片段拆分

- 先按业务 Stage、动作和可观测结果做语义切分，再按 Provider 时长、数量、编码、分辨率和大小限制生成模型派生片段；禁止只按固定秒数机械切割。
- Seedance 引用视频统一派生为 `MP4/H.264/yuv420p/1920x1080/CFR30`，单片段和单请求必须满足当前 Provider 能力边界。
- 每个派生片段保留 `source_artifact_id`、`source_start_ms`、`source_end_ms`、SHA-256、业务 Stage、派生参数和双 FFprobe 结果，保证能够还原到原始录屏。
- Provider 限制不满足时在发起网络请求前拒绝，并回退到真实素材的确定性编辑路径。

#### 模型镜头请求语义

- 模型请求采用 Provider-neutral 的结构化描述，同时允许文本、视频和截图组合输入。
- 视频必须区分 `reference_generate`（视频仅作动作、节奏或风格参考）和 `video_edit`（要求在原视频基础上处理）。
- 图片必须区分 `image_reference`（图片仅作主体、布局或视觉参考）和 `image_edit`（要求在原图基础上修改）。
- 每个请求必须携带 `preserve_requirements`，明确 UI 文本、业务状态、页面身份、布局和敏感区域中哪些内容不可改变。
- 即使 Provider 声明支持视频或图片编辑，也不得默认其能够精确保留产品 UI 和业务事实；核心业务镜头仍以真实 Browser Agent 证据为权威，生成式结果保持 `non_authoritative=true`、`presentation_only=true`。

#### 模型候选编排与确定性执行

- 模型不直接控制 Renderer，也不只返回无法校验的自然语言建议；模型输出结构化 `CandidateEditPlan`。
- `CandidateEditPlan` 至少包含素材与时间范围、对应业务步骤、镜头目的、构图/字幕/转场/时长建议、不可变区域、置信度、风险和失败回退方案。
- Server 按“App 包硬约束 → 真实 Browser Agent 证据 → Provider 合法性 → 模型软建议 → 编辑器质量门禁”的顺序合并约束。
- Server 仅将通过字段白名单、required-step 覆盖、证据绑定、时间轴、敏感信息和媒体质量校验的候选内容编译为不可变 `DemoEditPlan`。
- Renderer 只执行经过 Server 验证的 `DemoEditPlan`，不得在渲染阶段自行解释模型自然语言或补猜业务动作。
- 模型不可用、超时、返回非法计划或候选素材不合格时，必须回退到只使用真实录屏和截图的确定性成片路径，不阻断基础 MP4 交付。
- `render_manifest` 分别记录模型是否调用、模型建议、实际采用字段、拒绝原因、回退原因以及最终执行结果；“调用成功”不等于“候选被采用”。

#### 质量门禁补充

- `target_geometry_verified=false` 或 `target_geometry_time_mismatch` 时，禁止目标放大、圈选和指针强调，回退为完整页面镜头。
- 局部镜头必须基于真实目标几何动态计算安全裁剪，保留目标、必要业务上下文和字幕安全区；禁止固定倍数裁剪导致页面残缺。
- 正式拼接前生成代表性预览帧，校验裁剪完整性、目标对齐、文字可读性、覆盖层冲突和字幕安全区。
- 最终审计记录原始坐标、归一化坐标、裁剪变换、实际输出坐标、预览校验结论和回退原因。

#### 模型来源、预算与客户端确认

- 每个模型候选和最终镜头必须记录 `provider`、`model`、`task_id`、`request_id`、`real_call_made`、`source_artifact_ids`、原始/标准化 artifact ID 与 SHA-256、`provider_output_adopted`、`auto_applied`、`presentation_only` 和回退原因。
- `render_manifest` 记录机器可读的模型来源；最终视频可选增加片尾或交付报告中的模型来源摘要，但不默认在业务画面上叠加水印。
- Server 在调用前生成预算报价，至少包含 Provider、模型、请求数量、输入时长、输出时长、重试预留、TOS 存储/出站和 TTS 费用估算、估算区间、封顶金额、有效期和假设条件。
- 客户端确认绑定预算摘要哈希、任务输入哈希和过期时间；预算变化、素材变化或重试超过预留额度时必须重新确认。未确认不得发起真实 Provider 请求。
- 自动生成默认只适用于非事实展示候选；核心业务步骤仍必须由真实录屏、截图和确定性编辑计划覆盖。候选通过全部质量门禁后，是否自动写入最终时间线仍由单独的 `auto_apply` 策略控制。

#### 初始质量门禁建议值（待真实样本校准）

- 画面规格：原始证据必须为 `2560x1440/16:9`；模型派生和 Seedance 输入为 `1920x1080/H.264/yuv420p/CFR30`；最终从同一份 `DemoEditPlan` 同时交付 `2560x1440` Master 和 `1920x1080` 分发版，不允许播放器自动拉伸或分别生成两套不一致的时间线。
- 时长与帧率：CFR 目标 30fps，帧率偏差不超过 `0.1fps`；镜头时间线误差不超过 `250ms` 或目标时长的 `5%`（取较大者）；禁止空镜头和不可解码帧。
- 裁剪与几何：目标几何必须已验证且与截图 artifact、动作 Stage 和时间范围一致；输出画面四边保留至少 `32px` 安全边距，目标不得被裁切；未验证几何一律回退全画面。
- 业务焦点镜头：目标框宽度至少为输出宽度的 `12%` 或 `160px`（取较大者），高度至少为输出高度的 `5%` 或 `48px`（取较大者）；全页面镜头不强制放大，但必须保持页面身份和业务上下文。
- 覆盖层：圈选、指针和字幕只能绑定同一 evidence artifact；覆盖层不得遮挡目标或敏感区域，字幕安全区建议为底部 `10%`、左右各 `2.5%`；正式拼接前至少抽检首帧、中帧、尾帧。
- 生成候选：不得改变 required-step 顺序、业务文字、业务状态或成功结果；无法通过内容/几何/媒体门禁时标记拒绝并回退真实素材，不把模型生成 UI 当作事实证据。
- 以上数值作为第一版硬门禁，完成三轮真实页面样本后再按误拒率和漏检率校准；校准必须保留版本号，不得静默改变。

#### 双规格最终交付（已审核，待实现）

- `final_master_2k.mp4`：`2560x1440/16:9/H.264 High/yuv420p/CFR30/BT.709/faststart`，建议视频码率 `16-25 Mbps`；作为高质量验收和归档主文件。
- `final_delivery_1080p.mp4`：`1920x1080/16:9/H.264/yuv420p/CFR30/BT.709/faststart`，建议视频码率 `8-12 Mbps`；作为网页预览、模型读取和通用分发文件。
- 有旁白时两个文件均使用 `AAC-LC/48kHz/192kbps`；无旁白时允许不创建音频轨，但两个交付文件的音频策略必须一致。
- 两个文件必须来自同一 `DemoEditPlan` revision、同一素材集合和同一模型采用决定，业务步骤、字幕、镜头顺序和总时长必须一致；只允许输出分辨率和编码码率不同。
- `render_manifest` 分别记录两个文件的 artifact ID、SHA-256、媒体探测结果、编码参数和与计划 revision 的绑定关系。
- 1920x1080 模型候选进入 2K Master 时必须记录 `source_resolution`、`timeline_resolution`、`upscaled=true` 和 `native_2k=false`，不得把放大结果声明为原生 2K。
- 任一规格渲染或门禁失败时，整个双文件交付状态不得标记为完成；允许保留已成功文件用于诊断，但必须在结果包中明确另一规格失败。

#### TTS 用户决策与 TOS 生命周期建议

- TTS 未被用户明确要求时，默认采用 `auto_by_product_style`：Server 根据产品风格、目标受众和已批准业务 Stage 生成步骤旁白候选；用户明确选择无旁白、定制旁白、背景音乐或上传音频时，显式选择优先于默认策略。
- 旁白必须绑定 `stage_id`/`shot_id` 和时间范围；生成后检查可解码性、时长、响度和峰值，建议目标为 `-16 LUFS`、峰值不高于 `-1 dBTP`，不匹配时调整或拒绝。
- 推荐 TOS 默认采用“私有、按任务隔离、短时签名 URL”策略：模型输入只上传必要的标准化派生片段，不上传完整原始母版；签名 URL 有效期 `15` 分钟。
- TOS 默认生命周期统一为 `standard_30d`：同一任务的原始证据、标准化派生素材、模型候选、最终交付、哈希、任务状态、调用审计和错误日志默认保留 `30` 天；到期后按任务前缀执行清理。
- 客户端必须在任务确认界面展示“所有任务内容默认保存 30 天”，并回传已知悉确认；可选择自定义保留天数。Server 已提供项目级媒体交付偏好接口；后续接入真实 TOS 出站编排时，执行器必须以该确认作为启动门禁。
- 严格模式、标准模式和自定义模式均不得公开 Bucket 或长期暴露原始凭据；失败、取消和超时任务必须执行同样的清理流程。

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
- 原包要求 60 秒、1920x1080，但本轮 Server 安全静态编排实际为约 30 秒、2560x1440。质量报告已保留该意图差异；输出规格现已统一为同计划双文件交付，后续仍需由 App 意图或用户要求确定目标时长。

## 外部信息止损规则

同一外部阻塞连续三轮仍无法通过时，记录失败命令、错误、影响、所需信息和恢复入口，然后继续不依赖该信息的批次。不得伪造外部调用或把 dry-run 标记为真实成功。
