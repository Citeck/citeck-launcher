package ru.citeck.launcher.core.entity

import androidx.compose.runtime.Stable
import kotlinx.coroutines.suspendCancellableCoroutine
import ru.citeck.launcher.core.LauncherServices
import ru.citeck.launcher.core.database.Database
import ru.citeck.launcher.core.database.Repository
import ru.citeck.launcher.core.entity.events.EntitiesEvents
import ru.citeck.launcher.core.entity.events.EntityCreatedEvent
import ru.citeck.launcher.core.entity.events.EntityDeletedEvent
import ru.citeck.launcher.core.utils.ActionStatus
import ru.citeck.launcher.core.utils.IdUtils
import ru.citeck.launcher.core.utils.data.DataValue
import ru.citeck.launcher.core.utils.json.Json
import ru.citeck.launcher.core.workspace.WorkspaceDto
import ru.citeck.launcher.view.action.ActionDesc
import ru.citeck.launcher.view.action.ActionIcon
import ru.citeck.launcher.view.dialog.GlobalConfirmDialog
import ru.citeck.launcher.view.dialog.GlobalLoadingDialog
import ru.citeck.launcher.view.form.FormMode
import ru.citeck.launcher.view.form.GlobalFormDialog
import ru.citeck.launcher.view.form.WorkspaceFormDialog
import ru.citeck.launcher.view.form.exception.FormCancelledException
import java.util.concurrent.ConcurrentHashMap
import kotlin.coroutines.resume
import kotlin.coroutines.resumeWithException
import kotlin.reflect.KClass

@Stable
class EntitiesService(
    private val workspaceId: String
) {

    companion object {
        private const val ATT_ID = "id"

        private const val HISTORY_MAX_COUNT = 10
    }

    private val definitionsByClass = ConcurrentHashMap<KClass<*>, EntityDefWithRepo<*, *>>()
    private val definitionsByTypeId = ConcurrentHashMap<String, EntityDefWithRepo<*, *>>()

    private val defaultEntitiesByRef = ConcurrentHashMap<EntityRef, EntityInfo<Any>>()

    private lateinit var database: Database
    private lateinit var launcherEntities: EntitiesService

    private lateinit var idCounters: Repository<String, Long>

    val events = EntitiesEvents()

    private val formDialog = if (workspaceId == WorkspaceDto.GLOBAL_WS_ID) {
        GlobalFormDialog
    } else {
        WorkspaceFormDialog
    }

    fun init(services: LauncherServices) {
        this.database = services.database
        idCounters = database.getRepo(
            EntityIdType.String,
            Json.getSimpleType(Long::class),
            "entities-service",
            "idCounters"
        )
        launcherEntities = services.entitiesService
    }

    fun isEntityCreatable(entityType: KClass<*>): Boolean {
        val defWithRepo = getDefWithRepo<Any, Any>(entityType)
        return defWithRepo.definition.createForm != null
    }

    fun getTypeName(entityType: KClass<*>): String {
        val defWithRepo = getDefWithRepo<Any, Any>(entityType)
        return defWithRepo.definition.typeName
    }

    fun getTypeIdByClass(entityType: KClass<*>): String {
        val defWithRepo = getDefWithRepo<Any, Any>(entityType)
        return defWithRepo.definition.typeId
    }

    fun <T : Any> getByRefs(refs: List<EntityRef>): List<EntityInfo<T>> {
        val result = ArrayList<EntityInfo<T>>()
        for (ref in refs) {
            getByRef<T>(ref)?.let { result.add(it) }
        }
        return result
    }

    fun <T : Any> getByRef(entityRef: EntityRef): EntityInfo<T>? {
        if (entityRef.isEmpty()) {
            return null
        }
        val defWithRepo = getDefWithRepo<Any, T>(entityRef.typeId)
        getDefaultEntity<T>(entityRef)?.let { return it }
        val idType = defWithRepo.definition.idType as EntityIdType<*>
        val idValue = when (idType) {
            EntityIdType.Long -> entityRef.localId.toLong()
            EntityIdType.String -> entityRef.localId
        }
        val entity = defWithRepo.repository.get(idValue) ?: return null
        return wrapEntitiesToInfo(defWithRepo, listOf(entity)).first()
    }

    fun <T : Any> getFirst(type: KClass<T>): EntityInfo<T>? {
        val defWithRepo = getDefWithRepo<Any, T>(type)
        if (defWithRepo.defaultEntitiesInfo.isNotEmpty()) {
            return defWithRepo.defaultEntitiesInfo.first()
        }
        return defWithRepo.repository.getFirst()?.let {
            wrapEntitiesToInfo(defWithRepo, listOf(it))[0]
        }
    }

    fun <T : Any> getById(type: KClass<T>, id: Any): EntityInfo<T>? {
        val defWithRepo = getDefWithRepo<Any, T>(type)
        getDefaultEntity<T>(defWithRepo.getRef(id))?.let { return it }
        val entity = defWithRepo.repository[id] ?: return null
        return wrapEntitiesToInfo(defWithRepo, listOf(entity)).first()
    }

    private fun <T : Any> getDefaultEntity(ref: EntityRef): EntityInfo<T>? {
        defaultEntitiesByRef[ref]?.let {
            @Suppress("UNCHECKED_CAST")
            return it as EntityInfo<T>
        }
        if (launcherEntities !== this) {
            return launcherEntities.getDefaultEntity(ref)
        }
        return null
    }

    fun <T : Any> find(type: KClass<out T>, max: Int): List<EntityInfo<T>> {
        val defWithRepo = getDefWithRepo<Any, T>(type)
        val result = ArrayList(defWithRepo.defaultEntitiesInfo)
        result.addAll(wrapEntitiesToInfo(defWithRepo, defWithRepo.repository.find(max)))
        return result
    }

    fun <T : Any> getAll(type: KClass<out T>): List<EntityInfo<T>> {
        val defWithRepo = getDefWithRepo<Any, T>(type)
        val result = ArrayList(defWithRepo.defaultEntitiesInfo)
        result.addAll(wrapEntitiesToInfo(defWithRepo, defWithRepo.repository.find(100_000)))
        return result
    }

    private fun <T : Any> wrapEntitiesToInfo(defWithRepo: EntityDefWithRepo<Any, T>, entities: List<T>): List<EntityInfo<T>> {
        val definition = defWithRepo.definition
        val actions = ArrayList<ActionDesc<EntityInfo<T>>>()
        if (definition.editForm != null || definition.createForm != null) {
            actions.add(
                ActionDesc(
                "edit",
                ActionIcon.EDIT,
                "Edit"
            ) {
                try {
                    edit(it.entity)
                } catch (e: FormCancelledException) {
                    // do nothing
                }
            })
        }
        actions.add(ActionDesc(
            "delete",
            ActionIcon.DELETE,
            "Delete",
        ) {
            val def = defWithRepo.definition
            val confirmRes = GlobalConfirmDialog.showSuspended(
                "Are you sure to delete \n${def.typeName} " +
                    "'${def.getName(it.entity)} (${def.getId(it.entity)}')?"
            )
            if (confirmRes) {
                delete(it.entity, defWithRepo)
            }
        })
        return entities.map { entity ->
            EntityInfo(
                EntityRef.create(definition.typeId, definition.getId(entity).toString()),
                definition.getName(entity),
                actions,
                entity
            )
        }
    }

    fun <T : Any> delete(entity: T) {
        delete(entity, getDefWithRepo(entity::class))
    }

    private fun <T : Any> delete(entity: T, defWithRepo: EntityDefWithRepo<Any, T>) {
        val entityId = defWithRepo.definition.getId(entity)
        val existingEntity = defWithRepo.repository[entityId] ?: return
        storeValueToVersions(defWithRepo, existingEntity)
        database.doWithinTxn {
            defWithRepo.repository.delete(entityId)
            events.fireEntityDeletedEvent(defWithRepo.definition.valueType, EntityDeletedEvent(existingEntity))
        }
    }

    suspend fun <T : Any> create(type: KClass<T>): EntityRef {
        return suspendCancellableCoroutine { continuation ->
            create(
                type,
                onCancel = { continuation.resumeWithException(FormCancelledException()) },
                onSubmit = { continuation.resume(it) }
            )
        }
    }

    fun <T : Any> create(
        type: KClass<T>,
        onCancel: () -> Unit,
        onSubmit: (EntityRef) -> Unit
    ) {
        create(type, DataValue.NULL, onCancel, onSubmit)
    }

    fun <T : Any> create(
        type: KClass<T>,
        initialData: DataValue,
        onCancel: () -> Unit,
        onSubmit: (EntityRef) -> Unit
    ) {

        val defWithRepo = getDefWithRepo<Any, T>(type) as EntityDefWithRepo<*, T>
        val definition = defWithRepo.definition

        formDialog.show(
            spec = definition.createForm!!,
            mode = FormMode.CREATE,
            data = initialData,
            onCancel = { onCancel() }
        ) { data, onDataProcComplete ->
            val idType = definition.idType

            @Suppress("UNCHECKED_CAST")
            val repo = defWithRepo.repository as Repository<Any, T>

            val idValue = if (data.has("id")) {
                val idValue = when (idType) {
                    EntityIdType.String -> data["id"].asText()
                    EntityIdType.Long -> data["id"].asLong(-1L)
                }
                @Suppress("UNCHECKED_CAST")
                idType as EntityIdType<Any>
                if (!idType.isValidId(idValue)) {
                    error("Invalid id: '$idValue'")
                }
                if (repo[idValue] != null) {
                    error("Entity with id '$idValue' already exists'")
                }
                idValue
            } else {
                val idForNewEntity = generateNewId(defWithRepo)
                data[ATT_ID] = idForNewEntity
                idForNewEntity
            }
            val convertedEntity = definition.fromFormData(data)

            Thread.ofPlatform().name("create-entity-$idType").start {
                ActionStatus.doWithStatus { status ->
                    val closeLoading = GlobalLoadingDialog.show(status)
                    try {
                        database.doWithinTxn {
                            repo[idValue] = convertedEntity
                            events.fireEntityCreatedEvent(
                                defWithRepo.definition.valueType,
                                EntityCreatedEvent(convertedEntity)
                            )
                        }
                        onSubmit(EntityRef.create(definition.typeId, idValue.toString()))
                        onDataProcComplete()
                    } finally {
                        closeLoading()
                    }
                }
            }
        }
    }

    private fun <K : Any, T : Any> generateNewId(defWithRepo: EntityDefWithRepo<K, T>): K {

        val definition = defWithRepo.definition
        val repo = defWithRepo.repository
        var resultId: K

        var iteration = -1
        do {
            if (++iteration > 1000) {
                error("Something went wrong. We can't create unique id for new entity of type '${definition.typeId}'")
            }
            val rawId = when (definition.idType) {
                EntityIdType.String -> {
                    IdUtils.createStrId(iteration > 10)
                }
                EntityIdType.Long -> {
                    database.doWithinNewTxn {
                        val counterNextValue = (idCounters[definition.typeId] ?: 99L) + 1
                        idCounters[definition.typeId] = counterNextValue
                        counterNextValue
                    }
                }
            }
            @Suppress("UNCHECKED_CAST")
            resultId = rawId as K
        } while (repo[resultId] != null)

        return resultId
    }

    fun <T : Any> createWithData(entity: T) {

        val defWithRepo = getDefWithRepo<Any, T>(entity::class)
        val idType = defWithRepo.definition.idType

        var id = defWithRepo.definition.getId(entity)
        var entityToSave = entity
        if (idType.isEmpty(id)) {
            id = generateNewId(defWithRepo)
            entityToSave = DataValue.of(entity)
                .set(ATT_ID, id)
                .getAsNotNull(entity::class)
        } else {
            if (!defWithRepo.definition.idType.isValidId(id)) {
                error("Invalid id: '$id'")
            }
        }
        database.doWithinTxn {
            if (defWithRepo.repository[id] != null) {
                error("Entity with id '$id' already exists'")
            }
            defWithRepo.repository[id] = entityToSave

            events.fireEntityCreatedEvent(defWithRepo.definition.valueType, EntityCreatedEvent(entityToSave))
        }
    }

    suspend fun <T : Any> edit(entity: T) {

        val defWithRepo = getDefWithRepo<Any, T>(entity::class)
        val definition = defWithRepo.definition

        val currentData = definition.toFormData(entity)
        val editedEntityId = definition.getId(entity)
        if (!definition.idType.isValidId(editedEntityId)) {
            error("Entity can't be edited because id '$editedEntityId' is invalid. Entity: $entity")
        }
        val newData = formDialog.show(
            spec = definition.editForm ?: definition.createForm!!,
            FormMode.EDIT,
            data = currentData.copy()
        )
        newData[ATT_ID] = editedEntityId
        val newEntity: T = definition.fromFormData(newData)

        database.doWithinTxn {
            storeValueToVersions(defWithRepo, entity)
            defWithRepo.repository[editedEntityId] = newEntity
        }
    }

    private fun <K : Any, T : Any> storeValueToVersions(defWithRepo: EntityDefWithRepo<K, T> , entity: T) {
        if (!defWithRepo.definition.versionable) {
            return
        }
        val entityId = defWithRepo.definition.getId(entity)
        if (!defWithRepo.definition.idType.isValidId(entityId)) {
            error("Entity can't be stored because id '$entityId' is invalid. Entity: $entity")
        }
        val versions = ArrayList(defWithRepo.versionsRepo[entityId] ?: emptyList())
        versions.add(0, entity)
        while (versions.size > HISTORY_MAX_COUNT) {
            versions.removeLast()
        }
        defWithRepo.versionsRepo[entityId] = versions
    }

    private fun <K : Any, T : Any> getRepo(type: KClass<T>): Repository<K, T> {
        return getDefWithRepo<K, T>(type).repository
    }

    private fun <K : Any, T : Any> getRepo(typeId: String): Repository<K, T> {
        return getDefWithRepo<K, T>(typeId).repository
    }

    private fun <K : Any, T : Any> getDefWithRepo(typeId: String): EntityDefWithRepo<K, T> {
        return getDefWithRepo(typeId) { it.definitionsByTypeId }
    }

    private fun <K : Any, T : Any> getDefWithRepo(clazz: KClass<out T>): EntityDefWithRepo<K, T> {
        return getDefWithRepo(clazz) { it.definitionsByClass }
    }

    private fun <K0 : Any, K1 : Any, T : Any> getDefWithRepo(
        key: K0,
        getRegistry: (EntitiesService) -> Map<K0, EntityDefWithRepo<*, *>>
    ): EntityDefWithRepo<K1, T> {
        var defWithRepo = getRegistry(this)[key]
        if (defWithRepo == null && launcherEntities !== this) {
            defWithRepo = getRegistry(launcherEntities)[key]
        }
        @Suppress("UNCHECKED_CAST")
        return defWithRepo as? EntityDefWithRepo<K1, T> ?: error("No definition for entity '$key'. Workspace: $workspaceId")
    }

    fun <K : Any, T : Any> register(definition: EntityDef<K, T>) {

        val entityType = Json.getSimpleType(definition.valueType)
        val listType = Json.getListType(definition.valueType)

        val baseRepoKey = "entities/$workspaceId"

        val defaultEntitiesInfo = definition.defaultEntities.map {
            EntityInfo(
                EntityRef.create(definition.typeId, definition.getId(it).toString()),
                definition.getName(it),
                emptyList(),
                it
            )
        }
        defaultEntitiesInfo.forEach {
            @Suppress("UNCHECKED_CAST")
            defaultEntitiesByRef[it.ref] = it as EntityInfo<Any>
        }

        val defWithRepo = EntityDefWithRepo(
            definition,
            defaultEntitiesInfo,
            definition.customRepo ?: database.getRepo(
                definition.idType,
                entityType,
                baseRepoKey,
                definition.typeId
            ),
            database.getRepo(
                definition.idType,
                listType,
                "$baseRepoKey/versions",
                definition.typeId
            )
        )
        definitionsByClass[definition.valueType] = defWithRepo
        definitionsByTypeId[definition.typeId] = defWithRepo
    }

    private class EntityDefWithRepo<K : Any, T : Any>(
        val definition: EntityDef<K, T>,
        val defaultEntitiesInfo: List<EntityInfo<T>>,
        val repository: Repository<K, T>,
        val versionsRepo: Repository<K, List<T>>
    ) {

        fun getRef(localId: K): EntityRef {
            return EntityRef.create(definition.typeId, localId.toString())
        }
    }
}
