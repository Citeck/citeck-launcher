## Novedades
- **Búsqueda semántica RAG.** Los bundles enterprise ofrecen ahora una app `rag` para la búsqueda semántica sobre su base de conocimiento, desactivada por defecto. Al iniciarla se levanta automáticamente una base de datos vectorial Qdrant y se activa el acceso a la base de conocimiento para el asistente de IA; una `rag` desactivada sigue sin consumir memoria extra —Qdrant ni siquiera se crea—. Los bundles community no se ven afectados: ni `rag` ni Qdrant aparecen en ellos.
- **Dependencias de webapp configurables.** El `dependsOn` de una webapp ahora se puede ampliar mediante `webapps[].defaultProps.dependsOn` del espacio de trabajo y `webapps.<id>.dependsOn` de un namespace: las dependencias configuradas se suman a las incorporadas, no las sustituyen. Un ciclo en `dependsOn` ahora hace fallar la generación con un error claro, en lugar de dejar las apps implicadas esperando para siempre.

## Cambios
- **Una dependencia detenida ya no deja que la app dependiente arranque sin ella.** Antes, una app cuya dependencia se había detenido manualmente arrancaba igualmente y luego fallaba sus comprobaciones de salud. Ahora espera, mostrando en su estado qué está esperando exactamente, y continúa en cuanto se inicia la dependencia.
- **Una imagen fijada por el bundle ya no se puede sobrescribir con la configuración del espacio de trabajo o del namespace.** El bundle gana siempre ahora; para usar otra imagen en un stand concreto, use `citeck edit <app>` explícitamente. Una configuración que aún así intente sobrescribir la imagen se registra como advertencia en lugar de aplicarse en silencio.

## Correcciones
- **Cambiar el estado de alfresco ahora regenera el namespace**, igual que ya ocurría con onlyoffice y ai desde la 1.4.1. Antes, tras detener/iniciar alfresco manualmente, el proxy seguía tratándolo como disponible (o no) en contra de la realidad, hasta que la siguiente recarga no relacionada lo corregía.
