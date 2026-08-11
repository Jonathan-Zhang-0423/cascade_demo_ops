# Validation Agent 真实包验证签收清单

日期：2026-08-10  
验证人：明达  
对接：孟洋、一凯  
范围：真实 App 执行包的 Validation 链路独立验证签收

> **参考基线：** 受控场景包的预期 ValidationReport 规格见
> [`validation-report-expectations-controlled-packages-2026-08-10.md`](./validation-report-expectations-controlled-packages-2026-08-10.md)，
> 真实包的实际值应与该文档中对应场景的预期值逐字段比对。

## 一、验证目标

配合一凯的 Direct API 真实执行，对真实 App 包（非受控场景包）做 Validation 链路的独立签收。

**验收标准：**
- 成功场景：所有 required checks passed + final_decision = continue + ReplayManifest.status = "success"
- 失败场景：至少一个 blocking check failed + final_decision = stop_and_report + ReplayManifest.status = "failed" + 正确的 failure_code 和 responsibility_domain

## 二、五项必查点

### 2.1 源包一致性

**检查项：**
- `RecordingResultPackage.SourcePackageID` 与批准包的 `PackageID` 一致
- `RecordingResultPackage.AuditTrail.SourcePackageDigest` 与批准包的 `bundle_hash_sha256` 一致

**如何检查：**
```bash
# 提取结果包中的源包信息
jq '.source_package_id, .audit_trail.source_package_digest' result-package.json

# 对比批准包
jq '.package_id, .executable_script_bundle.reproducibility.bundle_hash_sha256' approved-package.json
```

**签收标准：**
- ✅ 两对值完全一致
- ❌ 任一不一致 → 不签收，报告不一致详情

### 2.2 Policy hash 一致性

**检查项：**
- 所有 ValidationReport 中的 `policy_hash_sha256` 与批准包的 `plan_hash_sha256` 一致

**如何检查：**
```bash
# 提取所有 ValidationReport 的 policy_hash
jq '.validation_reports[].policy_hash_sha256' result-package.json | sort -u

# 对比批准包的 plan_hash
jq '.executable_script_bundle.reproducibility.plan_hash_sha256' approved-package.json
```

**签收标准：**
- ✅ 所有 report 的 policy_hash 相同，且与批准包一致
- ❌ 出现多个不同的 policy_hash 或与批准包不一致 → 不签收

### 2.3 ValidationReport 数量与顺序

**检查项：**
- ValidationReport 数量 = 1（pre_execution）+ N（runtime_stage，N = stage 数量）+ 1（post_execution）
- 顺序固定：reports[0].phase = pre_execution，reports[1:N+1].phase = runtime_stage，reports[N+1].phase = post_execution

**如何检查：**
```bash
# 提取所有 report 的 phase
jq '.validation_reports[] | {phase: .phase, node_id: .node_id}' result-package.json
```

**签收标准：**
- ✅ 数量和顺序符合 1+N+1 模式
- ❌ 缺少 pre/post report，或顺序错乱 → 不签收

### 2.4 Blocking 失败码决策一致性

**检查项：**
- 如果任一 ValidationCheck 的 `severity = blocking` 且 `passed = false`，则：
  - 对应的 ValidationReport.decision 必须是 `stop_and_report` 或 `reunderstanding_required`
  - 最终的 ReplayManifest.final_decision 必须是 `stop_and_report` 或 `reunderstanding_required`

**如何检查：**
```bash
# 查找所有 blocking + failed 的 check
jq '.validation_reports[] | select(.checks[] | select(.severity == "blocking" and .passed == false)) | {report_id, decision}' result-package.json

# 检查 final_decision
jq '.replay_manifest.final_decision' result-package.json
```

**签收标准：**
- ✅ 有 blocking 失败 → decision 必须是 stop_and_report 或 reunderstanding
- ✅ 无 blocking 失败 → decision 可以是 continue 或 repair_allowed
- ❌ 有 blocking 失败但 decision = continue → 不签收（放行了不该放行的）

### 2.5 失败码与责任域正确性

**检查项：**
- 所有失败的 ValidationCheck 必须有 `code` 和 `responsibility_domain`
- `code` 必须是已定义失败码之一：以 `browser-agent-outcome-verifier-rules-v1.md` 的失败码表为准，并加上 `browser_agent_validation_annotations.go` 注解表中已登记的码（例如本轮新增的 warning 级 `EVIDENCE_ARTIFACT_REFERENCE_BROKEN`）。规则文档尚未收录该码时，以注解表为准，不作为不签收理由
- `responsibility_domain` 必须是 `app` / `server` / `validation` / `environment` 之一

**如何检查：**
```bash
# 提取所有失败 check 的 code 和 domain
jq '.validation_reports[].checks[] | select(.passed == false) | {code, responsibility_domain}' result-package.json
```

**签收标准：**
- ✅ 所有失败 check 都有 code 和 domain，且在已定义范围内
- ❌ 出现未定义的 code，或 domain 为空/非法值 → 不签收

## 三、成功场景签收标准

**前提：** 真实包是预期成功的执行（如：贪吃蛇成功创建项目）

**签收清单：**

| 项目 | 预期 | 实际 | 结论 |
|------|------|------|------|
| 2.1 源包一致性 | ✅ 一致 | | |
| 2.2 Policy hash 一致性 | ✅ 一致 | | |
| 2.3 ValidationReport 数量与顺序 | ✅ 符合 1+N+1 | | |
| 2.4 无 blocking 失败 | ✅ 无 blocking failed | | |
| 2.5 check 的 code 和 domain 有效 | ✅ 全部有效 | | |
| ReplayManifest.final_decision | `continue` | | |
| ReplayManifest.status | `"success"` | | |
| demo_video/MP4 artifact | ✅ 存在且完整 | | |
| Trace、StageEventLog artifact | ✅ 存在且完整 | | |

**签收结论：**
> ✅ 真实包「[包名]」验证链路签收通过。所有 ValidationReport 正确生成，final_decision = continue，无误判。

## 四、失败场景签收标准

**前提：** 真实包是预期失败的执行（如：跨域访问被拦截）

**签收清单：**

| 项目 | 预期 | 实际 | 结论 |
|------|------|------|------|
| 2.1 源包一致性 | ✅ 一致 | | |
| 2.2 Policy hash 一致性 | ✅ 一致 | | |
| 2.3 ValidationReport 数量与顺序 | ✅ 至少有 pre + 部分 runtime + post | | |
| 2.4 至少一个 blocking check failed | ✅ 存在 | | |
| 2.5 失败 check 的 code 和 domain | ✅ 正确，与实际失败原因匹配 | | |
| ReplayManifest.final_decision | `stop_and_report` 或 `reunderstanding_required` | | |
| ReplayManifest.status | `"failed"` | | |
| ReplayManifest.failed_node_id | ✅ 指向实际失败的 stage | | |
| 失败现场 artifact（截图、Trace、事件日志） | ✅ 存在 | | |
| 失败包未产生成功 MP4（孟洋 day-5）| demo_video/mp4 artifact **不存在**，或 Recording result Status = failed（未生成） | | |

**说明（最后一项）：** 确认失败执行不会伪造成功 MP4。如果失败包的结果中存在 demo_video/mp4 artifact 且 recording status 标记为 generated/success，则视为误判，不签收。

**如何检查最后一项：**
```bash
# 确认 demo_video artifact 不存在，或 recording status = failed
jq '.artifacts[] | select(.type == "demo_video" or .type == "mp4")' result-package.json
jq '.recording_result.status' result-package.json
```

**签收结论：**
> ✅ 真实包「[包名]」验证链路签收通过。Validation 正确识别失败，failure_code = [CODE]，responsibility_domain = [DOMAIN]，final_decision = stop_and_report，无误判无放行，未伪造成功 MP4。

## 五、不签收场景

**以下任一情况出现，立即停止签收并报告：**

1. **源包不一致**：`source_package_id` 或 `bundle_hash` 与批准包不匹配
2. **Policy hash 不一致**：ValidationReport 中的 policy_hash 与批准包不一致
3. **ValidationReport 缺失或乱序**：缺少 pre/post report，或顺序不符合 1+N+1
4. **决策不一致**：有 blocking 失败但 final_decision = continue（误放行）
5. **失败码缺失**：失败 check 没有 code 或 responsibility_domain
6. **失败码未定义**：出现既不在 `browser-agent-outcome-verifier-rules-v1.md` 失败码表、也不在 `browser_agent_validation_annotations.go` 注解表中的 code
7. **成功场景误判为失败**：预期成功的包被判为 failed
8. **失败场景误判为成功**：预期失败的包被判为 success
9. **失败包伪造成功 MP4**：失败包结果中存在 demo_video/mp4 artifact 且 recording status 非 failed（确认失败不会伪造成功 MP4）

**不签收报告模板：**
> ❌ 真实包「[包名]」验证链路不签收。原因：[具体原因]。详情：[不一致的字段值或缺失的检查]。需与孟洋/一凯确认修复方案。

## 六、使用方法

**第4天配合一凯验证时：**
1. 一凯提供真实包的 `RecordingResultPackage` JSON 文件（或指向 artifact 的路径）
2. 明达按照五项必查点逐一检查，记录结果
3. 根据成功/失败场景选择对应的签收清单，填写「实际」和「结论」列
4. 填写签收结论或不签收报告
5. 如签收通过，将结论发给孟洋；如不签收，立即与孟洋/一凯沟通

**可选：独立验证脚本**  
如时间允许，可编写 `backend/cmd/validate-result-package/main.go` 自动执行五项检查（含第4.失败场景第10项 MP4 伪造检测）。
