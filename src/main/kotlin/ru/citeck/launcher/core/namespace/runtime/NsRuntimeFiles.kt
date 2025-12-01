package ru.citeck.launcher.core.namespace.runtime

import com.dynatrace.hash4j.hashing.Hashing
import com.google.common.primitives.Longs
import ru.citeck.launcher.core.database.Repository
import ru.citeck.launcher.core.namespace.NamespaceRef
import ru.citeck.launcher.core.namespace.NamespacesService
import ru.citeck.launcher.core.utils.Digest
import ru.citeck.launcher.core.utils.file.CiteckFiles
import java.nio.file.Path
import java.util.Base64
import java.util.TreeMap
import kotlin.collections.component1
import kotlin.collections.component2
import kotlin.collections.iterator
import kotlin.collections.set
import kotlin.io.path.absolute
import kotlin.io.path.absolutePathString
import kotlin.io.path.deleteExisting
import kotlin.io.path.deleteIfExists
import kotlin.io.path.exists
import kotlin.io.path.isDirectory
import kotlin.io.path.outputStream
import kotlin.io.path.readBytes
import kotlin.io.path.relativeTo
import kotlin.text.startsWith
import kotlin.text.substring

class NsRuntimeFiles(
    namespaceRef: NamespaceRef,
    private val changedRtFiles: Repository<String, ByteArray>
) {
    companion object {
        private const val RT_FILES_DIR = "rtfiles"
    }

    private val filesDir = NamespacesService.getNamespaceDir(namespaceRef).resolve(RT_FILES_DIR)
    private lateinit var generatedFiles: Map<String, ByteArray>

    @Volatile
    private var runtimeFilesHash: TreeMap<Path, String> = TreeMap()

    private val isFileChangedCache = HashMap<Path, Boolean>()

    private fun Path.asUnixLocalPath(): String {
        return this.joinToString("/", prefix = "", postfix = "")
    }

    fun isFileEdited(path: Path): Boolean {
        return isFileChangedCache.computeIfAbsent(path) {
            changedRtFiles[it.asUnixLocalPath()] != null
        }
    }

    fun getFileContent(path: Path): ByteArray {
        val strPath = path.asUnixLocalPath()
        return changedRtFiles[strPath] ?: generatedFiles[strPath] ?: error("File by $strPath not found")
    }

    fun getPathsFiles(paths: List<String>): List<NsFileInfo> {
        val localPaths = LinkedHashSet<Path>()
        for (path in paths) {
            fillFilesForAbsPath(path, localPaths)
        }
        return localPaths.map {
            NsFileInfo(it, runtimeFilesHash[it] ?: "", isFileEdited(it))
        }
    }

    fun getPathsContentHash(paths: List<String>): String {
        val hashStream = Hashing.xxh3_64().hashStream()
        for (localPath in getPathsFiles(paths)) {
            runtimeFilesHash[localPath.path]?.let {
                hashStream.putString(it)
            }
        }
        return Base64.getEncoder()
            .encodeToString(Longs.toByteArray(hashStream.asLong))
            .substringBefore('=')
    }

    private fun fillFilesForAbsPath(path: String, files: MutableSet<Path>) {
        val absPath = if (path.startsWith("./")) {
            filesDir.resolve(path.substring(2)).absolute()
        } else {
            Path.of(path)
        }
        if (!absPath.startsWith(filesDir)) {
            return
        }
        val localPath = absPath.relativeTo(filesDir)
        if (runtimeFilesHash.containsKey(localPath)) {
            files.add(localPath)
        } else {
            runtimeFilesHash.forEach { (filePath, _) ->
                if (filePath.startsWith(localPath)) {
                    files.add(filePath)
                }
            }
        }
    }

    fun resetEditedFile(file: Path) {
        val key = file.asUnixLocalPath()
        changedRtFiles.delete(key)
        applyFileData(file, generatedFiles[key]!!, runtimeFilesHash)
        isFileChangedCache.remove(file)
    }

    fun applyGeneratedFiles(genFiles: Map<String, ByteArray>) {

        this.generatedFiles = HashMap(genFiles)

        val currentFiles = CiteckFiles.getFile(filesDir).getFilesPath().toMutableSet()
        val runtimeFilesHash = TreeMap<Path, String>()

        for ((localPath, genBytes) in genFiles) {
            val newFileBytes = changedRtFiles[localPath] ?: genBytes
            applyFileData(localPath, newFileBytes, runtimeFilesHash)
            currentFiles.remove(localPath)
        }
        this.runtimeFilesHash = runtimeFilesHash

        for (path in currentFiles) {
            filesDir.resolve(path).deleteIfExists()
        }

        isFileChangedCache.clear()
    }

    internal fun applyEditedFile(file: Path, content: ByteArray): Boolean {
        if (!runtimeFilesHash.containsKey(file)) {
            error("File $file is not registered")
        }
        val key = file.asUnixLocalPath()
        changedRtFiles[key] = content
        val resp = applyFileData(file, content, runtimeFilesHash)
        isFileChangedCache.remove(file)
        return resp
    }

    private fun applyFileData(localPath: String, fileBytes: ByteArray, filesHash: MutableMap<Path, String>): Boolean {
        return applyFileData(Path.of(localPath), fileBytes, filesHash)
    }

    private fun applyFileData(localPath: Path, fileBytes: ByteArray, filesHash: MutableMap<Path, String>): Boolean {

        filesHash[localPath] = Digest.sha256().update(fileBytes).toHex()

        val currentFile = filesDir.resolve(localPath)
        val currentFileData = if (currentFile.exists()) {
            currentFile.readBytes()
        } else {
            ByteArray(0)
        }

        if (fileBytes.contentEquals(currentFileData)) {
            return false
        }
        val targetFilePath = filesDir.resolve(localPath)
        val fileDir = targetFilePath.parent
        if (fileDir.exists() && !fileDir.isDirectory()) {
            fileDir.deleteExisting()
        }
        if (!fileDir.exists()) {
            fileDir.toFile().mkdirs()
        }
        try {
            targetFilePath.outputStream().use { it.write(fileBytes) }
        } catch (writeEx: Throwable) {
            throw RuntimeException(
                "File write failed. " +
                    "File path: '$localPath' " +
                    "Content: ${Base64.getEncoder().encodeToString(fileBytes)}",
                writeEx
            )
        }
        if (localPath.endsWith(".sh") && !targetFilePath.toFile().canExecute()) {
            targetFilePath.toFile().setExecutable(true, false)
        }
        return true
    }

    fun resolveAbsPathInFilesDir(path: String): String {
        return filesDir.resolve(path).absolutePathString()
    }
}
