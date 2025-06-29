package ru.citeck.launcher.view.form.spec

data class FormSpec(
    val label: String,
    val width: Width = Width.MEDIUM,
    val components: List<ComponentSpec>
) {
    fun forEachField(action: (ComponentSpec.Field<Any>) -> Unit) {
        for (component in components) {
            if (component is ComponentSpec.Field<*>) {
                @Suppress("UNCHECKED_CAST")
                action.invoke(component as ComponentSpec.Field<Any>)
            }
        }
    }

    enum class Width {
        SMALL,
        MEDIUM,
        LARGE
    }
}
