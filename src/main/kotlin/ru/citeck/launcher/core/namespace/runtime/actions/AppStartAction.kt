package ru.citeck.launcher.core.namespace.runtime.actions

import com.github.dockerjava.api.async.ResultCallbackTemplate
import com.github.dockerjava.api.command.CreateContainerCmd
import com.github.dockerjava.api.command.CreateContainerResponse
import com.github.dockerjava.api.exception.ConflictException
import com.github.dockerjava.api.exception.DockerException
import com.github.dockerjava.api.exception.NotFoundException
import com.github.dockerjava.api.model.*
import io.github.oshai.kotlinlogging.KotlinLogging
import org.apache.commons.lang3.exception.ExceptionUtils
import ru.citeck.launcher.core.actions.ActionContext
import ru.citeck.launcher.core.actions.ActionExecutor
import ru.citeck.launcher.core.actions.ActionParams
import ru.citeck.launcher.core.actions.ActionsService
import ru.citeck.launcher.core.appdef.ApplicationDef
import ru.citeck.launcher.core.appdef.ExecProbeDef
import ru.citeck.launcher.core.appdef.InitContainerDef
import ru.citeck.launcher.core.appdef.StartupCondition
import ru.citeck.launcher.core.namespace.NamespaceRef
import ru.citeck.launcher.core.namespace.init.ExecShell
import ru.citeck.launcher.core.namespace.runtime.AppRuntime
import ru.citeck.launcher.core.namespace.runtime.DockerImageNotFound
import ru.citeck.launcher.core.namespace.runtime.docker.DockerApi
import ru.citeck.launcher.core.namespace.runtime.docker.DockerConstants
import ru.citeck.launcher.core.namespace.runtime.docker.DockerLabels
import ru.citeck.launcher.core.utils.Digest
import ru.citeck.launcher.core.utils.MemoryUtils
import ru.citeck.launcher.core.utils.promise.Promise
import java.io.File
import java.net.HttpURLConnection
import java.net.URI
import java.time.Duration
import java.util.concurrent.CompletableFuture
import java.util.concurrent.TimeUnit

class AppStartAction(
    private val dockerApi: DockerApi
) : ActionExecutor<AppStartAction.Params, Unit> {

    companion object {
        private val log = KotlinLogging.logger {}

        fun execute(service: ActionsService, appRuntime: AppRuntime): Promise<Unit> {
            return service.execute(Params(appRuntime))
        }
    }

    override fun getName(context: ActionContext<Params>): String {
        return "run(${context.params.appRuntime.def.getValue().name})"
    }

    override fun execute(context: ActionContext<Params>) {

        val runtime = context.params.appRuntime
        val nsRuntime = runtime.nsRuntime

        val appDef = runtime.def.getValue()
        val namespaceRef = nsRuntime.namespaceRef
        val namespaceId = namespaceRef.namespace
        val workspaceId = namespaceRef.workspace

        val containers = dockerApi.getContainers(namespaceRef, appDef.name)

        if (appDef.image.isBlank()) {
            error("Application image is empty for ${appDef.name}")
        }

        val appHashDigest = Digest.sha256().update(appDef.getHash())
        fun updateAppHashByImage(image: String) {
            val imageInfo = dockerApi.inspectImageOrNull(image) ?: throw DockerImageNotFound(image)
            imageInfo.repoDigests?.forEach { appHashDigest.update(it) }
        }
        updateAppHashByImage(appDef.image)
        appDef.initContainers.forEach { updateAppHashByImage(it.image) }

        val deploymentHash = appHashDigest.toHex()

        val expectedReplicas = 1

        val validContainersNames = mutableSetOf<String>()
        for (container in containers) {
            val containerName = container.names[0].removePrefix("/")
            if (validContainersNames.size == expectedReplicas) {
                log.info {
                    "Remove unnecessary container with id ${container.id} " +
                        "and name $containerName"
                }
                dockerApi.stopAndRemoveContainer(container)
            } else if (container.labels[DockerLabels.APP_HASH] == deploymentHash && container.state == "running") {
                log.info { "[${appDef.name}] Container with id ${container.id} has actual hash" }
                validContainersNames.add(containerName)
            } else {
                log.info { "[${appDef.name}] Container with id ${container.id} has outdated hash. Stop and remove it" }
                dockerApi.stopAndRemoveContainer(container)
            }
        }

        var nameIdx = validContainersNames.size
        val containerBaseName = nsRuntime.namePrefix + appDef.name + nsRuntime.nameSuffix
        while (validContainersNames.size < expectedReplicas) {

            appDef.initContainers.forEach {
                runInitContainer(runtime, namespaceRef, appDef.name, it)
            }

            var containerName = containerBaseName
            while (!validContainersNames.add(containerName)) {
                containerName = containerBaseName + DockerConstants.NAME_DELIM + nameIdx++
            }
            val portBindings = appDef.ports.map {
                PortBinding.parse(it)
            }
            val createCmd = dockerApi.createContainerCmd(appDef.image)
                .withName(containerName)
                .withEnv(
                    appDef.environments.entries.map {
                        "${it.key}=${it.value}"
                    }
                )
                .withExposedPorts(portBindings.map { it.exposedPort })
                .withLabels(
                    mapOf(
                        DockerLabels.APP_NAME to appDef.name,
                        DockerLabels.APP_HASH to deploymentHash,
                        DockerLabels.NAMESPACE to namespaceId,
                        DockerLabels.WORKSPACE to workspaceId,
                        DockerLabels.LAUNCHER_LABEL_PAIR,
                        DockerLabels.DOCKER_COMPOSE_PROJECT to DockerConstants.getDockerProjectName(namespaceRef)
                    )
                ).withHostConfig(
                    run {
                        val config = HostConfig.newHostConfig()
                            .withRestartPolicy(RestartPolicy.unlessStoppedRestart())
                            .withPortBindings(portBindings)
                            .withNetworkMode(nsRuntime.networkName)
                            .withShmSize(MemoryUtils.parseMemAmountToBytes(appDef.shmSize))

                        if (appDef.volumes.isNotEmpty()) {
                            config.withBinds(appDef.volumes.mapNotNull { prepareVolume(runtime, it) })
                        }
                        val memoryLimit = appDef.resources?.limits?.memory ?: ""
                        if (memoryLimit.isNotEmpty()) {
                            val memory = MemoryUtils.parseMemAmountToBytes(memoryLimit)
                            config.withMemory(memory)
                                .withMemorySwap(memory)
                        }
                        config
                    }
                ).withHostName(appDef.name)

            if (appDef.cmd != null) {
                createCmd.withCmd(appDef.cmd)
            }
            val newContainerResp = execCreateContainer(appDef, createCmd)
            try {
                dockerApi.startContainer(newContainerResp.id)
            } catch (e: DockerException) {
                throw RuntimeException("Container starting failed for ${appDef.name}", e)
            }
            for (startupCondition in appDef.startupConditions) {
                waitStartup(dockerApi, containerName, newContainerResp.id, startupCondition)
            }
            appDef.initActions.forEach {
                if (it is ExecShell) {
                    val exec = dockerApi.execCreateCmd(newContainerResp.id)
                        .withCmd("/bin/sh", "-c", "exec " + it.command)
                        .withAttachStderr(true)
                        .withAttachStdout(true)
                        .exec()

                    val callback = FramesLogCallback(containerName)

                    val success = dockerApi
                        .execStartCmd(exec.id)
                        .exec(callback)
                        .awaitCompletion(10, TimeUnit.SECONDS)

                    if (!success) {
                        log.error { "[$containerName] Init command is not completed in 10 seconds" }
                    }
                } else {
                    error("Unsupported init action: ${it::class}")
                }
            }
        }
    }

    private fun execCreateContainer(
        appDef: ApplicationDef,
        createCmd: CreateContainerCmd
    ): CreateContainerResponse {
        val appName = appDef.name
        var iteration = 0
        var lastError: Throwable
        do {
            try {
                return createCmd.exec()
            } catch (createErr: DockerException) {
                lastError = createErr
                val rootCause = ExceptionUtils.getRootCause(createErr) ?: createErr
                log.warn {
                    "[$appName] Container creation failed: " +
                        "${createErr::class.simpleName} - ${createErr.message}"
                }
                if (iteration < 3) {
                    log.warn { "[$appName] Retrying in 2 seconds..." }
                    Thread.sleep(2000)
                    continue
                }
                log.warn { "[$appName] Retries didn't help. Attempting to fix the problem..." }
                try {
                    if (rootCause !is ConflictException) {
                        log.warn { "[$appName] Unknown root cause: ${rootCause::class}. Giving up." }
                        break
                    }
                    val containerName = createCmd.name ?: ""
                    log.warn {
                        "[$appName] Root cause is ConflictException – likely a container with the " +
                            "same name is already running. Attempting to stop and remove the existing container..."
                    }
                    val existingContainer = dockerApi.getContainerByNameOrNull(containerName)
                    if (existingContainer == null) {
                        log.warn {
                            "[$appName] Container not found by name '$containerName'. " +
                                "It might have already been removed."
                        }
                        continue
                    } else {
                        try {
                            dockerApi.stopAndRemoveContainer(existingContainer)
                        } catch (e: Throwable) {
                            log.error(e) { "[$appName] Failed to stop and remove container: ${existingContainer.id}" }
                        }
                    }
                } catch (fixError: Throwable) {
                    log.error(fixError) { "[$appName] Error occurred while trying to fix container creation failure" }
                }
            }
        } while (iteration++ < 5)
        throw RuntimeException("Container creation failed for $appName", lastError)
    }

    private fun runInitContainer(
        runtime: AppRuntime,
        namespaceRef: NamespaceRef,
        appName: String,
        initContainerDef: InitContainerDef
    ) {
        log.info { "Run init container '${initContainerDef.image}' for app '$appName'" }
        val startedAt = System.currentTimeMillis()
        val createCmd = dockerApi.createContainerCmd(initContainerDef.image)
            .withLabels(
                mapOf(
                    DockerLabels.WORKSPACE to namespaceRef.workspace,
                    DockerLabels.NAMESPACE to namespaceRef.namespace,
                    DockerLabels.LAUNCHER_LABEL_PAIR,
                    DockerLabels.DOCKER_COMPOSE_PROJECT to DockerConstants.getDockerProjectName(namespaceRef)
                )
            )
            .withEnv(
                initContainerDef.environments.entries.map {
                    "${it.key}=${it.value}"
                }
            ).withHostConfig(
                run {
                    val config = HostConfig.newHostConfig()
                        .withRestartPolicy(RestartPolicy.noRestart())

                    if (initContainerDef.volumes.isNotEmpty()) {
                        config.withBinds(
                            initContainerDef.volumes.mapNotNull {
                                prepareVolume(runtime, it)
                            }
                        )
                    }
                    val memory = MemoryUtils.parseMemAmountToBytes("100m")
                    config.withMemory(memory)
                        .withMemorySwap(memory)
                    config
                }
            )
        if (initContainerDef.cmd != null) {
            createCmd.withCmd(initContainerDef.cmd)
        }

        var containerId = ""
        try {
            containerId = createCmd.exec().id
            dockerApi.startContainer(containerId)
            val statusCode = dockerApi.waitContainer(containerId).awaitStatusCode(30, TimeUnit.SECONDS)
            if (statusCode != 0) {
                log.error {
                    "===== Init container completed with non-zero " +
                        "status code: $statusCode. Last 10_000 log messages: ====="
                }
                dockerApi.consumeLogs(containerId, 10_000, Duration.ofSeconds(10)) { msg -> log.warn { msg } }
                log.error { "===== End of init container logs =====" }
                throw RuntimeException("Init container completed with non-zero code")
            } else {
                val containerLog = ArrayList<String>()
                dockerApi.consumeLogs(containerId, 10, Duration.ofSeconds(10)) { containerLog.add(it) }
                var message = "Init container completed successfully " +
                    "in ${System.currentTimeMillis() - startedAt}ms."
                if (containerLog.isNotEmpty()) {
                    message += " Last 10 log messages:"
                    log.info { message }
                    containerLog.forEach { msg -> log.info { msg } }
                } else {
                    log.info { message }
                }
            }
        } catch (exception: Throwable) {
            val container = dockerApi.inspectContainerOrNull(containerId)
            if (container != null) {
                try {
                    dockerApi.stopAndRemoveContainer(container)
                } catch (e: Throwable) {
                    exception.addSuppressed(e)
                }
            }
            throw RuntimeException("Init container starting failed for $appName", exception)
        } finally {
            if (containerId.isNotBlank()) {
                dockerApi.removeContainer(containerId)
            }
        }
    }

    private fun waitStartup(
        dockerApi: DockerApi,
        containerName: String,
        containerId: String,
        startupCondition: StartupCondition?
    ) {

        Thread.sleep(2000)
        val runningWaitUntil = System.currentTimeMillis() + 240_000
        while (
            dockerApi.inspectContainer(containerId).state.running != true &&
            System.currentTimeMillis() < runningWaitUntil
        ) {
            Thread.sleep(1000)
        }

        startupCondition ?: return

        val probe = startupCondition.probe
        val waitStartedAt = System.currentTimeMillis()
        if (probe != null) {
            Thread.sleep(probe.initialDelaySeconds * 1000L)
            var iterations = 0
            while (++iterations < probe.failureThreshold) {
                try {
                    val probeRes = when {
                        probe.exec != null -> execProbeCheck(
                            dockerApi,
                            containerName,
                            containerId,
                            probe.exec,
                            probe.timeoutSeconds
                        )
                        probe.http != null -> httpProbeCheck(
                            dockerApi,
                            containerId,
                            probe.http.port,
                            probe.http.path,
                            probe.timeoutSeconds
                        )
                        else -> true
                    }
                    if (probeRes) {
                        break
                    }
                    Thread.sleep(probe.periodSeconds * 1000L)
                } catch (e: InterruptedException) {
                    Thread.currentThread().interrupt()
                    throw e
                } catch (e: Throwable) {
                    if (e is NotFoundException && e.message?.contains("No such container") == true) {
                        throw e
                    }
                    log.error { "Error while startup probe check: " + e.message }
                    Thread.sleep(probe.periodSeconds * 1000L)
                }
            }
            if (iterations == probe.failureThreshold) {
                error("[$containerName $containerId] Container is not ready after failure threshold")
            } else {
                val waitDuration = Duration.ofMillis(System.currentTimeMillis() - waitStartedAt)
                log.info {
                    "[$containerName $containerId] Startup waiting completed " +
                        "successfully after ${waitDuration.toSeconds()} seconds and $iterations iterations"
                }
            }
        } else if (startupCondition.log != null) {

            val pattern = startupCondition.log.pattern.toRegex()
            val logMessageFound = CompletableFuture<Unit>()
            dockerApi.watchLogs(containerId, 10_000) {
                if (it.matches(pattern)) {
                    logMessageFound.complete(Unit)
                }
            }.use {
                logMessageFound.get(startupCondition.log.timeoutSeconds.toLong(), TimeUnit.SECONDS)
            }
        }
    }

    private fun httpProbeCheck(
        dockerApi: DockerApi,
        containerId: String,
        port: Int,
        path: String,
        timeoutSeconds: Int
    ): Boolean {

        val containerInfo = dockerApi.inspectContainer(containerId)
        var targetPort = -1
        for ((exposedPort, binding) in containerInfo.networkSettings.ports.bindings) {
            val publishedPort = binding?.firstOrNull()?.hostPortSpec?.toInt() ?: -1
            if (publishedPort != -1 && exposedPort.port == port) {
                targetPort = publishedPort
                break
            }
        }
        if (targetPort == -1) {
            return false
        }
        val apiUri = URI("http://127.0.0.1:$targetPort$path")
        val httpConnection = apiUri.toURL().openConnection() as HttpURLConnection
        httpConnection.connectTimeout = timeoutSeconds * 1000
        httpConnection.readTimeout = 3000
        return try {
            httpConnection.requestMethod = "GET"
            httpConnection.responseCode == 200
        } catch (e: InterruptedException) {
            Thread.currentThread().interrupt()
            throw e
        } catch (e: Throwable) {
            false
        } finally {
            httpConnection.disconnect()
        }
    }

    private fun execProbeCheck(
        dockerApi: DockerApi,
        containerName: String,
        containerId: String,
        def: ExecProbeDef,
        timeoutSeconds: Int
    ): Boolean {

        val exec = dockerApi.execCreateCmd(containerId)
            .withCmd(*def.command.toTypedArray())
            .withAttachStderr(true)
            .withAttachStdout(true)
            .exec()

        val callback = FramesLogCallback(containerName, true)

        dockerApi
            .execStartCmd(exec.id)
            .exec(callback).awaitCompletion(timeoutSeconds.toLong(), TimeUnit.SECONDS)

        val healthRes = dockerApi.inspectExec(exec.id)

        return !healthRes.isRunning && healthRes.exitCodeLong == 0L
    }

    private fun prepareVolume(runtime: AppRuntime, volume: String): Bind? {

        val twoDotsIdx = volume.indexOf(':')
        if (twoDotsIdx < 1) {
            return null
        }

        var normalizedVolume = volume

        var srcName = volume.take(twoDotsIdx)
        if (srcName.startsWith("./")) {
            srcName = runtime.nsRuntime.runtimeFiles.resolveAbsPathInFilesDir(srcName.substring(2))
            normalizedVolume = srcName + volume.substring(twoDotsIdx)
        }

        if (srcName.contains(File.separator)) {
            return Bind.parse(normalizedVolume)
        }

        val volumeNameInNamespace = createVolumeIfNotExists(runtime, srcName)
        return Bind.parse(volume.replaceFirst(srcName, volumeNameInNamespace))
    }

    private fun createVolumeIfNotExists(runtime: AppRuntime, originalName: String): String {
        val nsRef = runtime.nsRuntime.namespaceRef
        val existingVolume = dockerApi.getVolumeByOriginalNameOrNull(nsRef, originalName)
        return if (existingVolume == null) {
            val volumeNameInNamespace = DockerConstants.getVolumeName(originalName, nsRef)
            log.info { "Create new volume '$volumeNameInNamespace'" }
            dockerApi.createVolume(runtime.nsRuntime.namespaceRef, originalName, volumeNameInNamespace)
            volumeNameInNamespace
        } else {
            log.info { "Volume $originalName -> ${existingVolume.name} already exists. Do nothing." }
            existingVolume.name
        }
    }

    class Params(
        val appRuntime: AppRuntime
    ) : ActionParams<Unit>

    private class FramesLogCallback(
        private val containerName: String,
        private val printLogsAsTrace: Boolean = false
    ) : ResultCallbackTemplate<FramesLogCallback, Frame>() {

        companion object {
            private val log = KotlinLogging.logger {}
        }

        override fun onNext(frame: Frame) {
            if (frame.payload == null || frame.payload.isEmpty()) {
                return
            }
            val payload = String(frame.payload).trimEnd('\n', ' ', '\t', '\r')
            if (printLogsAsTrace) {
                log.trace { "[$containerName] $payload" }
            } else {
                when (frame.streamType) {
                    StreamType.STDERR -> log.error { "[$containerName] $payload" }
                    else -> log.info { "[$containerName] $payload" }
                }
            }
        }
    }
}
