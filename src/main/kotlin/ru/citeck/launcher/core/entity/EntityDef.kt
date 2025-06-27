package ru.citeck.launcher.core.entity

import ru.citeck.launcher.core.database.Repository
import ru.citeck.launcher.core.utils.data.DataValue
import ru.citeck.launcher.view.dialog.form.spec.FormSpec
import kotlin.reflect.KClass

class EntityDef<K : Any, T : Any>(
    val idType: EntityIdType<K>,
    val valueType: KClass<T>,
    val typeName: String,
    val typeId: String,
    val getId: (T) -> K,
    val getName: (T) -> String,
    val createForm: FormSpec?,
    val editForm: FormSpec?,
    val defaultEntities: List<T>,
    val actions: List<ActionDef<T>> = emptyList(),
    val customRepo: Repository<K, T>? = null,
    val toFormData: (T) -> DataValue = { DataValue.of(it) },
    val fromFormData: (DataValue) -> T = { it.getAsNotNull(valueType) },
    val versionable: Boolean = true
) {

    class ActionDef<T : Any>(
        val id: String,
        val icon: String,
        val description: String,
        val condition: (T) -> Boolean = { true },
        val action: suspend (T) -> Unit
    )
}
