package ru.citeck.launcher.view.form.components.select

import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import ru.citeck.launcher.view.form.FormContext
import ru.citeck.launcher.view.form.spec.ComponentSpec
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
            val newOptions = component.options.invoke(formContext).map {
                SelectOption(it.label, it.value)
            }
            if (selectState.options.value != newOptions) {
                selectState.options.value = newOptions
                if (newOptions.isEmpty()) {
                    selectState.selected.value = ""
                } else {
                    val selectedValue = selectState.selected.value
                    if (newOptions.all { it.value != selectedValue }) {
                        selectState.selected.value = newOptions.last().value
                    }
                }
            }
        }
        selectState
    }

    CiteckSelect(state, component.mandatory) {
        formContext.setValue(component.key, it)
        state.selected.value = it
    }
}
