## 修复
- 修复了在 macOS 上即使 Docker Desktop 正在运行也显示“Docker 不可用”界面的问题。现在启动器会像 `docker` 命令行一样检测 Docker——通过当前活动的 Docker 上下文（Docker Desktop、colima、Rancher Desktop）——并在需要时回退到其他常见的套接字位置，而不再仅仅探测默认的套接字路径。
