# MiniMax-H3 视频生成接入说明

> 文档状态：项目级技术基线，包含官方接口契约摘要、Server 使用边界和当前实现状态
> 整理日期：2026-08-04
> 适用模型：`MiniMax-H3`
> API 基地址：`https://api.minimaxi.com`
> 范围：MiniMax 视频生成 V2；不包含 MiniMax 文本模型、语音、图像或旧版 Hailuo/S2V 视频接口

## 1. 项目定位

MiniMax-H3 在 Cascade DemoOps Server 中属于可选的展示视频候选 Provider。它可以生成片头、片尾、章节过渡、抽象 B-roll 和品牌氛围镜头，但不能参与业务事实判断，也不能替换 Playwright 真实录屏或截图。

```text
展示镜头意图
    -> Server 选择 Provider 并校验输入
    -> MiniMax-H3 异步生成
    -> Server 查询、下载、媒体探测和规范化
    -> 非权威候选素材
    -> 内容审核和用户明确选择
    -> DemoEditPlan
    -> 确定性 Renderer / FFmpeg 最终成片
```

H3 与 Seedance 2.0 是同级候选生成 Provider。默认不把一个模型的输出交给另一个模型再次生成。

## 2. 官方接口概览

所有接口使用 HTTP Bearer 鉴权：

```http
Authorization: Bearer <MINIMAX_API_KEY>
```

API Key 只能保存在环境变量或密钥管理系统中，不得写入执行包、日志、Trace、错误产物或本文档。

| 操作 | 方法与路径 | 用途 |
| --- | --- | --- |
| 创建任务 | `POST /v2/video_generation` | 创建异步 H3 视频生成任务 |
| 查询单个任务 | `GET /v2/query/video_generation/{task_id}` | 获取状态、错误、用量和生成视频 URL |
| 查询任务列表 | `GET /v2/query/video_generation` | 分页查询最近 7 天任务 |
| 取消或删除 | `DELETE /v2/video_generation/{task_id}` | queued 时取消，终态时删除记录 |

查询接口只支持最近 7 天内的任务。生成视频 URL 有时效，成功后必须及时下载并转存到 Server 管理目录。

## 3. 创建视频生成任务

### 3.1 请求

```http
POST /v2/video_generation
Content-Type: application/json
Authorization: Bearer <MINIMAX_API_KEY>
```

项目推荐的文生视频请求：

```json
{
  "model": "MiniMax-H3",
  "content": [
    {
      "type": "text",
      "text": "生成一个不包含产品 UI 和业务事实的抽象品牌片头"
    }
  ],
  "resolution": "2K",
  "duration": 5,
  "ratio": "16:9",
  "aigc_watermark": false
}
```

成功响应：

```json
{
  "task_id": "424010985738629"
}
```

创建成功只表示任务已受理，不表示视频已经生成。Server 必须继续查询任务状态或消费经过验证的回调。

### 3.2 顶层参数

| 参数 | 必需 | 官方能力 | 项目策略 |
| --- | ---: | --- | --- |
| `model` | 是 | 当前为 `MiniMax-H3` | 由 Server 固定，APP 不传 |
| `content` | 是 | 多模态数组 | 必须至少包含一个非空 text |
| `resolution` | 是 | 当前仅 `2K` | 由 Server 固定 |
| `duration` | 是 | 整数 4～15 秒 | 默认 5 秒 |
| `ratio` | 视模式而定 | 见宽高比规则 | 默认 16:9 或按模式归一化 |
| `callback_url` | 否 | 状态回调 | 首期继续轮询；稳定回调端点上线后再启用 |
| `aigc_watermark` | 否 | 默认 false | 当前保持 false |

### 3.3 content 类型

| `type` | 对象字段 | 可用 role |
| --- | --- | --- |
| `text` | `text` | 无 |
| `image_url` | `image_url.url` | `first_frame`、`last_frame`、`reference_image` |
| `video_url` | `video_url.url` | `reference_video` |
| `audio_url` | `audio_url.url` | `reference_audio` |

每次请求必须包含至少一个非空 `text`。单个 text 最多 7000 个字符。

媒体 URL 支持官方文档定义的公网 URL、`mm_file://{file_id}` 和受支持的 Base64 data URI。项目侧优先使用短时有效、可审计的 HTTPS URL 或 Provider 文件引用；大文件禁止使用 Base64。

## 4. 生成模式

### 4.1 文生视频

```text
text
```

- `ratio` 必填；
- 不允许使用 `adaptive`；
- 适合不包含产品 UI 的片头、片尾和抽象 B-roll。

### 4.2 首帧图生视频

```text
text + image_url(role=first_frame)
```

- 图片未填写 role 时，官方按 `first_frame` 处理；
- 宽高比由输入图决定；
- 请求比例按 `adaptive` 处理。

### 4.3 尾帧图生视频

```text
text + image_url(role=last_frame)
```

官方 V2 文档将仅尾帧列为支持场景。当前项目客户端尚未对齐此能力，见“当前实现状态与缺口”。

### 4.4 首尾帧图生视频

```text
text
+ image_url(role=first_frame)
+ image_url(role=last_frame)
```

- 首帧最多一张；
- 尾帧最多一张；
- 宽高比由输入图片决定；
- 适用于受控的展示过渡，不得让模型重绘事实性产品 UI。

### 4.5 多模态参考生视频

```text
text
+ reference_image 0～9
+ reference_video 0～3
+ reference_audio 0～3
```

约束：

- 至少有一个参考图片或参考视频；
- 参考音频不能作为唯一媒体输入；
- 可以使用 `adaptive` 或显式比例；
- APP 当前能力 Profile 仍应遵循 Server 更保守的参考素材总数限制，不应直接使用厂商理论最大值。

### 4.6 模式互斥

图生视频帧模式与多模态参考模式互斥：

```text
first_frame / last_frame
```

不得与以下 role 混用：

```text
reference_image / reference_video / reference_audio
```

Server 必须在调用前拒绝混合模式，不能静默删除或重解释输入。

## 5. 输入媒体限制

请求体总大小不得超过 64 MB。Base64 通常会增加约 33% 体积，大视频必须使用 URL 或文件引用。

### 5.1 图片

| 项目 | 官方限制 |
| --- | --- |
| 格式 | JPG、JPEG、PNG、WEBP、HEIC、HEIF |
| 单文件大小 | 不超过 30 MB |
| 宽高范围 | 256～5760 px |
| 宽高比 | 0.4～2.5 |
| 首帧数量 | 最多 1 张 |
| 尾帧数量 | 最多 1 张 |
| 参考图数量 | 最多 9 张 |

项目稳定输入范围当前优先使用 PNG/JPEG。其他格式应先由 Server 转换和验证。

### 5.2 参考视频

| 项目 | 官方限制 |
| --- | --- |
| 容器 | MP4、MOV |
| 视频编码 | H.264/AVC、H.265/HEVC |
| 音频编码 | AAC、MP3 |
| 单文件大小 | 不超过 50 MB |
| 数量 | 最多 3 个 |
| 单段时长 | 2～15 秒 |
| 总时长 | 不超过 15 秒 |
| 宽高范围 | 256～5760 px |
| 宽高比 | 0.4～2.5 |
| 帧率 | 23.976～60 FPS |

Server 必须使用媒体探测读取真实容器、编码、时长、尺寸和 FPS，不得仅相信 APP 元数据、扩展名或 MIME 声明。

### 5.3 参考音频

| 项目 | 官方限制 |
| --- | --- |
| 格式 | WAV、MP3 |
| 单文件大小 | 不超过 15 MB |
| 数量 | 最多 3 个 |
| 单段时长 | 2～15 秒 |
| 总时长 | 不超过 15 秒 |

当前 APP 能力 Profile 不开放 H3 参考音频。后续开放前必须补充授权、隐私、内容审核和音频生命周期策略。

## 6. 输出参数

### 6.1 分辨率

H3 当前创建接口只接受：

```text
2K
```

APP 不应传递模型原生分辨率。Server 下载结果后将其规范化到编辑器画布，例如 1920×1080。

### 6.2 生成时长

```text
4～15 秒，整数
```

项目默认使用 5 秒。最终 60 秒或更长视频必须由真实录屏、多个可选展示镜头和确定性编辑器合成，不能把最终成片时长直接作为单次 H3 生成时长。

### 6.3 宽高比

支持：

```text
adaptive
21:9
16:9
4:3
1:1
3:4
9:16
```

模式规则：

- 文生视频：必须显式指定非 adaptive 比例；
- 首帧、尾帧或首尾帧图生视频：由输入图决定，按 adaptive 处理；
- 多模态参考：可以 adaptive，也可以显式指定比例。

### 6.4 输出格式和 FPS

任务成功后返回有时效的 MP4 下载 URL。官方所给 H3 V2 资料没有提供可配置的输出 FPS 字段，因此 APP 不得要求 H3 原生输出指定 FPS。

Server 应保留原始输出，并另外生成编辑器规范化副本：

```text
MP4 / H.264 / yuv420p / 目标 CFR / 目标画布
```

原始输出与规范化输出必须分别计算 SHA-256，并通过父子素材关系关联。

## 7. 查询单个任务

```http
GET /v2/query/video_generation/{task_id}
Authorization: Bearer <MINIMAX_API_KEY>
```

响应主体：

```json
{
  "task": {
    "id": "424010985738629",
    "model": "MiniMax-H3",
    "status": "succeeded",
    "created_at": 1785125529,
    "updated_at": 1785125946,
    "content": {
      "url": "https://video-product.cdn.minimax.io/.../output.mp4"
    },
    "resolution": "2K",
    "duration": 5,
    "usage": {
      "total_seconds": 5,
      "input_seconds": 0,
      "output_seconds": 5,
      "image_count": 0
    },
    "ratio": "16:9",
    "task_type": "generation",
    "modality": "video"
  }
}
```

### 7.1 状态归一化

| 官方状态 | Server 状态语义 | 是否终态 |
| --- | --- | ---: |
| `queued` | 排队中 | 否 |
| `running` | 生成中 | 否 |
| `succeeded` | 成功，立即下载 | 是 |
| `failed` | 失败，记录错误 | 是 |
| `cancelled` | 已取消 | 是 |
| `expired` | 已过期 | 是 |

Server 不得根据创建响应自行宣称任务成功，也不得在 `content.url` 缺失时虚构候选素材。

### 7.2 用量字段

任务响应可能包含：

- `usage.total_seconds`；
- `usage.input_seconds`；
- `usage.output_seconds`；
- `usage.image_count`。

Server 应保存这些字段用于成本审计、配额和对账，但不得向不必要的客户端暴露供应商内部信息。

## 8. 查询任务列表

```http
GET /v2/query/video_generation
```

可选查询参数：

| 参数 | 说明 |
| --- | --- |
| `page_num` | 页码，从 1 开始 |
| `page_size` | 每页数量 |
| `filter.status` | 按状态过滤 |
| `filter.task_ids` | 按一个或多个任务 ID 过滤 |
| `filter.model` | 按模型过滤 |
| `filter.task_type` | 按任务类型过滤 |

列表接口只统计最近 7 天任务。它适合运维对账和任务恢复，不应代替 Server 自己的持久化任务状态。

## 9. 取消或删除任务

```http
DELETE /v2/video_generation/{task_id}
Authorization: Bearer <MINIMAX_API_KEY>
```

接口根据当前状态决定行为：

| 当前状态 | 操作 | 说明 |
| --- | --- | --- |
| `queued` | `cancel` | 取消尚未开始的任务 |
| `succeeded` | `delete` | 删除任务记录 |
| `failed` | `delete` | 删除任务记录 |
| `expired` | `delete` | 删除任务记录 |
| `running` | 不允许 | 生成中无法取消 |
| `cancelled` | 不允许 | 已取消任务不可再次操作 |

删除供应商任务记录不等于删除 Server 已下载素材。Server 素材删除必须遵循独立的保留期、审计和显式删除策略。

## 10. 回调

创建任务可以传 `callback_url`。MiniMax 会先发送包含 `challenge` 的验证请求，回调服务必须在 3 秒内原样返回 challenge；验证成功后，状态变更会推送与查询接口相同的数据结构。

项目策略：

1. 首期以轮询为准；
2. 回调上线后只把通知当作唤醒信号；
3. 收到成功通知后再次查询任务确认；
4. 回调必须校验任务是否属于当前 Server；
5. 回调不得凭外部 payload 直接将任务标记成功或登记产物；
6. 回调日志不得包含凭据、完整敏感 Prompt 或未脱敏媒体地址。

## 11. 错误和重试策略

官方错误采用 OpenAI 风格结构并返回真实 HTTP 状态码。项目需至少区分：

| HTTP/类别 | 处理策略 |
| --- | --- |
| 400 参数错误 | 不重试；记录字段级错误 |
| 401 鉴权失败 | 不重试；标记配置错误 |
| 402 余额或额度不足 | 不重试；通知运营/配额系统 |
| 422 内容安全或不可处理 | 不自动改写业务事实；返回内容拒绝 |
| 429 限流 | 有界指数退避并尊重任务幂等 |
| 500/529 服务错误或过载 | 有界退避；超过上限后降级 |

任何 H3 异常都不得把已经成功的 Browser Agent 录制和基础 MP4 改为失败。展示候选生成的默认失败策略是：

```text
continue_without_generated_candidate
```

## 12. Server 安全与素材边界

### 12.1 事实轨与展示轨

事实轨只能使用：

- Playwright 真实录屏；
- Playwright 真实截图；
- 确定性裁剪、缩放、标注、字幕和打码。

H3 只能产生展示轨素材：

- 抽象片头和片尾；
- 章节过渡；
- 不代表产品事实的 B-roll；
- 品牌氛围镜头。

### 12.2 候选素材默认元数据

```json
{
  "include_in_demo": false,
  "approved_for_demo": false,
  "non_authoritative": true,
  "presentation_only": true,
  "source_material_policy": "non_authoritative_generated_candidate"
}
```

生成素材不得：

- 绑定业务 `source_step_id`；
- 表示业务步骤或结果已经发生；
- 替换真实 UI 录屏；
- 未经审核自动进入时间线；
- 直接修改 `DemoEditPlan`。

### 12.3 素材发布

Server 不得向供应商传递本地文件路径。输入只能使用：

- 受控、短时有效的 HTTPS URL；
- Provider 文件引用；
- 满足 64 MB 总请求限制的小型 Base64 data URI。

发布前必须确认：

- 素材属于当前项目和编辑会话；
- 用户拥有使用权；
- 素材不包含不应上传的客户数据；
- 打码策略已在上传前应用；
- URL 权限和有效期符合最小授权原则。

## 13. 与 APP 的能力边界

APP 不传模型 ID、供应商、API Endpoint 或厂商参数，只提交逻辑能力和展示意图：

```json
{
  "capability": "presentation_video_candidate",
  "purpose": "intro",
  "required": false,
  "reference_asset_refs": [
    "stage_01_viewport_screenshot"
  ],
  "requested_slot": {
    "preferred_duration_sec": 5,
    "aspect_ratio": "16:9"
  },
  "content_policy": {
    "presentation_only": true,
    "may_represent_business_step": false,
    "may_replace_captured_ui": false,
    "requires_explicit_review": true
  },
  "failure_policy": "continue_without_generated_candidate"
}
```

APP 必须遵循 Server 返回的能力 Profile。Server 收到请求后仍需基于实际文件重新校验，不能信任 APP 提供的数量、格式、时长或 FPS。

当前项目给 APP 的保守边界为：

| 项目 | 当前 Server 边界 |
| --- | ---: |
| 图片和视频参考素材总数 | 最多 4 |
| 参考图片 | 最多 4 |
| 参考视频 | 最多 3 |
| 参考音频 | 0，暂不开放 |
| 单段参考视频 | 2～15 秒 |
| 参考视频总时长 | 不超过 15 秒 |
| 候选生成时长 | 4～15 秒，推荐 5 秒 |
| 稳定图片格式 | PNG、JPEG |
| 稳定视频格式 | MP4、MOV |

这是 Server 当前可验证能力，不等于 H3 厂商理论最大能力。

## 14. 与 Seedance 2.0 的编排关系

推荐三种模式：

### 14.1 normal

```text
Seedance 2.0 单独生成候选
```

Seedance 2.0 是当前主 Provider。

### 14.2 fallback

```text
Seedance 创建或生成失败
    -> Server 确认同一展示意图兼容 H3
    -> H3 使用原始受控参考素材独立生成候选
```

这是控制层串行降级，不是把 Seedance 输出交给 H3。

### 14.3 comparison

```text
同一展示意图
    -> Seedance 2.0 候选 A
    -> MiniMax-H3 候选 B
    -> 分别规范化和审核
    -> 用户二选一
```

comparison 可并行执行，但会增加成本。只能在用户显式请求或评测场景中使用。

默认禁止：

```text
Seedance 输出 -> H3 二次生成
H3 输出 -> Seedance 二次生成
任一模型产物未经审核自动加入时间线
```

### 14.4 A/B 候选的统一编辑器协议

Seedance 2.0 与 H3 的请求体和原始响应由各自 Provider Adapter 负责，编辑器不得读取厂商响应，也不得直接读取厂商返回的原始视频。两路结果必须先转换为同一个 Server 内部对象：

```text
Provider 原始响应
    -> Provider Adapter：统一 task_id / status / output URL
    -> original artifact：立即下载、只读保存、记录原始 SHA-256
    -> ffprobe：验证真实容器、编码、时长、尺寸、FPS、像素格式
    -> FFmpeg MediaNormalizer
    -> normalized artifact：MP4 / H.264 / yuv420p / CFR 30fps / 1920x1080
    -> 再次 ffprobe、记录规范化 SHA-256
    -> 候选内容审核与结构审核
    -> 显式选择 A 或 B
    -> DemoEditPlan 候选镜头补丁
    -> Renderer 再校验后读取
```

编辑器只接受满足以下条件的 `normalized artifact`：

- `kind=generated_video_candidate`；
- 本地持久化文件，不是厂商临时 URL；
- `normalization_status=ok`，且二次 `ffprobe` 与目标媒体 Profile 一致；
- `approved_for_demo=true`、`non_authoritative=true`、`presentation_only=true`；
- `source_material_policy=non_authoritative_generated_candidate`；
- 不绑定任何业务 `source_step_id`；
- 通过 SHA-256、文件大小、时长和显式时间段校验。

原始文件和规范化副本必须使用不同 artifact ID 与 SHA-256。A/B 只表示同一展示意图下的候选关系，不得依赖相同文件名、URL 字段或厂商响应结构。

当前代码已经完成厂商输出 URL 到 `generated_video_candidate` 的初步归一化、下载、完整性记录、候选审核和 Renderer 元数据门禁，但尚未完成候选视频的 `ffprobe -> FFmpeg -> ffprobe` 规范化闭环。因此在 MediaNormalizer 落地前，不得把 H3 原始产物直接交给编辑器，也不得宣称 A/B 原始产物可无条件互换。

### 14.5 分镜 JSON 的所有权

Seedance 2.0 和 H3 都不负责产出 Renderer 可执行的最终分镜 JSON；它们只负责生成候选视频素材。

| 文件 | 当前生产者 | 是否可直接驱动 Renderer |
| --- | --- | --- |
| `director_edit_suggestion.json` | Server `DirectorAdapter` 基于输入确定性构造建议；真实 Seedance 调用只附加异步媒体任务信息 | 否，必须转换、校验 |
| `director_edit_plan_patch.json` | Server `NewDirectorEditPlanPatchFromSuggestion` | 否，必须校验并受控合并 |
| `candidate_asset_edit_plan_patch.json` | Server `NewCandidateAssetEditPlanPatch` 从审核通过的生成候选构造 | 否，默认 `auto_apply=false` |
| `demo_edit_plan.json` | `video-worker` 根据素材目录和执行轨迹确定性生成，或读取 Server 已校验的显式计划 | 是，但必须先通过 `demo_edit_plan_validation.json` |

因此分镜控制权属于 Server/Renderer，不属于视频生成 Provider。模型输出不能直接改变镜头顺序、业务步骤绑定、事实素材引用或最终时间线。

## 15. 当前代码实现状态

实现位置：

- `backend/internal/media/minimax_h3_client.go`
- `backend/internal/media/minimax_h3_client_test.go`

### 15.1 已实现

- `MiniMax-H3` 固定模型校验；
- `POST /v2/video_generation` 创建任务；
- Bearer 鉴权；
- `dry_run`、`real`、`disabled` 模式；
- 2K、4～15 秒和宽高比校验；
- text、图片、视频、音频 content 类型；
- 帧模式与参考模式互斥校验；
- 参考图片、视频、音频数量上限校验；
- API Key 错误信息脱敏；
- 创建任务和请求校验单元测试；
- `GET /v2/query/video_generation/{task_id}` 单任务查询和 `task.content.url` 归一化；
- 独立 H3 Sidecar 配置工厂，默认 `disabled`；
- 专用 `CASCADE_MINIMAX_H3_MODE`、`MINIMAX_H3_API_KEY`、`MINIMAX_H3_BASE_URL`；
- `disabled`、`dry_run`、通用 MiniMax 密钥隔离和越界请求零 HTTP 调用测试。

当前 Sidecar 尚未注册到 Server 的执行、Director、Seedance fallback 或 A/B comparison 路由。仅设置 `CASCADE_ARK_MEDIA_MODE=real`、`MINIMAX_API_KEY`、`SEEDANCE_API_KEY` 或 `DOUBAO_API_KEY` 都不会创建 H3 Client，也不会触发 H3 视频生成。这是验收期间必须保持的隔离边界。

### 15.2 尚未实现或尚未接入

- 任务列表查询；
- `DELETE /v2/video_generation/{task_id}`；
- callback_url 公共请求字段和回调处理；
- 完整任务状态归一化；
- 输出 URL 下载和持久化；
- 下载后媒体探测与统一转码；
- H3 Provider 正式路由；
- 与 EditorSession 候选素材入口的连接；
- 内容级人工审核；
- 配额、成本、并发、重试和幂等控制。

### 15.3 仅尾帧模式的项目决策

官方 V2 文档允许：

```text
text + image_url(role=last_frame)
```

即仅尾帧图生视频。虽然官方契约允许，但当前项目明确不开放该模式：Server 能力 Profile 必须声明 `last_frame_only=false`，`last_frame` 必须与 `first_frame` 同时存在。

这属于“超出当前 Server 已开放、已验证能力边界”，不是“超出 H3 厂商理论能力”。无论 APP 是否通过自身校验，Server 都必须在 Provider 调用前重新校验。仅尾帧请求必须：

- 返回 `h3_last_frame_only_not_supported`；
- 调用轨迹标记 `provider_capability_not_enabled`；
- Provider HTTP 调用次数为 0；
- 不创建 task ID，不登记候选 artifact；
- 不通过补首帧、改写参数或放宽 Prompt 自动降级。

只有在能力 Profile 升版、输入/输出测试与质量验收全部完成后，才可通过独立变更开放；不得因官方支持而自动放行。

## 16. 正式启用前检查清单

- [x] 实现并测试单任务查询；
- [ ] 实现并测试取消/删除；
- [x] 明确仅尾帧模式的项目策略：当前禁止，调用前硬拒绝；
- [ ] 支持下载并立即转存时效 URL；
- [ ] 下载后校验真实 MIME、大小、编码、时长、尺寸和 FPS；
- [ ] 保存原始产物 SHA-256；
- [ ] 生成规范化副本并保存新 SHA-256；
- [ ] 区分 original 与 normalized artifact；
- [x] 使用独立 H3 开关，默认 disabled；
- [ ] 不复用 Seedance 的启用开关触发 H3；
- [ ] 接入 Provider-neutral 任务编排；
- [ ] 接入结构审核和内容审核；
- [ ] 保证候选生成失败不影响正式录屏交付；
- [ ] 增加 400、401、402、422、429、500/529 错误测试；
- [ ] 增加真实调用前的配额、超时和成本上限；
- [ ] 更新 APP 能力 Profile，但不向 APP 暴露具体模型 ID。

## 17. 资料来源与维护规则

本文基于 2026-07-31 获取的 MiniMax 官方接口资料整理：

- 创建视频生成任务；
- 查询视频生成任务；
- 查询视频生成任务列表；
- 取消或删除视频生成任务。

官方原始资料当前保存在本地开发资料目录，不作为仓库运行时依赖。仓库成员应以本文、代码和测试作为项目接入基线。

维护规则：

1. 官方 API 契约变化时先更新本文；
2. 再更新 Provider 适配器和测试；
3. 兼容性变化需要更新能力 Profile 版本；
4. 不得只改文档而假定代码已经支持；
5. 不得只改代码而遗漏项目限制和安全边界；
6. H3 之外的 MiniMax 语音、图像和旧视频模型必须使用独立文档，不得混入本文。
