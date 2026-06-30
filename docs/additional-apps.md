# Additional apps — run a custom container by configuration alone

`namespace.yml` can declare **additional apps**: arbitrary containers that run in the
namespace alongside the built-in Citeck/infra apps, **without any change to the
launcher**. This is the config-driven way to add a new service (a mock/simulator, an
auxiliary Go service, a sidecar, …) instead of writing a dedicated generator.

Each entry maps to a generic `ApplicationDef` (image, env, ports, dependencies,
probes, …). Environment values support the same `${PG_HOST}` / `${ZK_HOST}` /
`${ZK_PORT}` / `${RMQ_HOST}` / `${MONGO_HOST}` / … template variables as the rest of
the config. In **server mode** the container is internal to the Docker network
(reachable by `name` / `networkAliases`) — only the proxy publishes ports.

## Schema (`additionalApps[]`)

| Field | Required | Notes |
|---|---|---|
| `name` | yes | Container/app name; unique; must not collide with a built-in app. |
| `image` | yes | Full image ref (`registry/repo:tag`, or a locally-present tag). |
| `enabled` | no | Default `true`; set `false` to keep the definition but not deploy. |
| `kind` | no | `CITECK_CORE` / `CITECK_CORE_EXTENSION` / `CITECK_ADDITIONAL` / `THIRD_PARTY` (default). |
| `networkAliases` | no | Extra DNS aliases on the namespace network. |
| `environments` | no | `KEY: value` map; `${VAR}` template variables are resolved. |
| `cmd` | no | Override the image command. |
| `ports` | no | `host:container` (published only in desktop mode). |
| `volumes` | no | Docker volume / bind mounts. |
| `dependsOn` | no | App names this container starts after (e.g. `zookeeper`). |
| `startupConditions` | no | Readiness gates (`probe` / `log`). |
| `livenessProbe` | no | HTTP/exec liveness probe. |
| `resources` | no | `limits.memory`. |
| `shmSize` | no | `/dev/shm` size. |

## Example — the EDI simulator (`citeck-edi-sim`)

The simulator is a plain Go container that self-registers in the ECOS ZooKeeper
Service Registry, so the platform discovers it by name. Adding it is pure config:

```yaml
# namespace.yml
additionalApps:
  - name: edi-sim
    image: registry.citeck.ru/community/citeck-edi-sim:0.1.0
    networkAliases: [ EcosEdiSimApp ]
    dependsOn: [ zookeeper ]
    environments:
      ZOOKEEPER_HOSTS: "${ZK_HOST}:${ZK_PORT}"   # → registers under /ecos/webapps
      DISCOVERY_APPNAME: edi-sim
      # AUTH_JWTSECRET: "<base64 HS512>"          # optional: admin-only in-platform
    livenessProbe:
      http: { path: /health, port: 8080 }
```

On `citeck reload` the launcher pulls the image, starts the container with those env
vars, and the simulator registers itself in ZooKeeper — the real `ecos-edi` (or any
service) in the same namespace then finds it by name with no fixed address.
