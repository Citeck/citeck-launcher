## Fehlerbehebungen
- Mit Keycloak 26.6.2 oder neuer akzeptiert der Proxy wieder Access-Tokens: Anfragen mit dem Header `Authorization: Bearer` oder dem Cookie `PA` (die mobile App, Integrationen) wurden abgewiesen, während die Anmeldung im Browser weiter funktionierte.
- Mit Keycloak 26.5 oder neuer lassen sich die Einstellungen des Realms `ecos-app` in der Keycloak-Admin-Konsole wieder speichern; das Speichern schlug mit „Client Session Idle Timeout cannot be greater than Realm SSO Idle Timeout“ fehl.
- Das Update erstellt den Keycloak-Container einmal neu, damit die korrigierte Konfiguration wirkt.
