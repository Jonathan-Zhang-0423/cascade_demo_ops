# Validation Agent v1 报告闭环交付（2026-08-18）

## 本轮已落地

- 新增 `demoops.validation_run_report.v1`，统一 package/run/hash、正式来源、阶段汇总、失败分类、责任域、证据、复现条件和 App/Server 反馈。
- `RecordingResultPackage` 在 Outline Router、Direct Finalizer 和 dev-visible replay 路径中附加 `validation_run_report`。
- Replay Manifest 阶段增加 approved target URL、动作/结果证据 ID、录屏 offset 和非敏感 viewport；保持旧字段兼容。
- Runtime verifier 真实发出 `forbidden_operation_attempted` 和 `url_change_not_business_completion`。
- 增加成功受控 fixture 报告样例、App/Server 反馈模板。

## 仍然不是本轮完成项

1. `ValidationEventPayload` 通过 ExchangeStreamWriter 的实时发射。
2. video worker/uploader 原生生成 `media_artifact_generation_failed` / `media_artifact_upload_failed` finding。
3. 正式 App installation + 正式 credential + 部署环境下的 App→Server→视频→ACK 实验。
4. 跨 run 回归持久化与比对。

`docs/validation-run-report-v1-example.json` 明确标记 `formal_app_server_success=false`；它只能作为 Server-controlled 示例，不能作为正式 App 联调签收。

## 验证入口

```bash
cd backend
go test ./internal/model ./internal/app ./internal/orchestrator ./internal/directtransport ./internal/executor -count=1
```
