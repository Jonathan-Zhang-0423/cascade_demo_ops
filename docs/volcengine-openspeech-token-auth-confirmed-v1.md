# OpenSpeech LAS / 豆包 TTS Token 鉴权：最终确认参数

> 日期：2026-07-20。来源：Token 鉴权模式完整请求、控制台入口和 VMS 上传成功响应的补充资料。
>
> 本文是 Token 模式的当前实现依据；Signature/HMAC 仅作为可选后备方案保留，不是第一版依赖。

## 1. 鉴权与正式接口

| 服务 | 提交 | 查询 | Token 鉴权 |
| --- | --- | --- | --- |
| LAS | `POST /api/v1/auc/submit?Action=SubmitAucTask&Version=2022-01-01` | `POST /api/v1/auc/query?Action=QueryAucResult&Version=2022-01-01` | `Authorization: Bearer; <token>`；该 token 与 body 的 `app.token` 为同一值 |
| TTS | `POST /api/v3/tts/submit?Action=SubmitAsyncTtsTask&Version=2022-01-01` | `POST /api/v3/tts/query?Action=QueryAsyncTtsResult&Version=2022-01-01` | 不使用 `Authorization`；使用 `X-Api-App-Id`、`X-Api-Access-Key`、`X-Api-Resource-Id` |

两类接口 host 均为 `https://openspeech.bytedance.com`，地域为 `cn-beijing`。Token 模式下不需自行计算 HMAC。

## 2. Server 配置映射

```dotenv
VOLC_LAS_APP_ID=<语音服务应用管理的 appid>
VOLC_LAS_TOKEN=<语音服务应用管理的 token>
VOLC_LAS_CLUSTER=<语音服务应用管理的 cluster>

VOLC_TTS_APP_ID=<语音服务 API Key 管理中的 App ID>
VOLC_TTS_ACCESS_KEY=<语音服务 API Key 管理中的 Access Key>
VOLC_TTS_RESOURCE_ID=seed-tts-2.0
```

不得手填或持久化：LAS/TTS 的任务 ID、TTS 音频下载 URL、LAS Bearer header、`unique_id`、用户请求 ID。Server 在运行时生成或取得它们。

LAS 的 `appid/token/cluster` 在“语音服务控制台 → 应用管理 → 创建应用 → 开通录音文件识别服务”获取。TTS 的 App ID/Access Key 在“语音服务控制台 → API Key 管理”获取。两服务若改用签名鉴权，可共用同一组主账号 AK/SK；Token 第一版无需它们。

## 3. LAS 确认请求

```http
POST https://openspeech.bytedance.com/api/v1/auc/submit?Action=SubmitAucTask&Version=2022-01-01
Content-Type: application/json
Authorization: Bearer; <VOLC_LAS_TOKEN>
```

```json
{
  "app": {"appid": "<VOLC_LAS_APP_ID>", "token": "<VOLC_LAS_TOKEN>", "cluster": "<VOLC_LAS_CLUSTER>"},
  "user": {"uid": "server-generated-pseudonymous-id"},
  "audio": {"format": "mp3", "url": "https://public-or-presigned.example/audio.mp3"},
  "additions": {"use_itn": "False", "withspeakerinfo": "True"}
}
```

查询使用相同 Bearer header，body 为 `appid`、`token`、`cluster` 和提交响应的 `id`。LAS 支持 HTTPS TOS 预签名 URL；拉取时 URL 有效即可，建议至少 1 小时。支持 301/302；最大 512 MB、最长 5 小时；结果保留 24 小时。

## 4. TTS 确认请求

```http
POST https://openspeech.bytedance.com/api/v3/tts/submit?Action=SubmitAsyncTtsTask&Version=2022-01-01
Content-Type: application/json
X-Api-App-Id: <VOLC_TTS_APP_ID>
X-Api-Access-Key: <VOLC_TTS_ACCESS_KEY>
X-Api-Resource-Id: seed-tts-2.0
```

提交 JSON 使用 `unique_id`、`req_params.text`、`req_params.speaker`、`audio_params.format/sample_rate/speech_rate/loudness_rate/enable_timestamp`；查询 JSON 只传 `task_id`。`task_status=1/2/3` 分别表示排队/完成/失败。

`speech_rate`、`loudness_rate` 范围均为 `-50..100`，默认 `0`；`silence_duration` 为 `0..3000ms`。完成后 `audio_url` 直接下载，无需二次鉴权，有效期 1 小时；服务端结果保留 7 天，单次文本上限 10 万字符。TTS 时间戳是秒级浮点，写入内部时间线前必须乘以 1000。

## 5. LAS 音频 URL：方案优先级

1. **TOS 预签名 HTTPS URL（首选）**：稳定、速度优先；建议有效期至少 1 小时。
2. **VMS `GetResourceUploadUrl`（次选）**：适合小文件快速上传，免单独开通 TOS。
3. 自有公网 HTTPS 服务器。

VMS 获取上传 URL：

```text
https://cloud-vms.volcengineapi.com?Action=GetResourceUploadUrl&Version=2022-01-01
service=vms; region=cn-north-1
body: {"FileName":"unique-audio.mp3"}
response: Result.UploadUrl
```

对 `Result.UploadUrl` 发起 PUT，携带与文件匹配的 `Content-Type`（例如 MP3 使用 `audio/mpeg`），无需额外签名。上传后，按资料说明将该 URL 去除签名 query 参数，得到长期可用于 LAS `audio.url` 的资源 URL。

实现时的额外保护：在提交 LAS 前，Server 必须先以无凭据 HTTPS GET 实测该去签名 URL 返回成功且内容类型/大小正确；如果不能访问，改用有效的 TOS 预签名 GET URL，绝不把无法验证的地址提交给 LAS。

## 6. 最终状态

协议资料已足够实现 Token 模式 LAS/TTS 适配器和 VMS/TOS 音频 URL 提供器。真实开关前仅剩：

1. 用户在本机密钥库或 `.env` 配置第 2 节值；
2. 使用无业务敏感内容的短 WAV/MP3 完成一次 LAS 与 TTS 端到端验收；
3. 验收日志只保存 request ID、task ID、状态和 SHA-256，不保存 token 或临时 URL。
