## 新功能
- **RAG 语义搜索。** Enterprise bundle 现在提供 `rag` 应用，用于对知识库进行语义搜索，默认关闭。启动它会自动拉起 Qdrant 向量数据库，并为 AI 助手开启知识库访问；关闭状态下的 `rag` 依然不占用额外内存——Qdrant 甚至都不会被创建。Community bundle 不受影响，既不会出现 `rag`，也不会出现 Qdrant。
- **网页应用依赖可通过配置指定。** 网页应用的 `dependsOn` 现在可以通过 workspace 的 `webapps[].defaultProps.dependsOn` 和 namespace 的 `webapps.<id>.dependsOn` 扩展——配置的依赖会追加到内置依赖之上，而不是替换它们。`dependsOn` 出现循环依赖时，生成现在会以明确的错误失败，而不是让相关应用永远等待下去。

## 变更
- **依赖被停止后，依赖它的应用不再绕过它启动。** 以前，某个应用的依赖被手动停止后，该应用仍会启动，随后在健康检查中失败。现在它会等待，并在状态中显示正在等待什么；依赖启动后即可继续。
- **bundle 中固定的镜像不再能被 workspace 或 namespace 配置覆盖。** 现在 bundle 始终优先；如需在某个 stand 上使用不同镜像，请显式使用 `citeck edit <app>`。仍试图通过配置覆盖镜像的情况，现在会记录为警告，而不是悄悄生效。

## 修复
- **切换 alfresco 现在会重新生成 namespace**，与 onlyoffice 和 ai 自 1.4.1 起的行为一致。以前手动停止/启动 alfresco 后，代理会继续按与事实不符的状态（可用或不可用）处理它，直到下一次无关的 reload 才会更正。
