package ru.citeck.launcher.core.bundle

import io.github.oshai.kotlinlogging.KotlinLogging
import ru.citeck.launcher.core.bundle.BundleDef.BundleAppDef
import ru.citeck.launcher.core.namespace.AppName
import ru.citeck.launcher.core.utils.data.DataValue
import ru.citeck.launcher.core.utils.json.Yaml
import ru.citeck.launcher.core.workspace.WorkspaceConfig
import java.io.File
import java.nio.file.Path
import java.util.TreeMap
import kotlin.io.path.relativeTo

object BundleUtils {

    private val log = KotlinLogging.logger {}

    fun loadBundles(path: Path, workspaceConfig: WorkspaceConfig): List<BundleDef> {
        val kitsMap = TreeMap<BundleKey, BundleDef>(Comparator<BundleKey> { v0, v1 -> v1.compareTo(v0) })
        loadKitsFiles(path, workspaceConfig, path.toFile(), kitsMap)
        return kitsMap.values.toList()
    }

    private fun loadKitsFiles(
        rootPath: Path,
        workspaceConfig: WorkspaceConfig,
        path: File,
        result: MutableMap<BundleKey, BundleDef>
    ) {
        for (file in path.listFiles() ?: emptyArray()) {
            if (file.isFile) {
                if (file.name.endsWith(".yml") || file.name.endsWith(".yaml")) {

                    val fileNameWoExt = file.name.substringBeforeLast('.')
                    val pathFile = if (fileNameWoExt == "values") {
                        file.parentFile
                    } else {
                        file
                    }
                    var key = pathFile.toPath()
                        .relativeTo(rootPath)
                        .toString()
                        .replace(File.separatorChar, '/')
                    if (pathFile.isFile) {
                        key = key.substringBeforeLast('.')
                    }
                    val bundleKey = BundleKey(key)

                    val def = try {
                        readBundleFile(bundleKey, file, workspaceConfig)
                    } catch (e: Throwable) {
                        log.error(e) { "Could not read bundle file ${file.path}" }
                        continue
                    }
                    if (def.isEmpty()) {
                        continue
                    }

                    result[bundleKey] = def
                }
            } else {
                loadKitsFiles(rootPath, workspaceConfig, file, result)
            }
        }
    }

    private fun readBundleFile(key: BundleKey, file: File, workspaceConfig: WorkspaceConfig): BundleDef {
        val rawData = Yaml.read(file, DataValue::class)
        if (!rawData.isObject()) {
            return BundleDef.EMPTY
        }

        val applications = LinkedHashMap<String, BundleAppDef>()
        val citeckApps = ArrayList<BundleAppDef>()

        val eappsAppNames = mutableSetOf(AppName.EAPPS)
        workspaceConfig.webappsById[AppName.EAPPS]?.let {
            eappsAppNames.addAll(it.aliases)
        }
        val appNameByAliases = HashMap<String, String>()
        workspaceConfig.webapps.forEach { app ->
            app.aliases.forEach { appNameByAliases[it] = app.id }
        }
        workspaceConfig.citeckProxy.aliases.forEach {
            appNameByAliases[it] = AppName.PROXY
        }
        workspaceConfig.alfresco.aliases.forEach {
            appNameByAliases[it] = AppName.ALFRESCO
        }

        fun getImageUrl(repository: String, tag: String): String {
            if (repository.isBlank()) {
                return ""
            }
            val imagesRepoId = repository.substringBefore("/", "")
            var realRepository = repository
            val imageRepoInfo = workspaceConfig.imageReposById[imagesRepoId]
            if (imageRepoInfo != null) {
                realRepository = imageRepoInfo.url + "/" + repository.substringAfter("/")
            }
            return "$realRepository:$tag"
        }

        // Mirrors the 2.x launcher's resolveImageRefWithRepos exactly (see
        // internal/bundle/resolver.go): rewrites a plain "repo:tag" /
        // "repo@sha256:..." string whose first path segment is a known
        // imageRepos ID, and otherwise - no such segment, or it isn't a
        // known ID - returns the string verbatim, tag/digest and all. A
        // "host:port" registry prefix is NOT a tag delimiter: a ':' only
        // introduces a tag/digest when nothing after it contains a '/'.
        // This is deliberately NOT built on top of getImageUrl(repository,
        // tag): getImageUrl always appends ":$tag" and so has no way to
        // return a reference that carries no tag at all (e.g. "busybox",
        // "registry:5000/image") unchanged - which is exactly what Go does
        // for those shapes instead of treating them as unreadable.
        fun resolveImageRef(image: String): String {
            val trimmed = image.trim()
            if (trimmed.isEmpty()) {
                return trimmed
            }
            var repository = trimmed
            var suffix = ""
            val atIdx = repository.lastIndexOf('@')
            if (atIdx >= 0) {
                suffix = repository.substring(atIdx)
                repository = repository.substring(0, atIdx)
            }
            val colonIdx = repository.lastIndexOf(':')
            if (colonIdx >= 0 && !repository.substring(colonIdx).contains('/')) {
                suffix = repository.substring(colonIdx) + suffix
                repository = repository.substring(0, colonIdx)
            }
            val slashIdx = repository.indexOf('/')
            if (slashIdx < 0) {
                // No prefix segment to map - nothing to rewrite.
                return trimmed
            }
            val prefix = repository.substring(0, slashIdx)
            val rest = repository.substring(slashIdx + 1)
            val imageRepoInfo = workspaceConfig.imageReposById[prefix]
            return if (imageRepoInfo != null) {
                imageRepoInfo.url + "/" + rest + suffix
            } else {
                trimmed
            }
        }

        // The 2.x launcher accepts an image written as a single value (a plain
        // "repo:tag" string, or a {repository, tag} map) or as a LIST of either
        // shape. Outside its `dependencies:` section a list has no route to
        // walk - there is no pin, no hold and no migration out here, and none
        // of that exists in THIS launcher at all - so the first element is
        // taken: the most conservative rung, the one most likely to already
        // match what is on the volume. An empty list, or a shape that can't be
        // read, resolves to "" - the same as a missing image today.
        fun readImage(value: DataValue): String {
            val imageNode = value["/image"]
            val node = if (imageNode.isArray()) {
                if (imageNode.size() == 0) {
                    return ""
                }
                imageNode[0]
            } else {
                imageNode
            }
            return if (node.isTextual()) {
                resolveImageRef(node.asText())
            } else {
                getImageUrl(node["repository"].asText(), node["tag"].asText())
            }
        }

        fun processApp(appName: String, value: DataValue) {
            if (appName.isBlank()) {
                return
            }
            if (appName == "ecos") {
                if (value.isObject()) {
                    // some helm charts have core version under ecos key in kit
                    value.forEach { ecosScopeAppName, ecosScopeAppValue ->
                        processApp(ecosScopeAppName, ecosScopeAppValue)
                    }
                }
            } else {
                val image = readImage(value)
                if (image.isNotBlank()) {
                    applications[appNameByAliases[appName] ?: appName] = BundleAppDef(image)
                }
                if (eappsAppNames.contains(appName)) {
                    for (app in value["/ecosAppsImages"]) {
                        val citeckAppImage = getImageUrl(app["repository"].asText(), app["tag"].asText())
                        if (citeckAppImage.isNotBlank()) {
                            citeckApps.add(BundleAppDef(citeckAppImage))
                        }
                    }
                }
            }
        }
        rawData.forEach { appName, value ->
            processApp(appName, value)
        }
        return BundleDef(key, applications, citeckApps, rawData)
    }
}
