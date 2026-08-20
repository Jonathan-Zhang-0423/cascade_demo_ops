# 跨站自动导演与最终成片 Harness 验收报告（2026-08-20）

## 结论

当前功能分支通过本机工程验收与一次真实素材组合链路验收，可进入扩大样本验证；尚不满足发布级总验收签字条件。

- 工程验收：有条件通过。`guided-demo-v1` 已真实走通事实轨分析、导演计划、H3/Seedance 固定分工、自动质检、FFmpeg 合成、审核包和唯一一次终审。
- 发布级验收：未通过。计划要求的 10 次独立真实模型全链路、五类产品各三次产品内回归、非 Cascade 异步构建授权租户，以及 8/10 独立盲审尚未完成。
- 本报告不把手动浏览器站点烟测、重复使用已付费候选的确定性回放，冒充新的产品内录制或新的模型计费调用。

## 被验收版本

- 分支：`feat/automated-final-film-director`
- 基线：`origin/main@46a714c`
- 验收开始提交：`60c130b`
- H3 已付费任务：`432727588348195`
- Seedance 2.5 已付费任务：`cgt-20260820124122-kjstq`
- Provider 凭据只从本机 `.env` 读取；报告和提交不记录凭据值。

## 自动化链路实测

使用今天真实生成且已人工看过的 H3、Seedance 2.5 素材，通过生产 `guided-demo-v1` 状态机进行无新增费用的组合回放：

- 状态：`completed / completed_after_final_review`
- 事实步骤：6 个，顺序保持
- Provider 调用准入：4 个槽位、4 个持久化 attempt
  - `guided_intro` → H3
  - 两个 `guided_section_divider` → Seedance 2.5
  - `guided_outro` → H3
- 自动质量报告：4/4 `technical_pass=true`、`decision=accept`
- 成片：97.265 秒，1920×1080，H.264/yuv420p，标称 30fps，AAC 48kHz 双声道
- 音频：-15.0 LUFS，True Peak -2.3 dB
- 黑帧累计：200 ms
- 冻结判定：生成候选保持严格绝对门槛；全片使用“事实轨基线冻结量 + 3 秒”上限，避免把正常 UI 阅读停留误判为生成故障
- 审核包：18 个清单角色，包含 final、fact baseline、事实源、H3/Seedance original/normalized、Director plan、evidence digest、最终 EDL、候选质量报告、provider attempts、事件日志和 render manifest
- 唯一终审：审核包 revision/package ID 绑定后 `accept`，作业进入 `completed`

本次组合回放输出位于：

`artifacts/acceptance-20260820/guided-real-output-replay/run-01-1787206385189956900/`

审核 ZIP：

`artifacts/acceptance-20260820/guided-real-output-replay/run-01-1787206385189956900/outputs/finalfilm_real_auto_01_2/review-package-r22.zip`

## 跨站点现场烟测

这些测试使用运行时可见 DOM/ARIA 和画面变化，不使用站点固定 selector。它们证明公开目标可按通用语义操作，但不计为“产品自身完成录制与成片”的全链路次数。

| 原型 | 站点 | 实际结果 |
| --- | --- | --- |
| CRUD/表单 | Playwright TodoMVC | 保持输入原文“跨站自动导演验收任务”，提交后出现新条目和 `1 item left` |
| 数据看板 | Grafana Play | 进入 Eurovision 实际看板，`Full Results` 由未选中变为 selected，并出现结果 region |
| 画布/编辑器 | Excalidraw | 语义选择矩形工具并拖拽；画面摘要从 `2171298e...` 变为 `28e5ebc1...`，撤销由 disabled 变为 enabled |
| 交互应用 | 2048 | 关闭引导后发送键盘方向输入；画面摘要从 `f360553b...` 变为 `abe0c2ff...` |
| 异步构建 | 未执行 | `.env` 和仓库测试清单均未配置非 Cascade 授权租户，按计划不得宣称完成 |

## 验收中发现并修复的问题

1. 生成候选烟测夹具只设置旧 `approved_for_demo` 字段，未声明 normalized/probed profile，完整 verify 会失败。夹具已补齐当前质量合同，并新增 final-output-review-pending 路径。
2. 插入生成镜头后 EDL 仍复用基线 `target_duration_ms`，导致 8.47 秒时间线被声明为 4 秒并回退。现已区分基线目标和组装后实际时间线，手工补丁与自动导演均重新计算。
3. 自动导演候选虽然通过质量门，但 Renderer 只接受逐候选人工审批，导致“只终审一次”路径永远无法渲染。现增加严格的 `final_output_review_pending + final_output + automated_quality_gate_passed` 合同，不伪造逐候选人工批准。
4. FFmpeg 在文件末尾未输出 `freeze_end/freeze_duration` 时，探针会漏计冻结尾段，使基线相对门槛失真。现按媒体总时长闭合末尾区间，并新增单元测试。
5. 全片绝对 3 秒冻结门槛会把真实 UI 的结果阅读停留判成故障。现候选继续使用严格绝对门槛；全片不得比事实轨基线新增超过 3 秒冻结。

## 已通过门禁

- Director Skill registry：5/5 通过仓库校验。
- 站点无关静态门禁：49 个生产核心/Skill 文件通过。
- Go：全量包测试通过；真实 H3 持久化结果经生产 FinalFilm + Node/FFmpeg 回放通过。
- Worker：类型检查、构建、generated-candidate smoke、真实 FFmpeg 82/82 测试通过。
- Web：114/114 测试通过并完成生产构建。
- Review package：目录与 ZIP 可读，manifest 共 18 个文件角色，revision 绑定和终审状态转换通过。

## 未满足的发布级条件

1. 只有 1 次真实 H3 创建、1 次真实 Seedance 创建和 1 次两者组合的生产状态机回放；不是 10 次独立真实模型全链路。
2. 四个公开站点只完成现场语义交互烟测，尚未各执行三次“产品自身理解 → 录制 → 导演 → 成片”的确定性回归。
3. 非 Cascade 异步构建类缺授权租户。
4. 未组织 10 个成片样本的独立盲审，因此没有 8/10 质量签字。
5. 网络中断、Worker 重启和凭据重传已有自动化幂等测试，但尚未在 10 次真实付费批次中做完整故障注入统计。

在以上条件补齐前，正确的对外表述是“工程链路已走通，扩大样本验收中”，不能表述为“五类泛化和 10 次真实成片已发布验收通过”。
