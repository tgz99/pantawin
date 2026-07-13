package com.pantawin.app

import android.app.Application
import android.util.Log
import com.google.firebase.FirebaseApp
import com.google.firebase.FirebaseOptions
import com.google.firebase.messaging.FirebaseMessaging
import com.pantawin.app.data.SessionManager
import com.pantawin.app.push.Notifications
import com.pantawin.shared.api.PantawinApiClient
import com.pantawin.shared.api.PantawinRealtimeClient
import com.pantawin.shared.api.createPantawinHttpClient
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.launch

/**
 * Application-scoped manual DI container (M1/M3). The SessionManager, API
 * clients, and (when configured) Firebase live for the process lifetime.
 * Hilt is a later refactor.
 */
class PantawinApp : Application() {

    private val httpClient by lazy { createPantawinHttpClient() }

    val sessionManager: SessionManager by lazy {
        SessionManager(
            context = this,
            api = PantawinApiClient(httpClient, BuildConfig.API_BASE_URL),
        )
    }

    val realtimeClient: PantawinRealtimeClient by lazy {
        PantawinRealtimeClient(httpClient, BuildConfig.API_BASE_URL)
    }

    // True when a FirebaseApp exists — either auto-initialized by the
    // google-services plugin (google-services.json present) or built
    // programmatically from BuildConfig below. Mirrors the server's
    // dormant FcmChannel: no Firebase project, no push, app still works.
    val pushEnabled: Boolean
        get() = FirebaseApp.getApps(this).isNotEmpty()

    private val hasBuildConfigFirebase: Boolean
        get() = BuildConfig.FIREBASE_PROJECT_ID.isNotBlank() &&
            BuildConfig.FIREBASE_APP_ID.isNotBlank() &&
            BuildConfig.FIREBASE_API_KEY.isNotBlank() &&
            BuildConfig.FIREBASE_SENDER_ID.isNotBlank()

    override fun onCreate() {
        super.onCreate()
        Notifications.ensureChannels(this)
        initFirebaseIfConfigured()
    }

    // Fallback path: initialize Firebase programmatically from BuildConfig
    // (app/secrets.properties) when google-services.json isn't present but
    // the FIREBASE_* secrets are.
    private fun initFirebaseIfConfigured() {
        if (FirebaseApp.getApps(this).isNotEmpty()) {
            Log.i(TAG, "FCM push enabled (google-services.json)")
            return
        }
        if (!hasBuildConfigFirebase) {
            Log.i(TAG, "FCM push dormant (Firebase not configured)")
            return
        }
        val options = FirebaseOptions.Builder()
            .setProjectId(BuildConfig.FIREBASE_PROJECT_ID)
            .setApplicationId(BuildConfig.FIREBASE_APP_ID)
            .setApiKey(BuildConfig.FIREBASE_API_KEY)
            .setGcmSenderId(BuildConfig.FIREBASE_SENDER_ID)
            .build()
        FirebaseApp.initializeApp(this, options)
        Log.i(TAG, "FCM push enabled (project ${BuildConfig.FIREBASE_PROJECT_ID})")
    }

    /** Fetch the current FCM token and register it. Called after login. */
    fun registerPushTokenIfEnabled() {
        if (!pushEnabled) return
        FirebaseMessaging.getInstance().token.addOnSuccessListener { token ->
            appScope.launch { runCatching { sessionManager.registerPushToken(token) } }
        }
    }

    private val appScope = CoroutineScope(SupervisorJob() + Dispatchers.IO)

    private companion object {
        const val TAG = "PantawinApp"
    }
}
