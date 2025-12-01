package ru.citeck.launcher.core.namespace.runtime

import io.github.oshai.kotlinlogging.KotlinLogging
import ru.citeck.launcher.core.actions.ActionsService
import ru.citeck.launcher.core.appdef.ApplicationDef
import ru.citeck.launcher.core.config.bundle.BundleDef
import ru.citeck.launcher.core.config.bundle.BundleNotFoundException
import ru.citeck.launcher.core.config.bundle.BundlesService
import ru.citeck.launcher.core.config.cloud.CloudConfigServer
import ru.citeck.launcher.core.database.DataRepo
import ru.citeck.launcher.core.git.GitUpdatePolicy
import ru.citeck.launcher.core.namespace.NamespaceConfig
import ru.citeck.launcher.core.namespace.NamespaceRef
import ru.citeck.launcher.core.namespace.gen.NamespaceGenResp
import ru.citeck.launcher.core.namespace.gen.NamespaceGenerator
import ru.citeck.launcher.core.namespace.runtime.actions.AppImagePullAction
import ru.citeck.launcher.core.namespace.runtime.actions.AppStartAction
import ru.citeck.launcher.core.namespace.runtime.actions.AppStopAction
import ru.citeck.launcher.core.namespace.runtime.docker.DockerApi
import ru.citeck.launcher.core.namespace.runtime.docker.DockerConstants
import ru.citeck.launcher.core.namespace.runtime.docker.exception.DockerStaleNetworkException
import ru.citeck.launcher.core.utils.Disposable
import ru.citeck.launcher.core.utils.json.Json
import ru.citeck.launcher.core.utils.prop.MutProp
import ru.citeck.launcher.core.workspace.WorkspaceConfig
import java.nio.file.Path
import java.util.*
import java.util.concurrent.ArrayBlockingQueue
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.ConcurrentLinkedDeque
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicInteger
import java.util.concurrent.atomic.AtomicLong
import java.util.concurrent.locks.ReentrantLock
import kotlin.concurrent.thread
import kotlin.concurrent.withLock
import kotlin.math.min

class NamespaceRuntime(
    val namespaceRef: NamespaceRef,
    val namespaceConfig: MutProp<NamespaceConfig>,
    val workspaceConfig: MutProp<WorkspaceConfig>,
    val runtimeFiles: NsRuntimeFiles,
    private val namespaceGenerator: NamespaceGenerator,
    private val bundlesService: BundlesService,
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
        private const val STATE_BUNDLE_DEF = "bundleDef"

        private val RT_THREAD_IDLE_DELAY_SECONDS = listOf(1, 2, 3, 5, 8, 10)
    }

    var nsStatus = MutProp(NsRuntimeStatus.STOPPED)

    var namespaceGenResp = MutProp<NamespaceGenResp?>("ns-gen-res-$namespaceRef", null)

    val appRuntimes = MutProp<List<AppRuntime>>(emptyList())
    private val runtimesToRemove = Collections.synchronizedList(ArrayList<AppRuntime>())

    @Volatile
    private var runtimeThread: Thread? = null
    private val isActive = AtomicBoolean()
    private val activeStateVersion = AtomicLong()

    private val runtimeActiveStatusLock = ReentrantLock()

    val namePrefix = DockerConstants.getNamePrefix(namespaceRef)
    val nameSuffix = DockerConstants.getNameSuffix(namespaceRef)
    val networkName = "${namePrefix}network$nameSuffix"

    private val runtimeThreadSignalQueue = ArrayBlockingQueue<Boolean>(1)
    private val runtimeCommands = ConcurrentLinkedDeque<NsRuntimeCmd>()
    private val runtimeCommandsSize = AtomicInteger()

    private val appsStatusChangesCount = AtomicLong()
    private var appsStatusChangesCountProcessed = 0L
    @Volatile
    private var lastAppStatusChangeTime = System.currentTimeMillis()

    internal val detachedApps = Collections.newSetFromMap<String>(ConcurrentHashMap())

    private val editedAndLockedApps = Collections.newSetFromMap<String>(ConcurrentHashMap())
    private val editedApps = ConcurrentHashMap<String, ApplicationDef>()

    private var namespaceConfigWatcher: Disposable

    private var cachedBundleDef: BundleDef = BundleDef.EMPTY

    init {
        editedApps.putAll(nsRuntimeDataRepo[STATE_EDITED_APPS].asMap(String::class, ApplicationDef::class))
        editedAndLockedApps.addAll(nsRuntimeDataRepo[STATE_EDITED_AND_LOCKED_APPS].asStrList())
        detachedApps.addAll(nsRuntimeDataRepo[STATE_MANUAL_STOPPED_APPS].asStrList())

        val bundleDefFromRepo = nsRuntimeDataRepo[STATE_BUNDLE_DEF]
        if (bundleDefFromRepo.isEmpty()) {
            cachedBundleDef = BundleDef.EMPTY
        } else {
            try {
                cachedBundleDef = bundleDefFromRepo.getAsNotNull(BundleDef::class)
            } catch (e: Throwable) {
                log.error(e) { "Bundle from repo reading failed" }
                cachedBundleDef = BundleDef.EMPTY
            }
        }

        namespaceConfigWatcher = namespaceConfig.watch { _, _ ->
            if (isActive.get()) {
                generateNs(GitUpdatePolicy.ALLOWED_IF_NOT_EXISTS)
            }
        }

        nsStatus.watch { before, after ->
            log.info {
                "[${namespaceConfig.name} (${namespaceRef.namespace})] Namespace runtime " +
                    "status was changed: $before -> $after"
            }
            if (before == NsRuntimeStatus.STOPPED) {
                createNetworkIfNotExists(networkName)
            }
            nsRuntimeDataRepo[STATE_STATUS] = after.toString()
            flushRuntimeThread()
        }

        val statusBefore = nsRuntimeDataRepo[STATE_STATUS].asText()
        if (statusBefore.isNotBlank()) {
            try {
                nsStatus.setValue(NsRuntimeStatus.valueOf(statusBefore)) {}
            } catch (_: Exception) {
                log.warn { "Invalid status from db: '$statusBefore'" }
            }
        }
        val statusValue = nsStatus.getValue()
        if (
            statusValue == NsRuntimeStatus.RUNNING ||
            statusValue == NsRuntimeStatus.STARTING ||
            statusValue == NsRuntimeStatus.STALLED
        ) {
            nsStatus.setValue(NsRuntimeStatus.STARTING) {}
        } else if (statusValue == NsRuntimeStatus.STOPPING) {
            stop()
        }
        appsStatusChangesCount.incrementAndGet()
    }

    fun setActive(active: Boolean) = runtimeActiveStatusLock.withLock {
        val valueBefore = isActive.get()
        try {
            setActiveImpl(active)
        } catch (e: Throwable) {
            isActive.set(valueBefore)
            throw e
        }
    }

    private fun setActiveImpl(active: Boolean) {
        if (!active) {
            if (isActive.compareAndSet(true, false)) {
                flushRuntimeThread()
                runtimeThread?.interrupt()
                runtimeThread = null
            }
            return
        }
        if (!isActive.compareAndSet(false, true)) {
            return
        }
        val activeStateVersion = activeStateVersion.incrementAndGet()
        generateNs(GitUpdatePolicy.ALLOWED)
        runtimeThread?.interrupt()
        runtimeThread = thread(start = false, name = "nsrt$nameSuffix") {
            log.info { "(+) Namespace runtime thread was started" }
            try {
                var idleIterationsCounter = 0
                while (isActive.get() && activeStateVersion == this.activeStateVersion.get()) {
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
                                var repeats = 4
                                while (--repeats >= 0) {
                                    // add small delay to catch other "flush" commands and process it in one pass
                                    if (runtimeThreadSignalQueue.poll(250, TimeUnit.MILLISECONDS) == null) {
                                        break
                                    }
                                }
                            }
                            if ((System.currentTimeMillis() - lastAppStatusChangeTime) > 30_000) {
                                appsStatusChangesCount.incrementAndGet()
                                lastAppStatusChangeTime = System.currentTimeMillis()
                            }
                        } else {
                            idleIterationsCounter = 0
                        }
                    } catch (e: Throwable) {
                        if (e !is InterruptedException) {
                            log.error(e) { "Exception in namespace runtime thread" }
                            try {
                                Thread.sleep(3000)
                            } catch (_: InterruptedException) {
                                // do nothing
                            }
                        }
                    }
                }
            } finally {
                runtimeThread = null
                log.info { "(-) Namespace runtime thread was stopped" }
            }
        }
        runtimeThread?.start()
        flushRuntimeThread()
    }

    fun setDetachedApps(detachedApps: Set<String>) {
        if (this.detachedApps == detachedApps) {
            return
        }
        this.detachedApps.clear()
        this.detachedApps.addAll(detachedApps)
        detachedAppsChanged(detachedApps)
    }

    fun addDetachedApp(appName: String) {
        if (detachedApps.add(appName)) {
            detachedAppsChanged(setOf(appName))
        }
    }

    private fun removeDetachedApps(detachedAppsToRemove: Set<String>) {
        if (detachedAppsToRemove.isEmpty()) {
            return
        }
        val changedApps = detachedAppsToRemove.filterTo(HashSet()) { detachedApps.remove(it) }
        if (changedApps.isNotEmpty()) {
            detachedAppsChanged(changedApps)
        }
    }

    private fun scheduleRuntimeCmd(cmd: NsRuntimeCmd, systemCmd: Boolean) {
        if (runtimeCommands.peekLast() == cmd) {
            return
        }
        val cmdCount = runtimeCommandsSize.get()
        if (systemCmd && cmdCount < 100 || !systemCmd && cmdCount < 50) {
            runtimeCommands.offer(cmd)
            runtimeCommandsSize.incrementAndGet()
            flushRuntimeThread()
        }
    }

    private fun detachedAppsChanged(changedApps: Set<String>) {
        nsRuntimeDataRepo[STATE_MANUAL_STOPPED_APPS] = detachedApps
        val dependsOnDetachedApps = namespaceGenResp.getValue()?.dependsOnDetachedApps ?: emptySet()
        if (!nsStatus.getValue().isStoppingState() && changedApps.any { dependsOnDetachedApps.contains(it) }) {
            scheduleRuntimeCmd(RegenerateNsCmd, true)
        }
    }

    private fun flushRuntimeThread() {
        runtimeThreadSignalQueue.offer(true)
    }

    private fun collapseCommandsIfPossible(cmd0: NsRuntimeCmd, cmd1: NsRuntimeCmd): NsRuntimeCmd? {
        if (cmd0 == cmd1) {
            return cmd1
        }
        if ((cmd0 is StartNsCmd && cmd1 is StopNsCmd) || (cmd0 is StopNsCmd && cmd1 is StartNsCmd)) {
            return cmd1
        }
        if (cmd0 is RegenerateNsCmd && cmd1 is StartNsCmd) {
            return cmd1
        }
        if (cmd1 is RegenerateNsCmd && cmd0 is StartNsCmd) {
            return cmd0
        }
        return null
    }

    private fun runtimeThreadAction(): Boolean {
        var somethingChanged = false

        var rtCommand = runtimeCommands.poll()
        while (rtCommand != null) {
            runtimeCommandsSize.decrementAndGet()
            var nextCmd = runtimeCommands.poll()
            while (nextCmd != null) {
                val collapsedCmd = collapseCommandsIfPossible(rtCommand, nextCmd) ?: break
                runtimeCommandsSize.decrementAndGet()
                rtCommand = collapsedCmd
                nextCmd = runtimeCommands.poll()
            }
            when (rtCommand) {
                is StartNsCmd, is RegenerateNsCmd -> {
                    val gitUpdatePolicy = if (rtCommand is StartNsCmd) {
                        if (rtCommand.forceUpdate) {
                            GitUpdatePolicy.REQUIRED
                        } else {
                            GitUpdatePolicy.ALLOWED
                        }
                    } else {
                        GitUpdatePolicy.ALLOWED_IF_NOT_EXISTS
                    }
                    generateNs(gitUpdatePolicy)
                    if (rtCommand is StartNsCmd) {
                        appRuntimes.getValue().forEach {
                            if (!it.isDetached) {
                                it.start()
                            }
                        }
                    }
                    nsStatus.setValue(NsRuntimeStatus.STARTING)
                }
                is StopNsCmd -> {
                    if (!nsStatus.getValue().isStoppingState()) {
                        nsStatus.setValue(NsRuntimeStatus.STOPPING)
                        appRuntimes.getValue().forEach {
                            it.stop()
                        }
                    }
                }
            }
            rtCommand = nextCmd
        }
        // We don't aim for high precision here.
        // This count is only needed to prevent an infinite command queue.
        runtimeCommandsSize.set(runtimeCommands.size)

        if (runtimesToRemove.isNotEmpty()) {

            val runtimesToRemoveIt = runtimesToRemove.iterator()

            while (runtimesToRemoveIt.hasNext()) {

                val runtimeToRemove = runtimesToRemoveIt.next()
                val status = runtimeToRemove.status
                val statusVal = status.getValue()

                if (statusVal == AppRuntimeStatus.STOPPED) {

                    runtimesToRemoveIt.remove()
                } else if (statusVal == AppRuntimeStatus.READY_TO_STOP) {

                    status.setValue(AppRuntimeStatus.STOPPING)
                    val promise = AppStopAction.execute(actionsService, runtimeToRemove)
                    runtimeToRemove.activeActionPromise = promise
                    promise.catch {
                        log.error(it) { "Runtime stopping failed. App: ${runtimeToRemove.name}" }
                    }.finally {
                        status.setValue(AppRuntimeStatus.STOPPED)
                    }
                }
            }
        }

        val detachedAppsToRemove = HashSet<String>()
        val allRuntimes = appRuntimes.getValue()

        for (application in allRuntimes) {

            when (application.status.getValue()) {
                AppRuntimeStatus.READY_TO_PULL -> {

                    detachedAppsToRemove.add(application.name)

                    val pullIfPresent = application.pullImageIfPresent
                    application.status.setValue(AppRuntimeStatus.PULLING) { statusVersion ->
                        val promise = AppImagePullAction.execute(
                            actionsService,
                            application,
                            pullIfPresent
                        )
                        application.activeActionPromise = promise
                        promise.then {
                            val deps = application.dependenciesToWait
                            if (deps.isNotEmpty()) {
                                for (runtime in allRuntimes) {
                                    if (runtime.status.getValue() != AppRuntimeStatus.RUNNING) {
                                        continue
                                    }
                                    deps.remove(runtime.name)
                                    if (deps.isEmpty()) {
                                        break
                                    }
                                }
                            }
                            if (deps.isEmpty()) {
                                application.status.setValue(AppRuntimeStatus.READY_TO_START, statusVersion)
                            } else {
                                log.info { "Application '${application.name}' will wait for these dependencies to start: $deps" }
                                application.status.setValue(AppRuntimeStatus.DEPS_WAITING, statusVersion)
                            }
                            flushRuntimeThread()
                        }.catch {
                            application.status.setValue(AppRuntimeStatus.PULL_FAILED, statusVersion)
                        }
                    }
                    somethingChanged = true
                }

                AppRuntimeStatus.DEPS_WAITING -> {
                    val currentTime = System.currentTimeMillis()
                    if ((currentTime - application.lastDepsCheckingTime) > 20_000) {
                        val deps = application.dependenciesToWait
                        for (runtime in allRuntimes) {
                            if (runtime.status.getValue() == AppRuntimeStatus.RUNNING) {
                                application.dependenciesToWait.remove(runtime.name)
                            }
                            if (deps.isEmpty()) {
                                break
                            }
                        }
                        if (deps.isEmpty()) {
                            application.status.setValue(AppRuntimeStatus.READY_TO_START)
                            somethingChanged = true
                        }
                        application.lastDepsCheckingTime = currentTime
                    }
                }

                AppRuntimeStatus.READY_TO_START -> {
                    application.status.setValue(AppRuntimeStatus.STARTING) { statusVersion ->
                        val promise = AppStartAction.execute(actionsService, application)
                        application.activeActionPromise = promise
                        promise.then {
                            application.status.setValue(AppRuntimeStatus.RUNNING, statusVersion)
                        }.catch {
                            if (it is DockerImageNotFound) {
                                application.status.setValue(AppRuntimeStatus.READY_TO_PULL, statusVersion)
                            } else {
                                application.status.setValue(AppRuntimeStatus.START_FAILED, statusVersion)
                            }
                        }
                    }
                    somethingChanged = true
                }

                AppRuntimeStatus.READY_TO_STOP -> {

                    application.status.setValue(AppRuntimeStatus.STOPPING) { statusVersion ->
                        val promise = AppStopAction.execute(actionsService, application)
                        application.activeActionPromise = promise
                        promise.then {
                            application.status.setValue(AppRuntimeStatus.STOPPED, statusVersion)
                        }.catch {
                            application.status.setValue(AppRuntimeStatus.STOPPING_FAILED, statusVersion)
                        }
                    }
                    somethingChanged = true
                }

                else -> {}
            }
        }

        removeDetachedApps(detachedAppsToRemove)
        updateNsStatus(allRuntimes)

        return somethingChanged
    }

    private fun updateNsStatus(allRuntimes: List<AppRuntime>) {

        val statusChangesCount = this.appsStatusChangesCount.get()
        if (appsStatusChangesCountProcessed == statusChangesCount) {
            return
        }

        appsStatusChangesCountProcessed = statusChangesCount

        if (nsStatus.getValue() == NsRuntimeStatus.STALLED) {
            if (allRuntimes.all { !it.status.getValue().isStalledState() }) {
                var nsStatusChanged = false
                for (runtime in allRuntimes) {
                    val status = runtime.status.getValue()
                    if (status.isStartingState()) {
                        nsStatus.setValue(NsRuntimeStatus.STARTING)
                        nsStatusChanged = true
                        break
                    }
                }
                if (!nsStatusChanged) {
                    nsStatus.setValue(NsRuntimeStatus.STOPPING)
                }
            } else {
                return
            }
        } else {
            val stalledStates = allRuntimes.mapNotNull {
                if (it.status.getValue().isStalledState()) {
                    it.name to it.status.getValue()
                } else {
                    null
                }
            }
            if (stalledStates.isNotEmpty()) {
                log.error { "Found containers in stalled state: $stalledStates. Namespace is stalled" }
                nsStatus.setValue(NsRuntimeStatus.STALLED)
                return
            }
        }

        when (nsStatus.getValue()) {

            NsRuntimeStatus.STARTING -> {
                if (allRuntimes.all {
                        it.status.getValue().run {
                            isStoppingState() || this == AppRuntimeStatus.RUNNING
                        }
                    }
                ) {
                    nsStatus.setValue(NsRuntimeStatus.RUNNING)
                }
            }

            NsRuntimeStatus.STOPPING, NsRuntimeStatus.RUNNING -> {
                if (allRuntimes.all { it.status.getValue() == AppRuntimeStatus.STOPPED }) {
                    try {
                        dockerApi.deleteNetwork(networkName)
                    } catch (_: DockerStaleNetworkException) {
                        log.warn {
                            """
                            Failed to remove Docker network '$networkName': Docker reports the network has active endpoints, but no containers are attached.
                            This is likely caused by stale internal Docker state (e.g., orphaned endpoints or network namespaces).
                            You can try to remove network manually using 'docker network rm $networkName' or try to restart your system.
                            This is not a critical error — the launcher will continue using this network in future runs without issues.
                            """.trimIndent()
                        }
                    }
                    nsStatus.setValue(NsRuntimeStatus.STOPPED)
                }
            }

            else -> {}
        }
    }

    fun updateAndStart(forceUpdate: Boolean = false) {
        scheduleRuntimeCmd(StartNsCmd(forceUpdate), false)
    }

    fun stop() {
        scheduleRuntimeCmd(StopNsCmd, false)
    }

    fun resetAppDef(name: String) {

        log.info { "Reset app def: $name" }

        if (editedAndLockedApps.remove(name)) {
            nsRuntimeDataRepo[STATE_EDITED_AND_LOCKED_APPS] = editedAndLockedApps
        }
        if (editedApps.remove(name) != null) {
            nsRuntimeDataRepo[STATE_EDITED_APPS] = editedApps
        }

        val genRespDef = namespaceGenResp.getValue()?.applications?.find { it.name == name }
        if (genRespDef == null) {
            log.error { "Generated app def doesn't found for app '$name'. Reset can't be performed" }
            return
        }
        val runtime = appRuntimes.getValue().find { it.name == name } ?: return

        runtime.def.setValue(genRespDef.withActualVolumesContentHash())
        runtime.editedDef.setValue(false)
    }

    fun updateAppDef(appDefBefore: ApplicationDef, appDefAfter: ApplicationDef, locked: Boolean) {
        val appName = appDefBefore.name
        if (appDefBefore == appDefAfter && locked == editedAndLockedApps.contains(appName)) {
            return
        }
        log.info { "Update app def for '${appDefBefore.name}'. Locked: $locked" }
        val runtime = appRuntimes.getValue().find { it.name == appName } ?: error("Runtime is not found for app '$appName'")
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
            .build()

        if (fixedDef != appDefBefore) {
            editedApps[appName] = fixedDef
            nsRuntimeDataRepo[STATE_EDITED_APPS] = editedApps
            runtime.def.setValue(fixedDef.withActualVolumesContentHash())
        }
        runtime.editedDef.setValue(editedApps.containsKey(appName))
    }

    fun resetEditedFile(file: Path) {
        runtimeFiles.resetEditedFile(file)
        updateVolumeFilesHash(file)
    }

    fun pushEditedFile(file: Path, content: ByteArray) {
        if (!runtimeFiles.applyEditedFile(file, content)) {
            return
        }
        updateVolumeFilesHash(file)
    }

    private fun updateVolumeFilesHash(path: Path) {
        for (runtime in appRuntimes.getValue()) {
            if (runtime.volumeFiles.getValue().none { it.path == path }) {
                continue
            }
            val appDef = runtime.def.getValue()
            runtime.def.setValue(appDef.withActualVolumesContentHash())
        }
    }

    private fun generateNs(updatePolicy: GitUpdatePolicy) {

        val nsConfig = namespaceConfig.getValue()
        val bundleDef = try {
            bundlesService.getBundleByRef(nsConfig.bundleRef, updatePolicy)
        } catch (e: Throwable) {
            if (e is BundleNotFoundException) {
                log.warn { "Bundle doesn't found by ref ${nsConfig.bundleRef}. We will use cached definition." }
            } else {
                log.error(e) { "Bundle loading error. Ref: ${nsConfig.bundleRef}. We will use cached definition." }
            }
            if (cachedBundleDef.isEmpty()) {
                val latestBundleRef = bundlesService.getLatestRepoBundle(nsConfig.bundleRef.repo)
                bundlesService.getBundleByRef(latestBundleRef)
            } else {
                cachedBundleDef
            }
        }

        if (bundleDef != cachedBundleDef) {
            cachedBundleDef = bundleDef
            nsRuntimeDataRepo[STATE_BUNDLE_DEF] = bundleDef
        }

        val newGenRes = try {
            namespaceGenerator.generate(namespaceConfig.getValue(), bundleDef, detachedApps)
        } catch (e: Throwable) {
            if (updatePolicy != GitUpdatePolicy.REQUIRED) {
                generateNs(GitUpdatePolicy.REQUIRED)
                return
            } else {
                log.error(e) {
                    "Exception occurred while namespace generation. " +
                        "bundleDef: ${Json.toString(bundleDef)} " +
                        "Namespace config: ${Json.toString(namespaceConfig.getValue())} " +
                        "detachedApps: ${Json.toString(detachedApps)}"
                }
                val nsName = namespaceConfig.getValue().name
                throw IllegalStateException("Exception occurred while namespace generation: '$nsName'", e)
            }
        }
        val currentRuntimesByName = appRuntimes.getValue().associateByTo(mutableMapOf()) { it.name }
        val newRuntimes = ArrayList<AppRuntime>()

        runtimeFiles.applyGeneratedFiles(newGenRes.files)

        newGenRes.applications.forEach { appDef ->

            val currentRuntime = currentRuntimesByName.remove(appDef.name)

            var updatedAppDef = editedApps[appDef.name] ?: appDef
            updatedAppDef = updatedAppDef.withActualVolumesContentHash()

            if (currentRuntime == null) {
                newRuntimes.add(AppRuntime(this, updatedAppDef, dockerApi))
            } else {
                currentRuntime.def.setValue(updatedAppDef)
            }
        }
        runtimesToRemove.addAll(currentRuntimesByName.values)
        currentRuntimesByName.values.forEach { it.stop() }

        if (newRuntimes.isNotEmpty() || currentRuntimesByName.isNotEmpty()) {

            val resRuntimes = ArrayList(appRuntimes.getValue())
            resRuntimes.addAll(newRuntimes)
            if (currentRuntimesByName.isNotEmpty()) {
                val it = resRuntimes.iterator()
                while (it.hasNext()) {
                    val runtime = it.next()
                    if (currentRuntimesByName.containsKey(runtime.name)) {
                        log.info { "Remove runtime for app '${runtime.name}'" }
                        it.remove()
                    }
                }
            }

            appRuntimes.setValue(resRuntimes)

            if (newRuntimes.isNotEmpty()) {
                if (!nsStatus.getValue().isStoppingState()) {
                    newRuntimes.forEach {
                        if (!detachedApps.contains(it.name)) {
                            it.start()
                        }
                    }
                }
                newRuntimes.forEach { newRuntime ->
                    newRuntime.status.watch { _, after ->
                        appsStatusChangesCount.incrementAndGet()
                        lastAppStatusChangeTime = System.currentTimeMillis()
                        if (after == AppRuntimeStatus.READY_TO_PULL &&
                            (
                                nsStatus.getValue() == NsRuntimeStatus.RUNNING ||
                                    nsStatus.getValue() == NsRuntimeStatus.STOPPED
                                )
                        ) {
                            nsStatus.setValue(NsRuntimeStatus.STARTING)
                        }
                        if (after == AppRuntimeStatus.RUNNING) {
                            for (runtime in appRuntimes.getValue()) {
                                if (runtime.status.getValue() == AppRuntimeStatus.DEPS_WAITING) {
                                    runtime.dependenciesToWait.remove(newRuntime.name)
                                    runtime.lastDepsCheckingTime = 0L
                                }
                            }
                        }
                        flushRuntimeThread()
                    }
                    newRuntime.editedDef.setValue(editedApps.containsKey(newRuntime.name))
                }
            }
        }

        namespaceGenResp.setValue(newGenRes)

        cloudConfigServer.cloudConfig = newGenRes.cloudConfig
    }

    private fun ApplicationDef.withActualVolumesContentHash(): ApplicationDef {
        return this.withVolumesContentHash(
            runtimeFiles.getPathsContentHash(
                getVolumePathsToCalcContentHash(this)
            )
        )
    }

    private fun getVolumePathsToCalcContentHash(applicationDef: ApplicationDef): List<String> {
        val result = LinkedHashSet<String>()
        applicationDef.volumes.forEach {
            addVolumePathToCalcContentHash(it, result)
        }
        applicationDef.initContainers.forEach { initContainer ->
            initContainer.volumes.forEach {
                addVolumePathToCalcContentHash(it, result)
            }
        }
        return result.toList()
    }

    private fun addVolumePathToCalcContentHash(volume: String, result: MutableSet<String>) {
        if (!volume.startsWith("./")) return
        val twoDotsIdx = volume.indexOf(':')
        if (twoDotsIdx <= 0) return
        result.add(volume.take(twoDotsIdx))
    }

    override fun dispose() {
        namespaceConfigWatcher.dispose()
        setActive(false)
    }

    private fun createNetworkIfNotExists(networkName: String) {
        val networks = dockerApi.getNetworkByName(networkName)
        if (networks == null) {
            dockerApi.createBridgeNetwork(namespaceRef, networkName)
        }
    }
}
