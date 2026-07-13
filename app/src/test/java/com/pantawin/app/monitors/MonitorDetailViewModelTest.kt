package com.pantawin.app.monitors

import com.pantawin.app.data.MonitorGateway
import com.pantawin.shared.model.Incident
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
import kotlin.test.assertNotNull
import kotlin.test.assertNull

@OptIn(ExperimentalCoroutinesApi::class)
class MonitorDetailViewModelTest {

    private val dispatcher = UnconfinedTestDispatcher()

    @Before fun setUp() = Dispatchers.setMain(dispatcher)
    @After fun tearDown() = Dispatchers.resetMain()

    private class FakeGateway(
        var failStats: Boolean = false,
    ) : MonitorGateway {
        val statsRequests = mutableListOf<Pair<String, String>>() // (period, tz)

        override suspend fun list(): List<MonitorStatus> =
            listOf(MonitorStatus(id = 7, name = "m7", url = "https://m7.test", status = MonitorState.UP))

        override suspend fun create(input: MonitorInput): Monitor = throw NotImplementedError()
        override suspend fun pause(id: Long) {}
        override suspend fun resume(id: Long) {}
        override suspend fun delete(id: Long) {}

        override suspend fun stats(id: Long, period: String, tz: String): MonitorStats {
            if (failStats) throw RuntimeException("boom")
            statsRequests += period to tz
            return MonitorStats(
                period = period, tz = tz, from = "2026-06-10T17:00:00Z", to = "2026-06-11T17:00:00Z",
                checks = 540, fails = 95,
            )
        }

        override suspend fun incidents(id: Long): List<Incident> = listOf(
            Incident(id = 2, monitorId = id, startedAt = "2026-06-11T06:30:00Z", cause = "http_500"),
            Incident(
                id = 1, monitorId = id, startedAt = "2026-06-10T23:30:00Z",
                resolvedAt = "2026-06-11T00:15:00Z", cause = "timeout", durationS = 2700,
            ),
        )
    }

    // With UnconfinedTestDispatcher, init{ load() } completes synchronously.

    @Test
    fun loadsStatsAndIncidents_defaultsToDayInDeviceZone() = runTest(dispatcher) {
        val gateway = FakeGateway()
        val vm = MonitorDetailViewModel(gateway, monitorId = 7)

        val state = vm.state.value
        assertEquals("day", state.period)
        assertNotNull(state.stats)
        assertEquals(2, state.incidents.size)
        assertEquals(true, state.incidents[0].ongoing)
        assertNull(state.incidents[0].durationS)

        val (period, tz) = gateway.statsRequests.single()
        assertEquals("day", period)
        // The device zone, whatever it is on this JVM — never blank, never UTC-hardcoded.
        assertEquals(java.time.ZoneId.systemDefault().id, tz)
    }

    @Test
    fun setPeriodReloadsEveryPeriod() = runTest(dispatcher) {
        val gateway = FakeGateway()
        val vm = MonitorDetailViewModel(gateway, monitorId = 7)

        for (period in listOf("week", "month", "year")) {
            vm.setPeriod(period)
            assertEquals(period, vm.state.value.period)
            assertEquals(period, vm.state.value.stats?.period)
        }
        assertEquals(listOf("day", "week", "month", "year"), gateway.statsRequests.map { it.first })

        // Re-selecting the current period is a no-op.
        vm.setPeriod("year")
        assertEquals(4, gateway.statsRequests.size)
    }

    @Test
    fun statsFailureSurfacesError() = runTest(dispatcher) {
        val vm = MonitorDetailViewModel(FakeGateway(failStats = true), monitorId = 7)
        assertEquals("boom", vm.state.value.error)
        assertNull(vm.state.value.stats)
    }
}
