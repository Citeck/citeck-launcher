package ru.citeck.launcher.core.namespace.volume

import com.github.dockerjava.api.command.InspectVolumeResponse
import ru.citeck.launcher.core.WorkspaceServices
import ru.citeck.launcher.core.database.Repository
import ru.citeck.launcher.core.namespace.NamespaceRef
import ru.citeck.launcher.core.namespace.runtime.NsRuntimeStatus
import java.util.*

class VolumesRepo(
    private val workspaceServices: WorkspaceServices
) : Repository<String, VolumeInfo> {

    private val dockerApi = workspaceServices.dockerApi

    private fun getNsRef(): NamespaceRef? {
        val currentNamespace = workspaceServices.selectedNamespace.getValue() ?: return null
        return NamespaceRef(workspaceServices.workspace.id, currentNamespace.id)
    }

    override fun set(id: String, value: VolumeInfo) {
        error("Not supported")
    }

    override fun get(id: String): VolumeInfo? {
        return dockerApi.getVolumeByNameOrNull(id)?.toInfo(dockerApi.getVolumesSize())
    }

    override fun delete(id: String) {
        val nsRef = getNsRef() ?: return
        val currentVolumes = dockerApi.getVolumes(nsRef)
        if (currentVolumes.none { it.name == id }) {
            return
        }
        if (workspaceServices.getCurrentNsRuntime()?.nsStatus?.getValue() == NsRuntimeStatus.STOPPED) {
            dockerApi.deleteVolume(id)
        } else {
            error("You should stop namespace before deleting volume")
        }
    }

    override fun find(max: Int): List<VolumeInfo> {
        val volumesSize = dockerApi.getVolumesSize()
        return dockerApi.getVolumes(getNsRef())
            .map { it.toInfo(volumesSize) }
            .sortedBy { it.name }
    }

    override fun getFirst(): VolumeInfo? {
        return find(1).firstOrNull()
    }

    override fun forEach(action: (String, VolumeInfo) -> Boolean) {
        find(1000).forEach { action(it.name, it) }
    }

    private fun InspectVolumeResponse.toInfo(volumesSize: Map<String, Long>): VolumeInfo {
        val sizeBytes = volumesSize[name] ?: -1L
        val sizeMb = if (sizeBytes >= 0) {
            String.format(Locale.US, "%.2f", sizeBytes / (1024f * 1024f))
        } else {
            "-1"
        }
        return VolumeInfo(name, "$sizeMb mb", sizeBytes)
    }
}
