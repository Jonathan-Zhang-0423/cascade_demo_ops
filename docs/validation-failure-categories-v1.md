# Validation 失败分类 v1

| 分类 | 责任域 | 典型错误 | 是否可替代业务成功 |
| --- | --- | --- | --- |
| `app_package_contract` | `app` | selector、审批、需求映射或证据引用缺失 | 否 |
| `server_execution` | `server` / `validation` | 结果收集、运行时副本或验证器错误 | 否 |
| `browser_runtime` | `browser_runtime` | 导航失败、元素不可见、会话过期、超时 | 否 |
| `media_evidence` | `media_delivery` | 截图、录屏、几何、时间线或上传不一致 | 否 |
| `provider_candidate` | `provider_candidate` | 候选模型生成或候选审核失败 | 否；候选成功也不能证明页面业务成功 |
| `environment` | `environment` | 端口、网络、Chromium、FFmpeg 或本地依赖 | 否 |

每个未通过 finding 必须包含稳定错误码、责任域、阻断级别、至少一个证据引用、复现条件和最小修复建议。报告只引用脱敏证据 ID、artifact ID 与受控 URI，不包含密码、Cookie、Token、API Key 或页面输入值。
