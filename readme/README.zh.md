![Citeck Launcher](https://github.com/Citeck/ecos-ui/raw/develop/public/img/logo/ecos-logo.svg)

# Citeck Launcher

[English](../README.md) · [Русский](README.ru.md) · **中文** · [Español](README.es.md) · [Deutsch](README.de.md) · [Français](README.fr.md) · [Português](README.pt.md) · [日本語](README.ja.md)

[![Release](https://img.shields.io/github/v/release/Citeck/citeck-launcher?sort=semver)](https://github.com/Citeck/citeck-launcher/releases/latest)
[![Downloads](https://img.shields.io/github/downloads/Citeck/citeck-launcher/total)](https://github.com/Citeck/citeck-launcher/releases)
[![License: LGPL v3](https://img.shields.io/badge/license-LGPL--3.0-blue.svg)](https://www.gnu.org/licenses/lgpl-3.0)
![Platforms](https://img.shields.io/badge/platform-linux%20%7C%20macOS%20%7C%20windows-lightgrey)
[![Documentation](https://img.shields.io/badge/docs-readthedocs-8CA1AF?logo=readthedocs)](https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher_server.html)

**一条命令，让整个 Citeck 平台运行起来。**

Citeck Launcher 是面向 **Citeck** 低代码 BPM/ECM 平台的自托管安装器与容器管理器。它是一个体积约 24 MB 的单一二进制文件，可作为命令行工具、后台守护进程以及跨平台桌面应用使用，将每个 Citeck 服务作为 Docker 容器运行，并将它们分组到相互隔离的命名空间中。

[Citeck](https://github.com/Citeck) 是一个用于企业内容管理（ECM）和业务流程管理（BPM）的开源低代码平台。该启动器可在单台主机上通过一条命令完成完整 Citeck 技术栈（Keycloak、PostgreSQL、RabbitMQ 以及 Citeck Web 应用）的安装、升级与运维。

> **完整文档：** https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher_server.html

## 快速开始

前置条件：Docker（已运行）。

```bash
curl -fsSL https://github.com/Citeck/citeck-launcher/releases/latest/download/install.sh | bash
```

安装脚本会为你的平台下载最新版本并安装到 `/usr/local/bin/`。向导会设置命名空间并启动平台。

> **重要：** `citeck install` 命令是一个**交互式 TUI 向导**，需要真实的终端。向导会在最后**仅一次**打印生成的管理员密码——请务必复制并保存，因为关闭该界面后将无法恢复。如果丢失，可通过 `citeck setup admin-password` 重置（参见[命令参考](https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher_server/commands.html)）。在最后的“写入配置”步骤之前按 `Ctrl+C` 会退出且不做任何更改；如果在更晚阶段中断，请检查 `/opt/citeck/conf/` 中的部分状态。
>
> 自动化／非交互式安装是一项未来功能——如有需要，请提交 issue。

要**升级**现有安装，运行同一条命令——脚本会检测已安装的版本，提示更新，停止守护进程，并替换二进制文件（备份保存在 `/usr/local/bin/citeck.bak`，可通过 `citeck install --rollback` 恢复）。

### 离线安装

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

## 桌面应用

Citeck Launcher 也提供面向 Windows、macOS 和 Linux 的**桌面应用**——
即同一守护进程和 Web UI 封装在原生窗口（Wails）中。该应用将守护进程作为
子进程进行监管，因此即使关闭窗口，你的容器仍会继续运行。桌面安装包随每个
[GitHub release](https://github.com/Citeck/citeck-launcher/releases) 一同提供；请下载
适用于你平台的版本：

| 操作系统 | 文件 | 架构 |
|----|------|------|
| Windows | `citeck-desktop_<version>_windows_<arch>.msi` | amd64, arm64 |
| macOS | `citeck-desktop_<version>_darwin_<arch>.dmg` | amd64 (Intel), arm64 (Apple Silicon) |
| Linux | `citeck-desktop_<version>_linux_<arch>.deb` / `.rpm` | amd64, arm64 |

每个安装包都附带一个用于校验的 `.sha256` 文件。升级过程中会保留你的数据。

## 功能特性

- **交互式安装器**，支持 TLS 自动检测（Let's Encrypt / 自签名 / 自定义证书）
- **i18n**，支持 8 种语言：英语、俄语、中文、西班牙语、德语、法语、葡萄牙语、日语
- **实时更新**，通过 SSE 事件（应用状态、资源使用情况）
- **卷快照**，支持导出/导入（ZIP + tar.xz）
- **Let's Encrypt** 集成，支持自动续期（域名和 IP 地址）
- **自愈运行时**，具备存活探针、重启跟踪以及重启前诊断
- **Shell 补全**，支持 bash、zsh、fish、PowerShell

## CLI 用法

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

## 配置

有关 `daemon.yml` 和 `namespace.yml` 的详细信息，请参阅[配置参考](https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher_server/configuration.html)。

## 许可证

参见 [LICENSE](../LICENSE)。
