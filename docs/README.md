# Cascade DemoOps 文档索引

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

这些文档描述当前代码状态或具体运行方式。`Desktop App Local Test` 仍是 v1 联调方法；具体命令和已落地接口应结合当前代码验证。

## 历史架构与实现参考

- [Backend MVP Design](./backend-mvp-design.md)
- [Backend Next Steps](./backend-next-steps.md)
- [MVP Project Structure](./mvp-project-structure.md)
- [Sandbox Architecture v1](./sandbox-architecture-v1.md)

这些文件用于理解现有代码为何形成当前结构，不是新开发路线。其中可复用的安全、隔离和工程约束应迁入 v2 实现，但旧端到端流程不能继续扩展。

## 视频、素材与模型接入

- [Server Local Demo Editor MVP](./local-demo-editor-mvp.md)
- [Demo Edit Plan v1](./demo-edit-plan-v1.md)
- [Ark Media Integration](./ark-media-integration.md)
- [Ark Model Parameters](./ark-model-parameters.md)
- [Model Provider Credentials](./model-provider-credentials.md)
- [Legacy AIGC Integration Plan](./legacy-aigc-integration-plan.md)

其中 `DemoEditPlan` 等现有数据模型可作为 v2 本地轻量视频编辑器的迁移基础；标为 Legacy 的方案不应直接恢复为核心架构。
