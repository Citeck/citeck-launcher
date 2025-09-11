package ru.citeck.launcher.view.form

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Visibility
import androidx.compose.material.icons.filled.VisibilityOff
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.input.key.*
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.launch
import kotlinx.coroutines.suspendCancellableCoroutine
import ru.citeck.launcher.core.LauncherServices
import ru.citeck.launcher.core.WorkspaceServices
import ru.citeck.launcher.core.entity.EntitiesService
import ru.citeck.launcher.core.utils.data.DataValue
import ru.citeck.launcher.core.utils.json.Json
import ru.citeck.launcher.view.commons.LimitedText
import ru.citeck.launcher.view.commons.dialog.ErrorDialog
import ru.citeck.launcher.view.commons.dialog.GlobalMsgDialogParams
import ru.citeck.launcher.view.commons.dialog.MessageDialog
import ru.citeck.launcher.view.form.components.journal.JournalSelectComponent
import ru.citeck.launcher.view.form.components.select.SelectComponent
import ru.citeck.launcher.view.form.exception.FormCancelledException
import ru.citeck.launcher.view.form.spec.ComponentSpec
import ru.citeck.launcher.view.form.spec.FormSpec
import ru.citeck.launcher.view.popup.CiteckDialog
import kotlin.coroutines.resume
import kotlin.coroutines.resumeWithException

class FormDialog(
    private val params: InternalParams
) : CiteckDialog() {

    companion object {
        fun show(
            launcherServices: LauncherServices,
            spec: FormSpec,
            mode: FormMode = FormMode.CREATE,
            data: DataValue = DataValue.NULL,
            onCancel: () -> Unit,
            onSubmit: (DataValue, onComplete: () -> Unit) -> Unit
        ) {
            showDialog(
                InternalParams(
                    spec = spec,
                    formMode = mode,
                    data = data,
                    entitiesService = launcherServices.entitiesService,
                    workspaceServices = null,
                    onSubmit = onSubmit,
                    onCancel = onCancel
                )
            )
        }

        fun show(
            entitiesService: EntitiesService,
            workspaceServices: WorkspaceServices?,
            spec: FormSpec,
            mode: FormMode = FormMode.CREATE,
            data: DataValue = DataValue.NULL,
            onCancel: () -> Unit,
            onSubmit: (DataValue, onComplete: () -> Unit) -> Unit
        ) {
            showDialog(
                InternalParams(
                    spec = spec,
                    formMode = mode,
                    data = data,
                    entitiesService = entitiesService,
                    workspaceServices = workspaceServices,
                    onSubmit = onSubmit,
                    onCancel = onCancel
                )
            )
        }

        fun show(
            workspaceServices: WorkspaceServices,
            spec: FormSpec,
            mode: FormMode = FormMode.CREATE,
            data: DataValue = DataValue.NULL,
            onCancel: () -> Unit,
            onSubmit: (DataValue, onComplete: () -> Unit) -> Unit
        ) {
            showDialog(
                InternalParams(
                    spec = spec,
                    formMode = mode,
                    data = data,
                    entitiesService = workspaceServices.entitiesService,
                    workspaceServices = workspaceServices,
                    onSubmit = onSubmit,
                    onCancel = onCancel
                )
            )
        }

        suspend fun show(
            workspaceServices: WorkspaceServices,
            spec: FormSpec,
            mode: FormMode = FormMode.CREATE,
            data: DataValue = DataValue.NULL
        ): DataValue {
            return show(workspaceServices.entitiesService, workspaceServices, spec, mode, data)
        }

        suspend fun show(
            entitiesService: EntitiesService,
            spec: FormSpec,
            mode: FormMode = FormMode.CREATE,
            data: DataValue = DataValue.NULL
        ): DataValue {
            return show(entitiesService, null, spec, mode, data)
        }

        suspend fun show(
            launcherServices: LauncherServices,
            spec: FormSpec,
            mode: FormMode = FormMode.CREATE,
            data: DataValue = DataValue.NULL
        ): DataValue {
            return show(launcherServices.entitiesService, null, spec, mode, data)
        }

        suspend fun show(
            entitiesService: EntitiesService,
            workspaceServices: WorkspaceServices?,
            spec: FormSpec,
            mode: FormMode = FormMode.CREATE,
            data: DataValue = DataValue.NULL,
        ): DataValue {
            return suspendCancellableCoroutine { continuation ->
                showDialog(
                    InternalParams(
                        spec = spec,
                        formMode = mode,
                        data = data,
                        entitiesService = entitiesService,
                        workspaceServices = workspaceServices,
                        onSubmit = { data, onComplete ->
                            continuation.resume(data)
                            onComplete()
                        },
                        onCancel = { continuation.resumeWithException(FormCancelledException()) }
                    )
                )
            }
        }

        private fun showDialog(params: InternalParams) {
            showDialog(FormDialog(params))
        }
    }

    @Composable
    override fun render() {
        val coroutineScope = rememberCoroutineScope()
        val formContext = remember {
            val ctx = FormContext(
                params.spec,
                params.formMode,
                params.entitiesService,
                params.workspaceServices
            ) {
                val invalidFields = getInvalidFields()
                if (invalidFields.isNotEmpty()) {
                    coroutineScope.launch {
                        MessageDialog.show(
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
                            params.onSubmit(this.getValues()) {
                                closeDialog()
                            }
                        } catch (e: Throwable) {
                            ErrorDialog.show(e)
                        }
                    }
                }
            }
            val dataKeysWithFields = HashSet<String>()
            params.spec.forEachField {
                val value = if (params.data.has(it.key)) {
                    dataKeysWithFields.add(it.key)
                    Json.convertOrNull(params.data[it.key], Any::class)
                } else {
                    it.defaultValue
                }
                ctx.setValue(it.key, value)
            }
            params.data.forEach { key, value ->
                if (!dataKeysWithFields.contains(key)) {
                    ctx.setValue(key, Json.convertOrNull(value, Any::class))
                }
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

        dialog {
            if (params.spec.label.isNotEmpty()) {
                title(params.spec.label)
            }
            renderComponents(params.spec.components, dialogWidth, formContext, coroutineScope, params.entitiesService)

            buttonsRow {
                renderFormButtons(params, formContext)
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
        val isVisible = remember {
            val visibleFlag = mutableStateOf(this.isVisible(formContext, component))
            formContext.listenChanges(component.dependsOn) { k, v ->
                visibleFlag.value = this.isVisible(formContext, component)
            }
            visibleFlag
        }
        if (!isVisible.value) {
            return
        }
        if (component is ComponentSpec.Field<*>) {
            val width = dialogWidth * 0.3f
            LimitedText(component.label + ":", minWidth = width, maxWidth = width)
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
                        if (component.submitOnEnter && (event.key == Key.Enter || event.key == Key.NumPadEnter) && event.type == KeyEventType.KeyUp) {
                            executePopupAction("FormDialog -> Enter press") {
                                formContext.submit()
                            }
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

    private fun isVisible(context: FormContext, component: ComponentSpec): Boolean {
        if (component.visibleConditions.isEmpty()) {
            return true
        }
        return component.visibleConditions.all {
            it.invoke(context)
        }
    }

    @Composable
    private fun ButtonsRowContext.renderFormButtons(
        params: InternalParams,
        formContext: FormContext
    ) {
        button("Cancel") {
            params.onCancel()
            closeDialog()
        }
        spacer()
        button("Confirm") {
            formContext.submit()
        }
    }

    @Stable
    class InternalParams(
        val spec: FormSpec,
        val formMode: FormMode,
        val data: DataValue,
        val entitiesService: EntitiesService,
        val workspaceServices: WorkspaceServices? = null,
        val onSubmit: (DataValue, onComplete: () -> Unit) -> Unit,
        val onCancel: () -> Unit
    )
}
