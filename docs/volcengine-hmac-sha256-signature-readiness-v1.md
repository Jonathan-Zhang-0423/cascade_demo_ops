# 火山 OpenSpeech HMAC-SHA256 签名：接入就绪度核对

> 整理日期：2026-07-20。
>
> 依据：提供的 HMAC-SHA256 流程、Python/Java 示例，以及本项目已登记的 OpenSpeech LAS AUC 与 TTS v3 请求示例。

## 结论

签名资料已足够确定总体流程：规范请求 → 待签名字符串 → 日期/区域/服务派生 HMAC 密钥 → `Authorization`。但它 **不足以安全实现并验证生产签名**。建议优先采用官方 Go SDK 或索取同一接口的官方 Go 签名示例；在没有通过测试向量前，不开启 `real` 模式。

当前可以确定的 scope：

| 接口 | region | service |
| --- | --- | --- |
| LAS `SubmitAucTask` / `QueryAucResult` | `cn-beijing` | `las` |
| TTS `SubmitAsyncTtsTask` / `QueryAsyncTtsResult` | `cn-beijing` | `tts` |

## 已确认的签名流程

```text
canonical request
  -> SHA-256
  -> string to sign: algorithm + X-Date + credential scope + canonical-request hash
  -> HMAC 日期 -> region -> service -> request
  -> Authorization: Credential + SignedHeaders + Signature
```

请求 body 的 SHA-256 必须针对实际发送的 UTF-8 字节计算；因此 JSON 序列化后不得再格式化、重排或改写。

## 现有资料的关键缺口

1. **Canonical headers 的完整格式缺失。**
   提供的示例只列出了 `SignedHeaders` 名称，未明确规范请求是否、以及如何包含：`lowercase-header-name:normalized-value\n`。没有 header 值就无法重现服务器验签的 canonical request。

2. **URI 和 query 的 RFC3986 编码规则缺失。**
   需明确路径规范化、空 query、重复 query key、空值、空格、中文、`+`、`%2F` 和排序规则。LAS/TTS 的 `Action`、`Version` 在 query 内，编码错误即签名错误。

3. **密钥派生细节未验真。**
   需确认首轮 HMAC key 是原始 Secret、`VOLC` 前缀 Secret，还是其他形式；还需确认 HMAC 的 key/data 参数顺序。通用示例不能替代当前 OpenSpeech 服务的规则。

4. **请求头归一化规则缺失。**
   需明确小写化、前后空白压缩、重复 header 合并、多值 header 与 `Host` 端口的处理方式。TTS 示例含 `X-Api-Request-Id`，但其 `SignedHeaders` 未列它；这是否是预期行为必须以官方规则为准。

5. **示例本身不能直接运行。**
   Python/Java 片段存在未定义变量或命名不一致（例如 access key ID、query/canonical URI、日期变量）。它适合说明流程，不能当作可复制生产代码。

6. **缺少可验证测试向量。**
   必须有一组官方或 API Explorer 固定测试向量：固定的非生产 AK/SK、method、path、query、headers、body、时间和期望 Authorization。仅有 `<redacted>` 无法验证 Server 的实现是否与官方一致。

7. **LAS 的 HTTPS 音频交付仍未解决。**
   LAS 要求 `audio.url`。Server 的本地受控素材必须先得到符合厂商限制的 HTTPS URL（或官方确认的替代上传/资产引用方式），不能传 `file://` 或 Windows 路径。

## 实现前的最小补充项

请按优先级获取以下任一组合：

1. 官方 Go SDK 的仓库地址、Go module path、推荐版本，以及对 OpenSpeech LAS AUC 和 TTS v3 的完整调用示例；**或**
2. OpenSpeech 专用 HMAC 规范，明确本文件“关键缺口”中的全部规则；并提供一个可用于自动化测试的测试向量；
3. LAS 音频 URL 的获取方案：对象存储预签名 URL 的域名、最短有效期、重定向与下载范围限制；
4. 凭据映射说明：LAS body 中 `appid/token/cluster` 与 HMAC 的 AK/SK 如何获取和轮换；TTS 的 `X-Api-App-Id/X-Api-Access-Key` 与 HMAC AK/SK 的关系。

## 可直接发给官方的提示词

```text
我们已经掌握 OpenSpeech LAS AUC 与 TTS v3 的 endpoint、query 和请求字段，现需在 Go Server 端实现 HMAC-SHA256。

请提供以下其中之一：
1) 官方 Go SDK 的 module path、版本和可运行调用示例；或
2) OpenSpeech 专用签名规范和可验证测试向量。

测试向量请使用非生产凭据，并给出固定的 AK、SK、X-Date、method、path、query、所有 header、UTF-8 body、canonical request、string-to-sign 和最终 Authorization。若不能提供 AK/SK，请提供一段官方 Go 程序及其期望 canonical request / signature。

还请明确：
- canonical headers 是否包含“lowercase-key:normalized-value\\n”，每个 header 的归一化和排序规则；
- URI/query 的 RFC3986 编码、重复 key、空值、中文与空格规则；
- 首轮 HMAC key 是否需加 VOLC 前缀；
- TTS 的 X-Api-Request-Id 为何不在示例 SignedHeaders 中；
- LAS 的 appid/token/cluster 与 HMAC AK/SK 的获取及轮换关系；
- LAS audio.url 可接受的预签名 URL、有效期、域名、重定向和大小限制。
```
