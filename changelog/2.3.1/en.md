# 2.3.1
## Fixes

- Webapps now receive `ecos.webapp.properties.webUrl` from proxy
  config — email links and other absolute URLs honor the configured
  host/TLS instead of defaulting to `http://localhost`.
