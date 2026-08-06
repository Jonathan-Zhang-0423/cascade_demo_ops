# Browser Agent 直连部署手册

这是 App 正式执行主链路。App 不通过 DemoOps Exchange/control-plane 上传或执行正式任务；兼容代码不得作为直连失败时的回退。只有用户提供的真实 Ubuntu、TLS、DNS、防火墙和真实 Chromium 全链路通过后，才能标记发布可用。

## 1. 网络与最小权限

- 固定控制口默认 TCP `18443`，只接受 TLS 1.3 和 App bootstrap token。
- 数据口默认 TCP `24000-24031`，最多支持 32 个 App 安装同时持有独立监听端口；扩容必须经过防火墙暴露面审核。
- Worker API 固定在 `127.0.0.1:18444`，不得加入主机防火墙或云安全组入站规则。
- 控制口和数据口共用覆盖 advertised host 的有效证书；正式验收禁止 `curl -k`。
- Gateway 使用 `cascade-browser-gateway`，Worker 使用 `cascade-browser-worker`；不得共用服务账号。Gateway 不能读取 Worker 输出目录，Worker 不能读取 TLS 私钥或 Gateway spool。
- `/etc/cascade-browser-agent/gateway.env` 为 `root:root 0600`，包含 bootstrap token、Worker token和 TLS 路径。
- `/etc/cascade-browser-agent/worker.env` 为 `root:root 0600`，只包含 Worker token及运行时路径，禁止出现 bootstrap token或 TLS 私钥路径。
- 两个 token 必须独立随机生成。App token、Ed25519 安装私钥和短期租约只存 Windows Credential Manager，项目、包、日志和报告不得出现明文。
- Worker 需要 V8/Chromium JIT，systemd 单元不能使用 `MemoryDenyWriteExecute=true`。仍保留独立无登录账户、`NoNewPrivileges`、只读系统目录和专属可写根。

数据请求使用 lease token 派生的 HKDF-SHA256 密钥及 AES-256-GCM，并绑定时间戳、nonce、请求摘要、消息类型、方向、端口和安装身份。重复 nonce、过期时间戳、跨安装读取、分块认证失败和总 checksum 不一致均拒绝。

## 2. 构建受管运行时

在可信构建环境构建与测试，不在 App 电脑上用 WSL 冒充服务器验收：

```bash
cd backend
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o ../dist/server/linux-amd64/browser-agent-gateway ./cmd/browser-agent-gateway
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o ../dist/server/linux-amd64/browser-agent-direct-worker ./cmd/browser-agent-direct-worker
sha256sum ../dist/server/linux-amd64/browser-agent-gateway ../dist/server/linux-amd64/browser-agent-direct-worker
```

ARM64 服务器使用 `GOARCH=arm64`。服务器还需安装 Node.js、FFmpeg/ffprobe 和 Chromium 系统依赖。构建 Worker 时必须把浏览器安装到明确的受管目录，不能落在 root 的默认缓存：

```bash
cd video-worker
npm ci
npm run typecheck
npm test
npm run build
sudo install -d -o root -g root -m 0755 /opt/cascade-browser-agent/playwright-browsers.build
sudo env PLAYWRIGHT_BROWSERS_PATH=/opt/cascade-browser-agent/playwright-browsers.build \
  npx playwright install --with-deps chromium
sudo chown -R root:root /opt/cascade-browser-agent/playwright-browsers.build
sudo chmod -R go-w /opt/cascade-browser-agent/playwright-browsers.build
```

`npx playwright install --with-deps` 会修改系统依赖，只能在审核后的真实 Ubuntu 执行。安装器接收这个明确目录并原子复制到正式路径。

## 3. 一键安装

若服务器首次只开放密码登录，可把 `SSH_HOST`、`SSH_PORT`、`SSH_USER`、`SSH_PASSWORD` 写入被 Git 忽略且 ACL 收紧的本地 `.env`，然后运行 `scripts/bootstrap-real-ubuntu.ps1`。该密码只通过 `SSH_ASKPASS` 子进程环境完成一次临时 Ed25519 公钥安装，不进入命令参数、日志或仓库；成功后所有自动配置改用密钥认证。也可使用 `SSH_PASSWORD_SECRET_REF=env:LOCAL_SSH_PASSWORD`，但引用指向的环境变量必须在当前进程存在。

准备真实 DNS、覆盖该 DNS 的 full chain/private key，并确认二进制 SHA-256。安装器可重复运行，保留带 UTC 时间戳的上一版 Worker 和 Chromium 目录；它不会输出任何 token，也不会修改 UFW 或云安全组：

```bash
sudo bash ./deploy/install-browser-agent-direct.sh \
  --gateway-bin ./dist/server/linux-amd64/browser-agent-gateway \
  --gateway-sha256 '<verified-gateway-sha256>' \
  --worker-bin ./dist/server/linux-amd64/browser-agent-direct-worker \
  --worker-sha256 '<verified-worker-sha256>' \
  --video-worker-dir ./video-worker \
  --playwright-browsers-dir /opt/cascade-browser-agent/playwright-browsers.build \
  --tls-cert /secure/source/fullchain.pem \
  --tls-key /secure/source/privkey.pem \
  --advertised-host browser-agent.example.com
```

必须人工确认云安全组和主机防火墙只开放 TCP `18443` 与选定数据端口范围。若批准使用 UFW，可执行：

```bash
sudo bash deploy/configure-browser-agent-firewall.sh
```

bootstrap token 只由服务器管理员从 root-only 配置转移到 App 的操作系统凭据库；不要粘贴到聊天、命令参数、工单或普通文件。Worker token 不离开服务器。完成公钥 bootstrap 后，推荐在 App 所在的 Windows 用户会话运行：

```powershell
.\scripts\configure-browser-agent-direct.ps1
```

该脚本只从被 Git 忽略的 `.env` 读取 SSH 地址、用户名和端口，不读取服务器密码。Go 导入器使用固定远端命令、Ed25519 公钥认证和严格 `known_hosts`，把 gateway bootstrap token 仅通过加密 SSH 会话读入进程内存，然后调用与 Wails UI 相同的服务方法写入 Windows Credential Manager、生成每个 App 安装独立的 Ed25519 身份，并写入不含秘密的 `browser_agent_direct.json`。token 不经过 PowerShell、剪贴板、命令参数、stdout 或临时文件。输出仅包含布尔状态、控制地址 host 和脱敏错误类别；`reachable=false` 表示已安全配置但公网端口尚不可达，不得解释为执行链路可用。

## 4. 脱敏预检

预检输出单行 JSON，只包含布尔状态、端口范围和占用数量，不输出 DNS 地址、token、证书正文或本地路径：

```bash
sudo /usr/local/libexec/cascade-browser-agent-direct-preflight
```

The installer places this exact redacted preflight command on the server. It
does not print token contents or certificate paths. If the installer has not
yet been run, execute the repository copy once from the trusted deployment
checkout; a missing installed preflight is itself a deployment blocker.

它检查：配置隔离和权限、两个 systemd 服务、控制口监听、Worker 口仅 loopback、DNS、证书 SAN/七天有效期、服务账户下的 Node/FFmpeg/Worker health RPC、真实 headless Chromium 启动，以及经过认证的 TLS health。任何字段为 false 都是发布阻断项。

从 App 所在机器还必须用真实 DNS（不使用 `-k`）验证公网 TLS 和协议。不要把 token 写入 shell 历史；可在当前安全终端临时读取，调用后立即清除环境变量。

Windows 客户端可先运行不含凭据的外部预检：

```powershell
.\scripts\preflight-browser-agent-direct.ps1
```

它会把 UFW/云安全组阻断与 TLS/服务故障分开报告；数据端口只有在 App 获得真实租约后才验证。

### 公网阻断判定

UFW 规则与公网连通性必须分开记录。若服务器上的 `ufw status` 已显示允许 `18443/tcp` 和 `24000:24031/tcp`，且 `18444` 没有入站规则，但 App 所在机器的 TCP `18443` 仍不可达，则阻断位于云厂商安全组、上游 ACL 或运营商网络；不得把这种状态标记为 TLS/协议故障，也不得通过开放 `18444` 绕过。只有外部 TCP 成功后，才继续真实证书握手、认证 health、租约端口和素材下载验收。

## 5. App 操作与正式闭环

1. 设置 →“Ubuntu Browser Agent 服务器”，输入固定 HTTPS 控制地址和 bootstrap token，保存并验证。
2. UI 分别显示地址配置、令牌入系统凭据库和协议可达；“已配置”不能冒充“可上传”。
3. 完成执行图和正式包审批。审批前不得读取 token、申请租约或上传。
4. 审批后 App 申请独立数据端口并显示租约、到期时间和 job ID；失败不得回退 Exchange。
5. 含凭据任务通过租约 AEAD 独立上传一次性凭据封套。登录填写期间暂停 trace 并遮罩输入，确定性登录成功后才恢复。
6. Worker 只运行已批准的 `browser-agent-outline-v1`，不补 selector、不生成替代截图或伪造视频。
7. App 轮询真实状态并按 4 MiB 独立认证分块下载视频、截图、trace 和结果包，核对总 SHA-256/字节数后才标记已校验。

展示视频候选只使用 `presentation_video_candidate` 逻辑能力。App 不指定供应商、模型 ID、API endpoint 或模型原生参数；候选非权威、不得替代真实 UI/业务证据，必须显式人工审核，生成失败不得阻塞真实 Browser Agent 交付。

## 6. 升级、回滚和日志

升级前记录已验证二进制 hash并备份 `/etc/cascade-browser-agent` 到 root-only 存储。重新运行安装器后执行预检和完整 App 验收。安装器保留：

```text
/opt/cascade-browser-agent/video-worker.previous.<UTC timestamp>
/opt/cascade-browser-agent/playwright-browsers.previous.<UTC timestamp>
```

回滚时停止 Worker，将选定的 previous 目录改回正式目录，恢复与其匹配的二进制和环境文件，然后 `systemctl daemon-reload`、启动服务并重新预检。不要跨版本混用 Worker JS、Playwright package 和 Chromium cache。

查看脱敏服务状态：

```bash
sudo systemctl status cascade-browser-agent-gateway cascade-browser-agent-worker --no-pager
sudo journalctl -u cascade-browser-agent-gateway -u cascade-browser-agent-worker --since '-15 min' --no-pager
```

日志不得增加请求头、凭据封套正文、执行包正文或页面表单值。网关重启会关闭活动租约；无凭据任务可重新排队，含凭据任务必须回到 `credential_reupload_required`，绝不能从磁盘恢复明文。

## 7. 发布门禁

必须在用户授权的真实 Ubuntu 上完成：App 配置 → TLS/协议验证 → 正式审批 → 独立租约/端口 → credential envelope → Worker claim → 真实 Chromium → 真实视频/截图/trace → 状态与结果包 → App 分块下载 → SHA-256/字节数校验 → 打开/播放全部素材。材料写入新的 `test-runs/app-formal-e2e-<timestamp>/` 并执行 secret-leak 扫描。任一步缺失都保持 `awaiting_real_ubuntu_e2e`，不得声称可发布。
