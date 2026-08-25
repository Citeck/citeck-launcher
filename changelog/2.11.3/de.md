## Fehlerbehebungen
- **Beim Wechsel von 1.x kehren gelöschte Namespaces nicht mehr zurück.** Sie erschienen in der Liste als nackte IDs ohne Namen und ohne Bundle, und beim Öffnen kam `namespace "..." not found`. Das Löschen eines Namespace in 1.x ließ einen Teil seines Zustands zurück, und die Übernahme machte aus jedem Rest einen eigenen Eintrag. Wenn sie in Ihrer Liste bereits stehen, entfernen Sie sie über das Papierkorb-Symbol — sonst ist nichts betroffen.
- Namespaces im alten Standard-Workspace behalten nach der Übernahme den Zustand gestoppter Anwendungen und ihr gemerktes Bundle.
