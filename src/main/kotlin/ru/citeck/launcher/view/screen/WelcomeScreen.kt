package ru.citeck.launcher.view.screen

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.layout.LayoutCoordinates
import androidx.compose.ui.layout.onGloballyPositioned
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.em
import io.github.oshai.kotlinlogging.KotlinLogging
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import ru.citeck.launcher.core.LauncherServices
import ru.citeck.launcher.core.WorkspaceServices
import ru.citeck.launcher.core.config.bundle.BundleRef
import ru.citeck.launcher.core.entity.EntityInfo
import ru.citeck.launcher.core.git.GitUpdatePolicy
import ru.citeck.launcher.core.namespace.NamespaceConfig
import ru.citeck.launcher.core.utils.ActionStatus
import ru.citeck.launcher.core.workspace.WorkspaceConfig.QuickStartVariant
import ru.citeck.launcher.core.workspace.WorkspaceDto
import ru.citeck.launcher.core.workspace.WorkspaceEntityDef
import ru.citeck.launcher.view.commons.ContextMenu
import ru.citeck.launcher.view.commons.ContextMenu.contextMenu
import ru.citeck.launcher.view.commons.dialog.ErrorDialog
import ru.citeck.launcher.view.commons.dialog.LoadingDialog
import ru.citeck.launcher.view.commons.dialog.MessageDialog
import ru.citeck.launcher.view.drawable.CpIcon
import ru.citeck.launcher.view.drawable.CpImage
import ru.citeck.launcher.view.form.components.journal.JournalSelectDialog
import ru.citeck.launcher.view.form.exception.FormCancelledException
import ru.citeck.launcher.view.utils.rememberMutProp

private const val FAST_ACCESS_NAMESPACES_LIMIT = 3

private val log = KotlinLogging.logger {}

@Composable
fun WelcomeScreen(launcherServices: LauncherServices, selectedWorkspace: MutableState<WorkspaceDto?>) {

    val entitiesService = launcherServices.entitiesService
    val selectedWsValue = selectedWorkspace.value

    if (selectedWsValue == null) {
        LoadingScreen()
    } else {

        val workspaceServicesValue = rememberMutProp(launcherServices.getWorkspaceServices())
        val workspaceServices = workspaceServicesValue.value

        Box(modifier = Modifier.fillMaxSize()) {
            Row(modifier = Modifier.align(Alignment.TopStart)) {
                TextButton(
                    onClick = {
                        JournalSelectDialog.show(
                            JournalSelectDialog.Params(
                                WorkspaceDto::class,
                                listOf(WorkspaceEntityDef.getRef(selectedWsValue)),
                                false,
                                entitiesService = launcherServices.entitiesService
                            )
                        ) { selectedRefs ->
                            val currentRef = WorkspaceEntityDef.getRef(selectedWsValue)
                            val newRef = selectedRefs.firstOrNull() ?: currentRef
                            var newEntity = entitiesService.getById(WorkspaceDto::class, newRef.localId)?.entity
                            if (newEntity == null) {
                                newEntity = entitiesService.getFirst(WorkspaceDto::class)!!.entity
                            }
                            launcherServices.setWorkspace(newEntity.id)
                            selectedWorkspace.value = newEntity
                        }
                    }
                ) {
                    Text(selectedWsValue.name, color = MaterialTheme.colorScheme.scrim)
                }
                val actionsIconPosition = remember { mutableStateOf<LayoutCoordinates?>(null) }
                CpIcon(
                    "icons/ellipsis-vertical.svg",
                    modifier = Modifier.padding(0.dp)
                        .requiredSize(20.dp)
                        .align(Alignment.CenterVertically)
                        .onGloballyPositioned {
                            actionsIconPosition.value = it
                        }
                        .contextMenu(
                            ContextMenu.Button.LMB,
                            listOf(
                                ContextMenu.Item("Force Update") {
                                    val closeLoading = LoadingDialog.show()
                                    try {
                                        if (workspaceServices == null) {
                                            log.error { "workspaceServices is null" }
                                        } else {
                                            workspaceServices.updateConfig(GitUpdatePolicy.REQUIRED)
                                        }
                                    } finally {
                                        closeLoading()
                                    }
                                }
                            )
                        )
                )
            }
            Text(
                "Welcome To Citeck Launcher!",
                fontSize = 3.em,
                modifier = Modifier.align(Alignment.Center).padding(bottom = 435.dp)
            )

            Column(
                modifier = Modifier.align(Alignment.Center).padding(top = 30.dp).width(500.dp),
                horizontalAlignment = Alignment.CenterHorizontally
            ) {
                if (workspaceServices == null) {
                    Text("Workspace Is Empty", fontSize = 1.05.em, textAlign = TextAlign.Center)
                } else {
                    val existingNamespaces = remember(workspaceServices.workspace.id) {
                        val namespaces = mutableStateOf<List<EntityInfo<NamespaceConfig>>>(emptyList())
                        namespaces.value = workspaceServices.entitiesService.find(
                            NamespaceConfig::class,
                            FAST_ACCESS_NAMESPACES_LIMIT
                        )
                        namespaces
                    }
                    if (existingNamespaces.value.isEmpty()) {
                        Column(Modifier.fillMaxWidth().height(250.dp)) {
                            renderQuickStartButtons(workspaceServicesValue)
                        }
                    } else {
                        for (namespace in existingNamespaces.value) {
                            Button(
                                modifier = Modifier.fillMaxWidth().height(60.dp),
                                shape = RoundedCornerShape(16.dp),
                                onClick = {
                                    setSelectedNamespaceSafe(workspaceServices, namespace.ref.localId)
                                }
                            ) {
                                Box {
                                    Column(modifier = Modifier.align(Alignment.Center)) {
                                        Text(
                                            namespace.name,
                                            fontSize = 1.05.em,
                                            textAlign = TextAlign.Center,
                                            modifier = Modifier.fillMaxWidth()
                                        )
                                        Text(
                                            namespace.entity.bundleRef.toString(),
                                            fontSize = 0.8.em,
                                            textAlign = TextAlign.Center,
                                            modifier = Modifier.fillMaxWidth().padding(top = 2.dp)
                                        )
                                    }
                                    CpIcon(
                                        "icons/ellipsis-horizontal-circle.svg",
                                        modifier = Modifier.padding(0.dp)
                                            .requiredSize(25.dp)
                                            .align(Alignment.CenterEnd)
                                            .contextMenu(
                                                ContextMenu.Button.LMB,
                                                listOf(
                                                    ContextMenu.Item("Edit") {
                                                        try {
                                                            workspaceServices.entitiesService.edit(namespace.entity)
                                                            existingNamespaces.value = workspaceServices.entitiesService.find(
                                                                NamespaceConfig::class,
                                                                FAST_ACCESS_NAMESPACES_LIMIT
                                                            )
                                                        } catch (_: FormCancelledException) {
                                                            // do nothing
                                                        }
                                                    },
                                                    ContextMenu.Item("Delete") {
                                                        workspaceServices.entitiesService.delete(namespace.entity)
                                                        existingNamespaces.value = workspaceServices.entitiesService.find(
                                                            NamespaceConfig::class,
                                                            FAST_ACCESS_NAMESPACES_LIMIT
                                                        )
                                                    }
                                                )
                                            )
                                    )
                                }
                            }
                            buttonsSpacer()
                        }
                        Button(
                            modifier = Modifier.fillMaxWidth().height(35.dp),
                            shape = RoundedCornerShape(16.dp),
                            onClick = {
                                Thread.ofPlatform().start {
                                    val newRef = runBlocking {
                                        JournalSelectDialog.show(
                                            JournalSelectDialog.Params(
                                                NamespaceConfig::class,
                                                emptyList(),
                                                false,
                                                entitiesService = workspaceServices.entitiesService,
                                                closeWhenAllRecordsDeleted = true
                                            )
                                        )
                                    }.firstOrNull()
                                    if (newRef != null) {
                                        setSelectedNamespaceSafe(workspaceServices, newRef.localId)
                                    }
                                }
                            }
                        ) {
                            Column {
                                Text(
                                    "More",
                                    fontSize = 1.05.em,
                                    textAlign = TextAlign.Center,
                                    modifier = Modifier.fillMaxWidth()
                                )
                            }
                        }
                        buttonsSpacer()
                    }
                    Button(
                        modifier = Modifier.fillMaxWidth().height(60.dp),
                        shape = RoundedCornerShape(16.dp),
                        onClick = {
                            workspaceServices.entitiesService
                                .create(NamespaceConfig::class, {}, {})
                        }
                    ) {
                        Text("Create New Namespace", fontSize = 1.05.em, textAlign = TextAlign.Center)
                    }
                }
            }
            CpImage(
                "logo/slsoft_full_logo.svg",
                contentScale = ContentScale.FillHeight,
                modifier = Modifier.padding(start = 10.dp, bottom = 5.dp)
                    .requiredHeight(100.dp)
                    .align(Alignment.BottomStart)
            )
            CpImage(
                "logo/citeck_full_logo.svg",
                contentScale = ContentScale.FillHeight,
                modifier = Modifier.padding(bottom = 29.dp, end = 10.dp)
                    .requiredHeight(50.dp)
                    .align(Alignment.BottomEnd)
            )
        }
    }
}

private fun setSelectedNamespaceSafe(workspaceServices: WorkspaceServices, namespaceId: String) {
    try {
        workspaceServices.setSelectedNamespace(namespaceId)
    } catch (e: Throwable) {
        log.error(e) { "Namespace selection failed: $namespaceId" }
        ErrorDialog.show(e)
    }
}

private fun prepareNsDataToCreate(
    workspaceServices: WorkspaceServices,
    quickStart: QuickStartVariant
): NamespaceConfig {

    val workspaceConfig = workspaceServices.workspaceConfig.getValue()
    val namespaceTemplate = if (quickStart.template.isEmpty()) {
        workspaceConfig.defaultNsTemplate
    } else {
        workspaceConfig.namespaceTemplates.first {
            it.id == quickStart.template
        }
    }
    val namespaceConfig = namespaceTemplate.config
        .copy()
        .withName("Citeck Default")
        .withTemplate(namespaceTemplate.id)

    namespaceConfig.withBundleRef(
        quickStart.bundleRef.ifEmpty { namespaceTemplate.config.bundleRef }.ifEmpty {
            val bundleRepoId = workspaceConfig.bundleRepos.first().id
            BundleRef.create(bundleRepoId, "LATEST")
        }
    )

    if (namespaceConfig.bundleRef.key == "LATEST") {
        namespaceConfig.withBundleRef(
            workspaceServices.bundlesService.getLatestRepoBundle(namespaceConfig.bundleRef.repo)
        )
    }

    namespaceConfig.withSnapshot(
        quickStart.snapshot.ifEmpty {
            namespaceTemplate.config.snapshot
        }
    )

    return namespaceConfig.build()
}

@Composable
private fun ColumnScope.renderQuickStartButtons(
    workspaceServicesValue: MutableState<WorkspaceServices?>
) {
    val workspaceServices = workspaceServicesValue.value ?: return

    val quickStartVariants = rememberMutProp(workspaceServices, workspaceServices.workspaceConfig) { config ->
        val variants: List<QuickStartVariant> = config.quickStartVariants.ifEmpty {
            listOf(QuickStartVariant("Quick Start"))
        }
        variants.map { variant ->
            variant to prepareNsDataToCreate(workspaceServices, variant)
        }
    }

    for ((idx, variant) in quickStartVariants.value.withIndex()) {
        renderQuickStartButton(workspaceServicesValue, variant, idx == 0)
        buttonsSpacer()
    }
}

@Composable
private fun buttonsSpacer() {
    Spacer(modifier = Modifier.height(8.dp))
}

@Composable
private fun ColumnScope.renderQuickStartButton(
    workspaceServicesValue: MutableState<WorkspaceServices?>,
    variantAndConfig: Pair<QuickStartVariant, NamespaceConfig>,
    primary: Boolean
) {
    val workspaceServices = workspaceServicesValue.value ?: return
    val (variant, namespaceConfig) = variantAndConfig
    val coroutineScope = rememberCoroutineScope()
    Button(
        modifier = Modifier.fillMaxWidth().weight(if (primary) 0.7f else 0.3f),
        shape = RoundedCornerShape(16.dp),
        onClick = {
            if (workspaceServices.entitiesService.getFirst(NamespaceConfig::class) != null) {
                coroutineScope.launch {
                    MessageDialog.show(
                        "Workspace already has namespaces\nQuick start is disabled."
                    )
                }
            } else {
                Thread.ofPlatform().start {
                    ActionStatus.doWithStatus { actionStatus ->
                        val closeLoadingDialog = LoadingDialog.show(actionStatus)
                        try {
                            workspaceServices.entitiesService.createWithData(namespaceConfig)
                            val runtime = workspaceServices.getCurrentNsRuntime()
                            if (runtime == null) {
                                coroutineScope.launch {
                                    MessageDialog.show("Namespace runtime is null")
                                }
                            } else {
                                runtime.updateAndStart()
                            }
                        } catch (e: Throwable) {
                            log.error(e) { "Quick start completed with error. Variant: $variant" }
                            ErrorDialog.show(e)
                        } finally {
                            closeLoadingDialog()
                        }
                    }
                }
            }
        }
    ) {
        Column(
            modifier = Modifier.fillMaxWidth()
                .align(Alignment.CenterVertically)
        ) {
            Text(
                variant.name,
                fontSize = if (primary) 1.7.em else 1.em,
                textAlign = TextAlign.Center,
                modifier = Modifier.fillMaxWidth()
            )
            Spacer(modifier = Modifier.height(5.dp))
            Text(
                namespaceConfig.bundleRef.toString(),
                textAlign = TextAlign.Center,
                modifier = Modifier.fillMaxWidth()
            )
        }
    }
}
