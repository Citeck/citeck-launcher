## Fixes
- Fixed the "Docker is not available" screen appearing on macOS even when Docker Desktop was running. The launcher now detects Docker the same way the `docker` CLI does — via the active Docker context (Docker Desktop, colima, Rancher Desktop) — and falls back to additional common socket locations instead of probing only the default socket path.
