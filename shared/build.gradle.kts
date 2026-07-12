plugins {
    alias(libs.plugins.kotlin.multiplatform)
    alias(libs.plugins.kotlin.serialization)
    // AGP 9's built-in Kotlin does not support KMP modules — these still
    // need com.android.kotlin.multiplatform.library, which replaces the
    // classic com.android.library plugin (the two can't coexist under
    // built-in Kotlin) and moves Android config into kotlin.androidLibrary{}
    // below instead of a standalone android{} block.
    alias(libs.plugins.android.kotlin.multiplatform.library)
}

kotlin {
    androidLibrary {
        namespace = "com.pantawin.shared"
        compileSdk = 37
        minSdk = 30
    }
    jvm() // fast commonTest execution without an Android device/emulator

    sourceSets {
        commonMain.dependencies {
            // api, not implementation: PantawinApiClient's public constructor
            // takes an HttpClient, so consumers like :app need that type
            // visible on their own compile classpath.
            api(libs.ktor.client.core)
            implementation(libs.ktor.client.content.negotiation)
            implementation(libs.ktor.serialization.kotlinx.json)
            implementation(libs.kotlinx.serialization.json)
            implementation(libs.kotlinx.coroutines.core)
        }
        commonTest.dependencies {
            implementation(kotlin("test"))
            implementation(libs.ktor.client.mock)
            implementation(libs.kotlinx.coroutines.test)
        }
        androidMain.dependencies {
            implementation(libs.ktor.client.okhttp)
        }
        val jvmMain by getting {
            dependencies {
                implementation(libs.ktor.client.okhttp)
            }
        }
    }
}
