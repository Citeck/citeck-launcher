package ru.citeck.launcher.cli.output

import com.fasterxml.jackson.module.kotlin.jacksonObjectMapper
import com.fasterxml.jackson.module.kotlin.readValue
import ru.citeck.launcher.api.dto.EventDto
import ru.citeck.launcher.cli.client.DaemonClient
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.util.concurrent.CountDownLatch

object EventPrinter {

    private val mapper = jacksonObjectMapper()
    private val timeFormatter = DateTimeFormatter.ofPattern("HH:mm:ss").withZone(ZoneId.systemDefault())

    fun watchEvents(client: DaemonClient, echo: (String) -> Unit) {
        val latch = CountDownLatch(1)

        Runtime.getRuntime().addShutdownHook(
            Thread {
                latch.countDown()
            }
        )

        client.watchEvents(
            onMessage = { json ->
                try {
                    val event = mapper.readValue<EventDto>(json)
                    val time = timeFormatter.format(Instant.ofEpochMilli(event.timestamp))
                    val label = event.appName.ifBlank { event.namespaceId }
                    echo("[$time] $label: ${event.before} -> ${event.after}")
                } catch (_: Throwable) {
                    // skip malformed events
                }
            },
            onClose = {
                echo("Connection closed")
                latch.countDown()
            }
        )

        latch.await()
    }
}
