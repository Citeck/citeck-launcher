![Citeck Launcher](https://github.com/Citeck/ecos-ui/raw/develop/public/img/logo/ecos-logo.svg)

# Citeck Launcher

[English](../README.md) · [Русский](README.ru.md) · [中文](README.zh.md) · [Español](README.es.md) · [Deutsch](README.de.md) · [Français](README.fr.md) · [Português](README.pt.md) · **日本語**

[![Release](https://img.shields.io/github/v/release/Citeck/citeck-launcher?sort=semver)](https://github.com/Citeck/citeck-launcher/releases/latest)
[![Downloads](https://img.shields.io/github/downloads/Citeck/citeck-launcher/total)](https://github.com/Citeck/citeck-launcher/releases)
[![License: LGPL v3](https://img.shields.io/badge/license-LGPL--3.0-blue.svg)](https://www.gnu.org/licenses/lgpl-3.0)
![Platforms](https://img.shields.io/badge/platform-linux%20%7C%20macOS%20%7C%20windows-lightgrey)
[![Documentation](https://img.shields.io/badge/docs-readthedocs-8CA1AF?logo=readthedocs)](https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher_server.html)

**Citeck プラットフォーム全体を、たった 1 つのコマンドで起動。**

Citeck Launcher は、**Citeck** ローコード BPM/ECM プラットフォーム向けのセルフホスト型インストーラーおよびコンテナマネージャーです。約 24 MB の単一バイナリで、コマンドラインツール、バックグラウンドデーモン、クロスプラットフォームのデスクトップアプリとして動作し、すべての Citeck サービスを Docker コンテナとして実行し、それらを分離された名前空間にグループ化します。

[Citeck](https://github.com/Citeck) は、Enterprise Content Management (ECM) および Business Process Management (BPM) のためのオープンソースのローコードプラットフォームです。Launcher は、完全な Citeck スタック（Keycloak、PostgreSQL、RabbitMQ、および Citeck ウェブアプリケーション）を 1 台のホスト上に 1 つのコマンドでインストール、アップグレード、運用します。

> **完全なドキュメント:** https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher_server.html

## クイックスタート

前提条件: Docker（実行中であること）。

```bash
curl -fsSL https://github.com/Citeck/citeck-launcher/releases/latest/download/install.sh | bash
```

インストールスクリプトは、お使いのプラットフォーム向けの最新リリースをダウンロードし、`/usr/local/bin/` にインストールします。ウィザードが名前空間をセットアップし、プラットフォームを起動します。

> **重要:** `citeck install` コマンドは**対話型の TUI ウィザード**であり、実際のターミナルが必要です。ウィザードは生成された管理者パスワードを最後に**一度だけ**表示します。画面を閉じた後は復元できないため、必ずコピーして保存してください。紛失した場合は、`citeck setup admin-password` でリセットしてください（[コマンドリファレンス](https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher_server/commands.html)を参照）。最後の「設定の書き込み」ステップの前に `Ctrl+C` を押すと、変更を加えずに終了します。それ以降に中断された場合は、`/opt/citeck/conf/` で部分的な状態を確認してください。
>
> 自動 / 非対話型インストールは今後の機能です。必要な場合は issue を起票してください。

既存のインストールを**アップグレード**するには、同じワンライナーを実行します。スクリプトはインストール済みのバージョンを検出し、更新を促し、デーモンを停止してバイナリを置き換えます（バックアップは `/usr/local/bin/citeck.bak` に保持され、`citeck install --rollback` で復元可能です）。

### オフラインインストール

インターネットにアクセスできないサーバーの場合は、バイナリとワークスペースアーカイブの両方を事前にダウンロードしてください:

1. **バイナリ:** [リリースページ](https://github.com/Citeck/citeck-launcher/releases)から。
2. **ワークスペースアーカイブ:** [Citeck/launcher-workspace](https://github.com/Citeck/launcher-workspace)
   から（Releases セクション、または「Download ZIP」ボタン）。このアーカイブには、Launcher が通常 git から取得するバンドル定義が含まれています。

次に、対象のサーバーで:

```bash
citeck install --workspace /path/to/launcher-workspace.zip --offline
```

`--workspace` フラグはバンドルリポジトリをローカルに展開するため、起動時にインターネットは不要です。
後で新しいアーカイブから再インストールせずにワークスペースを更新するには: `citeck update -f <zip>`。

## デスクトップアプリ

Citeck Launcher は、Windows、macOS、Linux 向けの**デスクトップアプリケーション**としても利用できます。
同じデーモンと Web UI をネイティブウィンドウ（Wails）でラップしたものです。アプリはデーモンを
子プロセスとして監視するため、ウィンドウを閉じてもコンテナは実行され続けます。デスクトップインストーラーは各
[GitHub release](https://github.com/Citeck/citeck-launcher/releases)に添付されています。お使いの
プラットフォーム向けのものをダウンロードしてください:

| OS | ファイル | アーキテクチャ |
|----|------|------|
| Windows | `citeck-desktop_<version>_windows_<arch>.msi` | amd64, arm64 |
| macOS | `citeck-desktop_<version>_darwin_<arch>.dmg` | amd64 (Intel), arm64 (Apple Silicon) |
| Linux | `citeck-desktop_<version>_linux_<arch>.deb` / `.rpm` | amd64, arm64 |

各インストーラーには検証用の `.sha256` サイドカーが付属しています。アップグレード時にデータは保持されます。

## 機能

- TLS 自動検出（Let's Encrypt / 自己署名 / カスタム証明書）を備えた**対話型インストーラー**
- 8 言語の **i18n**: 英語、ロシア語、中国語、スペイン語、ドイツ語、フランス語、ポルトガル語、日本語
- SSE イベントによる**リアルタイム更新**（アプリのステータス、リソース使用状況）
- エクスポート/インポート対応の**ボリュームスナップショット**（ZIP + tar.xz）
- 自動更新機能を備えた **Let's Encrypt** 統合（ドメインおよび IP アドレス）
- 死活監視プローブ、再起動の追跡、再起動前の診断機能を備えた**自己修復ランタイム**
- bash、zsh、fish、PowerShell 向けの**シェル補完**

## CLI の使い方

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

グローバルフラグ: `--format (text|json)`、`--yes/-y`。

## 設定

`daemon.yml` および `namespace.yml` の詳細については、[設定リファレンス](https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher_server/configuration.html)を参照してください。

## ライセンス

[LICENSE](../LICENSE) を参照してください。
