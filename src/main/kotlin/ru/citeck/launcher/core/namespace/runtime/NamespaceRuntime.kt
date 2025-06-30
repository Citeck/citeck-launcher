package ru.citeck.launcher.core.namespace.runtime

import io.github.oshai.kotlinlogging.KotlinLogging
import ru.citeck.launcher.core.actions.ActionsService
import ru.citeck.launcher.core.appdef.ApplicationDef
import ru.citeck.launcher.core.config.cloud.CloudConfigServer
import ru.citeck.launcher.core.database.DataRepo
import ru.citeck.launcher.core.namespace.NamespaceDto
import ru.citeck.launcher.core.namespace.NamespaceRef
import ru.citeck.launcher.core.namespace.NamespacesService
import ru.citeck.launcher.core.namespace.gen.NamespaceGenResp
import ru.citeck.launcher.core.namespace.gen.NamespaceGenerator
import ru.citeck.launcher.core.namespace.runtime.actions.AppImagePullAction
import ru.citeck.launcher.core.namespace.runtime.actions.AppRunAction
import ru.citeck.launcher.core.namespace.runtime.actions.AppStopAction
import ru.citeck.launcher.core.namespace.runtime.docker.DockerApi
import ru.citeck.launcher.core.namespace.runtime.docker.DockerConstants
import ru.citeck.launcher.core.utils.Digest
import ru.citeck.launcher.core.utils.Disposable
import ru.citeck.launcher.core.utils.file.CiteckFiles
import ru.citeck.launcher.core.utils.promise.Promise
import ru.citeck.launcher.core.utils.promise.Promises
import ru.citeck.launcher.core.utils.prop.MutProp
import ru.citeck.launcher.core.workspace.WorkspaceConfig
import java.nio.file.Path
import java.util.*
import java.util.concurrent.ArrayBlockingQueue
import java.util.concurrent.CompletableFuture
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicLong
import kotlin.concurrent.thread
import kotlin.io.path.*
import kotlin.math.min

class NamespaceRuntime(
    val namespaceRef: NamespaceRef,
    val namespaceDto: NamespaceDto,
    val workspaceConfig: WorkspaceConfig,
    private val namespaceGenerator: NamespaceGenerator,
    private val actionsService: ActionsService,
    private val dockerApi: DockerApi,
    private val nsRuntimeDataRepo: DataRepo,
    private val cloudConfigServer: CloudConfigServer
) : Disposable {

    companion object {
        private val log = KotlinLogging.logger {}

        private const val STATE_STATUS = "status"
        private const val STATE_MANUAL_STOPPED_APPS = "manualStoppedApps"
        private const val STATE_EDITED_APPS = "editedApps" // map<name, config>
        private const val STATE_EDITED_AND_LOCKED_APPS = "editedAndLockedApps" // set<name>

        private const val NAME_PREFIX = "citeck${DockerConstants.NAME_DELIM}"

        private val RT_THREAD_IDLE_DELAY_SECONDS = listOf(1, 2, 3, 5, 8, 10)

        val STALLED_APP_STATUSES = setOf(
            AppRuntimeStatus.PULL_FAILED,
            AppRuntimeStatus.START_FAILED,
            AppRuntimeStatus.STOPPING_FAILED
        )
    }

    var status = MutProp(NsRuntimeStatus.STOPPED)

    private var namespaceGenResp: NamespaceGenResp? = null

    @Volatile
    private var runtimeFilesHash: Map<Path, String> = emptyMap()

    val appRuntimes = MutProp<List<AppRuntime>>(emptyList())
    private val runtimesToRemove = Collections.synchronizedList(ArrayList<AppRuntime>())

    @Volatile
    private var runtimeThread: Thread? = null

    @Volatile
    private var isRuntimeThreadRunning = false

    val namePrefix = NAME_PREFIX
    val nameSuffix = DockerConstants.getNameSuffix(namespaceRef)
    val networkName = "${namePrefix}network$nameSuffix"
    val boundedNs = MutProp(true)

    private val nsRuntimeFilesDir = NamespacesService.getNamespaceDir(namespaceRef).resolve("rtfiles")

    private val runtimeThreadSignalQueue = ArrayBlockingQueue<Boolean>(1)

    private val appsStatusChangesCount = AtomicLong()
    private var appsStatusChangesCountProcessed = 0L

    private var currentActionType: NsRuntimeActionType = NsRuntimeActionType.NONE
    private var currentActionFuture: CompletableFuture<Unit>? = null

    private val manualStoppedAtts = Collections.newSetFromMap<String>(ConcurrentHashMap())

    private val editedAndLockedApps = Collections.newSetFromMap<String>(ConcurrentHashMap())
    private val editedApps = ConcurrentHashMap<String, ApplicationDef>()

    init {
        editedApps.putAll(nsRuntimeDataRepo[STATE_EDITED_APPS].asMap(String::class, ApplicationDef::class))
        editedAndLockedApps.addAll(nsRuntimeDataRepo[STATE_EDITED_AND_LOCKED_APPS].asStrList())
        manualStoppedAtts.addAll(nsRuntimeDataRepo[STATE_MANUAL_STOPPED_APPS].asStrList())

        status.watch { _, after ->
            if (after != NsRuntimeStatus.STOPPED && runtimeThread == null) {
                isRuntimeThreadRunning = true
                createNetworkIfNotExists(networkName)
                runtimeThread = thread(name = "nsrt$nameSuffix") {
                    try {
                        while (isRuntimeThreadRunning) {
                            var idleIterationsCounter = 0
                            try {
                                if (!runtimeThreadAction()) {
                                    val delaySeconds = RT_THREAD_IDLE_DELAY_SECONDS.let {
                                        it[min(idleIterationsCounter++, it.lastIndex)]
                                    }
                                    if (runtimeThreadSignalQueue.poll(
                                            delaySeconds.toLong(),
                                            TimeUnit.SECONDS
                                        ) != null
                                    ) {
                                        for (i in 0..3) {
                                            // add small delay to catch other "flush" commands and process it in one pass
                                            if (runtimeThreadSignalQueue.poll(250, TimeUnit.MILLISECONDS) == null) {
                                                break
                                            }
                                        }
                                    }
                                } else {
                                    idleIterationsCounter = 0
                                }
                            } catch (e: Throwable) {
                                if (e !is InterruptedException) {
                                    log.error(e) { "Exception in namespace runtime thread" }
                                }
                            }
                        }
                        runtimeThread = null
                    } finally {
                        isRuntimeThreadRunning = false
                        flushRuntimeThread()
                    }
                }
            }
            nsRuntimeDataRepo[STATE_STATUS] = after.toString()
            flushRuntimeThread()
        }
        generateNs()

        val statusBefore = nsRuntimeDataRepo[STATE_STATUS].asText()
        if (statusBefore.isNotBlank()) {
            try {
                status.value = NsRuntimeStatus.valueOf(statusBefore)
            } catch (_: Exception) {
                log.warn { "Invalid status from db: '$statusBefore'" }
            }
        }
        if (
            status.value == NsRuntimeStatus.RUNNING
            || status.value == NsRuntimeStatus.STARTING
            || status.value == NsRuntimeStatus.STALLED
        ) {
            appRuntimes.value.forEach {
                if (manualStoppedAtts.contains(it.name)) {
                    it.stop(true)
                } else {
                    it.status.value = AppRuntimeStatus.READY_TO_PULL
                }
            }
            status.value = NsRuntimeStatus.STARTING
            flushRuntimeThread()
        } else if (status.value == NsRuntimeStatus.STOPPING) {
            stop()
        }
    }

    private fun runNamespaceAction(actionType: NsRuntimeActionType): Promise<Unit> {
        if (this.currentActionType == actionType || actionType == NsRuntimeActionType.NONE) {
            return Promises.resolve(Unit)
        }
        if (actionType == NsRuntimeActionType.STOP && status.value == NsRuntimeStatus.STOPPED) {
            return Promises.resolve(Unit)
        }
        if (currentActionType != NsRuntimeActionType.NONE) {
            currentActionFuture?.cancel(true)
            currentActionFuture = null
        }
        this.currentActionType = actionType
        val currentActionFuture = object : CompletableFuture<Unit>() {
            override fun cancel(mayInterruptIfRunning: Boolean): Boolean {
                currentActionType = NsRuntimeActionType.NONE
                currentActionFuture = null
                return super.cancel(mayInterruptIfRunning)
            }
        }
        this.currentActionFuture = currentActionFuture
        when (actionType) {
            NsRuntimeActionType.START -> {
                cleanEditedNonLockedApps()
                generateNs()
                appRuntimes.value.forEach {
                    if (!it.manualStop) {
                        it.activeActionPromise.cancel(true)
                        it.start()
                    }
                }
                status.value = NsRuntimeStatus.STARTING
                boundedNs.value = true
            }

            NsRuntimeActionType.STOP -> {
                status.value = NsRuntimeStatus.STOPPING
                appRuntimes.value.forEach {
                    it.activeActionPromise.cancel(true)
                    it.stop()
                }
                flushRuntimeThread()
            }

            else -> error("Unsupported actionm type: $actionType")
        }
        return Promises.create(currentActionFuture)
    }

    private fun flushRuntimeThread() {
        runtimeThreadSignalQueue.offer(true)
    }

    private fun runtimeThreadAction(): Boolean {
        var somethingChanged = false

        if (runtimesToRemove.isNotEmpty()) {
            val runtimesToRemoveIt = runtimesToRemove.iterator()
            while (runtimesToRemoveIt.hasNext()) {
                val runtimeToRemove = runtimesToRemoveIt.next()
                val status = runtimeToRemove.status.value
                if (!status.isStoppingState()) {
                    runtimeToRemove.stop(false)
                } else if (status == AppRuntimeStatus.STOPPED) {
                    runtimesToRemoveIt.remove()
                }
            }
        }

        for (application in appRuntimes.value) {
            if (!application.activeActionPromise.isDone()) {
                continue
            }
            when (application.status.value) {
                AppRuntimeStatus.READY_TO_PULL -> {

                    if (manualStoppedAtts.remove(application.name)) {
                        nsRuntimeDataRepo[STATE_MANUAL_STOPPED_APPS] = manualStoppedAtts
                    }

                    val pullIfPresent = application.pullImageIfPresent
                    application.status.value = AppRuntimeStatus.PULLING

                    val promise = AppImagePullAction.execute(
                        actionsService,
                        application,
                        pullIfPresent
                    )
                    application.activeActionPromise = promise
                    promise.then {
                        application.status.value = AppRuntimeStatus.READY_TO_START
                        flushRuntimeThread()
                    }.catch {
                        if (application.status.value == AppRuntimeStatus.PULLING) {
                            application.status.value = AppRuntimeStatus.PULL_FAILED
                        }
                    }
                    somethingChanged = true
                }

                AppRuntimeStatus.READY_TO_START -> {
                    application.status.value = AppRuntimeStatus.STARTING
                    val promise = AppRunAction.execute(actionsService, application, runtimeFilesHash)
                    application.activeActionPromise = promise
                    promise.then {
                        application.status.value = AppRuntimeStatus.RUNNING
                        flushRuntimeThread()
                    }.catch {
                        if (application.status.value == AppRuntimeStatus.STARTING) {
                            application.status.value = AppRuntimeStatus.START_FAILED
                        }
                    }
                    somethingChanged = true
                }

                AppRuntimeStatus.READY_TO_STOP -> {
                    if (application.manualStop) {
                        if (manualStoppedAtts.add(application.name)) {
                            nsRuntimeDataRepo[STATE_MANUAL_STOPPED_APPS] = manualStoppedAtts
                        }
                    }
                    application.status.value = AppRuntimeStatus.STOPPING
                    val promise = AppStopAction.execute(actionsService, application)
                    application.activeActionPromise = promise
                    promise.then {
                        application.status.value = AppRuntimeStatus.STOPPED
                        flushRuntimeThread()
                    }.catch {
                        if (application.status.value == AppRuntimeStatus.STOPPING) {
                            application.status.value = AppRuntimeStatus.STOPPING_FAILED
                        }
                    }
                    somethingChanged = true
                }

                else -> {}
            }
        }
        val statusChangesCount = this.appsStatusChangesCount.get()
        if (status.value != NsRuntimeStatus.STALLED && appsStatusChangesCountProcessed != statusChangesCount) {

            if (appRuntimes.value.any { STALLED_APP_STATUSES.contains(it.status.value) }) {
                status.value = NsRuntimeStatus.STALLED
                currentActionFuture?.completeExceptionally(RuntimeException("Namespace stalled"))
                resetCurrentActionState()
            } else {
                when (status.value) {
                    NsRuntimeStatus.STARTING -> {
                        if (appRuntimes.value.all {
                                it.manualStop || it.status.value == AppRuntimeStatus.RUNNING
                            }) {
                            status.value = NsRuntimeStatus.RUNNING
                            currentActionFuture?.complete(Unit)
                            resetCurrentActionState()
                        }
                    }

                    NsRuntimeStatus.STOPPING -> {
                        if (appRuntimes.value.all { it.status.value == AppRuntimeStatus.STOPPED }) {
                            dockerApi.deleteNetwork(networkName)
                            status.value = NsRuntimeStatus.STOPPED
                            resetCurrentActionState()
                        }
                    }

                    else -> {}
                }
            }
            appsStatusChangesCountProcessed = statusChangesCount
        }
        return somethingChanged
    }

    private fun resetCurrentActionState() {
        currentActionType = NsRuntimeActionType.NONE
        currentActionFuture = null
    }

    fun start(): Promise<Unit> {
        return runNamespaceAction(NsRuntimeActionType.START)
    }

    fun stop(): Promise<Unit> {
        return runNamespaceAction(NsRuntimeActionType.STOP)
    }

    private fun fixVolume(volume: String): String {
        if (!volume.startsWith("./")) {
            return volume
        }
        val delimIdx = volume.indexOf(":")
        if (delimIdx <= 0) {
            return volume
        }
        var localPath = volume.substring(0, delimIdx)
        localPath = nsRuntimeFilesDir.resolve(localPath.substring(2))
            .absolutePathString()
        return localPath + volume.substring(delimIdx)
    }

    private fun cleanEditedNonLockedApps() {

        val editedAppsIt = editedApps.iterator()
        var changed = false
        while (editedAppsIt.hasNext()) {
            val entry = editedAppsIt.next()
            if (!editedAndLockedApps.contains(entry.key)) {
                editedAppsIt.remove()
                appRuntimes.value.find { it.name == entry.key }?.editedDef?.value = false
                changed = true
            }
        }
        if (changed) {
            nsRuntimeDataRepo[STATE_EDITED_APPS] = editedApps
        }
    }

    fun isEditedAndLockedApp(appName: String): Boolean {
        return editedAndLockedApps.contains(appName)
    }

    fun resetAppDef(name: String) {

        editedAndLockedApps.remove(name)
        editedApps.remove(name)

        val genRespDef = namespaceGenResp?.applications?.find { it.name == name }
        if (genRespDef == null) {
            log.error { "Generated app def doesn't found for app '$name'. Reset can't be performed" }
            return
        }
        val runtime = appRuntimes.value.find { it.name == name } ?: return
        runtime.def.value = normalizeGeneratedAppDef(genRespDef)
        runtime.editedDef.value = false
    }

    fun updateAppDef(appDefBefore: ApplicationDef, appDefAfter: ApplicationDef, locked: Boolean) {
        val appName = appDefBefore.name
        if (appDefBefore == appDefAfter && locked == editedAndLockedApps.contains(appName)) {
            return
        }
        log.info { "Update app def for '${appDefBefore.name}'. Locked: $locked" }
        val runtime = appRuntimes.value.find { it.name == appName } ?: error("Runtime is not found for app '$appName'")
        if (locked) {
            if (editedAndLockedApps.add(appDefBefore.name)) {
                nsRuntimeDataRepo[STATE_EDITED_AND_LOCKED_APPS] = editedAndLockedApps
            }
        } else {
            if (editedAndLockedApps.remove(appDefBefore.name)) {
                nsRuntimeDataRepo[STATE_EDITED_AND_LOCKED_APPS] = editedAndLockedApps
            }
        }
        val fixedDef = appDefAfter.copy()
            .withKind(appDefBefore.kind)
            .withReplicas(appDefBefore.replicas)
            .withScalable(appDefBefore.scalable)
            .build()

        if (fixedDef != appDefBefore) {
            editedApps[appName] = fixedDef
            nsRuntimeDataRepo[STATE_EDITED_APPS] = editedApps
            runtime.def.value = fixedDef
        }
        runtime.editedDef.value = editedApps.containsKey(appName)
    }

    private fun normalizeGeneratedAppDef(appDef: ApplicationDef): ApplicationDef {
        return appDef.copy()
            .withVolumes(appDef.volumes.map(this::fixVolume))
            .withInitContainers(appDef.initContainers.map { ic ->
                ic.copy().withVolumes(ic.volumes.map(this::fixVolume)).build()
            }).build()
    }

    private fun generateNs() {

        val newGenRes = namespaceGenerator.generate(namespaceDto)
        val currentRuntimesByName = appRuntimes.value.associateByTo(mutableMapOf()) { it.name }
        val newRuntimes = ArrayList<AppRuntime>()

        newGenRes.applications.forEach { appDef ->

            val currentRuntime = currentRuntimesByName.remove(appDef.name)

            val updatedAppDef = editedApps[appDef.name] ?: normalizeGeneratedAppDef(appDef)
            if (currentRuntime == null) {
                newRuntimes.add(AppRuntime(this, updatedAppDef, dockerApi))
            } else {
                currentRuntime.def.value = updatedAppDef
            }
        }
        if (newRuntimes.isNotEmpty()) {
            val resRuntimes = ArrayList(appRuntimes.value)
            resRuntimes.addAll(newRuntimes)
            appRuntimes.value = resRuntimes
            if (status.value != NsRuntimeStatus.STOPPED) {
                newRuntimes.forEach { it.start() }
            }
            newRuntimes.forEach {
                it.status.watch { _, after ->
                    appsStatusChangesCount.incrementAndGet()
                    if (after == AppRuntimeStatus.READY_TO_PULL && status.value == NsRuntimeStatus.RUNNING) {
                        status.value = NsRuntimeStatus.STARTING
                    }
                    flushRuntimeThread()
                }
                it.editedDef.value = editedApps.containsKey(it.name)
            }
        }
        runtimesToRemove.addAll(currentRuntimesByName.values)
        currentRuntimesByName.values.forEach { it.stop() }

        val currentFiles = CiteckFiles.getFile(nsRuntimeFilesDir).getFilesContent().toMutableMap()
        val runtimeFilesHash = TreeMap<Path, String>()
        for ((path, bytes) in newGenRes.files) {
            val currentData = currentFiles.remove(path)
            val targetFilePath = nsRuntimeFilesDir.resolve(path)
            if (!bytes.contentEquals(currentData)) {
                val fileDir = targetFilePath.parent
                if (fileDir.exists() && !fileDir.isDirectory()) {
                    fileDir.deleteExisting()
                }
                if (!fileDir.exists()) {
                    fileDir.toFile().mkdirs()
                }
                try {
                    targetFilePath.outputStream().use { it.write(bytes) }
                } catch (writeEx: Throwable) {
                    throw RuntimeException(
                        "File write failed. " +
                            "File path: '$path' " +
                            "Content: ${Base64.getEncoder().encodeToString(bytes)}",
                        writeEx
                    )
                }
            }
            if (path.endsWith(".sh") && !targetFilePath.toFile().canExecute()) {
                targetFilePath.toFile().setExecutable(true, false)
            }
            runtimeFilesHash[targetFilePath] = Digest.sha256().update(bytes).toHex()
        }
        this.runtimeFilesHash = runtimeFilesHash
        for (path in currentFiles.keys) {
            nsRuntimeFilesDir.resolve(path).deleteIfExists()
        }

        namespaceGenResp = newGenRes

        cloudConfigServer.cloudConfig = newGenRes.cloudConfig
    }

    override fun dispose() {
        isRuntimeThreadRunning = false
        flushRuntimeThread()
        runtimeThread?.interrupt()
        runtimeThread = null
    }

    private fun createNetworkIfNotExists(networkName: String) {
        val networks = dockerApi.getNetworkByName(networkName)
        if (networks == null) {
            dockerApi.createBridgeNetwork(networkName)
        }
    }

    private enum class NsRuntimeActionType {
        START, STOP, NONE
    }
}


