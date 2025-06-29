package ru.citeck.launcher.core.secrets.auth

import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.sync.Semaphore
import ru.citeck.launcher.core.LauncherServices
import ru.citeck.launcher.core.entity.EntityDef
import ru.citeck.launcher.core.entity.EntityIdType
import ru.citeck.launcher.core.namespace.runtime.actions.AuthenticationCancelled
import ru.citeck.launcher.core.secrets.storage.SecretsStorage
import ru.citeck.launcher.core.utils.json.Json
import ru.citeck.launcher.view.form.GlobalFormDialog
import ru.citeck.launcher.view.form.exception.FormCancelledException
import ru.citeck.launcher.view.form.spec.ComponentSpec
import ru.citeck.launcher.view.form.spec.FormSpec
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.atomic.AtomicBoolean

class AuthSecretsService {

    companion object {
        private const val SECRETS_STORAGE_KEY = "auth-secrets"
    }

    private lateinit var secrets: SecretsMap

    private lateinit var secretsStorage: SecretsStorage

    private val semaphore = Semaphore(1)

    private val initialized = AtomicBoolean(false)

    fun init(services: LauncherServices) {
        secretsStorage = services.secretsStorage
    }

    fun getSecretEntityDef(): EntityDef<String, AuthSecret> {
        return EntityDef(
            EntityIdType.String,
            AuthSecret::class,
            "Auth Secret",
            "auth-secret",
            { it.id},
            { it.id },
            createForm = null,
            editForm = null,
            emptyList(),
            emptyList(),
            customRepo = AuthSecretsRepo(this),
            versionable = false
        )
    }

    fun deleteSecret(id: String) {
        secrets.remove(id)
        secretsStorage.put(SECRETS_STORAGE_KEY, secrets)
    }

    fun getSecrets(): List<AuthSecret> {
        return runBlocking {
            getSecretsMap().values.toList()
        }
    }

    internal suspend fun getSecretsMap(): SecretsMap {
        if (initialized.compareAndSet(false, true)) {
            secretsStorage.initMasterPassword()
            val secretsFromStorage = secretsStorage.get(SECRETS_STORAGE_KEY)
            secrets = if (secretsFromStorage.isObject()) {
                secretsFromStorage.forEach { k, value ->
                    if (value["id"].isEmpty()) {
                        value["id"] = k
                    }
                    if (value["version"].isNull()) {
                        value["version"] = 1000
                    }
                }
                Json.convert(secretsFromStorage, SecretsMap::class)
            } else {
                SecretsMap()
            }
        }
        return secrets
    }

    suspend fun getSecret(params: SecretDef, requiredFor: String, resetSecret: Boolean): AuthSecret {
        return getSecret(params, requiredFor) { resetSecret }
    }

    suspend fun getSecret(params: SecretDef, requiredFor: String, updateVersion: Long): AuthSecret {
        return getSecret(params, requiredFor) { it.version == updateVersion }
    }

    private suspend fun getSecret(
        params: SecretDef,
        requiredFor: String,
        isSecretShouldBeUpdated: (AuthSecret) -> Boolean
    ): AuthSecret {
        semaphore.acquire()
        try {
            val secrets = getSecretsMap()
            val existing = secrets[params.id]
            if (existing != null && existing.isValid() && !isSecretShouldBeUpdated(existing)) {
                return existing
            }
            val secret = try {
                when (params.type) {
                    AuthType.BASIC -> {
                        val pwdData = GlobalFormDialog.show(
                            FormSpec(
                                "Login",
                                components = listOf(
                                    ComponentSpec.Text("Enter username and password for $requiredFor"),
                                    ComponentSpec.TextField("username", "Username").mandatory(),
                                    ComponentSpec.PasswordField("password", "Password").mandatory()
                                ),
                            )
                        )
                        AuthSecret.Basic(
                            params.id,
                            System.currentTimeMillis(),
                            pwdData["username"].asText(),
                            pwdData["password"].asText().toCharArray()
                        )
                    }
                    AuthType.TOKEN -> {
                        val pwdData = GlobalFormDialog.show(
                            FormSpec(
                                "Token",
                                components = listOf(
                                    ComponentSpec.Text("Enter token for $requiredFor"),
                                    ComponentSpec.PasswordField("token", "Token").mandatory()
                                )
                            )
                        )
                        AuthSecret.Token(
                            params.id,
                            System.currentTimeMillis(),
                            pwdData["token"].asText()
                        )
                    }
                    else -> error("Unsupported secret type: ${params.type}")
                }
            } catch (e: FormCancelledException) {
                throw AuthenticationCancelled(params, requiredFor)
            }
            secrets[params.id] = secret
            secretsStorage.put(SECRETS_STORAGE_KEY, secrets)
            return secret
        } finally {
            semaphore.release()
        }
    }

    internal class SecretsMap : ConcurrentHashMap<String, AuthSecret>()
}
