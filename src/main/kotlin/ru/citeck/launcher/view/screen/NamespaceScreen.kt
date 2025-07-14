package ru.citeck.launcher.view.screen

import androidx.compose.foundation.*
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.pointer.PointerEventType
import androidx.compose.ui.input.pointer.isSecondaryPressed
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.DpOffset
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.em
import io.github.oshai.kotlinlogging.KotlinLogging
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import ru.citeck.launcher.core.WorkspaceServices
import ru.citeck.launcher.core.appdef.ApplicationKind
import ru.citeck.launcher.core.config.AppDir
import ru.citeck.launcher.core.logs.AppLogUtils
import ru.citeck.launcher.core.namespace.NamespaceConfig
import ru.citeck.launcher.core.namespace.NamespaceEntityDef
import ru.citeck.launcher.core.namespace.NamespacesService
import ru.citeck.launcher.core.namespace.runtime.AppRuntime
import ru.citeck.launcher.core.namespace.runtime.AppRuntimeStatus
import ru.citeck.launcher.core.namespace.runtime.NsRuntimeStatus
import ru.citeck.launcher.core.namespace.runtime.NsRuntimeStatus.*
import ru.citeck.launcher.core.namespace.volume.VolumeInfo
import ru.citeck.launcher.core.secrets.auth.AuthSecret
import ru.citeck.launcher.core.utils.ActionStatus
import ru.citeck.launcher.core.utils.CompressionAlg
import ru.citeck.launcher.core.utils.file.FileUtils
import ru.citeck.launcher.view.action.ActionDesc
import ru.citeck.launcher.view.action.ActionIcon
import ru.citeck.launcher.view.action.CiteckIconAction
import ru.citeck.launcher.view.commons.CiteckTooltipArea
import ru.citeck.launcher.view.dialog.AppDefEditDialog
import ru.citeck.launcher.view.dialog.GlobalErrorDialog
import ru.citeck.launcher.view.dialog.GlobalLoadingDialog
import ru.citeck.launcher.view.drawable.CpImage
import ru.citeck.launcher.view.form.components.journal.JournalSelectDialog
import ru.citeck.launcher.view.form.exception.FormCancelledException
import ru.citeck.launcher.view.logs.GlobalLogsWindow
import ru.citeck.launcher.view.logs.LogsDialogParams
import ru.citeck.launcher.view.utils.SystemDumpUtils
import ru.citeck.launcher.view.utils.rememberMutProp
import java.awt.Desktop
import java.awt.Toolkit
import java.awt.datatransfer.StringSelection
import java.net.URI

private val STARTING_STOPPING_COLOR = Color(0xFFF4E909)
private val RUNNING_COLOR = Color(0xFF33AB50)
private val STOPPED_COLOR = Color(0xFF424242)
private val STALLED_COLOR = Color(0xFFDB831D)

private val log = KotlinLogging.logger {}

@Composable
fun NamespaceScreen(services: WorkspaceServices, selectedNamespace: MutableState<NamespaceConfig?>) {

    val coroutineScope = rememberCoroutineScope()

    val selectedNsValue = selectedNamespace.value ?: return

    val nsRuntime = remember(selectedNsValue.id) {
        services.namespacesService.getRuntime(selectedNsValue.id)
    }
    val nsGenRes = rememberMutProp(nsRuntime.namespaceGenResp)
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
                                    NamespaceConfig::class,
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
                CiteckTooltipArea(
                    tooltip = "Please stop all running apps before namespace changing",
                    enabled = runtimeStatus.value != STOPPED
                ) {
                    Column(modifier = Modifier.padding(start = 10.dp, top = 2.dp)) {
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
                val rmbStartDropDownShow = remember { mutableStateOf(false) }
                var rmbStartDropDownOffset by remember { mutableStateOf(DpOffset.Zero) }
                Row(
                    verticalAlignment = Alignment.CenterVertically,
                    modifier = Modifier.weight(0.7f).fillMaxHeight()
                        .pointerInput(Unit) {
                            awaitPointerEventScope {
                                while (true) {
                                    val event = awaitPointerEvent()
                                    val pointer = event.changes.firstOrNull()
                                    if (nsActionInProgress.value) {
                                        continue
                                    }
                                    if (event.type == PointerEventType.Press &&
                                        event.buttons.isSecondaryPressed &&
                                        pointer != null
                                    ) {
                                        val position = pointer.position
                                        rmbStartDropDownOffset = DpOffset(position.x.dp, position.y.dp)
                                        rmbStartDropDownShow.value = true
                                        pointer.consume()
                                    }
                                }
                            }
                        }
                        .clickable(enabled = !nsActionInProgress.value) {
                            nsActionInProgress.value = true
                            Thread.ofPlatform().start {
                                runBlocking {
                                    GlobalErrorDialog.doActionSafe({
                                        nsRuntime.updateAndStart(false)
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
                DropdownMenu(
                    expanded = rmbStartDropDownShow.value,
                    offset = rmbStartDropDownOffset,
                    onDismissRequest = { rmbStartDropDownShow.value = false }
                ) {
                    DropdownMenuItem(
                        text = { Text("Force Update And Start") },
                        onClick = {
                            nsActionInProgress.value = true
                            rmbStartDropDownShow.value = false
                            Thread.ofPlatform().start {
                                try {
                                    runBlocking {
                                        GlobalErrorDialog.doActionSafe({
                                            nsRuntime.updateAndStart(true)
                                        }, { "Namespace start error" }, {})
                                    }
                                } finally {
                                    nsActionInProgress.value = false
                                }
                            }
                        }
                    )
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
            val tooltipText = when (runtimeStatus.value) {
                STARTING -> "The application is starting. Please wait..."
                STOPPING, STOPPED -> "The application is not running. Start it to open in the browser."
                STALLED -> "The application is stalled. Please try to start it again."
                RUNNING -> "Open Citeck in your browser.\n Default username: admin\n Default password: admin"
            }
            val linkTextPadding = 55.dp
            CiteckTooltipArea(
                tooltip = tooltipText
            ) {
                Box(
                    modifier = Modifier.fillMaxWidth().border(1.dp, Color.LightGray)
                        .clickable(enabled = runtimeStatus.value == RUNNING) {
                            Desktop.getDesktop().browse(URI.create("http://localhost"))
                        }
                ) {
                    CpImage(
                        "logo.svg",
                        modifier = Modifier.align(Alignment.CenterStart)
                            .padding(start = 7.dp, top = 5.dp, bottom = 5.dp)
                            .requiredSize(40.dp)
                    )
                    Text("Open In Browser", modifier = Modifier.align(Alignment.CenterStart)
                        .padding(start = linkTextPadding))
                }
            }
            Spacer(Modifier.height(2.dp))
            HorizontalDivider()
            for (link in (nsGenRes.value?.links ?: emptyList())) {
                CiteckTooltipArea(
                    tooltip = link.description
                ) {
                    Box(
                        modifier = Modifier.fillMaxWidth()
                            .clickable(enabled = runtimeStatus.value == RUNNING) {
                                Desktop.getDesktop().browse(URI.create(link.url))
                            }
                    ) {
                        Box(modifier = Modifier.align(Alignment.CenterStart).padding(start = 12.dp, top = 5.dp, bottom = 5.dp)) {
                            CpImage(
                                link.icon,
                                modifier = Modifier.align(Alignment.CenterStart).requiredSize(30.dp),
                            )
                        }
                        Text(
                            link.name,
                            modifier = Modifier.align(Alignment.CenterStart)
                                .padding(start = linkTextPadding)
                        )
                    }
                }
                HorizontalDivider()
            }

            Spacer(Modifier.weight(1f))
            HorizontalDivider()
            Spacer(modifier = Modifier.height(14.dp))
            Row(modifier = Modifier.height(30.dp).padding(start = 10.dp, bottom = 15.dp)) {
                CiteckTooltipArea(
                    tooltip = "Please stop all running apps before returning to the welcome screen",
                    enabled = runtimeStatus.value != STOPPED,
                    modifier = Modifier.fillMaxHeight()
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
                val rmbVolumesDropDownShow = remember { mutableStateOf(false) }
                var rmbVolumesDropDownOffset by remember { mutableStateOf(DpOffset.Zero) }
                CiteckIconAction(
                    coroutineScope,
                    modifier = Modifier.fillMaxHeight().pointerInput(Unit) {
                        awaitPointerEventScope {
                            while (true) {
                                val event = awaitPointerEvent()
                                val pointer = event.changes.firstOrNull()
                                if (nsActionInProgress.value) {
                                    continue
                                }
                                if (event.type == PointerEventType.Press &&
                                    event.buttons.isSecondaryPressed &&
                                    pointer != null
                                ) {
                                    val position = pointer.position
                                    rmbVolumesDropDownOffset = DpOffset(position.x.dp, position.y.dp)
                                    rmbVolumesDropDownShow.value = true
                                    pointer.consume()
                                }
                            }
                        }
                    },
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
                DropdownMenu(
                    expanded = rmbVolumesDropDownShow.value,
                    offset = rmbVolumesDropDownOffset,
                    onDismissRequest = { rmbVolumesDropDownShow.value = false }
                ) {
                    DropdownMenuItem(
                        text = { Text("Create volumes snapshot") },
                        onClick = {
                            nsActionInProgress.value = true
                            val status = ActionStatus.Mut()
                            val closeLoading = GlobalLoadingDialog.show(status)
                            Thread.ofPlatform().start {
                                runBlocking {
                                    GlobalErrorDialog.doActionSafe<Unit>({
                                        val snapshotsDir = NamespacesService.getNamespaceDir(nsRuntime.namespaceRef)
                                            .resolve("snapshots")
                                        services.dockerApi.exportSnapshot(
                                            nsRuntime.namespaceRef,
                                            snapshotsDir.resolve(FileUtils.createNameWithCurrentDateTime()),
                                            CompressionAlg.XZ,
                                            status
                                        )
                                        Desktop.getDesktop().open(snapshotsDir.toFile())
                                    }, { "Snapshot creation failed" }, {})
                                }
                                nsActionInProgress.value = false
                                closeLoading()
                            }
                            rmbVolumesDropDownShow.value = false
                        }
                    )
                }
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
                CiteckIconAction(
                    coroutineScope,
                    modifier = Modifier.fillMaxHeight(),
                    actionDesc = ActionDesc(
                        "feedback",
                        ActionIcon.EXCLAMATION_TRIANGLE,
                        "Export System Info"
                    ) {
                        SystemDumpUtils.dumpSystemInfo()
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
                    "Citeck Core Extensions",
                    appsByKind.value[ApplicationKind.CITECK_CORE_EXTENSION],
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
    if (applications.isNullOrEmpty()) {
        return
    }

    val portsWidth = 80.dp
    val tagWidth = 180.dp
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
            Text("Ports", modifier = Modifier.width(portsWidth), maxLines = 1)
            Text("Tag", modifier = Modifier.width(tagWidth), maxLines = 1)
            Text("Actions", modifier = Modifier.weight(0.5f), maxLines = 1)
        }

        HorizontalDivider()

        for (application in applications) {
            val statusText = rememberMutProp(application, application.statusText)
            val appStatus = rememberMutProp(application, application.status)
            val editedDef = rememberMutProp(application, application.editedDef)
            val appDef = rememberMutProp(application, application.def)
            val ports = remember(appDef) {
                appDef.value.ports.mapNotNull {
                    var port = it.substringBefore(":", "")
                    if (port.startsWith("!")) {
                        port = port.substring(1)
                    }
                    port.ifEmpty { null }
                }
            }
            Row(modifier = Modifier.fillMaxWidth().height(30.dp), verticalAlignment = Alignment.CenterVertically) {
                Text(application.name, modifier = Modifier.weight(0.8f), maxLines = 1)
                Row(
                    modifier = Modifier.weight(1f).fillMaxHeight(),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    val statusColor = if (appStatus.value.isStalledState()) {
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
                if (ports.isEmpty()) {
                    Row(modifier = Modifier.width(portsWidth)) {}
                } else if (ports.size == 1) {
                    Text(text = ports.first().toString(), modifier = Modifier.width(portsWidth))
                } else {
                    CiteckTooltipArea(
                        tooltip = ports.joinToString("\n"),
                        modifier = Modifier.width(portsWidth)
                    ) {
                        Text(
                            text = ports.first().toString() + " ..",
                            maxLines = 1
                        )
                    }
                }
                CiteckTooltipArea(
                    tooltip = application.image,
                    modifier = Modifier.width(tagWidth),
                ) {
                    val image = appDef.value.image
                    Text(
                        text = image.substringAfterLast(":", "unknown"),
                        modifier = Modifier.clickable(onClick = {
                            Toolkit.getDefaultToolkit()
                                .systemClipboard
                                .setContents(StringSelection(image), null)
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
                    }
                    if (!runtimeStatus.value.isStoppingState() && !appStatus.value.isStartingState()) {
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
                    Box {
                        CiteckIconAction(
                            coroutineScope,
                            modifier = Modifier.fillMaxHeight(),
                            actionDesc = ActionDesc(
                                "edit-app",
                                ActionIcon.COG_6_TOOTH,
                                "Edit App"
                            ) {
                                runCatching {
                                    val appDef = application.def.value
                                    try {
                                        val editRes = AppDefEditDialog.show(
                                            appDef,
                                            application.nsRuntime.isEditedAndLockedApp(appDef.name)
                                        )
                                        if (editRes == null) {
                                            application.nsRuntime.resetAppDef(appDef.name)
                                        } else {
                                            application.nsRuntime.updateAppDef(
                                                appDef,
                                                editRes.appDef,
                                                editRes.locked
                                            )
                                        }
                                    } catch (_: FormCancelledException) {
                                        // do nothing
                                    }
                                }
                            }
                        )
                        if (editedDef.value) {
                            Box(
                                modifier = Modifier
                                    .size(10.dp)
                                    .align(Alignment.TopEnd)
                                    .background(Color.Blue, CircleShape)
                            )
                        }
                    }
                }
            }
            HorizontalDivider()
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
