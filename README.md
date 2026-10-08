### 因我个人习惯使用XrayR，我会继续维护此项目。但不保证更新频率。如您在使用过程中出现任何问题，请联系 ModusNyan@gmail.com 我会尽力修复
# XrayR

[![](https://img.shields.io/badge/TgChat-@XrayR讨论-blue.svg)](https://t.me/XrayR_project)
[![](https://img.shields.io/badge/Channel-@XrayR通知-blue.svg)](https://t.me/XrayR_channel)
![](https://img.shields.io/github/stars/modusnyan/XrayR)
![](https://img.shields.io/github/forks/modusnyan/XrayR)
![](https://github.com/modusnyan/XrayR/actions/workflows/release.yml/badge.svg)
![](https://github.com/modusnyan/XrayR/actions/workflows/docker.yml/badge.svg)
[![Github All Releases](https://img.shields.io/github/downloads/modusnyan/XrayR/total.svg)]()


[English](https://github.com/modusnyan/XrayR/blob/master/README-en.md)|[Iranian](https://github.com/modusnyan/XrayR/blob/master/README_Fa.md)|[Vietnamese](https://github.com/modusnyan/XrayR/blob/master/README-vi.md)

A Xray backend framework that can easily support many panels.

一个基于 Xray 的后端框架，支持 V2ray、Trojan、Shadowsocks 协议，极易扩展，支持多面板对接。

如果您喜欢本项目，可以右上角点个 star+watch，持续关注本项目的进展。

使用教程：[详细使用教程](https://github.com/modusnyan/XrayR)


## 免责声明

本项目只是本人个人学习开发并维护，本人不保证任何可用性，也不对使用本软件造成的任何后果负责。

## 特点

* 永久开源且免费。
* 支持 V2ray、Trojan、Shadowsocks 多种协议，以及 Vless、XTLS、REALITY 等新特性。
* 支持单实例对接多面板、多节点，无需重复启动。
* 支持限制在线 IP、节点端口级别限速、用户级别限速。
* 支持自动申请与续签 TLS 证书（ACME http / tls / dns 三种方式）。
* 支持审计规则、自定义 DNS 与路由。
* **配置可在启动前完整校验**：一次返回全部问题，错误信息带字段路径与修复建议。
* **配置热重载**：修改配置文件自动生效；新配置无效或启动失败时保留上一份可用配置。
* **面板不可用时仍可启动**：使用本地快照缓存恢复上一份有效配置。
* **内置可观测性**：Prometheus 指标 + `/healthz` `/readyz` `/status` 诊断端点。
* 方便编译和升级，可以快速更新核心版本，支持 Xray-core 新特性。

## 命令行

```
XrayR config init      # 交互式（或纯 flag）生成配置
XrayR config check     # 纯本地静态校验，可作为 systemd ExecStartPre
XrayR config show      # 显示归一化后的最终配置（敏感字段已脱敏）
XrayR config migrate   # 迁移旧版配置到当前版本
XrayR doctor           # 只读体检：配置 / DNS / TCP / TLS / 面板 API / Redis / 端口占用
XrayR                  # 启动服务（-c 指定配置文件）
```

推荐部署流程：

```bash
XrayR config init
XrayR config check
XrayR doctor
systemctl enable --now XrayR
```

完整说明见 [docs/cli.md](docs/cli.md)。

## 功能介绍

| 功能        | v2ray | trojan | shadowsocks |
|-----------|-------|--------|-------------|
| 获取节点信息    | √     | √      | √           |
| 获取用户信息    | √     | √      | √           |
| 用户流量统计    | √     | √      | √           |
| 服务器信息上报   | √     | √      | √           |
| 自动申请tls证书 | √     | √      | √           |
| 自动续签tls证书 | √     | √      | √           |
| 在线人数统计    | √     | √      | √           |
| 在线用户限制    | √     | √      | √           |
| 审计规则      | √     | √      | √           |
| 节点端口限速    | √     | √      | √           |
| 按照用户限速    | √     | √      | √           |
| 自定义DNS    | √     | √      | √           |

## 支持前端

| 前端                                                     | v2ray | trojan | shadowsocks             |
|--------------------------------------------------------|-------|--------|-------------------------|
| sspanel-uim                                            | √     | √      | √ (单端口多用户和V2ray-Plugin) |
| v2board                                                | √     | √      | √                       |
| [PMPanel](https://github.com/ByteInternetHK/PMPanel)   | √     | √      | √                       |
| [ProxyPanel](https://github.com/ProxyPanel/ProxyPanel) | √     | √      | √                       |
| [WHMCS (V2RaySocks)](https://v2raysocks.doxtex.com/)    | √     | √      | √                       |
| [GoV2Panel](https://github.com/pingProMax/gov2panel)   | √     | √      | √                       |
| [BunPanel](https://github.com/pennyMorant/bunpanel-release)   | √     | √      | √                       |
| [Xboard](https://github.com/cedar2025/Xboard)          | √     | √      | √                       |

面板名称大小写不敏感，并保留历史别名（`Xboard` / `NewV2board` / `V2board` 等价）。写错时会提示最接近的候选名称。各面板支持的功能矩阵见 [docs/panels.md](docs/panels.md)。

## 软件安装

### 一键安装（Debian）

```bash
curl -fsSL https://raw.githubusercontent.com/Activity163/XrayR/master/release/install.sh -o install.sh
sudo bash install.sh
```

不带参数运行会打开管理菜单：

```
  1) 安装 / 重装（二进制 + systemd）      7) 查看日志
  2) 安装 / 重装（Docker）                8) 编辑配置
  3) 启动                                9) 校验配置
  4) 停止                               10) 更新到最新版本
  5) 重启                               11) 开机自启 开 / 关
  6) 查看状态                           12) 卸载
```

安装会把自身装成 `xrayr` 命令，之后随时 `xrayr menu` 打开菜单，也可用子命令：
`xrayr start|stop|restart|status|logs|edit|check|update|enable|disable|uninstall`。

**安装不会自动启动服务。** 装完只做三件事：

```bash
xrayr edit      # 改配置里的 ApiHost / ApiKey / NodeID / NodeType
xrayr check     # 校验配置
xrayr start     # 启动（会等服务稳定运行，失败时直接打日志）
```

安装时生成 `/etc/XrayR/config.yml` —— **一份带完整注释的模板**，
只有 4 个字段需要改（面板地址、密钥、节点 ID、协议），其余是可选的，
默认注释掉。不用来回查文档。

### 从源码编译

需要 Go 1.25.3 或更高版本：

```bash
git clone https://github.com/Activity163/XrayR.git
cd XrayR
go build -trimpath -ldflags "-s -w" -o XrayR .
```

### 使用 Docker 部署

菜单第 2 项（或 `xrayr docker`）会自动拉镜像、生成配置、挂载规则数据并启动容器：

```bash
docker run -d --name xrayr --network host --restart unless-stopped \
  -v /etc/XrayR/config.yml:/etc/XrayR/config.yml:ro \
  -v /etc/XrayR/cache:/etc/XrayR/cache \
  ghcr.io/xrayr-project/xrayr:latest
```

### 规则数据（geoip.dat / geosite.dat）

`geoip:` / `geosite:` 路由规则依赖 `geoip.dat` 与 `geosite.dat`。这两个文件**不再存放在本仓库**（合计约 14 MB 且持续更新），每次安装都会从
[Loyalsoldier/v2ray-rules-dat](https://github.com/Loyalsoldier/v2ray-rules-dat)
的最新 release 下载并校验 sha256：

```bash
bash release/download-rules-dat.sh /etc/XrayR
```

脚本需与 `config.yml` 放在同一目录（XrayR 通过 `XRAY_LOCATION_ASSET` 在该目录查找规则数据）。Docker 镜像已内置这两个文件。

## 配置文件及详细使用教程

配置采用带版本号的 YAML（`ConfigVersion: 1`），未知字段会被拒绝而不是静默忽略。仓库提供三份模板：

| 模板 | 用途 |
|------|------|
| `release/config/config.minimal.yml` | 首次运行必须修改的最小字段 |
| `release/config/config.yml.example` | 常用功能 + 注释，适合大多数用户 |
| `release/config/config.full.yml` | 全部高级字段与可选值 |

文档索引见 [docs/index.md](docs/index.md)，包括
[配置](docs/configuration.md)、[命令行](docs/cli.md)、[诊断](docs/diagnostics.md)、
[可观测性](docs/observability.md)、[迁移](docs/migration.md)、[故障排查](docs/troubleshooting.md)。

## 开发

```bash
go build ./... && go vet ./... && go test -race ./...
golangci-lint run
bash tools/e2e/run-e2e.sh      # 端到端：假面板 → 节点 → 真客户端 → 真流量
```

`tools/e2e/run-e2e.sh` 不需要真实面板，也不需要外网：它起一个 Xboard 桩面板，
用真 xray-core 客户端把流量打穿节点（`curl → socks → VMess/WS → 节点 → freedom → 本地 HTTP 服务`），
并验证面板改端口时节点会平滑迁移、下发无法应用的配置时会回滚。CI 每次都会跑。

## Thanks

* [Project X](https://github.com/XTLS/)
* [V2Fly](https://github.com/v2fly)
* [VNet-V2ray](https://github.com/ProxyPanel/VNet-V2ray)
* [Air-Universe](https://github.com/crossfw/Air-Universe)
* [Loyalsoldier/v2ray-rules-dat](https://github.com/Loyalsoldier/v2ray-rules-dat)

## Licence

[Mozilla Public License Version 2.0](https://github.com/modusnyan/XrayR/blob/master/LICENSE)

## Telgram

[XrayR后端讨论](https://t.me/XrayR_project)

[XrayR通知](https://t.me/XrayR_channel)

## Stargazers over time

[![Stargazers over time](https://starchart.cc/modusnyan/XrayR.svg)](https://starchart.cc/modusnyan/XrayR)
