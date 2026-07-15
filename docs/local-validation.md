# Local Validation

Use the repo-level validation command before pushing backend, worker, or web
changes:

```powershell
corepack.cmd pnpm verify
```

The command runs:

- `go version`
- `go build ./...`
- `go test ./...`
- `@cascade/video-worker` typecheck and build
- `@cascade/web` tests and build
- `git diff --check`

For partial checks:

```powershell
corepack.cmd pnpm verify -- --go-only
corepack.cmd pnpm verify -- --wsl-go --go-only
corepack.cmd pnpm verify -- --wsl-go --wsl-proxy=7897 --go-only
corepack.cmd pnpm verify -- --node-only
corepack.cmd pnpm verify -- --skip-go
```

`--skip-go` is not strict validation. Use it only when Windows Application
Control blocks Go in the local tool environment and Go validation will be run in
another trusted environment such as WSL or the server.

## Windows Application Control

On locked-down Windows hosts, Device Guard or Application Control may block:

- `C:\Program Files\Go\bin\go.exe`
- Go-generated test binaries under `.gotmp`

The verification script pins Go output directories to the repo:

```powershell
GOCACHE=D:\Engine-7-8\.gocache
GOTMPDIR=D:\Engine-7-8\.gotmp
```

Preferred fixes:

1. Run strict validation in WSL/Linux or on the server.
2. Ask the policy owner to allow `go.exe` and generated test binaries under
   `D:\Engine-7-8\.gotmp`.
3. As a short-term fallback, run `corepack.cmd pnpm verify -- --skip-go` locally
   and run `corepack.cmd pnpm verify -- --go-only` in the trusted environment.

## WSL Go Validation

On Windows hosts where `go.exe` is blocked, install Ubuntu WSL and run Go checks
inside Linux:

```powershell
wsl.exe --install -d Ubuntu --no-launch
wsl.exe -d Ubuntu -- bash -lc "apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y golang-go"
```

If `proxy.golang.org` times out, configure a reachable Go proxy inside WSL:

```powershell
wsl.exe -d Ubuntu -- bash -lc "go env -w GOPROXY=https://goproxy.cn,direct GOSUMDB=sum.golang.google.cn"
```

Then run strict validation from Windows while routing Go through WSL:

```powershell
corepack.cmd pnpm verify -- --wsl-go
```

The command runs backend Go checks inside the `Ubuntu` WSL distro and keeps
worker/web checks on the Windows Node toolchain.

If Windows has a local mixed proxy port, WSL cannot reliably use
`127.0.0.1:<port>` because it runs behind a NAT boundary. Pass the proxy port so
the script resolves the Windows host gateway from `/etc/resolv.conf` and exports
WSL-side proxy variables:

```powershell
corepack.cmd pnpm verify -- --wsl-go --wsl-proxy=7897
```

The same value can be provided as an environment variable:

```powershell
$env:CASCADE_WSL_PROXY_PORT = "7897"
corepack.cmd pnpm verify -- --wsl-go
```
