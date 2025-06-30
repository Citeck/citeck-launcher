package ru.citeck.launcher.core.namespace.runtime

import com.github.dockerjava.api.model.Container
import com.github.dockerjava.api.model.PortBinding
import io.github.oshai.kotlinlogging.KotlinLogging
import ru.citeck.launcher.core.appdef.ApplicationDef
import ru.citeck.launcher.core.appdef.ApplicationKind
import ru.citeck.launcher.core.namespace.runtime.docker.DockerApi
import ru.citeck.launcher.core.utils.prop.MutProp
import ru.citeck.launcher.core.utils.promise.Promise
import ru.citeck.launcher.core.utils.promise.Promises
import java.net.BindException
import java.net.ServerSocket
import java.net.Socket
import java.net.SocketException
import java.util.*
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicInteger
import java.util.concurrent.atomic.AtomicLong
import kotlin.collections.ArrayList
import kotlin.collections.HashSet

class AppRuntime(
    val nsRuntime: NamespaceRuntime,
    def: ApplicationDef,
    private val dockerApi: DockerApi
) {
    companion object {
        private val log = KotlinLogging.logger {}

        private val connectionsIdCounter = AtomicLong()
    }

    val status = MutProp(AppRuntimeStatus.STOPPED)
    val statusText = MutProp("")
    @Volatile
    var activeActionPromise: Promise<*> = Promises.resolve(Unit)

    val def = MutProp(def)

    val name: String get() = def.value.name
    val image: String get() = def.value.image
    val editedDef = MutProp(false)

    @Volatile
    var containers: List<Container> = emptyList()
    private val portsBindings: Map<Int, HostPortBinding>

    var pullImageIfPresent = false
    var manualStop = false

    init {
        portsBindings = def.ports.mapNotNull { port ->
            val portInfo = PortBinding.parse(port)
            val hostPort = portInfo.binding.hostPortSpec?.toInt() ?: -1
            if (hostPort != -1) {
                HostPortBinding(
                    def.name,
                    hostPort,
                    portInfo.exposedPort.port
                )
            } else {
                null
            }
        }.associateBy { it.exposedPort }

        this.def.watch { before, after ->
            if (!status.value.isStoppingState()) {
                if (before.image != after.image) {
                    status.value = AppRuntimeStatus.READY_TO_PULL
                } else {
                    status.value = AppRuntimeStatus.READY_TO_START
                }
                activeActionPromise.cancel(true)
            }
        }
        status.watch { before, after ->
            if (after == AppRuntimeStatus.RUNNING) {
                this.containers = dockerApi.getContainers(nsRuntime.namespaceRef, def.name)
                if (nsRuntime.boundedNs.value) {
                    for (container in containers) {
                        for ((exposed, published) in dockerApi.getExposedPorts(container.id)) {
                            portsBindings[exposed]?.instancesPorts?.add(published)
                        }
                    }
                    portsBindings.values.forEach { it.start() }
                }
            } else if (after == AppRuntimeStatus.STOPPED) {
                this.containers = emptyList()
            }
            if (before == AppRuntimeStatus.READY_TO_PULL) {
                pullImageIfPresent = false
            }
            if (before == AppRuntimeStatus.RUNNING) {
                portsBindings.values.forEach { it.stop() }
            }
        }
    }

    fun start() {
        pullImageIfPresent = if (def.value.kind == ApplicationKind.THIRD_PARTY) {
            false
        } else {
            def.value.image.contains("snapshot", true)
        }
        manualStop = false
        status.value = AppRuntimeStatus.READY_TO_PULL
    }

    fun stop(manual: Boolean = false) {
        if (manual) {
            manualStop = true
        }
        status.value = AppRuntimeStatus.READY_TO_STOP
        activeActionPromise.cancel(true)
    }

    fun watchLogs(tail: Int, logsCallback: (String) -> Unit): AutoCloseable {
        return dockerApi.getContainers(nsRuntime.namespaceRef, name).firstOrNull()?.let {
            dockerApi.watchLogs(it.id, tail, logsCallback)
        } ?: return AutoCloseable { }
    }

    private inner class HostPortBinding(
        private val appName: String,
        val hostPort: Int,
        val exposedPort: Int
    ) {

        val instancesPorts = ArrayList<Int>()

        private val serverActive = AtomicBoolean(false)
        var serverThread: Thread? = null
        private var connectionThreads = Collections.newSetFromMap<Thread>(ConcurrentHashMap())

        private val connectionCounter = AtomicLong()
        private val activeConnections = AtomicInteger()

        fun start() {
            if (!serverActive.compareAndSet(false, true)) {
                return
            }
            val serverSocket = try {
                ServerSocket(hostPort)
            } catch (e: BindException) {
                val appsOccupiedPort = HashSet<String>()
                var msg = "Server socket bind failed for port $hostPort of app $appName. "
                msg += if (appsOccupiedPort.isEmpty()) {
                    "Port owners doesn't found in launcher apps"
                } else {
                    "Port occupied by $appsOccupiedPort."
                }
                throw RuntimeException(msg, e)
            }
            log.info { "[$appName:$hostPort] Server listening at ${serverSocket.localSocketAddress}" }
            serverThread = Thread.ofVirtual().start {
                try {
                    while (serverActive.get()) {
                        while (serverActive.get() && instancesPorts.isEmpty()) {
                            Thread.sleep(1000)
                        }
                        if (!serverActive.get()) {
                            break
                        }

                        val sourceSocket = serverSocket.accept()
                        val connectionId = connectionsIdCounter.getAndIncrement()

                        val targetPort =
                            instancesPorts[(connectionCounter.getAndIncrement() % instancesPorts.size).toInt()]

                        log.info {
                            "[$appName:$hostPort:$connectionId] New socket connection. Route it from " +
                                "localhost:$hostPort to $targetPort"
                        }

                        sourceSocket.keepAlive = true
                        sourceSocket.soTimeout = 0

                        var connectionThread: Thread? = null

                        connectionThread = Thread.ofVirtual().name("$appName-$hostPort-$connectionId").unstarted {

                            activeConnections.incrementAndGet()

                            var targetToSourceThread: Thread? = null
                            var sourceToTargetThread: Thread? = null

                            try {

                                Socket("localhost", targetPort).use { targetSocket ->

                                    targetSocket.keepAlive = true
                                    targetSocket.soTimeout = 0

                                    fun moveBytes(threadName: String, from: Socket, to: Socket): Thread {
                                        return Thread.ofVirtual().name(threadName).unstarted {
                                            from.getInputStream().use { fromInput ->
                                                to.getOutputStream().use { toOutput ->
                                                    val buffer = ByteArray(4048)
                                                    try {
                                                        var bytesCount = fromInput.read(buffer)
                                                        while (bytesCount != -1) {
                                                            toOutput.write(buffer, 0, bytesCount)
                                                            toOutput.flush()
                                                            bytesCount = fromInput.read(buffer)
                                                        }
                                                    } catch (e: SocketException) {
                                                        val message = e.message ?: ""
                                                        if (!message.contains("Closed by interrupt") && !message.contains("Socket closed")) {
                                                            log.error(e) { "Exception in thread $threadName" }
                                                        }
                                                    }
                                                    log.trace { "[$appName:$hostPort:$connectionId] Move bytes completed" }
                                                }
                                            }
                                        }
                                    }

                                    targetToSourceThread = moveBytes("targetToSource", targetSocket, sourceSocket)
                                    sourceToTargetThread = moveBytes("sourceToTarget", sourceSocket, targetSocket)

                                    targetToSourceThread.start()
                                    sourceToTargetThread.start()

                                    targetToSourceThread.join()
                                    sourceToTargetThread.join()
                                }
                            } catch (_: Throwable) {
                                targetToSourceThread?.interrupt()
                                sourceToTargetThread?.interrupt()
                            } finally {
                                log.trace { "[$appName:$hostPort:$connectionId] Close target socket in finally block" }
                                activeConnections.decrementAndGet()
                                connectionThreads.remove(connectionThread)
                                try {
                                    sourceSocket.close()
                                } catch (e: Throwable) {
                                    // do nothing
                                }
                            }
                        }
                        connectionThreads.add(connectionThread)
                        connectionThread.start()
                    }
                } catch (e: Throwable) {
                    if (e !is SocketException || !(e.message ?: "").contains("Closed by interrupt")) {
                        log.warn(e) { "[$appName:$hostPort] Exception while server socket processing" }
                    }
                    serverActive.set(false)
                } finally {
                    try {
                        serverSocket.close()
                    } catch (e: Throwable) {
                        // do nothing
                    }
                }
            }
        }

        fun stop() {
            log.info { "BINDING WAS STOPPED: $hostPort -> $exposedPort" }
            try {
                serverActive.set(false)
                serverThread?.interrupt()
                serverThread = null
                connectionThreads.forEach { it.interrupt() }
                val stoppingStartedAt = System.currentTimeMillis()
                while (activeConnections.get() > 0 && (System.currentTimeMillis() - stoppingStartedAt) < 10_000) {
                    Thread.sleep(1000)
                }
                if (activeConnections.get() > 0) {
                    log.error { "[$appName:$hostPort] Active connections is stuck: " + activeConnections.get() }
                }
            } catch (e: Throwable) {
                log.error(e) { "[$appName:$hostPort] Exception while server socket stopped" }
            }
            instancesPorts.clear()
        }
    }
}
