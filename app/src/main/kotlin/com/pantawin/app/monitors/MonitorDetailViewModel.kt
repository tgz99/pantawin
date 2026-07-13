package com.pantawin.app.monitors

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.pantawin.app.data.MonitorGateway
import com.pantawin.shared.model.MonitorStats
import com.pantawin.shared.model.MonitorStatus
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

data class MonitorDetailState(
    val monitor: MonitorStatus? = null,
    val stats: MonitorStats? = null,
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
                // stats carries the analytics. Fetched together on each
                // period switch so the header stays fresh too.
                val monitor = gateway.list().firstOrNull { it.id == monitorId }
                val stats = gateway.stats(monitorId, period)
                monitor to stats
            }.onSuccess { (monitor, stats) ->
                _state.value = MonitorDetailState(
                    monitor = monitor, stats = stats, period = period, loading = false,
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
