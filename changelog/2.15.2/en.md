## Changes
- **Ports published without an address now listen on 127.0.0.1 only.** Before, such a port was open on every interface, and Docker bypasses the host firewall. Only the proxy is published on all interfaces (always in server mode; on the desktop when the namespace host is not local). To open a port to the network, name the address yourself: `'*:15432:5432'` for all interfaces, or a specific one. A running container keeps its old binding until it is recreated.
- A proxy `ports` edit saved by an earlier version names no address, so it now means 127.0.0.1 too — check it with `citeck edit proxy`.
- Links to RabbitMQ, Mailpit and PgAdmin open on this machine (127.0.0.1 / localhost).

## Fixes
- `citeck edit` accepts a port with an address (`127.0.0.1:15432:5432`); before, such a port kept the app from starting. A port the launcher cannot read is refused before anything is saved.
- An oversized edit, or a definition without an image, is refused instead of being saved cut short.
- New databases can be created again on PostgreSQL clusters initialized by an older image (a collation version mismatch after a glibc update blocked them). The postgres container is recreated once.
- Security: the proxy no longer lets anonymous requests for Alfresco and Share resources (`/alfresco/…`, `/share/res/…`) through as the guest user. The proxy container is recreated once.
