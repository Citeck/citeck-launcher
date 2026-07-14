![Citeck Launcher](https://github.com/Citeck/ecos-ui/raw/develop/public/img/logo/ecos-logo.svg)

# Citeck Launcher

[English](../README.md) · [Русский](README.ru.md) · [中文](README.zh.md) · [Español](README.es.md) · [Deutsch](README.de.md) · [Français](README.fr.md) · **Português** · [日本語](README.ja.md)

[![Release](https://img.shields.io/github/v/release/Citeck/citeck-launcher?sort=semver)](https://github.com/Citeck/citeck-launcher/releases/latest)
[![Downloads](https://img.shields.io/github/downloads/Citeck/citeck-launcher/total)](https://github.com/Citeck/citeck-launcher/releases)
[![License: LGPL v3](https://img.shields.io/badge/license-LGPL--3.0-blue.svg)](https://www.gnu.org/licenses/lgpl-3.0)
![Platforms](https://img.shields.io/badge/platform-linux%20%7C%20macOS%20%7C%20windows-lightgrey)
[![Documentation](https://img.shields.io/badge/docs-readthedocs-8CA1AF?logo=readthedocs)](https://citeck-ecos.readthedocs.io/en/latest/index.html)

**A forma oficial de executar o Citeck.**

🌐 [citeck.ru](https://www.citeck.ru) · ✉️ [Fale conosco](https://www.citeck.ru/contacts/) · 💬 [Comunidade no Telegram](https://telegram.me/citeck)

[Citeck](https://github.com/Citeck) é uma plataforma low-code auto-hospedada e de código aberto que substitui as suítes proprietárias de ECM/BPM. Você a usa para quase qualquer tarefa que envolva documentos corporativos, da aprovação de contratos e compras a processos de RH, um arquivo eletrônico ou um portal corporativo. Você desenha a rota de cada processo no designer BPMN integrado e configura os tipos de documento sem código; usuários, papéis e permissões já vêm prontos para uso.

O Citeck Launcher é a maneira mais fácil de colocar a plataforma em funcionamento e mantê-la assim. Você baixa um único binário de ~24 MB — ele instala a plataforma e inicia seus serviços através do Docker. A partir daí, o Launcher monitora a saúde deles e reinicia automaticamente qualquer um que falhe, além de tornar a atualização da plataforma simples e previsível. No seu próprio computador, ele funciona como aplicativo desktop; em um servidor, pela linha de comando.

![Citeck Launcher dashboard](screenshots/running.png)

**Você vai precisar de:** Docker · **16 GB** de RAM para a edição Community, **24–32 GB** para a Enterprise (~24 serviços) · **mais de 50 GB** de disco livre para imagens e dados. No Windows e no macOS, instale primeiro o [Docker Desktop](https://www.docker.com/products/docker-desktop/).

## Desktop ou servidor?

Há duas maneiras de executá-lo — escolha a que corresponde a **onde** você quer que o Citeck seja executado:

| | 🖥 **Aplicativo desktop** | 🖧 **Servidor (CLI)** |
|---|---|---|
| Para | Seu próprio computador | Um servidor / VM Linux (geralmente via SSH) |
| Instalação | Baixe um instalador, percorra o assistente | Um único comando `curl … \| bash` |
| Interface | Janela nativa do aplicativo (GUI) | Terminal — CLI `citeck` + assistente de configuração |
| Comece aqui | [Aplicativo desktop](#aplicativo-desktop) | [Instalação no servidor](#instalação-no-servidor) |

> **Atenção:** o início rápido `curl … | bash` e os comandos `citeck` deste README são para **instalações em servidor**. No seu próprio computador, execute o Citeck através do **aplicativo desktop** — lá tudo é feito a partir da interface.

## Aplicativo desktop

O aplicativo desktop executa o Citeck na sua própria máquina Windows, macOS ou Linux — uma janela de aplicativo comum, sem linha de comando. O Citeck continua em execução em segundo plano mesmo depois de você fechar a janela.

Instale primeiro o Docker Desktop e depois baixe o instalador da sua plataforma na [release mais recente](https://github.com/Citeck/citeck-launcher/releases/latest):

| SO | Arquivo | Arquitetura |
|----|------|------|
| Windows | `citeck-desktop_<version>_windows_<arch>.msi` | amd64, arm64 |
| macOS | `citeck-desktop_<version>_darwin_<arch>.dmg` | amd64 (Intel), arm64 (Apple Silicon) |
| Linux | `citeck-desktop_<version>_linux_<arch>.deb` / `.rpm` | amd64, arm64 |

Cada instalador tem um arquivo `.sha256` auxiliar para verificação. Seus dados são preservados durante as atualizações.

## Instalação no servidor

> **Para um servidor ou VM Linux** (amd64 ou arm64) — execute estas etapas no servidor, via SSH. Pré-requisito: o Docker instalado e em execução.

```bash
curl -fsSL https://github.com/Citeck/citeck-launcher/releases/latest/download/install.sh | bash
```

O script baixa a versão mais recente para sua plataforma, a instala em `/usr/local/bin/citeck` e então inicia o assistente de configuração (`citeck install`). O assistente é **interativo e requer um terminal real**. Ele pergunta:

- o **nome de domínio ou IP** que você usará para acessar a plataforma no navegador;
- como **proteger a conexão** — automático, Let's Encrypt, um certificado autoassinado, seu próprio certificado ou HTTP puro. (O Let's Encrypt precisa de um nome DNS público apontando para este host e da porta 80 aberta para entrada; se não estiver acessível, o assistente recorre a um certificado autoassinado.)
- se deve implantar **dados de demonstração** e se deve instalar um **serviço systemd**.

### Primeira execução: o que esperar

**Demora um pouco — isso é normal.** O launcher baixa vários GB de imagens Docker e, depois, a própria plataforma precisa de cerca de **10 a 15 minutos** para subir: os serviços iniciam na ordem de dependência e o Keycloak importa seu realm na primeira execução. Acompanhe os aplicativos mudando para `RUNNING` um a um:

```bash
citeck status -w
```

Quando tudo estiver no ar, o assistente exibe seus dados de acesso:

```
Citeck is ready!

Open in browser:  https://<the domain you entered>/
Login:            admin / <generated password>
```

Duas coisas importantes sobre essa tela:

- **A senha de administrador é exibida uma única vez.** Copie-a — ela não pode ser recuperada depois. Se você a perder, redefina-a com `citeck setup admin-password`.
- **Com um certificado autoassinado, seu navegador exibirá um aviso.** Isso é esperado — clique em *Avançado* → *Continuar*.

Se algo continuar parecendo travado após uns 20 minutos, comece por `citeck diagnose` (adicione `--fix` para deixá-lo reparar o que for possível) e `citeck logs <app>`.

### Atualizando o launcher

Execute o mesmo one-liner novamente — o script detecta a versão instalada, pergunta se você deseja atualizar, para o daemon e substitui o binário. O binário anterior é mantido em `/usr/local/bin/citeck.bak` e pode ser restaurado com `citeck install --rollback`. Seus dados são preservados.

## Conceitos

Três palavras que aparecem por toda a CLI e a documentação:

- **Namespace** — uma instância isolada da plataforma (com seus próprios contêineres, volumes e dados). Nada a ver com os namespaces do Linux ou do Kubernetes; é um conceito do launcher. Um servidor típico executa exatamente um.
- **Bundle** — quais aplicativos e quais versões compõem uma release da plataforma, por exemplo uma release Community ou Enterprise. `citeck upgrade <bundle:version>` alterna entre elas.
- **Workspace** — de onde vêm essas definições (normalmente um repositório Git, ou um `.zip` offline para instalações sem acesso à rede).

## Comandos do dia a dia (modo servidor)

No modo desktop, as mesmas operações estão disponíveis na interface do aplicativo.

```bash
citeck status -w                 # acompanhe o namespace e cada aplicativo
citeck logs <app> -f             # transmita os logs (sem app = o log do próprio daemon)
citeck stop <app>                # pare um aplicativo — e mantenha-o parado entre reinícios
citeck start <app>               # inicie-o novamente (reanexar)
citeck reload                    # aplique mudanças de configuração, recriando só o que mudou
citeck snapshot export <name>    # faça backup de todos os volumes (para a plataforma e a reinicia)
citeck upgrade <bundle:version>  # mude para outra versão da plataforma
citeck diagnose --fix            # verificações de saúde com reparo automático opcional
citeck setup                     # altere configurações (senha de admin, TLS, e-mail, recursos…)
citeck edit <app>                # edite a definição de um aplicativo, no estilo kubectl edit
```

Note que `citeck stop <app>` **desanexa** o aplicativo: ele permanece parado entre reinícios e recargas até que você execute `citeck start <app>`. Essa também é a maneira de liberar memória em um host pequeno — desanexar alguns aplicativos opcionais economiza vários GB.

Flags globais: `--format (text|json)` para scripts, `--yes/-y` para pular confirmações, `-d/--detach` para retornar imediatamente em vez de esperar. Referência completa: `citeck --help` ou a [referência de comandos](https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher_server/commands.html).

## O que você ganha

- **Runtime de autorrecuperação** — liveness probes reiniciam serviços que caíram, e o launcher registra o motivo da queda
- **Backup e restauração** — exporte todos os volumes para um único arquivo e importe-os de volta neste host ou em outro
- **HTTPS pronto para uso** — Let's Encrypt com renovação automática (domínios *e* endereços IP), ou seu próprio certificado
- **Status e logs em tempo real** — uso de recursos e logs transmitidos ao vivo para cada serviço, no aplicativo desktop ou na CLI
- Localizado em 8 idiomas, com autocompletar de shell para bash, zsh, fish e PowerShell

## Edições

A edição **Community** é totalmente de código aberto e gratuita, e cobre as funcionalidades essenciais da plataforma. A edição comercial **Enterprise** adiciona suporte profissional e recursos extras; instalá-la exige uma chave de licença emitida pela Citeck. Este launcher instala qualquer uma das duas.

## Documentação

- **Modo servidor:** [instalação e configuração](https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher_server.html) (`daemon.yml` / `namespace.yml`) e a [referência de comandos](https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher_server/commands.html)
- **Aplicativo desktop:** [documentação do modo desktop](https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher.html)
- **Notas de release:** [CHANGELOG.md](../CHANGELOG.md)

## Desenvolvimento

Construído em Go (daemon + CLI) e React (Web UI embarcada); o aplicativo desktop encapsula a mesma interface em um webview Wails. Pré-requisitos, targets de build e o gate completo de verificação local (`make check`) estão documentados em [AGENTS.md](../AGENTS.md).

## Licença

O Citeck Launcher é de código aberto sob a licença **LGPL-3.0** — consulte [LICENSE](../LICENSE).
