# 2048 闭环实验 v3：未通过样本分析

## 结论

运行 `experiment_ba6b66e93701861166413540` 不满足交付条件，已保留为失败回归样本。系统未启动 FinalFilm，也未调用 H3、Seedance 或导演视觉模型，因此没有不合格成片或额外 Provider 消费。

## 可确认的业务事实

- 用户原始一句话被原样发送给目标平台。
- 新建实体名称为 `2048 闭环演示版 · 413540`，创建时间晚于本次运行开始时间。
- 首次 Browser Job 为 `job_340aa573d5f0f285c146f4ef`，创建与首次构建提交各发生一次。
- 首轮只通过了产品表面可见性，required 能力未全部通过，因此导演门禁保持关闭。
- 系统在同一实体内提交了一轮简短产品修复，Repair Job 为 `job_883884d77408c55db012fd9a`；没有创建第二个项目。
- 预算归因：目标提交 `1/1`，Browser 视觉调用 `0/5`，Provider 调用 `0/6`，FinalFilm Job `0/1`。

## 失败时间线

1. `2026-08-28T06:23:36Z` 创建运行。
2. `2026-08-28T06:27:58Z` 绑定本次新实体。
3. `2026-08-28T06:55:40Z` 首次 once-effect 得到确认并持久化。
4. `2026-08-28T06:56:32Z` 左右开始同实体修复后的观察。
5. `2026-08-28T07:19:41Z` 左右 Worker 在仍显示忙碌时结束，尚未到达约定的 30 分钟刷新点。
6. App 将 observe-only 节点上的通用 `browser_agent_action_failed` 错投影为 `terminal_failed`。

## 根因

- Worker 的 30 分钟刷新只受 `requireVisualTerminal` 控制；同实体修复使用 `require_repair_idle_transition` 且刻意不消费视觉模型，因而错误地失去刷新与刷新后观察窗口。
- App 仅把专用 observation error 视为可延期，没有结合失败节点的 replay policy 识别 observe-only 上的通用 Worker 中断。
- 恢复素材历史只加载 completed Job，未加载“产品尚未合格但录屏仍有效”的 failed Job。
- 创建 reconciliation revision 前，没有先物化中断轮次素材并把 source result 写入既有 repair history。

## 已实施修复

- 生命周期刷新由“需要视觉终态或需要修复后空闲跃迁”共同启用；刷新本身不消耗视觉预算。
- 通用 action failure 只有在 Interaction Plan 明确标记为 observe-only 时才转为可恢复观察；真实动作失败仍然阻断，避免误重放。
- reconciliation 前先物化失败轮次的录屏与页面证据；恢复捕获历史同时接受 completed 和 failed Job，并过滤过大的 Trace。
- source result 复用现有 repair history 持久化，后续 revision 可以恢复前半段素材，不新增领域实体。

## 重新验收条件

- 本样本不得恢复为成功，也不得跨实验接管旧实体。
- 部署修复后必须以新的授权创建全新 v3 ExperimentRun。
- 新运行仍要求：一个新项目、一次首次提交、零中途代操作、required 条件全过后才允许导演与 Provider 消费。
