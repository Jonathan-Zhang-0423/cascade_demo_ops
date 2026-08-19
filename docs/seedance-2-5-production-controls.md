# Seedance 2.5 生产回调、限流与预算控制

状态：**已完成官方资料对齐；尚未替代本项目的真实调用验收。**  
适用模型：`doubao-seedance-2-5-260628`。  
资料日期：2026-08-19。

本文件只沉淀可执行的 Server 规则；不记录账号 ID、余额、API Key、Endpoint ID、完整预签名 URL 或任务 ID。

## 1. 回调协议

创建任务时可选传入 `callback_url`。方舟以 HTTP `POST`、`Content-Type: application/json; charset=utf-8` 推送，其 JSON 结构与查询任务接口返回体一致。

| 状态 | Server 需处理的字段 |
| --- | --- |
| `queued` / `running` | `id`、`model`、`status`、`created_at`、`updated_at`、`service_tier`、`execution_expires_after` |
| `succeeded` | 上述字段，加 `content.video_url`、可选 `last_frame_url`、`usage`、输出媒体参数和 `seed` |
| `failed` / `expired` | 上述基础字段，加脱敏 `error.code` 与 `error.message` |
| `cancelled` | **不作为回调前提**；它是合法查询状态，但官方未将其列入 callback 推送状态清单 |

回调没有厂商签名 Header、Bearer Token 或固定来源 IP 段。不得把回调 IP 当作身份依据，也不得信任没有关联到已创建任务的任意回调内容。

### 1.1 接收端安全与性能要求

1. 使用公开可访问的 HTTPS 域名和有效 CA 证书，TLS 1.2 以上；不能使用 VPC 私网地址、自签名证书、mTLS 或依赖自定义 Header 的入口。
2. 每个任务生成一个高熵一次性回调 secret，写入 callback URL 的受控查询参数；网关、访问日志和应用日志必须脱敏/不记录 query string。secret 与任务 ID 绑定，终态完成后失效。
3. 校验 JSON、任务 ID、允许的状态值、模型前缀和 `content.video_url` 的厂商输出域名白名单；禁止直接访问回调中任意未校验 URL，防止 SSRF。
4. HTTP 请求内只完成 secret 校验和耐久化入队，1 秒内返回 2xx；下载、转存、媒体探测、转码和通知全部异步执行。
5. 对 `succeeded` / `failed`，5 秒内未收到 2xx 时厂商最多投递 3 次；重试间隔未公开，不能在代码中假定固定间隔。`queued`、`running`、`expired` 的失败重试语义未单独公开。

### 1.2 幂等、乱序与兜底

- 主键为 `task_id`，幂等键为 `(task_id, status, updated_at)`。
- 仅以更大的 `updated_at` 覆盖本地状态；缺省字段不得覆盖已经持久化的成功字段。
- 一旦进入 `succeeded`、`failed`、`expired` 回调终态，旧回调只记录接收审计并返回 2xx。`cancelled` 不依赖回调，见下方取消规则。
- 回调只是最佳努力通知。对 `queued`/`running` 滞留任务定期 GET 查询对账；任务记录通常保留 7 天，成功产物 URL 约 24 小时有效，视频 URL 下载上限约 100 次。

### 1.3 取消规则

厂商明确承诺会推送的 callback 状态只有 `queued`、`running`、`succeeded`、`failed`、`expired`。`cancelled` 虽是查询接口的合法状态，但官方未承诺会回调，也未给出 callback JSON 示例。

- 仅 `queued` 的任务可由 `DELETE /contents/generations/tasks/{task_id}` 取消；`running` 任务不可取消。
- Server 发起 DELETE 并收到成功响应后，立即将本地任务标记为“取消请求成功/待对账”，然后 GET 查询确认 `cancelled`；不能等待 callback。
- `cancelled` 记录约 24 小时后自动清理。该状态的内容字段形态未由官方 callback 文档保证，不能以推断 JSON 编写严格解析逻辑。
- 回调接收器仍可容忍 `cancelled` 作为未知但合法的防御性状态并入审计；它不得驱动核心状态机转换，除非同一任务的 DELETE/GET 证据已存在。

## 2. 限流与错误处置

当前账号资料显示企业档默认模型上限为 RPM 600、并发 10。该上限按“主账号 + 模型版本”共享，所有绑定同一 Seedance 2.5 模型的 Endpoint 共用总池，不能通过增加 Endpoint 绕过。

Endpoint 的 `ContentGeneration.ConcurrentRequests` / `CreateTaskRpm` 是该入口的子配额封顶，不是从账号总池预分配的硬配额。多个 Endpoint 的配置值之和可以超过账号级上限；单个请求实际能否运行，同时受 Endpoint 封顶和账号模型剩余总池约束，可理解为两层 `min(endpoint_limit, account_model_remaining)`。因此：

- 需要业务隔离时，建议各 Endpoint 配置值之和等于或低于账号总池；
- 需要峰值共享时，可以让配置值之和超过账号总池，但必须接受运行时争抢、排队和 429；
- 无论如何，不能把 Endpoint 配置调大当成扩容方式；超过账号级 10 并发/600 RPM 仍需工单提额。

| 类别 | 例子 | Server 行为 |
| --- | --- | --- |
| 瞬时可恢复 | `RateLimitExceeded`、`ServerOverloaded`、`RequestBurstTooFast`、`InternalServiceError` | 指数退避加抖动；按错误类别限制 3–5 次；记录每次尝试 |
| 并发/排队饱和 | `InflightBatchsizeExceeded`、部分 `QuotaExceeded` | 暂停新建任务，等现有任务终态或 30–60 秒后有限重试；不可忙等 |
| 费用熔断 | `SetLimitExceeded` | 立即停止模型提交、报警；禁止自动重试，等待人工恢复限额 |
| 参数、审核、鉴权 | `InvalidParameter.*`、`SensitiveContentDetected.*`、`AuthenticationError`、`AccessDenied` | 不重试；保存脱敏原因并标为不可执行或需人工处理 |

推荐初始退避：RPM 2–60 秒、服务过载/5xx 5–60 秒、并发已满 30–120 秒。每项延迟必须带随机抖动，防止多个 Worker 同时重试。

## 3. Endpoint 与预算治理

直调 Model ID 可用于开发，但生产建议按环境/业务线使用独立 Endpoint 和 API Key，获得限流、监控、费用分拆和 Key 隔离。

建议最小划分：

| 环境 | 并发建议 | 创建 RPM 建议 | 用途 |
| --- | ---: | ---: | --- |
| production | 8 | 60 | 用户候选任务 |
| staging | 2 | 10 | 集成和回归 |

上表是强隔离建议，合计等于账号总池，并非厂商强制条件。若采用峰值共享，可提高单个 Endpoint 的封顶，但必须由 Server 全局调度器继续把实际提交控制在账号级总池以内。Endpoint 限流是流量防护，不是金额封顶。

必须叠加三层治理：

1. Endpoint 的 `RateLimit.ContentGeneration.ConcurrentRequests` 与 `CreateTaskRpm`，控制突刺。
2. 控制台的模型“推理限额”，作为达到即停止新请求的灾难熔断；它不是自动日/月重置预算。
3. 费用中心按 Endpoint 的预算告警（建议 80%/100%），以及业务侧按任务预估/实际 `usage.completion_tokens` 实现的日预算熔断。

价格、折扣和活动有效期以调用当日控制台/官方价目为准。任务完成后，以响应 `usage.completion_tokens` 和当日价目版本记录实际成本；调用前成本只用于拦截，不能作为结算事实。

### 3.1 限额读取接口

账号级模型总池可通过火山公共 OpenAPI 管控面读取，不是方舟推理数据面：

```text
POST https://open.volcengineapi.com/?Action=ListModelRateLimit&Version=2024-01-01
Service=ark
Region=cn-beijing
Body: {"FoundationModelNames":["doubao-seedance-2-5-260628"]}
```

该接口必须使用火山 V4 HMAC-SHA256 签名的 AK/SK 或 STS 凭证；Seedance 的 `ARK_API_KEY` 不能调用它。最小读取权限为 `ark:ListModelRateLimit`，生产建议使用只读 IAM 子用户/角色，不在 Server 配置中放 Root 凭证。

响应中的 `Items[].ContentGenerationRateLimit` 提供模型级 `ConcurrentRequests`、`CreateTaskRpm`、查询/删除任务 RPM 和 4K 变体；`DefaultRateLimit` / `CurrentRateLimit` 提供通用模型限流。该接口不返回各 Endpoint 的子配额。

各 Endpoint 的 `ContentGeneration` 配置需另调 `ListEndpoints`（同为 `ark`、`2024-01-01` 管控面接口）读取。Server 的限流快照应同时保存：读取时间、模型名、账号级上限、Endpoint 子配额和配置版本，不保存签名头、AK/SK 或完整请求。

建议每 5–15 分钟刷新一次限额快照，变更或读取失败时写入告警；不要秒级轮询，也不要把“上限”误当成“当前使用量”。在途任务数和实际 RPM 仍需由 Server 自己的任务/指标系统统计。

## 4. 对项目素材与音频的影响

- 候选输入使用 `cn-beijing` 私有 TOS 的 `ivolces.com` HTTPS GET 预签名 URL；产物应立即下载、探测、哈希并转存，禁止长期依赖厂商临时 URL。
- 默认 `generate_audio=false`。若测试原生音画，结果是非权威候选；下载后以 FFprobe 实测声道、采样率和码率。
- 独立豆包 TTS 是可审计旁白路径：无声候选视频与 TTS 成品由本地后期混音，不能假定 Seedance 会原样透传 `reference_audio`。

## 5. 未由公开资料完全确定的点

1. callback 的具体重试间隔未公开；实现必须依赖幂等与 GET 对账，不能假定固定退避。

## 6. 官方资料

- 创建视频生成任务：`https://docs.volcengine.com/docs/82379/1520757`
- 查询视频生成任务：`https://docs.volcengine.com/docs/82379/1521309`
- 使用 Webhook 通知：`https://docs.volcengine.com/docs/82379/2298881`
- Seedance 2.5 教程：`https://docs.volcengine.com/docs/82379/2607688`
- 推理限额：`https://docs.volcengine.com/docs/82379/1159200`
- UpdateEndpoint：`https://docs.volcengine.com/docs/82379/1262814`
- ListModelRateLimit：`https://docs.volcengine.com/docs/82379/2612140`
- 模型价格：`https://docs.volcengine.com/docs/82379/1544106`
