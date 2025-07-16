package ru.citeck.launcher.view.form.spec

import com.fasterxml.jackson.annotation.JsonSubTypes
import com.fasterxml.jackson.annotation.JsonTypeInfo
import ru.citeck.launcher.core.entity.EntityIdType
import ru.citeck.launcher.view.form.FormContext
import ru.citeck.launcher.view.form.FormMode
import kotlin.reflect.KClass

@JsonTypeInfo(
    use = JsonTypeInfo.Id.NAME,
    include = JsonTypeInfo.As.PROPERTY,
    property = "type"
)
@JsonSubTypes(
    JsonSubTypes.Type(value = ComponentSpec.TextField::class, name = "text"),
    JsonSubTypes.Type(value = ComponentSpec.PasswordField::class, name = "password")
)
sealed class ComponentSpec {

    val visibleConditions: MutableList<(context: FormContext) -> Boolean> = mutableListOf()
    val dependsOn: MutableSet<String> = mutableSetOf()

    open fun dependsOn(vararg fields: String): ComponentSpec {
        dependsOn.addAll(fields)
        return this
    }

    open fun visibleWhen(condition: (context: FormContext) -> Boolean): ComponentSpec {
        visibleConditions.add(condition)
        return this
    }

    /**
     * Component without ability to enter or change some value.
     * e.g. Panel, Tabs, Text, Button etc
     */
    sealed class NonField : ComponentSpec()

    class Button(val text: String, val onClick: suspend (FormContext) -> Unit) : NonField()

    class Text(val text: String) : NonField()

    sealed class Field<T : Any>(
        val key: String,
        val label: String,
        val defaultValue: T,
        val valueType: KClass<T>
    ) : ComponentSpec() {

        val enabledConditions: MutableList<(context: FormContext, value: T?) -> Boolean> = mutableListOf()
        val validations: MutableList<(context: FormContext, value: T?) -> String> = mutableListOf()

        override fun dependsOn(vararg fields: String): ComponentSpec {
            super.dependsOn(*fields)
            return this
        }

        override fun visibleWhen(condition: (context: FormContext) -> Boolean): Field<T> {
            super.visibleWhen(condition)
            return this
        }

        fun enableWhen(condition: (context: FormContext, value: T?) -> Boolean): Field<T> {
            enabledConditions.add(condition)
            return this
        }

        fun validation(validation: (context: FormContext, value: T?) -> String): Field<T> {
            validations.add(validation)
            return this
        }
    }

    open class IdField : Field<String>("id", "Identifier", "", String::class) {
        init {
            validation { _, value ->
                if (value.isNullOrBlank()) {
                    "ID can't be empty"
                } else if (value.length > 30) {
                    "Value length is too long. Allowed length: 30 Actual length: ${value.length}"
                } else if (!EntityIdType.String.isValidId(value)) {
                    "Invalid id: $value"
                } else {
                    ""
                }
            }
            enableWhen { context, _ ->
                context.mode == FormMode.CREATE
            }
        }
    }

    open class TextField(
        key: String,
        label: String,
        val placeholder: String = "",
        defaultValue: String = ""
    ) : Field<String>(key, label, defaultValue = defaultValue, String::class) {

        var mandatory: Boolean = false
        var submitOnEnter = false

        fun mandatory(): TextField {
            mandatory = true
            validation { context, value ->
                if (enabledConditions.isNotEmpty() && enabledConditions.any { it(context, value) == false }) {
                    ""
                } else if (value.isNullOrBlank()) {
                    "Value is mandatory"
                } else {
                    ""
                }
            }
            return this
        }

        fun submitOnEnter(): TextField {
            submitOnEnter = true
            return this
        }
    }

    open class NameField : TextField("name", "Name", "") {
        init {
            mandatory()
            validation { _, value ->
                if (value.isNullOrBlank()) {
                    "Name can't be empty"
                } else if (value.length > 50) {
                    "Value length is too long. Allowed length: 50 Actual length: ${value.length}"
                } else {
                    ""
                }
            }
        }
    }

    open class IntField(
        key: String,
        label: String,
        defaultValue: Long = 0,
    ) : Field<Long>(key, label, defaultValue, Long::class)

    open class SelectField(
        key: String,
        label: String,
        defaultValue: String,
        val options: (FormContext) -> List<Option>
    ) : TextField(key, label, defaultValue = defaultValue) {

        constructor(
            key: String,
            label: String,
            defaultValue: String,
            options: List<Option>
        ) : this(key, label, defaultValue, { options })

        class Option(
            val value: String,
            val label: String
        )
    }

    class PasswordField(
        key: String,
        label: String,
        placeholder: String = ""
    ) : TextField(key, label, placeholder = placeholder)

    class JournalSelect(
        key: String,
        label: String,
        val entityType: KClass<*>,
        val multiple: Boolean = false
    ) : TextField(key, label, "")
}
