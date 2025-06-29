package ru.citeck.launcher

import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Typography
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.*
import androidx.compose.runtime.snapshots.SnapshotStateList
import androidx.compose.ui.Alignment
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.*
import io.github.oshai.kotlinlogging.KotlinLogging
import kotlinx.coroutines.runBlocking
import org.jetbrains.compose.resources.decodeToSvgPainter
import ru.citeck.launcher.core.LauncherServices
import ru.citeck.launcher.core.socket.AppLocalSocket
import ru.citeck.launcher.core.utils.AppLock
import ru.citeck.launcher.core.workspace.WorkspaceDto
import ru.citeck.launcher.view.dialog.*
import ru.citeck.launcher.view.form.GlobalFormDialog
import ru.citeck.launcher.view.form.WorkspaceFormDialog
import ru.citeck.launcher.view.form.components.journal.JournalSelectDialog
import ru.citeck.launcher.view.logs.GlobalLogsWindow
import ru.citeck.launcher.view.screen.LoadingScreen
import ru.citeck.launcher.view.screen.NamespaceScreen
import ru.citeck.launcher.view.screen.WelcomeScreen
import ru.citeck.launcher.view.theme.LauncherTheme
import ru.citeck.launcher.view.tray.CiteckSystemTray
import ru.citeck.launcher.view.utils.ImageUtils
import ru.citeck.launcher.view.utils.rememberMutProp
import ru.citeck.launcher.view.window.AdditionalWindowState
import java.time.Instant
import java.time.temporal.ChronoUnit
import java.util.concurrent.atomic.AtomicBoolean
import kotlin.system.exitProcess

private val log = KotlinLogging.logger {}

fun main(@Suppress("UNUSED_PARAMETER") args: Array<String>) {
    // initial phase messages printed without logging framework
    // to avoid conflicts with logging to same file from two apps
    printLogMsg("Application starting. Try to take app lock")
    var tryToLockError: Throwable? = null
    try {
        if (!AppLock.tryToLock()) {
            exitProcess(0)
        }
        printLogMsg("App lock was successfully acquired.")
    } catch (e: Throwable) {
        printLogMsg("Exception occurred while app lock acquisition")
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
        val dialogStates: SnapshotStateList<CiteckDialogState> = remember { mutableStateListOf() }
        val additionalWindowStates: SnapshotStateList<AdditionalWindowState> = remember { mutableStateListOf() }

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
                title = "Citeck Launcher",
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
                val services = servicesValue.value
                if (services == null) {
                    LoadingScreen()
                } else if (services.isSuccess) {
                    GlobalConfirmDialog.ConfirmDialog(dialogStates)
                    GlobalErrorDialog.ErrorDialog(dialogStates)
                    val servicesVal = services.getOrThrow()
                    GlobalFormDialog.FormDialog(dialogStates, servicesVal.entitiesService)
                    JournalSelectDialog.JournalDialog(dialogStates, servicesVal.entitiesService)
                    GlobalMessageDialog.MessageDialog(dialogStates)
                    AskMasterPasswordDialog.AskMasterPwd(dialogStates)
                    CreateMasterPasswordDialog.CreateMasterPwd(dialogStates)
                    GlobalLogsWindow.LogsDialog(additionalWindowStates, logo)
                    remember {
                        fun takeFocus() {
                            windowVisible.value = true
                            window.isMinimized = false
                            window.requestFocus()
                            window.toFront()
                        }
                        CiteckSystemTray.listenLmbClick { takeFocus() }
                        AppLocalSocket.listenMessages(AppLocalSocket.TakeFocusCommand::class) { takeFocus() }
                    }
                    App(services.getOrThrow(), dialogStates)
                } else {
                    GlobalErrorDialog.ErrorDialog(
                        dialogStates,
                        GlobalErrorDialog.Params(services.exceptionOrNull()!!, onClose = ::exitApplication)
                    )
                }
                for (dialogState in dialogStates) {
                    dialogState.content.invoke()
                }
            }
            for (additionalWindowState in additionalWindowStates) {
                additionalWindowState.content.invoke()
            }
        }
    }
}

@Composable
fun App(services: LauncherServices, dialogStates: SnapshotStateList<CiteckDialogState>) {

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
                services.setWorkspace(selectedWs.id)
                wsDataState.value = selectedWs
            } catch (e: Throwable) {
                log.error(e) { "Exception while selected namespace loading" }
                error.value = e
            }
        }

        wsDataState
    }

    if (error.value != null) {
        GlobalErrorDialog.ErrorDialog(dialogStates, GlobalErrorDialog.Params(error.value!!) {
            exitProcess(0)
        })
    } else if (selectedWorkspace.value == null) {
        LoadingScreen()
    } else {
        val workspaceServices = services.getWorkspaceServices()
        WorkspaceFormDialog.FormDialog(dialogStates, workspaceServices.entitiesService, workspaceServices)
        val selectedNamespace = rememberMutProp(workspaceServices, workspaceServices.selectedNamespace)
        if (selectedNamespace.value == null) {
            WelcomeScreen(services, selectedWorkspace)
        } else {
            NamespaceScreen(workspaceServices, selectedNamespace)
        }
    }
}

private fun printLogMsg(msg: String) {
    val time = Instant.now().truncatedTo(ChronoUnit.MILLIS).toString()
    println("${time.substring(0, time.length - 1)} [${Thread.currentThread().name}] INFO - $msg")
}
