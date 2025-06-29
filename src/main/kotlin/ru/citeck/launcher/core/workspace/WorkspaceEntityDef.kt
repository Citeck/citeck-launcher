package ru.citeck.launcher.core.workspace

import ru.citeck.launcher.core.entity.EntityDef
import ru.citeck.launcher.core.entity.EntityIdType
import ru.citeck.launcher.core.entity.EntityRef
import ru.citeck.launcher.core.secrets.auth.AuthType
import ru.citeck.launcher.view.form.spec.ComponentSpec.*
import ru.citeck.launcher.view.form.spec.FormSpec

object WorkspaceEntityDef {

    val formSpec = FormSpec(
        "Workspace",
        components = listOf(
            NameField(),
            TextField("repoUrl", "Repo URL").mandatory(),
            TextField("repoBranch", "Repo Branch", defaultValue = "main").mandatory(),
            TextField("repoPullPeriod", "Pull Period (ISO 8601)", defaultValue = "PT2H").mandatory(),
            SelectField(
                "authType",
                "Auth Type",
                AuthType.NONE.name,
                listOf(AuthType.NONE, AuthType.TOKEN).map {
                    SelectField.Option(it.name, it.displayName)
                }
            )
        )
    )

    val definition = EntityDef(
        idType = EntityIdType.String,
        valueType = WorkspaceDto::class,
        typeId = "workspace",
        typeName = "Workspace",
        getId = { it.id },
        getName = { it.name },
        createForm = formSpec,
        editForm = null,
        defaultEntities = listOf(WorkspaceDto.DEFAULT),
        actions = emptyList(),
    )

    fun getRef(entity: WorkspaceDto): EntityRef {
        return EntityRef.create(definition.typeId, entity.id)
    }
}
