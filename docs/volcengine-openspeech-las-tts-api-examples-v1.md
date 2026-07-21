# 火山 OpenSpeech LAS 与豆包 TTS：最新脱敏接口参数

> 整理日期：2026-07-20。来源：提供的 LAS 标准版和 TTS 异步合成脱敏 API Explorer 示例。
>
> 本文以最新示例为准；它修正了旧草案中将 LAS/TTS 写作 `las.volcengineapi.com` 或 `tts.volcengineapi.com` 的未验证假设。真实调用仍必须由 Server 生成签名，不能把 API Explorer 的 Authorization 直接复制到代码或配置文件。

## 1. LAS 异步语音识别（录音文件识别标准版）

| 项 | 最新确认值 |
| --- | --- |
| 提交接口 | `POST https://openspeech.bytedance.com/api/v1/auc/submit?Action=SubmitAucTask&Version=2022-01-01` |
| 查询接口 | `POST https://openspeech.bytedance.com/api/v1/auc/query?Action=QueryAucResult&Version=2022-01-01` |
| 鉴权 | `HMAC-SHA256 Credential=…/YYYYMMDD/cn-beijing/las/request, SignedHeaders=content-type;host;x-date, Signature=…` |
| 必填 header | `Content-Type`、`Host`、`X-Date`、`Authorization` |
| 提交结果 ID | `resp.id` |
| 请求认证信息 | body 内 `app.appid`、`app.token`、`app.cluster`（敏感，绝不入库） |
| 输入音频 | `audio.format`、HTTPS `audio.url` |
| 文件约束 | 示例说明：最长 5 小时、最大 512 MB；转写结果保留 24 小时 |

提交 body：

```json
{
  "app": {"appid": "<redacted>", "token": "<redacted>", "cluster": "<redacted>"},
  "user": {"uid": "opaque-user-id"},
  "audio": {"format": "mp3", "url": "https://example.com/obj/sample.mp3"},
  "additions": {"use_itn": "False", "with_speaker_info": "True"}
}
```

查询 body：

```json
{"appid": "<redacted>", "token": "<redacted>", "cluster": "<redacted>", "id": "<submit-resp.id>"}
```

成功结果位于 `resp`：`text`、`utterances[]`、`utterances[].start_time/end_time`、词级 `words[].start_time/end_time` 和说话人 `additions.speaker`。时间单位为毫秒。第一版字幕候选按 `utterances[]` 建立；词级结果仅用于细对齐。

## 2. 豆包语音 TTS 异步合成

| 项 | 最新确认值 |
| --- | --- |
| 提交接口 | `POST https://openspeech.bytedance.com/api/v3/tts/submit?Action=SubmitAsyncTtsTask&Version=2022-01-01` |
| 查询接口 | `POST https://openspeech.bytedance.com/api/v3/tts/query?Action=QueryAsyncTtsResult&Version=2022-01-01` |
| 资源 ID | header `X-Api-Resource-Id: seed-tts-2.0` |
| 必填 header | `X-Api-App-Id`、`X-Api-Access-Key`、`X-Api-Resource-Id`、`X-Api-Request-Id`、`Content-Type`、`X-Date`、`Authorization` |
| 鉴权 | `HMAC-SHA256 Credential=…/YYYYMMDD/cn-beijing/tts/request, SignedHeaders=content-type;host;x-date;x-api-app-id;x-api-access-key;x-api-resource-id, Signature=…` |
| 排队/完成/失败 | `task_status=1/2/3` |
| 成品地址 | `data.audio_url`；示例说明下载链接有效期 1 小时 |
| 结果保留 | 示例说明合成音频在服务端保留 7 天 |

提交 body：

```json
{
  "user": {"uid": "opaque-user-id"},
  "unique_id": "server-generated-idempotency-id",
  "req_params": {
    "text": "…",
    "speaker": "zh_female_shuangyue_bigtts",
    "audio_params": {
      "format": "mp3",
      "sample_rate": 24000,
      "speech_rate": 0,
      "loudness_rate": 0,
      "enable_timestamp": true
    },
    "additions": {"silence_duration": 1000}
  }
}
```

提交成功：`code=20000000`，任务 ID 位于 `data.task_id`，排队状态为 `data.task_status=1`。查询 body 仅含 `{"task_id":"…"}`。完成后返回 `data.audio_url`、`sentences[]`、句级 `startTime/endTime`、词级 `words[].word/startTime/endTime/confidence`、`url_expire_time`。

TTS 时间戳示例为秒（浮点），导入内部 `CaptionCue` 或对齐数据时必须乘以 1000 并取整；LAS 的 `start_time/end_time` 则保留毫秒。绝不混用两种单位。

## 3. Server 实施规则

- Server 生成 `X-Date`、`X-Api-Request-Id` 与 TTS `unique_id`；不依赖浏览器或 API Explorer 生成。
- 真实签名和 app/token/AK/SK 只能来自环境变量或凭据库；不写入 `DemoEditPlan`、数据库普通字段、日志或 Git。
- 上传到 LAS 的音频必须是可访问 HTTPS URL；本地路径不能直接作为 `audio.url`。
- TTS `audio_url` 下载后，限制大小/重定向/超时，计算 SHA-256，FFprobe 回验后才登记为候选音频。
- ASR 和 TTS 的文字、词级置信度、说话人和时间戳都是编辑候选信息，不得改变业务步骤或代表业务执行成功。

## 4. 签名接入状态

当前已明确端点、请求结构和签名字段，但提供的通用签名示例仍不足以手写生产实现。完整核对和提示词见 [volcengine-hmac-sha256-signature-readiness-v1.md](./volcengine-hmac-sha256-signature-readiness-v1.md)。在获得官方 Go SDK 或可验证测试向量前，不开启真实调用。

```text
请提供上述 LAS AUC 与 TTS v3 接口的 HMAC-SHA256 签名计算官方文档或 SDK 示例：canonical request 的组成、payload hash、日期/区域/service、签名密钥派生、header 排序、query 编码规则和时钟容差。请使用 <redacted> 替换所有真实密钥与签名值。
```
