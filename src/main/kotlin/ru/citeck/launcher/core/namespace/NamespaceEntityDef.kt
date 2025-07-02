package ru.citeck.launcher.core.namespace

import ru.citeck.launcher.core.config.bundle.BundleRef
import ru.citeck.launcher.core.entity.EntityDef
import ru.citeck.launcher.core.entity.EntityIdType
import ru.citeck.launcher.core.entity.EntityRef
import ru.citeck.launcher.core.utils.data.DataValue
import ru.citeck.launcher.view.form.spec.ComponentSpec.NameField
import ru.citeck.launcher.view.form.spec.ComponentSpec.SelectField
import ru.citeck.launcher.view.form.spec.FormSpec

object NamespaceEntityDef {

    const val FORM_FIELD_BUNDLES_REPO = "bundlesRepo"
    const val FORM_FIELD_BUNDLE_KEY = "bundleKey"

    val formSpec = FormSpec(
        "Namespace",
        components = listOf(
            NameField(),
            SelectField(
                FORM_FIELD_BUNDLES_REPO,
                "Bundles Repo",
                "",
                options = { ctx ->
                    ctx.workspaceServices
                        ?.workspaceConfig
                        ?.value
                        ?.bundleRepos
                        ?.map { SelectField.Option(it.id, it.name) } ?: emptyList()
                }
            ).mandatory(),
            SelectField(
                FORM_FIELD_BUNDLE_KEY,
                "Bundle",
                "",
                dependsOn = setOf(FORM_FIELD_BUNDLES_REPO),
                options = { ctx ->
                    ctx.workspaceServices
                        ?.bundlesService
                        ?.getRepoBundles(ctx.getStrValue(FORM_FIELD_BUNDLES_REPO))
                        ?.map { SelectField.Option(it.key, it.key) } ?: emptyList()
                }
            ).mandatory()
        )
    )

    val definition = EntityDef(
        idType = EntityIdType.String,
        valueType = NamespaceDto::class,
        typeId = "namespace",
        typeName = "Namespace",
        getId = { it.id },
        getName = { it.name },
        createForm = formSpec,
        editForm = null,
        defaultEntities = emptyList(),
        actions = emptyList(),
        toFormData = {
            val data = DataValue.of(it)
            data[FORM_FIELD_BUNDLES_REPO] = it.bundleRef.repo
            data[FORM_FIELD_BUNDLE_KEY] = it.bundleRef.key
            data
        },
        fromFormData = {
            val bundleRef = BundleRef.create(
                it[FORM_FIELD_BUNDLES_REPO].asText(),
                it[FORM_FIELD_BUNDLE_KEY].asText()
            )
            it["bundleRef"] = bundleRef
            it.getAsNotNull(NamespaceDto::class)
        }
    )

    fun getRef(entity: NamespaceDto): EntityRef {
        return EntityRef.create(definition.typeId, entity.id)
    }

}
