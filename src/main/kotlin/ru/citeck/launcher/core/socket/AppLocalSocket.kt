package ru.citeck.launcher.core.socket

import com.fasterxml.jackson.annotation.JsonSubTypes
import com.fasterxml.jackson.annotation.JsonTypeInfo
import io.github.oshai.kotlinlogging.KotlinLogging
import ru.citeck.launcher.core.utils.json.Json
import java.net.ServerSocket
import java.net.Socket
import java.net.SocketException
import java.util.*
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.atomic.AtomicBoolean
import kotlin.reflect.KClass

object AppLocalSocket {

    private val log = KotlinLogging.logger {}

    private var serverSocket: ServerSocket? = null
    private var serverThread: Thread? = null

    private val listeners = ConcurrentHashMap<KClass<*>, MutableList<(Any) -> Unit>>()

    fun run(): Int {
        val socket = ServerSocket(0)
        serverSocket = socket
        val active = AtomicBoolean(true)
        serverThread = Thread.ofPlatform().name("AppLocalSocket").start {
            while (active.get()) {
                val acceptedSocket = try {
                    socket.accept()
                } catch (e: SocketException) {
                    // connection closed
                    break
                }
                log.info { "Accepted new connection from ${acceptedSocket.inetAddress}" }
                Thread.ofPlatform().start {
                    try {
                        acceptedSocket.keepAlive = true
                        val inputStream = acceptedSocket.getInputStream()
                        val outputStream = acceptedSocket.getOutputStream()
                        acceptedSocket.soTimeout = 5000
                        SocketUtils.readInt(inputStream) // version

                        while (true) {
                            acceptedSocket.soTimeout = 0
                            val messageSize = SocketUtils.readInt(inputStream)
                            if (messageSize > 200_000) {
                                error("Message size is too long: $messageSize")
                            }
                            log.info { "Message size - $messageSize bytes" }
                            val messageData = ByteArray(messageSize)

                            acceptedSocket.soTimeout = 5000
                            inputStream.read(messageData)
                            try {
                                val parsedMessage = Json.readJson(messageData)
                                val message = Json.convert(parsedMessage, LocalSocketCommand::class)
                                log.info { "Parsed message - $message" }
                                if (message is CloseConnection) {
                                    break
                                }
                                listeners[message::class]?.forEach { it.invoke(message) }
                            } catch (error: Throwable) {
                                throw RuntimeException(
                                    "Invalid message: " +
                                    Base64.getEncoder().encodeToString(messageData),
                                    error
                                )
                            }
                            val respData = Json.toBytes(Json.newObjectNode())
                            try {
                                SocketUtils.writeInt(outputStream, respData.size)
                                outputStream.write(respData)
                            } catch (error: Throwable) {
                                throw RuntimeException(
                                    "Response write failed: " +
                                        Base64.getEncoder().encodeToString(respData),
                                    error
                                )
                            }
                        }
                    } catch (e: Throwable) {
                        log.debug { "Message reading interrupted" }
                    } finally {
                        acceptedSocket.close()
                    }
                }
            }
        }
        Runtime.getRuntime().addShutdownHook(Thread {
            active.set(false)
            serverThread?.interrupt()
            serverSocket?.close()
        })
        return socket.localPort
    }

    fun <T : Any> sendCommand(port: Int, command: LocalSocketCommand, respType: KClass<T>): T {
        try {
            Socket("127.0.0.1", port).use { socket ->

                val output = socket.getOutputStream()
                val cmdBytes = Json.toBytes(command)

                SocketUtils.writeInt(output, 0) // api version
                SocketUtils.writeInt(output, cmdBytes.size) // message size
                output.write(cmdBytes)

                val inputStream = socket.getInputStream()
                val respSize = SocketUtils.readInt(inputStream)
                val respData = ByteArray(respSize)
                inputStream.read(respData)

                val response: T = if (respType == Unit::class) {
                    Json.readJson(respData)
                    @Suppress("UNCHECKED_CAST")
                    Unit as T
                } else {
                    Json.read(respData, respType)
                }
                val closeSocketCmd = Json.toBytes(CloseConnection)
                SocketUtils.writeInt(output, closeSocketCmd.size)
                output.write(closeSocketCmd)

                return response
            }
        } catch (e: Throwable) {
            throw RuntimeException("Command send failed. Port: $port Command: $command", e)
        }
    }

    fun <T : LocalSocketCommand> listenMessages(type: KClass<T>, action: (T) -> Unit) {
        @Suppress("UNCHECKED_CAST")
        listeners.computeIfAbsent(type as KClass<Any>) { CopyOnWriteArrayList() }
            .add(action as ((Any) -> Unit))
    }

    @JsonTypeInfo(
        use = JsonTypeInfo.Id.NAME,
        include = JsonTypeInfo.As.PROPERTY,
        property = "type"
    )
    @JsonSubTypes(
        JsonSubTypes.Type(value = TakeFocusCommand::class, name = "take-focus"),
        JsonSubTypes.Type(value = CloseConnection::class, name = "close")
    )
    sealed class LocalSocketCommand

    data object CloseConnection : LocalSocketCommand()

    data object TakeFocusCommand : LocalSocketCommand()
}
