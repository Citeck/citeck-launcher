## Correcciones
- Una dependencia cuyo volumen de datos se eliminó (por ejemplo, desde la página de volúmenes) ya no se ofrece como actualización cuya migración luego falla: al no quedar datos que proteger, en el próximo arranque se aplica la versión del bundle.
- Cuando una migración requiere un launcher más reciente, `citeck deps` ahora indica volver a ejecutar `install.sh`; `citeck update` solo actualiza las definiciones del workspace y de los bundles.
- El aviso de escritorio antes de eliminar un namespace ahora indica que también se eliminan sus volúmenes de datos y su carpeta, incluidos los snapshots.
