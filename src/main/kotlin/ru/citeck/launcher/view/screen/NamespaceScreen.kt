package ru.citeck.launcher.view.screen

import androidx.compose.foundation.*
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.VerticalDivider
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.em
import androidx.compose.ui.unit.sp
import io.github.oshai.kotlinlogging.KotlinLogging
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import org.apache.commons.io.FilenameUtils
import ru.citeck.launcher.core.WorkspaceServices
import ru.citeck.launcher.core.appdef.ApplicationKind
import ru.citeck.launcher.core.logs.AppLogUtils
import ru.citeck.launcher.core.namespace.NamespaceConfig
import ru.citeck.launcher.core.namespace.NamespaceEntityDef
import ru.citeck.launcher.core.namespace.NamespacesService
import ru.citeck.launcher.core.namespace.gen.GlobalLinks
import ru.citeck.launcher.core.namespace.runtime.AppRuntime
import ru.citeck.launcher.core.namespace.runtime.AppRuntimeStatus
import ru.citeck.launcher.core.namespace.runtime.NsRuntimeStatus.*
import ru.citeck.launcher.core.namespace.volume.VolumeInfo
import ru.citeck.launcher.core.secrets.auth.AuthSecret
import ru.citeck.launcher.view.action.ActionIcon
import ru.citeck.launcher.view.action.IconBtn
import ru.citeck.launcher.view.commons.CiteckTooltipArea
import ru.citeck.launcher.view.commons.ContextMenu
import ru.citeck.launcher.view.commons.ContextMenu.contextMenu
import ru.citeck.launcher.view.commons.LimitedText
import ru.citeck.launcher.view.commons.dialog.ConfirmDialog
import ru.citeck.launcher.view.commons.dialog.ErrorDialog
import ru.citeck.launcher.view.dialog.AppCfgEditWindow
import ru.citeck.launcher.view.dialog.SnapshotsDialog
import ru.citeck.launcher.view.drawable.CpImage
import ru.citeck.launcher.view.form.components.journal.JournalSelectDialog
import ru.citeck.launcher.view.form.exception.FormCancelledException
import ru.citeck.launcher.view.logs.LogsDialogParams
import ru.citeck.launcher.view.logs.LogsWindow
import ru.citeck.launcher.view.utils.SystemDumpUtils
import ru.citeck.launcher.view.utils.rememberMutProp
import java.awt.Desktop
import java.awt.Toolkit
import java.awt.datatransfer.StringSelection
import java.net.URI

private val log = KotlinLogging.logger {}

private val STARTING_STOPPING_COLOR = Color(0xFFF4E909)
private val RUNNING_COLOR = Color(0xFF33AB50)
private val STOPPED_COLOR = Color(0xFF424242)
private val STALLED_COLOR = Color(0xFFDB831D)

private val EDITABLE_FILE_EXTENSIONS = setOf(
    "yaml", "yml", "json", "kt", "java", "js", "lua", "Dockerfile", "sh", "txt", "conf"
)

@Composable
fun NamespaceScreen(
    services: WorkspaceServices,
    selectedNamespace: MutableState<NamespaceConfig?>
) {

    val coroutineScope = rememberCoroutineScope()

    val selectedNsValue = selectedNamespace.value ?: return

    val nsRuntime = remember(selectedNsValue.id) {
        services.namespacesService.getRuntime(selectedNsValue.id)
    }
    val nsGenRes = rememberMutProp(nsRuntime.namespaceGenResp)
    val runtimeStatus = rememberMutProp(nsRuntime, nsRuntime.nsStatus)
    val nsActionInProgress = remember { mutableStateOf(false) }

    Row(modifier = Modifier.fillMaxSize()) {
        Column(
            modifier = Modifier.fillMaxHeight()
                .width(300.dp)
        ) {
            Row(
                modifier = Modifier.fillMaxWidth(),
                verticalAlignment = Alignment.CenterVertically
            ) {
                Column(
                    modifier = Modifier.padding(start = 10.dp, top = 2.dp)
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
                                try {
                                    services.setSelectedNamespace(newRef.localId)
                                } catch (e: Throwable) {
                                    log.error(e) { "Namespace selection failed. Namespace: ${newRef.localId}" }
                                    ErrorDialog.show(e)
                                }
                            }
                        }
                ) {
                    Row {
                        LimitedText(selectedNsValue.name, maxWidth = 170.dp)
                        Text(" (" + selectedNsValue.id + ")")
                    }
                    Text(selectedNsValue.bundleRef.toString(), fontSize = 0.8.em, color = Color.Gray)
                }
                CpImage(
                    "icons/cog-6-tooth.svg",
                    modifier = Modifier.width(28.dp)
                        .height(29.dp)
                        .align(Alignment.Top)
                        .padding(start = 5.dp, top = 2.dp)
                        .clickable {
                            Thread.ofPlatform().name("ns-edit-thread").start {
                                try {
                                    runBlocking {
                                        services.entitiesService.edit(selectedNamespace.value!!)
                                    }
                                } catch (_: FormCancelledException) {
                                    // do nothing
                                }
                            }
                        }
                )
            }
            HorizontalDivider()
            Row(
                modifier = Modifier.fillMaxWidth().height(30.dp).padding(top = 5.dp, bottom = 5.dp),
                verticalAlignment = Alignment.CenterVertically
            ) {
                val color = when (runtimeStatus.value) {
                    STOPPING -> STARTING_STOPPING_COLOR
                    STOPPED -> STOPPED_COLOR
                    STARTING -> STARTING_STOPPING_COLOR
                    STALLED -> STALLED_COLOR
                    RUNNING -> RUNNING_COLOR
                }
                Spacer(Modifier.width(10.dp))
                StatusIndicator(color)
                Spacer(Modifier.width(10.dp))
                Text(
                    runtimeStatus.value.name,
                    fontSize = 1.em,
                    lineHeight = 1.em
                )
            }
            HorizontalDivider()

            val nsStats = rememberMutProp(nsRuntime, nsRuntime.namespaceStats)
            NamespaceStatsSummary(nsStats.value)

            HorizontalDivider()
            Row(modifier = Modifier.height(30.dp), verticalAlignment = Alignment.CenterVertically) {
                Row(
                    verticalAlignment = Alignment.CenterVertically,
                    modifier = Modifier.weight(0.7f).fillMaxHeight()
                        .contextMenu(
                            ContextMenu.Button.RMB,
                            listOf(
                                ContextMenu.Item("Force Update And Start") {
                                    nsActionInProgress.value = true
                                    ErrorDialog.doActionSafe({
                                        nsRuntime.updateAndStart(true)
                                    }, { "Namespace start error" }, {})
                                    nsActionInProgress.value = false
                                }
                            )
                        )
                        .clickable(enabled = !nsActionInProgress.value) {
                            nsActionInProgress.value = true
                            Thread.ofPlatform().start {
                                runBlocking {
                                    ErrorDialog.doActionSafe({
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
                VerticalDivider()
                Row(
                    verticalAlignment = Alignment.CenterVertically,
                    modifier = Modifier.weight(0.3f).fillMaxHeight()
                        .clickable(enabled = !nsActionInProgress.value && runtimeStatus.value != STOPPED) {
                            nsActionInProgress.value = true
                            Thread.ofPlatform().start {
                                runBlocking {
                                    ErrorDialog.doActionSafe({
                                        nsRuntime.stop()
                                    }, { "Namespace stop error" }, {})
                                }
                                nsActionInProgress.value = false
                            }
                        }
                ) {
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
                    Text(
                        "Open In Browser",
                        modifier = Modifier.align(Alignment.CenterStart)
                            .padding(start = linkTextPadding)
                    )
                }
            }
            Spacer(Modifier.height(2.dp))
            HorizontalDivider()
            var currentCategory: String? = null
            val allLinks = (nsGenRes.value?.links ?: emptyList()) + GlobalLinks.LINKS
            for (link in allLinks) {
                if (link.category != null && link.category != currentCategory) {
                    currentCategory = link.category
                    Spacer(Modifier.height(8.dp))
                    Text(
                        currentCategory,
                        modifier = Modifier.padding(start = 12.dp, top = 4.dp, bottom = 4.dp),
                        style = MaterialTheme.typography.labelMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant
                    )
                }
                CiteckTooltipArea(
                    tooltip = link.description
                ) {
                    Box(
                        modifier = Modifier.fillMaxWidth()
                            .clickable(enabled = link.alwaysEnabled || runtimeStatus.value == RUNNING) {
                                Desktop.getDesktop().browse(URI.create(link.url))
                            }
                    ) {
                        Box(
                            modifier = Modifier.align(Alignment.CenterStart)
                                .padding(start = 12.dp, top = 5.dp, bottom = 5.dp)
                        ) {
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
            Spacer(modifier = Modifier.height(4.dp))
            Row(
                modifier = Modifier.height(30.dp).padding(start = 10.dp),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(4.dp)
            ) {
                val backToWelcomeAllowed = runtimeStatus.value == STOPPED
                IconBtn(
                    ActionIcon.ARROW_LEFT,
                    enabled = backToWelcomeAllowed,
                    tooltip = if (!backToWelcomeAllowed) {
                        "Please stop all running apps before returning to the welcome screen"
                    } else {
                        "Back to Welcome Screen"
                    }
                ) {
                    services.setSelectedNamespace("")
                }
                IconBtn(ActionIcon.OPEN_DIR, "Open Namespace Dir") {
                    val nsDir = NamespacesService.getNamespaceDir(nsRuntime.namespaceRef).toFile()
                    Desktop.getDesktop().open(nsDir)
                }
                IconBtn(ActionIcon.BARS_ARROW_DOWN, "Show Launcher Logs") {
                    LogsWindow.show(
                        LogsDialogParams("Launcher Logs", 5000) { logsCallback ->
                            runCatching {
                                AppLogUtils.watchAppLogs { logsCallback.invoke(it) }
                            }
                        }
                    )
                }
                IconBtn(ActionIcon.STORAGE, "Show And Manage Volumes") {
                    JournalSelectDialog.show(
                        JournalSelectDialog.Params(
                            VolumeInfo::class,
                            emptyList(),
                            false,
                            services.entitiesService,
                            false,
                            selectable = false,
                            columns = listOf(
                                JournalSelectDialog.JournalColumn("Name", "name", 200.dp, 450.dp),
                                JournalSelectDialog.JournalColumn("Size", "sizeMb", 50.dp, 100.dp)
                            ),
                            customButtons = listOf(
                                JournalSelectDialog.JournalButton("Snapshots") {
                                    SnapshotsDialog.showSuspended(
                                        SnapshotsDialog.Params(
                                            nsRuntime,
                                            services
                                        )
                                    )
                                },
                                JournalSelectDialog.JournalButton("Delete All", enabledIf = {
                                    runtimeStatus.value == STOPPED
                                }, loading = true) {
                                    var entities = services.entitiesService.find(VolumeInfo::class, 100)
                                    if (entities.isNotEmpty() && ConfirmDialog.showSuspended("All your data in volumes will be lost")) {
                                        log.info {
                                            "Begin full deletion of volumes for namespace " +
                                                "${selectedNamespace.value?.name} (${selectedNamespace.value?.id})"
                                        }
                                        var iterations = 100
                                        while (--iterations > 0 && entities.isNotEmpty()) {
                                            for (entity in entities) {
                                                log.info { "Delete ${entity.name} (${entity.ref.localId})" }
                                                services.entitiesService.delete(entity.entity)
                                            }
                                            entities = services.entitiesService.find(VolumeInfo::class, 100)
                                        }
                                        if (iterations <= 0) {
                                            error(
                                                "Delete All action failed. Iterations limit was reached. " +
                                                    "Entities: " + entities.joinToString { it.ref.toString() }
                                            )
                                        }
                                    }
                                    false
                                }
                            )
                        )
                    )
                }
                IconBtn(ActionIcon.KEY, "Show Auth Secrets") {
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
                IconBtn(ActionIcon.EXCLAMATION_TRIANGLE, "Export System Info") {
                    val nsDir = NamespacesService.getNamespaceDir(nsRuntime.namespaceRef)
                    SystemDumpUtils.dumpSystemInfo(nsDir.resolve("reports"))
                }
            }
            Spacer(modifier = Modifier.height(4.dp))
        }
        VerticalDivider(color = Color.Black)
        Column(modifier = Modifier.fillMaxHeight()) {
            val scrollState = rememberScrollState()
            Column(modifier = Modifier.fillMaxHeight().verticalScroll(scrollState).padding(start = 6.dp, end = 6.dp)) {
                val appsByKind = rememberMutProp(nsRuntime, nsRuntime.appRuntimes) {
                    val appsByKind = HashMap<ApplicationKind, MutableList<AppRuntime>>()
                    nsRuntime.appRuntimes.getValue().forEach {
                        appsByKind.computeIfAbsent(it.def.getValue().kind) { ArrayList() }.add(it)
                    }
                    appsByKind.values.forEach {
                        it.sortWith { r1, r2 -> r1.name.compareTo(r2.name) }
                    }
                    appsByKind
                }
                RenderApps(
                    "Citeck Core",
                    appsByKind.value[ApplicationKind.CITECK_CORE],
                    coroutineScope
                )
                RenderApps(
                    "Citeck Core Extensions",
                    appsByKind.value[ApplicationKind.CITECK_CORE_EXTENSION],
                    coroutineScope
                )
                RenderApps(
                    "Citeck Additional",
                    appsByKind.value[ApplicationKind.CITECK_ADDITIONAL],
                    coroutineScope
                )
                RenderApps(
                    "Third Party",
                    appsByKind.value[ApplicationKind.THIRD_PARTY],
                    coroutineScope
                )
            }
        }
    }
}

@Composable
private fun RenderApps(
    header: String,
    applications: List<AppRuntime>?,
    coroutineScope: CoroutineScope
) {
    if (applications.isNullOrEmpty()) {
        return
    }

    Text(
        header,
        fontSize = 1.1.em,
        fontWeight = FontWeight.Bold,
        maxLines = 1,
        modifier = Modifier.padding(start = 5.dp, top = 10.dp, bottom = 10.dp)
    )
    Column(modifier = Modifier.padding(start = 5.dp, end = 5.dp)) {

        Row(modifier = Modifier.fillMaxWidth()) {
            Text("Name", modifier = Modifier.weight(AppTableColumns.NAME_WEIGHT), maxLines = 1)
            Text("Status", modifier = Modifier.weight(AppTableColumns.STATUS_WEIGHT), maxLines = 1)
            Text("CPU", modifier = Modifier.width(AppTableColumns.CPU_WIDTH), maxLines = 1)
            Text("MEM", modifier = Modifier.width(AppTableColumns.MEM_WIDTH), maxLines = 1)
            Text("Ports", modifier = Modifier.width(AppTableColumns.PORTS_WIDTH), maxLines = 1)
            Text("Tag", modifier = Modifier.width(AppTableColumns.TAG_WIDTH), maxLines = 1)
            Text("Actions", modifier = Modifier.width(AppTableColumns.ACTIONS_WIDTH), maxLines = 1)
        }

        HorizontalDivider()

        for (application in applications) {
            val statusText = rememberMutProp(application, application.statusText)
            val appStatus = rememberMutProp(application, application.status)
            val editedDef = rememberMutProp(application, application.editedDef)
            val appDef = rememberMutProp(application, application.def)
            val containerStats = rememberMutProp(application, application.containerStats)
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
                Text(application.name, modifier = Modifier.weight(AppTableColumns.NAME_WEIGHT), maxLines = 1)
                Row(
                    modifier = Modifier.weight(AppTableColumns.STATUS_WEIGHT).fillMaxHeight(),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    val statusColor = if (appStatus.value.isStalledState()) {
                        STALLED_COLOR
                    } else {
                        when (appStatus.value) {
                            AppRuntimeStatus.STOPPED -> STOPPED_COLOR
                            AppRuntimeStatus.RUNNING -> RUNNING_COLOR
                            else -> STARTING_STOPPING_COLOR
                        }
                    }
                    Text(appStatus.value.toString(), color = statusColor, maxLines = 1)
                    Spacer(Modifier.width(5.dp))
                    Text(statusText.value, maxLines = 1, overflow = TextOverflow.Ellipsis)
                }

                AppStatsCells(
                    appStatus = appStatus.value,
                    containerStats = containerStats.value
                )

                if (ports.isEmpty()) {
                    Row(modifier = Modifier.width(AppTableColumns.PORTS_WIDTH)) {}
                } else if (ports.size == 1) {
                    Text(text = ports.first(), modifier = Modifier.width(AppTableColumns.PORTS_WIDTH))
                } else {
                    CiteckTooltipArea(
                        tooltip = ports.joinToString("\n"),
                        modifier = Modifier.width(AppTableColumns.PORTS_WIDTH)
                    ) {
                        Text(
                            text = ports.first() + " ..",
                            maxLines = 1
                        )
                    }
                }
                CiteckTooltipArea(
                    tooltip = application.image,
                    modifier = Modifier.width(AppTableColumns.TAG_WIDTH),
                ) {
                    val image = appDef.value.image
                    Text(
                        text = image.substringAfterLast(":", "unknown"),
                        modifier = Modifier.clickable(
                            onClick = {
                                Toolkit.getDefaultToolkit()
                                    .systemClipboard
                                    .setContents(StringSelection(image), null)
                            }
                        ),
                        maxLines = 1
                    )
                }
                Row(
                    modifier = Modifier.width(AppTableColumns.ACTIONS_WIDTH).fillMaxHeight(0.85f),
                    horizontalArrangement = Arrangement.Start,
                    verticalAlignment = Alignment.CenterVertically
                ) {
                    if (appStatus.value.isStoppingState()) {
                        IconBtn(ActionIcon.START, tooltip = "Start Application") {
                            application.start()
                        }
                    } else {
                        IconBtn(ActionIcon.STOP, tooltip = "Stop Application") {
                            application.stop(manual = true)
                        }
                    }
                    IconBtn(
                        ActionIcon.BARS_ARROW_DOWN,
                        enabled = appStatus.value != AppRuntimeStatus.STOPPED,
                        tooltip = "Show Logs"
                    ) {
                        LogsWindow.show(
                            LogsDialogParams(application.name, 5000) { logsCallback ->
                                runCatching {
                                    application.watchLogs(5000, logsCallback)
                                }
                            }
                        )
                    }

                    val anyVolumeFilesEdited = remember { mutableStateOf(false) }

                    val volumeFilesItems = rememberMutProp(application.volumeFiles) { volumeFiles ->
                        anyVolumeFilesEdited.value = volumeFiles.any { it.edited }
                        volumeFiles.mapNotNull { fileInfo ->
                            val path = fileInfo.path
                            val filename = path.fileName.toString()
                            val extension = FilenameUtils.getExtension(filename)
                            if (EDITABLE_FILE_EXTENSIONS.contains(extension)) {
                                ContextMenu.Item(
                                    filename,
                                    decoration = if (fileInfo.edited) {
                                        {
                                            Box(
                                                Modifier
                                                    .align(Alignment.CenterEnd)
                                                    .width(5.dp)
                                                    .fillMaxHeight()
                                                    .background(Color.Blue)
                                            )
                                        }
                                    } else {
                                        {}
                                    }
                                ) {
                                    val contentToEdit = application.nsRuntime.runtimeFiles.getFileContent(path)
                                    try {
                                        val editRes = AppCfgEditWindow.show(
                                            filename,
                                            String(contentToEdit, Charsets.UTF_8)
                                        )?.content
                                        if (editRes == null) {
                                            application.nsRuntime.resetEditedFile(path)
                                        } else {
                                            application.nsRuntime
                                                .pushEditedFile(path, editRes.toByteArray())
                                        }
                                    } catch (_: FormCancelledException) {
                                        // do nothing
                                    }
                                }
                            } else {
                                null
                            }
                        }
                    }
                    Box(
                        modifier = Modifier.width(33.dp)
                            .contextMenu(ContextMenu.Button.RMB, volumeFilesItems.value)
                    ) {
                        IconBtn(
                            ActionIcon.COG_6_TOOTH,
                            "Left Click - Edit App Docker Config\n" +
                                "Right Click - Edit Volume Files\n" +
                                "A blue marker means this app has a manual config that\n" +
                                "won’t be managed by the launcher\n" +
                                "To reset manual changes, open the editor and click 'Reset'"
                        ) {

                            val appDefToEdit = application.def.getValue()
                            try {
                                val editRes = AppCfgEditWindow.show(appDefToEdit)
                                if (editRes == null) {
                                    application.nsRuntime.resetAppDef(appDefToEdit.name)
                                } else {
                                    application.nsRuntime.updateAppDef(
                                        appDefToEdit,
                                        editRes.appDef,
                                        editRes.locked
                                    )
                                }
                            } catch (_: FormCancelledException) {
                                // do nothing
                            }
                        }
                        if (editedDef.value || anyVolumeFilesEdited.value) {
                            Box(
                                modifier = Modifier
                                    .padding(end = 2.dp, top = 2.dp)
                                    .size(6.dp)
                                    .align(Alignment.TopEnd)
                                    .background(Color.Blue, CircleShape)
                            )
                        }
                        if (volumeFilesItems.value.isNotEmpty()) {
                            Text(
                                text = volumeFilesItems.value.size.toString(),
                                fontSize = 12.sp,
                                lineHeight = 0.5.em,
                                modifier = Modifier.align(Alignment.BottomEnd)
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
