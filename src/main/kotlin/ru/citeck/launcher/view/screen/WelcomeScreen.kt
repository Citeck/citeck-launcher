package ru.citeck.launcher.view.screen

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Button
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.em
import kotlinx.coroutines.asCoroutineDispatcher
import kotlinx.coroutines.launch
import ru.citeck.launcher.core.LauncherServices
import ru.citeck.launcher.core.namespace.NamespaceDto
import ru.citeck.launcher.core.namespace.NamespaceEntityDef
import ru.citeck.launcher.core.utils.data.DataValue
import ru.citeck.launcher.core.workspace.WorkspaceDto
import ru.citeck.launcher.core.workspace.WorkspaceEntityDef
import ru.citeck.launcher.view.dialog.GlobalMessageDialog
import ru.citeck.launcher.view.form.components.journal.JournalSelectDialog
import ru.citeck.launcher.view.drawable.CpImage
import java.util.concurrent.Executors

val coroutineContext = Executors.newFixedThreadPool(1).asCoroutineDispatcher()

@Composable
fun WelcomeScreen(launcherServices: LauncherServices, selectedWorkspace: MutableState<WorkspaceDto?>) {

    val entitiesService = launcherServices.entitiesService
    val coroutineScope = rememberCoroutineScope { coroutineContext }
    val selectedWsValue = selectedWorkspace.value
    if (selectedWsValue == null) {
        LoadingScreen()
    } else {
        Box(modifier = Modifier.fillMaxSize()) {
            TextButton(
                modifier = Modifier
                    .align(Alignment.TopStart)
                    .padding(5.dp),
                onClick = {
                    coroutineScope.launch {
                        val currentRef = WorkspaceEntityDef.getRef(selectedWsValue)
                        val newRef = JournalSelectDialog.show(
                            JournalSelectDialog.Params(
                                WorkspaceDto::class,
                                listOf(WorkspaceEntityDef.getRef(selectedWsValue)),
                                false
                            )
                        ).firstOrNull() ?: currentRef
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

            Text(
                "Welcome To Citeck Launcher!",
                fontSize = 3.em,
                modifier = Modifier.align(Alignment.Center).padding(bottom = 435.dp)
            )

            Column(
                modifier = Modifier.align(Alignment.Center).padding(top = 30.dp).width(500.dp),
                horizontalAlignment = Alignment.CenterHorizontally
            ) {
                val workspaceServices = launcherServices.getWorkspaceServices()
                var defaultBundleRef = workspaceServices.workspaceConfig.defaultBundleRef
                if (defaultBundleRef.key == "LATEST") {
                    defaultBundleRef = workspaceServices.bundlesService
                        .getLatestRepoBundle(defaultBundleRef.repo)
                        .ifEmpty { defaultBundleRef }
                }
                val existingNamespaces = remember(workspaceServices.workspace.id) {
                    workspaceServices.entitiesService.find(NamespaceDto::class, 3)
                }
                if (existingNamespaces.isEmpty()) {
                    Button(
                        modifier = Modifier.fillMaxWidth().height(250.dp),
                        shape = RoundedCornerShape(16.dp),
                        onClick = {
                            if (workspaceServices.entitiesService.getFirst(NamespaceDto::class) != null) {
                                coroutineScope.launch {
                                    GlobalMessageDialog.show(
                                        "Workspace already has namespaces\nFast start is disabled."
                                    )
                                }
                            } else {
                                workspaceServices.entitiesService.createWithData(
                                    NamespaceDto.Builder()
                                        .withName("Citeck Default")
                                        .withBundleRef(defaultBundleRef)
                                        .build()
                                )
                                val runtime = workspaceServices.getCurrentNsRuntime()
                                if (runtime == null) {
                                    coroutineScope.launch {
                                        GlobalMessageDialog.show("Namespace runtime is null")
                                    }
                                } else {
                                    runtime.start()
                                }
                            }
                        }
                    ) {
                        Box {
                            Text(
                                "Fast Start",
                                fontSize = 2.em,
                                textAlign = TextAlign.Center,
                                modifier = Modifier.fillMaxWidth().align(Alignment.Center)
                            )
                            Spacer(modifier = Modifier.height(10.dp))
                            Text(
                                defaultBundleRef.toString(),
                                textAlign = TextAlign.Center,
                                modifier = Modifier.fillMaxWidth().align(Alignment.Center).padding(top = 60.dp)
                            )
                        }
                    }
                    Spacer(modifier = Modifier.height(8.dp))
                } else {
                    for (namespace in existingNamespaces) {
                        Button(
                            modifier = Modifier.fillMaxWidth().height(60.dp),
                            shape = RoundedCornerShape(16.dp),
                            onClick = {
                                workspaceServices.setSelectedNamespace(namespace.ref.localId)
                            }
                        ) {
                            Column {
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
                        }
                        Spacer(modifier = Modifier.height(8.dp))
                    }
                }
                Button(
                    modifier = Modifier.fillMaxWidth().height(60.dp),
                    shape = RoundedCornerShape(16.dp),
                    onClick = {
                        launcherServices.getWorkspaceServices()
                            .entitiesService
                            .create(
                                NamespaceDto::class,
                                DataValue.createObj()
                                    .set(NamespaceEntityDef.FORM_FIELD_BUNDLES_REPO, defaultBundleRef.repo)
                                    .set(NamespaceEntityDef.FORM_FIELD_BUNDLE_KEY, defaultBundleRef.key),
                                {}, {}
                            )
                    }
                ) {
                    Text("Create New Namespace", fontSize = 1.05.em, textAlign = TextAlign.Center)
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
