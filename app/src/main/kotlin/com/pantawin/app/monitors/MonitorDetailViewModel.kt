package com.pantawin.app.monitors

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.pantawin.app.data.MonitorGateway
import com.pantawin.shared.model.Incident
import com.pantawin.shared.model.MonitorStats
import com.pantawin.shared.model.MonitorStatus
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import java.time.ZoneId

// The selectable analytics periods, in chip order (M4 day/week, M5 month/year).
val STATS_PERIODS = listOf("day", "week", "month", "year")

data class MonitorDetailState(
    val monitor: MonitorStatus? = null,
    val stats: MonitorStats? = null,
    val incidents: List<Incident> = emptyList(),
    val period: String = "day",
    val loading: Boolean = true,
    val error: String? = null,
)

class MonitorDetailViewModel(
    private val gateway: MonitorGateway,
    private val monitorId: Long,
) : ViewModel() {

    private val _state = MutableStateFlow(MonitorDetailState())
    val state: StateFlow<MonitorDetailState> = _state.asStateFlow()

    // Stats windows follow the device's calendar (spec M5: WIB rendering
    // over UTC storage) — the server buckets in whatever IANA zone we send.
    private val tz: String = ZoneId.systemDefault().id

    init {
        load()
    }

    fun setPeriod(period: String) {
        if (period == _state.value.period) return
        _state.value = _state.value.copy(period = period)
        load()
    }

    fun load() {
        val period = _state.value.period
        _state.value = _state.value.copy(loading = true, error = null)
        viewModelScope.launch {
            runCatching {
                // The list endpoint carries the header info (name/url/status);
                // stats carries the analytics; incidents the history card.
                // Fetched together on each period switch so all stay fresh.
                val monitor = gateway.list().firstOrNull { it.id == monitorId }
                val stats = gateway.stats(monitorId, period, tz)
                val incidents = gateway.incidents(monitorId)
                Triple(monitor, stats, incidents)
            }.onSuccess { (monitor, stats, incidents) ->
                _state.value = MonitorDetailState(
                    monitor = monitor, stats = stats, incidents = incidents,
                    period = period, loading = false,
                )
            }.onFailure {
                _state.value = _state.value.copy(
                    loading = false,
                    error = it.message ?: "Failed to load stats",
                )
            }
        }
    }
}
