![Citeck Launcher](https://github.com/Citeck/ecos-ui/raw/develop/public/img/logo/ecos-logo.svg)

# Citeck Launcher

[English](../README.md) · [Русский](README.ru.md) · **中文** · [Español](README.es.md) · [Deutsch](README.de.md) · [Français](README.fr.md) · [Português](README.pt.md) · [日本語](README.ja.md)

[![Release](https://img.shields.io/github/v/release/Citeck/citeck-launcher?sort=semver)](https://github.com/Citeck/citeck-launcher/releases/latest)
[![Downloads](https://img.shields.io/github/downloads/Citeck/citeck-launcher/total)](https://github.com/Citeck/citeck-launcher/releases)
[![License: LGPL v3](https://img.shields.io/badge/license-LGPL--3.0-blue.svg)](https://www.gnu.org/licenses/lgpl-3.0)
![Platforms](https://img.shields.io/badge/platform-linux%20%7C%20macOS%20%7C%20windows-lightgrey)
[![Documentation](https://img.shields.io/badge/docs-readthedocs-8CA1AF?logo=readthedocs)](https://citeck-ecos.readthedocs.io/en/latest/index.html)

**用一个二进制文件运行 Citeck 平台——在你自己的电脑上，或者在服务器上。**

[Citeck](https://github.com/Citeck) 是专有 ECM/BPM 套件的自托管开源替代方案：管理文档与记录，使用内置的 BPMN 设计器自动化审批流程，并以极少代码甚至无需代码构建内部应用——门户、CRM、案件管理。用户、角色和权限均已内置。

手动运行它意味着要编排二十多个 Docker 服务。Citeck Launcher 替你完成这件事：一个约 24 MB 的单一二进制文件，负责安装平台、以 Docker 容器方式运行每一个服务（Keycloak、PostgreSQL、RabbitMQ 以及 Citeck Web 应用）、保持它们健康运行并进行升级。它是运行 Citeck 的官方推荐方式——既可作为桌面应用，也可在服务器上通过命令行使用。

<!-- TODO(screenshot): add an English-locale screenshot of the launcher dashboard here, e.g.
     ![Citeck Launcher](docs/img/dashboard.png) -->

**你需要准备：** Docker · Community 版需 **16 GB** 内存，Enterprise 版（约 24 个服务）需 **24–32 GB** · **50 GB 以上**空闲磁盘用于存放镜像和数据。在 Windows 和 macOS 上，请先安装 [Docker Desktop](https://www.docker.com/products/docker-desktop/)。

## 桌面应用还是服务器？

有两种运行方式——根据你希望 Citeck **在哪里**运行来选择：

| | 🖥 **桌面应用** | 🖧 **服务器（CLI）** |
|---|---|---|
| 适用于 | 你自己的电脑 | Linux 服务器 / 虚拟机（通常通过 SSH） |
| 安装方式 | 下载安装包，按向导逐步操作 | 一条 `curl … \| bash` 命令 |
| 界面 | 原生应用窗口（GUI） | 终端——`citeck` CLI + 设置向导 |
| 从这里开始 | [桌面应用](#桌面应用) | [服务器安装](#服务器安装) |

> **提示：** 本 README 中的 `curl … | bash` 快速开始以及各条 `citeck` 命令都仅适用于**服务器安装**。在你自己的电脑上，请通过**桌面应用**运行 Citeck——那里的一切操作都在 UI 中完成。

## 桌面应用

桌面应用在你自己的 Windows、macOS 或 Linux 电脑上运行 Citeck——就是一个普通的应用窗口，无需命令行。即使关闭窗口，Citeck 也会在后台继续运行。

请先安装 Docker Desktop，然后从[最新发布版本](https://github.com/Citeck/citeck-launcher/releases/latest)下载适用于你平台的安装包：

| 操作系统 | 文件 | 架构 |
|----|------|------|
| Windows | `citeck-desktop_<version>_windows_<arch>.msi` | amd64, arm64 |
| macOS | `citeck-desktop_<version>_darwin_<arch>.dmg` | amd64 (Intel), arm64 (Apple Silicon) |
| Linux | `citeck-desktop_<version>_linux_<arch>.deb` / `.rpm` | amd64, arm64 |

每个安装包都附带一个用于校验的 `.sha256` 文件。升级过程中会保留你的数据。

## 服务器安装

> **适用于 Linux 服务器或虚拟机**（amd64 或 arm64）——请通过 SSH 在服务器上执行以下步骤。前置条件：Docker 已安装并正在运行。

```bash
curl -fsSL https://github.com/Citeck/citeck-launcher/releases/latest/download/install.sh | bash
```

该脚本会为你的平台下载最新版本，安装到 `/usr/local/bin/citeck`，然后启动设置向导（`citeck install`）。向导是**交互式的，需要真实的终端**。它会询问你：

- 你将在浏览器中用于访问平台的**域名或 IP 地址**；
- 如何**保护连接**——自动、Let's Encrypt、自签名证书、你自己的证书，或纯 HTTP。（Let's Encrypt 需要一个指向本主机的公共 DNS 名称以及开放的入站 80 端口；如果无法访问，向导会回退到自签名证书。）
- 是否部署**演示数据**，以及是否安装 **systemd 服务**。

### 首次运行：会发生什么

**这需要一些时间——这是正常的。** 启动器会拉取数 GB 的 Docker 镜像，之后平台本身还需要大约 **10–15 分钟**才能启动完成：各服务按依赖顺序启动，Keycloak 会在首次启动时导入自己的 realm。你可以看到应用逐个变为 `RUNNING`：

```bash
citeck status -w
```

一切就绪后，向导会打印你的访问信息：

```
Citeck is ready!

Open in browser:  https://<the domain you entered>/
Login:            admin / <generated password>
```

关于这个界面，有两点需要注意：

- **管理员密码只显示一次。** 请复制保存——之后无法找回。如果丢失，可用 `citeck setup admin-password` 重置。
- **使用自签名证书时，浏览器会发出警告。** 这是预期行为——点击*高级* → *继续访问*。

如果约 20 分钟后仍看起来卡住，请先运行 `citeck diagnose`（加上 `--fix` 可让它自动修复能处理的问题）和 `citeck logs <app>`。

### 升级启动器

再次运行同一条命令——脚本会检测已安装的版本，提示更新，停止守护进程，并替换二进制文件。旧的二进制文件保留在 `/usr/local/bin/citeck.bak`，可通过 `citeck install --rollback` 恢复。你的数据会被保留。

## 概念

CLI 和文档中反复出现的三个术语：

- **Namespace（命名空间）** — 平台的一个隔离实例（拥有各自的容器、卷和数据）。它与 Linux 或 Kubernetes 的 namespace 无关，是启动器自己的概念。一台典型的服务器只运行一个。
- **Bundle（组合包）** — 决定一个平台版本由哪些应用、哪些版本组成，例如 Community 版或 Enterprise 版。`citeck upgrade <bundle:version>` 用于在它们之间切换。
- **Workspace（工作区）** — 这些定义的来源（通常是一个 Git 仓库，或用于隔离网络安装的离线 `.zip`）。

## 日常命令（服务器模式）

在桌面模式下，同样的操作可在应用 UI 中完成。

```bash
citeck status -w                 # 实时查看命名空间和每个应用的状态
citeck logs <app> -f             # 流式查看日志（不指定应用则查看守护进程自身的日志）
citeck stop <app>                # 停止某个应用——并在重启后仍保持停止状态
citeck start <app>               # 重新启动它（重新纳管）
citeck reload                    # 应用配置变更，仅重建发生变化的部分
citeck snapshot export <name>    # 备份所有卷（先停止平台，随后重新启动）
citeck upgrade <bundle:version>  # 切换到另一个平台版本
citeck diagnose --fix            # 健康检查，可选自动修复
citeck setup                     # 修改设置（管理员密码、TLS、邮件、资源……）
citeck edit <app>                # 以 kubectl edit 的方式编辑应用的定义
```

请注意，`citeck stop <app>` 会将应用**分离（detach）**：在你运行 `citeck start <app>` 之前，它会在重启和重新加载后依然保持停止状态。这也是在小内存主机上释放内存的方式——分离几个可选应用可以节省数 GB。

全局标志：`--format (text|json)` 便于脚本处理，`--yes/-y` 跳过确认，`-d/--detach` 立即返回而不等待。完整参考：`citeck --help` 或[命令参考](https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher_server/commands.html)。

## 你能获得什么

- **自愈运行时** — 存活探针会重启崩溃的服务，启动器还会记录它们崩溃的原因
- **备份与恢复** — 将所有卷导出为单个归档文件，并在本机或其他主机上导入
- **开箱即用的 HTTPS** — Let's Encrypt 自动续期（支持域名*和* IP 地址），或使用你自己的证书
- **实时状态与日志** — 在桌面应用或 CLI 中查看每个服务的资源使用情况和实时日志
- 支持 8 种语言的本地化，并提供 bash、zsh、fish 和 PowerShell 的 Shell 补全

## 版本

**Community** 版完全开源且免费，涵盖平台的核心功能。商业的 **Enterprise** 版增加了专业支持和额外功能；安装它需要由 Citeck 签发的许可证密钥。本启动器两个版本皆可安装。

## 安全模型

我们希望把话说在前面：**服务器模式的守护进程控制着 Docker，因此应把它的 API 视为等同于主机上的 root 权限**（例如 `citeck exec` 会在容器内执行命令）。正因如此，默认采用的是更安全的选项。

- **CLI** 通过仅限守护进程用户访问的 Unix 套接字（权限 0600）与守护进程通信。
- **服务器模式下，启动器自身的 Web UI 默认处于禁用状态** — 受支持的服务器界面是 CLI/TUI。当你启用它（在 `daemon.yml` 中设置 `server.webui.enabled: true`）后，守护进程还会监听一个 TCP 端口。绑定到 localhost 时，完整 API 仅受浏览器 CSRF 防护——这**不是**身份认证，任何能访问该端口的本地用户或进程都将获得完全控制权。请谨慎地主动启用，并且只在**单租户主机**上启用——该主机的所有本地用户都已被信任拥有 Docker/root 级访问权限。绑定到非 localhost 地址则要求 mTLS 客户端证书。
- **要弥补 localhost 上的这一缺口**，请启用 API 令牌认证：在 `daemon.yml` 中设置 `api_auth.enabled: true`。此后经由 TCP 的每个 `/api` 请求都需要携带 `Authorization: Bearer <token>`（或由 `GET /auth/session?token=…` 颁发的浏览器会话 Cookie）。令牌取自 `api_auth.token`，或在启动时自动生成到 `conf/api-token`（权限 0600）。运行 `citeck ui` 会打印并打开一个已认证的链接。静态 UI 资源保持公开，仅 API 受保护。Unix 套接字、桌面应用和 mTLS 客户端不受影响。

（本节讲的是*启动器*的管理 UI，而不是你安装完成后登录的 Citeck 平台 UI。）

## 文档

- **服务器模式：** [安装与配置](https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher_server.html)（`daemon.yml` / `namespace.yml`）以及[命令参考](https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher_server/commands.html)
- **桌面应用：** [桌面模式文档](https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher.html)
- **发布说明：** [CHANGELOG.md](../CHANGELOG.md)

## 开发

由 Go（守护进程 + CLI）和 React（内嵌 Web UI）构建；桌面应用通过 Wails webview 包装同一套 UI。前置条件、构建目标以及完整的本地检查关卡（`make check`）记录在 [AGENTS.md](../AGENTS.md) 中。

## 许可证与联系方式

Citeck Launcher 是基于 **LGPL-3.0** 许可证的开源软件——参见 [LICENSE](../LICENSE)。

如有疑问、需要 Enterprise 授权或希望咨询，请[联系 Citeck 团队](https://www.citeck.ru/contacts/)。
