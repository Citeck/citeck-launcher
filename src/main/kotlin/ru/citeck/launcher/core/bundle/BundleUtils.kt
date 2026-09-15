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

    // The key whose entries are read like top-level ones. It is skipped BY NAME
    // in the main loop rather than handled inside processApp, so that an entry
    // id colliding with the section's own schema cannot be read as an
    // application called "dependencies".
    private const val DEPENDENCIES_SECTION = "dependencies"

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
        val dependencies = LinkedHashMap<String, BundleAppDef>()
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

        // An `image:` is either the map every top-level entry uses
        // ({repository, tag}) or the plain string the `dependencies:` section
        // may also be written with. The 2.x launcher accepts both, so a bundle
        // written against it must not read differently here: a string form
        // silently ignored would be the same failure as not reading the section
        // at all. The repository rewriting is the same in both cases, which is
        // why the string is split rather than used as it stands — the split is
        // at the last ':' after the last '/', so a registry port is never
        // mistaken for a tag.
        fun readImage(imageValue: DataValue): String {
            if (imageValue.isTextual()) {
                val ref = imageValue.asText()
                val lastSlash = ref.lastIndexOf('/')
                val colon = ref.lastIndexOf(':')
                if (colon <= lastSlash || colon < 0) {
                    return ""
                }
                return getImageUrl(ref.substring(0, colon), ref.substring(colon + 1))
            }
            return getImageUrl(imageValue["repository"].asText(), imageValue["tag"].asText())
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
                val image = readImage(value["image"])
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
            if (appName == DEPENDENCIES_SECTION) {
                // A bundle may put its third-party images under `dependencies:`
                // instead of at the top level. That section is INVISIBLE to
                // launchers that do not know it, which is the point of it: an
                // infra version raised there reaches only launchers that gate
                // such a move, and everyone else keeps running what they run.
                //
                // This launcher has to read it for one reason — qdrant. Its
                // image comes from the bundle and from nowhere else: there is
                // no built-in fallback and no workspace default, so a bundle
                // that keeps qdrant in this section would leave `rag` running
                // with no vector store at all, silently. postgres, rabbitmq and
                // onlyoffice are unaffected either way, since this generator
                // takes those from the workspace config rather than the bundle.
                //
                // Entries here are read exactly like top-level ones, including
                // an id this launcher has no use for: keeping it costs nothing
                // and failing the bundle over it would defeat a section whose
                // whole job is to carry things some launchers ignore.
                if (value.isObject()) {
                    value.forEach { depName, depValue ->
                        val image = readImage(depValue["image"])
                        if (image.isNotBlank()) {
                            dependencies[appNameByAliases[depName] ?: depName] = BundleAppDef(image)
                        } else {
                            log.warn { "Bundle dependency entry names no image; ignoring it: $depName" }
                        }
                    }
                }
            } else {
                processApp(appName, value)
            }
        }
        return BundleDef(key, applications, citeckApps, rawData, dependencies)
    }
}
