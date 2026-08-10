# Server 生成视频统一候选产物内部协议

> 文档状态：Server 内部预发布协议  
> 整理日期：2026-08-05  
> App 可见性：不可见；不修改现有 App 包体或 schema

## 1. 目的

本协议定义 Seedance 2.0 与 MiniMax-H3 在完成各自 Provider 请求、下载和媒体规范化后，如何汇合为统一的 Server 内部候选对象。厂商请求体、原始响应、临时下载 URL 和模型 ID 不得进入本对象，也不得直接交给编辑器。

当前 schema：

```text
demoops.generated_shot_candidate.v1
```

当前实现提供严格校验、H3 隔离流水线结果转换器、确定性结构审核、人工内容决定记录契约、候选集合/选择契约、Editor approval 契约和统一编辑器引用编译器，尚未注册到 App、Director、Executor、EditorSession、Renderer、审核 UI、`normal`、`comparison` 或 `fallback` 路由。

## 2. 汇合位置

```text
GeneratedShotIntent
    -> Provider 专属 Capability Profile 和请求编译
    -> Provider 独立生成
    -> 原始产物立即下载和 SHA-256
    -> ffprobe -> FFmpeg -> ffprobe
    -> GeneratedShotCandidate
    -> 确定性结构审核（旁路纯函数已实现）
    -> 人工内容审核决定记录（旁路契约已实现，审核 UI 尚未接入）
-> 显式选择 A/B 或批准进入编辑器（尚未实现）
    -> GeneratedShotEditorAssetRef（仅补丁构造输入，旁路已实现）
    -> GeneratedShotEditPlanPatchProposal（旁路已实现）
    -> CandidateAssetEditPlanPatch / EditorSession（尚未接入）
    -> 候选分镜补丁（默认不自动应用）
```

Seedance 与 H3 只能在 `GeneratedShotIntent` 和 `GeneratedShotCandidate` 两个 Provider-neutral 层汇合。不得交换厂商请求、响应或把一方输出作为另一方的输入。

## 3. 强制安全语义

候选对象必须满足：

- `status=normalized_candidate_ready_for_review`；
- `failure_policy=continue_without_generated_candidate`；
- `non_authoritative=true`；
- `presentation_only=true`；
- `requires_explicit_review=true`；
- `approved_for_demo=false`；
- `include_in_demo=false`。

通过本协议只表示媒体格式已达到“可进入审核”状态，不表示内容真实、审核通过、用户已经选择，也不授权进入时间线。

本对象故意不包含 `source_step_id`、时间线位置、自动应用开关或业务步骤绑定字段。生成素材不能替代真实录屏、截图、UI 按钮、文字、数字、表格、状态或业务结果。

## 4. 原始产物

`original_artifact` 必须：

- 使用本地绝对路径，不接受厂商临时 URL；
- 带 64 位 SHA-256 和正数文件大小；
- `role=original`；
- 保留真实媒体探测数据；
- 与 Provider task ID 保持一致的审计关系。

原始产物只用于审计和问题诊断，不能直接进入编辑器。

## 5. 规范化产物

`normalized_artifact` 必须：

- 使用本地绝对路径；
- `role=normalized`；
- `mime_type=video/mp4`；
- `normalization_status=ok`；
- `normalization_profile=editor_mp4_h264_yuv420p_1920x1080_cfr30_v1`；
- 二次探测结果为 MP4/H.264/yuv420p/1920×1080/CFR 30fps；
- 时长为正数；
- 路径及 SHA-256 均与原始产物不同。

Server 不信任扩展名、MIME 声明或 Provider 元数据，必须依据下载文件和二次媒体探测结果校验。

## 6. H3 当前转换边界

`NewGeneratedShotCandidateFromMiniMaxH3` 只接受：

- H3 pipeline 状态为 `normalized_candidate_ready_for_review`；
- 没有 pipeline 错误；
- original 与 normalized artifact 同时存在；
- 两个 artifact 都是 `presentation_only=true`、`authoritative=false`；
- pipeline task ID 与两个 artifact 的 source task ID 完全一致。

转换器不会：

- 创建 H3 Client 或发起 HTTP 调用；
- 创建、查询或取消 Provider 任务；
- 发布素材；
- 写入 EditorSession；
- 执行内容审核；
- 将 `approved_for_demo` 或 `include_in_demo` 改为 true。

## 7. Seedance 当前状态

Seedance 2.0 仍使用现有运行链路。本轮不修改该链路，也不把它切换到本协议。后续必须先为 Seedance 补齐与 H3 同等级的 `download -> probe -> normalize -> probe` 产物证据，再增加独立转换器；不能仅因 Seedance 返回 MP4 就跳过规范化门禁。

## 8. 结构审核与内容审核

结构审核使用：

```text
demoops.generated_shot_structural_review.v1
```

它只执行确定性校验：

- intent 和 candidate 本身均通过各自硬门禁；
- candidate 的 `intent_id` 与被审核 intent 一致；
- 规范化后时长仍在 4～15 秒，并与请求时长保持在允许误差内；
- original 与 normalized 的探测时长没有异常漂移；
- 结构审核不能设置 `approved_for_demo=true` 或 `include_in_demo=true`。

结构审核通过后的状态仅为：

```text
eligible_for_content_review
```

内容审核决定使用：

```text
demoops.generated_shot_content_review.v1
```

当前项目 Profile 要求人工审核，不接受模型自我批准。批准必须同时满足：

- `reviewer_kind=human`；
- reviewer、时间和结构审核绑定完整；
- 至少一个审核证据引用；
- 候选用途与 intent 一致；
- 没有替换真实 UI；
- 没有表达业务事实；
- 没有未经核验的文字或数字；
- 没有修改参考素材中的事实性要素。

内容批准后的状态仅为：

```text
content_approved_pending_selection
```

此时仍固定 `approved_for_demo=false`、`include_in_demo=false`，必须等待未来独立的显式选择/批准步骤。拒绝决定必须填写原因，且不能阻断真实素材的确定性交付。

## 9. 候选集合与显式选择

候选集合使用：

```text
demoops.generated_shot_candidate_set.v1
```

当前只定义两种不可执行的选择集合：

| 模式 | 约束 |
| --- | --- |
| `normal` | 恰好一个已经通过人工内容审核的候选，仍需明确选择 |
| `comparison` | 恰好两个候选，必须分别来自 Seedance 2.0 和 MiniMax-H3，且属于同一个 intent |

`comparison` 还要求：

- candidate ID 唯一；
- Provider task identity 唯一；
- content review ID 唯一；
- normalized SHA-256 唯一；
- 两个候选都保持 `approved_for_demo=false`、`include_in_demo=false`；
- 两个候选都具有 `decision=approve` 的人工内容审核及证据；
- 不能将一方输出作为另一方的输入。

候选集合固定：

```text
status=ready_for_explicit_selection
selection_required=true
executable=false
approved_for_demo=false
include_in_demo=false
auto_apply=false
```

`fallback` 不属于当前候选集合模式，也未实现执行。Seedance 失败不会自动触发 H3。

显式选择记录使用：

```text
demoops.generated_shot_selection.v1
```

当前只允许人工选择，并要求选择人、时间、原因和至少一条证据。选择前 Server 必须重新校验序列化候选集合，防止 schema、候选摘要、Provider 组合、SHA-256 或安全标志被篡改。

选择结果状态仅为：

```text
selected_pending_editor_approval
```

它表达“用户在已审核候选中做出了选择”，不等于编辑器批准。选择结果继续固定：

```text
editor_approval_required=true
approved_for_demo=false
include_in_demo=false
auto_apply=false
```

因此本阶段仍不会生成或自动应用 `candidate_asset_edit_plan_patch.json`，也不会修改 `DemoEditPlan`。

## 10. 统一编辑器素材引用

统一引用使用：

```text
demoops.generated_shot_editor_asset_ref.v1
```

`CompileGeneratedShotEditorAssetRef` 只接受已经通过候选、集合、选择和 Editor approval 校验的对象，并输出：

- 本地绝对路径的 normalized artifact；
- `kind=generated_video_candidate`；
- MP4/H.264/yuv420p/1920×1080/CFR30 探测摘要；
- normalized SHA-256、文件大小和时长；
- Provider 仅作为 Server 审计字段，不作为编辑器分支条件；
- `source_material_policy=non_authoritative_generated_candidate`；
- 目标计划 ID、expected revision 和受控 placement。

引用编译器明确拒绝：

- H3/Seedance 临时 URL、Provider asset ID 或原始响应；
- 未规范化或非 CFR30 媒体；
- digest 与 approval 不一致的候选；
- `auto_apply=true` 或 `include_in_demo=true` 的引用；
- 任何业务 `source_step_id` 绑定。

这是跨 Provider 混合进入编辑器前的统一边界：不同模型可以在不同镜头产生候选，但编辑器只消费同一种 normalized 本地引用。该引用仍只是后续补丁构造输入，不会写 EditorSession、应用补丁或授权 Renderer。

## 11. 受控候选补丁提案

补丁提案使用：

```text
demoops.generated_shot_edit_plan_patch_proposal.v1
```

`CompileGeneratedShotEditPlanPatchProposal` 只接受通过统一引用、Editor approval 和候选集合校验的对象，并生成与既有 `candidate_asset_edit_plan_patch.v1` 语义兼容的 Server 内部提案：

```text
status=proposed_pending_explicit_apply
application_mode=manual_or_explicit_opt_in_required
requires_explicit_opt_in=true
requires_renderer_validation=true
presentation_only=true
non_authoritative_only=true
must_not_bind_source_step=true
auto_apply=false
approved_for_demo=false
include_in_demo=false
```

提案只保存统一 `asset_ref_id`、目标计划 ID、expected revision、purpose、placement 和时长，不保存 Provider 请求、临时 URL 或业务 `source_step_id`。它不会修改 `DemoEditPlan` 或 `EditorSession`，不会执行 revision compare-and-swap、导入文件、应用补丁或触发 Renderer。

后续正式接入必须先将该提案映射为现有 `CandidateAssetEditPlanPatch`，再经过既有 patch validator、expected revision compare-and-swap、人工显式 opt-in 和 Renderer validation。不能绕过现有 Executor 协议直接把统一引用写入时间线。

## 12. H3 回调通知边界

H3 回调解析使用 Server 内部 schema：

```text
demoops.minimax_h3_callback.v1
```

当前只实现无副作用的 payload 解析器：

- challenge 验证请求只原样返回 challenge；
- 任务通知必须包含 task ID 和受支持的六态之一；
- 如果调用方提供 expected task ID，回调任务必须完全匹配；
- 成功通知必须设置 `requires_query=true`，不能仅凭回调宣称任务成功；
- `content.url`、`output.video_url` 和顶层 `video_url` 只接受 HTTPS，且只作为待查询线索；
- 解析器不创建任务、不调用 Provider、不登记 artifact、不生成候选、不推进编排状态；
- 回调仍是轮询的唤醒信号，正式接入后必须再次调用 H3 查询接口确认状态和输出 URL。

当前没有注册 HTTP callback route，也没有把 `callback_url` 写入现有 H3 生产请求；轮询仍是唯一运行路径。

## 13. Editor approval 前置契约

Editor approval 使用：

```text
demoops.generated_shot_editor_approval.v1
```

它位于人工内容审核和显式选择之后。批准前必须重新绑定并校验：

- `GeneratedShotIntent`；
- 完整 `GeneratedShotCandidate`；
- `GeneratedShotCandidateSet`；
- `GeneratedShotSelection`；
- 选中候选的 Provider、task ID 和 normalized SHA-256；
- 目标 `DemoEditPlan` ID 与正数 expected revision；
- 人工批准人、时间、原因和证据。

placement 必须与 intent purpose 对应：

| purpose | 允许 placement |
| --- | --- |
| `intro` | `before_first_required_step` |
| `outro` | `after_last_required_step` |
| `section_divider`、`transition` | `between_sections` |
| `abstract_b_roll`、`brand_atmosphere` | `presentation_gap` |

Editor approval 状态为：

```text
editor_approved_pending_patch
```

它只允许后续构造一个受控候选补丁：

```text
patch_creation_authorized=true
patch_apply_authorized=false
renderer_authorized=false
must_not_bind_source_step=true
approved_for_demo=true
include_in_demo=false
auto_apply=false
```

因此批准并不修改 EditorSession，也不应用 `candidate_asset_edit_plan_patch.json`，更不授权 Renderer。补丁构造、补丁校验、revision compare-and-swap、人工应用和 Renderer 复验仍是后续独立阶段。

## 14. 不可执行编排状态机

编排审计记录使用：

```text
demoops.generated_shot_orchestration.v1
```

当前允许的前向状态为：

```text
preflight_complete
    -> candidates_ready_for_review
    -> selected_pending_editor_approval
    -> editor_approved_pending_patch
```

任一非终态也可以安全停止为：

```text
stopped_without_generated_candidate
```

停止和任意失败仍保持：

```text
failure_policy=continue_without_generated_candidate
```

每次状态推进都必须携带并重校验对应 typed artifact，不能只提交目标状态字符串。状态 revision 必须递增，更新时间必须单调递增。comparison 的 preflight 必须同时包含 Seedance 2.0 与 MiniMax-H3 两个独立 Provider 结果。

该状态机固定：

```text
runtime_route_registered=false
provider_calls_allowed=false
editor_writes_allowed=false
renderer_allowed=false
auto_apply=false
```

这是一份旁路审计状态机，不是生产编排器。它不持有 Client，不创建或查询任务，不下载素材，不触发 Provider，不写编辑器，也不注册 `normal`、`comparison` 或 `fallback` 路由。

## 15. 与 App 协议的关系

本文件是 Server 内部产物协议，不是 App 输入协议：

- App 不提交候选对象；
- App 不提交 Provider、模型 ID、task ID、文件路径或规范化字段；
- 当前 `browser-agent-outline-v1` 和现有 App 执行包零变更；
- 未来 App 只表达 Provider-neutral 展示意图，Server 负责生成、下载、探测、规范化与审核。

公共 App 边界见 [App ↔ Server 生成展示视频能力协议](app-server-generated-video-capability-protocol.md)。

### 15.1 H3 callback notification-only 去重旁路

Server 已具备 `MiniMaxH3CallbackDeduper` 纯内存旁路，用于把已解析的 H3 回调转换为“唤醒查询”的内部记录：

- 去重键为 `task_id + normalized_status + payload_sha256`；同一回调重复到达时返回 `duplicate=true` 且 `query_required=false`。
- 首次通知只返回 `query_required=true`，并记录接收时间、任务 ID、六态状态和原始 payload 的 SHA-256；不保存完整 Prompt、Key 或完整临时 URL。
- 记录固定 `notification_only=true`，不会标记任务成功、创建候选、登记 artifact、推进编排、写入 EditorSession 或调用 Provider。
- 当前实现为有界内存 deduper，仅用于离线协议验证；尚未注册 HTTP route、来源认证、持久化或分布式去重。因此 callback 仍不能替代查询，正式启用前必须补齐这些能力。

## 16. 当前验证方式

验收并行期间仅允许：

- `gofmt`；
- `go test -c`，只编译测试二进制但不执行；
- `go build ./internal/media`；
- `git diff --check` 和生产引用静态搜索。

禁止为验证本协议启动或停止 Server、执行 E2E、调用真实模型或运行真实 FFmpeg。
