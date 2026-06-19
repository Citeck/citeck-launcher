## Novedades
- Edita la configuración del espacio de trabajo (workspace-v1.yml) directamente desde la pantalla de bienvenida: un engranaje junto al selector de espacio de trabajo abre un editor YAML con marcadores de cambios. Tus ediciones se guardan como un delta sobre la referencia de git y se vuelven a aplicar en cada sincronización.
- Elige exactamente qué volúmenes incluir al crear una instantánea.

## Correcciones
- Los estados en la app de escritorio ahora se actualizan en tiempo real en todas las plataformas; en Windows ya no aparecían congelados (por ejemplo, todos en «Cargando») durante un arranque largo hasta pulsar «Detener».
- En Windows, la ventana ya no se vuelve no clicable tras confirmar la contraseña maestra o cerrar un diálogo anidado.
- Los fallos al descargar imágenes ahora registran la causa en los registros (por ejemplo, una conexión al registro rechazada), para ver por qué no se completó la descarga.
- La lista de namespaces muestra estados localizados, y los namespaces eliminados desaparecen de la lista de inicio rápido.
- La pantalla de bienvenida es legible en el tema claro, y las casillas nativas se renderizan correctamente y alineadas en ambos temas.
- El encabezado del namespace se actualiza de forma fiable tras un cambio de nombre, y elegir un valor en un desplegable ya no desplaza el diseño.
- El instalador ahora inicia la aplicación automáticamente tras una instalación o actualización en Windows.
