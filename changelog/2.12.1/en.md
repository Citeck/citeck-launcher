## New features
- **A bundle or a workspace config can name its third-party images in a new `dependencies:` section.** Launchers older than 2.12 do not read that section, so raising PostgreSQL, RabbitMQ or any other infrastructure version there reaches only the launchers that know how to protect the existing data — everyone else keeps running what they run today. In a workspace config the section also raises a version for every namespace of that workspace at once.

## Fixes
- **A desktop update no longer rolls back just because Docker was slow.** The launcher's daemon now answers as soon as its process is alive instead of at the very end of its startup, so the update's health check judges the new version rather than how long the machine took to start. Reported on Windows: a perfectly good release was installed, declared "not starting" after 60 seconds of waiting on an unresponsive Docker Desktop, and rolled back.
- **An update that failed can be installed again.** A release that failed once used to be refused forever; the update window now offers **Try again**, while the launcher still never retries it on its own.
- The update window no longer claims you are on the latest version next to a failed update, and no longer offers to finish installing an update that was rolled back.
- `citeck reload` on a stopped namespace no longer waits forever: it reloads the configuration and says that it applies on the next start. No wait for a namespace or for the daemon can hang indefinitely any more.

## Changes
- pgAdmin now takes its image the same way every other third-party app does. On a desktop stand where both the workspace config and the bundle name a pgAdmin image, the bundle's wins and the container is recreated once.
- A failed update now says why in the log, the daemon records which version it is running at startup, and the system dump includes the launcher's own log and the update manifest.
