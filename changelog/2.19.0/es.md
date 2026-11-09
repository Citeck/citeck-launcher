## Nuevas funciones

- Conecte agentes de IA locales mediante MCP para consultar el estado y los registros; el rol Developer también permite gestionar aplicaciones, editar parámetros de ejecución y configuración de Spring, y recopilar diagnósticos de JVM.
- Gestione el acceso de los agentes en los ajustes de la aplicación de escritorio: apruebe inicios de sesión OAuth o cree tokens manualmente, elija Observer o Developer, establezca la caducidad y revoque el acceso. MCP está desactivado por defecto y, al activarse, solo escucha en la interfaz de bucle local.
- Establezca límites de CPU por aplicación en núcleos, incluidos valores fraccionarios, desde el editor de configuración de la aplicación.

## Correcciones

- La recarga ahora informa de los errores al escribir archivos de configuración generados, en lugar de continuar con archivos no guardados.
- Los archivos de configuración editados ya no se sustituyen silenciosamente por contenido vacío cuando el archivo actual no está disponible y no se pueden volver a aplicar los cambios guardados.
- Las ediciones de configuración YAML conservan todos los documentos de los archivos con varios documentos.
- Los comandos de diagnóstico de JVM ya no se repiten automáticamente si es posible que ya hayan llegado a la JVM.
