## 新功能
- **在 workspace 配置中定义附加应用。** 直接在 workspace 配置（`workspace-v1.yml` → `additionalApps:`）中声明任意自定义容器——模拟器、辅助服务或 sidecar——无需修改启动器。只需定义一次，附加应用便会分发到使用该 workspace 的所有命名空间，并在下次 reload 时生效，与内置的 `webapps` 完全一样。支持完整的容器配置项：镜像（包括 `<repoId>/path:tag` 这种通过镜像仓库解析的 bundle 写法）、带 `${VAR}` 模板的环境变量、命令、端口、卷、`dependsOn`、init 容器、init 动作、探针、资源、共享内存大小和停止超时。

## 变更
- 如果某个应用的 `dependsOn` 指向一个未部署的服务，现在该应用会（沿依赖链）从命名空间中被排除，而不再在缺少依赖的情况下启动。
- Web 应用不再依赖 Keycloak；只有代理和模型服务依赖它，且仅在启用 Keycloak 认证时。升级到此版本会将 Web 应用容器重新创建一次（其部署哈希发生变化）——无需任何操作。
