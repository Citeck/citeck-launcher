package ru.citeck.launcher.core.entity

import ru.citeck.launcher.core.utils.data.DataValue
import ru.citeck.launcher.view.action.ActionDesc

class EntityInfo<T : Any>(
    val ref: EntityRef,
    val name: String,
    val getCustomProp: (String) -> DataValue,
    val actions: List<ActionDesc<EntityInfo<T>>>,
    val entity: T
) {
    override fun toString(): String {
        return "EntityInfo(ref=$ref, name='$name')"
    }
}
