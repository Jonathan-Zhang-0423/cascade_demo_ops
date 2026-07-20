# OpenSpeech LAS / 豆包 TTS：推荐 Token 鉴权接入结论

> 日期：2026-07-20。依据最新确认信息整理。
>
> 本文优先级高于此前的 HMAC 实施草案：第一版优先使用 Token 鉴权；HMAC-SHA256 仅作为后续可选鉴权模式保留。

## 1. 鉴权决策

| 服务 | 第一版模式 | 不需要的工作 | 后备模式 |
| --- | --- | --- | --- |
| LAS 异步识别 | Token | 不手写 HMAC canonical request/Authorization | 语音服务自定义 Signature |
| 豆包 TTS 异步合成 | Token | 不手写 HMAC canonical request/Authorization | 语音服务自定义 Signature |

Token 模式的目标是减少接入复杂度。代码中仍保留 provider-neutral 适配层，不让鉴权细节进入 `DemoEditPlan`、浏览器或业务步骤协议。

## 2. 最新确认的正式接口

```text
LAS submit: POST https://openspeech.bytedance.com/api/v1/auc/submit?Action=SubmitAucTask&Version=2022-01-01
LAS query:  POST https://openspeech.bytedance.com/api/v1/auc/query?Action=QueryAucResult&Version=2022-01-01

TTS submit: POST https://openspeech.bytedance.com/api/v3/tts/submit?Action=SubmitAsyncTtsTask&Version=2022-01-01
TTS query:  POST https://openspeech.bytedance.com/api/v3/tts/query?Action=QueryAsyncTtsResult&Version=2022-01-01
```

语音服务地域统一为 `cn-beijing`；语音服务标识为 `speech`。这与此前收集的通用火山 HMAC `las` / `tts` scope 不同，后者只在明确选择 Signature 鉴权时再以官方语音服务规则为准。

## 3. Token 模式所需配置

| 配置 | 用于 | 说明 |
| --- | --- | --- |
| `VOLC_LAS_APP_ID` | LAS body `app.appid` | 控制台应用标识 |
| `VOLC_LAS_TOKEN` | LAS body `app.token` / Token 鉴权 | 不写日志、不下发浏览器 |
| `VOLC_LAS_CLUSTER` | LAS body `app.cluster` | 控制台集群 ID |
| `VOLC_TTS_APP_ID` | TTS `X-Api-App-Id` | 控制台应用 ID |
| `VOLC_TTS_ACCESS_KEY` | TTS `X-Api-Access-Key` | 控制台访问密钥 |
| `VOLC_TTS_RESOURCE_ID` | TTS `X-Api-Resource-Id` | 首选 `seed-tts-2.0` |

Token 模式下不需要为 LAS/TTS 预先填写通用 HMAC 的 AK/SK。若选 TOS 预签名 URL 托管 LAS 输入音频，TOS 自身仍需要独立的受控访问凭据、bucket、endpoint 和 region。

## 4. 请求字段与时间单位

LAS：提交 body 必含 `app.appid`、`app.token`、`app.cluster`、`user.uid`、`audio.url`；查询 body 必含 `appid`、`token`、`cluster`、`id`。转写 `utterances[].start_time/end_time` 为毫秒。

TTS：提交必含 App ID、Access Key、Resource ID、文本、speaker 与 audio parameters；查询以 `task_id` 获取结果。`task_status=1/2/3` 分别为排队/完成/失败。TTS 句词时间 `startTime/endTime` 为秒级浮点，进入内部时间线前乘以 1000。

字段名以 API Explorer 导出的 **JSON 实例** 为唯一准则。此前资料中同时出现过 `req_params`/`reqparams`、`audio_params`/`audioparams`、`unique_id`/`uniqueid` 等文字写法；实现时不能据文字说明擅自改名。

## 5. LAS 音频 URL

LAS 不接收本地路径。推荐链路：

```text
受控本地音频
  -> TOS 私有对象
  -> 预签名 HTTPS GET URL（最长按已确认限制设置）
  -> LAS audio.url
```

备选链路是语音服务 `GetResourceUploadUrl`：

```text
https://cloud-vms.volcengineapi.com?Action=GetResourceUploadUrl&Version=2022-01-01
service=vms, region=cn-north-1
body: {"FileName":"unique-audio.mp3"}
```

它返回短期 PUT 上传地址。接入前仍需一组脱敏成功响应，确认上传所需 headers、上传完成后可供 LAS 读取的 URL 字段与有效期；在此之前不猜测响应结构。

## 6. 真实调用前仅剩两项确认

1. 用 API Explorer 的 **Token 鉴权模式** 各导出一条 LAS 和 TTS 请求：保留 header 名与 body 字段，所有值脱敏。重点确认：
   - LAS `Authorization: Bearer; <token>` 中的 token 是否与 body `app.token` 为同一值；
   - TTS Token 模式是否还需要 `Authorization`；若需要，准确格式是什么。
2. 确定 LAS 音频 URL 方案：TOS（推荐）或 VMS 上传；若选 VMS，补充成功响应。

完成这两项后即可实现 Token 模式真实适配器；真实开关仍应在无敏感测试音频端到端验收通过后开启。
