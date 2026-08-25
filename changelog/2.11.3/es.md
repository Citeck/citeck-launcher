## Correcciones
- **La actualización desde 1.x ya no resucita los namespaces que había eliminado.** Aparecían en la lista como identificadores sueltos, sin nombre ni bundle, y al abrirlos daban `namespace "..." not found`. Eliminar un namespace en 1.x dejaba parte de su estado, y el traslado convertía cada resto en una entrada propia. Si ya aparecen en su lista, bórrelos con el icono de papelera — no afecta a nada más.
- Los namespaces del antiguo workspace por defecto conservan tras el traslado el estado de las aplicaciones detenidas y el bundle recordado.
