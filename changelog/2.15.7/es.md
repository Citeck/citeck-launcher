## Correcciones
- Actualizar desde un lanzador 1.x antiguo ya no impide que el lanzador arranque: un espacio de nombres que guardaba sus usuarios como una sola línea «nombre:contraseña» se migra con esos nombres de usuario.
- La imagen de proxy definida en ese espacio de nombres se conserva en lugar de perderse.
- Un espacio de nombres procedente de un lanzador anterior a la 1.3.6 conserva su último bundle resuelto, así que arranca aunque ese bundle se haya movido después en el repositorio de bundles.
