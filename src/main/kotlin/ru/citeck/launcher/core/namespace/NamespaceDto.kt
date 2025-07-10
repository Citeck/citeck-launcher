package ru.citeck.launcher.core.namespace

import com.fasterxml.jackson.databind.annotation.JsonDeserialize
import ru.citeck.launcher.core.config.bundle.BundleRef
import ru.citeck.launcher.core.utils.data.DataValue
import ru.citeck.launcher.core.utils.json.Json

@JsonDeserialize(builder = NamespaceDto.Builder::class)
class NamespaceDto(
    val id: String,
    val name: String,
    val snapshot: String,
    val template: String,
    val authentication: AuthenticationProps,
    val bundleRef: BundleRef,
    val pgAdmin: PgAdminProps,
    val mongodb: MongoDbProps,
    val citeckProxy: ProxyProps,
    val alfresco: AlfrescoProps,
    val webapps: Map<String, WebappProps>
) {

    companion object {
        val DEFAULT = Builder().build()
    }

    fun copy(): Builder {
        return Builder(this)
    }

    class Builder() {

        var id: String = ""
        var name: String = ""
        var snapshot: String = ""
        var template: String = ""
        var authentication: AuthenticationProps = AuthenticationProps.DEFAULT
        var bundleRef: BundleRef = BundleRef.EMPTY
        var pgAdmin: PgAdminProps = PgAdminProps.DEFAULT
        var mongodb: MongoDbProps = MongoDbProps.DEFAULT
        var proxy: ProxyProps = ProxyProps.DEFAULT
        var alfresco: AlfrescoProps = AlfrescoProps.DEFAULT
        var webapps: Map<String, WebappProps> = mapOf()

        constructor(props: NamespaceDto) : this() {
            id = props.id
            name = props.name
            snapshot = props.snapshot
            template = props.template
            authentication = props.authentication
            bundleRef = props.bundleRef
            pgAdmin = props.pgAdmin
            mongodb = props.mongodb
            proxy = props.citeckProxy
            alfresco = props.alfresco
            webapps = props.webapps
        }

        fun withId(id: String): Builder {
            this.id = id
            return this
        }

        fun withName(name: String): Builder {
            this.name = name
            return this
        }

        fun withSnapshot(snapshot: String): Builder {
            this.snapshot = snapshot
            return this
        }

        fun withTemplate(template: String): Builder {
            this.template = template
            return this
        }

        fun withAuthentication(authentication: AuthenticationProps?): Builder {
            this.authentication = authentication ?: DEFAULT.authentication
            return this
        }

        fun withBundleRef(bundleRef: BundleRef?): Builder {
            this.bundleRef = bundleRef ?: DEFAULT.bundleRef
            return this
        }

        fun withMongodb(mongodb: MongoDbProps?): Builder {
            this.mongodb = mongodb ?: DEFAULT.mongodb
            return this
        }

        fun withProxy(proxy: ProxyProps?): Builder {
            this.proxy = proxy ?: DEFAULT.citeckProxy
            return this
        }

        fun withAlfresco(alfresco: AlfrescoProps?): Builder {
            this.alfresco = alfresco ?: DEFAULT.alfresco
            return this
        }

        fun withWebapps(webapps: Map<String, WebappProps>?): Builder {
            this.webapps = webapps ?: DEFAULT.webapps
            return this
        }

        fun build(): NamespaceDto {
            return NamespaceDto(
                id = id,
                name = name,
                snapshot = snapshot,
                template = template,
                authentication = authentication,
                bundleRef = bundleRef,
                pgAdmin = pgAdmin,
                mongodb = mongodb,
                citeckProxy = proxy,
                alfresco = alfresco,
                webapps = webapps
            )
        }
    }

    class MongoDbProps(
        val image: String = ""
    ) {
        companion object {
            val DEFAULT = MongoDbProps()
        }
    }

    class PgAdminProps(
        val enabled: Boolean = true,
        val image: String = ""
    ) {
        companion object {
            val DEFAULT = PgAdminProps()
        }
    }

    class WebappProps(
        val enabled: Boolean? = null,
        val image: String = "",
        val cloudConfig: DataValue = DataValue.createObj(),
        val environments: Map<String, String> = emptyMap(),
        val debugPort: Int = -1,
        val heapSize: String = "",
        val memoryLimit: String = "",
        val serverPort: Int = 0,
        val javaOpts: String = "",
        val dataSources: Map<String, DataValue> = emptyMap(),
        val springProfiles: String = ""
    ) {
        companion object {
            val DEFAULT = WebappProps()
        }

        fun apply(other: WebappProps?): WebappProps {
            other ?: return this
            val otherData = Json.toNonDefaultJsonObj(other)
            if (otherData.isEmpty) {
                return this
            }
            val newData = DataValue.createAsIs(Json.toJson(this))
            val fields = otherData.fieldNames()
            while (fields.hasNext()) {
                val field = fields.next()
                newData[field] = mergeValues(newData[field], DataValue.createAsIs(otherData[field]))
            }
            return Json.convert(newData, WebappProps::class)
        }

        private fun mergeValues(value0: DataValue, value1: DataValue): DataValue {
            if (!value0.isObject()) {
                return value1
            }
            val newValue = value0.copy()
            val fieldNames = value1.fieldNames()
            while (fieldNames.hasNext()) {
                val nextField = fieldNames.next()
                newValue[nextField] = mergeValues(newValue[nextField], value1[nextField])
            }
            return newValue
        }
    }

    data class ProxyProps(
        val image: String = ""
    ) {
        companion object {
            val DEFAULT = ProxyProps()
        }
    }

    data class AlfrescoProps(
        val enabled: Boolean = false,
        val javaOpts: String = "",
        val heapSize: String = "",
        val memoryLimit: String = ""
    ) {
        companion object {
            val DEFAULT = AlfrescoProps()
        }
    }

    @JsonDeserialize(builder = AuthenticationProps.Builder::class)
    data class AuthenticationProps(
        val type: AuthenticationType = AuthenticationType.BASIC,
        val users: Set<String> = setOf("admin", "fet")
    ) {
        companion object {
            val DEFAULT = AuthenticationProps()
        }

        class Builder {

            var type: AuthenticationType = AuthenticationType.BASIC
            var users: Set<String> = setOf("admin", "fet")

            fun withType(type: AuthenticationType): Builder {
                this.type = type
                return this
            }

            fun withUsers(users: DataValue): Builder {
                if (users.isTextual()) {
                    this.users = users.asText().split(",").map { it.split(":")[0].trim() }.filter { it.isNotBlank() }.toSet()
                } else {
                    this.users = users.toStrSet()
                }
                return this
            }

            fun build(): AuthenticationProps {
                return AuthenticationProps(
                    type, users
                )
            }
        }
    }

    enum class AuthenticationType {
        BASIC, KEYCLOAK
    }

    data class JdbcDataSource(
        val url: String
    )
}
