## Fehlerbehebungen
- Der KI-Dienst (Anrufaufzeichnung / Spracherkennung) ist jetzt über den Proxy erreichbar, und die Verknüpfung mit dem STT-Sidecar wird korrekt angewendet.
- Das Anbinden oder Abkoppeln von KI, dem STT-Sidecar oder OnlyOffice zur Laufzeit wirkt sich jetzt sofort aus — der Proxy und die KI-Verknüpfungen werden aktualisiert, ohne den Namespace neu zu erstellen.
- Externe Microservices, die außerhalb von Docker laufen, können den Cloud-Konfigurationsserver des Desktops wieder erreichen (für RabbitMQ-, ZooKeeper- und Datenbankadressen); zuvor lauschte er fälschlicherweise nur auf Loopback.
- Die Aufforderung „Registry-Anmeldedaten eingeben" erscheint nun zuverlässig wieder, wenn ein Image-Pull mit einem Authentifizierungsfehler fehlschlägt, selbst wenn die einmalige Benachrichtigung verpasst wurde.
- Das Löschen von Registry-Anmeldedaten hinterlässt keine verwaiste Verknüpfung „(nicht gefunden)" mehr für den Host.
- Das Fenster des Arbeitsbereich-Konfigurationseditors ist größer und besser lesbar, und „Auf Git zurücksetzen" wird jetzt mit einem Klick angewendet.
- CPU- und Speicherzellen behalten nach einer versehentlichen Auswahl keine hängende Hervorhebung mehr.
- Der Windows-Installer hängt nach einer Installation oder Aktualisierung nicht mehr bei „Abbrechen…", was andere Installer blockieren konnte.
