# Seedance 2.5 与 Ark 媒体能力迁移基线

状态：**文档与设计基线已整理；运行配置尚未切换；未做真实厂商调用验收。**  
目标：后续以 Seedance 2.5 替换 Server 当前 Seedance 2.0 的候选视频 Provider；不改变“真实产品录屏优先、生成素材仅为候选”的交付边界。

回调接收、幂等对账、限流、Endpoint 隔离和预算熔断的生产规则见
[Seedance 2.5 生产回调、限流与预算控制](seedance-2-5-production-controls.md)。

## 1. 资料来源与采用范围

本基线从以下本地资料提炼项目直接可用的约束、模型参数和提示词规则：

| 来源 | 已采用的内容 |
| --- | --- |
| `C:\Users\15193\Desktop\新技术文档\Doubao Seedance 2.5 教程.md` | Seedance 2.5 模型、任务路由、输入限制、输出规格、编辑/延长限制、开通条件 |
| `C:\Users\15193\Desktop\新技术文档\Doubao Seedance 2.5 提示词指南.md` | 结构化提示词、参考素材映射、时间轴、编辑和关键帧写法 |
| `C:\Users\15193\Desktop\模型参数\创建视频生成任务 API.md` | Ark 任务创建/查询基础路径与鉴权模型 |
| `C:\Users\15193\Desktop\模型参数\视频生成教程.md` | 2.0 既有能力及与 2.5 的兼容参照 |
| `C:\Users\15193\Desktop\模型参数\Seedream 4.0-5.0 教程.md` | 非产品 UI 候选图片的模型、尺寸和多参考约束 |
| `C:\Users\15193\Desktop\模型参数\图片生成 API.md` | Ark 图片生成基础接口与结果形态 |
| 用户补充的 Seedance 2.5 API 速查资料 | 完整请求字段、时间戳 Prompt 语法、素材限制、输出容器、查询响应、错误码与生命周期说明 |

本文件不复制厂商示例素材、临时 URL、密钥、账号数据或完整示例项目；只保存本项目需要遵守的规范。

## 2. 迁移目标与不可变边界

| 项目 | 决策 |
| --- | --- |
| 目标 Provider | Seedance 2.5 |
| 目标模型 ID | `doubao-seedance-2-5-260628` |
| Ark 基础地址 | `https://ark.cn-beijing.volces.com/api/v3` |
| 创建任务 | `POST /contents/generations/tasks` |
| 查询任务 | `GET /contents/generations/tasks/{task_id}` |
| 鉴权 | `Authorization: Bearer <ARK_API_KEY>` |
| 当前代码配置 | **仍为 Seedance 2.0；本次未修改** |

### 2.1 账号能力快照（2026-08-19，脱敏）

用户提供的方舟账号信息显示：Seedance 2.5 已开通、模型为 Public、当前账号可直接以 Model ID 调用，不要求预先创建 Endpoint。该账号满足模型开通的余额门槛，且已有一次 2.5 调用记录。账号编号、余额金额、用量明细、资源包和任何凭据均不写入仓库。

| 能力 | 已确认的结论 | Server 采用方式 |
| --- | --- | --- |
| 调用标识 | 可直接用 `doubao-seedance-2-5-260628`；Endpoint ID 是可选的高级治理能力 | 首期按 Model ID；生产如按业务隔离再创建 Endpoint，但不能突破共享模型总池 |
| 默认限流（企业档） | RPM 600、并发 10；主账号+模型版本共享总池；超过并发进入 `queued` | 本地并发器上限必须不高于 10，并以任务状态轮询/回调记录排队 |
| 日/月硬配额 | 厂商未公布固定数值；受余额、RPM、并发和控制台限额共同约束 | 在发起候选任务前做本地预算预估；收到 `SetLimitExceeded` 即停止重试 |
| 取消与失败 | 参数、审核、取消、过期、内部错误等未生成产物的失败不计费；仅成功任务按 `usage.completion_tokens` 计费 | 所有失败都记录原因与关联 ID；不得把失败任务当作候选成功 |
| 实际用量 | 查询任务响应含 `usage.completion_tokens`；控制台/CLI 可按模型统计 | 下载完成后写入候选审计，不记录 API Key 或完整临时 URL |

成本采用厂商返回的 `usage.completion_tokens` 作为单任务事实依据；预估仅用于调用前拦截。2026-08-19 的价目基线为：480p/720p 的非视频输入 70 元/百万 token、含视频输入 42 元/百万 token；1080p 当期存在限时折扣，必须在实际调用日前以控制台最新价为准。费用公式为：`单价 × completion_tokens / 1,000,000`。Server 需要把预计与实际 token、价格版本和任务状态一并写入候选审计。

Seedance 2.5 创建任务的完整顶层字段基线如下：

```json
{
  "model": "doubao-seedance-2-5-260628",
  "content": [],
  "omni_reference_task_type": "auto",
  "resolution": "720p",
  "ratio": "adaptive",
  "duration": -1,
  "generate_audio": false,
  "watermark": false,
  "output_format": "mp4",
  "seed": -1,
  "return_last_frame": false,
  "callback_url": "<optional controlled HTTPS callback>",
  "execution_expires_after": 172800,
  "priority": 0,
  "safety_identifier": "<hashed pseudonymous user id>",
  "tools": []
}
```

Provider 默认值不等于项目默认值：Provider 可能默认生成有声视频，但项目默认仍为 `generate_audio=false`，旁白优先走独立 TTS，以保持音频来源可追溯。

不可变边界：

1. Browser Agent 的真实录屏、截图、Trace、步骤结果仍是业务事实来源。
2. Seedance 2.5 生成的视频、尾帧和音频均为非权威候选素材，不能替代业务步骤证据或改写执行包。
3. 候选素材必须经过下载、媒体探测、SHA-256、来源审计、显式审核和编辑器质量门禁后，才可能进入演示计划。
4. Provider 失败、输入不合法、预算不足或候选未通过审核时，必须继续走无生成候选的确定性录屏成片路线。

## 3. Seedance 2.5 任务类型与硬约束

| 任务类型 | 触发条件 | Server 强制约束 | 当前项目建议 |
| --- | --- | --- | --- |
| 文生视频 | 仅文本 | `ratio`、`duration` 无特殊锁定 | 不用于替代产品 UI；仅限非事实展示候选 |
| 首帧/首尾帧 | `first_frame` / `last_frame` | `ratio=adaptive`；首尾帧建议同一画幅 | 仅作为非产品视觉候选 |
| 参考生视频 | 至少一个 `reference_image`、`reference_video` 或 `reference_audio` | 可用 `omni_reference_task_type=auto` | 仅使用已审核、脱敏、可发布的素材 |
| 视频编辑 | 参考视频 + 明确编辑意图 | `omni_reference_task_type=edit`；`ratio=adaptive`；`duration=-1`；视频 4–30 秒 | 不得编辑或伪造产品业务证据；可用于演示候选 |
| 视频延长 | 参考视频 + 明确延长意图 | `omni_reference_task_type=extend`；`ratio=adaptive` | 不得延长真实证据以制造未发生的业务事实 |

任务意图和参数不一致可能出现：

- 同步错误：显式 `edit` / `extend` 时的参数前置校验失败；
- 异步错误：模型实际判定任务类型后发现 `ratio`、`duration` 或素材不满足约束；
- 典型错误语义：`InvalidParameter.TaskTypeMismatch`、`InvalidParameter.TaskTypeConstraint`。

Server 必须在提交前编译任务类型并记录理由；不得依赖异步失败作为正常分流手段。

## 4. 输入素材与输出规格

### 4.1 Provider 上限

| 素材类型 | 单次任务上限 | 额外限制 |
| --- | --- | --- |
| 参考素材总数 | 50 | 图像、视频、音频合计 |
| 图片 | 30 张 | 单张不高于 4K |
| 视频 | 10 段 | 所有视频总时长不超过 30 秒 |
| 音频 | 10 段 | 所有音频总时长不超过 30 秒 |
| 视频编辑输入 | 1 个以上参考视频 | 4–30 秒；实践建议 20 秒以内更稳定 |
| 视频参考图编辑 | 1–5 张优先 | 6–8 张可尝试但稳定性下降 |

项目提交策略不能直接使用上限：首轮候选任务应使用最小、明确、已通过质量门禁的素材集合。对真实 UI 演示，优先选取一个明确通过的业务 Stage 的规范化片段，而非将整段录屏或大量截图交给模型。

### 4.2 输出规格

| 参数 | Seedance 2.5 支持范围 | 当前项目候选策略 |
| --- | --- | --- |
| `duration` | `4..30` 秒，或 `-1` 由模型选择 | 首轮 5–15 秒；编辑任务固定 `-1` |
| `ratio` | `21:9`、`16:9`、`4:3`、`1:1`、`3:4`、`9:16`、`adaptive` | 横屏候选优先 `16:9`；锁定类任务用 `adaptive` |
| `resolution` | 480p（8-bit）、720p（8-bit）、1080p（10-bit） | 候选优先 1080p；最终交付仍由本地编辑器生成 2K 与 1080p 双规格 |
| `output_format` | `mp4`、`mov` | 普通候选用 MP4；编辑/延长优先 MOV 后再本地规范化 |
| `generate_audio` | 支持 | 默认 `false`；项目旁白优先走独立、可审计的 TTS 路线 |
| `return_last_frame` | 支持 | 仅作为后续候选链路参考，不替代截图证据 |

Seedance 2.5 的 1080p 为 10-bit 输出。进入项目编辑器前必须探测编解码、像素格式、分辨率、帧率、时长和声道；必要时生成独立的编辑器兼容派生文件，原始下载文件保持不变。

### 4.3 `content[]` 输入字段

| 类型 | URL 形式 | 角色与限制 |
| --- | --- | --- |
| `text` | `content[].text` | 中文不超过 500 字，英文不超过 1000 词；提示词中可使用时间戳语法 |
| `image_url` | HTTPS URL、`data:image/*;base64,...`、`asset://<ASSET_ID>` | `first_frame`、`last_frame` 或 `reference_image`；图片格式 jpeg/png/webp/bmp/tiff/gif/heic/heif，单图小于 30MB，请求体不超过 64MB，宽高比 `[0.4,2.5]`，边长 300–6000px |
| `video_url` | HTTPS URL 或 `asset://<ASSET_ID>`；不支持 Base64 | 角色固定为 `reference_video`；MP4/MOV，H.264/H.265 + AAC/MP3/PCM，单段 2–30 秒（编辑 4–30 秒），单段小于 200MB，分辨率 480p/720p/1080p/4K，边长 300–6000px，总像素约 407,696–8,295,044，FPS 24–60，请求体不超过 64MB |
| `audio_url` | HTTPS URL、`data:audio/*;base64,...`、`asset://<ASSET_ID>` | 角色固定为 `reference_audio`；WAV/MP3，单段 2–30 秒、单段小于 15MB，请求体不超过 64MB |

Provider 宣称的总上限为 50 个参考素材，但项目仍需执行更保守的素材门禁：只提交明确通过的 Stage 片段和必要参考图，避免把整段业务录屏交给模型。

### 4.4 任务类型前置字段

| `omni_reference_task_type` | 用途 | 前置约束 |
| --- | --- | --- |
| `auto` | 由模型结合素材和 Prompt 判断 | 可能在异步阶段因任务类型不兼容而失败 |
| `reference` | 全模态参考生视频 | 无编辑/延长的特殊锁定 |
| `edit` | 视频编辑 | 至少一个 `reference_video`；视频 4–30 秒；`ratio=adaptive`、`duration=-1` |
| `extend` | 视频延长 | 至少一个 `reference_video`；`ratio=adaptive` |

即使前置校验通过，Prompt 意图与显式类型不一致仍会返回 `InvalidParameter.TaskTypeMismatch`。

## 5. Prompt 时间戳与素材指代

Seedance 2.5 没有独立的 `segments`、`timestamps` 或 `edits` API 字段。时间戳编辑通过 `content[].text` 的自然语言语法实现：

```text
[0-10秒] 第一段画面与动作。
[10-20秒] 第二段画面与动作。
[20-30秒] 第三段画面与动作。
```

规则：

- 时间区间应连续，不能出现空洞；推荐以 1 秒为最小叙事单位。
- 时间点可使用 `@5秒` 表示第 5 秒触发事件。
- 编辑动作应明确引用 `@video1`，图片和音频分别使用 `@image1`、`@audio1`。
- Prompt 必须先声明素材映射，再描述动作、镜头、台词和音频；不能只依赖图片内文字标注。
- 不用时间戳描述高频重复动作，例如“每秒摇头三次”。

## 6. 查询响应、生命周期与错误审计

查询响应的关键字段：

```json
{
  "id": "<task id>",
  "model": "doubao-seedance-2-5-260628",
  "status": "queued|running|cancelled|succeeded|failed|expired",
  "content": {
    "video_url": "<temporary url>",
    "last_frame_url": "<temporary url>"
  },
  "created_at": 0,
  "updated_at": 0,
  "duration": 15,
  "framespersecond": 24,
  "resolution": "1080p",
  "ratio": "16:9",
  "generate_audio": false,
  "seed": 0,
  "execution_expires_after": 172800,
  "usage": { "completion_tokens": 0, "total_tokens": 0 },
  "service_tier": "default",
  "safety_identifier": "<hashed pseudonymous user id>",
  "error": null
}
```

- 视频和尾帧 URL 有效期约 24 小时，下载次数上限约 100 次；Server 应立即下载、探测并保存本地受控副本，不持久化临时 URL。
- 任务记录通常从 `created_at` 起保留 7 天。
- `duration` 可能是按 24fps 总帧数向下取整的近似值；编辑任务可能与实际时长存在小数秒差异。
- `status=expired` 必须视为失败，不得继续轮询或标记候选成功。
- `cancelled` 只会由排队中的任务被 DELETE 后产生；官方未承诺它会通过 `callback_url` 推送。取消工作流必须以 DELETE 响应和 GET 查询对账为事实来源。
- 失败时保留脱敏的 `error.code`、`error.message`、provider/model、task ID 摘要和请求关联 ID。

重点错误类别包括：

- `InvalidParameter.TaskTypeConstraint`
- `InvalidParameter.TaskTypeMismatch`
- `InvalidParameter.*` / `MissingParameter.*`
- `AuthenticationError`、`AccessDenied`、`ModelNotOpen`
- `EndpointRPMExceeded`、`EndpointTPMExceeded`、`QuotaExceeded`、`InflightBatchsizeExceeded`
- `Input*SensitiveContentDetected`、`Output*SensitiveContentDetected`
- `InternalServiceError`

### 6.1 私有 TOS 输入与下载保全

火山官方文档《通过 TOS 内网预签名 URL 传入文件》已明确确认：华北 2（`cn-beijing`）**私有** TOS 桶可使用 `*.tos-cn-beijing.ivolces.com` 域名签发 HTTPS GET 预签名 URL，在**不公开源文件**的前提下作为方舟输入。Seedance 2.5 的 `image_url.url`、`video_url.url` 可直接使用该 URL；`audio_url.url` 的字段形式对称，音频小文件也可保守地使用合规 Base64。

此处的 `ivolces.com` 是官方为方舟服务端拉取素材定义的内网预签名域名，不能与 `*.tos-cn-beijing-internal.volces.com` 这类仅限同 VPC 客户端访问的私网 endpoint 混为一谈。Server 不得以“浏览器能否直接打开”作为 `ivolces.com` URL 有效性的判断。

Server 的最小安全发布规则：

1. 素材桶必须位于 `cn-beijing`，保持 private；只为已审核、脱敏、可发布的候选输入签发 `https`、仅 GET 的 `ivolces.com` 预签名 URL。
2. 有效期推荐 1 小时（最低 30 分钟、最长不建议超过 24 小时），覆盖 `queued` 排队、服务端拉取和有限重试；模型输入视频不能用 Base64。
3. 不使用 Cookie、自定义 Header、Referer/IP 白名单；方舟下载节点不承诺固定出口 IP。普通公网 `volces.com` URL 仅作为兼容回退，不是生产首选。
4. Server 仅保存 URL 摘要、签发/到期时间和对象版本；不得把完整预签名查询串写入 Git、普通日志或客户端。
5. 产物 URL 约 24 小时有效且视频单 URL 下载约 100 次；收到成功状态后立即下载、用 FFprobe 探测、计算 SHA-256，并将原始文件转存到受控私有目录。

### 6.2 原生有声与独立 TTS 的边界

`generate_audio=true` 的 MP4 产出为 H.264/AVC + AAC、`yuv420p`、单声道；MOV 为 H.264/AVC + PCM、`yuv444p`、单声道。厂商不提供固定的采样率、码率、`voice_id`、语速、情绪或 SSML 字段；语言、音色、台词时机仅能通过 Prompt 和可选参考音频表达。

因此项目保持以下决策：

- 默认 `generate_audio=false`，旁白走独立、可审计的豆包 TTS 链路；独立 TTS 必须在后期与无声视频混音。
- 不能指望 `reference_audio` 或视频原声被原样保留：原生有声视频会端到端重生成音轨。
- 用户明确要求测试模型原生音画时，才允许建立独立的 `generate_audio=true` 非权威候选；下载后由 FFprobe 写入实际声道/采样率/码率审计字段。
- 无论有声或无声，Seedance 2.5 单价按输入是否含视频及输出分辨率计算，不按音频开关分档；独立 TTS 另按字符计费。

## 7. 结构化提示词编译规则

Server 不应直接拼接自由文本。每次候选请求应从已验证的素材和编辑计划编译为以下结构：

1. **任务意图**：参考、编辑或延长；必须与 `omni_reference_task_type` 一致。
2. **素材映射**：按上传顺序声明“图片 N / 视频 N / 音频 N”的用途，例如主体外观、动作、运镜、场景、音色或背景音乐。
3. **事实边界**：明确哪些素材只参考动作/运镜，哪些仅参考外观/风格；不得把模型候选描述为已发生的产品操作。
4. **时间轴**：以连续整数秒区间或“镜头 N”描述，不留时间空洞，不用时间戳控制不可靠的高频次数。
5. **镜头语言**：景别、机位、推进、环绕、转场及节奏，避免前后矛盾。
6. **负向控制**：可明确“不要字幕”“不要 BGM”“不要新增对白”等展示限制。
7. **输出要求**：比例、时长、格式、是否生成音频，且不与锁定型任务的硬约束冲突。

多宫格分镜只提供大致结构，不保证严格画面一致；若必须按顺序贴近每个画面，应使用独立关键帧并在提示词开头声明顺序。多宫格建议小于 15 格，避免在图中堆放文字。

## 8. 从模型参数目录复用的 Seedream 基线

Seedream 仍定位为标题卡、章节过渡或抽象品牌视觉等非产品 UI 候选，不替代网页录屏。已有参数基线位于 `docs/ark-model-parameters.md`：

| 模型 | Model ID | 可用定位 |
| --- | --- | --- |
| Seedream 5.0 Pro | `doubao-seedream-5-0-pro-260628` | 默认的高精度单图候选 |
| Seedream 5.0 Lite | `doubao-seedream-5-0-260128` / `doubao-seedream-5-0-lite-260128` | 多图、组图候选 |
| Seedream 4.5 | `doubao-seedream-4-5-251128` | 兼容候选 |
| Seedream 4.0 | `doubao-seedream-4-0-250828` | 兼容候选 |

接口为 `POST /images/generations`。项目已具备基础客户端，但多图输入、图生图、组图的完整请求字段必须以当前官方 API 文档完成一次请求级对齐后才能开真实调用。

## 9. 当前资料与项目策略的冲突

| 主题 | 新资料描述 | 项目必须采用的处理 |
| --- | --- | --- |
| 素材 URL | 教程使用公网可访问 URL | 首选 `cn-beijing` 私有 TOS + `ivolces.com` HTTPS GET 预签名 URL；官方已确认可作为 2.5 输入，不设长期公共读 |
| 生成有声视频 | Seedance 可原生生成 | 默认关闭，避免与独立 TTS、字幕和音画审计产生不可追溯的混音 |
| 长视频直出 | 可达 30 秒 | 不替代本地编辑器；长演示仍由真实录屏、审核候选和双规格渲染组成 |
| 编辑真实视频 | 模型可做 V2V | 不能用于篡改、补造或替换真实产品业务步骤 |

## 10. 仍缺的官方资料与手动确认项

在切换 `SEEDANCE_MODEL` 或发起首个 Seedance 2.5 真实候选任务前，仍缺少以下可核验资料：

附件已补齐并纳入本基线的内容：账号已开通与 Model ID 直调、企业档 RPM/并发、失败计费规则、预算/用量入口、私有 TOS 预签名 URL 输入、原生有声音轨边界、Seedream 多图/图生图/组图字段及权限、2.5 创建请求字段、`content[]` 素材 URL 形式及限制、`omni_reference_task_type` 强制参数、Prompt 时间戳语法、MP4/MOV 输出说明、查询响应字段、临时 URL 生命周期和常见错误码。

在切换 `SEEDANCE_MODEL` 或发起首个 Seedance 2.5 真实候选任务前，只剩以下**项目级验证**，而不是再索要通用 API 字段：

1. 对一个脱敏、非业务事实的最小候选，验证 TOS 预签名 URL 在实际方舟下载节点可读，并审计签发时间、URL 摘要和对象哈希。
2. 用 5–15 秒的无声候选验证 1080p 10-bit 下载、FFprobe 探测、SHA-256、编辑器兼容派生和候选审计闭环。
3. 若要启用原生有声候选，实测下载文件的声道、采样率、码率和 TTS 混音策略；在此之前不将其作为默认配音路径。
4. 由部署负责人确认预算阈值、单任务最大成本、Endpoint 划分和并发上限；Server 再把这些值写成可配置限额。
5. 若启用 callback，验证公开 HTTPS 接收端的一次性 secret 脱敏、1 秒内入队应答、重复/乱序回调与轮询对账；不把回调作为唯一状态来源。

上述验证完成前，Server 可保持现有确定性录屏成片；不得将 Seedance 2.5 配置为生产默认值或声称其真实可用。

上述资料补齐前，Server 可保持现有确定性录屏成片；不得将 Seedance 2.5 配置为生产默认值或声称其真实可用。
