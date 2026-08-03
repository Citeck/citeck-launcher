## Neu
- **Einstellungsseite.** Das Zahnrad öffnet jetzt eine echte Einstellungsseite mit den Git-Repository-Einstellungen des Arbeitsbereichs und den Zugangsdaten für private Docker-Registries — bisher nur über ein Hover-Menü oder einen Fehlerdialog erreichbar.

## Fehlerbehebungen
- **macOS: „citeck-launcher is damaged and can't be opened".** Heruntergeladene Builds trugen keine Signatur, was Gatekeeper mit dieser irreführenden Meldung quittiert, ohne Umgehungsmöglichkeit. Das App-Bundle wird wieder signiert, sodass beim ersten Start die übliche Warnung zum unbekannten Entwickler erscheint — öffnen Sie die App einmal per Rechtsklick → „Öffnen".
- **Das Upgrade von 1.x verliert den Arbeitsbereich nicht mehr.** Ließ sich die alte Datenbank nicht lesen, ersetzte der Launcher den Arbeitsbereich stillschweigend durch einen leeren. Repository-URL und Branch werden nun aus dem vorhandenen Klon wiederhergestellt, und eine Migration, die nicht alles übernehmen konnte, sagt das auch, statt wie eine Neuinstallation auszusehen.
- **Ein Netzwerkaussetzer tauscht kein Bundle-Repository mehr aus.** Ein kurzer Verbindungs-Timeout beim Git-Pull konnte ein Repository durch den Inhalt eines anderen ersetzen. Vorübergehende Netzwerkfehler behalten jetzt die vorhandene Kopie, und ein erneutes Klonen bricht ab, wenn es ein anderes Repository installieren würde.
- **Registry-Zugangsdaten lassen sich korrigieren.** Die falsche Wahl beim ersten Mal ließ keinen Weg zurück. Zuordnungen stehen jetzt in den Einstellungen, lassen sich neu vergeben oder entfernen, und ein Zugangsdatum eines anderen Hosts wird markiert statt gültig zu wirken.
- **Klarere Meldung bei unbekanntem Bundle-Repository.** Statt still den Standard-Arbeitsbereich von Citeck auszuliefern, nennt der Launcher das Repository, die deklarierten IDs und wo sich das beheben lässt.
- **Das Zahnrad verschwindet nicht mehr.** Ein Klick ohne geöffneten Namespace führte zurück zum Willkommensbildschirm und blendete das Symbol aus.
