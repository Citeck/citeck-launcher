package ru.citeck.launcher.core.secrets.storage

import io.github.oshai.kotlinlogging.KotlinLogging
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.suspendCancellableCoroutine
import ru.citeck.launcher.core.database.Database
import ru.citeck.launcher.core.database.Repository
import ru.citeck.launcher.core.entity.EntityIdType
import ru.citeck.launcher.core.utils.data.DataValue
import ru.citeck.launcher.core.utils.json.Json
import ru.citeck.launcher.view.commons.dialog.AskMasterPasswordDialog
import ru.citeck.launcher.view.commons.dialog.CreateMasterPwdDialog
import ru.citeck.launcher.view.commons.dialog.GlobalMessageDialog
import ru.citeck.launcher.view.commons.dialog.GlobalMsgDialogParams
import ru.citeck.launcher.view.popup.DialogWidth
import kotlin.coroutines.resume
import kotlin.reflect.KClass

class SecretsStorage {

    companion object {
        private const val STORAGE_KEY = "storage"
        private val log = KotlinLogging.logger {}
    }

    @Volatile
    private lateinit var keyParams: KeyParams

    @Volatile
    private var masterKey: ByteArray = ByteArray(0)

    @Volatile
    private var secrets: DataValue = DataValue.NULL
    private lateinit var secretsRepo: Repository<String, ByteArray>

    fun init(database: Database) {
        secretsRepo = database.getRepo(
            EntityIdType.String,
            Json.getSimpleType(ByteArray::class),
            "secrets",
            "data"
        )
    }

    private fun askMasterPassword(
        storage: EncryptedStorage,
        onSubmit: (DataValue, ByteArray) -> Unit,
        onReset: () -> Unit
    ) {
        AskMasterPasswordDialog.show({ rawPwd ->

            val masterKey = SecretsEncryptor.deriveKey(rawPwd, keyParams)
            val secrets = try {
                SecretsEncryptor.decrypt(storage, masterKey, DataValue::class)
            } catch (_: Throwable) {
                DataValue.NULL
            }
            if (!secrets.isObject()) {
                runBlocking {
                    GlobalMessageDialog.show(
                        GlobalMsgDialogParams(
                            "Invalid password",
                            "",
                            width = DialogWidth.EXTRA_SMALL
                        )
                    )
                }
                false
            } else {
                onSubmit(secrets, masterKey)
                true
            }
        }, onReset)
    }

    private suspend fun loadSecrets() {
        val storageData = secretsRepo[STORAGE_KEY]
        try {
            if (storageData != null) {
                val encryptedStorage = Json.read(storageData, EncryptedStorage::class)
                keyParams = encryptedStorage.key

                suspendCancellableCoroutine { continuation ->
                    askMasterPassword(
                        encryptedStorage,
                        onSubmit = { secrets, key ->
                            this.secrets = secrets
                            this.masterKey = key
                            continuation.resume(Unit)
                        },
                        onReset = {
                            secretsRepo.delete(STORAGE_KEY)
                            secrets = DataValue.createObj()
                            keyParams = SecretsEncryptor.createKeyParams()
                            continuation.resume(Unit)
                        }
                    )
                }
            } else {
                secrets = DataValue.createObj()
                keyParams = SecretsEncryptor.createKeyParams()
            }
        } catch (e: Exception) {
            log.error(e) { "Secrets storage reading failed" }
            secrets = DataValue.createObj()
            keyParams = SecretsEncryptor.createKeyParams()
        }
    }

    suspend fun initMasterPassword() {
        if (masterKey.isNotEmpty()) {
            return
        }
        loadSecrets()
        if (masterKey.isNotEmpty()) {
            return
        }
        val masterKeyPwd = CreateMasterPwdDialog.showSuspend()
        this.masterKey = SecretsEncryptor.deriveKey(masterKeyPwd, keyParams)
        writeSecretsToDisk()
    }

    fun <T : Any> getList(key: String, elementType: KClass<T>): List<T> {
        return get(key).asList(elementType)
    }

    fun put(key: String, value: Any?) {
        secrets[key] = value
        writeSecretsToDisk()
    }

    fun get(key: String): DataValue {
        return secrets[key]
    }

    private fun writeSecretsToDisk() {
        if (masterKey.isEmpty()) {
            error("Master key is empty")
        }
        secretsRepo[STORAGE_KEY] = Json.toBytes(SecretsEncryptor.encrypt(secrets, masterKey, keyParams))
    }
}
