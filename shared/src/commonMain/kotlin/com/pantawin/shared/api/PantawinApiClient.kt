package com.pantawin.shared.api

import com.pantawin.shared.model.Monitor
import com.pantawin.shared.model.MonitorInput
import com.pantawin.shared.model.MonitorStatus
import com.pantawin.shared.model.Tokens
import io.ktor.client.HttpClient
import io.ktor.client.call.body
import io.ktor.client.request.delete
import io.ktor.client.request.get
import io.ktor.client.request.header
import io.ktor.client.request.patch
import io.ktor.client.request.post
import io.ktor.client.request.setBody
import io.ktor.client.statement.HttpResponse
import io.ktor.http.ContentType
import io.ktor.http.HttpStatusCode
import io.ktor.http.contentType
import io.ktor.http.isSuccess
import kotlinx.serialization.Serializable

/** Raised for any non-2xx API response so callers can react to status. */
class PantawinApiException(val status: Int, message: String) : Exception(message)

/**
 * Thin wrapper over the Pantawin REST API (spec section 4). Platform code
 * supplies the [HttpClient] (see androidMain's HttpClientFactory) so this
 * class stays engine-agnostic.
 */
class PantawinApiClient(
    private val client: HttpClient,
    private val baseUrl: String,
) {
    @Serializable
    private data class LoginRequest(val email: String, val password: String)

    @Serializable
    private data class RefreshRequest(val refresh_token: String)

    @Serializable
    private data class RegisterDeviceRequest(val fcm_token: String, val platform: String = "android")

    @Serializable
    private data class ChangePasswordRequest(val current_password: String, val new_password: String)

    @Serializable
    private data class GoogleLoginRequest(val id_token: String)

    // --- Auth ---

    suspend fun login(email: String, password: String): Tokens =
        client.post("$baseUrl/v1/auth/login") {
            contentType(ContentType.Application.Json)
            setBody(LoginRequest(email, password))
        }.requireSuccess().body()

    suspend fun register(email: String, password: String): Tokens =
        client.post("$baseUrl/v1/auth/register") {
            contentType(ContentType.Application.Json)
            setBody(LoginRequest(email, password))
        }.requireSuccess().body()

    /** Exchanges a Google ID token (from Credential Manager) for a session. */
    suspend fun loginWithGoogle(idToken: String): Tokens =
        client.post("$baseUrl/v1/auth/google") {
            contentType(ContentType.Application.Json)
            setBody(GoogleLoginRequest(idToken))
        }.requireSuccess().body()

    suspend fun refresh(refreshToken: String): Tokens =
        client.post("$baseUrl/v1/auth/refresh") {
            contentType(ContentType.Application.Json)
            setBody(RefreshRequest(refreshToken))
        }.requireSuccess().body()

    /** Rotates the password; the server revokes all prior sessions and
     *  returns a fresh token pair for this one. */
    suspend fun changePassword(accessToken: String, currentPassword: String, newPassword: String): Tokens =
        client.post("$baseUrl/v1/auth/change-password") {
            bearer(accessToken)
            contentType(ContentType.Application.Json)
            setBody(ChangePasswordRequest(currentPassword, newPassword))
        }.requireSuccess().body()

    // --- Monitors ---

    suspend fun getMonitors(accessToken: String): List<MonitorStatus> =
        client.get("$baseUrl/v1/monitors") { bearer(accessToken) }
            .requireSuccess().body()

    suspend fun getMonitor(accessToken: String, id: Long): Monitor =
        client.get("$baseUrl/v1/monitors/$id") { bearer(accessToken) }
            .requireSuccess().body()

    suspend fun createMonitor(accessToken: String, input: MonitorInput): Monitor =
        client.post("$baseUrl/v1/monitors") {
            bearer(accessToken)
            contentType(ContentType.Application.Json)
            setBody(input)
        }.requireSuccess().body()

    suspend fun updateMonitor(accessToken: String, id: Long, input: MonitorInput): Monitor =
        client.patch("$baseUrl/v1/monitors/$id") {
            bearer(accessToken)
            contentType(ContentType.Application.Json)
            setBody(input)
        }.requireSuccess().body()

    suspend fun deleteMonitor(accessToken: String, id: Long) {
        client.delete("$baseUrl/v1/monitors/$id") { bearer(accessToken) }.requireSuccess()
    }

    suspend fun pauseMonitor(accessToken: String, id: Long): Monitor =
        client.post("$baseUrl/v1/monitors/$id/pause") { bearer(accessToken) }
            .requireSuccess().body()

    suspend fun resumeMonitor(accessToken: String, id: Long): Monitor =
        client.post("$baseUrl/v1/monitors/$id/resume") { bearer(accessToken) }
            .requireSuccess().body()

    // --- Devices (M3 push) ---

    suspend fun registerDevice(accessToken: String, fcmToken: String) {
        client.post("$baseUrl/v1/devices") {
            bearer(accessToken)
            contentType(ContentType.Application.Json)
            setBody(RegisterDeviceRequest(fcmToken))
        }.requireSuccess()
    }
}

private fun io.ktor.client.request.HttpRequestBuilder.bearer(token: String) {
    header("Authorization", "Bearer $token")
}

private suspend fun HttpResponse.requireSuccess(): HttpResponse {
    if (!status.isSuccess() && status != HttpStatusCode.NoContent) {
        val body = runCatching { body<Map<String, String>>()["error"] }.getOrNull()
        throw PantawinApiException(status.value, body ?: "request failed with ${status.value}")
    }
    return this
}
