## Correcciones
- Los espacios de nombres migrados desde la versión 1.x ya no se corrompen en el primer arranque: se eliminó una reimportación automática de instantánea no deseada que podía dañar la base de datos. Importar una instantánea es ahora siempre una acción manual.
- Tras cambiar el espacio de nombres activo, la aplicación muestra los contenedores de ese espacio de nombres — el encabezado y los detalles de la aplicación ya no se contradicen.
- Al eliminar un espacio de nombres ahora también se eliminan sus volúmenes, red y contenedores de Docker, por lo que los datos sobrantes ya no se acumulan.
- El uso de CPU de los contenedores vuelve a mostrarse correctamente (se quedaba en 0%).
- El visor de registros ya no se congela tras reiniciar un contenedor — se reconecta automáticamente.
- El formulario de creación/edición de espacios de nombres está más ordenado: se ocultan los repositorios de paquetes sin versiones publicadas y se selecciona automáticamente la última versión al cambiar de repositorio.
- El título de la ventana de escritorio ahora refleja la versión en ejecución.
- Los registros del demonio se guardan en una sola carpeta «logs» (como en la versión 1.x) en lugar de dividirse tras una actualización.
