## Fixes
- With Keycloak 26.6.2 or newer, the proxy accepts access tokens again: requests with an `Authorization: Bearer` header or the `PA` cookie (the mobile app, integrations) were refused while the browser login kept working.
- With Keycloak 26.5 or newer, the `ecos-app` realm settings can be saved in the Keycloak admin console again; saving failed with "Client Session Idle Timeout cannot be greater than Realm SSO Idle Timeout".
- Updating recreates the Keycloak container once, so the corrected configuration takes effect.
