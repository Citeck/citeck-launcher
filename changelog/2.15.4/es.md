## Correcciones
- Si el contenedor de una aplicación no se detiene dentro de su tiempo de espera de parada (por ejemplo, un servicio Java que ignora la señal de parada), ahora se elimina a la fuerza y la aplicación pasa a «Detenido», en lugar de quedarse en «Error al detener» con registros que no se podían abrir.
- Una aplicación que usted detuvo y que quedó bloqueada en «Error al detener» ahora reintenta la parada por sí sola y pasa a «Detenido»; no se vuelve a iniciar.
