## Novedades
- **Diagnóstico de la JVM sin JDK en la imagen.** `citeck jstack <app>` para un volcado de hilos, `citeck jmap <app>` para un volcado de memoria descargado a su equipo y `citeck jcmd <app> <comando>` para todo lo demás que la propia JVM informa. Funciona mientras un servicio arranca o falla, no solo cuando está en marcha.
- **`citeck export ls|get|rm`.** Cada contenedor tiene ahora un directorio de salida para artefactos —volcados de memoria, volcados de base de datos, informes— y los archivos se listan, descargan y eliminan desde la CLI.
- **Las aplicaciones web de Citeck generan un volcado de memoria ante OutOfMemoryError de fábrica**, comprimido, en ese directorio. Solo se conserva el volcado más reciente, de modo que un ciclo de caídas no llene el disco.
- **Todos los grupos de memoria de la JVM se calculan ahora a partir del límite del contenedor**: heap, memoria directa, metaspace y caché de código. Antes solo el heap estaba acotado, así que una fuga en cualquier otro grupo agotaba el contenedor y el núcleo lo mataba sin error de Java ni volcado. Un heap fijado a mano se respeta tal cual y el resto se calcula alrededor; un contenedor demasiado pequeño para calcularlo con seguridad recibe topes solo donde no pueden romperlo.
- **Se captura un volcado de hilos antes de reiniciar un servicio que no supera su comprobación de salud**, de modo que la causa queda en el archivo de diagnóstico en lugar de desaparecer con el contenedor.

## Correcciones
- **Un servicio en una pausa larga del recolector de basura ya no se reinicia como muerto.** La comprobación tolera ahora unos 60 segundos de fallos continuos en lugar de unos 20.
- **El proxy se recrea junto con el gateway.** Tras un cambio de configuración que recreaba el gateway, nginx seguía enrutando a la dirección antigua y las páginas detrás del gateway devolvían 502 aunque todo pareciera correcto.
- **`citeck diff` ya no afirma que una recarga desactivará HTTPS** en espacios de nombres con certificado autofirmado o de Let's Encrypt.
- **Si las notas de la actualización no se cargan, ahora se informa** en lugar de mostrar un diálogo vacío.

## Actualización
- En el primer arranque tras la actualización cada contenedor se recrea una vez: cada servicio recibe el directorio de exportación y los nuevos ajustes de la JVM. En un espacio de nombres con 17 servicios esto llevó unos 5 minutos.
