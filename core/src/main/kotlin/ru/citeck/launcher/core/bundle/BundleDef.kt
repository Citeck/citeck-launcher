package ru.citeck.launcher.core.bundle

import ru.citeck.launcher.core.utils.data.DataValue

data class BundleDef(
    val key: BundleKey,
    val applications: Map<String, BundleAppDef>,
    val citeckApps: List<BundleAppDef>,
    val content: DataValue = DataValue.createObj()
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

    data class BundleAppDef(
        val image: String
    )
}
