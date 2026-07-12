package com.pantawin.shared.api

import com.pantawin.shared.model.MonitorStatus
import com.pantawin.shared.model.Tokens
import io.ktor.client.HttpClient
import io.ktor.client.call.body
import io.ktor.client.request.get
import io.ktor.client.request.header
import io.ktor.client.request.post
import io.ktor.client.request.setBody
import io.ktor.http.ContentType
import io.ktor.http.contentType
import kotlinx.serialization.Serializable

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

    suspend fun login(email: String, password: String): Tokens {
        return client.post("$baseUrl/v1/auth/login") {
            contentType(ContentType.Application.Json)
            setBody(LoginRequest(email, password))
        }.body()
    }

    suspend fun getMonitors(accessToken: String): List<MonitorStatus> {
        return client.get("$baseUrl/v1/monitors") {
            header("Authorization", "Bearer $accessToken")
        }.body()
    }
}
