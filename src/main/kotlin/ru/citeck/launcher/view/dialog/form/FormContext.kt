package ru.citeck.launcher.view.dialog.form

import androidx.compose.runtime.MutableState
import androidx.compose.runtime.Stable
import androidx.compose.runtime.mutableStateOf
import ru.citeck.launcher.core.WorkspaceServices
import ru.citeck.launcher.core.entity.EntitiesService
import ru.citeck.launcher.core.utils.data.DataValue
import ru.citeck.launcher.view.dialog.form.spec.ComponentSpec
import ru.citeck.launcher.view.dialog.form.spec.FormSpec
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.CopyOnWriteArrayList
import kotlin.reflect.KClass
import kotlin.reflect.cast

@Stable
class FormContext(
    private val spec: FormSpec,
    val mode: FormMode,
    val entitiesService: EntitiesService,
    val workspaceServices: WorkspaceServices?,
    private val submit: FormContext.() -> Unit
) {

    private val values = ConcurrentHashMap<String, MutableState<Any?>>()
    private val fieldsByKey = HashMap<String, ComponentData>()

    private val onChangedListeners = ConcurrentHashMap<String, MutableList<(String, Any?) -> Unit>>()

    init {
        spec.forEachField {
            fieldsByKey[it.key] = ComponentData(it)
        }
    }

    fun submit() {
        this.submit(this)
    }

    fun listenChanges(fields: Set<String>, action: (String, Any?) -> Unit) {
        if (fields.isEmpty()) {
            return
        }
        for (field in fields) {
            onChangedListeners.computeIfAbsent(field) { CopyOnWriteArrayList() }.add(action)
        }
    }

    fun isAllFieldsValid(): Boolean {
        return fieldsByKey.all { it.value.valid.value.isEmpty() }
    }

    fun getInvalidFields(): Map<String, String> {
        val invalidFields = mutableMapOf<String, String>()
        spec.forEachField {
            if (it.validations.isNotEmpty()) {
                val value = values[it.key]?.value
                for (validation in it.validations) {
                    val message = validation.invoke(this, value)
                    if (message.isNotEmpty()) {
                        invalidFields[it.label] = message
                        break
                    }
                }
            }
        }
        return invalidFields
    }

    fun setValue(key: String, value: Any?) {
        var isNewValue = false
        val state = values.computeIfAbsent(key) {
            isNewValue = true
            mutableStateOf(value)
        }
        if (!isNewValue) {
            state.value = value
        }
        fieldsByKey[key]?.let { fieldData ->
            var invalidMsg = ""
            for (validation in fieldData.field.validations) {
                val msg = validation.invoke(this, value)
                if (msg.isNotEmpty()) {
                    invalidMsg = msg
                    break
                }
            }
            if (fieldData.valid.value != invalidMsg) {
                fieldData.valid.value = invalidMsg
            }
        }
        onChangedListeners[key]?.forEach { it.invoke(key, value) }
    }

    fun <T : Any> getValue(key: String, type: KClass<T>): T? {
        return values[key]?.let { type.cast(it.value) }
    }

    fun getValue(key: String): Any? {
        return values[key]?.value
    }

    fun getStrListValue(key: String): List<String> {
        val value = getValue(key, Any::class) ?: return emptyList()
        if (value is List<*>) {
            @Suppress("UNCHECKED_CAST")
            return value as List<String>
        } else {
            val text = value as? String ?: return emptyList()
            return if (text.isBlank()) {
                emptyList()
            } else {
                listOf(text)
            }
        }
    }

    fun getStrValue(key: String): String {
        return getValue(key, String::class) ?: ""
    }

    fun getValues(): DataValue {
        val result = DataValue.createObj()
        for ((k, v) in values) {
            result[k] = v.value
        }
        return result
    }

    private class ComponentData(
        val field: ComponentSpec.Field<Any>,
        val valid: MutableState<String> = mutableStateOf("")
    )
}
