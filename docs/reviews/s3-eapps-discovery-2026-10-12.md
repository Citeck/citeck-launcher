# Local S3 readiness blocked by eapps discovery

Read-only investigation on 2026-10-12, namespace `3q5h43y`, installed Launcher
UI version 2.16.2. SeaweedFS, content, eapps and gateway are RUNNING, but the
S3 connection state is `waiting`. Gateway records queries fail with HTTP 500:
`App is not available: 'eapps'`.

Two live eapps instances are registered in ZooKeeper:

| Instance | Address | Priority | Build date |
|---|---|---|---|
| Container `siixitx3ufp2` | 172.18.0.20:17023 | 0 | 2026-10-09T11:01:27.459668724Z |
| Local JVM `saomdqcnoyzs` | 192.168.0.236:8089 | 1 | 1970-01-01T00:00:00Z |

Both registrations have status UP and both HTTP health endpoints return 200.
The local JVM belongs to the user's development environment and was left untouched.

The gateway image is `sha256:eedba31d97e4bb7baeb69093075d699012a6522ab6f2559726dfa3cc3b4c4729`.
Its ecos-webapp-lib 3.33.0 `WebAppZkDiscoveryService.getInstances` selects the
maximum priority, but initializes the build-date filter from the first instance
before filtering by that priority. If the first, lower-priority instance has a
newer build date, the higher-priority instance cannot replace that date. The final
filter requires both maximum priority and that newer date, returning no instances.
`getActiveInstances` therefore returns an empty list, and the router reports the
application unavailable. The inspected 3.34.9 discovery bytecode from eapps is
identical; merely substituting that version is not a verified fix.

A standalone Java probe using the gateway's actual libraries reproduced this
against the existing ZooKeeper registrations, with registration disabled:

```text
RESULT discovery children=[siixitx3ufp2, saomdqcnoyzs]
RESULT active=[]
RESULT reversed active=[eapps:saomdqcnoyzs]
RESULT container-only active=[eapps:siixitx3ufp2]
```

The last two cases wrap only the probe's in-memory child list. No ZooKeeper
records, application state, configuration, containers or objects were changed.
This establishes an order-dependent selection defect, rather than an S3 storage
or credential failure. No upload/write-capacity test was performed in this run.

The local source fix in Solution member `ecos-webapp-commons` (baseline commit
`55b1734245759b46283b2270fe85ed20b88cab08`) chooses the maximum build date only
among instances with the selected priority. Regression tests cover both
enumeration orders, same-priority newest builds/ties, and own-app selection.
A clean Maven verify of ecos-webapp-lib passed all five selected discovery and
main-info tests and produced the library JAR. A standalone read-only probe with
only the freshly compiled discovery classes ahead of the installed gateway
libraries now selects the local eapps in either order; container-only selection
also remains correct. No installed JAR/image was replaced, and no release or
deployment was performed. The running gateway still needs an updated library. A single-instance workaround
works in the probe; stopping the user's local development JVM was not performed,
and an end-to-end workaround on the live platform remains unverified.

Evidence: Solution `.tmp/s3-readiness-oct12/ReadDiscovery.java`,
`read-discovery.log`, `gateway-discovery.txt`, `new-discovery.txt`, and `router.txt`.
Knowledge used: Launcher rev 4, gateway rev 3, web-api rev 3 and
infrastructure-local-dev rev 3; the task context also supplied pinned Launcher
troubleshooting/CLI/system/configuration documents at rev 2.

Fix verification: Solution `.tmp/discovery-priority-fix/green-build.log` and
`current-registration-proof.log`. The earlier Mockito harness attempts failed
before reaching assertions; the final tests use the existing ZooKeeper test
fixture and an in-memory cache proxy. The unchanged source fails two regression
tests; the fixed source passes. Test containers were disposed.

## Authorized local workaround verified

The user explicitly requested stopping the non-Docker local eapps on 2026-10-12.
The verified host JVM PID 694088 received SIGTERM and exited; port 8089 no longer
listens. Container eapps remains RUNNING. Launcher namespace `3q5h43y` then
reported S3 `connected` at 2026-10-12T06:04:16Z. No upload test was performed.
Other host Citeck service JVMs were absent; a Gradle Server development JVM
remained. The old gateway library defect is still present until an updated
library is deployed; removing the competing local instance is a verified
workaround on this namespace.
