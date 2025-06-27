package ru.citeck.launcher.core.secrets.storage

import ru.citeck.launcher.core.utils.json.Json
import java.security.SecureRandom
import javax.crypto.Cipher
import javax.crypto.SecretKeyFactory
import javax.crypto.spec.GCMParameterSpec
import javax.crypto.spec.PBEKeySpec
import javax.crypto.spec.SecretKeySpec
import kotlin.reflect.KClass

object SecretsEncryptor {

    private const val PBKDF2_ITERATIONS: Int = 1_000_000
    private const val AES_KEY_SIZE: Int = 256
    private const val SALT_LENGTH: Int = 16

    private const val GCM_IV_LENGTH: Int = 16
    private const val GCM_TAG_LENGTH: Int = 128

    fun encrypt(store: Any, key: ByteArray, keyParams: KeyParams): EncryptedStorage {
        if (key.size * 8 != AES_KEY_SIZE) {
            error("Invalid key length: ${key.size}")
        }

        val iv = ByteArray(GCM_IV_LENGTH)
        SecureRandom().nextBytes(iv)

        val cipher = Cipher.getInstance("AES/GCM/NoPadding")
        val keySpec = SecretKeySpec(key, "AES")
        val gcmSpec = GCMParameterSpec(GCM_TAG_LENGTH, iv)

        cipher.init(Cipher.ENCRYPT_MODE, keySpec, gcmSpec)
        val encrypted = cipher.doFinal(Json.toBytes(store))

        return EncryptedStorage(keyParams, 0, iv, GCM_TAG_LENGTH, encrypted)
    }

    fun <T : Any> decrypt(store: EncryptedStorage, key: ByteArray, type: KClass<T>): T {
        if (key.size * 8 != AES_KEY_SIZE) {
            error("Invalid key length: ${key.size}")
        }
        if (store.alg != 0) {
            error("Invalid algorithm: ${store.alg}")
        }
        val cipher = Cipher.getInstance("AES/GCM/NoPadding")
        val keySpec = SecretKeySpec(key, "AES")
        val gcmSpec = GCMParameterSpec(store.tagLen, store.iv)

        cipher.init(Cipher.DECRYPT_MODE, keySpec, gcmSpec)
        val decryptedBytes = cipher.doFinal(store.data, 0, store.data.size)

        return Json.read(decryptedBytes, type)
    }

    fun createKeyParams(): KeyParams {
        val salt = ByteArray(SALT_LENGTH)
        SecureRandom().nextBytes(salt)
        return KeyParams(0, salt, AES_KEY_SIZE, PBKDF2_ITERATIONS)
    }

    fun deriveKey(password: CharArray, params: KeyParams): ByteArray {
        if (params.alg != 0) {
            error("Invalid algorithm: ${params.alg}")
        }
        val spec = PBEKeySpec(password, params.salt, params.iterations, params.keySize)
        val factory = SecretKeyFactory.getInstance("PBKDF2WithHmacSHA256")
        val res = factory.generateSecret(spec).encoded
        password.fill('0')
        return res
    }
}
