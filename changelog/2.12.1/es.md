## Novedades
- **Un bundle o una configuración de espacio de trabajo puede declarar las imágenes de los componentes de terceros en una nueva sección `dependencies:`.** Los lanzadores anteriores a 2.12 no leen esa sección, así que subir la versión de PostgreSQL, RabbitMQ u otra pieza de infraestructura allí solo llega a los lanzadores capaces de proteger los datos existentes; el resto sigue ejecutando lo mismo que hoy. En la configuración del espacio de trabajo la sección además sube la versión para todos los namespaces de ese espacio a la vez.

## Correcciones
- **Una actualización de escritorio ya no se revierte solo porque Docker iba lento.** El demonio responde ahora en cuanto su proceso está vivo, y no al final de su arranque, de modo que la comprobación de salud juzga la versión nueva y no lo que tardó la máquina en arrancar. Reportado en Windows: una versión perfectamente correcta se instaló, se declaró «no arranca» tras 60 segundos esperando a un Docker Desktop bloqueado y se revirtió.
- **Una actualización fallida se puede volver a instalar.** Una versión que fallaba una vez quedaba rechazada para siempre; la ventana de actualización ofrece ahora **Reintentar**, mientras que el lanzador sigue sin reintentarla nunca por su cuenta.
- La ventana de actualización ya no afirma que estás en la última versión junto a una actualización fallida, ni ofrece reiniciar para «terminar de instalar» una actualización revertida.
- `citeck reload` sobre un namespace detenido ya no se queda esperando: recarga la configuración e indica que se aplicará en el próximo arranque. Ninguna espera de un namespace o del demonio puede quedarse colgada indefinidamente.

## Cambios
- pgAdmin elige su imagen igual que cualquier otra aplicación de terceros. En un equipo de escritorio donde tanto la configuración del espacio de trabajo como el bundle nombran una imagen de pgAdmin, gana la del bundle y el contenedor se recrea una vez.
- Una actualización fallida ahora explica su motivo en el registro, el demonio anota al arrancar qué versión está ejecutando y el volcado del sistema incluye el registro del propio lanzador y el manifiesto de actualizaciones.
