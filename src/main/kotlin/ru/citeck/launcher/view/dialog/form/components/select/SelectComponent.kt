package ru.citeck.launcher.view.dialog.form.components.select

import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import ru.citeck.launcher.view.dialog.form.FormContext
import ru.citeck.launcher.view.dialog.form.spec.ComponentSpec
import ru.citeck.launcher.view.select.CiteckSelect
import ru.citeck.launcher.view.select.CiteckSelectState
import ru.citeck.launcher.view.select.SelectOption

@Composable
fun SelectComponent(formContext: FormContext, component: ComponentSpec.SelectField) {

    val state = remember {
        val selectState = CiteckSelectState(
            component.options.invoke(formContext).map { SelectOption(it.label, it.value) },
            formContext.getStrValue(component.key)
        )
        formContext.listenChanges(component.dependsOn) { _, _ ->
            selectState.options.value = component.options.invoke(formContext).map {
                SelectOption(it.label, it.value)
            }
        }
        selectState
    }

    CiteckSelect(state) {
        formContext.setValue(component.key, it)
        state.selected.value = it
    }
}
