# 火山方舟 Agent Plan TTS v3（Server 候选链路）

## 适用范围

Server 的旁白候选预检与候选素材生成。TTS 候选不会自动修改 App 执行包、浏览器证据、DemoEditPlan 或最终视频；只有人工审核后才可导入编辑计划。

## 固定接入

- HTTP Chunked/SSE 单向地址：`https://openspeech.bytedance.com/api/v3/plan/tts/unidirectional`
- `X-Api-Key`：必须是火山方舟 Agent Plan 专属 API Key。它与普通方舟 API Key、旧 OpenSpeech Access Key 不同。
- `X-Api-Resource-Id`：`seed-tts-2.0`
- `X-Api-Request-Id`：Server 为每次请求生成的不透明请求 ID。
- 请求格式：`application/json`；响应为 chunked 或 `text/event-stream`。

Server 只使用上面的 Agent Plan 路径，不回退到 `/api/v3/tts/submit`、`/api/v3/tts/query` 异步任务接口，避免误走旧计费链路。

## 环境变量

```dotenv
VOLC_TTS_AGENT_PLAN_API_KEY=
VOLC_TTS_AGENT_PLAN_BASE_URL=https://openspeech.bytedance.com/api/v3/plan/tts/unidirectional
VOLC_TTS_RESOURCE_ID=seed-tts-2.0
VOLC_TTS_SPEAKER=
```

为兼容已有本地配置，`VOLC_TTS_ACCESS_KEY` 也可作为 Agent Plan Key 的别名读取；代码不会因此发送旧版 `X-Api-Access-Key`。真实 Key 不得写入 Git、日志、审计文件或文档。

## 请求要点

请求体包含 `user.uid`、`unique_id` 和 `req_params`。`req_params.speaker` 使用账户音色列表中的 `voice_type`，音频默认 `mp3`、24000 Hz。TTS 2.0 使用 `audio_params.enable_subtitle=true` 获取句级字幕时间轴；`enable_timestamp` 是 TTS 1.0 语义，不作为 TTS 2.0 的主字段。

`req_params.additions` 按厂商协议编码为 JSON 字符串，而不是对象。Server 仅发送有限的受控选项，不把 App 任意字段原样转发。

## 流式响应处理

每行可为 `data: {json}` 或 JSON 行。Server：

1. 识别 `code=0` 的音频块并 Base64 解码后按顺序拼接；
2. 识别 `sentence.words[].startTime/endTime`，生成内部字幕候选时间轴；
3. 以 `code=20000000` 作为成功结束信号；
4. 限制总音频大小，写入受限候选目录；
5. 使用 FFprobe 检查音频流、时长、采样率、声道并计算 SHA-256；
6. 输出脱敏的候选审计记录，不保存临时 URL、Key 或本地路径到可序列化结果。

## 失败与验收

网络、权限、音色、Resource-ID、流解析、Base64、媒体校验任一失败，都只记录脱敏错误并保持 `candidate_only`。TTS 成功不等于最终视频成功；双规格视频、业务镜头覆盖、编辑器质量门禁仍需独立通过。

## 参考资料

- `HTTP Chunked-SSE单向流式-V3.md`
- `WebSocket 单向流式-V3.md`、`WebSocket 双向流式-V3.md`（实时场景参考，不替代离线旁白候选）
- `小模型音色列表.md`
