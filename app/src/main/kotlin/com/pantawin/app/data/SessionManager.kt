package com.pantawin.app.data

import android.content.Context
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.stringPreferencesKey
import androidx.datastore.preferences.preferencesDataStore
import com.pantawin.shared.api.PantawinApiClient
import com.pantawin.shared.api.PantawinApiException
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.map

private val Context.dataStore by preferencesDataStore(name = "pantawin_session")

/**
 * Holds the authenticated session: access + refresh tokens persisted in
 * DataStore. [authed] wraps an API call so a 401 transparently refreshes the
 * access token once and retries (spec 7.2 item 7: expired access -> silent
 * refresh -> retry; refresh failure -> logout).
 *
 * Manual DI at M1 (constructed in PantawinApp) — Hilt is a later refactor.
 */
class SessionManager(
    private val context: Context,
    val api: PantawinApiClient,
) {
    private val accessKey = stringPreferencesKey("access_token")
    private val refreshKey = stringPreferencesKey("refresh_token")

    val isLoggedIn = context.dataStore.data.map { it[accessKey] != null }

    suspend fun login(email: String, password: String) {
        val tokens = api.login(email, password)
        save(tokens.accessToken, tokens.refreshToken)
    }

    /** Exchange a Google ID token for a session (server verifies it). */
    suspend fun loginWithGoogle(idToken: String) {
        val tokens = api.loginWithGoogle(idToken)
        save(tokens.accessToken, tokens.refreshToken)
    }

    suspend fun logout() {
        context.dataStore.edit { it.clear() }
    }

    private suspend fun save(access: String, refresh: String) {
        context.dataStore.edit {
            it[accessKey] = access
            it[refreshKey] = refresh
        }
    }

    private suspend fun accessToken(): String? = context.dataStore.data.first()[accessKey]
    private suspend fun refreshToken(): String? = context.dataStore.data.first()[refreshKey]

    /** Current access token, for opening the realtime WebSocket. */
    suspend fun currentAccessToken(): String? = accessToken()

    /** Thrown after a refresh attempt fails — callers route the user to login. */
    class SessionExpired : Exception()

    /** Change the account password. The server revokes every other session's
     * refresh token and returns a fresh pair for this one, which we persist —
     * this device stays signed in, all others must log in again. */
    suspend fun changePassword(currentPassword: String, newPassword: String) {
        val tokens = authed { token -> api.changePassword(token, currentPassword, newPassword) }
        save(tokens.accessToken, tokens.refreshToken)
    }

    /** Register an FCM device token with the backend (POST /devices). No-op
     * if not logged in — the token is re-sent on next login via onNewToken. */
    suspend fun registerPushToken(fcmToken: String) {
        if (accessToken() == null) return
        authed { token -> api.registerDevice(token, fcmToken) }
    }

    suspend fun <T> authed(call: suspend (accessToken: String) -> T): T {
        val token = accessToken() ?: throw SessionExpired()
        return try {
            call(token)
        } catch (e: PantawinApiException) {
            if (e.status != 401) throw e
            // Silent refresh, then retry once.
            val refresh = refreshToken() ?: run { logout(); throw SessionExpired() }
            val newTokens = try {
                api.refresh(refresh)
            } catch (refreshError: Exception) {
                logout()
                throw SessionExpired()
            }
            save(newTokens.accessToken, newTokens.refreshToken)
            call(newTokens.accessToken)
        }
    }
}
