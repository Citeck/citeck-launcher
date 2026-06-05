![Citeck Launcher](https://github.com/Citeck/ecos-ui/raw/develop/public/img/logo/ecos-logo.svg)

# Citeck Launcher

[English](../README.md) · [Русский](README.ru.md) · **中文** · [Español](README.es.md) · [Deutsch](README.de.md) · [Français](README.fr.md) · [Português](README.pt.md) · [日本語](README.ja.md)

[![Release](https://img.shields.io/github/v/release/Citeck/citeck-launcher?sort=semver)](https://github.com/Citeck/citeck-launcher/releases/latest)
[![Downloads](https://img.shields.io/github/downloads/Citeck/citeck-launcher/total)](https://github.com/Citeck/citeck-launcher/releases)
[![License: LGPL v3](https://img.shields.io/badge/license-LGPL--3.0-blue.svg)](https://www.gnu.org/licenses/lgpl-3.0)
![Platforms](https://img.shields.io/badge/platform-linux%20%7C%20macOS%20%7C%20windows-lightgrey)
[![Documentation](https://img.shields.io/badge/docs-readthedocs-8CA1AF?logo=readthedocs)](https://citeck-ecos.readthedocs.io/en/latest/index.html)

**安装并运行完整的 Citeck 平台——既可作为电脑上的桌面应用，也可在服务器上通过一条命令完成。**

Citeck Launcher 是面向 **Citeck** 低代码 BPM/ECM 平台的官方安装器与容器管理器。一个体积约 24 MB 的单一二进制文件，可作为命令行工具、后台守护进程以及跨平台桌面应用使用——将每个 Citeck 服务（Keycloak、PostgreSQL、RabbitMQ 以及 Citeck Web 应用）作为 Docker 容器运行，并将它们分组到相互隔离的命名空间中。

[Citeck](https://github.com/Citeck) 是一个用于构建业务应用的开源平台，融合了 **无代码、低代码和专业代码** 三种方式来管理内容与流程。在实际使用中，你可以用它来 **管理文档与记录（ECM）、借助内置的 BPMN 设计器自动化业务流程与审批流程，并以极少代码甚至无需代码构建内部应用——门户、CRM、案件管理**，并内置了用户账户、角色和权限。它是专有 ECM/BPM 套件的自托管替代方案，适合从业务分析师到开发者的所有人。

**Community** 版完全开源且免费——它涵盖了平台的核心功能，并且在设计上对各类扩展都十分友好。对于要求更高的场景，商业的 **Enterprise** 版增加了专业支持和额外的企业级功能。本启动器两个版本皆可安装。如需咨询或了解更多信息，请[联系 Citeck 团队](https://www.citeck.ru/contacts/)。

## 桌面应用还是服务器？

有两种运行方式——根据你希望 Citeck **在哪里**运行来选择：

| | 🖥 **桌面应用** | 🖧 **服务器（CLI）** |
|---|---|---|
| 适用于 | 你自己的电脑 | Linux 服务器 / 虚拟机（通常通过 SSH） |
| 安装方式 | 下载安装包，按向导逐步操作 | 一条 `curl … \| bash` 命令 |
| 界面 | 原生应用窗口（GUI） | 终端 — `citeck` CLI + 设置向导（TUI） |
| 从这里开始 | [桌面应用](#桌面应用) | [服务器安装](#服务器安装) |

> **提示：** 本 README 中的 `curl … | bash` 快速开始以及 `citeck` CLI 仅适用于**服务器安装**。在你自己的电脑上，请通过**桌面应用**运行 Citeck——那里的一切操作都在 UI 中完成。

**前置要求：** Docker，以及一台性能尚可的机器——完整技术栈会运行多个容器（身份认证、数据库、消息代理以及 Citeck Web 应用）。Community 版大约需准备 **16 GB RAM**（Enterprise 版约 24 个服务则需 24–32 GB），以及数十 GB 的磁盘空间用于存放镜像。这是一个完整的平台，而非轻量级工具。

## 桌面应用

**桌面应用**在你自己的 Windows、macOS 或 Linux 电脑上运行 Citeck——就是一个普通的应用窗口，无需命令行。即使关闭窗口，Citeck 也会在后台继续运行。

桌面安装包随每个 [GitHub release](https://github.com/Citeck/citeck-launcher/releases) 一同提供——请下载适用于你平台的版本：

| 操作系统 | 文件 | 架构 |
|----|------|------|
| Windows | `citeck-desktop_<version>_windows_<arch>.msi` | amd64, arm64 |
| macOS | `citeck-desktop_<version>_darwin_<arch>.dmg` | amd64 (Intel), arm64 (Apple Silicon) |
| Linux | `citeck-desktop_<version>_linux_<arch>.deb` / `.rpm` | amd64, arm64 |

每个安装包都附带一个用于校验的 `.sha256` 文件。升级过程中会保留你的数据。

## 服务器安装

> **适用于 Linux 服务器或虚拟机** — 在服务器上通过 SSH 执行以下步骤。

前置条件：一台已运行 Docker 的 Linux 主机。

```bash
curl -fsSL https://github.com/Citeck/citeck-launcher/releases/latest/download/install.sh | bash
```

安装脚本会为你的平台下载最新版本并安装到 `/usr/local/bin/`。随后向导会设置命名空间并启动平台。

> **重要：** `citeck install` 是一个**交互式 TUI 向导**，需要真实的终端。向导会在最后**仅一次**打印生成的管理员密码——请务必复制并保存，因为关闭该界面后将无法恢复。如果丢失，可通过 `citeck setup admin-password` 重置（参见[命令参考](https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher_server/commands.html)）。

要**升级**现有服务器安装，运行同一条命令——脚本会检测已安装的版本，提示更新，停止守护进程，并替换二进制文件（备份保存在 `/usr/local/bin/citeck.bak`，可通过 `citeck install --rollback` 恢复）。

### 离线安装（服务器）

对于无法访问互联网的服务器，请预先下载二进制文件和工作区归档：

1. **二进制文件：** 从[发布页面](https://github.com/Citeck/citeck-launcher/releases)下载。
2. **工作区归档：** 从 [Citeck/launcher-workspace](https://github.com/Citeck/launcher-workspace)
   下载（Releases 区域，或“Download ZIP”按钮）。该归档包含启动器通常会从 git 获取的 bundle
   定义。

然后在目标服务器上：

```bash
citeck install --workspace /path/to/launcher-workspace.zip --offline
```

`--workspace` 标志会在本地解压 bundle 仓库，从而在启动过程中无需联网。
要在之后通过新归档更新工作区而无需重新安装：`citeck update -f <zip>`。

## 功能特性

- **交互式安装器**，支持 TLS 自动检测（Let's Encrypt / 自签名 / 自定义证书）
- **i18n**，支持 8 种语言：英语、俄语、中文、西班牙语、德语、法语、葡萄牙语、日语
- **实时更新**，通过 SSE 事件（应用状态、资源使用情况）
- **卷快照**，支持导出/导入（ZIP + tar.xz）
- **Let's Encrypt** 集成，支持自动续期（域名和 IP 地址）
- **自愈运行时**，具备存活探针、重启跟踪以及重启前诊断
- **Shell 补全**，支持 bash、zsh、fish、PowerShell

## CLI 用法（服务器模式）

这些命令通过 CLI 管理**服务器模式**安装。（在桌面模式下，同样的操作可在应用 UI 中完成。）

```
citeck install [--workspace <zip>]        Interactive setup wizard (offline with --workspace)
citeck start [app] [-d|--detach]          Start daemon/namespace (--detach = don't wait)
citeck stop [app...] [-d|--detach]        Stop namespace or app(s) (--detach = don't wait)
citeck restart [app] [-d|--detach]        Restart an app or the entire namespace (waits by default)
citeck reload [--dry-run] [-d|--detach]   Reload config and regenerate changed containers
citeck status [-w|--watch]                Show namespace status
citeck describe <app>                     Show container details (image, ports, env, volumes)
citeck health                             Health check (exit 0=healthy, 1=daemon down, 8=unhealthy)
citeck diagnose [--fix] [--dry-run]       Run diagnostics (with optional auto-fix)
citeck logs [app] [-f|--follow]           Stream logs (daemon if no app)
citeck exec <app> -- <command>            Execute command in container
citeck update [-f|--file <zip>]           Pull workspace/bundle defs (or import from ZIP)
citeck upgrade [bundle:version] [--yes]   Switch to a different bundle version
citeck snapshot list|export|import|delete Manage volume snapshots (auto stop/start)
citeck config view|validate|edit          Show, check, or edit namespace.yml
citeck setup [setting]                    Configure settings (TUI menu or by ID)
citeck setup history                      Show config change history
citeck clean [--force] [--volumes] [--images]  Clean orphaned resources / prune images
citeck dump-system-info [--full]          Collect diagnostics ZIP (status, logs, docker inspect, journalctl)
citeck version [--short]                  Show version info
citeck completion bash|zsh|fish           Generate shell completion
citeck uninstall [--delete-data]          Remove systemd service, binary, and (optionally) data
```

全局标志：`--format (text|json)`、`--yes/-y`。

## 文档

- **服务器模式：** [启动器服务器模式文档](https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher_server.html)——安装、配置（`daemon.yml` / `namespace.yml`）以及[命令参考](https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher_server/commands.html)。
- **桌面应用：** [桌面模式文档](https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher.html)。

## 许可证

Citeck Launcher 是基于 **LGPL-3.0** 许可证的开源软件——参见 [LICENSE](../LICENSE)。
