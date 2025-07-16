package ru.citeck.launcher.core.config.bundle

import io.github.oshai.kotlinlogging.KotlinLogging
import ru.citeck.launcher.core.config.bundle.BundleDef.BundleAppDef
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
        val kitsMap = TreeMap<BundleKey, BundleDef>()
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
        val data = Yaml.read(file, DataValue::class)
        if (!data.isObject()) {
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
            appNameByAliases.put(it, AppName.PROXY)
        }

        var isEnterpriseBundle = false

        fun getImageUrl(repository: String, tag: String): String {
            if (repository.isBlank()) {
                return ""
            }
            val imagesRepoId = repository.substringBefore("/", "")
            if (imagesRepoId.isNotBlank() && imagesRepoId != "core") {
                isEnterpriseBundle = true
            }
            var realRepository = repository
            val imageRepoInfo = workspaceConfig.imageReposById[imagesRepoId]
            if (imageRepoInfo != null) {
                realRepository = imageRepoInfo.url + "/" + repository.substringAfter("/")
            }
            return "$realRepository:$tag"
        }

        data.forEach { appName, value ->
            val image = getImageUrl(value["/image/repository"].asText(), value["/image/tag"].asText())
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
        return BundleDef(key, applications, citeckApps, isEnterpriseBundle)
    }
}
