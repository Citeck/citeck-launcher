## 新功能
- 实验性（Beta）模块：发行版可以在 bundle 的 `experimental:` 部分以 Beta 形式提供新服务。在创建/编辑对话框的新「实验性功能」选项卡中按环境开启；这类应用在应用表和 `citeck status` 中标记为 BETA。
- 服务器模式：`citeck features` 列出环境 bundle 提供的 Beta 模块，`citeck features enable|disable <id>` 开启或关闭它们；`citeck install` 会询问要启用哪些。关闭的模块会在下次应用配置时移除，其数据卷会保留。
- 后续发行版将 Beta 模块转为正式模块后，它在已开启的环境中继续运行；在其他环境中则像任何新应用一样，按照 workspace 模板提供。
