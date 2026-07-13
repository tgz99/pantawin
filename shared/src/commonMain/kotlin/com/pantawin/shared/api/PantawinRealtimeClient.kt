package com.pantawin.shared.api

import com.pantawin.shared.model.RealtimeEvent
import io.ktor.client.HttpClient
import io.ktor.client.plugins.websocket.webSocket
import io.ktor.websocket.Frame
import io.ktor.websocket.readText
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.flow
import kotlinx.serialization.json.Json

/**
 * Streams the live dashboard feed over WebSocket (spec 6.4). Emits a
 * [RealtimeEvent] per server message; the flow completes when the socket
 * closes. Callers collect it while the dashboard is foregrounded and cancel
 * the collecting coroutine to disconnect.
 *
 * The [httpClient] must have Ktor's WebSockets plugin installed (the Android
 * factory does).
 */
class PantawinRealtimeClient(
    private val httpClient: HttpClient,
    private val baseUrl: String,
) {
    private val json = Json { ignoreUnknownKeys = true }

    fun events(accessToken: String): Flow<RealtimeEvent> = flow {
        // ws(s):// scheme derived from the API base URL.
        val wsUrl = baseUrl.replaceFirst("http", "ws") + "/v1/ws?access_token=$accessToken"
        httpClient.webSocket(urlString = wsUrl) {
            for (frame in incoming) {
                if (frame is Frame.Text) {
                    val event = runCatching { json.decodeFromString<RealtimeEvent>(frame.readText()) }.getOrNull()
                    if (event != null) emit(event)
                }
            }
        }
    }
}
