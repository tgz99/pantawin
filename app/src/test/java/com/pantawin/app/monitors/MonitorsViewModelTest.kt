package com.pantawin.app.monitors

import com.pantawin.app.data.MonitorGateway
import com.pantawin.shared.model.Monitor
import com.pantawin.shared.model.MonitorInput
import com.pantawin.shared.model.MonitorState
import com.pantawin.shared.model.MonitorStats
import com.pantawin.shared.model.MonitorStatus
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
class MonitorsViewModelTest {

    private val dispatcher = UnconfinedTestDispatcher()

    @Before fun setUp() = Dispatchers.setMain(dispatcher)
    @After fun tearDown() = Dispatchers.resetMain()

    private class FakeGateway(
        var monitors: List<MonitorStatus> = emptyList(),
        var failList: Boolean = false,
    ) : MonitorGateway {
        val paused = mutableListOf<Long>()
        val deleted = mutableListOf<Long>()
        override suspend fun list(): List<MonitorStatus> {
            if (failList) throw RuntimeException("network down")
            return monitors
        }
        override suspend fun create(input: MonitorInput): Monitor = throw NotImplementedError()
        override suspend fun pause(id: Long) { paused += id }
        override suspend fun resume(id: Long) {}
        override suspend fun delete(id: Long) { deleted += id }
        override suspend fun stats(id: Long, period: String): MonitorStats = throw NotImplementedError()
    }

    private fun status(id: Long, state: MonitorState) =
        MonitorStatus(id = id, name = "m$id", url = "https://m$id.test", status = state)

    // With UnconfinedTestDispatcher the ViewModel's init{ refresh() } runs to
    // completion synchronously during construction, so uiState has already
    // settled to its terminal value by the time we read it.

    @Test
    fun loadsMonitorsIntoLoadedState() = runTest(dispatcher) {
        val gateway = FakeGateway(monitors = listOf(status(1, MonitorState.UP)))
        val vm = MonitorsViewModel(gateway)

        val loaded = vm.uiState.value
        assertIs<MonitorsUiState.Loaded>(loaded)
        assertEquals(1, loaded.monitors.size)
    }

    @Test
    fun listFailureBecomesErrorState() = runTest(dispatcher) {
        val vm = MonitorsViewModel(FakeGateway(failList = true))

        val err = vm.uiState.value
        assertIs<MonitorsUiState.Error>(err)
        assertTrue(err.message.isNotBlank())
    }

    @Test
    fun deleteInvokesGatewayAndRefreshes() = runTest(dispatcher) {
        val gateway = FakeGateway(monitors = listOf(status(3, MonitorState.UP)))
        val vm = MonitorsViewModel(gateway)

        vm.delete(3)

        assertEquals(listOf(3L), gateway.deleted)
    }
}
