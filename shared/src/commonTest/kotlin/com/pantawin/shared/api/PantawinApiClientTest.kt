package com.pantawin.shared.api

import com.pantawin.shared.model.MonitorState
import io.ktor.client.HttpClient
import io.ktor.client.engine.mock.MockEngine
import io.ktor.client.engine.mock.respond
import io.ktor.http.HttpHeaders
import io.ktor.http.HttpStatusCode
import io.ktor.http.headersOf
import kotlinx.coroutines.test.runTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull

class PantawinApiClientTest {

    private fun clientWith(status: HttpStatusCode, body: String): PantawinApiClient {
        val mockEngine = MockEngine { _ ->
            respond(
                content = body,
                status = status,
                headers = headersOf(HttpHeaders.ContentType, "application/json"),
            )
        }
        val httpClient = HttpClient(mockEngine) { installPantawinJson() }
        return PantawinApiClient(httpClient, baseUrl = "https://api.pantawin.gratisaja.com")
    }

    @Test
    fun login_parsesAccessAndRefreshTokens() = runTest {
        val client = clientWith(
            HttpStatusCode.OK,
            """{"access_token":"abc123","refresh_token":"def456"}""",
        )

        val tokens = client.login("tester@pantawin.gratisaja.com", "correct horse battery staple")

        assertEquals("abc123", tokens.accessToken)
        assertEquals("def456", tokens.refreshToken)
    }

    @Test
    fun getMonitors_parsesEmptyList() = runTest {
        val client = clientWith(HttpStatusCode.OK, "[]")

        val monitors = client.getMonitors(accessToken = "abc123")

        assertEquals(emptyList(), monitors)
    }

    @Test
    fun getMonitors_parsesMonitorWithNullLastChecked() = runTest {
        val client = clientWith(
            HttpStatusCode.OK,
            """[{"id":1,"name":"gratisaja.com","url":"https://gratisaja.com","status":"PENDING","last_checked_at":null,"response_time_ms":null}]""",
        )

        val monitors = client.getMonitors(accessToken = "abc123")

        assertEquals(1, monitors.size)
        val monitor = monitors[0]
        assertEquals(1L, monitor.id)
        assertEquals("gratisaja.com", monitor.name)
        assertEquals(MonitorState.PENDING, monitor.status)
        assertNull(monitor.lastCheckedAt)
        assertNull(monitor.responseTimeMs)
    }

    @Test
    fun getMonitors_parsesUpMonitorWithCheckData() = runTest {
        val client = clientWith(
            HttpStatusCode.OK,
            """[{"id":2,"name":"gratisaja.com","url":"https://gratisaja.com","status":"UP","last_checked_at":"2026-07-12T10:00:00Z","response_time_ms":123}]""",
        )

        val monitors = client.getMonitors(accessToken = "abc123")

        val monitor = monitors[0]
        assertEquals(MonitorState.UP, monitor.status)
        assertEquals("2026-07-12T10:00:00Z", monitor.lastCheckedAt)
        assertEquals(123, monitor.responseTimeMs)
    }
}
