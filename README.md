![Citeck ECOS Logo](https://github.com/Citeck/ecos-ui/raw/develop/public/img/logo/ecos-logo.svg)

# `Citeck Launcher`

Welcome to the Citeck Launcher repository!

## Download

Download the latest version from our [website](https://citeck.github.io/citeck-launcher/) or the [releases page](https://github.com/Citeck/citeck-launcher/releases).

## Dependencies

To run this application the following are needed:

* docker

## Development

To start application from source code, simply run:

```
./gradlew run
```

### Building for production

To build the application for production, run:

```
./gradlew packageDist -PtargetOs=linux_x64

targetOs may be:
- macos_x64
- macos_arm64
- linux_x64
- linux_arm64
- windows_x64
- windows_arm64

result will be located in:

windows - build/compose/binaries/main/msi/citeck-launcher_{version}_{targetOs}.msi
macos   - build/compose/binaries/main/dmg/citeck-launcher_{version}_{targetOs}.dmg
linux   - build/compose/binaries/main/deb/citeck-launcher_{version}_{targetOs}.deb
```

## Useful Links

- [Documentation](https://citeck-ecos.readthedocs.io/ru/latest/index.html) provides more in-depth information.

## Contributing

We welcome contributions from the community to make Citeck even better. Everyone interacting in the Citeck project’s codebases, issue trackers, chat rooms, and forum is expected to follow the [contributor code of conduct](https://github.com/rubygems/rubygems/blob/master/CODE_OF_CONDUCT.md).

## Support

If you need any assistance or have any questions regarding Citeck Launcher, please create an issue in this repository or reach out to our [support team](mailto:support@citeck.ru).

## License

Citeck Launcher is released under the [GNU Lesser General Public License](LICENSE).
