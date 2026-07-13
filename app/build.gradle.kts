import java.util.Properties

plugins {
    alias(libs.plugins.android.application)
    // No org.jetbrains.kotlin.android plugin — AGP 9's built-in Kotlin
    // support covers app modules automatically. The Compose compiler
    // plugin is compatible with built-in Kotlin and still needs applying.
    alias(libs.plugins.kotlin.compose)
}

// google-services.json is gitignored; apply the plugin only when it exists so
// the app still builds without a Firebase project (push simply stays dormant,
// see PantawinApp). With the file present the plugin auto-initializes the
// default FirebaseApp — no FIREBASE_* entries in secrets.properties needed.
if (file("google-services.json").exists()) {
    apply(plugin = "com.google.gms.google-services")
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
    compileSdk = 37

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
        // Firebase / FCM config (M3). Empty = push dormant; the app runs and
        // WebSocket realtime works without it. Fill these from your Firebase
        // project's google-services.json values to activate push.
        buildConfigField("String", "FIREBASE_PROJECT_ID", "\"${secrets.getProperty("FIREBASE_PROJECT_ID", "")}\"")
        buildConfigField("String", "FIREBASE_APP_ID", "\"${secrets.getProperty("FIREBASE_APP_ID", "")}\"")
        buildConfigField("String", "FIREBASE_API_KEY", "\"${secrets.getProperty("FIREBASE_API_KEY", "")}\"")
        buildConfigField("String", "FIREBASE_SENDER_ID", "\"${secrets.getProperty("FIREBASE_SENDER_ID", "")}\"")
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
    implementation(platform(libs.firebase.bom))
    implementation(libs.firebase.messaging)
    // Coil loads monitor favicons (Google s2 favicon service) in the dashboard.
    implementation(libs.coil.compose)
    implementation(libs.coil.network.okhttp)
    // Google sign-in via Credential Manager (dormant without a web client id
    // in google-services.json — the button hides itself).
    implementation(libs.androidx.credentials)
    implementation(libs.androidx.credentials.play.services)
    implementation(libs.googleid)
    debugImplementation(libs.compose.ui.tooling)

    testImplementation(libs.junit)
    testImplementation(kotlin("test"))
    testImplementation(libs.kotlinx.coroutines.test)
    testImplementation(libs.ktor.client.mock)
    testImplementation(libs.turbine)
    androidTestImplementation(libs.androidx.espresso.core)
    androidTestImplementation(libs.androidx.junit)
}
