## 新功能
- **附加应用的平台变量。** `additionalApps` 条目的环境变量（以及命令 / init）值现在可以通过新的 `${VAR}` 占位符引用由平台管理的值，从而让配置驱动的服务无需在 workspace 配置中硬编码密钥即可与 ECOS 的认证和消息集成：`${JWT_SECRET}`、`${OIDC_SECRET}`、`${WEB_URL}`（命名空间的公开基础 URL）、`${RMQ_USER}` / `${RMQ_PASSWORD}`、`${KK_HOST}` 和 `${ADMIN_PASSWORD}`，此外还有已有的基础设施主机/端口变量。例如，`AUTH_JWTSECRET: "${JWT_SECRET}"` 会让服务使用与内置 webapps 相同的密钥来验证网关转发的 JWT。
