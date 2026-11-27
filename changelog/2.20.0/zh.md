## 新功能

- Web 应用伴随服务配置（`webapps[].companions`）：仅当伴随服务（例如可选的 LLM 网关）存在于环境中时，工作区才会向 Web 应用注入环境变量和启动依赖，并保持命名空间覆盖配置优先。
- 附加服务使用主 PostgreSQL 数据库（`additionalApps[].postgresDatabases`）：自定义服务可在环境的主 PostgreSQL 集群中申请数据库，自动纳入迁移、快照和 pgAdmin，且开关功能时不会重建 PostgreSQL。
- 自定义侧边栏链接多语言提示（`links[].descriptions`）：工作区链接现支持按语言配置悬浮提示（兼容旧版本回退），并在切换界面语言时立即刷新。
- 新增侧边栏 LiteLLM 图标并更新了 Mailpit 图标。
