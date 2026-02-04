package ru.citeck.launcher.view.dialog

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.MutableState
import androidx.compose.runtime.mutableStateOf
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.em
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.suspendCancellableCoroutine
import ru.citeck.launcher.core.WorkspaceServices
import ru.citeck.launcher.core.namespace.NamespacesService
import ru.citeck.launcher.core.namespace.runtime.NamespaceRuntime
import ru.citeck.launcher.core.namespace.runtime.NsRuntimeStatus
import ru.citeck.launcher.core.namespace.volume.VolumeInfo
import ru.citeck.launcher.core.utils.ActionStatus
import ru.citeck.launcher.core.utils.CompressionAlg
import ru.citeck.launcher.core.utils.MemoryUtils
import ru.citeck.launcher.core.utils.file.FileUtils
import ru.citeck.launcher.core.utils.promise.Promise
import ru.citeck.launcher.core.utils.promise.Promises
import ru.citeck.launcher.view.commons.dialog.*
import ru.citeck.launcher.view.drawable.CpImage
import ru.citeck.launcher.view.popup.CiteckDialog
import ru.citeck.launcher.view.popup.DialogWidth
import java.awt.Desktop
import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.attribute.BasicFileAttributes
import java.text.SimpleDateFormat
import java.time.Duration
import java.time.Instant
import java.util.Date
import kotlin.coroutines.resume
import kotlin.io.path.absolute
import kotlin.io.path.deleteExisting
import kotlin.io.path.exists
import kotlin.io.path.name
import kotlin.math.roundToInt

class SnapshotsDialog(
    private val params: InternalParams
) : CiteckDialog() {

    companion object {
        private val CREATED_FORMAT = SimpleDateFormat("HH:mm dd.MM.yyyy")

        suspend fun showSuspended(params: Params): Boolean {
            return suspendCancellableCoroutine { continuation ->
                showDialog(
                    SnapshotsDialog(
                        InternalParams(params) {
                            continuation.resume(it)
                        }
                    )
                )
            }
        }
    }

    private val workspaceService: WorkspaceServices = params.params.workspaceService
    private val nsRuntime: NamespaceRuntime = params.params.nsRuntime

    private val workspaceSnapshots: MutableState<List<SnapshotInfo>> = mutableStateOf(loadWorkspaceSnapshots())
    private val namespaceSnapshots: MutableState<List<SnapshotInfo>> = mutableStateOf(loadNamespaceSnapshots())

    private fun getOrCreateNsSnapshotsDir(): Path {
        val snapshotsDir = NamespacesService.getNamespaceDir(nsRuntime.namespaceRef)
            .resolve("snapshots")
        if (!snapshotsDir.exists()) {
            snapshotsDir.toFile().mkdirs()
        }
        return snapshotsDir
    }

    @Composable
    override fun render() {
        dialog(width = DialogWidth.MEDIUM) {
            if (workspaceSnapshots.value.isNotEmpty()) {
                renderSnapshots("Workspace Snapshots", true, workspaceSnapshots)
                Spacer(modifier = Modifier.height(10.dp))
            }
            renderSnapshots("Namespace Snapshots", false, namespaceSnapshots)
            buttonsRow {
                button("Cancel") {
                    closeDialog()
                    params.onClose(false)
                }
                spacer()
                button("Create Snapshot", enabledIf = {
                    nsRuntime.nsStatus.getValue() == NsRuntimeStatus.STOPPED
                }) {
                    val snapshotsDir = getOrCreateNsSnapshotsDir()
                    val snapshotName = CreateOrEditSnapshotDialog.showCreate(
                        snapshotsDir,
                        FileUtils.createNameWithCurrentDateTime()
                    )
                    if (snapshotName.isNotEmpty()) {
                        val status = ActionStatus.Mut()
                        val closeLoading = LoadingDialog.show(status)
                        try {
                            workspaceService.dockerApi.exportSnapshot(
                                nsRuntime.namespaceRef,
                                snapshotsDir.resolve(snapshotName),
                                CompressionAlg.XZ,
                                status
                            )
                            namespaceSnapshots.value = loadNamespaceSnapshots()
                        } finally {
                            closeLoading()
                        }
                    }
                }
                button("Open NS Directory") {
                    Desktop.getDesktop().open(getOrCreateNsSnapshotsDir().toFile())
                }
            }
        }
    }

    @Composable
    private fun renderSnapshots(header: String, isGlobal: Boolean, snapshots: MutableState<List<SnapshotInfo>>) {

        val nameWeight = 0.5f
        val createdWidth = 250.dp
        val sizeWidth = 100.dp
        val actionsWidth = 100.dp

        val isNsStopped = nsRuntime.nsStatus.getValue() == NsRuntimeStatus.STOPPED

        Text(
            header,
            fontSize = 1.1.em,
            fontWeight = FontWeight.Bold,
            maxLines = 1,
            modifier = Modifier.padding(start = 5.dp, top = 0.dp, bottom = 10.dp)
        )
        Column(modifier = Modifier.padding(start = 5.dp, end = 5.dp)) {

            Row(modifier = Modifier.fillMaxWidth()) {
                Text("Name", modifier = Modifier.weight(nameWeight), maxLines = 1)
                if (!isGlobal) {
                    Text("Created", modifier = Modifier.width(createdWidth), maxLines = 1)
                }
                Text("Size", modifier = Modifier.width(sizeWidth), maxLines = 1)
                Text("Actions", modifier = Modifier.width(actionsWidth), maxLines = 1)
            }

            HorizontalDivider()

            for (snapshot in snapshots.value) {
                Row(modifier = Modifier.fillMaxWidth().height(30.dp), verticalAlignment = Alignment.CenterVertically) {
                    Text(snapshot.name, modifier = Modifier.weight(nameWeight), maxLines = 1)
                    if (!isGlobal) {
                        Text(
                            CREATED_FORMAT.format(Date.from(snapshot.created)),
                            modifier = Modifier.width(createdWidth),
                            maxLines = 1
                        )
                    }
                    Text(snapshot.sizeMb + " mb", modifier = Modifier.width(sizeWidth), maxLines = 1)
                    Row(modifier = Modifier.width(actionsWidth).fillMaxHeight()) {
                        if (!isGlobal) {
                            CpImage(
                                "icons/pencil.svg",
                                modifier = Modifier.padding(start = 7.dp, top = 5.dp, bottom = 5.dp)
                                    .clickable {
                                        Thread.ofPlatform().start {
                                            val snapPath = snapshot.getPath().get(Duration.ofMinutes(1))
                                            runBlocking {
                                                ErrorDialog.doActionSafe({
                                                    val newName = CreateOrEditSnapshotDialog.showEdit(
                                                        snapPath.fileName.toString().substringBeforeLast(".")
                                                    )
                                                    if (newName.isNotBlank()) {
                                                        val moveTo = snapPath.parent.resolve("$newName.zip")
                                                        log.info {
                                                            "Move snapshot from '${snapPath.absolute()}' " +
                                                                "to '${moveTo.absolute()}'"
                                                        }
                                                        Files.move(snapPath, moveTo)
                                                        namespaceSnapshots.value = loadNamespaceSnapshots()
                                                    }
                                                }, { "Snapshot edit failed" }, {})
                                            }
                                        }
                                    }
                            )
                        }
                        CpImage(
                            "icons/cube-transparent.svg",
                            modifier = Modifier.padding(start = 7.dp, top = 5.dp, bottom = 5.dp)
                                .clickable {
                                    Thread.ofPlatform().start {
                                        runBlocking {
                                            ErrorDialog.doActionSafe({

                                                if (!isNsStopped) {
                                                    MessageDialog.show(
                                                        GlobalMsgDialogParams(
                                                            "Namespace is running",
                                                            "You should stop namespace before import snapshot",
                                                            DialogWidth.SMALL
                                                        )
                                                    )
                                                    return@doActionSafe
                                                }

                                                val existingVolumes = workspaceService.entitiesService.find(
                                                    VolumeInfo::class,
                                                    1000
                                                )
                                                var cancelImport = false
                                                if (existingVolumes.isNotEmpty()) {
                                                    val nsName = nsRuntime.namespaceConfig.getValue().name + " (" + nsRuntime.namespaceRef.namespace + ")"
                                                    cancelImport = !ConfirmDialog.showSuspended(
                                                        "Current namespace $nsName has active volumes. " +
                                                            "Do you want to delete existing volumes and import selected snapshot?"
                                                    )
                                                }
                                                if (!cancelImport) {
                                                    val actionStatus = ActionStatus.Mut()
                                                    val snapPath = snapshot.getPath().get(Duration.ofMinutes(100))
                                                    actionStatus.set("Delete existing volumes", 0f)
                                                    workspaceService.dockerApi.validateSnapshot(snapPath)
                                                    val closeDialog = LoadingDialog.show(actionStatus)
                                                    try {
                                                        if (existingVolumes.isNotEmpty()) {
                                                            actionStatus.set("Delete existing volumes", 0.1f)
                                                            val deletionStatus = actionStatus.subStatus(0.1f)
                                                            for ((idx, volumeEntity) in existingVolumes.withIndex()) {
                                                                val msg = "Delete ${volumeEntity.name}"
                                                                deletionStatus.set(msg, idx / existingVolumes.size.toFloat())
                                                                log.info { msg }
                                                                workspaceService.entitiesService.delete(volumeEntity.entity)
                                                            }
                                                            deletionStatus.set("Deletion completed", 1f)
                                                        }
                                                        val namespaceRef = nsRuntime.namespaceRef
                                                        log.info { "Import snapshot from '${snapPath.absolute()}' to namespace '$namespaceRef'" }
                                                        val importedVolumes = workspaceService.dockerApi.importSnapshot(
                                                            namespaceRef,
                                                            snapPath,
                                                            actionStatus.subStatus(0.8f)
                                                        )
                                                        actionStatus.set("Completed", 1f)
                                                        if (importedVolumes.isEmpty()) {
                                                            MessageDialog.show(
                                                                GlobalMsgDialogParams(
                                                                    "Nothing imported",
                                                                    "Nothing imported from snapshot. ",
                                                                    DialogWidth.SMALL
                                                                )
                                                            )
                                                        } else {
                                                            MessageDialog.show(
                                                                GlobalMsgDialogParams(
                                                                    "Import completed",
                                                                    "Snapshot import was successful.",
                                                                    DialogWidth.SMALL
                                                                )
                                                            )
                                                        }
                                                    } finally {
                                                        closeDialog()
                                                    }
                                                }
                                            }, { "Snapshot import failed" }, {})
                                        }
                                    }
                                }
                        )
                        if (!isGlobal) {
                            CpImage(
                                "icons/delete.svg",
                                modifier = Modifier.padding(start = 7.dp, top = 5.dp, bottom = 5.dp)
                                    .clickable {
                                        Thread.ofPlatform().start {
                                            runBlocking {
                                                ErrorDialog.doActionSafe({
                                                    val snapPath = snapshot.getPath().get(Duration.ofSeconds(5))
                                                    val deleteConfirmed = ConfirmDialog.showSuspended(
                                                        "Are you sure to delete snapshot '${snapPath.name}'?"
                                                    )
                                                    if (deleteConfirmed) {
                                                        snapPath.deleteExisting()
                                                        namespaceSnapshots.value = loadNamespaceSnapshots()
                                                    }
                                                }, { "Snapshot deletion failed" }, {})
                                            }
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

    private fun loadNamespaceSnapshots(): List<SnapshotInfo> {
        val localSnapshotFiles = getOrCreateNsSnapshotsDir().toFile().listFiles { _, name ->
            name.endsWith(".zip")
        } ?: emptyArray()
        return localSnapshotFiles.map { file ->
            val fileAtts = Files.readAttributes(file.toPath(), BasicFileAttributes::class.java)
            val createdAt = Instant.ofEpochMilli(
                (fileAtts.creationTime() ?: fileAtts.lastModifiedTime())?.toMillis() ?: 0
            )
            SnapshotInfo(
                file.name.substringBeforeLast("."),
                createdAt,
                ((file.length() * 10f / 1024f / 1024f).roundToInt() / 10f).toString()
            ) { Promises.resolve(file.toPath()) }
        }.sortedByDescending { it.created }
    }

    private fun loadWorkspaceSnapshots(): List<SnapshotInfo> {
        return workspaceService.workspaceConfig.getValue().snapshots.map {
            val size = MemoryUtils.parseMemAmountToBytes(it.size)
            val sizeInMb = ((size * 10f / 1024f / 1024f).roundToInt() / 10f).toString()
            SnapshotInfo(it.name, Instant.EPOCH, sizeInMb) {
                val loadingStatus = ActionStatus.Mut()
                val closeLoading = LoadingDialog.show(loadingStatus)
                workspaceService.snapshotsService.getSnapshot(it.id, loadingStatus).finally {
                    closeLoading()
                }
            }
        }
    }

    private class SnapshotInfo(
        val name: String,
        val created: Instant,
        val sizeMb: String,
        val getPath: () -> Promise<Path>
    )

    class Params(
        val nsRuntime: NamespaceRuntime,
        val workspaceService: WorkspaceServices
    )

    class InternalParams(
        val params: Params,
        val onClose: (Boolean) -> Unit
    )
}
