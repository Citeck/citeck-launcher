package ru.citeck.launcher

import androidx.compose.material.Text
import androidx.compose.runtime.*
import androidx.compose.runtime.snapshots.SnapshotStateList
import androidx.compose.ui.Alignment
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.*
import io.github.oshai.kotlinlogging.KotlinLogging
import kotlinx.coroutines.runBlocking
import org.jetbrains.compose.resources.decodeToSvgPainter
import ru.citeck.launcher.core.LauncherServices
import ru.citeck.launcher.core.git.GitPullCancelledException
import ru.citeck.launcher.core.socket.AppLocalSocket
import ru.citeck.launcher.core.utils.AppLock
import ru.citeck.launcher.core.utils.StdOutLog
import ru.citeck.launcher.core.utils.data.DataValue
import ru.citeck.launcher.core.utils.file.CiteckFiles
import ru.citeck.launcher.core.utils.json.Json
import ru.citeck.launcher.core.workspace.WorkspaceDto
import ru.citeck.launcher.view.dialog.*
import ru.citeck.launcher.view.logs.GlobalLogsWindow
import ru.citeck.launcher.view.screen.LoadingScreen
import ru.citeck.launcher.view.screen.NamespaceScreen
import ru.citeck.launcher.view.screen.WelcomeScreen
import ru.citeck.launcher.view.theme.LauncherTheme
import ru.citeck.launcher.view.tray.CiteckSystemTray
import ru.citeck.launcher.view.utils.ImageUtils
import ru.citeck.launcher.view.utils.SystemDumpUtils
import ru.citeck.launcher.view.utils.rememberMutProp
import ru.citeck.launcher.view.window.AdditionalWindowState
import java.util.concurrent.atomic.AtomicBoolean
import kotlin.system.exitProcess

private val log = KotlinLogging.logger {}

fun main(@Suppress("unused") args: Array<String>) {
    // initial phase messages printed without logging framework
    // to avoid conflicts with logging to same file from two apps
    StdOutLog.info("Application starting. Try to take app lock")
    var tryToLockError: Throwable? = null
    try {
        if (!AppLock.tryToLock()) {
            exitProcess(0)
        }
        StdOutLog.info("App lock was successfully acquired.")
    } catch (e: Throwable) {
        StdOutLog.info("Exception occurred while app lock acquisition")
        e.printStackTrace()
        tryToLockError = e
    }
    // initial phase completed
    application {

        val traySupported = remember {
            val traySupported = AtomicBoolean()
            Thread.ofPlatform().start {
                traySupported.set(CiteckSystemTray.initialize())
                if (!traySupported.get()) {
                    log.warn { "Tray is not supported" }
                }
            }
            traySupported
        }

        val state = rememberWindowState(
            width = 1100.dp,
            height = 700.dp,
            position = WindowPosition(Alignment.Center)
        )

        val density = LocalDensity.current
        val logo = remember {
            ImageUtils.load("classpath:logo.svg").decodeToSvgPainter(density)
        }

        val windowVisible = remember { mutableStateOf(true) }
        val additionalWindowStates: SnapshotStateList<AdditionalWindowState> = remember { mutableStateListOf() }

        var launcherVersion: String = remember {
            var version = "unknown"
            try {
                val buildInfoData = CiteckFiles.getFile("classpath:build-info.json").readBytes()
                val buildInfo = Json.read(buildInfoData, DataValue::class)
                version = buildInfo["version"].asText().ifBlank { "unknown" }
            } catch (e: Throwable) {
                log.warn(e) { "Launcher version reading failed" }
            }
            version
        }

        LauncherTheme {
            Window(
                onCloseRequest = {
                    if (traySupported.get()) {
                        additionalWindowStates.forEach { it.closeWindow() }
                        windowVisible.value = false
                    } else {
                        exitApplication()
                    }
                },
                title = "Citeck Launcher v$launcherVersion",
                state = state,
                icon = logo,
                visible = windowVisible.value
            ) {
                LaunchedEffect(Unit) {
                    window.minimumSize = java.awt.Dimension(300, 400)
                }

                val servicesValue = remember {
                    if (tryToLockError != null) {
                        mutableStateOf(Result.failure(tryToLockError))
                    } else {
                        val servicesRes = mutableStateOf<Result<LauncherServices>?>(null)
                        Thread.ofPlatform().start {
                            try {
                                val launcherServices = LauncherServices()
                                runBlocking {
                                    launcherServices.init()
                                }
                                servicesRes.value = Result.success(launcherServices)
                            } catch (e: Exception) {
                                log.error(e) { "Services initialization failed" }
                                servicesRes.value = Result.failure(e)
                            }
                        }
                        servicesRes
                    }
                }

                CiteckDialog.renderDialogs()

                val services = servicesValue.value
                if (services == null) {
                    LoadingScreen()
                } else if (services.isSuccess) {

                    val servicesVal = services.getOrThrow()

                    GlobalLogsWindow.LogsDialog(additionalWindowStates, logo)
                    remember {
                        SystemDumpUtils.init(servicesVal)
                        fun takeFocus() {
                            windowVisible.value = true
                            window.isMinimized = false
                            window.requestFocus()
                            window.toFront()
                        }
                        CiteckSystemTray.listenLmbClick { takeFocus() }
                        AppLocalSocket.listenMessages(AppLocalSocket.TakeFocusCommand::class) { takeFocus() }
                    }
                    App(services.getOrThrow())
                } else {
                    ErrorDialog.show(services.exceptionOrNull()!!) { exitApplication() }
                }
            }
            for (additionalWindowState in additionalWindowStates) {
                additionalWindowState.content.invoke()
            }
        }
    }
}

@Composable
fun App(services: LauncherServices) {

    val error = remember { mutableStateOf<Throwable?>(null) }

    val selectedWorkspace = remember {

        val wsDataState = mutableStateOf<WorkspaceDto?>(null)
        val entitiesService = services.entitiesService

        Thread.ofPlatform().start {
            try {
                val selectedWsId = services.launcherStateService.getSelectedWorkspace()
                var selectedWs = entitiesService.getById(WorkspaceDto::class, selectedWsId)?.entity
                if (selectedWs == null) {
                    selectedWs = entitiesService.getFirst(WorkspaceDto::class)!!.entity
                }
                try {
                    services.setWorkspace(selectedWs.id)
                    wsDataState.value = selectedWs
                } catch (_: GitPullCancelledException) {
                    log.warn { "Git pull cancelled for repo '${selectedWs.id}'. Fallback to default workspace." }
                    services.setWorkspace(WorkspaceDto.DEFAULT.id)
                    wsDataState.value = entitiesService.getFirst(WorkspaceDto::class)?.entity
                        ?: error("Default workspace is null")
                }
            } catch (e: Throwable) {
                log.error(e) { "Exception while selected workspace loading" }
                error.value = e
            }
        }

        wsDataState
    }

    val errorVal = error.value
    if (errorVal != null) {
        ErrorDialog.show(errorVal) { exitProcess(0) }
    } else if (selectedWorkspace.value == null) {
        LoadingScreen()
    } else {
        val workspaceServices = rememberMutProp(services.getWorkspaceServices())
        workspaceServices.value?.let { wsServices ->
            val selectedNamespace = rememberMutProp(wsServices, wsServices.selectedNamespace)
            if (selectedNamespace.value == null) {
                WelcomeScreen(services, selectedWorkspace)
            } else {
                NamespaceScreen(wsServices, selectedNamespace)
            }
        } ?: Text("Selected workspace is empty")
    }
}
