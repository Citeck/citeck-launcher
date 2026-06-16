## Novedades
- Amplíe o reduzca la interfaz con los controles de zoom nativos del webview.
- Reinicie una sola aplicación desde un menú contextual, disponible en cualquier estado de la aplicación.

## Correcciones
- La instalación del servidor con datos de demostración ahora importa realmente el snapshot de demostración: antes el namespace se creaba pero arrancaba con datos vacíos.
- La instalación del servidor ahora guarda el inicio de sesión del registro privado que introduce durante la configuración, en lugar de descartarlo silenciosamente.
- Mayor paridad con el launcher 1.x para configuraciones migradas o editadas a mano: usuarios de namespace por defecto (admin + fet), ajustes de bundle/workspace y valores por defecto de probe/log-startup.
- Ahora se le pide establecer una contraseña maestra antes de añadir su primer secreto, en lugar de mostrar un error.
- Quick Start fija la versión más reciente a una release concreta, actualiza las versiones disponibles tras desbloquear los secretos y abre el panel del namespace en cuanto se inicia.
- Los repositorios de workspace personalizados se recuperan automáticamente tras añadir o corregir su token de acceso, sin necesidad de reiniciar.
- Las credenciales de registro guardadas se asocian de inmediato a su host de registro.
- Los valores de los secretos se etiquetan claramente como Token o Password.
- Diálogo de snapshots: una sola cabecera compartida y una actualización automática al finalizar una exportación o importación.
- Panel de detalles de la aplicación con mayor contraste; el editor de configuración ahora sigue el tema de la aplicación.
- Texto de registro más legible y una indicación que explica cuándo «Eliminar todo» está deshabilitado.
- Al eliminar un workspace también se eliminan sus vínculos de credenciales de registro.
