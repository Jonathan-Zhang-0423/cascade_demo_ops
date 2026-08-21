# 2048 全链路组合实验 v2

这个目录是实验 fixture，不是通用内核。产品名、目标站点和冻结验收文案只能留在这里、测试数据和运行报告中。

## 启动边界

1. 先运行仓库根目录的 `pnpm verify`；失败时不得开启真实实验。
2. 用 `CASCADE_EXPERIMENT_AUTO_RUN=1` 启动当前分支的 DemoOps App。
3. 将登录信息保存到本机凭据仓，只把 `credential://demo/<ref>` 交给实验 API。
4. 执行 `run.ps1 -CredentialRef credential://demo/<ref>`。脚本只调用 DemoOps 实验端口，不操作目标站点。
5. 主跑与恢复跑都完成后，状态进入 `waiting_input / awaiting_final_review`。此时进行唯一一次人工终审。

恢复跑的远端 package 带有内部恢复注入元数据。Worker 在第一个 durable once-effect `stage_completed` 后释放任务；下一 Worker 必须读取原事件日志、发出 `stage_resumed` 并跳过该动作。外部 Direct task ID 会在上传回执后立即持久化，App 重启也只能续接同一任务。

任何 Codex 代操作、目标站点 API 直调或中途人工浏览器修补都必须通过归因接口记录，并使该实验失效。
