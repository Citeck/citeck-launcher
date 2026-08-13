## New features
- **JVM diagnostics without a JDK in the image.** `citeck jstack <app>` for a thread dump, `citeck jmap <app>` for a heap dump downloaded to your machine, `citeck jcmd <app> <command>` for anything else the JVM itself reports. It works while a service is starting or failing, not only while it is running.
- **`citeck export ls|get|rm`.** Every container now has an output directory for artifacts — heap dumps, database dumps, reports — and files are listed, downloaded and deleted from the CLI.
- **Citeck webapps dump the heap on OutOfMemoryError out of the box**, compressed, into that directory. Only the newest dump is kept, so a crash loop cannot fill the disk.
- **Every JVM memory pool is now sized from the container limit** — heap, direct memory, metaspace and code cache. Only the heap used to be bounded, so a leak in any other pool exhausted the container and the kernel killed it with no Java error and no dump. A heap you set by hand is kept as it is and the remaining pools are budgeted around it; a container too small to budget safely gets ceilings only where they cannot break it.
- **A thread dump is captured before a service is restarted for failing its health check**, so the reason ends up in the diagnostics file instead of disappearing with the container.

## Fixes
- **A service in a long garbage-collection pause is no longer restarted as dead.** The health check now tolerates about 60 seconds of continuous failure instead of about 20.
- **The proxy is recreated together with the gateway.** After a configuration change that recreated the gateway, nginx kept routing to the old address and pages behind the gateway returned 502 while everything looked healthy.
- **`citeck diff` no longer claims that a reload will turn HTTPS off** on namespaces with a self-signed or Let's Encrypt certificate.
- **Update notes that fail to load are now reported** instead of showing an empty dialog.

## Upgrading
- On the first start after the upgrade every container is recreated once: each service gains the export directory and the new JVM settings. On a 17-service namespace this took about 5 minutes.
