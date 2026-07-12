package com.pantawin.app.dashboard

import app.cash.turbine.test
import com.pantawin.shared.api.PantawinApiClient
import com.pantawin.shared.api.installPantawinJson
import io.ktor.client.HttpClient
import io.ktor.client.engine.mock.MockEngine
import io.ktor.client.engine.mock.respond
import io.ktor.http.HttpHeaders
import io.ktor.http.HttpStatusCode
import io.ktor.http.headersOf
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.UnconfinedTestDispatcher
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import org.junit.After
import org.junit.Before
import org.junit.Test
import kotlin.test.assertEquals
import kotlin.test.assertIs
import kotlin.test.assertTrue

@OptIn(ExperimentalCoroutinesApi::class)
class DashboardViewModelTest {

    private val testDispatcher = UnconfinedTestDispatcher()

    @Before
    fun setUp() {
        Dispatchers.setMain(testDispatcher)
    }

    @After
    fun tearDown() {
        Dispatchers.resetMain()
    }

    private fun apiRespondingWith(monitorsJson: String): PantawinApiClient {
        val mockEngine = MockEngine { request ->
            val body = if (request.url.encodedPath.endsWith("/auth/login")) {
                """{"access_token":"test-access","refresh_token":"test-refresh"}"""
            } else {
                monitorsJson
            }
            respond(content = body, status = HttpStatusCode.OK, headers = headersOf(HttpHeaders.ContentType, "application/json"))
        }
        val httpClient = HttpClient(mockEngine) { installPantawinJson() }
        return PantawinApiClient(httpClient, baseUrl = "https://api.pantawin.gratisaja.com")
    }

    // Turbine's awaitItem() suspends properly across the ViewModel's real
    // Ktor call (which genuinely dispatches off-thread even against
    // MockEngine), unlike reading uiState.value synchronously right after
    // construction, which races the in-flight coroutine and observes
    // Loading before it resolves.
    @Test
    fun refresh_populatesLoadedStateWithMonitors() = runTest(testDispatcher) {
        val api = apiRespondingWith(
            """[{"id":1,"name":"gratisaja.com","url":"https://gratisaja.com","status":"UP","last_checked_at":"2026-07-12T10:00:00Z","response_time_ms":120}]""",
        )
        val viewModel = DashboardViewModel(api = api, email = "tester@pantawin.gratisaja.com", password = "irrelevant")

        viewModel.uiState.test {
            assertEquals(DashboardUiState.Loading, awaitItem())
            val loaded = awaitItem()
            assertIs<DashboardUiState.Loaded>(loaded)
            assertEquals(1, loaded.monitors.size)
            assertEquals("gratisaja.com", loaded.monitors[0].name)
        }
    }

    @Test
    fun refresh_setsErrorStateOnFailure() = runTest(testDispatcher) {
        val mockEngine = MockEngine { _ ->
            respond(content = "boom", status = HttpStatusCode.InternalServerError)
        }
        val httpClient = HttpClient(mockEngine) { installPantawinJson() }
        val api = PantawinApiClient(httpClient, baseUrl = "https://api.pantawin.gratisaja.com")

        val viewModel = DashboardViewModel(api = api, email = "tester@pantawin.gratisaja.com", password = "irrelevant")

        viewModel.uiState.test {
            assertEquals(DashboardUiState.Loading, awaitItem())
            val errored = awaitItem()
            assertIs<DashboardUiState.Error>(errored)
            assertTrue(errored.message.isNotBlank())
        }
    }
}
