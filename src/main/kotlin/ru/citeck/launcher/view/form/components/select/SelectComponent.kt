package ru.citeck.launcher.view.form.components.select

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import ru.citeck.launcher.view.dialog.LoadingDialog
import ru.citeck.launcher.view.drawable.CpIcon
import ru.citeck.launcher.view.form.FormContext
import ru.citeck.launcher.view.form.spec.ComponentSpec
import ru.citeck.launcher.view.select.CiteckSelect
import ru.citeck.launcher.view.select.CiteckSelectState
import ru.citeck.launcher.view.select.SelectOption

private fun updateOptions(
    selectState: CiteckSelectState,
    formContext: FormContext,
    component: ComponentSpec.SelectField
) {
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

@Composable
fun SelectComponent(formContext: FormContext, component: ComponentSpec.SelectField) {

    val state = remember {
        val selectState = CiteckSelectState(
            component.options.invoke(formContext).map { SelectOption(it.label, it.value) },
            formContext.getStrValue(component.key)
        )
        formContext.listenChanges(component.dependsOn) { _, _ ->
            updateOptions(selectState, formContext, component)
        }
        selectState
    }

    Row {
        CiteckSelect(state, mandatory = component.mandatory, modifier = Modifier.weight(1f)) {
            formContext.setValue(component.key, it)
            state.selected.value = it
        }
        val onManualUpdate = component.onManualUpdate
        if (onManualUpdate != null) {
            CpIcon(
                "icons/arrow-path.svg",
                modifier = Modifier.size(30.dp)
                    .padding(start = 5.dp, top = 2.dp, bottom = 2.dp)
                    .clickable {
                        val closeLoading = LoadingDialog.show()
                        Thread.ofPlatform().name("manual-select-update").start {
                            try {
                                onManualUpdate(formContext)
                                updateOptions(state, formContext, component)
                            } finally {
                                closeLoading()
                            }
                        }
                    }
            )
        }
    }
}
