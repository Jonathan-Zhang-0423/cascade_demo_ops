# 验证链路本周交接：需孟洋/一凯确认或决策的事项

日期：2026-08-10
提出人：明达（验证链路）
分支：`feat/validation-agent-completeness-week2`（详细工作见同批次 `validation-agent-week-summary-2026-08-10.md`）

本文件只列**需要你们看一眼、做决定或给答复**的事项，不重复完整交付细节。

## 一、需一凯决策：STAGE_VALIDATION_FAILURE_THRESHOLD 目前不可达

**现象**：失败码 `STAGE_VALIDATION_FAILURE_THRESHOLD`（≥50% stage 验证失败 → decision 转 `reunderstanding_required`）在当前 orchestrator adapter 路径下永远不会触发。已用 `t.Skip` 如实记录，不是隐藏问题。

**根因**：`browser_agent_outcome_verifier_adapter.go` 里的 `convertEventsToPostExecutionAnalyses`（约 L1224）构建 `PostExecutionAnalysis` 时从未填充 `ObservedIssues` 字段。下游 `failedStageCount` 结构上恒为 0，阈值分支永远进不去。

**安全性**：不是漏洞。真正失败的 stage 仍会被 `STAGE_FAILED` / `REQUIRED_ASSERTION_FAILED` 拦下，decision 正确转为 `stop_and_report`。丢的只是"是否该转 reunderstanding_required 这一更细的分类"。

**需要你决定**：是否要让适配器把真实 observed issues 映射进 `ObservedIssues`（执行层/adapter wiring 改动，成本由你评估）？还是暂时接受这个分支不可达（毕竟安全性没问题）？我这边不改这块，因为它牵涉执行侧数据怎么传进来，超出验证链路职责。

## 二、需孟洋确认：3 个失败码的 responsibility_domain

冻结文档 `validation-report-expectations-controlled-packages-2026-08-10.md` 里，以下 3 处责任域按规则文档现有信息推断，标了"待与孟洋对齐"，需要你拍板：

| 场景 | 失败码 | 当前标注 | 疑点 |
| --- | --- | --- | --- |
| locator_missing | STAGE_FAILED | app（暂定） | 规则文档示例给的是 server，但根因是 App 侧 selector 猜测，我按根因标了 app，需确认哪个口径对 |
| required_validation_failure | REQUIRED_ASSERTION_FAILED | 待定 | 规则文档没明确写这个码的责任域 |
| wait_timeout | STAGE_FAILED | server（暂定，按规则文档示例） | 需确认等待超时算 server 还是 environment |

## 三、需孟洋知悉：真实包复跑仍卡 APP-001

Day-4/5 的真实 App 包独立验证，我这边已经把**冻结预期规格**和**验收清单**都准备好了（成功包 + 3 个失败场景，五项必查点，签收/不签收标准），一旦 APP-001（App 侧 selector 生成问题）修好、能出真实包，我可以马上对照签收，不需要等我再开发。

## 四、需一凯知悉：Direct API 落地后要复核一件事

目前 Direct API（TLS 控制口/lease/gateway）还没进代码库，只在你的规格文档里。当前验证链路通过 `browser_agent_outline_runner.go` 走三阶段 OutcomeVerifier，没有 legacy bypass。**等你把 Direct 结果路径实现出来后，麻烦确认一下它是否复用同一个三阶段调用链路**——如果复用，我这边不需要改动；如果不复用（比如结果走了新的组装路径），需要我们再对一次，确保新路径也完整跑三阶段验证。

## 五、仅供参考、无需处理

- `MISSING_MP4_VIDEO` 目前只在产物完整性测试里跟其他产物一起检测，没单独断言 code/severity。Warning 级、非阻断，风险低，可以不管。
- 本周新增的 warning 码 `EVIDENCE_ARTIFACT_REFERENCE_BROKEN` 已进注解表，但还没写进 `browser-agent-outcome-verifier-rules-v1.md` 的失败码表；验收清单已同步措辞避免误判为"未定义码"而拒签，规则文档本身的更新不着急。
