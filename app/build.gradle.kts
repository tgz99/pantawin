import java.util.Properties

plugins {
    alias(libs.plugins.android.application)
    // No org.jetbrains.kotlin.android plugin — AGP 9's built-in Kotlin
    // support covers app modules automatically. The Compose compiler
    // plugin is compatible with built-in Kotlin and still needs applying.
    alias(libs.plugins.kotlin.compose)
}

// M0-only bootstrap credentials (see app/secrets.properties.example) — the
// dashboard screen logs in with these since there's no login screen yet
// (M1 adds one). File is gitignored; falls back to empty strings so CI's
// assembleDebug still succeeds without secrets present.
val secretsFile = rootProject.file("app/secrets.properties")
val secrets = Properties().apply {
    if (secretsFile.exists()) {
        secretsFile.inputStream().use { load(it) }
    }
}

android {
    namespace = "com.pantawin.app"
    compileSdk {
        version = release(37)
    }

    defaultConfig {
        applicationId = "com.pantawin.app"
        minSdk = 30
        targetSdk = 37
        versionCode = 1
        versionName = "1.0"

        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"

        buildConfigField("String", "ADMIN_EMAIL", "\"${secrets.getProperty("ADMIN_EMAIL", "")}\"")
        buildConfigField("String", "ADMIN_PASSWORD", "\"${secrets.getProperty("ADMIN_PASSWORD", "")}\"")
        buildConfigField(
            "String",
            "API_BASE_URL",
            "\"${secrets.getProperty("API_BASE_URL", "https://api.pantawin.gratisaja.com")}\"",
        )
    }

    buildTypes {
        release {
            optimization {
                enable = false
            }
        }
    }
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_11
        targetCompatibility = JavaVersion.VERSION_11
    }
    buildFeatures {
        compose = true
        buildConfig = true
    }
}

dependencies {
    implementation(project(":shared"))

    implementation(libs.androidx.appcompat)
    implementation(libs.androidx.core.ktx)
    implementation(libs.material)

    implementation(platform(libs.compose.bom))
    implementation(libs.compose.ui)
    implementation(libs.compose.ui.tooling.preview)
    implementation(libs.compose.material3)
    implementation(libs.compose.material.icons.extended)
    implementation(libs.androidx.activity.compose)
    implementation(libs.androidx.lifecycle.viewmodel.compose)
    implementation(libs.androidx.lifecycle.runtime.compose)
    implementation(libs.androidx.datastore.preferences)
    implementation(libs.androidx.navigation.compose)
    debugImplementation(libs.compose.ui.tooling)

    testImplementation(libs.junit)
    testImplementation(kotlin("test"))
    testImplementation(libs.kotlinx.coroutines.test)
    testImplementation(libs.ktor.client.mock)
    testImplementation(libs.turbine)
    androidTestImplementation(libs.androidx.espresso.core)
    androidTestImplementation(libs.androidx.junit)
}
