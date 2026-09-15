package ru.citeck.launcher.core.bundle

import ru.citeck.launcher.core.utils.data.DataValue

data class BundleDef(
    val key: BundleKey,
    val applications: Map<String, BundleAppDef>,
    val citeckApps: List<BundleAppDef>,
    val content: DataValue = DataValue.createObj(),
    /**
     * Third-party images the bundle declares under its `dependencies:` section.
     *
     * Kept apart from [applications] rather than merged into it, exactly as the
     * 2.x launcher keeps them apart, and for the same reason: [isEmpty] has to
     * go on meaning "this bundle carries no CITECK application". A bundle whose
     * only content is this section would otherwise load as a valid one, and the
     * namespace would come up as third-party containers with none of the
     * product in them — which passes every probe and reports RUNNING.
     */
    val dependencies: Map<String, BundleAppDef> = emptyMap()
) {
    companion object {
        val EMPTY = BundleDef(
            BundleKey("0.0.0"),
            emptyMap(),
            emptyList(),
            DataValue.createObj()
        )
    }

    fun isNotEmpty(): Boolean {
        return !isEmpty()
    }

    fun isEmpty(): Boolean {
        return applications.isEmpty() && citeckApps.isEmpty()
    }

    /**
     * The image this bundle names for an app, `dependencies:` first.
     *
     * One lookup instead of every caller reaching into [applications], so the
     * section cannot be honoured on one image and forgotten on the next. The
     * order matches the 2.x resolver's: a `dependencies:` entry outranks a
     * top-level one of the same name.
     */
    fun imageOf(appName: String): String {
        val dependency = dependencies[appName]?.image
        if (!dependency.isNullOrBlank()) {
            return dependency
        }
        return applications[appName]?.image ?: ""
    }

    data class BundleAppDef(
        val image: String
    )
}
