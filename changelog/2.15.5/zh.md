## 修复
- 使用 Keycloak 26.6.2 或更高版本时，代理重新接受访问令牌：此前带有 `Authorization: Bearer` 请求头或 `PA` cookie 的请求（移动应用、集成）会被拒绝，而浏览器登录仍然正常。
- 使用 Keycloak 26.5 或更高版本时，可以重新在 Keycloak 管理控制台中保存 `ecos-app` realm 的设置；此前保存会失败，提示“Client Session Idle Timeout cannot be greater than Realm SSO Idle Timeout”。
- 更新时会重新创建一次 Keycloak 容器，使修正后的配置生效。
