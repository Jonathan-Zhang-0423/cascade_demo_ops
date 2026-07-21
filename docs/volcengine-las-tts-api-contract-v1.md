# 火山 LAS / TTS 异步接口契约（待官方验真）

> 状态：已登记四组脱敏示例；尚未启用真实调用。
>
> 这些示例保留了字段层级和数据类型，但未包含可验真的签名规则、账号配置或 API Explorer 原始导出。因此本文只记录 Server 适配边界，不能把示例中的 URL、模型或参数当作已验证的厂商事实。

## 已掌握的接口结构

| 能力 | 已知结构 | Server 可据此准备 | 仍不可假设 |
| --- | --- | --- | --- |
| LAS 提交 | POST、异步、`audio_url`、热词、时间戳、置信度 | DTO、任务表、字幕候选转换 | 授权签名、真实 query/header 编码、资源 ID |
| LAS 查询 | `task_id`、终态、句/词时间戳和置信度 | 轮询状态机 | 查询鉴权、结果保留期、错误码语义 |
| TTS 提交 | 文本、音色、速度/音调/音量、格式 | DTO、任务表、候选音频登记 | 授权签名、真实模型/资源 ID、SSML 细节 |
| TTS 查询 | `task_id`、`download_url`、音频元数据 | 下载、SHA-256、媒体回验 | 下载 URL 生命周期、二次鉴权、回收规则 |

## LAS 请求和结果映射

示例声明的提交方式：

```text
POST https://las.volcengineapi.com/
Content-Type: application/json
Authorization: <待确认>
Region: cn-beijing; Service: las
Action: CreateRecognitionTask; Version: 2023-01-01
输入: HTTPS audio_url；模式: asynchronous
```

已出现的请求字段：`model`、`operator`、`resource_id`、`cluster`、`audio_url`、`audio_format`、`sample_rate`、`channels`、`language`、`enablesentencetimestamp`、`enablewordtimestamp`、`enable_punctuation`、`enable_confidence`、`enable_hotwords`、`hotwords`、`enable_vad`、`enable_denoise`、`callback_url`、`idempotency_key`、`user_id`、`app_id`。

查询示例：

```text
GET https://las.volcengineapi.com/?Action=GetRecognitionTaskResult&Version=2023-01-01&task_id=<task_id>
建议轮询：10–30 秒；终态：succeeded | failed | cancelled
```

成功结果中可用：`audio_info.duration_ms`、`result.text`、`result.segments[]`、句级 `start_ms/end_ms/text/confidence/speakerid`、词级 `words[]`、`hotwords_hit`、`silencesegments`。

适配规则：

- 句级 segment 生成字幕候选；词级字段仅用于细对齐。
- 示例中同时出现 `start_ms`/`startms`、`end_ms`/`endms`，适配器必须兼容两种拼写，并原样保存 vendor 响应。
- `confidence` 只能排序或提示，不能覆盖用户字幕、不能证明业务步骤正确。
- 用户确认后才转换为 `caption_cues[].source = asr_confirmed`。

## TTS 请求、查询与下载

示例声明：

```text
POST https://tts.volcengineapi.com/
Content-Type: application/json
Authorization: <待确认>
模式: asynchronous
```

已出现的请求字段：`model`、`resource_id`、`text`、`voiceid`、`speed`、`pitch`、`volume`、`emotion`、`enable_ssml`、`audio_format`、`sample_rate`、`channels`、`callback_url`、`idempotency_key`、`user_id`、`app_id`。

查询示例：

```text
GET https://tts.volcengineapi.com/?Action=GetTtsTaskResult&Version=2023-01-01&task_id=<task_id>
建议轮询：5–10 秒；终态：succeeded | failed | cancelled
```

成功结果使用 `audio_info`（时长、采样率、声道、格式、大小）和 `result.download_url`。该 URL 是临时凭据：绝不写入 Git、日志、`DemoEditPlan` 或前端持久化状态。

下载成功后，Server 必须限制重定向、大小和超时，将文件写入受控目录，计算 SHA-256，并用 FFprobe/FFmpeg 回验格式与元数据。它只能先成为待试听确认候选；确认后才可成为 `narration_audio`，并以 `tts_confirmed` 写入 `narrations[]`。

## 安全和运行策略

- 只允许 HTTPS `audio_url` 或后续确认的 provider asset reference；禁止本地路径。
- `idempotency_key` 由 Server 生成，并绑定输入 SHA-256、操作和规范化参数；重试复用该 key。
- 未有回调验签说明前关闭 callback，第一版仅轮询。
- `400/401` 不自动重试；`429/503` 指数退避并受总等待上限约束；终态失败或取消不创建候选素材。
- 不得入库：真实 Authorization、AccessKey/SecretKey、SessionToken、API Key、预签名下载 URL、真实 `user_id/app_id`、业务音频公开链接和完整原始响应。

## 仍必须补充的最小信息

1. LAS 和 TTS 的真实 API Explorer/cURL 导出：完整 method、query、header 名称、body、成功响应；所有鉴权值替换为 `<redacted>`。
2. 鉴权机制：Bearer、火山签名 V4、临时凭据或其他；需给出 canonical request、签名覆盖范围、时钟容差和轮换方式。
3. `Action`、`Version`、`task_id` 的准确位置，以及 GET 查询是否需 `Authorization`。
4. `resource_id`、`cluster`、`operator`、`model` 的控制台来源、地域及测试/生产差异（只需字段说明和脱敏示例）。
5. `audio_url` 是否允许预签名 URL、最短有效期、域名/重定向限制、最大文件及格式。
6. callback body、签名 header、验签算法、重放保护和重试策略；若没有请明确。
7. TTS 下载 URL 的有效期、是否二次鉴权、是否可能返回 `audio_base64`、最大大小、Content-Type 和校验和。
8. 完整状态与错误码：尤其 `running`、超时、取消、配额不足、可否安全重试。

## 发给控制台客服/API Explorer 的提示词

```text
我们要在 Server 侧接入 LAS 异步语音识别和豆包语音 TTS 异步合成。

请分别提供当前账号、cn-beijing 地域下可在 API Explorer 验证的官方请求/响应示例。密钥、令牌、签名、预签名 URL、真实 user_id/app_id 可替换为 <redacted>，但必须保留字段名、字段位置、数据类型，以及 query/header/body 的完整结构。

LAS 请提供：CreateRecognitionTask 和 GetRecognitionTaskResult 的 method、endpoint、Action/Version/task_id 位置、全部必填 header、鉴权方式；audio_url 的预签名 URL/有效期/重定向限制；pending/running/succeeded/failed/cancelled 的响应；句级和词级时间戳、置信度、热词、VAD/降噪字段；callback 的验签和重试规则。

TTS 请提供：创建异步任务和查询任务的完整请求；model/resource_id/voiceid 的控制台来源；speed/pitch/volume/emotion/SSML 的合法值和单位；全部状态响应；download_url/audio_base64 的返回规则、有效期、二次鉴权、最大文件和 checksum；错误码、限流、幂等、超时、取消、数据保留和商用授权。

请特别确认：这些接口属于 LAS/豆包语音服务，而不是通用 doubao-seed-2-0-mini-260428 Responses API；以及示例中的 operator、cluster、resource_id、Action、Version 是否是当前正式可用值。
```
