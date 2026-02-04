package ru.citeck.launcher.core.form

import ru.citeck.launcher.core.WorkspaceServices
import kotlin.reflect.KClass

interface FormContext {
    val mode: FormMode
    val workspaceServices: WorkspaceServices?
    fun getStrValue(key: String): String
    fun <T : Any> getValue(key: String, type: KClass<T>): T?
    fun getValue(key: String): Any?
    fun setValue(key: String, value: Any?)
    fun listenChanges(fields: Set<String>, action: (String, Any?) -> Unit)
    fun submit()
}
