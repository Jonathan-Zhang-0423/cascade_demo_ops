# 豆包语音合成 2.0：异步 HTTP TTS 接口基线

## 2026-08-20 implementation evidence

- The Server candidate preflight reached OpenSpeech successfully after the
  local network policy was lifted.
- The provider rejected object-valued `additions` with code `55000000`.
  The active adapter now sends `additions` as a JSON-encoded string, while
  `speech_rate` and `loudness_rate` are sent in `audio_params`.
- The active adapter now sends a Server-generated opaque `user.uid` and
  `unique_id`, and queries with `task_id`.
- The current real response is `55000000: load grant: requested grant not
  found in SaaS storage`. This is an OpenSpeech console grant issue for the
  configured TTS App ID / Access Key / Seed-TTS 2.0 resource, not a network
  or request-body issue. Do not mark TTS as accepted until the grant is
  restored and the candidate download plus FFprobe preflight succeeds.

状态：待真实账户验收。  
来源：`C:\Users\15193\Desktop\豆包TTS技术文档\TTS技术文档.txt`（2026-08-19 整理）。  
适用范围：Server 生成演示视频时的候选旁白合成；不得作为 Browser Agent 业务步骤成功的证据。

## 1. 已确认的服务路线

当前已开通的“语音合成 2.0”使用异步 HTTP 接口：

| 操作 | 方法 | URL |
| --- | --- | --- |
| 提交合成任务 | `POST` | `https://openspeech.bytedance.com/api/v3/tts/submit` |
| 查询合成任务 | `POST` | `https://openspeech.bytedance.com/api/v3/tts/query` |

该路线与 Server 当前的 OpenSpeech TTS 适配器一致；它不是旧版 `/api/v1/tts_async/*` 接口，也不是 WebSocket、SSE 或语音识别接口。

## 2. 鉴权与环境变量

接口不使用 `Authorization`、`X-Date` 或 HMAC。每个提交和查询请求都必须携带：

| Header | 环境变量 | 说明 |
| --- | --- | --- |
| `X-Api-App-Id` | `VOLC_TTS_APP_ID` | 语音合成服务的 App ID |
| `X-Api-Access-Key` | `VOLC_TTS_ACCESS_KEY` | 语音服务 API Key 管理中的 Access Key |
| `X-Api-Resource-Id` | `VOLC_TTS_RESOURCE_ID` | 当前服务固定为 `seed-tts-2.0` |
| `X-Api-Request-Id` | Server 运行时生成 | 每次请求的随机、不可预测请求 ID；不写入配置 |
| `Content-Type` | 固定 | `application/json` |

另外需要：

```dotenv
VOLC_TTS_SPEAKER=<控制台音色列表中的完整 speaker ID>
VOLC_TTS_BASE_URL=https://openspeech.bytedance.com
```

`seed-tts-2.0` 是请求使用的 Resource ID/模型标识；控制台实例名、TOS AccessKey、Ark/Seedance API Key、主账号 AK/SK 均不能替代它或 `VOLC_TTS_ACCESS_KEY`。

不得把 App ID、Access Key、任务 ID、下载 URL、请求 ID 或签名写入 Git、日志正文、DemoEditPlan 或 App 执行包。

## 3. 提交任务

提交请求至少包含文本、音色和音频参数。脱敏的最小示例：

```json
{
  "req_params": {
    "text": "这是一次无业务敏感信息的语音合成验收。",
    "speaker": "<VOLC_TTS_SPEAKER>",
    "audio_params": {
      "format": "mp3",
      "sample_rate": 24000,
      "enable_timestamp": true
    },
    "additions": {
      "speech_rate": 0
    }
  }
}
```

已确认约束：

- `speaker` 是异步 HTTP 接口的音色字段；不要使用 WebSocket 文档中的 `voice_type` 字段名。
- 输出格式支持 `mp3`、`ogg_opus`、`pcm`、`wav`。
- 推荐首轮验收：`mp3`、`24000Hz`、`enable_timestamp=true`。
- `speech_rate` 和 `loudness_rate` 范围为 `-50..100`，其中 `0` 表示默认值。
- 当前语音合成 2.0 音色不支持 SSML；Server 不得注入 SSML。
- 音调能力属于可选后续能力；未确认请求字段兼容性前，首轮验收不传递音调参数。

## 4. 查询与结果

查询使用同一组鉴权 Header。任务状态的官方语义：

| `task_status` | 含义 | Server 行为 |
| --- | --- | --- |
| `1` | 处理中 | 在总超时内轮询 |
| `2` | 成功 | 下载音频并执行 FFprobe、SHA-256 和候选审计 |
| `3` | 失败 | 返回脱敏错误类，不写入编辑计划 |

成功后应处理：

- `data.task_id`：仅用于本次审计关联；不向 App 暴露。
- `data.audio_url`：临时下载地址，有效期约 1 小时；必须立即受限下载，不得持久化 URL。
- `data.urlexpiretime`：Unix 过期时间。
- `data.sentences[]`：句级文本及时间戳，可作为字幕/旁白对齐的候选信息。

服务端合成结果通常保留 7 天；交付所需的音频、审计产物和最终视频仍按项目 TOS 保留策略处理。

## 5. 与当前 Server 适配器的兼容性待核验项

文档确认了接口路径、Header、`speaker`、`seed-tts-2.0` 和成功状态 `2`。但是在真实调用前必须以一条无业务敏感信息的短文本请求核验以下字段，不得猜测或静默兼容：

| 项目 | 当前适配器行为 | 本次资料中的写法 | 验收要求 |
| --- | --- | --- | --- |
| 查询体任务字段 | `task_id` | `taskid` | 以真实接口响应为准，必要时只改 Server 适配器 |
| 请求节奏字段 | `audio_params.speech_rate` | `additions.speech_rate` | 验证厂商接受的准确位置 |
| 响应句级时间戳 | `startTime` / `endTime` | `begin_time` / `end_time` | 同时兼容或按官方响应改正，并转换为内部毫秒时间线 |
| URL 过期字段 | 未持久化 | `urlexpiretime` | 仅作运行时下载窗口判断，不向 App 输出 |
| 音色 | 由 `VOLC_TTS_SPEAKER` 提供 | 应使用当前账户已开通的音色 | 用控制台实际可用 speaker 做短文本验收 |

首轮真实验收的成功条件：提交成功、轮询至状态 `2`、下载成功、音频可由 FFprobe 识别、时长/采样率/声道有效、SHA-256 和候选审计文件已生成。候选音频默认不自动写入 `DemoEditPlan` 或最终交付视频。

## 6. 计费和权限

- 需要在控制台开通“语音合成大模型/语音合成 2.0”。
- 按字符数使用额度；真实调用前应确认项目有可用字符包或按量计费资格。
- App ID 对应的项目必须拥有该服务权限。
- `VOLC_TTS_SPEAKER` 必须来自该账户的音色列表，而非界面展示名称、实例名或声音复刻任务 ID。

## 7. 实施边界

1. TTS 仅生成展示旁白候选，不能修改真实业务步骤、Selector、验证结果或浏览器证据。
2. 成功的 TTS 调用不等于最终视频通过；双规格 MP4、业务镜头覆盖和结果校验仍须独立通过。
3. 真实调用、下载和采用必须保留脱敏的 provider/model、任务状态、媒体探测、摘要和决策审计。
4. 如果音色、接口字段或服务权限无法确认，应跳过旁白候选并继续生成无旁白版本，而不得阻断真实业务证据链。
