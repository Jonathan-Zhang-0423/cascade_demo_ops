# 模型供应商凭据预留

本项目预留以下模型/视频模型供应商 API Key 位置：

| Provider | API Key env | Base URL env | Default model env |
| --- | --- | --- | --- |
| GLM | `GLM_API_KEY` | `GLM_BASE_URL` | `GLM_MODEL` |
| Kimi | `KIMI_API_KEY` | `KIMI_BASE_URL` | `KIMI_MODEL` |
| MiniMax | `MINIMAX_API_KEY` | `MINIMAX_BASE_URL` | `MINIMAX_MODEL` |
| MiniMax-H3 视频 Sidecar | `MINIMAX_H3_API_KEY` | `MINIMAX_H3_BASE_URL` | Server 固定 `MiniMax-H3` |
| Seedance | `SEEDANCE_API_KEY` | `SEEDANCE_BASE_URL` | `SEEDANCE_MODEL` |
| 豆包 / Ark | `DOUBAO_API_KEY`, `ARK_API_KEY` | `DOUBAO_BASE_URL` | `DOUBAO_MODEL` |
| DeepSeek | `DEEPSEEK_API_KEY` | `DEEPSEEK_BASE_URL` | `DEEPSEEK_MODEL` |

## 默认 Base URL

Runtime 会预设公开官方端点，仍可通过对应 `*_BASE_URL` 覆盖：

| Provider | Default base URL | 说明 |
| --- | --- | --- |
| GLM | `https://open.bigmodel.cn/api/paas/v4` | 智谱 BigModel OpenAI 兼容端点 |
| Kimi | `https://api.moonshot.cn/v1` | Moonshot/Kimi OpenAI 兼容端点 |
| MiniMax | `https://api.minimaxi.com/v1` | MiniMax OpenAI SDK 文档端点 |
| MiniMax-H3 视频 Sidecar | `https://api.minimaxi.com` | H3 V2 视频生成端点；当前未注册到正式路由 |
| Seedance | `https://ark.cn-beijing.volces.com/api/v3` | 火山方舟 Ark 端点，默认用于 Seedance 2.0 视频能力 |
| 豆包 / Ark | `https://ark.cn-beijing.volces.com/api/v3` | 与 Seedance 共用 Ark 网关 |
| DeepSeek | `https://api.deepseek.com` | DeepSeek OpenAI 兼容端点 |

Seedance 的 key 读取顺序是：`SEEDANCE_API_KEY` -> `DOUBAO_API_KEY` -> `ARK_API_KEY`。也就是说，如果视频操作阶段使用豆包/火山方舟账号，填 `DOUBAO_API_KEY` 或 `ARK_API_KEY` 即可，不需要额外复制一份到 `SEEDANCE_API_KEY`。

MiniMax-H3 视频 Sidecar 使用独立模式开关：

```text
CASCADE_MINIMAX_H3_MODE=disabled|dry_run|real
```

默认值为 `disabled`。Sidecar 不读取 `MINIMAX_API_KEY`、`CASCADE_ARK_MEDIA_MODE`、Seedance 或豆包密钥；只认专用 `MINIMAX_H3_API_KEY`。当前 Sidecar 尚未接入执行、Director、comparison 或 fallback 路由，因此设置这些变量也不会影响现有 Server 验收，除非后续代码显式构造并调用 Sidecar。

官方参考：

- Kimi / Moonshot: https://platform.moonshot.cn/docs/guide/start-using-kimi-api
- GLM / 智谱 BigModel: https://docs.bigmodel.cn/cn/guide/start/quick-start
- MiniMax OpenAI SDK: https://platform.minimaxi.com/docs/api-reference/text-openai-api.md
- 火山方舟 Ark API: https://www.volcengine.com/docs/82379/1298459
- DeepSeek API: https://api-docs.deepseek.com/

## 安全约束

- `.env.example` 只放空白占位，禁止提交真实 API Key。
- `AppRuntimeConfig` 可以从环境变量读取 key，但 `RuntimeConfigView` 只向桌面 UI 暴露 `configured` 状态和 env 名，不返回 key、base URL 或本地路径。
- 数据库和执行包只应保存 `secret_ref` 或 provider 名称，不保存原始 key。
- 后续 provider adapter 应从 runtime config 或 vault resolver 取密钥，不从用户输入、脚本文档或 exchange package 读取明文 key。

## 当前状态

本阶段已经接入 provider-neutral LLM adapter。`CASCADE_LLM_MODE=real` 时会真实调用模型，失败直接返回脱敏错误；`auto` 会在模型不可用时降级 deterministic；`deterministic` 完全不调用外部模型。

可通过桌面设置页的“真实模型诊断”或 Dev Bridge 接口检查当前配置：

```text
POST /v1/desktop/model-diagnostics
```

诊断结果只包含 provider、task、model、base URL 的 host/path、HTTP 状态、脱敏错误和延迟，不返回 API Key、Authorization 或 prompt。

## 默认任务路由

| Task | Provider | Model | Override env |
| --- | --- | --- | --- |
| 计划/执行方案生成 | Kimi | `kimi-k2.7-code` | `CASCADE_PLANNING_PROVIDER`, `CASCADE_PLANNING_MODEL` |
| 代码阅读/结构摘要 | GLM | `glm-5.2` | `CASCADE_CODE_READING_PROVIDER`, `CASCADE_CODE_READING_MODEL` |
| 多模态需求/页面理解 | Minimax | `minimax-m3` | `CASCADE_MULTIMODAL_PROVIDER`, `CASCADE_MULTIMODAL_MODEL` |
| 视频操作/视频生成能力 | Seedance | `doubao-seedance-2-0-260128` | `CASCADE_VIDEO_PROVIDER`, `CASCADE_VIDEO_MODEL` |

DeepSeek 的 key 位已预留，当前不作为默认任务路由；后续可用于推理增强、备用规划或成本路由。
