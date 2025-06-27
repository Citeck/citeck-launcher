package ru.citeck.launcher.view.dialog.form.components.journal

import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.snapshots.SnapshotStateList
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.em
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.launch
import kotlinx.coroutines.suspendCancellableCoroutine
import ru.citeck.launcher.core.entity.EntitiesService
import ru.citeck.launcher.core.entity.EntityInfo
import ru.citeck.launcher.core.entity.EntityRef
import ru.citeck.launcher.core.utils.promise.Promise
import ru.citeck.launcher.core.utils.promise.Promises
import ru.citeck.launcher.view.action.CiteckIconAction
import ru.citeck.launcher.view.commons.LimitedText
import ru.citeck.launcher.view.dialog.CiteckDialog
import ru.citeck.launcher.view.dialog.CiteckDialogState
import ru.citeck.launcher.view.dialog.form.exception.FormCancelledException
import ru.citeck.launcher.view.table.table.DataTable
import java.util.concurrent.CompletableFuture
import kotlin.coroutines.resume
import kotlin.reflect.KClass

object JournalSelectDialog {

    private lateinit var showDialog: (InternalParams) -> (() -> Unit)

    suspend fun show(params: Params): List<EntityRef> {
        return suspendCancellableCoroutine { continuation ->
            showDialog(
                InternalParams(
                    params,
                    { continuation.resume(it) },
                    { continuation.resume(params.selected) }
                )
            )
        }
    }

    @Composable
    fun JournalDialog(statesList: SnapshotStateList<CiteckDialogState>, defaultEntitiesService: EntitiesService) {

        showDialog = CiteckDialog(statesList) { params, closeDialog ->

            val entitiesService = params.params.entitiesService ?: defaultEntitiesService

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
            val coroutineScope = rememberCoroutineScope()

            Dialog(properties = DialogProperties(usePlatformDefaultWidth = false), onDismissRequest = {}) {
                Surface(
                    shape = RoundedCornerShape(10.dp),
                    tonalElevation = 0.dp,
                    modifier = Modifier.width(700.dp)
                ) {
                    Column(modifier = Modifier.padding(15.dp)) {
                        Text(dialogTitle, style = MaterialTheme.typography.titleLarge)
                        Spacer(modifier = Modifier.height(16.dp))
                        renderTable(entitiesService, params, allRecords, closeDialog)
                        Spacer(modifier = Modifier.height(16.dp))
                        renderButtons(entitiesService, coroutineScope, params, allRecords, closeDialog, isEntityCreatable)
                    }
                }
            }
        }
    }

    private fun updateTableRows(
        entities: EntitiesService,
        params: InternalParams,
        rows: MutableState<List<RecordRow>>,
        createdRef: EntityRef = EntityRef.EMPTY,
        closeDialog: () -> Unit
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

    @Composable
    private fun renderTable(
        entities: EntitiesService,
        params: InternalParams,
        rows: MutableState<List<RecordRow>>,
        closeDialog: () -> Unit
    ) {
        val coroutineScope = rememberCoroutineScope()

        DataTable(
            columns = {
                if (params.params.selectable) {
                    column {
                        Checkbox(false, onCheckedChange = {}, enabled = false)
                    }
                }
                column {
                    Text(
                        "Name",
                        fontWeight = FontWeight.Bold,
                        modifier = Modifier.padding(bottom = 5.dp)
                    )
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
                    cell(modifier = Modifier.pointerInput(Unit) {
                        detectTapGestures(
                            onDoubleTap = {
                                if (params.params.multiple) {
                                    row.selected.value = true
                                } else {
                                    params.onSubmit(listOf(row.record.ref))
                                    closeDialog()
                                }
                            }
                        )
                    }) {
                        LimitedText(
                            text = row.record.name,
                            modifier = Modifier.padding(0.dp),
                            fontSize = 1.1.em,
                            maxWidth = 500.dp
                        )
                    }
                    cell {
                        Row(
                            horizontalArrangement = Arrangement.Start,
                            verticalAlignment = Alignment.CenterVertically
                        ) {
                            row.record.actions.forEach { action ->
                                CiteckIconAction(coroutineScope, action, row.record) {
                                    updateTableRows(entities, params, rows, closeDialog = closeDialog)
                                }
                            }
                        }
                    }
                }
            }
        }
    }

    @Composable
    private fun renderButtons(
        entitiesService: EntitiesService,
        coroutineScope: CoroutineScope,
        params: InternalParams,
        records: MutableState<List<RecordRow>>,
        closeDialog: () -> Unit,
        isEntityCreatable: Boolean
    ) {
        Row(modifier = Modifier.fillMaxWidth()) {
            Button(
                onClick = {
                    params.onCancel()
                    closeDialog()
                }
            ) {
                Text("Cancel")
            }
            if (isEntityCreatable) {
                Spacer(modifier = Modifier.width(8.dp))
                Button(
                    onClick = {
                        coroutineScope.launch {
                            try {
                                val createdRef = entitiesService.create(params.params.entityType)
                                updateTableRows(entitiesService, params, records, createdRef, closeDialog)
                            } catch (e: FormCancelledException) {
                                // do nothing
                            }
                        }
                    }
                ) {
                    Text("Create")
                }
            }
            Spacer(modifier = Modifier.weight(1f))
            Button(
                onClick = {
                    params.onSubmit(records.value.filter { it.selected.value }.map { it.record.ref })
                    closeDialog()
                }
            ) {
                Text("Confirm")
            }
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
        val entitiesService: EntitiesService? = null,
        val closeWhenAllRecordsDeleted: Boolean = false,
        val selectable: Boolean = true
    )

    private data class InternalParams(
        val params: Params,
        val onSubmit: (List<EntityRef>) -> Unit,
        val onCancel: () -> Unit
    )
}
