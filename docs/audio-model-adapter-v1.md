# Server 音频模型适配层 v1

状态：Provider 骨架已实现；模型和配套 LAS/TTS 资料已整理到 [doubao-seed-2-0-mini-260428.md](./doubao-seed-2-0-mini-260428.md)，真实厂商协议仍需用 API Explorer 做请求级确认。

## 目标

音频模型只能生成候选音频或字幕结果，不能直接修改 `DemoEditPlan`、业务步骤或最终视频。Server 保留以下确定性主链：

```text
原始录音/用户文案
    -> Audio Model Provider（候选）
    -> 用户确认
    -> ProcessedAudioAsset / CaptionCue
    -> DemoEditPlan
    -> FFmpeg Renderer
```

## 当前 Provider 操作

统一操作名：

- `transcribe`：音频转字幕候选；
- `synthesize`：文案转配音候选；
- `enhance`：降噪/增强候选；
- `voice_convert`：音色转换候选。

代码位置：[audio_model.go](../backend/internal/media/audio_model.go)。

目前支持 `dry_run`，会记录规范化请求但不访问厂商。`real` 模式已按已确认的 OpenSpeech Token 协议接入 `synthesize`：异步提交、轮询、短时 HTTPS 下载、SHA-256 与 FFprobe 回验；`transcribe`、`enhance`、`voice_convert` 仍明确返回 `protocol_pending`。

配置完成后可在 `backend` 目录运行 `go run ./cmd/ttspreflight`。该命令只合成固定的非敏感短句并生成待试听候选，输出哈希和 FFprobe 元数据；不会把候选写入正式 `DemoEditPlan`。

注意：`transcribe` 使用 LAS 语音识别算子，`synthesize` 使用豆包语音服务，不能把两者都直接发送到通用模型 `doubao-seed-2-0-mini-260428` 的 Responses API。

## 等待厂商文档确认的字段

1. 模型实际支持哪些 operation，不能仅凭模型名推断；
2. 输入是 HTTPS URL、Provider asset ID、Base64 还是 multipart；
3. ASR 是否返回句子级/词级时间戳和置信度；
4. TTS 的 `voice_id`、情感、语速、音调和输出格式；
5. 增强/变声是否是独立模型或独立产品线；
6. 同步/异步任务、轮询、回调、超时和幂等键；
7. 音频时长、文件大小、采样率、声道和并发限制；
8. 数据保存、删除、声音授权和商业使用限制。

## 安全边界

- Provider 请求不得携带 Server 本地路径，只能使用 provider asset reference 或 HTTPS URL；
- 原始录音只读保存，模型输出登记为候选素材；
- 用户确认前不写入正式配音轨；
- 字幕候选不覆盖用户已配置文本；
- 模型输出不代表业务执行成功，也不能替换真实录屏证据；
- `InputSHA256`、请求摘要、模型版本和输出 SHA-256 必须可追溯。

## 已接入的本地成片链路

已确认的配音和字幕现在通过同一份 `DemoEditPlan` 进入本地 Renderer：

```text
narrations[] / caption_cues[]
    -> Worker 校验素材、来源、时长和输出时间范围
    -> FFmpeg 先裁剪并拼接原录屏
    -> 配音 atrim + 延迟对齐 + 音量调整 + 原声压低 + amix
    -> 全局字幕生成 ASS 后烧录
    -> Render Manifest 记录已应用的 narration / caption 操作
```

- `narrations[].source` 只接受 `user_recorded`、`user_uploaded`、`tts_confirmed`；`caption_cues[].source` 只接受 `user_configured`、`asr_confirmed`、`model_confirmed`。
- 配音必须引用已导入、非敏感、非生成候选的音频素材；原始录音和音频素材均保持只读。
- 配音源片段必须覆盖其输出时长；第一版不通过变速来弥补素材长度。
- 当计划含配音或全局字幕而 FFmpeg 不可用、素材缺失或后处理失败时，Renderer 返回 `planned`，绝不把未处理的原录屏标为成功交付。
- 本机尚未发现可执行的 `ffmpeg`。真实混音和字幕烧录验收前，需配置 `CASCADE_FFMPEG_PATH`（以及建议配置 `CASCADE_FFPROBE_PATH`）。

## 本地音频素材接入

编辑器上传入口现支持 `wav`、`mp3`、`m4a`、`aac`、`ogg`、`flac`（与已有的视频格式并列）。音频文件导入后登记为 `kind=narration_audio`、`asset_role=narration_audio`，并带 `presentation_only=true`：

- 不会自动创建视频 shot，不改变业务步骤、视频时间线或 `recording_artifact_id`；
- 可被用户显式加入 `narrations[]`；
- 与视频素材一样进入受控 Server 管理目录，保存 SHA-256 和媒体探测结果；
- 后续浏览器录音、TTS 已确认产物都复用这一素材入口，不另建平行时间线。
