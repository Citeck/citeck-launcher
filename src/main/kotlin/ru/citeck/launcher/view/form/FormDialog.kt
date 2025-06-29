package ru.citeck.launcher.view.form

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Visibility
import androidx.compose.material.icons.filled.VisibilityOff
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.snapshots.SnapshotStateList
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.input.key.*
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.launch
import kotlinx.coroutines.suspendCancellableCoroutine
import ru.citeck.launcher.core.WorkspaceServices
import ru.citeck.launcher.core.entity.EntitiesService
import ru.citeck.launcher.core.utils.data.DataValue
import ru.citeck.launcher.core.utils.json.Json
import ru.citeck.launcher.view.commons.LimitedText
import ru.citeck.launcher.view.dialog.*
import ru.citeck.launcher.view.form.components.journal.JournalSelectComponent
import ru.citeck.launcher.view.form.components.select.SelectComponent
import ru.citeck.launcher.view.form.exception.FormCancelledException
import ru.citeck.launcher.view.form.spec.ComponentSpec
import ru.citeck.launcher.view.form.spec.FormSpec
import kotlin.coroutines.resume
import kotlin.coroutines.resumeWithException

open class FormDialog {

    private lateinit var showDialog: (InternalParams) -> (() -> Unit)

    fun show(
        spec: FormSpec,
        mode: FormMode = FormMode.CREATE,
        data: DataValue = DataValue.NULL,
        onCancel: () -> Unit,
        onSubmit: (DataValue) -> Boolean
    ) {
        showDialog(
            InternalParams(
                spec = spec,
                formMode = mode,
                data = data,
                onSubmit = onSubmit,
                onCancel = onCancel
            )
        )
    }

    suspend fun show(
        spec: FormSpec,
        mode: FormMode = FormMode.CREATE,
        data: DataValue = DataValue.NULL
    ): DataValue {
        return suspendCancellableCoroutine { continuation ->
            showDialog(
                InternalParams(
                    spec = spec,
                    formMode = mode,
                    data = data,
                    onSubmit = {
                        continuation.resume(it)
                        true
                    },
                    onCancel = { continuation.resumeWithException(FormCancelledException()) }
                ))
        }
    }

    @Composable
    fun FormDialog(
        statesList: SnapshotStateList<CiteckDialogState>,
        entitiesService: EntitiesService,
        workspaceServices: WorkspaceServices? = null
    ) {
        showDialog = CiteckDialog(statesList) { params, closeDialog ->

            val coroutineScope = rememberCoroutineScope()
            val formContext = remember {
                val ctx = FormContext(
                    params.spec,
                    params.formMode,
                    entitiesService,
                    workspaceServices
                ) {
                    val invalidFields = getInvalidFields()
                    if (invalidFields.isNotEmpty()) {
                        coroutineScope.launch {
                            GlobalMessageDialog.show(
                                GlobalMsgDialogParams(
                                    "Invalid form fields:",
                                    invalidFields.entries.joinToString("\n") {
                                        it.key + ": " + it.value
                                    }
                                )
                            )
                        }
                    } else {
                        Thread.ofPlatform().start {
                            try {
                                if (params.onSubmit(this.getValues())) {
                                    closeDialog()
                                }
                            } catch (e: Throwable) {
                                GlobalErrorDialog.show(GlobalErrorDialog.Params(e) {})
                            }
                        }
                    }
                }
                params.spec.forEachField {
                    val value = if (params.data.has(it.key)) {
                        Json.convertOrNull(params.data[it.key], Any::class)
                    } else {
                        it.defaultValue
                    }
                    ctx.setValue(it.key, value)
                }
                ctx
            }
            val dialogWidth = remember {
                when (params.spec.width) {
                    FormSpec.Width.SMALL -> 600.dp
                    FormSpec.Width.MEDIUM -> 800.dp
                    FormSpec.Width.LARGE -> 1000.dp
                }
            }

            Dialog(
                properties = DialogProperties(
                usePlatformDefaultWidth = false),
                onDismissRequest = {}
            ) {
                Surface(
                    shape = RoundedCornerShape(10.dp),
                    tonalElevation = 0.dp,
                    modifier = Modifier.width(dialogWidth).padding(5.dp)
                ) {
                    Column(modifier = Modifier.padding(15.dp)) {
                        if (params.spec.label.isNotEmpty()) {
                            Text(
                                text = params.spec.label,
                                style = MaterialTheme.typography.titleLarge
                            )
                            Spacer(modifier = Modifier.height(16.dp))
                        }
                        renderComponents(params.spec.components, dialogWidth, formContext, coroutineScope, entitiesService)
                        Spacer(modifier = Modifier.height(16.dp))
                        renderFormButtons(params, closeDialog, formContext)
                    }
                }
            }
        }
    }

    @Composable
    private fun renderComponents(
        components: List<ComponentSpec>,
        dialogWidth: Dp,
        formContext: FormContext,
        coroutineScope: CoroutineScope,
        entitiesService: EntitiesService
    ) {
        for ((idx, component) in components.withIndex()) {
            if (idx > 0) {
                Spacer(modifier = Modifier.height(8.dp))
            }
            Row(verticalAlignment = Alignment.CenterVertically) {
                renderComponent(formContext, dialogWidth, component, coroutineScope, entitiesService)
            }
        }
    }

    @Composable
    private fun renderComponent(
        formContext: FormContext,
        dialogWidth: Dp,
        component: ComponentSpec,
        coroutineScope: CoroutineScope,
        entitiesService: EntitiesService
    ) {
        if (component is ComponentSpec.Field<*>) {
            LimitedText(component.label + ":", maxWidth = dialogWidth * 0.3f)
        }
        when (component) {
            is ComponentSpec.Text -> {
                Text(
                    text = component.text,
                    style = MaterialTheme.typography.bodyLarge,
                    color = MaterialTheme.colorScheme.onSurfaceVariant
                )
            }

            is ComponentSpec.IdField -> {
                OutlinedTextField(
                    enabled = isEnabled(formContext, component),
                    value = formContext.getStrValue(component.key),
                    onValueChange = { formContext.setValue(component.key, it) },
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth()
                )
            }

            is ComponentSpec.IntField -> {
                OutlinedTextField(
                    enabled = isEnabled(formContext, component),
                    value = (formContext.getValue(component.key, Long::class) ?: 0L).toString(),
                    onValueChange = { newValue: String ->
                        newValue.toLongOrNull()?.let {
                            formContext.setValue(component.key, it)
                        }
                    },
                    keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number),
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth()
                )
            }

            is ComponentSpec.NameField -> {
                OutlinedTextField(
                    enabled = isEnabled(formContext, component),
                    value = formContext.getStrValue(component.key),
                    onValueChange = { formContext.setValue(component.key, it) },
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth()
                )
            }

            is ComponentSpec.PasswordField -> {
                var passwordVisible by remember { mutableStateOf(false) }
                OutlinedTextField(
                    enabled = isEnabled(formContext, component),
                    value = formContext.getStrValue(component.key),
                    onValueChange = { formContext.setValue(component.key, it) },
                    placeholder = if (component.placeholder.isNotEmpty()) {
                        { Text(component.placeholder) }
                    } else {
                        null
                    },
                    singleLine = true,
                    visualTransformation = if (passwordVisible) VisualTransformation.None else PasswordVisualTransformation(),
                    trailingIcon = {
                        val icon = if (passwordVisible) Icons.Default.Visibility else Icons.Default.VisibilityOff
                        IconButton(onClick = { passwordVisible = !passwordVisible }) {
                            Icon(icon, contentDescription = null)
                        }
                    },
                    modifier = Modifier.fillMaxWidth().onPreviewKeyEvent { event ->
                        if (component.submitOnEnter && event.key == Key.Enter && event.type == KeyEventType.KeyUp) {
                            formContext.submit()
                            true
                        } else {
                            false
                        }
                    }
                )
            }

            is ComponentSpec.Button -> {
                Button(
                    onClick = {
                        coroutineScope.launch {
                            component.onClick(formContext)
                        }
                    }
                ) {
                    Text(component.text)
                }
            }

            is ComponentSpec.JournalSelect -> {
                JournalSelectComponent(
                    formContext,
                    component,
                    entitiesService
                )
            }

            is ComponentSpec.SelectField -> {
                SelectComponent(formContext, component)
            }

            is ComponentSpec.TextField -> {
                OutlinedTextField(
                    enabled = isEnabled(formContext, component),
                    value = formContext.getStrValue(component.key),
                    onValueChange = { formContext.setValue(component.key, it) },
                    placeholder = if (component.placeholder.isNotEmpty()) {
                        { Text(component.placeholder) }
                    } else {
                        null
                    },
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth()
                )
            }
        }
    }

    private fun isEnabled(context: FormContext, component: ComponentSpec): Boolean {
        if (component !is ComponentSpec.Field<*>) {
            return true
        }
        if (component.enabledConditions.isEmpty()) {
            return true
        }
        @Suppress("UNCHECKED_CAST")
        component as ComponentSpec.Field<Any>
        return component.enabledConditions.all {
            it.invoke(
                context,
                context.getValue(component.key, component.valueType) ?: component.defaultValue
            )
        }
    }

    @Composable
    private fun renderFormButtons(
        params: InternalParams,
        closeDialog: () -> Unit,
        formContext: FormContext
    ) {
        val buttonsEnabled = remember { mutableStateOf(true) }
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.Start
        ) {
            Button(
                enabled = buttonsEnabled.value,
                onClick = {
                    buttonsEnabled.value = false
                    try {
                        params.onCancel()
                        closeDialog()
                    } finally {
                        buttonsEnabled.value = true
                    }
                }
            ) {
                Text("Cancel")
            }
            Spacer(modifier = Modifier.weight(1f))
            //Spacer(modifier = Modifier.width(8.dp))
            Button(enabled = buttonsEnabled.value,
                onClick = {
                    buttonsEnabled.value = false
                    try {
                        formContext.submit()
                    } finally {
                        buttonsEnabled.value = true
                    }
                }) {
                Text("Confirm")
            }
        }
    }

    @Stable
    private class InternalParams(
        val spec: FormSpec,
        val formMode: FormMode,
        val data: DataValue,
        val onSubmit: (DataValue) -> Boolean,
        val onCancel: () -> Unit
    )
}
