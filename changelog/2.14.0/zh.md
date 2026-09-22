## 新功能
- **Observer —— 环境的可观测性。** 只要软件包中指定了 `observer` 镜像，启动器现在就会将该服务连同它自己的 PostgreSQL 一起启动：接收日志与 OTLP 遥测数据、在 17016 端口提供内置界面，并监控环境中的 RabbitMQ、PostgreSQL 与 ZooKeeper。`namespace.yml` 中的 `observer` 开关已被移除——环境是否具备可观测性由它所运行的发行版决定，而不是由环境自身的文件决定，旧的配置键会被直接忽略。Observer 的数据库成为一等的受管依赖：拥有自己的版本锁定、自己的卷代次，以及 `citeck deps upgrade observer-postgres` 和 `citeck deps rollback observer-postgres`。
- **随新软件包首次出现的应用不再自行启动**——前提是工作区模板在 `detachedApps` 中列出了它们。这类应用会以已停止的状态出现，并带有启动按钮；执行 `citeck start <app>` 即可永久启用。您已经在运行的应用不受该规则影响——已存在的容器即为证据；而当无法查询 Docker 或工作区仓库时，决定会推迟到下一次加载，而不是凭猜测作出。
- **额外的数据库在工作区配置中声明。** 新增的 `databases:` 小节（id、镜像、用户、端口、内存上限、服务器参数、`requiredBy`）意味着再增加一个 PostgreSQL 集群不再需要发布新版本的启动器，而且它能获得主数据库的全部能力：版本锁定、卷代次计数、大版本迁移与回滚。
- **依赖升级报告。** 每次 `citeck deps upgrade` 和 `citeck deps rollback`，以及每一次被拒绝的预检查，都会在 `logs/reports/` 中写入纯文本报告：是什么拦住了升级、它停在哪一步，以及临时容器的日志——这些日志是在回滚删除它们之前采集的。报告会包含在系统信息归档中（`citeck dump-system-info`），于是“它就是不更新”终于变成一个可以回答的问题。

## 变更
- 启动器不再自行分离应用。已停止应用的伴随组件（`rag` 的 `qdrant`、`ai` 的 `stt-sidecar`）现在会像其他应用一样随命名空间启动——若不想让环境为它付出资源，请用 `citeck stop <app>` 停止它，或把它列入模板的 `detachedApps`。

## 修复
- 系统信息归档不再遮蔽镜像版本。同一个容器在非机密字段中公开的值（例如数据库用户 `postgres`）不再被当作密码处理；在此之前，整个归档中的镜像标签都可能被替换成 `***REDACTED***`。
