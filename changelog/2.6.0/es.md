## Novedades
- Credenciales de registro reutilizables. Un acceso a un registro privado que guarde ahora se puede reutilizar entre namespaces y workspaces en lugar de volver a introducirlo en cada uno. Además, antes de iniciar un namespace el launcher comprueba que cada registro privado necesario tenga credenciales, de modo que un acceso ausente se detecta de entrada y no bloquea una descarga más tarde.

## Cambios
- El selector de bundle ya no tiene una entrada «LATEST» aparte: la versión más reciente se marca como «(LATEST)» y un namespace queda siempre fijado a una versión concreta, por lo que nunca cambia a una versión más nueva por su cuenta.
