package ru.citeck.launcher.view.form.components.journal

import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.layout.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.em
import kotlinx.coroutines.suspendCancellableCoroutine
import ru.citeck.launcher.core.entity.EntitiesService
import ru.citeck.launcher.core.entity.EntityInfo
import ru.citeck.launcher.core.entity.EntityRef
import ru.citeck.launcher.core.utils.promise.Promise
import ru.citeck.launcher.core.utils.promise.Promises
import ru.citeck.launcher.view.action.CiteckIconAction
import ru.citeck.launcher.view.commons.LimitedText
import ru.citeck.launcher.view.commons.dialog.LoadingDialog
import ru.citeck.launcher.view.form.exception.FormCancelledException
import ru.citeck.launcher.view.popup.CiteckDialog
import ru.citeck.launcher.view.table.table.DataTable
import java.util.concurrent.CompletableFuture
import kotlin.coroutines.resume
import kotlin.reflect.KClass

class JournalSelectDialog(
    private val params: InternalParams
) : CiteckDialog() {

    companion object {
        suspend fun show(params: Params): List<EntityRef> {
            return suspendCancellableCoroutine { continuation ->
                show(params) { continuation.resume(it) }
            }
        }

        fun show(params: Params, onSubmit: (List<EntityRef>) -> Unit) {
            showDialog(
                JournalSelectDialog(
                    InternalParams(
                        params,
                        { onSubmit(it) },
                        { onSubmit(params.selected) }
                    )
                )
            )
        }
    }

    @Composable
    override fun render() {

        val entitiesService = params.params.entitiesService

        val dialogTitle = remember {
            "Select " + entitiesService.getTypeName(params.params.entityType)
        }
        val allRecords = remember {
            val state = mutableStateOf<List<RecordRow>>(emptyList())
            getTableRows(entitiesService, params, params.params.selected).then {
                state.value = it
            }
            state
        }
        val isEntityCreatable = remember {
            entitiesService.isEntityCreatable(params.params.entityType)
        }

        dialog {
            title(dialogTitle)
            renderTable(entitiesService, params, allRecords)
            Spacer(modifier = Modifier.height(16.dp))
            buttonsRow {
                renderButtons(entitiesService, params, allRecords, isEntityCreatable)
            }
        }
    }

    private fun updateTableRows(
        entities: EntitiesService,
        params: InternalParams,
        rows: MutableState<List<RecordRow>>,
        createdRef: EntityRef = EntityRef.EMPTY
    ) {
        val selectedRows = rows.value.filter { it.selected.value }.mapTo(ArrayList()) { it.record.ref }
        if (createdRef.isNotEmpty()) {
            if (params.params.multiple) {
                selectedRows.add(createdRef)
            } else {
                selectedRows.clear()
                selectedRows.add(createdRef)
            }
        }
        val valuesBefore = rows.value
        getTableRows(entities, params, selectedRows).then {
            rows.value = it
            if (params.params.closeWhenAllRecordsDeleted && valuesBefore.isNotEmpty() && rows.value.isEmpty()) {
                closeDialog()
            }
        }
    }

    private fun getTableRows(
        entities: EntitiesService,
        params: InternalParams,
        selectedRows: List<EntityRef>
    ): Promise<List<RecordRow>> {
        val future = CompletableFuture<List<RecordRow>>()
        Thread.ofPlatform().start {
            var somethingSelected = false

            val resultRows = entities.getAll(params.params.entityType).map {
                val row = RecordRow(it)
                if (selectedRows.contains(it.ref)) {
                    row.selected.value = true
                    somethingSelected = true
                }
                row
            }
            if (!params.params.multiple && !somethingSelected && selectedRows.isNotEmpty() && resultRows.isNotEmpty()) {
                resultRows.first().selected.value = true
            }
            future.complete(resultRows)
        }
        return Promises.create(future)
    }

    private fun submitSelected(records: MutableState<List<RecordRow>>) {
        val closeLoading = LoadingDialog.show()
        Thread.ofPlatform().start {
            try {
                params.onSubmit(
                    records.value
                        .filter { it.selected.value }
                        .map { it.record.ref }
                )
            } finally {
                closeLoading()
            }
            closeDialog()
        }
    }

    @Composable
    private fun renderTable(
        entities: EntitiesService,
        params: InternalParams,
        rows: MutableState<List<RecordRow>>
    ) {
        val coroutineScope = rememberCoroutineScope()

        DataTable(
            columns = {
                if (params.params.selectable) {
                    column {
                        Checkbox(false, onCheckedChange = {}, enabled = false)
                    }
                }
                params.params.columns.forEach {
                    column {
                        Text(
                            it.name,
                            fontWeight = FontWeight.Bold,
                            modifier = Modifier.padding(bottom = 5.dp)
                        )
                    }
                }
                column {
                    Text(
                        "Actions",
                        fontWeight = FontWeight.Bold,
                        modifier = Modifier.padding(bottom = 5.dp)
                    )
                }
            },
            cellPadding = PaddingValues(horizontal = 0.dp, vertical = 0.dp),
        ) {
            val rowsValue = rows.value
            for (row in rowsValue) {
                row(modifier = Modifier.height(1.dp)) {
                    if (params.params.selectable) {
                        cell {
                            Checkbox(row.selected.value, onCheckedChange = {
                                if (it && !params.params.multiple) {
                                    for (otherRow in rowsValue) {
                                        if (otherRow !== row) {
                                            otherRow.selected.value = false
                                        }
                                    }
                                }
                                row.selected.value = it
                            })
                        }
                    }
                    params.params.columns.forEach { column ->
                        if (column.property == "name") {
                            cell(
                                modifier = Modifier.pointerInput(Unit) {
                                    detectTapGestures(
                                        onDoubleTap = {
                                            if (params.params.selectable) {
                                                if (params.params.multiple) {
                                                    row.selected.value = true
                                                } else {
                                                    rowsValue.forEach {
                                                        it.selected.value = it.record.ref == row.record.ref
                                                    }
                                                    submitSelected(rows)
                                                }
                                            }
                                        }
                                    )
                                }
                            ) {
                                LimitedText(
                                    text = row.record.name,
                                    modifier = Modifier.padding(0.dp),
                                    fontSize = 1.1.em,
                                    minWidth = column.sizeMin,
                                    maxWidth = column.sizeMax
                                )
                            }
                        } else {
                            cell {
                                LimitedText(
                                    text = row.record.getCustomProp(column.property).asText(),
                                    modifier = Modifier.padding(0.dp),
                                    fontSize = 1.1.em,
                                    minWidth = column.sizeMin,
                                    maxWidth = column.sizeMax
                                )
                            }
                        }
                    }
                    cell {
                        Row(
                            horizontalArrangement = Arrangement.Start,
                            verticalAlignment = Alignment.CenterVertically
                        ) {
                            row.record.actions.forEach { action ->
                                CiteckIconAction(coroutineScope, action, row.record) {
                                    updateTableRows(entities, params, rows)
                                }
                            }
                        }
                    }
                }
            }
        }
    }

    @Composable
    private fun ButtonsRowContext.renderButtons(
        entitiesService: EntitiesService,
        params: InternalParams,
        records: MutableState<List<RecordRow>>,
        isEntityCreatable: Boolean
    ) {
        button(if (params.params.selectable) "Cancel" else "Close") {
            params.onCancel()
            closeDialog()
        }
        if (isEntityCreatable) {
            button("Create") {
                try {
                    val createdRef = entitiesService.create(params.params.entityType)
                    updateTableRows(entitiesService, params, records, createdRef)
                } catch (_: FormCancelledException) {
                    // do nothing
                }
            }
        }
        spacer()
        params.params.customButtons.forEach { buttonDesc ->
            button(buttonDesc.name, buttonDesc.enabledIf) {
                val closeLoading: () -> Unit = if (buttonDesc.loading) {
                    LoadingDialog.show()
                } else {
                    {}
                }
                val closeDialog = try {
                    buttonDesc.action.invoke()
                } finally {
                    closeLoading()
                }
                if (closeDialog) {
                    closeDialog()
                } else {
                    updateTableRows(entitiesService, params, records)
                }
            }
        }
        if (params.params.selectable) {
            button("Confirm") { submitSelected(records) }
        }
    }

    class RecordRow(
        val record: EntityInfo<Any>,
        val selected: MutableState<Boolean> = mutableStateOf(false)
    )

    data class Params(
        val entityType: KClass<out Any>,
        val selected: List<EntityRef>,
        val multiple: Boolean,
        val entitiesService: EntitiesService,
        val closeWhenAllRecordsDeleted: Boolean = false,
        val selectable: Boolean = true,
        val columns: List<JournalColumn> = listOf(JournalColumn.NAME),
        val customButtons: List<JournalButton> = emptyList()
    )

    data class JournalButton(
        val name: String,
        val loading: Boolean = false,
        val enabledIf: () -> Boolean = { true },
        val action: suspend () -> Boolean,
    )

    data class JournalColumn(
        val name: String,
        val property: String,
        val sizeMin: Dp,
        val sizeMax: Dp
    ) {
        companion object {
            val NAME = JournalColumn("Name", "name", 500.dp, 500.dp)
        }
    }

    data class InternalParams(
        val params: Params,
        val onSubmit: (List<EntityRef>) -> Unit,
        val onCancel: () -> Unit
    )
}
