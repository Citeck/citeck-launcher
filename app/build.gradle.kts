import com.github.jengelman.gradle.plugins.shadow.tasks.ShadowJar
import org.jetbrains.compose.desktop.application.dsl.TargetFormat
import java.nio.file.Files
import java.nio.file.StandardCopyOption
import java.time.Instant

plugins {
    kotlin("jvm")
    id("org.jetbrains.compose")
    id("org.jetbrains.kotlin.plugin.compose")
    id("org.jlleitschuh.gradle.ktlint")
    id("com.gradleup.shadow")
}

group = "ru.citeck.launcher"
version = rootProject.version
val distPackageName = "citeck-launcher"

repositories {
    mavenCentral()
    mavenLocal()
    maven("https://maven.pkg.jetbrains.space/public/p/compose/dev")
    google()
}

val targetOs = findProperty("targetOs") as String? ?: "current"

dependencies {

    implementation(project(":core"))

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

    implementation("org.apache.xmlgraphics:batik-transcoder:1.19")
    implementation("org.apache.xmlgraphics:batik-codec:1.19")
    implementation("com.fifesoft:rsyntaxtextarea:3.6.1")

    testImplementation("org.assertj:assertj-core:3.27.7")
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
            "version" to project.version.toString(),
            "buildTime" to Instant.now().toString(),
            "javaVersion" to System.getProperty("java.version")
        )
        outputFile.writeText(
            groovy.json.JsonOutput.prettyPrint(groovy.json.JsonOutput.toJson(buildInfo))
        )
    }
}

sourceSets["main"].resources.srcDir(generatedResources)
tasks.named("processResources") { dependsOn(generateBuildInfo) }

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
            packageName = distPackageName
            vendor = "Citeck LLC"
            packageVersion = project.version.toString()
            licenseFile.set(rootProject.file("LICENSE"))
            linux {
                iconFile.set(rootProject.file("icons/logo.png"))
                debMaintainer = "info@citeck.ru"
                appCategory = "Utility"
                shortcut = true
            }
            windows {
                iconFile.set(rootProject.file("icons/logo.ico"))
                dirChooser = true
                perUserInstall = true
                menuGroup = "Citeck Tools"
                upgradeUuid = "3fa61060-0739-4463-985e-c58d1bc4e9b2"
            }
            macOS {
                appCategory = "public.app-category.utilities"
                iconFile.set(rootProject.file("icons/icon.icns"))
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
    sourceCompatibility = JavaVersion.VERSION_25
    targetCompatibility = JavaVersion.VERSION_25
}

kotlin {
    compilerOptions {
        jvmTarget = org.jetbrains.kotlin.gradle.dsl.JvmTarget.JVM_25
    }
}

tasks.named<ShadowJar>("shadowJar") {
    dependsOn(configurations)

    mergeServiceFiles()
    isReproducibleFileOrder = true

    archiveClassifier = null as String?
    archiveVersion = project.version.toString()
    archiveBaseName = "citeck-launcher-" + targetOs

    layout.buildDirectory.file("compose/jars").get().asFile.also { destination ->
        if (!destination.exists()) destination.parentFile?.mkdirs()
        destinationDirectory = destination
    }
    manifest {
        attributes("Main-Class" to "ru.citeck.launcher.MainKt")
    }
}

tasks.register("packageDist") {
    val outDir = when {
        targetOs.contains("macos") -> "dmg"
        targetOs.contains("windows") -> "msi"
        targetOs.contains("linux") -> "deb"
        else -> error("Unsupported target OS: $targetOs")
    }
    dependsOn("packageDistributionForCurrentOS")
    doLast {
        val buildDir = layout.buildDirectory

        val packageFile = buildDir.file("compose/binaries/main/$outDir/").get().asFile.listFiles()?.find {
            it.name.contains(project.version.toString())
        }
        if (packageFile == null) {
            error("Package file not found in dir: $outDir")
        }
        val sourceFile = packageFile.toPath()
        val extension = packageFile.name.substringAfterLast(".")
        val targetFile = sourceFile.parent.resolve(
            distPackageName + "_" + project.version + "_" + targetOs + "." + extension
        )

        Files.move(packageFile.toPath(), targetFile, StandardCopyOption.REPLACE_EXISTING)
    }
}
