package ru.citeck.launcher.core.config.bundle

class BundleDef(
    val key: BundleKey,
    val applications: Map<String, BundleAppDef>,
    val citeckApps: List<BundleAppDef>,
    val isEnterpriseBundle: Boolean
) {
    companion object {
        val EMPTY = BundleDef(BundleKey("0.0.0"), emptyMap(), emptyList(), false)
    }

    fun isNotEmpty(): Boolean {
        return !isEmpty()
    }

    fun isEmpty(): Boolean {
        return applications.isEmpty() && citeckApps.isEmpty()
    }

    class BundleAppDef(
        val image: String
    )
}
