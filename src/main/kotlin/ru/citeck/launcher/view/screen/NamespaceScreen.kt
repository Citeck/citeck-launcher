@file:OptIn(ExperimentalFoundationApi::class)

package ru.citeck.launcher.view.screen

import androidx.compose.foundation.*
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.*
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.em
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import ru.citeck.launcher.core.WorkspaceServices
import ru.citeck.launcher.core.appdef.ApplicationKind
import ru.citeck.launcher.core.config.AppDir
import ru.citeck.launcher.core.logs.AppLogUtils
import ru.citeck.launcher.core.namespace.NamespaceDto
import ru.citeck.launcher.core.namespace.NamespaceEntityDef
import ru.citeck.launcher.core.namespace.runtime.AppRuntime
import ru.citeck.launcher.core.namespace.runtime.AppRuntimeStatus
import ru.citeck.launcher.core.namespace.runtime.NamespaceRuntime
import ru.citeck.launcher.core.namespace.runtime.NsRuntimeStatus
import ru.citeck.launcher.core.namespace.runtime.NsRuntimeStatus.*
import ru.citeck.launcher.core.namespace.volume.VolumeInfo
import ru.citeck.launcher.core.secrets.auth.AuthSecret
import ru.citeck.launcher.view.action.ActionDesc
import ru.citeck.launcher.view.action.ActionIcon
import ru.citeck.launcher.view.action.CiteckIconAction
import ru.citeck.launcher.view.dialog.GlobalErrorDialog
import ru.citeck.launcher.view.dialog.form.components.journal.JournalSelectDialog
import ru.citeck.launcher.view.image.CpImage
import ru.citeck.launcher.view.logs.GlobalLogsWindow
import ru.citeck.launcher.view.logs.LogsDialogParams
import ru.citeck.launcher.view.utils.rememberMutProp
import java.awt.Desktop
import java.awt.Toolkit
import java.awt.datatransfer.StringSelection
import java.net.URI

private val STARTING_STOPPING_COLOR = Color(0xFFF4E909)
private val RUNNING_COLOR = Color(0xFF33AB50)
private val STOPPED_COLOR = Color(0xFF424242)
private val STALLED_COLOR = Color(0xFFDB831D)

@Composable
fun NamespaceScreen(services: WorkspaceServices, selectedNamespace: MutableState<NamespaceDto?>) {

    val coroutineScope = rememberCoroutineScope()

    val selectedNsValue = selectedNamespace.value ?: return

    val nsRuntime = remember(selectedNsValue.id) {
        services.namespacesService.getRuntime(selectedNsValue.id)
    }
    val runtimeStatus = rememberMutProp(nsRuntime, nsRuntime.status)
    val nsActionInProgress = remember { mutableStateOf(false) }

    Row(modifier = Modifier.fillMaxSize()) {
        Column(
            modifier = Modifier.fillMaxHeight()
                .width(300.dp)
                .border(1.dp, Color.Black)
        ) {
            Row(
                modifier = Modifier.fillMaxWidth()
                    .clickable(enabled = runtimeStatus.value == STOPPED) {
                        coroutineScope.launch {
                            val currentRef = NamespaceEntityDef.getRef(selectedNsValue)
                            val newRef = JournalSelectDialog.show(
                                JournalSelectDialog.Params(
                                    NamespaceDto::class,
                                    listOf(currentRef),
                                    false,
                                    entitiesService = services.entitiesService,
                                    closeWhenAllRecordsDeleted = true
                                )
                            ).firstOrNull() ?: currentRef
                            services.setSelectedNamespace(newRef.localId)
                        }
                    }, verticalAlignment = Alignment.CenterVertically
            ) {
                TooltipArea(
                    tooltip = {
                        if (runtimeStatus.value != STOPPED) {
                            Surface(shadowElevation = 4.dp, shape = RoundedCornerShape(4.dp)) {
                                Text(
                                    text = "Please stop all running apps before namespace changing",
                                    modifier = Modifier.padding(8.dp)
                                )
                            }
                        }
                    }
                ) {
                    Column(modifier = Modifier.padding(start = 10.dp, top = 2.dp/*, bottom = 5.dp*/)) {
                        Text(
                            selectedNsValue.name + " (" + selectedNsValue.id + ")",
                            color = MaterialTheme.colorScheme.scrim
                        )
                        Text(selectedNsValue.bundleRef.toString(), fontSize = 0.8.em, color = Color.LightGray)
                    }
                }
            }
            HorizontalDivider()
            Row(modifier = Modifier.fillMaxWidth().padding(top = 5.dp, bottom = 5.dp)) {
                val color = when (runtimeStatus.value) {
                    STOPPING -> STARTING_STOPPING_COLOR
                    STOPPED -> STOPPED_COLOR
                    STARTING -> STARTING_STOPPING_COLOR
                    STALLED -> STALLED_COLOR
                    RUNNING -> RUNNING_COLOR
                }
                Spacer(Modifier.width(10.dp))
                StatusIndicator(color, modifier = Modifier.align(Alignment.CenterVertically))
                Spacer(Modifier.width(10.dp))
                Text(runtimeStatus.value.name, modifier = Modifier.align(Alignment.CenterVertically))
            }
            HorizontalDivider()
            Row(modifier = Modifier.height(30.dp), verticalAlignment = Alignment.CenterVertically) {
                Row(
                    verticalAlignment = Alignment.CenterVertically,
                    modifier = Modifier.weight(0.7f).fillMaxHeight()
                        .clickable(enabled = !nsActionInProgress.value) {
                            nsActionInProgress.value = true
                            Thread.ofPlatform().start {
                                runBlocking {
                                    GlobalErrorDialog.doActionSafe({
                                        nsRuntime.start()
                                    }, { "Namespace start error" }, {})
                                }
                                nsActionInProgress.value = false
                            }
                        }
                ) {
                    CpImage(
                        "icons/start.svg",
                        modifier = Modifier.fillMaxHeight()
                            .padding(start = 7.dp, top = 4.dp, bottom = 4.dp),
                        contentScale = ContentScale.FillHeight
                    )
                    Text("Update&Start", modifier = Modifier.padding(start = 5.dp))
                }
                VerticalDivider()
                Row(
                    verticalAlignment = Alignment.CenterVertically,
                    modifier = Modifier.weight(0.3f).fillMaxHeight()
                        .clickable(enabled = !nsActionInProgress.value && runtimeStatus.value != STOPPED) {
                            nsActionInProgress.value = true
                            Thread.ofPlatform().start {
                                runBlocking {
                                    GlobalErrorDialog.doActionSafe({
                                        nsRuntime.stop()
                                    }, { "Namespace stop error" }, {})
                                }
                                nsActionInProgress.value = false
                            }
                        }) {
                    CpImage(
                        "icons/stop.svg",
                        modifier = Modifier.fillMaxHeight()
                            .padding(start = 5.dp, top = 4.dp, bottom = 4.dp),
                        contentScale = ContentScale.FillHeight
                    )
                    Text("Stop", modifier = Modifier.padding(start = 5.dp))
                }
            }
            /*val logoPainter = remember {
                ImageUtils.loadPng("classpath:logo.svg", 512f)
                    .decodeToImageBitmap()
                    .asSkiaBitmap()
                    .asImage()
                    .asPainter(PlatformContext.INSTANCE)
            }*/
            TooltipArea(
                tooltip = {
                    val text = when (runtimeStatus.value) {
                        STARTING -> "The application is starting. Please wait..."
                        STOPPING, STOPPED -> "The application is not running. Start it to open in the browser."
                        STALLED -> "The application is stalled. Please try to start it again."
                        RUNNING -> ""
                    }
                    if (text.isNotEmpty()) {
                        Surface(shadowElevation = 4.dp, shape = RoundedCornerShape(4.dp)) {
                            Text(text = text, modifier = Modifier.padding(8.dp))
                        }
                    }
                }
            ) {
                Row(
                    modifier = Modifier.fillMaxWidth().border(1.dp, Color.LightGray)
                        .clickable(enabled = runtimeStatus.value == RUNNING) {
                            Desktop.getDesktop().browse(URI.create("http://localhost"))
                        },
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    CpImage("logo.svg",
                        modifier = Modifier.padding(start = 7.dp, top = 5.dp, bottom = 5.dp)
                            .requiredSize(40.dp)
                    )
                    Text("Open In Browser", modifier = Modifier.padding(start = 10.dp))
                }
            }
            Spacer(Modifier.weight(1f))
            HorizontalDivider()
            Spacer(modifier = Modifier.height(14.dp))
            Row(modifier = Modifier.height(30.dp).padding(start = 10.dp, bottom = 15.dp)) {
                TooltipArea(
                    modifier = Modifier.fillMaxHeight(),
                    tooltip = {
                        if (runtimeStatus.value != STOPPED) {
                            Surface(shadowElevation = 4.dp, shape = RoundedCornerShape(4.dp)) {
                                Text(
                                    text = "Please stop all running apps before returning to the welcome screen",
                                    modifier = Modifier.padding(8.dp)
                                )
                            }
                        }
                    }
                ) {
                    CiteckIconAction(
                        coroutineScope,
                        enabled = runtimeStatus.value == STOPPED,
                        modifier = Modifier.fillMaxHeight(),
                        ActionDesc(
                            "back-to-workspaces",
                            ActionIcon.ARROW_LEFT,
                            "Open Launcher Dir"
                        ) {
                            services.setSelectedNamespace("")
                        }
                    )
                }
                Spacer(modifier = Modifier.width(10.dp))
                CiteckIconAction(
                    coroutineScope,
                    modifier = Modifier.fillMaxHeight(),
                    actionDesc = ActionDesc(
                        "open-launcher-dir",
                        ActionIcon.OPEN_DIR,
                        "Open Launcher Dir"
                    ) { Desktop.getDesktop().open(AppDir.PATH.toFile()) }
                )
                CiteckIconAction(
                    coroutineScope,
                    modifier = Modifier.fillMaxHeight(),
                    actionDesc = ActionDesc(
                        "show-launcher-logs",
                        ActionIcon.LOGS,
                        "Show Launcher Logs"
                    ) {
                        runCatching {
                            GlobalLogsWindow.show(
                                LogsDialogParams("Launcher Logs", 5000) { logsCallback ->
                                    runCatching {
                                        AppLogUtils.watchAppLogs { logsCallback.invoke(it) }
                                    }
                                }
                            )
                        }
                    }
                )
                CiteckIconAction(
                    coroutineScope,
                    modifier = Modifier.fillMaxHeight(),
                    actionDesc = ActionDesc(
                        "show-volumes",
                        ActionIcon.STORAGE,
                        "Show Volumes"
                    ) {
                        runCatching {
                            JournalSelectDialog.show(
                                JournalSelectDialog.Params(
                                    VolumeInfo::class,
                                    emptyList(),
                                    false,
                                    services.entitiesService,
                                    false,
                                    selectable = false
                                )
                            )
                        }
                    }
                )
                CiteckIconAction(
                    coroutineScope,
                    modifier = Modifier.fillMaxHeight(),
                    actionDesc = ActionDesc(
                        "open-secrets-list",
                        ActionIcon.KEY,
                        "Show Auth Secrets"
                    ) {
                        runCatching {
                            JournalSelectDialog.show(
                                JournalSelectDialog.Params(
                                    AuthSecret::class,
                                    emptyList(),
                                    false,
                                    services.entitiesService,
                                    true,
                                    selectable = false
                                )
                            )
                        }
                    }
                )
            }
        }
        Column(modifier = Modifier.fillMaxHeight().border(1.dp, Color.Black)) {
            val scrollState = rememberScrollState()
            Column(modifier = Modifier.fillMaxHeight().verticalScroll(scrollState).padding(start = 6.dp, end = 6.dp)) {
                val appsByKind = rememberMutProp(nsRuntime, nsRuntime.appRuntimes) {
                    val appsByKind = HashMap<ApplicationKind, MutableList<AppRuntime>>()
                    nsRuntime.appRuntimes.value.forEach {
                        appsByKind.computeIfAbsent(it.def.value.kind) { ArrayList() }.add(it)
                    }
                    appsByKind.values.forEach {
                        it.sortWith { r1, r2 -> r1.name.compareTo(r2.name) }
                    }
                    appsByKind
                }
                renderApps(
                    runtimeStatus,
                    "Citeck Core",
                    appsByKind.value[ApplicationKind.CITECK_CORE],
                    coroutineScope
                )
                renderApps(
                    runtimeStatus,
                    "Citeck Additional",
                    appsByKind.value[ApplicationKind.CITECK_ADDITIONAL],
                    coroutineScope
                )
                renderApps(
                    runtimeStatus,
                    "Third Party",
                    appsByKind.value[ApplicationKind.THIRD_PARTY],
                    coroutineScope
                )
            }
        }
    }
}

@Composable
private fun renderApps(
    runtimeStatus: MutableState<NsRuntimeStatus>,
    header: String,
    applications: List<AppRuntime>?,
    coroutineScope: CoroutineScope
) {
    Text(
        header,
        fontSize = 1.1.em,
        fontWeight = FontWeight.Bold,
        maxLines = 1,
        modifier = Modifier.padding(start = 5.dp, top = 10.dp, bottom = 10.dp)
    )
    Column(modifier = Modifier.padding(start = 5.dp, end = 5.dp)) {

        Row(modifier = Modifier.fillMaxWidth()) {
            Text("Name", modifier = Modifier.weight(0.8f), maxLines = 1)
            Text("Status", modifier = Modifier.weight(1f), maxLines = 1)
            Text("Tag", modifier = Modifier.width(200.dp), maxLines = 1)
            Text("Actions", modifier = Modifier.weight(0.5f), maxLines = 1)
        }

        HorizontalDivider()

        if (applications != null) {
            for (application in applications) {
                val statusText = rememberMutProp(application, application.statusText)
                val appStatus = rememberMutProp(application, application.status)
                Row(modifier = Modifier.fillMaxWidth().height(30.dp), verticalAlignment = Alignment.CenterVertically) {
                    Text(application.name, modifier = Modifier.weight(0.8f), maxLines = 1)
                    Row(
                        modifier = Modifier.weight(1f).fillMaxHeight(),
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        val statusColor = if (NamespaceRuntime.STALLED_APP_STATUSES.contains(appStatus.value)) {
                            STALLED_COLOR
                        } else when (appStatus.value) {
                            AppRuntimeStatus.STOPPED -> STOPPED_COLOR
                            AppRuntimeStatus.RUNNING -> RUNNING_COLOR
                            else -> STARTING_STOPPING_COLOR
                        }
                        Text(appStatus.value.toString(), color = statusColor, maxLines = 1)
                        Spacer(Modifier.width(5.dp))
                        Text(statusText.value, maxLines = 1, overflow = TextOverflow.Ellipsis)
                    }
                    TooltipArea(
                        modifier = Modifier.width(200.dp)/*.weight(0.5f)*/,
                        tooltip = {
                            Surface(shadowElevation = 4.dp, shape = RoundedCornerShape(4.dp)) {
                                Text(
                                    text = application.image,
                                    modifier = Modifier.padding(8.dp)
                                )
                            }
                        }
                    ) {
                        Text(
                            text = application.image.substringAfterLast(":", "unknown"),
                            modifier = Modifier.clickable(onClick = {
                                Toolkit.getDefaultToolkit()
                                    .systemClipboard
                                    .setContents(StringSelection(application.image), null)
                                }
                            ),
                            maxLines = 1
                        )
                    }
                    Row(
                        modifier = Modifier.weight(0.5f).fillMaxHeight(),
                        horizontalArrangement = Arrangement.Start,
                        verticalAlignment = Alignment.CenterVertically
                    ) {
                        if (!appStatus.value.isStoppingState()) {
                            CiteckIconAction(
                                coroutineScope,
                                modifier = Modifier.fillMaxHeight(),
                                actionDesc = ActionDesc(
                                    "stop-app",
                                    ActionIcon.STOP,
                                    "Stop application"
                                ) {
                                    application.stop(manual = true)
                                }
                            )
                        } else if (!runtimeStatus.value.isStoppingState()) {
                            CiteckIconAction(
                                coroutineScope,
                                modifier = Modifier.fillMaxHeight(),
                                actionDesc = ActionDesc(
                                    "start-app",
                                    ActionIcon.START,
                                    "Start application"
                                ) {
                                    application.start()
                                }
                            )
                        }
                        if (appStatus.value != AppRuntimeStatus.STOPPED) {
                            CiteckIconAction(
                                coroutineScope,
                                modifier = Modifier.fillMaxHeight(),
                                actionDesc = ActionDesc(
                                    "show-logs",
                                    ActionIcon.LOGS,
                                    "Show Logs"
                                ) {
                                    runCatching {
                                        GlobalLogsWindow.show(
                                            LogsDialogParams(application.name, 5000) { logsCallback ->
                                                runCatching {
                                                    application.watchLogs(5000, logsCallback)
                                                }
                                            }
                                        )
                                    }
                                }
                            )
                        }
                    }
                }
                HorizontalDivider()
            }
        }
    }
}

@Composable
fun StatusIndicator(
    color: Color,
    borderColor: Color = Color.Black,
    size: Dp = 20.dp,
    borderWidth: Dp = 1.dp,
    modifier: Modifier = Modifier,
) {
    Box(
        modifier = modifier
            .size(size)
            .border(borderWidth, borderColor, CircleShape)
            .background(color, CircleShape)
    )
}
