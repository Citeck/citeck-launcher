import com.github.jengelman.gradle.plugins.shadow.tasks.ShadowJar
import groovy.json.JsonOutput
import org.jetbrains.compose.desktop.application.dsl.TargetFormat
import org.jetbrains.kotlin.gradle.dsl.JvmTarget
import java.time.Instant

plugins {
    kotlin("jvm")
    id("org.jetbrains.compose")
    id("org.jetbrains.kotlin.plugin.compose")
    id("org.jlleitschuh.gradle.ktlint")
    id("com.gradleup.shadow")
}

group = "ru.citeck.launcher"
version = "1.1.7"

repositories {
    mavenCentral()
    mavenLocal()
    maven("https://maven.pkg.jetbrains.space/public/p/compose/dev")
    google()
}
val targetOs = findProperty("targetOs") as String? ?: "current"

dependencies {

    when (targetOs) {
        "macos" -> {
            implementation(compose.desktop.macos_x64)
            implementation(compose.desktop.macos_arm64)
        }
        "macos_x64" -> implementation(compose.desktop.macos_x64)
        "macos_arm64" -> implementation(compose.desktop.macos_arm64)
        "linux" -> {
            implementation(compose.desktop.linux_x64)
            implementation(compose.desktop.linux_arm64)
        }
        "linux_x64" -> implementation(compose.desktop.linux_x64)
        "linux_arm64" -> implementation(compose.desktop.linux_arm64)
        "windows" -> {
            implementation(compose.desktop.windows_x64)
            implementation(compose.desktop.windows_arm64)
        }
        "windows_x64" -> implementation(compose.desktop.windows_x64)
        "windows_arm64" -> implementation(compose.desktop.windows_arm64)
        "current" -> implementation(compose.desktop.currentOs)
        else -> error("Unknown targetOs: $targetOs")
    }

    implementation(compose.components.resources)
    implementation(compose.materialIconsExtended)
    implementation(compose.material3)

    implementation("com.h2database:h2:2.3.232")

    val drJavaV = project.properties["docker-java.version"]
    implementation("com.github.docker-java:docker-java-core:$drJavaV")
    implementation("com.github.docker-java:docker-java-transport-httpclient5:$drJavaV")

    implementation("org.snakeyaml:snakeyaml-engine:2.9")
    implementation("com.fasterxml.jackson.core:jackson-databind:2.19.1")
    implementation("com.fasterxml.jackson.module:jackson-module-kotlin:2.19.1")
    implementation("org.apache.commons:commons-lang3:3.18.0")
    implementation("commons-codec:commons-codec:1.18.0")
    implementation("org.apache.xmlgraphics:batik-transcoder:1.19")
    implementation("org.apache.xmlgraphics:batik-codec:1.19")

    implementation("org.eclipse.jgit:org.eclipse.jgit:7.3.0.202506031305-r")
    implementation("ch.qos.logback:logback-classic:1.5.18")
    implementation("io.github.oshai:kotlin-logging-jvm:7.0.7")
    implementation("io.ktor:ktor-client-cio-jvm:3.2.1")

    val ktorV = project.properties["ktor.version"]
    implementation("io.ktor:ktor-server-core-jvm:$ktorV")
    implementation("io.ktor:ktor-server-cio:$ktorV")
    implementation("io.ktor:ktor-client-core:$ktorV")
    implementation("io.ktor:ktor-client-cio:$ktorV")

    testImplementation("org.assertj:assertj-core:3.27.3")
    testImplementation(kotlin("test"))
}

val generatedResources by lazy {
    val resourcesDir = layout.buildDirectory.file("generated/resources").get().asFile
    if (!resourcesDir.exists()) {
        resourcesDir.mkdirs()
    }
    resourcesDir
}
val generateBuildInfo by tasks.registering {
    val outputFile = generatedResources.resolve("build-info.json")
    outputs.file(outputFile)
    doLast {
        val buildInfo = mapOf(
            "version" to (project.version.toString()),
            "buildTime" to Instant.now().toString(),
            "javaVersion" to System.getProperty("java.version")
        )
        outputFile.writeText(JsonOutput.prettyPrint(JsonOutput.toJson(buildInfo)))
    }
}

sourceSets["main"].resources.srcDir(generatedResources)
tasks.named("processResources") {
    dependsOn(generateBuildInfo)
}

compose.desktop {
    application {
        mainClass = "ru.citeck.launcher.MainKt"

        nativeDistributions {
            modules("java.naming")
            targetFormats(
                TargetFormat.Dmg,
                TargetFormat.Msi,
                TargetFormat.Deb
            )
            jvmArgs("-Xmx200m")
            description = "Citeck Launcher"
            copyright = "© 2025 Citeck LLC. All Rights Reserved"
            packageName = "citeck-launcher"
            vendor = "Citeck LLC"
            packageVersion = project.version.toString()
            licenseFile.set(project.file("LICENSE"))
            linux {
                iconFile.set(project.file("icons/logo.png"))
                debMaintainer = "info@citeck.ru"
                appCategory = "Utility"
                shortcut = true
            }
            windows {
                iconFile.set(project.file("icons/logo.ico"))
                dirChooser = true
                perUserInstall = true
                menuGroup = "Citeck Tools"
                upgradeUuid = "3fa61060-0739-4463-985e-c58d1bc4e9b2"
            }
            macOS {
                appCategory = "public.app-category.utilities"
                iconFile.set(project.file("icons/icon.icns"))
                jvmArgs("-Dapple.awt.enableTemplateImages=true")
            }

            // ./gradlew suggestRuntimeModules
            modules(
                "java.compiler",
                "java.instrument",
                "java.management",
                "java.naming",
                "java.scripting",
                "java.security.jgss",
                "java.sql"
            )
        }
    }
}

configurations.all {
    resolutionStrategy {
        val kotlinV = project.properties["kotlin.version"]
        force("org.jetbrains.kotlin:kotlin-reflect:$kotlinV")
        force("org.jetbrains.kotlin:kotlin-stdlib:$kotlinV")
    }
}

java {
    sourceCompatibility = JavaVersion.VERSION_21
    targetCompatibility = JavaVersion.VERSION_21
}

kotlin {
    compilerOptions {
        jvmTarget = JvmTarget.JVM_21
    }
}

tasks.jar {
    dependsOn(tasks.named("addKtlintFormatGitPreCommitHook"))
}

tasks.named<ShadowJar>("shadowJar") {
    dependsOn(configurations)
    mergeServiceFiles()
    isReproducibleFileOrder = true
    archiveClassifier = null as String?
    archiveVersion = project.version.toString()
    archiveBaseName = project.name + "-" + targetOs

    layout.buildDirectory.file("compose/jars").get().asFile.also { destination ->
        if (!destination.exists()) destination.parentFile?.mkdirs()
        destinationDirectory = destination
    }
    manifest {
        attributes("Main-Class" to "ru.citeck.launcher.MainKt")
    }
}
