package com.pantawin.shared.api

import com.pantawin.shared.model.MonitorInput
import com.pantawin.shared.model.MonitorState
import io.ktor.client.HttpClient
import io.ktor.client.engine.mock.MockEngine
import io.ktor.client.engine.mock.respond
import io.ktor.http.HttpHeaders
import io.ktor.http.HttpMethod
import io.ktor.http.HttpStatusCode
import io.ktor.http.headersOf
import io.ktor.utils.io.ByteReadChannel
import kotlinx.coroutines.test.runTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue

class MonitorCrudTest {

    private val monitorJson = """
        {"id":7,"name":"gratisaja.com","url":"https://gratisaja.com","method":"GET",
         "interval_seconds":60,"timeout_ms":10000,"expected_status_min":200,
         "expected_status_max":399,"failure_threshold":2,"status":"PENDING",
         "created_at":"2026-07-13T00:00:00Z"}
    """.trimIndent()

    private fun client(handler: (io.ktor.client.request.HttpRequestData) -> Pair<HttpStatusCode, String>): PantawinApiClient {
        val engine = MockEngine { request ->
            val (status, body) = handler(request)
            respond(
                content = ByteReadChannel(body),
                status = status,
                headers = headersOf(HttpHeaders.ContentType, "application/json"),
            )
        }
        val http = HttpClient(engine) { installPantawinJson() }
        return PantawinApiClient(http, "https://api.pantawin.gratisaja.com")
    }

    @Test
    fun createMonitor_sendsInputAndParsesResponse() = runTest {
        val api = client { request ->
            assertEquals(HttpMethod.Post, request.method)
            assertTrue(request.url.encodedPath.endsWith("/v1/monitors"))
            HttpStatusCode.Created to monitorJson
        }
        val monitor = api.createMonitor("tok", MonitorInput(url = "https://gratisaja.com", intervalSeconds = 60))
        assertEquals(7L, monitor.id)
        assertEquals(MonitorState.PENDING, monitor.status)
        assertEquals(60, monitor.intervalSeconds)
    }

    @Test
    fun pauseMonitor_hitsPauseEndpoint() = runTest {
        val paused = monitorJson.replace("\"PENDING\"", "\"PAUSED\"")
        val api = client { request ->
            assertTrue(request.url.encodedPath.endsWith("/v1/monitors/7/pause"))
            HttpStatusCode.OK to paused
        }
        val monitor = api.pauseMonitor("tok", 7)
        assertEquals(MonitorState.PAUSED, monitor.status)
    }

    @Test
    fun deleteMonitor_acceptsNoContent() = runTest {
        val api = client { request ->
            assertEquals(HttpMethod.Delete, request.method)
            HttpStatusCode.NoContent to ""
        }
        api.deleteMonitor("tok", 7) // must not throw on 204
    }

    @Test
    fun apiError_surfacesAsPantawinApiException() = runTest {
        val api = client { _ ->
            HttpStatusCode.UnprocessableEntity to """{"error":"monitor quota exceeded"}"""
        }
        val ex = assertFailsWith<PantawinApiException> {
            api.createMonitor("tok", MonitorInput(url = "https://gratisaja.com"))
        }
        assertEquals(422, ex.status)
        assertTrue(ex.message!!.contains("quota"))
    }
}
