# Final Film 代码框架与复用评估 v1

## 1. 结论

最终成片链路应保持两条严格分离的轨道：

1. **事实轨**：Browser Agent 真实录屏、截图、验证结果和用户确认的音频，只允许由 `DemoEditPlan` 与 FFmpeg 确定性处理。
2. **展示轨**：片头、片尾、章节转场、抽象 B-roll 和品牌氛围等非事实镜头，可由 Seedance、MiniMax-H3、Aleph 等 Provider 生成或修改，但必须作为候选经过审核后才能进入时间线。

现有仓库已经实现了大部分底层安全组件。按能力模块估算约 **70% 可复用**；但按已经接入生产主链路估算约 **45% 已连通**。本阶段不应重写候选协议、编辑计划或 Renderer，而应增加一个 Server 侧 `FinalFilmService`，把已有模块编排成持久化、可恢复、可审计的作业链。

## 2. 当前实现与复用比例

| 层 | 当前实现 | 复用判断 | 估算 |
| --- | --- | --- | ---: |
| 素材目录与事实绑定 | `AssetTimelineCatalog`、真实步骤时间范围、Artifact hash | 直接复用 | 90% |
| 确定性编辑计划 | `DemoEditPlan`、locked fields、required step order | 直接复用，补 plan revision/constraint ref | 85% |
| Director 输入与受限 Patch | `DirectorInput`、Suggestion、Patch Builder、Patch Validator | 复用；拆掉其中的 Provider 调用职责 | 70% |
| Provider-neutral 意图 | `GeneratedShotIntent`、Preflight、Capability Profile | 复用；把 4–15 秒公共硬编码下沉到 Provider Profile | 75% |
| MiniMax-H3 | Client、Admission、Harness、轮询、下载、规范化、结构审核 | 直接复用并接入正式 Job Runner | 80% |
| Seedance | Ark Client、真实任务调用、轮询、候选下载、2.0 Compiler | 复用传输层；新增 2.5 Profile/Compiler | 60% |
| Aleph 2 | 尚无专属 Adapter | 复用下载、规范化、候选、审核和配额框架 | 20% |
| 候选审核与选择 | Structural Review、Human Content Review、Candidate Set、Selection、Editor Approval | 直接复用并接 API | 85% |
| 编辑器候选门禁 | 候选媒体检查、显式用户审批、禁止绑定业务步骤 | 直接复用 | 80% |
| FFmpeg Renderer | trim/concat、静帧、字幕、音频、旁白、画面操作、候选门禁 | 直接复用；后续再拆文件 | 90% |
| 作业编排与恢复 | 有 dry-run Orchestration Record，但禁止真实 Provider/Editor/Renderer 权限 | 新建 production job state machine | 30% |
| 持久化 | EditorSession 为本地 JSON；DB 有通用 jobs/assets/reviews/render_jobs | 复用表思想，新增 final-film 专用关联与幂等记录 | 40% |
| 前端 | 已有候选审核和时间线编辑 | 复用；新增生成请求、进度、A/B 选择和失败回退 UI | 65% |

## 3. 当前必须修正的结构问题

### 3.1 Director 与视频 Provider 职责混合

当前 `executor/director_adapter.go` 既产生确定性的 Director 建议，也可直接提交 Seedance 任务。规划模型不应拥有 Provider 调用权。需要拆成：

```text
DirectorPlanner
  -> DirectorEditSuggestion
  -> DirectorEditPlanPatchBuilder
  -> DirectorEditPlanPatchValidator

GeneratedShotService
  -> ProviderRouter
  -> ProviderAdapter
  -> CandidatePipeline
```

`DirectorPlanner` 只输出建议；`GeneratedShotService` 只实现已经通过约束编译和 Patch 校验的展示镜头意图。

### 3.2 两套展示镜头意图合同重复

仓库同时存在 App/Exchange 的 `PresentationGenerationIntent` 和 Server 内部的 `GeneratedShotIntent`。两者边界合理，但缺少唯一编译入口：

```go
type PresentationIntentCompiler interface {
    Compile(
        appIntent model.PresentationGenerationIntent,
        constraints model.StoryboardConstraintSet,
        catalog model.AssetTimelineCatalog,
    ) (media.GeneratedShotIntent, model.IntentCompilationReport)
}
```

禁止 HTTP Handler、Director 或 Provider Adapter 自行转换这两个类型。

### 3.3 公共意图混入 Provider 交集限制

当前 `GeneratedShotIntent` 把时长锁在 4–15 秒、引用数量锁在 4 个。这与当前 H3/Seedance 2.0 展示镜头 Profile 一致；Aleph 2 的 2–30 秒 V2V 编辑应使用后续独立的 localized-edit intent，不能把更宽的编辑权限混入展示镜头合同。

调整原则：

- 公共合同只表达用户请求和安全政策；
- Provider Profile 表达供应商的时长、格式、引用类型和音频能力；
- Router 根据 Profile 做兼容性预检；
- App 只看到 Server 聚合后的稳定 Capability，不看到模型 ID、endpoint 或密钥。

### 3.4 现有 Orchestration 仅能做诊断

`GeneratedShotOrchestrationRecord` 明确禁止 Provider 调用、Editor 写入和 Renderer 执行，因此不能直接扩成生产 Job。保留它作为 dry-run/preflight 审计记录，新建可持久化的 `FinalFilmJob`，其每一步权限来自已保存的 gate，而不是布尔字段被任意翻转。

### 3.5 H3 专属规范化应抽成公共组件

H3 已完成 `ffprobe -> FFmpeg -> ffprobe`，输出锁定为 MP4/H.264/yuv420p/1920x1080/CFR30。应把实现抽成 Provider-neutral `MediaNormalizer`；H3、Seedance 2.0、Aleph 2 都调用同一实现，Provider 目录只保留结果转换。

## 4. 推荐目录结构

先接线、后搬迁。第一阶段不移动已有大文件，避免破坏已经通过的测试。

```text
backend/internal/
  finalfilm/
    service.go                 # 唯一应用编排入口
    job.go                     # FinalFilmJob 与状态迁移
    job_runner.go              # 可恢复的单步执行器
    store.go                   # Store 接口与幂等/CAS 约束
    router.go                  # 按用途、能力、成本和可用性选路
    intent_compiler.go         # App Intent -> GeneratedShotIntent
    constraint_compiler.go     # App/Plan/Catalog -> StoryboardConstraintSet
    baseline_builder.go        # 生成确定性 edit-plan draft
    candidate_pipeline.go      # normalize/review/set/select/approval/patch
    errors.go                  # 稳定错误分类

  model/
    storyboard_constraint.go   # 新增硬约束合同
    final_film_job.go          # 新增持久化作业合同
    demo_edit_plan.go          # 复用，后续增加 constraint/revision refs
    director_input.go          # 复用

  executor/
    director_planner.go        # 从现有 director_adapter 拆出纯规划接口
    director_edit_plan_patch.go# 原样复用

  media/
    generated_shot_*.go        # 现有 provider-neutral 合同/审核/选择
    normalizer.go              # 从 H3 normalizer 抽公共实现
    provider_adapter.go        # 新增统一 Provider Adapter 接口
    provider_registry.go       # 新增 Adapter 注册表
    minimax_h3_*.go            # 现有实现
    seedance_20_*.go           # 现行 Profile/Compiler/Adapter
    runway_client.go           # Aleph/Seedance via Runway 的共享 transport
    aleph2_*.go                # 局部 V2V 编辑 Adapter

  app/
    final_film_service.go      # HTTP/Desktop bridge 边界
    final_film_http.go         # 创建、查询、取消、审核、选择
    editor_service.go          # 继续管理最终时间线和渲染

video-worker/src/
  renderer.ts                  # 第一阶段保持不动
  media-probe.ts               # 复用
  # 第二阶段再拆：
  timeline-catalog.ts
  edit-plan-validator.ts
  ffmpeg-compositor.ts
  render-manifest.ts

frontend/web/src/
  finalFilm.ts                 # API 类型与 client
  FinalFilmPanel.tsx           # 作业进度、候选、失败回退
  VideoEditor.tsx              # 复用候选审核与时间线
```

## 5. 核心合同

### 5.1 StoryboardConstraintSet

```go
type StoryboardConstraintSet struct {
    SchemaVersion           string
    ConstraintSetID         string
    SourcePackageID         string
    CatalogID               string
    CatalogDigestSHA256     string
    RequiredStepOrder       []string
    RequiredStepCoverage    map[string]RequiredStepConstraint
    AllowedArtifactIDs      []string
    PresentationSlots       []PresentationSlotConstraint
    TargetDurationMS        int
    Canvas                  RenderCanvas
    FactTrackPolicy         FactTrackPolicy
    GeneratedTrackPolicy    GeneratedTrackPolicy
    RequirementBindings     []RequirementBinding
    CreatedAt               time.Time
}
```

必须覆盖验收台账中的以下问题：

- 精确用户输入值到 stage/action/validation 的闭环；
- action target 与 outcome target 分离；
- required step 覆盖和顺序；
- 动态 route 参数；
- source/page 双证据及快照哈希；
- 模型失败时 fail closed；
- 计划陈旧性和重新编译条件。

### 5.2 Provider Adapter

```go
type VideoProviderAdapter interface {
    Descriptor() ProviderDescriptor
    Profile() GeneratedShotCapabilityProfile
    Preflight(ctx context.Context, intent GeneratedShotIntent) ProviderPreflight
    Submit(ctx context.Context, request ProviderSubmitRequest) (ProviderTask, error)
    Poll(ctx context.Context, task ProviderTask) (ProviderTask, error)
    Cancel(ctx context.Context, task ProviderTask) error
    ResolveOutputs(ctx context.Context, task ProviderTask) ([]ProviderOutput, error)
}
```

约束：

- Adapter 不写 EditorSession；
- Adapter 不创建或应用 `DemoEditPlanPatch`；
- Adapter 不读取整个 `.env`，只接收配置层解析后的 secret value；
- 日志和审计只保存 `secret_ref`/来源环境变量名，不保存 key；
- Provider 原生响应必须转换为统一 task/output/error 类型；
- Submit 必须带幂等键、预算、超时和 admission 决策。

### 5.3 FinalFilmJob

```text
created
  -> compiling_constraints
  -> building_baseline
  -> baseline_ready
  -> planning_optional_shots
  -> awaiting_generation_approval
  -> generating_candidates
  -> reviewing_candidates
  -> awaiting_candidate_selection
  -> awaiting_editor_approval
  -> patching_edit_plan
  -> validating_final_plan
  -> rendering
  -> validating_output
  -> completed
```

任何生成失败都进入：

```text
generated_track_skipped -> validating_final_plan -> rendering -> completed_without_generated_track
```

不得因可选生成镜头失败把真实素材基线成片标记为失败。

### 5.4 Job Step 幂等

每个外部调用的幂等键固定为：

```text
sha256(job_id + job_revision + intent_id + provider + profile_version + request_digest)
```

保存以下信息后才允许进入下一状态：

- Provider task ID；
- request digest，不保存密钥和完整敏感 prompt；
- admission/budget decision；
- poll cursor 和最后状态；
- original/normalized artifact hash；
- review、selection、approval 和 patch identity；
- base plan revision 与 target plan revision。

## 6. Provider 路由策略

| 意图 | 默认路由 | 备选 | 禁止条件 |
| --- | --- | --- | --- |
| 真实素材拼接、裁剪、字幕、音频 | Renderer | 无 | 永远不交给生成模型 |
| intro/outro/brand atmosphere | MiniMax-H3 或 Seedance 2.0 | 两路 A/B | 不得出现产品事实或可读 UI |
| section divider/transition | Seedance 2.0 | H3 首尾帧 | 不能替换 required UI shot |
| 多素材视听叙事 | Seedance 2.0 | H3 短镜头 | 必须为非事实展示槽位 |
| 局部 V2V 修补 | Aleph 2 | 不自动 fallback | 真实 UI、数字、表格、按钮默认禁止 |
| UI 录屏质量修复 | FFmpeg/确定性滤镜 | 重新录制 | 禁止生成式重绘 |

Router 的决策顺序：

1. 内容政策；
2. 意图用途；
3. 输入素材类型和长度；
4. Provider Capability Profile；
5. 凭据/endpoint 可用性；
6. 预算、并发和配额；
7. 用户选择的 normal/comparison；
8. 质量和延迟偏好。

## 7. API 边界

```text
POST   /v1/final-film/jobs
GET    /v1/final-film/jobs/{job_id}
POST   /v1/final-film/jobs/{job_id}/cancel
POST   /v1/final-film/jobs/{job_id}/generation-approval
POST   /v1/final-film/jobs/{job_id}/candidates/{candidate_id}/content-review
POST   /v1/final-film/jobs/{job_id}/selection
POST   /v1/final-film/jobs/{job_id}/editor-approval
POST   /v1/final-film/jobs/{job_id}/render
GET    /v1/final-film/jobs/{job_id}/events
```

App-facing 响应只返回稳定 capability、状态、候选摘要和审核要求，不返回 Provider endpoint、API key、完整原生响应或内部成本账户信息。

## 8. 持久化策略

开发模式可以继续使用原子写入的本地 JSON，但接口必须与生产 Store 一致：

```go
type Store interface {
    CreateJob(context.Context, FinalFilmJob) error
    GetJob(context.Context, string) (FinalFilmJob, error)
    CompareAndSwapJob(context.Context, string, int, FinalFilmJob) error
    AppendEvent(context.Context, FinalFilmEvent) error
    PutArtifact(context.Context, FinalFilmArtifact) error
    PutReview(context.Context, FinalFilmReview) error
}
```

生产数据库可以复用现有 `jobs`、`assets`、`asset_reviews` 和 `render_jobs` 的概念，但应增加明确的 FinalFilm revision/intent/candidate/selection 绑定，避免所有状态塞入一个不可查询的 JSONB。

## 9. Renderer 保持的硬门禁

现有 Renderer 门禁继续作为最后一道防线：

- required 业务步骤顺序不变；
- generated candidate 不得有 `source_step_id`；
- candidate 必须是本地 normalized MP4；
- `presentation_only=true`、`non_authoritative=true`；
- 内容审核、选择、Editor approval 和 plan revision 全部匹配；
- source range、目标时长、画布、fps、音轨和字幕经过最终校验；
- render manifest 保存输入 plan/catalog/artifact digest；
- 输出再经过 ffprobe、黑帧/静音/时长/分辨率基础质量检查。

## 10. 分阶段落地

### P0：主链路骨架

- 新增 `StoryboardConstraintSet` 和 compiler；
- 新增 `FinalFilmJob`、Store、Service、Runner；
- 把现有 H3 Harness 接到 Provider Registry；
- 提供 create/get/cancel/events API；
- 生成失败自动使用 baseline；
- 保持 Editor/Renderer 行为不变。

### P1：候选闭环

- 接入现有 structural/content review、candidate set、selection、editor approval、patch proposal；
- 前端增加作业进度和 A/B 选择；
- 将当前 Editor 候选审核与新的 content/editor approval 身份关联；
- 增加作业恢复、重复回调和幂等测试。

### P2：Seedance 2.0 生产 Adapter

- 复用既有 Ark Client、2.0 Profile/Compiler、HTTPS 下载和 FFmpeg 规范化；
- Provider ID 与 Model ID 分开记录，不把 Ark 原始响应交给 Editor；
- 保持 4–15 秒展示槽位，对图片/视频参考继续执行公共 URI/MIME 门禁；
- 对音频生成和 V2V 编辑另建 capability flag，默认不进入当前展示镜头合同；
- 真实调用只在显式 real mode、密钥、预算和用户授权均通过后发生。

### P3：Aleph 2 局部编辑

- 新增 `localized_v2v_edit` 意图类型；
- 要求 mask/keyframe/time range 和受限修改说明；
- 默认拒绝 authoritative UI；
- 增加输入输出 OCR/关键帧差异报告；
- 不满足保持性阈值时丢弃候选并回到原素材。

### P4：Renderer 拆分与质量门禁

- 将 3,000+ 行 `renderer.ts` 按 catalog/validator/compositor/manifest 拆分；
- 增加黑帧、冻结帧、音频响度、字幕安全区和 OCR 风险检查；
- Preview 与 Final 绑定同一 plan revision；
- 建立黄金样片和 Provider 合同测试。

## 11. 第一批代码变更建议

第一批只做框架接线，不升级模型：

1. `model/storyboard_constraint.go`；
2. `model/final_film_job.go`；
3. `finalfilm/store.go`、`service.go`、`job_runner.go`；
4. `media/provider_adapter.go`、`provider_registry.go`；
5. H3 Adapter 包装现有 Harness；
6. `app/final_film_http.go`；
7. Service/Store/Runner/HTTP 的状态机和幂等测试；
8. 一条不调用 Provider 的 baseline E2E；
9. 一条 H3 失败后仍完成 baseline render 的 E2E；
10. 一条未审核候选无法进入 Renderer 的 E2E。

完成这批后，再接 Seedance 2.0 或 Aleph 2，不会把新供应商逻辑继续堆进 Director 或 `renderer.ts`。

## 12. 验收口径

- 没有显式生成授权时 Provider HTTP 调用为 0；
- `.env` 中存在 API key 不等于授权生成；
- 约束编译失败时不创建生成作业；
- Director 超时或非法输出时 baseline 仍可渲染；
- Provider 失败、超时、取消、无输出、下载失败或媒体不合规时 baseline 仍可渲染；
- 生成候选不能绑定 required business step；
- 未完成人工内容审核、选择和 Editor approval 时不能进入时间线；
- 每个最终镜头可追溯到真实素材或已批准的展示意图；
- 最终视频可追溯到 catalog、constraint set、edit plan revision 和 artifact digest；
- 日志、事件、API 和产物中不出现 API key、Authorization header 或本地敏感凭据。

## 13. 实施状态

### 2026-08-17：P0 第一批接线完成

已落地：

- `StoryboardConstraintSet`、required-step 覆盖、事实素材绑定、时间范围和展示槽位验证；
- `FinalFilmJob`、revision CAS、本地原子持久化和连续事件审计；
- baseline-first Service：创建作业前先编译约束并调用 Renderer 校验，创建后先渲染事实轨；
- 存在展示意图时停在 `awaiting_generation_approval`，没有显式授权时 Provider 调用为 0；
- 用户拒绝生成或生成轨失败时，使用已完成的 baseline 进入 `completed_without_generated_track`；
- App API：创建、读取、事件、baseline render、generation approval 和 cancel；
- Provider Registry 与 MiniMax-H3 Adapter；H3 Adapter 在任何 Provider 调用前再次校验持久化授权、幂等键、admission scope、输出目录和 capability preflight；
- 全量 Go 回归通过。

尚未完成：

- Director 输出受控 `GeneratedShotIntent.prompt` 的正式合同和规划模型接线；
- FinalFilmJob Runner 调用 Provider Registry 并持久化候选；
- 内容审核、A/B 选择、Editor approval 与候选 Patch 的 API 串联；
- Aleph 2 localized V2V Adapter；
- 前端 FinalFilm 作业面板；
- 真实 Provider workflow E2E 和最终生成候选合成验收。

### 2026-08-17：P0 第二批执行边界完成

已落地：

- `FinalFilmDirectorPlan` / `FinalFilmGeneratedSpec`：Director 只能给出展示用途、prompt 和已批准素材引用，不能指定视频 Provider、模型、API，也不能绑定业务步骤；prompt 以 SHA-256 锁定；
- `POST /v1/final-film/jobs/{id}/director-plan`：生成授权前持久化并验证 Director 规格；没有合法计划不能批准生成；
- `POST /v1/final-film/jobs/{id}/generate`：Job Runner 只消费已持久化计划与授权引用，经 Provider Registry 执行，并持久化执行记录、标准化候选和结构审核；
- H3 workflow 从 `.env` 读取显式视频路由和 API key，并额外要求 Server 侧价格/额度配置；缺少任一配置时以 disabled adapter 失败关闭，不影响事实轨基线交付；
- 生成失败或无可选 Provider 时，状态收敛为 `completed_without_generated_track`，最终输出仍绑定先前完成的确定性录屏基线。

后续尚需：候选人工内容审核、A/B 选择、Editor approval、补丁显式应用、最终合成与输出验收；Seedance 2.0/Aleph Adapter；前端控制面板。

### 2026-08-17：P0 第三批审核与合成闭环完成

已落地：

- App API 串联人工内容审核、候选选择、独立 Editor approval；每一层均复用既有 provider-neutral media 合同，并持久化进 `GeneratedTrackRecord`；
- Editor approval 只生成 `GeneratedShotEditPlanPatchProposal`，不直接修改时间线；`POST /apply-patch` 是独立显式 opt-in；
- apply 阶段构造隔离的 final catalog/plan，生成候选不得绑定 `source_step_id`，并逐镜头验证 baseline 的事实素材、步骤与时间范围没有变化；
- Renderer 再次验证最终计划后执行 FFmpeg 确定性合成；Render audit 明确记录 Provider 输出已采用、补丁 ID 与新增镜头 ID；
- 最终输出必须通过 ffprobe 完整性、分辨率/FPS、MP4/H.264/yuv420p、FFmpeg 无 fallback、无 skipped operation、需求满足报告等验收，否则自动回退 baseline；
- 多候选审核/选择/批准可以持久化推进；同一作业的全部补丁必须按显式顺序一次提交、一次校验和一次渲染，任一补丁缺失、重复、越权或锚点非法都会整批拒绝；
- `between_sections` / `presentation_gap` 必须绑定 required step 锚点，服务端将其插入该步骤最后一个事实镜头之后；intro/outro 则明确约束为首尾位置；
- Director 已接入现有 LLM Router：模型只返回受限的视觉风格、运动和色板枚举，服务端再编译不含产品 UI、业务事实、数字、Logo 和可读文本的生成提示词，并锁定 hash、时长、比例和素材引用；
- `POST /v1/final-film/jobs/{id}/plan-director` 从当前 job revision 读取锁定约束并落库，仍保留 `director-plan` 端点用于经过同等校验的外部计划提交。
- Web Editor 增加“最终成片”控制面板：创建/恢复作业、baseline、Director、费用授权、H3 执行、逐候选人工内容审核、人工选择、Editor 批准、锚点选择、全补丁原子应用和事件审计均按 job state/revision 推进；
- 候选播放器只通过 `GET /v1/final-film/jobs/{id}/media/{candidate_id}` 读取该作业已持久化且通过结构校验的 normalized 文件，不接受浏览器传入任意本机路径；
- 未保存的 Editor 草稿不能打开工作流；前端 Mock 模式明确禁用真实工作流，不伪造 Provider 或审核结果。

新增 Provider 落地：

- Seedance 2.0 已实现 `GeneratedShotProviderAdapter`，复用 Ark create/query transport、公共 HTTPS 下载器和 FFmpeg/FFprobe normalizer；
- Adapter 在 Provider HTTP 前再次要求持久化生成授权、幂等键、admission scope、输出目录和 capability preflight；
- 真实路由还必须同时满足 `CASCADE_ARK_MEDIA_MODE=real`、Seedance 凭据、精确的视频 route 和独立 `CASCADE_SEEDANCE_FINAL_FILM_ENABLED=true`，仅配置 API key 不会启用；
- Web 面板可显式选择 H3 或 Seedance 2.0；两路产物最终都收敛为同一 candidate/review/selection/approval/patch 合同。

### 2026-08-17：真实 H3 workflow 端到端成片验收完成

- 新增环境门控的 `real_h3_e2e_test.go`：复用已付费并持久化的 H3 task/candidate，明确不发起第二次 Provider HTTP 请求；
- 用例完整经过 baseline、Director 计划、生成授权、Provider Registry、人工内容审核、人工选择、Editor approval、显式补丁应用、Renderer 和最终输出验收共 11 个持久化事件；
- 真实 Node Worker + FFmpeg 将 1920×1080/CFR30 H3 开场候选与事实轨 fixture 确定性拼接，输出 MP4/H.264/yuv420p/AAC，最终状态为 `completed_with_approved_generated_track`；
- 时间轴抽帧确认 H3 展示轨位于开场，事实轨顺序保持不变，无黑帧和错序；该事实轨是专用于 harness 的确定性测试 fixture，不冒充真实产品录屏；
- 真实验收同时修复 Worker `sha256:<hex>` 与 Go 验收器裸 digest 的格式不兼容，并将平均帧率容差与 Worker 现有规范化门禁统一为 ±0.25fps，仍拒绝实际 Profile 漂移。

后续尚需：黑帧/冻结帧/响度/OCR 深度质量门禁；Aleph 2 localized V2V Adapter；生产级分布式 admission、跨进程幂等和实际 usage 对账。Seedance 2.0 真实出站验收应单独授权和执行，不作为 H3 闭环的完成条件。
