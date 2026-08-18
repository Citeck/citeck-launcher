## New features
- **Auto-update now works on macOS and Windows**, not only on Linux. The launcher downloads the new version, verifies its signature and installs it itself.

## Fixes
- **An interrupted update no longer leaves the launcher unable to start.** An update that was downloaded but never checked — because the app was closed or crashed in between — is now health-checked on the next start and rolled back automatically if it does not come up.
- **Alfresco and Solr are no longer given JVM options their Java 8 cannot parse.** An unrecognized option is not ignored: the JVM refuses to start at all.
- **Alfresco and Solr now size their memory from the container limit.** Solr's heap used to be exactly the size of its own container, which ends in the kernel killing it; Alfresco had no limit at all. Solr's limit is now 2560 MB and Alfresco's is 8 GB.
- **Services no longer claim their whole heap at startup.** `heapSize` sets the maximum only, so starting a large namespace takes memory as it is actually needed.

## Improvements
- The daemon log no longer repeats the JVM memory calculation for every service on every reload.
- Built with Go 1.26.6, which carries security fixes for the standard library.

## Upgrading
- Java containers are recreated once on the first start after the update.
