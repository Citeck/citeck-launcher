## Änderungen
- **Ohne Adresse veröffentlichte Ports lauschen jetzt nur noch auf 127.0.0.1.** Bisher war ein solcher Port auf allen Schnittstellen offen, und Docker umgeht die Firewall des Hosts. Nur der Proxy wird auf allen Schnittstellen veröffentlicht (im Servermodus immer; auf dem Desktop, wenn der Host des Namespace nicht lokal ist). Um einen Port ins Netz zu öffnen, geben Sie die Adresse selbst an: `'*:15432:5432'` für alle Schnittstellen oder eine bestimmte. Ein laufender Container behält seine alte Bindung, bis er neu erstellt wird.
- Eine von einer früheren Version gespeicherte `ports`-Änderung am Proxy nennt keine Adresse und bedeutet deshalb jetzt ebenfalls 127.0.0.1 – prüfen Sie sie mit `citeck edit proxy`.
- Links zu RabbitMQ, Mailpit und PgAdmin öffnen sich auf diesem Rechner (127.0.0.1 / localhost).

## Fehlerbehebungen
- `citeck edit` akzeptiert einen Port mit Adresse (`127.0.0.1:15432:5432`); bisher verhinderte ein solcher Port den Start der App. Ein Port, den der Launcher nicht lesen kann, wird abgelehnt, bevor etwas gespeichert wird.
- Eine zu große Änderung oder eine Definition ohne Image wird abgelehnt, statt gekürzt gespeichert zu werden.
- Auf PostgreSQL-Clustern, die mit einem älteren Image initialisiert wurden, lassen sich wieder neue Datenbanken anlegen (nach einem glibc-Update blockierte sie eine abweichende Collation-Version). Der postgres-Container wird einmal neu erstellt.
- Sicherheit: Der Proxy lässt anonyme Anfragen an Alfresco- und Share-Ressourcen (`/alfresco/…`, `/share/res/…`) nicht mehr als Benutzer guest durch. Der Proxy-Container wird einmal neu erstellt.
