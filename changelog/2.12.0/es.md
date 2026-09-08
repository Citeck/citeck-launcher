## Novedades
- **Las versiones de la infraestructura ahora están ligadas a los datos.** Una actualización del bundle ya no puede llevar PostgreSQL, RabbitMQ, ZooKeeper, Keycloak o MongoDB a una versión incompatible con los datos que ya tiene un namespace. El launcher sigue ejecutando la versión con la que se crearon los datos e informa de la nueva — con un aviso y el nuevo diálogo **Dependencias** en la aplicación de escritorio, y con `citeck deps` en el servidor.
- **Migración de PostgreSQL 17 a 18**, desde el diálogo «Dependencias» o con `citeck deps upgrade postgres`. Se detiene el namespace, se vuelcan los datos, se crea un clúster nuevo en un volumen **nuevo**, se restauran y se verifican, y el namespace pasa a usarlo, con progreso paso a paso. Cualquier fallo se revierte automáticamente a los datos anteriores.
- El volumen de datos anterior **se conserva** tras una migración correcta (aparece en la página «Volúmenes»); elimínelo usted cuando esté conforme con el resultado.
- Si queda un volumen de destino de un intento anterior, el launcher muestra su tamaño y su versión y solo lo borra tras su confirmación.

## Cambios
- Los namespaces nuevos se crean con PostgreSQL 18; los existentes se quedan en PostgreSQL 17 hasta que los migre.
- Las actualizaciones de RabbitMQ, ZooKeeper, Keycloak y MongoDB se notifican pero aún no se realizan: esperan a una versión del launcher capaz de migrarlas.
- Cambiar la imagen de una dependencia a una versión incompatible con sus datos se rechaza, tanto en el editor de configuración como en `citeck edit`.
- Iniciar, recargar o eliminar un namespace se rechaza mientras se ejecuta una instantánea o una migración de dependencia, y el mensaje indica cuál de las dos.
