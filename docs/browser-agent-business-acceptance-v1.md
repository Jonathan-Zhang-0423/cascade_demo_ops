# Server Browser Agent 受控业务执行验收 v1

## 目的

严格验收包验证协议防线：错误的语义目标、缺失定位器和失败的结果验证必须被拦截。

本验收包验证另一件事：在不触碰 App 数据、用户网站和生产凭据的前提下，Server 的新路径能否完整执行一段有业务含义的流程，并把结果交给视频编辑器。

## 受控流程

Server 在运行时临时启动一个只存在于本机内存的“项目构建”页面，并生成一个完整的 `browser-agent-outline-v1` 执行包：

1. 打开项目工作台（`session_setup`）。
2. 输入已审批的项目名称（`business_input`）。
3. 选择构建模式（`mode_selection`）。
4. 提交构建（`business_submit`）。
5. 从真实页面证据验证构建结果（`final_observe`）。

每个 Stage 都同时出现在 Graph、Plan JSON、Stage Approval Plan 和 Script Outline 中；所有动作都标为非破坏性，并且有必填的浏览器结果验证。

## 通过条件

- Exchange Intake、Runtime Router、Policy Guard、Outline Runner 和 Outcome Verifier 全链路完成；
- 五个 Stage 各有一个通过的必填验证报告；
- 结果包包含原始录屏、Trace、阶段事件、截图和最终演示视频；
- 成功结果自动创建本地 Editor Session，使素材进入“待编辑素材”；
- 页面只显示验收报告和本地证据路径，不暴露用户数据或凭据。

## 运行入口

平台“执行包”页面提供“受控业务执行验收”按钮：

- `POST /v1/desktop/browser-agent-business-acceptance/run`
- `GET /v1/desktop/browser-agent-business-acceptance`

报告写入：`artifacts/browser-agent-business-acceptance/latest/acceptance-report.json`。

## 边界

这不是客户产品的验收，也不能替代 App 侧正式执行包联调。它只证明 Server 新路径在受控业务场景中具备端到端执行与素材交接能力。真实产品验收仍必须使用 App 已审批、符合协议的 Outline 包，并在隔离环境中执行。
