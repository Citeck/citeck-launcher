![Citeck Launcher](https://github.com/Citeck/ecos-ui/raw/develop/public/img/logo/ecos-logo.svg)

# Citeck Launcher

[English](../README.md) · [Русский](README.ru.md) · [中文](README.zh.md) · [Español](README.es.md) · [Deutsch](README.de.md) · [Français](README.fr.md) · **Português** · [日本語](README.ja.md)

[![Release](https://img.shields.io/github/v/release/Citeck/citeck-launcher?sort=semver)](https://github.com/Citeck/citeck-launcher/releases/latest)
[![Downloads](https://img.shields.io/github/downloads/Citeck/citeck-launcher/total)](https://github.com/Citeck/citeck-launcher/releases)
[![License: LGPL v3](https://img.shields.io/badge/license-LGPL--3.0-blue.svg)](https://www.gnu.org/licenses/lgpl-3.0)
![Platforms](https://img.shields.io/badge/platform-linux%20%7C%20macOS%20%7C%20windows-lightgrey)
[![Documentation](https://img.shields.io/badge/docs-readthedocs-8CA1AF?logo=readthedocs)](https://citeck-ecos.readthedocs.io/en/latest/index.html)

**Instale e execute uma plataforma Citeck completa — como um aplicativo desktop no seu computador, ou com um único comando em um servidor.**

O Citeck Launcher é o instalador oficial e gerenciador de contêineres para a plataforma low-code BPM/ECM **Citeck**. Um único binário de aproximadamente 24 MB funciona como ferramenta de linha de comando, daemon em segundo plano e aplicativo desktop multiplataforma, executando cada serviço Citeck (Keycloak, PostgreSQL, RabbitMQ e os aplicativos web do Citeck) como um contêiner Docker e agrupando-os em namespaces isolados. Quais aplicativos e versões são executados é definido por um **bundle** (por exemplo, uma edição Community ou Enterprise).

[Citeck](https://github.com/Citeck) é uma plataforma de código aberto para a criação de aplicativos de negócio, combinando as abordagens **no-code, low-code e pro-code** para gerenciar conteúdo e processos. Na prática, você a utiliza para **gerenciar documentos e registros (ECM), automatizar processos de negócio e fluxos de aprovação com um designer BPMN integrado, e construir aplicativos internos — portais, CRM, gestão de casos — com pouco ou nenhum código**, com contas de usuário, papéis e permissões já incorporados. É uma alternativa auto-hospedada às suítes proprietárias de ECM/BPM, adequada para todos, de analistas de negócio a desenvolvedores.

A edição **Community** é totalmente de código aberto e gratuita — ela cobre as funcionalidades essenciais da plataforma e foi projetada para ser amigável a extensões de qualquer tipo. Para configurações mais exigentes, a edição comercial **Enterprise** adiciona suporte profissional e recursos corporativos extras. Este launcher instala qualquer uma das edições. Para dúvidas ou uma consultoria, [entre em contato com a equipe Citeck](https://www.citeck.ru/contacts/).

## Desktop ou servidor?

Há duas maneiras de executá-lo — escolha a que corresponde a **onde** você quer que o Citeck seja executado:

| | 🖥 **Aplicativo desktop** | 🖧 **Servidor (CLI)** |
|---|---|---|
| Para | Seu próprio computador | Um servidor / VM Linux (geralmente via SSH) |
| Instalação | Baixe um instalador, percorra o assistente | Um único comando `curl … \| bash` |
| Interface | Janela nativa do aplicativo (GUI) | Terminal — CLI `citeck` + assistente de configuração (TUI) |
| Comece aqui | [Aplicativo Desktop](#aplicativo-desktop) | [Instalação no servidor](#instalação-no-servidor) |

> **Atenção:** o início rápido `curl … | bash` e a CLI `citeck` neste README são para **instalações em servidor**. No seu próprio computador, execute o Citeck através do **aplicativo Desktop** — lá tudo é feito a partir da interface.

**Requisitos:** Docker; cerca de **16 GB** de RAM para a edição Community (**24–32 GB** para os ~24 serviços do Enterprise); e dezenas de GB de disco para as imagens.

## Aplicativo Desktop

O **aplicativo desktop** executa o Citeck na sua própria máquina Windows, macOS ou Linux — uma janela de aplicativo comum, sem linha de comando. O Citeck continua em execução em segundo plano mesmo depois de você fechar a janela.

Os instaladores desktop são anexados a cada [release do GitHub](https://github.com/Citeck/citeck-launcher/releases) — baixe aquele para sua plataforma:

| SO | Arquivo | Arquitetura |
|----|------|------|
| Windows | `citeck-desktop_<version>_windows_<arch>.msi` | amd64, arm64 |
| macOS | `citeck-desktop_<version>_darwin_<arch>.dmg` | amd64 (Intel), arm64 (Apple Silicon) |
| Linux | `citeck-desktop_<version>_linux_<arch>.deb` / `.rpm` | amd64, arm64 |

Cada instalador tem um arquivo `.sha256` auxiliar para verificação. Seus dados são preservados durante as atualizações.

## Instalação no servidor

> **Para um servidor ou VM Linux** — execute estas etapas no servidor, via SSH.

Pré-requisitos: um host Linux com o Docker em execução.

```bash
curl -fsSL https://github.com/Citeck/citeck-launcher/releases/latest/download/install.sh | bash
```

O script de instalação baixa a versão mais recente para sua plataforma e instala em `/usr/local/bin/`. O assistente então configura o namespace e inicia a plataforma.

> **Importante:** O comando `citeck install` é um **assistente TUI interativo** e requer um terminal real. O assistente exibe a senha de administrador gerada **uma única vez** ao final — copie-a e salve-a, pois ela não pode ser recuperada após fechar a tela. Se você a perder, redefina-a via `citeck setup admin-password` (consulte a [referência de comandos](https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher_server/commands.html)).

Para **atualizar** uma instalação de servidor existente, execute o mesmo one-liner — o script detecta a versão instalada, solicita a atualização, para o daemon e substitui o binário (um backup é mantido em `/usr/local/bin/citeck.bak`, restaurável via `citeck install --rollback`).

### Instalação Offline (servidor)

Para servidores sem acesso à internet, baixe previamente tanto o binário quanto o arquivo do workspace:

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

## Recursos

- **Instalador interativo** com detecção automática de TLS (Let's Encrypt / autoassinado / certificado personalizado)
- **i18n** com 8 idiomas: Inglês, Russo, Chinês, Espanhol, Alemão, Francês, Português, Japonês
- **Atualizações em tempo real** via eventos SSE (status de aplicativos, uso de recursos)
- **Snapshots de volumes** com exportação/importação (ZIP + tar.xz)
- **Integração com Let's Encrypt** com renovação automática (domínios e endereços IP)
- **Runtime de autorrecuperação** com liveness probes, rastreamento de reinícios e diagnósticos pré-reinício
- **Autocompletar de shell** para bash, zsh, fish, PowerShell

## Uso da CLI (modo servidor)

Estes comandos gerenciam uma instalação em **modo servidor** pela CLI. (No modo desktop as mesmas operações estão disponíveis a partir da interface do aplicativo.)

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

## Documentação

- **Modo servidor:** [Documentação do modo servidor do launcher](https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher_server.html) — instalação, configuração (`daemon.yml` / `namespace.yml`) e a [referência de comandos](https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher_server/commands.html).
- **Aplicativo desktop:** [Documentação do modo desktop](https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher.html).

## Licença

O Citeck Launcher é de código aberto sob a licença **LGPL-3.0** — consulte [LICENSE](LICENSE).
