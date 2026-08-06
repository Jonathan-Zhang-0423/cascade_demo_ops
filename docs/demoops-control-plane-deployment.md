# DemoOps 控制面部署边界

> [!WARNING]
> 本文仅保留旧环境兼容参考。App 的正式执行上传不再连接 DemoOps 控制面。新环境请部署 [Ubuntu Browser Agent 直连网关](browser-agent-direct-deployment.md)。

DemoOps App 的 `product_url` 只表示客户待录制网站。执行服务地址必须由
`CASCADE_CLOUD_EXCHANGE_BASE_URL=<DEMOOPS_CONTROL_PLANE_BASE_URL>` 独立配置，
绝不从产品 URL、源码仓库 URL 或页面内容推导。

当前开发阶段建议让服务器进程监听 `127.0.0.1:4317`，由开发机建立 SSH
端口转发，再把 App 控制面配置指向隧道本地端口。不得在公网明文暴露 token、
项目摘要或执行包。外部测试前应配置 DemoOps 专属 HTTPS API 域名、TLS 和安装身份认证。

公网部署使用 `backend/cmd/controlplane`，不得直接暴露 `cmd/devserver`。
前者只注册 installation、execution package、SSE、result、download、ack、
review 和 revision 接口，并始终监听 HTTPS 反向代理后的环回地址。详细步骤见
`docs/demoops-control-plane-production-runbook.md`。

`cascadeai.cn` 是测试目标网站，只有明确的 smoke fixture 可以把它写入
`product_url` 和该任务的录制域名白名单。它不是 DemoOps 控制面、更新源、遥测
入口或数据库依赖。

部署前必须轮换已经暴露过的服务器密码，禁用密码登录并改用 SSH 密钥。IP、SSH
凭据、测试 token 和测试站点地址都不得进入源码、安装包、更新清单或日志。
