# 火山 OpenSpeech HMAC-SHA256：补充规范结论

> 日期：2026-07-20。本文补充并更新 `volcengine-hmac-sha256-signature-readiness-v1.md`。

## 签名算法信息已完整到可开发程度

最新补充已经解决签名实现中的核心歧义：

- canonical headers 按 `lowercase-name:trimmed-value\n` 拼接，按 ASCII 升序；中间连续空白压缩为一个空格；
- `SignedHeaders` 是参与签名 header 的小写、分号分隔列表，且必须包含 `host` 与 `x-date`；
- URI/query 采用 RFC3986 百分号编码，空格为 `%20`；参数名按 ASCII 升序，同名参数的不同值保留请求原顺序；
- `kSecret` 直接使用原始 Secret Access Key，不加 `VOLC` 前缀；再依次对 date、region、service、`request` 进行 HMAC-SHA256 派生；
- 已提供 IAM 官方测试向量及期望签名，能够作为签名器实现的私有验算基准。

因此，LAS（service=`las`、region=`cn-beijing`）和 TTS（service=`tts`、region=`cn-beijing`）的 HMAC 签名器可以开始实现。

## 安全限制

测试向量中出现的 AK/SK 即使标注为测试用途，也不能复制到 Git、项目文档、日志或测试源码。签名单元测试只能在受控环境临时执行，或改用官方 SDK 的等价测试能力。

## 真实调用前仍需完成的部署前提

签名规范已不再阻塞；以下是环境配置和端到端验收前提：

1. **受控凭据配置。** LAS 需 HMAC AK/SK 与 body 内 `appid/token/cluster`；TTS 需 HMAC AK/SK、`X-Api-App-Id`、`X-Api-Access-Key`、`X-Api-Resource-Id`。所有值只能进入环境变量或密钥库。
2. **LAS 的 HTTPS 音频 URL。** 推荐 TOS 预签名 URL；也可走 `GetResourceUploadUrl`：`https://cloud-vms.volcengineapi.com?Action=GetResourceUploadUrl&Version=2022-01-01`，service=`vms`、region=`cn-north-1`，取得 PUT 上传 URL 后再供 `audio.url` 使用。
3. **VMS 上传响应仍需验真。** 需要记录其真实成功响应字段、PUT 所需 header、上传后供 LAS 读取的 URL 形式与有效期；未得到响应样例前不猜测 schema。
4. **受控端到端验收。** 用无业务敏感信息的短 WAV/MP3 完成 LAS 提交→查询和 TTS 提交→查询→下载→SHA-256/媒体回验，成功后才允许编辑器使用 `real` 模式。

## 最新判断

| 项目 | 状态 |
| --- | --- |
| HMAC 签名器 | 可以开始开发 |
| LAS/TTS 请求适配器 | 可以开始开发 |
| LAS 音频对外 URL | 需选定并配置 TOS 或补齐 VMS 响应 |
| 真实 API 调用 | 等受控凭据与端到端验收，不应提前启用 |
