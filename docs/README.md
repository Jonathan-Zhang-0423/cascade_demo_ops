# Cascade DemoOps 文档索引

- [Browser Agent 直连部署手册](browser-agent-direct-deployment.md)：App 正式执行主链路、Ubuntu 网关、专属端口、Worker 对接与真实验收门禁。

## 模块化工作流与通用执行端口

- [当前全工作链路代码审计（2026-08-21）](./architecture/current-system-workflow-audit-2026-08-21.md)
- [安全数据与门禁治理规范 v1](./architecture/safety-data-governance-v1.md)
- [模块化工作流重构方案 v1](./architecture/modular-workflow-refactor-v1.md)
- [通用执行端口协议 v1](./protocols/universal-execution-port-v1.md)

以上文档共同定义下一阶段的模块化迁移基线：以传输无关 Lifecycle Kernel 统一 Go 进程内、Browser Worker JSON-RPC 和远端 HTTP/SSE 运行语义；现有 v1 协议通过 Adapter 保持兼容。机器契约和正反例位于 `contracts/execution-port/v1/`，使用 `pnpm validate:execution-port` 校验。

## Server 侧权威架构

- [Server 侧 Browser Agent、执行与本地视频编辑系统架构 v2](./server-browser-agent-execution-editor-architecture-v2.md)

该文档是 Server 侧目标架构基线。它定义了：

- 外部业务动作证据与 Server Browser Agent 页面证据的融合；
- 冲突检查、分项置信度、硬门槛和执行草案编译；
- 草案 Hash 授权、Playwright 确定性执行和 Outcome Verifier；
- 受 Repair Policy 约束的 Runtime Repair Patch Overlay；
- 录屏、素材目录、本地轻量视频编辑器和 FFmpeg 渲染交付。

v2 是目标架构，部分模块仍在迁移或尚未实现，不能仅凭文档假定代码已经具备对应能力。对已经落地的交换包和 Browser Agent Outline 字段，当前协议文档与代码优先；架构迁移必须通过新 schema/version 完成，不能用设计文档覆盖现行契约。

## 当前交换与执行包协议

以下规范对应当前代码、fixtures 和联调接口：

- [Client to Cloud Exchange Protocol](./client-cloud-exchange-protocol.md)
- [执行脚本文档协议 v1](./execution-script-document-v1.md)
- [可执行录制脚本包协议 v1](./executable-recording-script-bundle-v1.md)

其中 `browser-agent-outline-v1` 是当前主路径，`playwright-restricted-sandbox` 是 Legacy 兼容路径。协议演进遵循两条规则：

1. 已落地接口和数据结构继续按当前 version 校验。
2. 不兼容变更必须增加 schema/version，并提供迁移和兼容测试。

## 当前实现状态、部署与验证

- [Go Backend Rewrite Status](./go-backend-rewrite-status.md)
- [Local Validation](./local-validation.md)
- [Development Exchange HTTP Test Channel](./dev-exchange-http-test-channel.md)
- [Desktop App Local Test](./desktop-app-local-test.md)
- [Desktop Packaging](./desktop-packaging.md)
- [App 端封装打包路线图 v1](./desktop-packaging-roadmap-v1.md)

这些文档描述当前代码状态或具体运行方式。`Desktop App Local Test` 仍是 v1 联调方法；具体命令和已落地接口应结合当前代码验证。

## 历史架构与实现参考

- [Backend MVP Design](./backend-mvp-design.md)
- [Backend Next Steps](./backend-next-steps.md)
- [MVP Project Structure](./mvp-project-structure.md)
- [Sandbox Architecture v1](./sandbox-architecture-v1.md)

这些文件用于理解现有代码为何形成当前结构，不是新开发路线。其中可复用的安全、隔离和工程约束应迁入 v2 实现，但旧端到端流程不能继续扩展。

## 视频、素材与模型接入

- [App ↔ Server 生成展示视频能力协议](./app-server-generated-video-capability-protocol.md)
- [Seedance 2.5 FinalFilm 接入说明](./seedance-2-5-video-generation.md)
- [Server 生成视频统一候选产物内部协议](./generated-video-candidate-internal-protocol.md)
- [Server Local Demo Editor MVP](./local-demo-editor-mvp.md)
- [Demo Edit Plan v1](./demo-edit-plan-v1.md)
- [Ark Media Integration](./ark-media-integration.md)
- [Ark Model Parameters](./ark-model-parameters.md)
- [MiniMax-H3 视频生成接入说明](./minimax-h3-video-generation.md)
- [Model Provider Credentials](./model-provider-credentials.md)
- [Legacy AIGC Integration Plan](./legacy-aigc-integration-plan.md)

其中 `DemoEditPlan` 等现有数据模型可作为 v2 本地轻量视频编辑器的迁移基础；标为 Legacy 的方案不应直接恢复为核心架构。

受约束分镜设计、规划模型辅助，以及 Seedance 2.5/MiniMax-H3 分镜头实现计划，统一收录在权威架构文档的[阶段四：受约束分镜设计与多 Provider 分镜头实现](./server-browser-agent-execution-editor-architecture-v2.md#阶段四受约束分镜设计与多-provider-分镜头实现)。当前 FinalFilm 已注册两类 Provider，但真实调用仍需要各自的显式授权与运行时门禁。
