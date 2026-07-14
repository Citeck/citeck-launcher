![Citeck Launcher](https://github.com/Citeck/ecos-ui/raw/develop/public/img/logo/ecos-logo.svg)

# Citeck Launcher

[English](../README.md) · [Русский](README.ru.md) · [中文](README.zh.md) · [Español](README.es.md) · [Deutsch](README.de.md) · [Français](README.fr.md) · [Português](README.pt.md) · **日本語**

[![Release](https://img.shields.io/github/v/release/Citeck/citeck-launcher?sort=semver)](https://github.com/Citeck/citeck-launcher/releases/latest)
[![Downloads](https://img.shields.io/github/downloads/Citeck/citeck-launcher/total)](https://github.com/Citeck/citeck-launcher/releases)
[![License: LGPL v3](https://img.shields.io/badge/license-LGPL--3.0-blue.svg)](https://www.gnu.org/licenses/lgpl-3.0)
![Platforms](https://img.shields.io/badge/platform-linux%20%7C%20macOS%20%7C%20windows-lightgrey)
[![Documentation](https://img.shields.io/badge/docs-readthedocs-8CA1AF?logo=readthedocs)](https://citeck-ecos.readthedocs.io/en/latest/index.html)

**Citeck を実行するための公式の方法です。**

🌐 [citeck.ru](https://www.citeck.ru) · ✉️ [お問い合わせ](https://www.citeck.ru/contacts/) · 💬 [Telegram コミュニティ](https://telegram.me/citeck)

[Citeck](https://github.com/Citeck) は、独自仕様の ECM/BPM スイートに代わるセルフホスト型のオープンソースのローコードプラットフォームです。企業文書に関わるほぼあらゆる業務に利用でき、契約書や購買の承認から、人事プロセス、電子アーカイブ、社内ポータルまで、その用途は多岐にわたります。各プロセスのルートは組み込みの BPMN デザイナーで描き、文書タイプはコードを書かずに設定できます。ユーザー、ロール、権限は標準で組み込まれています。

Citeck Launcher は、このプラットフォームを立ち上げ、その状態を保ち続けるための最も簡単な方法です。約 24 MB の単一バイナリをダウンロードするだけで、プラットフォームをインストールし、Docker を通じてサービスを起動します。その後は Launcher がサービスの正常性を監視し、停止したものを自動的に再起動します。また、プラットフォームのアップグレードもシンプルで予測可能なものにします。お使いのコンピューターではデスクトップアプリとして、サーバーではコマンドラインから動作します。

![Citeck Launcher dashboard](screenshots/running.png)

**必要なもの:** Docker · Community エディションで **16 GB** RAM、Enterprise エディション（約 24 サービス）で **24〜32 GB** · イメージとデータ用に **50 GB 以上**の空きディスク。Windows と macOS では、先に [Docker Desktop](https://www.docker.com/products/docker-desktop/) をインストールしてください。

## デスクトップとサーバーのどちらを選ぶか

実行方法は 2 通りあります。Citeck を**どこで**動かしたいかに合わせて選んでください:

| | 🖥 **デスクトップアプリ** | 🖧 **サーバー (CLI)** |
|---|---|---|
| 対象 | お使いのコンピューター | Linux サーバー / VM（通常は SSH 経由） |
| インストール | インストーラーをダウンロードし、ウィザードに従って進める | `curl … \| bash` の 1 コマンド |
| インターフェース | ネイティブのアプリウィンドウ（GUI） | ターミナル — `citeck` CLI + セットアップウィザード |
| ここから開始 | [デスクトップアプリ](#デスクトップアプリ) | [サーバーインストール](#サーバーインストール) |

> **注意:** この README にある `curl … | bash` のクイックスタートと `citeck` コマンドは**サーバーインストール**向けです。お使いのコンピューターでは、**デスクトップアプリ**から Citeck を実行してください。そこではすべての操作を UI から行えます。

## デスクトップアプリ

デスクトップアプリケーションは、お使いの Windows、macOS、Linux マシン上で Citeck を実行します。コマンドライン不要の、通常のアプリウィンドウです。ウィンドウを閉じても、Citeck はバックグラウンドで動き続けます。

先に Docker Desktop をインストールし、[最新リリース](https://github.com/Citeck/citeck-launcher/releases/latest)からお使いのプラットフォーム向けのインストーラーをダウンロードしてください:

| OS | ファイル | アーキテクチャ |
|----|------|------|
| Windows | `citeck-desktop_<version>_windows_<arch>.msi` | amd64, arm64 |
| macOS | `citeck-desktop_<version>_darwin_<arch>.dmg` | amd64 (Intel), arm64 (Apple Silicon) |
| Linux | `citeck-desktop_<version>_linux_<arch>.deb` / `.rpm` | amd64, arm64 |

各インストーラーには検証用の `.sha256` サイドカーが付属しています。アップグレード時にデータは保持されます。

## サーバーインストール

> **Linux サーバーまたは VM 向け**（amd64 または arm64） — これらの手順はサーバー上で、SSH 経由で実行してください。前提条件: Docker がインストールされ、稼働していること。

```bash
curl -fsSL https://github.com/Citeck/citeck-launcher/releases/latest/download/install.sh | bash
```

スクリプトはお使いのプラットフォーム向けの最新リリースをダウンロードし、`/usr/local/bin/citeck` にインストールしたうえで、セットアップウィザード（`citeck install`）を起動します。このウィザードは**対話型で、実際のターミナルが必要です**。ウィザードでは次の項目を尋ねられます:

- ブラウザーからプラットフォームにアクセスするために使う**ドメイン名または IP アドレス**
- **接続の保護方法** — 自動、Let's Encrypt、自己署名証明書、独自の証明書、またはプレーン HTTP。（Let's Encrypt には、このホストを指す公開 DNS 名と、受信ポート 80 が必要です。到達できない場合、ウィザードは自己署名証明書にフォールバックします。）
- **デモデータ**を配置するかどうか、および **systemd サービス**をインストールするかどうか

### 初回起動時に予想されること

**時間がかかりますが、それが正常です。** Launcher は数 GB の Docker イメージを取得し、その後プラットフォーム自体が起動するまでにおよそ **10〜15 分**かかります。サービスは依存関係の順に起動し、Keycloak は初回起動時にレルムをインポートします。アプリが 1 つずつ `RUNNING` に変わっていく様子を確認できます:

```bash
citeck status -w
```

すべてが起動すると、ウィザードがアクセス情報を表示します:

```
Citeck is ready!

Open in browser:  https://<the domain you entered>/
Login:            admin / <generated password>
```

この画面について知っておくべきことが 2 つあります:

- **管理者パスワードは一度しか表示されません。** 必ずコピーしてください — 後から復元することはできません。紛失した場合は `citeck setup admin-password` でリセットしてください。
- **自己署名証明書を使うと、ブラウザーが警告を表示します。** これは想定どおりの動作です。*詳細設定* → *アクセスする* をクリックしてください。

20 分ほど経っても先に進んでいないように見える場合は、まず `citeck diagnose`（`--fix` を付けると修復可能なものを自動で修復します）と `citeck logs <app>` から確認してください。

### Launcher のアップグレード

同じワンライナーをもう一度実行します。スクリプトはインストール済みのバージョンを検出し、更新を促し、デーモンを停止してバイナリを置き換えます。以前のバイナリは `/usr/local/bin/citeck.bak` に保持され、`citeck install --rollback` で復元できます。データは保持されます。

## 概念

CLI とドキュメント全体を通して登場する 3 つの用語です:

- **Namespace（名前空間）** — プラットフォームの分離された 1 つのインスタンス（専用のコンテナ、ボリューム、データを持ちます）。Linux や Kubernetes の名前空間とは関係のない、Launcher 固有の概念です。一般的なサーバーで実行するのはちょうど 1 つです。
- **Bundle（バンドル）** — どのアプリのどのバージョンでプラットフォームのリリースを構成するか（例: Community リリースや Enterprise リリース）。`citeck upgrade <bundle:version>` で切り替えます。
- **Workspace（ワークスペース）** — それらの定義の取得元（通常は Git リポジトリ、またはエアギャップ環境向けのオフライン `.zip`）。

## 日常的に使うコマンド（サーバーモード）

デスクトップモードでは、同じ操作をアプリの UI から行えます。

```bash
citeck status -w                 # 名前空間とすべてのアプリを監視する
citeck logs <app> -f             # ログをストリーミングする（アプリ未指定ならデーモン自身のログ）
citeck stop <app>                # アプリを停止し、再起動後も停止したままにする
citeck start <app>               # 再び起動する（再アタッチ）
citeck reload                    # 設定変更を適用し、変更されたものだけを再作成する
citeck snapshot export <name>    # 全ボリュームをバックアップする（プラットフォームを停止し、その後再起動する）
citeck upgrade <bundle:version>  # 別のプラットフォームバージョンに切り替える
citeck diagnose --fix            # ヘルスチェック（任意で自動修復）
citeck setup                     # 設定を変更する（管理者パスワード、TLS、メール、リソースなど）
citeck edit <app>                # kubectl edit スタイルでアプリの定義を編集する
```

`citeck stop <app>` はアプリを**デタッチ**する点に注意してください。`citeck start <app>` を実行するまで、再起動やリロードをまたいで停止したままになります。これは小規模なホストでメモリを解放する方法でもあります — 必須ではないアプリをいくつかデタッチすれば数 GB を節約できます。

グローバルフラグ: スクリプト向けの `--format (text|json)`、確認をスキップする `--yes/-y`、待たずにすぐ戻る `-d/--detach`。完全なリファレンスは `citeck --help` または[コマンドリファレンス](https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher_server/commands.html)を参照してください。

## 提供される機能

- **自己修復ランタイム** — 死活監視プローブがクラッシュしたサービスを再起動し、Launcher がクラッシュの原因を記録します
- **バックアップとリストア** — 全ボリュームを単一のアーカイブにエクスポートし、同じホストにも別のホストにもインポートできます
- **標準で HTTPS** — 自動更新付きの Let's Encrypt（ドメイン*および* IP アドレス）、または独自の証明書
- **ライブのステータスとログ** — デスクトップアプリでも CLI でも、すべてのサービスのリソース使用状況とストリーミングログを確認できます
- 8 言語にローカライズ済みで、bash、zsh、fish、PowerShell 向けのシェル補完付き

## エディション

**Community** エディションは完全なオープンソースかつ無償で、プラットフォームのコア機能を網羅しています。商用の **Enterprise** エディションはプロフェッショナルサポートと追加機能を提供し、インストールには Citeck が発行するライセンスキーが必要です。この Launcher はそのどちらのエディションもインストールできます。

## ドキュメント

- **サーバーモード:** [インストールと設定](https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher_server.html)（`daemon.yml` / `namespace.yml`）および[コマンドリファレンス](https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher_server/commands.html)
- **デスクトップアプリ:** [デスクトップモードのドキュメント](https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher.html)
- **リリースノート:** [CHANGELOG.md](../CHANGELOG.md)

## 開発

Go（デーモン + CLI）と React（埋め込みの Web UI）で構築されています。デスクトップアプリは同じ UI を Wails の WebView でラップしたものです。前提条件、ビルドターゲット、およびローカルの完全なチェックゲート（`make check`）については [AGENTS.md](../AGENTS.md) に記載しています。

## ライセンス

Citeck Launcher は **LGPL-3.0** ライセンスのオープンソースです — [LICENSE](../LICENSE) を参照してください。
