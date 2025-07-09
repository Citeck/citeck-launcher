package ru.citeck.launcher.core.namespace.gen

import ru.citeck.launcher.core.WorkspaceServices
import ru.citeck.launcher.core.appdef.*
import ru.citeck.launcher.core.git.GitUpdatePolicy
import ru.citeck.launcher.core.namespace.AppName
import ru.citeck.launcher.core.namespace.NamespaceDto
import ru.citeck.launcher.core.namespace.init.ExecShell
import ru.citeck.launcher.core.utils.TmplUtils
import ru.citeck.launcher.core.utils.data.DataValue
import ru.citeck.launcher.core.utils.file.CiteckFiles
import ru.citeck.launcher.core.utils.json.Json
import ru.citeck.launcher.core.utils.json.Yaml

class NamespaceGenerator {

    companion object {
        private val CORE_APPS = setOf(
            AppName.PROXY,
            AppName.GATEWAY,
            AppName.PROCESS,
            AppName.MODEL,
            AppName.UISERV,
            AppName.EAPPS,
            AppName.HISTORY,
            AppName.NOTIFICATIONS,
            AppName.TRANSFORMATIONS
        )
        private val CORE_EXT_APPS = setOf(
            AppName.INTEGRATIONS,
            AppName.EDI,
            AppName.CONTENT
        )
    }

    private lateinit var services: WorkspaceServices

    private val defaultAppFiles by lazy {
        CiteckFiles.getFile("classpath:appfiles").getFilesContent()
    }

    fun init(services: WorkspaceServices) {
        this.services = services
    }

    fun generate(props: NamespaceDto, updatePolicy: GitUpdatePolicy): NamespaceGenResp {

        services.updateConfig(updatePolicy)

        val context = NsGenContext(
            props,
            services.bundlesService.getBundleByRef(props.bundleRef, updatePolicy),
            services.workspaceConfig.value,
            HashMap(defaultAppFiles)
        )

        generateMailhog(context)
        generateMongoDb(context)
        generatePgAdmin(context)
        generatePostgres(context)
        generateZookeeper(context)
        generateRabbitMq(context)

        for (app in context.bundle.applications) {
            if (!context.workspaceConfig.webappsById.contains(app.key)) {
                continue
            }
            if (app.key != AppName.PROXY) {
                generateWebapp(app.key, context)
            }
        }
        generateProxyApp(context)
        generateOnlyOffice(context)

        context.links.add(
            NamespaceLink(
                "http://localhost/gateway/eapps/admin/wallboard",
                "Spring Boot Admin",
                "Spring Boot Admin is used to manage and monitor Spring Boot applications",
                "icons/app/spring-boot-admin.svg",
                -1f
            )
        )

        context.links.sortBy { it.order }

        return NamespaceGenResp(
            context.applications.values.map {
                it.build()
            },
            context.appFiles,
            context.cloudConfig,
            context.links
        )
    }

    private fun generateAlfresco(context: NsGenContext) {

    }

    private fun generateMailhog(context: NsGenContext) {
        context.getOrCreateApp(AppName.MAILHOG)
            .withReplicas(1)
            .withImage("mailhog/mailhog")
            .withPorts(
                listOf(
                    "1025:1025",
                    "8025:8025/tcp"
                )
            ).withResources(
                AppResourcesDef(
                    AppResourcesDef.LimitsDef("128m")
                )
            )
        context.links.add(
            NamespaceLink(
                "http://localhost:8025",
                "MailHog",
                "MailHog is an email testing tool",
                "icons/app/mailhog.svg",
                1f
            )
        )
    }

    private fun generateOnlyOffice(context: NsGenContext) {
        val props = context.props.onlyOffice
        if (!props.enabled) {
            return
        }
        context.getOrCreateApp(AppName.ONLY_OFFICE)
            .withImage("onlyoffice/documentserver:7.1")
            .withScalable(false)
            .withReplicas(1)
            .addPort("8070:80/tcp")
            .addPort("443/tcp")
            .withResources(
                AppResourcesDef(
                    AppResourcesDef.LimitsDef("1g")
                )
            )
    }

    private fun generatePgAdmin(context: NsGenContext) {

        val props = context.props.pgAdmin
        if (!props.enabled) {
            return
        }
        context.getOrCreateApp(AppName.PGADMIN)
            .withImage(props.image.ifBlank { "dpage/pgadmin4:8.13.0" })
            .addPort("5050:80")
            .addEnv("PGADMIN_DEFAULT_EMAIL", "admin@admin.com")
            .addEnv("PGADMIN_DEFAULT_PASSWORD", "admin")
            .addVolume("pgadmin:/var/lib/pgadmin")
            .addVolume("./pgadmin/servers.json:/pgadmin4/servers.json")
            .withResources(
                AppResourcesDef(
                    AppResourcesDef.LimitsDef("256m")
                )
            )
        context.links.add(
            NamespaceLink(
                "http://localhost:5050",
                "PG Admin",
                "Postgres database management and design tool\nUser: admin@admin.com\nPassword: admin",
                "icons/app/postgres.svg",
                0f
            )
        )
    }

    private fun generateMongoDb(context: NsGenContext) {
        context.getOrCreateApp(AppName.MONGODB)
            .withImage(context.props.mongodb.image.ifBlank { "mongo:4.0.2" })
            .addPort("27017:27017")
            .addVolume("mongo:/data/db")
            .withResources(
                AppResourcesDef(
                    AppResourcesDef.LimitsDef("512m")
                )
            )
    }

    private fun generateProxyApp(context: NsGenContext) {

        val gatewayPort = context.applications[AppName.GATEWAY]!!.getEnv("SERVER_PORT")!!.toInt()

        val app = context.getOrCreateApp(AppName.PROXY)
        if (context.props.onlyOffice.enabled) {
            app.addEnv("ONLYOFFICE_TARGET", NsGenContext.ONLY_OFFICE_HOST)
                .addDependsOn(AppName.ONLY_OFFICE)
        }
        val proxyProps = context.props.citeckProxy

        app.withImage(proxyProps.image.ifBlank {
            context.bundle.applications[AppName.PROXY]?.image ?: ""
        })

        app.addEnv("DEFAULT_LOCATION_V2", "true")
            .addEnv("GATEWAY_TARGET", "${AppName.GATEWAY}:$gatewayPort")
            .addEnv("PROXY_TARGET", "${AppName.GATEWAY}:$gatewayPort")
            .addEnv("ECOS_INIT_DELAY", "0")
            .addPort("!80:80")
            .addDependsOn(AppName.GATEWAY)
            .withKind(ApplicationKind.CITECK_CORE)
            .withResources(
                AppResourcesDef(
                    AppResourcesDef.LimitsDef("128m")
                )
            ).withStartupCondition(
                StartupCondition(
                    probe = AppProbeDef(http = HttpProbeDef("/eis.json", 80))
                )
            )

        val basicAuth = context.props.authentication.users
        if (basicAuth.isNotBlank()) {
            app.addEnv("BASIC_AUTH_ACCESS", basicAuth)
        }
    }

    private fun generateWebapp(name: String, context: NsGenContext) {

        val webappProps = TmplUtils.applyAtts(
            context.workspaceConfig.defaultWebappProps
                .apply(context.workspaceConfig.webappsById[name]?.defaultProps)
                .apply(context.props.webapps[name]),
            DataValue.create(NsGenContext.VARS)
        )

        if (webappProps.enabled == false) {
            return
        }

        var javaOpts = ""

        if (webappProps.heapSize.isNotBlank()) {
            javaOpts = "-Xmx${webappProps.heapSize} -Xms${webappProps.heapSize}"
        }
        if (webappProps.javaOpts.isNotBlank()) {
            javaOpts += " " + webappProps.javaOpts
        }
        if (webappProps.debugPort > 0) {
            javaOpts += " -agentlib:jdwp=transport=dt_socket,server=y,suspend=n,address=*:" + webappProps.debugPort
        }

        var port = webappProps.serverPort
        if (port == 0) {
            port = context.portsCounter.getAndIncrement()
        }

        val app = context.getOrCreateApp(name)

        val webappCloudConfig = DataValue.createObj()
        val cloudConfig = DataValue.createObj()
        webappProps.dataSources.forEach { (key, config) ->
            webappCloudConfig["/ecos/webapp/dataSources/$key"] = processDataSource(context, app, key, config, false)
            cloudConfig["/ecos/webapp/dataSources/$key"] = processDataSource(context, app, key, config, true)
        }
        context.cloudConfig.put(name, cloudConfig)

        webappCloudConfig.mergeDataFrom(webappProps.cloudConfig)

        if (app.name == AppName.EAPPS && context.workspaceConfig.licenses.isNotEmpty()) {
            webappCloudConfig["ecos.webapp.license.instances"] =
                context.workspaceConfig.licenses.map { Json.toString(it) }
        }

        if (webappCloudConfig.isNotEmpty()) {
            context.appFiles["app/$name/props/application-launcher.yml"] =
                Yaml.toString(webappCloudConfig).toByteArray()
            app.addEnv("SPRING_CONFIG_ADDITIONALLOCATION", "/run/java.io/spring-props/")
            app.addVolume("./app/$name/props:/run/java.io/spring-props/")
        }
        val springProfiles = linkedSetOf("dev", "launcher")
        webappProps.springProfiles.split(",").forEach {
            if (it.isNotBlank()) {
                springProfiles.add(it.trim())
            }
        }
        val kind = if (CORE_APPS.contains(app.name)) {
            ApplicationKind.CITECK_CORE
        } else if (CORE_EXT_APPS.contains(app.name)) {
            ApplicationKind.CITECK_CORE_EXTENSION
        } else {
            ApplicationKind.CITECK_ADDITIONAL
        }

        app.withImage(webappProps.image.ifBlank { context.bundle.applications[name]?.image ?: "" })
            .withScalable(true)
            .withKind(kind)
            .withReplicas(1)
            .addEnv("SERVER_PORT", port.toString())
            .addEnv("SPRING_PROFILES_ACTIVE", springProfiles.joinToString())
            .addEnv("ECOS_WEBAPP_RABBITMQ_HOST", NsGenContext.RMQ_HOST)
            .addEnv("ECOS_WEBAPP_ZOOKEEPER_HOST", NsGenContext.ZK_HOST)
            .addEnv("ECOS_INIT_DELAY", "0")
            .addEnv("SPRING_CLOUD_CONFIG_ENABLED", "false")
            .addEnv("SPRING_CONFIG_IMPORT", "")
            .addEnv("ECOS_WEBAPP_WEB_AUTHENTICATORS_JWT_SECRET", NsGenContext.JWT_SECRET)
            .addPort("$port:$port")
            .addDependsOn(NsGenContext.ZK_HOST)
            .addDependsOn(NsGenContext.RMQ_HOST)
            .withStartupCondition(
                StartupCondition(
                    probe = AppProbeDef(http = HttpProbeDef("/management/health", port))
                )
            ).withResources(
                AppResourcesDef(
                    AppResourcesDef.LimitsDef(webappProps.memoryLimit.ifBlank { "1g" })
                )
            )

        if (javaOpts.isNotEmpty()) {
            app.addEnv("JAVA_OPTS", javaOpts)
        }

        webappProps.environments.forEach { (k, v) -> app.addEnv(k, v) }

        if (app.name == AppName.EAPPS) {
            app.withInitContainers(
                context.bundle.citeckApps.map {
                    InitContainerDef.create()
                        .withImage(it.image)
                        .addEnv("ECOS_APPS_TARGET_DIR", "/run/ecos-apps")
                        .addVolume("./app/$name/ecos-apps:/run/ecos-apps")
                        .withKind(ApplicationKind.CITECK_ADDITIONAL)
                        .build()
                }
            )
            app.addEnv("ECOS_WEBAPP_EAPPS_ADDITIONAL_ARTIFACTS_LOCATIONS", "/run/ecos-artifacts")
            app.addVolume("./app/$name/ecos-apps:/run/ecos-artifacts/app/ecosapp")
        }
    }

    private fun processDataSource(
        context: NsGenContext,
        app: ApplicationDef.Builder,
        dataSourceKey: String,
        dataSourceConfig: DataValue,
        cloudConfig: Boolean
    ): DataValue {

        val url = dataSourceConfig["url"].asText()
        val dbType = if (url.startsWith("jdbc:")) {
            if (!cloudConfig) {
                app.addDependsOn(NsGenContext.PG_HOST)
            }
            DbType.JDBC
        } else if (url.startsWith("mongodb:")) {
            if (!cloudConfig) {
                app.addDependsOn(NsGenContext.MONGO_HOST)
            }
            DbType.MONGODB
        } else {
            return dataSourceConfig
        }

        val hostWithDb = url.substringAfter("://")

        var host = hostWithDb.substringBefore('/')
        var port = dbType.defaultPort
        if (host.contains(":")) {
            port = host.substringAfter(":").toInt()
            host = host.substringBefore(":")
        }
        if (cloudConfig) {
            host = "localhost"
            port = when (dbType) {
                DbType.JDBC -> 14523
                DbType.MONGODB -> 27017
            }
        }

        val dbName = hostWithDb.substringAfter('/')

        if (!cloudConfig && dbType == DbType.JDBC) {
            context.applications[NsGenContext.PG_HOST]!!
                .addInitAction(ExecShell("/init_db_and_user.sh $dbName"))
        }
        val newConfig = dataSourceConfig.copy()
        if (dbType == DbType.JDBC) {
            newConfig["username"] = dbName
            newConfig["password"] = dbName
        }
        val dsUrl = url.substringBefore("://") + "://" + host + ":" + port + "/$dbName"
        newConfig["url"] = dsUrl

        if (!cloudConfig) {
            if (dataSourceKey == "main" && dbType == DbType.JDBC) {
                app.addEnv("SPRING_DATASOURCE_USERNAME", dbName)
                    .addEnv("SPRING_DATASOURCE_PASSWORD", dbName)
                    .addEnv("SPRING_DATASOURCE_URL", dsUrl)
            } else if (dbType == DbType.MONGODB) {
                app.addEnv("SPRING_DATA_MONGODB_URI", dsUrl)
            }
        }

        return newConfig
    }

    private fun generateRabbitMq(context: NsGenContext) {
        context.getOrCreateApp(NsGenContext.RMQ_HOST)
            .withImage("bitnami/rabbitmq:4.0.3-debian-12-r1")
            .withScalable(false)
            .withReplicas(1)
            .addPort("5672:${NsGenContext.RMQ_PORT}")
            .addPort("15672:15672")
            .addEnv("RABBITMQ_USERNAME", "admin")
            .addEnv("RABBITMQ_PASSWORD", "admin")
            .addEnv("RABBITMQ_VHOST", "/")
            .addEnv("RABBITMQ_MANAGEMENT_ALLOW_WEB_ACCESS", "true")
            .addVolume("rabbitmq:/bitnami/rabbitmq/mnesia")
            .withResources(
                AppResourcesDef(
                    AppResourcesDef.LimitsDef("256m")
                )
            )
        context.links.add(
            NamespaceLink(
                "http://localhost:15672",
                "Rabbit MQ",
                "Rabbit MQ is a message broker — it helps different parts\n" +
                    "of a system communicate by sending and receiving messages between them\n" +
                    "Username: admin\nPassword: admin",
                "icons/app/rabbitmq.svg",
                2f
            )
        )
    }

    private fun generateZookeeper(context: NsGenContext) {
        context.getOrCreateApp(NsGenContext.ZK_HOST)
            .withImage("bitnami/zookeeper:3.9.3-debian-12-r3")
            .withScalable(false)
            .withReplicas(1)
            .addPort("2181:${NsGenContext.ZK_PORT}")
            .addEnv("ZOO_AUTOPURGE_INTERVAL", "1")
            .addEnv("ALLOW_ANONYMOUS_LOGIN", "yes")
            .addVolume("zookeeper:/bitnami/zookeeper")
            .withResources(
                AppResourcesDef(
                    AppResourcesDef.LimitsDef("512m")
                )
            )
    }

    private fun generatePostgres(context: NsGenContext) {
        context.getOrCreateApp(NsGenContext.PG_HOST)
            .withImage("bitnami/postgresql:13.17.0")
            .withScalable(false)
            .withReplicas(1)
            .addEnv("POSTGRESQL_USERNAME", "postgres")
            .addEnv("POSTGRESQL_PASSWORD", "postgres")
            .addPort("14523:${NsGenContext.PG_PORT}")
            .addVolume("postgres:/bitnami/postgresql")
            .addVolume("./postgres/init_db_and_user.sh:/init_db_and_user.sh")
            .addVolume("./postgres/postgresql.conf:/opt/bitnami/postgresql/conf/postgresql.conf")
            .withStartupCondition(
                StartupCondition(
                    log = LogStartupCondition(".*database system is ready to accept connections.*")
                )
            ).withResources(
                AppResourcesDef(
                    AppResourcesDef.LimitsDef("1g")
                )
            )
    }

    private enum class DbType(val defaultPort: Int) {
        JDBC(5432),
        MONGODB(27017)
    }
}
