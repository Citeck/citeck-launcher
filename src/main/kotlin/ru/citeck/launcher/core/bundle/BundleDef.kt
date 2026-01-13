package ru.citeck.launcher.core.bundle

data class BundleDef(
    val key: BundleKey,
    val applications: Map<String, BundleAppDef>,
    val citeckApps: List<BundleAppDef>
) {
    companion object {
        val EMPTY = BundleDef(BundleKey("0.0.0"), emptyMap(), emptyList())
    }

    fun isNotEmpty(): Boolean {
        return !isEmpty()
    }

    fun isEmpty(): Boolean {
        return applications.isEmpty() && citeckApps.isEmpty()
    }

    data class BundleAppDef(
        val image: String
    )
}
