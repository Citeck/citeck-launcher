![Citeck Launcher](https://github.com/Citeck/ecos-ui/raw/develop/public/img/logo/ecos-logo.svg)

# Citeck Launcher

[English](../README.md) · [Русский](README.ru.md) · [中文](README.zh.md) · [Español](README.es.md) · [Deutsch](README.de.md) · [Français](README.fr.md) · **Português** · [日本語](README.ja.md)

[![Release](https://img.shields.io/github/v/release/Citeck/citeck-launcher?sort=semver)](https://github.com/Citeck/citeck-launcher/releases/latest)
[![Downloads](https://img.shields.io/github/downloads/Citeck/citeck-launcher/total)](https://github.com/Citeck/citeck-launcher/releases)
[![License: LGPL v3](https://img.shields.io/badge/license-LGPL--3.0-blue.svg)](https://www.gnu.org/licenses/lgpl-3.0)
![Platforms](https://img.shields.io/badge/platform-linux%20%7C%20macOS%20%7C%20windows-lightgrey)
[![Documentation](https://img.shields.io/badge/docs-readthedocs-8CA1AF?logo=readthedocs)](https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher_server.html)

**Toda a sua plataforma Citeck, no ar com um único comando.**

O Citeck Launcher é um instalador auto-hospedado e gerenciador de contêineres para a plataforma low-code BPM/ECM **Citeck**. É um único binário de aproximadamente 24 MB que funciona como ferramenta de linha de comando, daemon em segundo plano e aplicativo desktop multiplataforma, executando cada serviço Citeck como um contêiner Docker e agrupando-os em namespaces isolados.

[Citeck](https://github.com/Citeck) é uma plataforma low-code de código aberto para Gestão de Conteúdo Empresarial (ECM) e Gestão de Processos de Negócio (BPM). O launcher instala, atualiza e opera uma stack completa do Citeck — Keycloak, PostgreSQL, RabbitMQ e os aplicativos web do Citeck — em um único host com um comando.

> **Documentação completa:** https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher_server.html

## Início Rápido

Pré-requisitos: Docker (em execução).

```bash
curl -fsSL https://github.com/Citeck/citeck-launcher/releases/latest/download/install.sh | bash
```

O script de instalação baixa a versão mais recente para sua plataforma e instala em `/usr/local/bin/`. O assistente configura o namespace e inicia a plataforma.

> **Importante:** O comando `citeck install` é um **assistente TUI interativo** e requer um terminal real. O assistente exibe a senha de administrador gerada **uma única vez** ao final — certifique-se de copiá-la e salvá-la, pois você não conseguirá recuperá-la após fechar a tela. Se você a perder, redefina-a via `citeck setup admin-password` (consulte a [referência de comandos](https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher_server/commands.html)). Pressionar `Ctrl+C` antes da etapa final de "write configuration" sai sem fazer alterações; se interrompido posteriormente, verifique `/opt/citeck/conf/` para estado parcial.
>
> A instalação automatizada / não interativa é um recurso futuro — por favor, abra uma issue se você precisar dela.

Para **atualizar** uma instalação existente, execute o mesmo one-liner — o script detecta a versão instalada, solicita a atualização, para o daemon e substitui o binário (um backup é mantido em `/usr/local/bin/citeck.bak`, restaurável via `citeck install --rollback`).

### Instalação Offline

Para servidores sem acesso à internet, baixe previamente tanto o binário quanto o arquivo
do workspace:

1. **Binário:** da [página de releases](https://github.com/Citeck/citeck-launcher/releases).
2. **Arquivo do workspace:** de [Citeck/launcher-workspace](https://github.com/Citeck/launcher-workspace)
   (seção Releases, ou botão "Download ZIP"). Este arquivo contém as definições de bundle
   que o launcher normalmente buscaria no git.

Depois, no servidor de destino:

```bash
citeck install --workspace /path/to/launcher-workspace.zip --offline
```

A flag `--workspace` extrai os repositórios de bundle localmente, de modo que nenhuma internet é necessária durante a inicialização.
Para atualizar o workspace mais tarde a partir de um novo arquivo sem reinstalar: `citeck update -f <zip>`.

## Aplicativo Desktop

O Citeck Launcher também está disponível como **aplicativo desktop** para Windows, macOS e Linux —
o mesmo daemon e Web UI encapsulados em uma janela nativa (Wails). O aplicativo supervisiona o daemon como um
processo filho, de modo que seus contêineres continuam em execução mesmo quando a janela é fechada. Os instaladores desktop são anexados a cada
[release do GitHub](https://github.com/Citeck/citeck-launcher/releases); baixe aquele para sua
plataforma:

| SO | Arquivo | Arquitetura |
|----|------|------|
| Windows | `citeck-desktop_<version>_windows_<arch>.msi` | amd64, arm64 |
| macOS | `citeck-desktop_<version>_darwin_<arch>.dmg` | amd64 (Intel), arm64 (Apple Silicon) |
| Linux | `citeck-desktop_<version>_linux_<arch>.deb` / `.rpm` | amd64, arm64 |

Cada instalador tem um arquivo `.sha256` auxiliar para verificação. Seus dados são preservados durante as atualizações.

## Recursos

- **Instalador interativo** com detecção automática de TLS (Let's Encrypt / autoassinado / certificado personalizado)
- **i18n** com 8 idiomas: Inglês, Russo, Chinês, Espanhol, Alemão, Francês, Português, Japonês
- **Atualizações em tempo real** via eventos SSE (status de aplicativos, uso de recursos)
- **Snapshots de volumes** com exportação/importação (ZIP + tar.xz)
- **Integração com Let's Encrypt** com renovação automática (domínios e endereços IP)
- **Runtime de autorrecuperação** com liveness probes, rastreamento de reinícios e diagnósticos pré-reinício
- **Autocompletar de shell** para bash, zsh, fish, PowerShell

## Uso da CLI

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

Flags globais: `--format (text|json)`, `--yes/-y`.

## Configuração

Consulte a [referência de configuração](https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher_server/configuration.html) para detalhes sobre `daemon.yml` e `namespace.yml`.

## Licença

Consulte [LICENSE](../LICENSE).
