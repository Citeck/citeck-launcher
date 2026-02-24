![Citeck Logo](https://github.com/Citeck/ecos-ui/raw/develop/public/img/logo/ecos-logo.svg)

# `Citeck Launcher`

Welcome to the Citeck Launcher repository!

## Download

Download the latest version from our [website](https://citeck.github.io/citeck-launcher/) or the [releases page](https://github.com/Citeck/citeck-launcher/releases).

## Dependencies

To run this application the following are needed:

* docker

## Development

To start the desktop application from source code:

```bash
./gradlew :app:run
```

### Building for production

Desktop application (GUI):

```bash
./gradlew :app:packageDist -PtargetOs=linux_x64
```

Values for `targetOs`: `linux_x64`, `linux_arm64`, `macos_x64`, `macos_arm64`, `windows_x64`, `windows_arm64`

Output:
- Linux: `app/build/compose/binaries/main/deb/`
- macOS: `app/build/compose/binaries/main/dmg/`
- Windows: `app/build/compose/binaries/main/msi/`

### Building CLI

CLI tool (headless daemon for servers). See [cli/README.md](cli/README.md) for
full documentation (commands, configuration, architecture, API).

```bash
./gradlew :cli:dist
```

Output:
- `cli/build/dist/citeck-cli-{version}-linux_x64.tar.gz`
- `cli/build/dist/citeck-install.sh`

## CLI (Server Mode)

Run Citeck on headless servers without a GUI.
Full documentation: **[cli/README.md](cli/README.md)**

Quick start:

```bash
curl -fsSL https://github.com/Citeck/citeck-launcher/releases/latest/download/citeck-install.sh | sudo bash -s install
```

Commands:

| Command | Description |
|---------|-------------|
| `sudo citeck install` | Interactive setup wizard |
| `sudo citeck uninstall` | Uninstall platform |
| `citeck start [--foreground]` | Start platform (auto-starts daemon if needed) |
| `citeck stop [--shutdown]` | Stop platform (`--shutdown` also stops daemon) |
| `citeck status [--watch] [--apps]` | Show status (`--watch` streams events) |
| `citeck reload` | Reload configuration from YAML files |

## Useful Links

- [Documentation](https://citeck-ecos.readthedocs.io/ru/latest/index.html) provides more in-depth information.

## Contributing

We welcome contributions from the community to make Citeck even better. Everyone interacting in the Citeck project's codebases, issue trackers, chat rooms, and forum is expected to follow the [contributor code of conduct](https://github.com/rubygems/rubygems/blob/master/CODE_OF_CONDUCT.md).

## Support

If you need any assistance or have any questions regarding Citeck Launcher, please create an issue in this repository or reach out to our [support team](mailto:support@citeck.ru).

## License

Citeck Launcher is released under the [GNU Lesser General Public License](LICENSE).
