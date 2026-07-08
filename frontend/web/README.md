# Cascade 桌面端 Web UI

这是由桌面壳加载的 React + TypeScript 操作工作台。界面面向中国客户，默认使用中文文案，并保持本地优先的 DemoOps 审批流程。

MVP 工作流：

```text
项目基础设置
输入材料复核
本地产品理解
执行方案审批
加密执行包审批
云端录制状态
视频和步骤文档验收
```

当前实现使用 `src/bridge.ts` 中的类型化 mock bridge。后续接入 Wails 时可以替换桥接实现，而不需要改动 React 视图模型。

常用命令：

```text
pnpm --filter @cascade/web dev
pnpm --filter @cascade/web build
pnpm --filter @cascade/web test
```
