## 修正
- Keycloak 26.6.2 以降で、プロキシが再びアクセストークンを受け付けるようになりました。以前は `Authorization: Bearer` ヘッダーまたは `PA` cookie を使うリクエスト（モバイルアプリ、連携）が拒否されていました。ブラウザーでのログインは動作していました。
- Keycloak 26.5 以降で、Keycloak 管理コンソールから `ecos-app` realm の設定を再び保存できるようになりました。以前は「Client Session Idle Timeout cannot be greater than Realm SSO Idle Timeout」というエラーで保存に失敗していました。
- 更新時に Keycloak コンテナが一度再作成され、修正後の設定が反映されます。
