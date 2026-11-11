## Improvements

- MCP agents with the Developer role can now edit the full application container configuration, including images, commands, mounts, probes, dependencies and public ports, with the same validation as the UI.
- MCP file editing now covers mounted application text files, including scripts, proxy configuration and nested files, instead of only Spring YAML.

## Fixes

- Fixed an HTTP/1 server crash caused by a race when handling oversized requests (Go 1.27.4, CVE-2026-97054).
