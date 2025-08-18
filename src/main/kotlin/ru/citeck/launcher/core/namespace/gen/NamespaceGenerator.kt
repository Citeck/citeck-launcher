package ru.citeck.launcher.core.namespace.gen

import io.github.oshai.kotlinlogging.KotlinLogging
import ru.citeck.launcher.core.WorkspaceServices
import ru.citeck.launcher.core.appdef.*
import ru.citeck.launcher.core.config.bundle.BundleDef
import ru.citeck.launcher.core.git.GitUpdatePolicy
import ru.citeck.launcher.core.namespace.AppName
import ru.citeck.launcher.core.namespace.NamespaceConfig
import ru.citeck.launcher.core.namespace.NamespaceConfig.AuthenticationType
import ru.citeck.launcher.core.namespace.init.ExecShell
import ru.citeck.launcher.core.namespace.runtime.docker.DockerApi
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

        private val log = KotlinLogging.logger {}
    }

    private lateinit var services: WorkspaceServices

    private val defaultAppFiles by lazy {
        CiteckFiles.getFile("classpath:appfiles").getFilesContent()
    }

    fun init(services: WorkspaceServices) {
        this.services = services
    }

    fun generate(
        props: NamespaceConfig,
        updatePolicy: GitUpdatePolicy,
        bundleDef: BundleDef,
        detachedApps: Set<String>
    ): NamespaceGenResp {

        services.updateConfig(updatePolicy)

        val context = NsGenContext(
            props,
            bundleDef,
            services.workspaceConfig.getValue(),
            appFiles = HashMap(defaultAppFiles),
            detachedApps = detachedApps,
        )

        generateMailhog(context)
        generateMongoDb(context)
        generatePgAdmin(context)
        generatePostgres(context)
        generateZookeeper(context)
        generateRabbitMq(context)
        generateKeycloak(context)
        generateAlfresco(context)

        for (app in context.bundle.applications) {
            if (!context.workspaceConfig.webappsById.contains(app.key)) {
                continue
            }
            generateWebapp(app.key, context)
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
            context.links,
            setOf(AppName.ONLYOFFICE)
        )
    }

    private fun generateKeycloak(context: NsGenContext) {

        val dbName = "citeck_keycloak"

        // add init script to db even without enabled keycloak to avoid db restarting when keycloak will be enabled
        context.applications[NsGenContext.PG_HOST]!!
            .addInitAction(ExecShell("/init_db_and_user.sh $dbName"))

        if (context.namespaceConfig.authentication.type != AuthenticationType.KEYCLOAK) {
            return
        }

        context.links.add(
            NamespaceLink(
                "http://localhost/ecos-idp/auth/",
                "Keycloak Admin",
                "Keycloak is a tool for user authentication and authorization.\n Username: admin\n Password: admin",
                "icons/app/keycloak.svg",
                -10f
            )
        )

        context.getOrCreateApp(AppName.KEYCLOAK)
            .withImage("keycloak/keycloak:26.3.1")
            .withResources(
                AppResourcesDef(
                    AppResourcesDef.LimitsDef("1g")
                )
            )
            .withKind(ApplicationKind.THIRD_PARTY)
            .addEnv("KC_BOOTSTRAP_ADMIN_USERNAME", "admin")
            .addEnv("KC_BOOTSTRAP_ADMIN_PASSWORD", "admin")
            .addEnv("KC_HOSTNAME_STRICT_HTTPS", "false")
            .addDependsOn(NsGenContext.PG_HOST)
            .addVolume("./keycloak/ecos-app-realm.json:/opt/keycloak/data/import/ecos-app-realm.json")
            .addVolume("./keycloak/healthcheck.sh:/healthcheck.sh")
            .withCmd(
                listOf(
                    "start",
                    "--hostname=http://localhost/ecos-idp/auth/",
                    "--http-enabled=true",
                    "--health-enabled=true",
                    "--db=postgres",
                    "--hostname-backchannel-dynamic=true",
                    "--db-url=jdbc:postgresql://${NsGenContext.PG_HOST}:${NsGenContext.PG_PORT}/$dbName",
                    "--db-username=$dbName",
                    "--db-password=$dbName",
                    "--proxy-headers=xforwarded",
                    "--import-realm"
                )
            )
            .withStartupCondition(
                StartupCondition(
                    probe = AppProbeDef(
                        exec = ExecProbeDef(
                            listOf("bash", "/healthcheck.sh")
                        )
                    )
                )
            )
    }

    private fun generateMailhog(context: NsGenContext) {
        context.getOrCreateApp(AppName.MAILHOG)
            .withImage("mailhog/mailhog:v1.0.1")
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
        val props = context.workspaceConfig.onlyoffice
        context.getOrCreateApp(AppName.ONLYOFFICE)
            .withImage(props.image)
            .addPort("8070:80/tcp")
            .addPort("443/tcp")
            .addEnv("JWT_ENABLED", "false")
            .addEnv("ALLOW_PRIVATE_IP_ADDRESS", "true")
            .withResources(
                AppResourcesDef(
                    AppResourcesDef.LimitsDef(props.memoryLimit)
                )
            )
    }

    private fun generatePgAdmin(context: NsGenContext) {

        val props = context.namespaceConfig.pgAdmin
        if (!props.enabled) {
            return
        }
        context.getOrCreateApp(AppName.PGADMIN)
            .withImage(props.image.ifBlank { "dpage/pgadmin4:8.13.0" })
            .addPort("5050:80")
            .addEnv("PGADMIN_DEFAULT_EMAIL", "admin@admin.com")
            .addEnv("PGADMIN_DEFAULT_PASSWORD", "admin")
            .addVolume("pgadmin2:/var/lib/pgadmin")
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
                "Postgres database management and design tool\n" +
                    "Username: admin@admin.com\n" +
                    "Password: admin\n" +
                    "Password for database: postgres",
                "icons/app/postgres.svg",
                0f
            )
        )
    }

    private fun generateMongoDb(context: NsGenContext) {
        context.getOrCreateApp(AppName.MONGODB)
            .withImage(context.namespaceConfig.mongodb.image.ifBlank { "mongo:4.0.2" })
            .addPort("27017:27017")
            .addVolume("mongo2:/data/db")
            .withResources(
                AppResourcesDef(
                    AppResourcesDef.LimitsDef("512m")
                )
            )
    }

    private fun generateProxyApp(context: NsGenContext) {

        val gatewayPort = context.applications[AppName.GATEWAY]!!.getEnv("SERVER_PORT")!!.toInt()

        val app = context.getOrCreateApp(AppName.PROXY)
        if (!context.detachedApps.contains(AppName.ONLYOFFICE)) {
            app.addEnv("ONLYOFFICE_TARGET", NsGenContext.ONLYOFFICE_HOST)
                .addDependsOn(AppName.ONLYOFFICE)
        }
        when (context.namespaceConfig.authentication.type) {
            AuthenticationType.BASIC -> {
                val users = context.namespaceConfig.authentication.users
                app.addEnv("BASIC_AUTH_ACCESS", users.joinToString(",") { "$it:$it" })
            }
            AuthenticationType.KEYCLOAK -> {
                app.addEnv("EIS_TARGET", "${NsGenContext.KK_HOST}:8080")
                app.addEnv("ENABLE_OIDC_FULL_ACCESS", "true")
                app.addEnv("CLIENT_ID", "ecos-proxy-app")
                app.addEnv("EIS_SCHEME", "http")
                app.addEnv("EIS_ID", "${NsGenContext.KK_HOST}:8080")
                app.addEnv("REALM_ID", "ecos-app")
                app.addEnv("EIS_LOCATION", "auth")
                app.addEnv("REDIRECT_LOGOUT_URI", "http://localhost")
                app.addEnv("CLIENT_SECRET", "2996117d-9a33-4e06-b48a-867ce6a235db")
                app.addVolume("./proxy/lua_oidc_full_access.lua:/etc/nginx/includes/lua_oidc_full_access.lua:ro")
                // app.addVolume("./proxy/openidc.lua:/usr/local/openresty/luajit/share/lua/5.1/resty/openidc.lua:ro")
                app.addInitAction(
                    ExecShell(
                        "sed -i -e '/location \\/ecos-idp\\/auth\\/ {/a\\\n" +
                            "    rewrite ^/ecos-idp/auth/(.*)\\$ /\\$1 break;\n' " +
                            "-e 's|http://keycloak:8080/auth/|http://keycloak:8080/|g' /etc/nginx/conf.d/default.conf "
                    )
                )
                app.addInitAction(ExecShell("nginx -s reload"))
            }
        }
        app.addEnv("RABBITMQ_TARGET", "${NsGenContext.RMQ_HOST}:15672")
        app.addEnv("ENABLE_LOGGING", "warn")
        app.addEnv("ENABLE_SERVER_STATUS", "true")
        app.addEnv("MAILHOG_TARGET", NsGenContext.MAILHOG_HOST + ":8025")
        app.addEnv("ECOS_PAGE_TITLE", "Citeck Launcher")

        val proxyProps = context.namespaceConfig.citeckProxy

        app.withImage(
            proxyProps.image.ifBlank {
                context.bundle.applications[AppName.PROXY]?.image ?: ""
            }
        )

        val alfEnabled = context.applications.containsKey(AppName.ALFRESCO) &&
            !context.detachedApps.contains(AppName.ALFRESCO)

        val proxyTarget = if (alfEnabled) {
            app.addDependsOn(AppName.ALFRESCO)
            AppName.ALFRESCO + ":8080"
        } else {
            AppName.GATEWAY + ":" + gatewayPort
        }

        app.addEnv("DEFAULT_LOCATION_V2", "true")
            .addEnv("GATEWAY_TARGET", "${AppName.GATEWAY}:$gatewayPort")
            .addEnv("PROXY_TARGET", proxyTarget)
            .addEnv("ECOS_INIT_DELAY", "0")
            .addEnv("ALFRESCO_ENABLED", alfEnabled.toString())
            .addPort("80:80")
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
    }

    private fun generateWebapp(name: String, context: NsGenContext) {

        val variables = DataValue.create(NsGenContext.VARS)
        variables["KK_ENABLED"] = context.namespaceConfig.authentication.type == AuthenticationType.KEYCLOAK

        val webappProps = TmplUtils.applyAtts(
            context.workspaceConfig.defaultWebappProps
                .apply(context.workspaceConfig.webappsById[name]?.defaultProps)
                .apply(context.namespaceConfig.webapps[name]),
            DataValue.create(variables)
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
            .withKind(kind)
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

        webappProps.environments.forEach { (k, v) ->
            try {
                app.addEnv(k, v)
            } catch (e: Throwable) {
                throw IllegalStateException("Invalid env param $k with value $v", e)
            }
        }

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

    private fun generateAlfresco(
        context: NsGenContext
    ) {
        if (!context.workspaceConfig.alfresco.enabled) {
            return
        }

        val alfrescoBundle = context.bundle.applications[AppName.ALFRESCO]
        if (alfrescoBundle == null) {
            log.debug {
                "Alfresco enabled, but bundle " +
                    "${context.namespaceConfig.bundleRef} doesn't contain alfresco image info"
            }
            return
        }

        context.links.add(
            NamespaceLink(
                "http://localhost/alfresco/s/admin/",
                "Alfresco Admin",
                "Alfresco Admin Console",
                "icons/app/alfresco.svg",
                100f
            )
        )

        val alfDbName = "alf-postgres"
        context.getOrCreateApp(alfDbName)
            .withImage("postgres:9.4")
            .addPort("54329:5432")
            .addEnv("POSTGRES_USER", "postgres")
            .addEnv("POSTGRES_PASSWORD", "postgres")
            .addEnv("PGDATA", "/var/lib/postgresql/data")
            .addVolume("alf_postgres:/var/lib/postgresql/data")
            .addVolume("./postgres/init_db_and_user.sh:/init_db_and_user.sh")
            .addInitAction(ExecShell("/init_db_and_user.sh alfresco"))
            .addInitAction(ExecShell("/init_db_and_user.sh alf_flowable"))
            .withStartupConditions(
                listOf(
                    StartupCondition(
                        log = LogStartupCondition(".*database system is ready to accept connections.*")
                    ),
                    StartupCondition(
                        probe = AppProbeDef(
                            exec = ExecProbeDef(
                                listOf(
                                    "/bin/sh",
                                    "-c",
                                    "psql -U postgres -d postgres -c 'SELECT 1' || exit 1"
                                )
                            )
                        )
                    )
                )
            )

        val alfApp = context.getOrCreateApp(AppName.ALFRESCO)

        alfApp.withImage(alfrescoBundle.image)
            .withKind(ApplicationKind.CITECK_ADDITIONAL)
            .addVolume("alf_content:/content")
            .addVolume("./alfresco/alfresco_additional.properties:/tmp/alfresco/alfresco_additional.properties")
            .addPort("${context.portsCounter.getAndIncrement()}:8080")
            .addDependsOn(alfDbName)
            .withStartupCondition(
                StartupCondition(
                    probe = AppProbeDef(
                        http = HttpProbeDef("/alfresco/s/citeck/ecos/eureka-status", 8080)
                    )
                )
            )

        alfApp.addEnv("ALFRESCO_USER_STORE_ADMIN_PASSWORD", "fefdbb615556a4b1dbb36e7935d77cf2")
            .addEnv("USE_EXTERNAL_AUTH", "true")
            .addEnv("SOLR_HOST", "alf-solr")
            .addEnv("SOLR_PORT", "8080")
            .addEnv("DB_HOST", alfDbName)
            .addEnv("DB_PORT", NsGenContext.PG_PORT.toString())
            .addEnv("DB_NAME", AppName.ALFRESCO)
            .addEnv("DB_USERNAME", AppName.ALFRESCO)
            .addEnv("DB_PASSWORD", AppName.ALFRESCO)
            .addEnv("ALFRESCO_HOSTNAME", AppName.ALFRESCO)
            .addEnv("ALFRESCO_PROTOCOL", "http")
            .addEnv("SHARE_HOSTNAME", AppName.ALFRESCO)
            .addEnv("SHARE_PROTOCOL", "http")
            .addEnv("SHARE_PORT", "80")
            .addEnv("ALFRESCO_PORT", "8080")
            .addEnv("FLOWABLE_URL", "http://localhost")
            .addEnv("MAIL_HOST", NsGenContext.MAILHOG_HOST)
            .addEnv("MAIL_PORT", "1025")
            .addEnv("MAIL_FROM_DEFAULT", "citeck@ecos24.ru")
            .addEnv("MAIL_PROTOCOL", "smtp")
            .addEnv("MAIL_SMTP_AUTH", "false")
            .addEnv("MAIL_SMTP_STARTTLS_ENABLE", "false")
            .addEnv("MAIL_SMTPS_AUTH", "false")
            .addEnv("MAIL_SMTPS_STARTTLS_ENABLE", "false")
            .addEnv("FLOWABLE_DB_HOST", alfDbName)
            .addEnv("FLOWABLE_DB_PORT", NsGenContext.PG_PORT.toString())
            .addEnv("FLOWABLE_DB_NAME", "alf_flowable")
            .addEnv("FLOWABLE_DB_USERNAME", "alf_flowable")
            .addEnv("FLOWABLE_DB_PASSWORD", "alf_flowable")
            .addEnv("JAVA_OPTS", "-Xms4G -Xmx4G -Duser.country=EN -Duser.language=en -Djava.security.egd=file:///dev/urandom -Djavamelody.authorized-users=admin:admin")

        context.links.add(
            NamespaceLink(
                "http://localhost:38080/solr4/#/",
                "Solr Admin",
                "Solr Admin Console",
                "icons/app/solr4.svg",
                120f
            )
        )

        context.getOrCreateApp("alf-solr")
            .withKind(ApplicationKind.CITECK_ADDITIONAL)
            .withImage("nexus.citeck.ru/ess:1.1.0")
            .addVolume("alf_solr_data:/opt/solr4_data")
            .addPort("38080:8080")
            .addEnv("TWEAK_SOLR", "true")
            .addEnv("JAVA_OPTS", "-Xms1G -Xmx1G")
            .addEnv("ALFRESCO_HOST", AppName.ALFRESCO)
            .addEnv("ALFRESCO_PORT", "8080")
            .addEnv("ALFRESCO_INDEX_TRANSFORM_CONTENT", "false")
            .addEnv("ALFRESCO_RECORD_UNINDEXED_NODES", "false")
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
            .withImage("rabbitmq:4.1.2-management")
            .addPort("5672:${NsGenContext.RMQ_PORT}")
            .addPort("15672:15672")
            .addEnv("RABBITMQ_DEFAULT_USER", "admin")
            .addEnv("RABBITMQ_DEFAULT_PASS", "admin")
            .addEnv("RABBITMQ_DEFAULT_VHOST", "/")
            .addEnv("RABBITMQ_MANAGEMENT_ALLOW_WEB_ACCESS", "true")
            .addVolume("rabbitmq2:/var/lib/rabbitmq")
            .withResources(
                AppResourcesDef(
                    AppResourcesDef.LimitsDef("256m")
                )
            )
        context.links.add(
            NamespaceLink(
                // ip instead of localhost to avoid 'headers too large' error
                "http://127.0.0.1:15672",
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
            .withImage("zookeeper:3.9.3")
            .addPort("2181:${NsGenContext.ZK_PORT}")
            .addPort("${context.portsCounter.andIncrement}:8080")
            .addEnv("ZOO_AUTOPURGE_PURGEINTERVAL", "1")
            .addEnv("ZOO_AUTOPURGE_SNAPRETAINCOUNT", "3")
            .addEnv("ALLOW_ANONYMOUS_LOGIN", "yes")
            .addEnv("ZOO_DATA_DIR", "/citeck/zookeeper/data")
            .addEnv("ZOO_DATA_LOG_DIR", "/citeck/zookeeper/datalog")
            .addVolume("zookeeper2:/citeck/zookeeper")
            .withResources(
                AppResourcesDef(
                    AppResourcesDef.LimitsDef("512m")
                )
            ).withInitContainers(
                listOf(
                    InitContainerDef.create()
                        .withImage(DockerApi.UTILS_IMAGE)
                        .withCmd(listOf("/bin/sh", "-c", "mkdir -p /zkdir/data /zkdir/datalog"))
                        .withVolumes(listOf("zookeeper2:/zkdir"))
                        .build()
                )
            )
    }

    private fun generatePostgres(context: NsGenContext) {
        context.getOrCreateApp(NsGenContext.PG_HOST)
            .withImage("postgres:17.5")
            .addEnv("POSTGRES_USER", "postgres")
            .addEnv("POSTGRES_PASSWORD", "postgres")
            .addEnv("PGDATA", "/var/lib/postgresql/data")
            .addPort("14523:${NsGenContext.PG_PORT}")
            .withShmSize("128m")
            .addVolume("postgres2:/var/lib/postgresql/data")
            .addVolume("./postgres/init_db_and_user.sh:/init_db_and_user.sh")
            .addVolume("./postgres/postgresql.conf:/etc/postgresql/postgresql.conf")
            .addVolume("./postgres/pg_hba.conf:/etc/postgresql/pg_hba.conf")
            .withStartupConditions(
                listOf(
                    StartupCondition(
                        log = LogStartupCondition(".*database system is ready to accept connections.*")
                    ),
                    StartupCondition(
                        probe = AppProbeDef(
                            exec = ExecProbeDef(
                                listOf(
                                    "/bin/sh",
                                    "-c",
                                    "psql -U postgres -d postgres -c 'SELECT 1' || exit 1"
                                )
                            )
                        )
                    )
                )
            ).withResources(
                AppResourcesDef(
                    AppResourcesDef.LimitsDef("1g")
                )
            ).withCmd(listOf("-c", "config_file=/etc/postgresql/postgresql.conf"))
    }

    private enum class DbType(val defaultPort: Int) {
        JDBC(5432),
        MONGODB(27017)
    }
}
