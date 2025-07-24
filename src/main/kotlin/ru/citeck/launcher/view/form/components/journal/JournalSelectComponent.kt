package ru.citeck.launcher.view.form.components.journal

import androidx.compose.foundation.layout.Column
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.launch
import ru.citeck.launcher.core.entity.EntitiesService
import ru.citeck.launcher.core.entity.EntityInfo
import ru.citeck.launcher.core.entity.EntityRef
import ru.citeck.launcher.view.commons.LimitedText
import ru.citeck.launcher.view.form.FormContext
import ru.citeck.launcher.view.form.spec.ComponentSpec

@Composable
fun JournalSelectComponent(
    formContext: FormContext,
    component: ComponentSpec.JournalSelect,
    entitiesService: EntitiesService
) {
    val selectedEntities: MutableState<List<EntityInfo<*>>> = remember {
        mutableStateOf(entitiesService.getByRefs<Any>(formContext.getStrListValue(component.key).map { EntityRef.valueOf(it) }))
    }

    val selectedEntitiesValue = selectedEntities.value
    if (selectedEntitiesValue.isEmpty()) {
        LimitedText("(No Value)", maxWidth = 300.dp, color = MaterialTheme.colorScheme.primary)
    } else {
        Column {
            for (entity in selectedEntities.value) {
                LimitedText(entity.name, maxWidth = 300.dp, color = MaterialTheme.colorScheme.primary)
            }
        }
    }

    val coroutineScope = rememberCoroutineScope()

    Button(
        onClick = {
            coroutineScope.launch {
                val newEntities = JournalSelectDialog.show(
                    JournalSelectDialog.Params(
                        component.entityType,
                        selectedEntities.value.map { it.ref },
                        component.multiple,
                        entitiesService = entitiesService
                    )
                )
                selectedEntities.value = entitiesService.getByRefs<Any>(newEntities)
                val formValue: Any? = if (component.multiple) {
                    newEntities
                } else {
                    newEntities.firstOrNull()
                }
                formContext.setValue(component.key, formValue)
            }
        }
    ) {
        Text("Select")
    }
}
