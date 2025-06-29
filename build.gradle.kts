import org.jetbrains.compose.desktop.application.dsl.TargetFormat
import org.jetbrains.kotlin.gradle.dsl.JvmTarget

plugins {
    kotlin("jvm")
    id("org.jetbrains.compose")
    id("org.jetbrains.kotlin.plugin.compose")
}

group = "ru.citeck.launcher"
version = "1.0.1"

repositories {
    mavenCentral()
    mavenLocal()
    maven("https://maven.pkg.jetbrains.space/public/p/compose/dev")
    google()
}

dependencies {

    implementation(compose.desktop.currentOs)
    implementation(compose.components.resources)
    implementation(compose.materialIconsExtended)
    implementation(compose.material3)

    implementation("com.h2database:h2:2.3.232")

    implementation("com.github.docker-java:docker-java:${project.properties["docker-java.version"]}")
    implementation("com.github.docker-java:docker-java-transport-httpclient5:${project.properties["docker-java.version"]}")

    implementation("org.snakeyaml:snakeyaml-engine:2.9")
    implementation("com.fasterxml.jackson.core:jackson-databind:2.19.1")
    implementation("com.fasterxml.jackson.module:jackson-module-kotlin:2.19.1")
    implementation("org.apache.commons:commons-lang3:3.17.0")
    implementation("commons-codec:commons-codec:1.18.0")
    implementation("org.apache.xmlgraphics:batik-transcoder:1.19")
    implementation("org.apache.xmlgraphics:batik-codec:1.19")

    implementation("org.eclipse.jgit:org.eclipse.jgit:7.3.0.202506031305-r")
    implementation("ch.qos.logback:logback-classic:1.5.18")
    implementation("io.github.oshai:kotlin-logging-jvm:7.0.7")

    implementation("io.ktor:ktor-server-core-jvm:3.2.0")
    implementation("io.ktor:ktor-server-cio:3.2.0")

    testImplementation("org.junit.jupiter:junit-jupiter-api:5.13.1")
    testImplementation("org.junit.jupiter:junit-jupiter-engine:5.13.1")
}

tasks.withType(Test::class.java) {
    useJUnitPlatform()
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
            jvmArgs("-Xmx128m")
            description = "Citeck Launcher"
            copyright = "© 2025 Citeck LLC. All Rights Reserved"
            packageName = "citeck-launcher"
            vendor = "Citeck LLC"
            packageVersion = project.version.toString()
            licenseFile.set(project.file("LICENSE"))
            linux {
                iconFile.set(project.file("icons/logo.png"))
                debMaintainer = "info@citeck.ru"
                appCategory = "Development;Utility"
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
                //iconFile.set(project.file("icons/logo.icns")) //todo
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

java {
    sourceCompatibility = JavaVersion.VERSION_21
    targetCompatibility = JavaVersion.VERSION_21
}

kotlin {
    compilerOptions {
        jvmTarget = JvmTarget.JVM_21
    }
}

//zip -d file.jar 'META-INF/*.SF' 'META-INF/*.RSA' 'META-INF/*.DSA'

