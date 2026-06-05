![Citeck Launcher](https://github.com/Citeck/ecos-ui/raw/develop/public/img/logo/ecos-logo.svg)

# Citeck Launcher

[English](../README.md) · [Русский](README.ru.md) · [中文](README.zh.md) · **Español** · [Deutsch](README.de.md) · [Français](README.fr.md) · [Português](README.pt.md) · [日本語](README.ja.md)

[![Release](https://img.shields.io/github/v/release/Citeck/citeck-launcher?sort=semver)](https://github.com/Citeck/citeck-launcher/releases/latest)
[![Downloads](https://img.shields.io/github/downloads/Citeck/citeck-launcher/total)](https://github.com/Citeck/citeck-launcher/releases)
[![License: LGPL v3](https://img.shields.io/badge/license-LGPL--3.0-blue.svg)](https://www.gnu.org/licenses/lgpl-3.0)
![Platforms](https://img.shields.io/badge/platform-linux%20%7C%20macOS%20%7C%20windows-lightgrey)
[![Documentation](https://img.shields.io/badge/docs-readthedocs-8CA1AF?logo=readthedocs)](https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher_server.html)

**Instala y ejecuta una plataforma Citeck completa — como aplicación de escritorio en tu equipo, o con un solo comando en un servidor.**

Citeck Launcher es el instalador oficial y gestor de contenedores para la plataforma low-code de BPM/ECM **Citeck**. Un único binario de ~24 MB funciona como herramienta de línea de comandos, demonio en segundo plano y aplicación de escritorio multiplataforma, ejecutando cada servicio de Citeck (Keycloak, PostgreSQL, RabbitMQ y las aplicaciones web de Citeck) como un contenedor de Docker y agrupándolos en espacios de nombres aislados.

[Citeck](https://github.com/Citeck) es una plataforma low-code de código abierto para la gestión de contenido empresarial (Enterprise Content Management, ECM) y la gestión de procesos de negocio (Business Process Management, BPM).

## ¿Escritorio o servidor?

Hay dos maneras de ejecutarlo — elige la que se ajuste a **dónde** quieres que se ejecute Citeck:

| | 🖥 **Aplicación de escritorio** | 🖧 **Servidor (CLI)** |
|---|---|---|
| Para | Tu propio equipo | Un servidor / VM Linux (normalmente por SSH) |
| Instalación | Descarga un instalador y sigue el asistente | Un solo comando `curl … \| bash` |
| Interfaz web | Ventana nativa integrada | Servida sobre HTTPS (con TLS / Let's Encrypt) |
| Empieza aquí | [Aplicación de escritorio](#aplicación-de-escritorio) | [Instalación en servidor](#instalación-en-servidor) |

> **Aviso:** el inicio rápido con `curl … | bash` y la CLI `citeck` de este README son para **instalaciones en servidor**. En tu propio equipo, ejecuta Citeck a través de la **aplicación de escritorio** — allí todo se hace desde la interfaz.

Requiere Docker en cualquier caso.

## Aplicación de escritorio

La **aplicación de escritorio** ejecuta Citeck en tu propia máquina Windows, macOS o Linux — el mismo demonio e interfaz web envueltos en una ventana nativa (Wails). La aplicación supervisa el demonio como un proceso hijo, de modo que tus contenedores siguen ejecutándose incluso cuando la ventana está cerrada.

Los instaladores de escritorio se adjuntan a cada [versión de GitHub](https://github.com/Citeck/citeck-launcher/releases); descarga el correspondiente a tu plataforma:

| SO | Archivo | Arquitectura |
|----|------|------|
| Windows | `citeck-desktop_<version>_windows_<arch>.msi` | amd64, arm64 |
| macOS | `citeck-desktop_<version>_darwin_<arch>.dmg` | amd64 (Intel), arm64 (Apple Silicon) |
| Linux | `citeck-desktop_<version>_linux_<arch>.deb` / `.rpm` | amd64, arm64 |

Cada instalador tiene un archivo adjunto `.sha256` para su verificación. Tus datos se conservan durante las actualizaciones.

## Instalación en servidor

> **Para un servidor o VM Linux** (se ejecuta por SSH). En tu propio equipo, ejecuta Citeck a través de la [aplicación de escritorio](#aplicación-de-escritorio).

Requisitos previos: un host Linux con Docker en ejecución.

```bash
curl -fsSL https://github.com/Citeck/citeck-launcher/releases/latest/download/install.sh | bash
```

El script de instalación descarga la última versión para tu plataforma e instala en `/usr/local/bin/`. A continuación, el asistente configura el espacio de nombres e inicia la plataforma.

> **Importante:** El comando `citeck install` es un **asistente TUI interactivo** y requiere una terminal real. El asistente imprime la contraseña de administrador generada **una sola vez** al final — cópiala y guárdala, ya que no podrás recuperarla después de cerrar la pantalla. Si la pierdes, restablécela mediante `citeck setup admin-password` (consulta la [referencia de comandos](https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher_server/commands.html)). Pulsar `Ctrl+C` antes del paso final de "escribir configuración" sale sin realizar cambios; si se interrumpe más tarde, revisa `/opt/citeck/conf/` por si hubiera un estado parcial.
>
> La instalación automatizada / no interactiva es una función futura — por favor, abre una incidencia si la necesitas.

Para **actualizar** una instalación en servidor existente, ejecuta el mismo comando de una sola línea — el script detecta la versión instalada, solicita confirmación para actualizar, detiene el demonio y reemplaza el binario (se conserva una copia de seguridad en `/usr/local/bin/citeck.bak`, restaurable mediante `citeck install --rollback`).

### Instalación sin conexión (servidor)

Para servidores sin acceso a internet, descarga previamente tanto el binario como el archivo del espacio de trabajo:

1. **Binario:** desde la [página de versiones](https://github.com/Citeck/citeck-launcher/releases).
2. **Archivo del espacio de trabajo:** desde [Citeck/launcher-workspace](https://github.com/Citeck/launcher-workspace)
   (sección Releases, o el botón "Download ZIP"). Este archivo contiene las definiciones de
   los bundles que el launcher normalmente obtendría desde git.

Luego, en el servidor de destino:

```bash
citeck install --workspace /path/to/launcher-workspace.zip --offline
```

El flag `--workspace` extrae los repositorios de bundles localmente, por lo que no se necesita internet durante el arranque.
Para actualizar el espacio de trabajo más tarde desde un nuevo archivo sin reinstalar: `citeck update -f <zip>`.

## Características

- **Instalador interactivo** con detección automática de TLS (Let's Encrypt / autofirmado / certificado personalizado)
- **i18n** con 8 idiomas: inglés, ruso, chino, español, alemán, francés, portugués, japonés
- **Actualizaciones en tiempo real** mediante eventos SSE (estado de la aplicación, uso de recursos)
- **Instantáneas de volúmenes** con exportación/importación (ZIP + tar.xz)
- **Integración con Let's Encrypt** con renovación automática (dominios y direcciones IP)
- **Tiempo de ejecución autorreparable** con sondas de actividad, seguimiento de reinicios y diagnósticos previos al reinicio
- **Autocompletado de shell** para bash, zsh, fish, PowerShell

## Uso de la CLI (modo servidor)

Estos comandos gestionan una instalación en **modo servidor** a través de la CLI. (En modo escritorio, las mismas operaciones están disponibles desde la interfaz de la aplicación.)

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

Flags globales: `--format (text|json)`, `--yes/-y`.

## Documentación

- **Modo servidor:** [Documentación del modo servidor del launcher](https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher_server.html) — instalación, configuración (`daemon.yml` / `namespace.yml`) y la [referencia de comandos](https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher_server/commands.html).
- **Aplicación de escritorio:** autónoma — se configura mediante el propio asistente e interfaz de la aplicación; no se necesita configuración aparte.

## Licencia

Consulta [LICENSE](../LICENSE).
