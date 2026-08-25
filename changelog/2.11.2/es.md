## Correcciones
- **La actualización desde 1.x ahora traslada todos sus workspaces y namespaces.** Solo se leía una entrada por página de almacenamiento, así que una instalación con 19 namespaces migraba uno — e informaba de que la migración había ido bien. Ahora se restauran los workspaces, los namespaces, los repositorios git, el estado de las aplicaciones detenidas y los archivos modificados de cada namespace. Esto corrige el traslado en sí; los datos perdidos en una actualización ya realizada no se recuperan automáticamente — su base de datos 1.x original sigue intacta en `storage.db.kotlin-bak`, escríbanos y podrá volver a importarse.
- **macOS: actualizar ya no bloquea el lanzador.** Tras pulsar «Actualizar», la ventana se quedaba en «Actualizando…» con todos los botones desactivados, incluido «Cancelar», y el lanzador quedaba inservible hasta reiniciarlo. Ahora la ventana se recarga sola en cuanto arranca la nueva versión, «Cancelar» funciona siempre y los botones ya no se desplazan al iniciar la actualización.

## Actualizaciones
- Mailpit 1.31.0, pgAdmin 9.17 y ZooKeeper 3.9.5.
