## Novedades
- **Un bundle ahora puede exigir una versión mínima del launcher.** Crear o editar un namespace se rechaza si el bundle seleccionado necesita una versión del launcher más nueva que la instalada; el mensaje indica a qué versión actualizar. Un namespace que ya usa ese bundle no se ve afectado, y elegir «LATEST» siempre selecciona el bundle más reciente que este launcher puede ejecutar realmente.
- **El engranaje de ajustes del namespace ahora muestra un punto cuando hay un bundle más reciente disponible** en tu repositorio de bundles: verde si este launcher puede ejecutarlo, ámbar si antes hace falta un launcher más nuevo. Pasa el cursor sobre el icono para ver la versión y qué hacer.

## Correcciones
- **«Force Update» vuelve a leer realmente el repositorio del workspace.** Antes el botón informaba de éxito sin descargar nada durante una hora tras la sincronización anterior, de modo que una versión de bundle recién publicada no aparecía ni en la lista de versiones ni en el indicador «hay un bundle más nuevo».
- **El botón ↻ junto a la lista de bundles actualiza el repositorio que el launcher lee de verdad.** Cuando los repositorios de bundles están dentro del repositorio del workspace —la disposición habitual—, clonaba una segunda copia sin uso por repositorio e informaba de éxito aunque en pantalla no cambiara nada.
