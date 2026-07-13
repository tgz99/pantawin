package com.pantawin.app.monitors

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.pantawin.app.data.MonitorGateway
import com.pantawin.shared.model.MonitorState
import com.pantawin.shared.model.MonitorStatus
import com.pantawin.shared.model.RealtimeEvent
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.emptyFlow
import kotlinx.coroutines.launch

sealed interface MonitorsUiState {
    data object Loading : MonitorsUiState
    data class Loaded(val monitors: List<MonitorStatus>) : MonitorsUiState
    data class Error(val message: String) : MonitorsUiState
}

class MonitorsViewModel(
    private val gateway: MonitorGateway,
    // Live WebSocket feed (spec 6.4). Defaults to empty so unit tests need no
    // realtime plumbing; the app supplies the real stream.
    private val realtimeEvents: Flow<RealtimeEvent> = emptyFlow(),
    private val onSessionExpired: () -> Unit = {},
) : ViewModel() {

    private val _uiState = MutableStateFlow<MonitorsUiState>(MonitorsUiState.Loading)
    val uiState: StateFlow<MonitorsUiState> = _uiState.asStateFlow()

    init {
        refresh()
        observeRealtime()
    }

    fun refresh() {
        _uiState.value = MonitorsUiState.Loading
        viewModelScope.launch {
            runCatching { gateway.list() }
                .onSuccess { _uiState.value = MonitorsUiState.Loaded(it) }
                .onFailure { handle(it) }
        }
    }

    // Apply live status/incident events to the in-memory list without a full
    // refetch, so the dashboard updates instantly while foregrounded.
    private fun observeRealtime() {
        viewModelScope.launch {
            runCatching {
                realtimeEvents.collect { event -> applyEvent(event) }
            }
            // Silent on failure: the WS just isn't live; pull-to-refresh and
            // the periodic list fetch still work.
        }
    }

    private fun applyEvent(event: RealtimeEvent) {
        val current = _uiState.value as? MonitorsUiState.Loaded ?: return
        // An event for a monitor we don't have means the list is stale
        // (e.g. added from another device) — refetch instead of patching.
        if (current.monitors.none { it.id == event.monitorId }) {
            refresh()
            return
        }
        val newState = runCatching { MonitorState.valueOf(event.status) }.getOrNull() ?: return
        val updated = current.monitors.map { m ->
            if (m.id == event.monitorId) {
                m.copy(status = newState, responseTimeMs = event.responseTimeMs ?: m.responseTimeMs)
            } else {
                m
            }
        }
        _uiState.value = MonitorsUiState.Loaded(updated)
    }

    fun pause(id: Long) = mutate { gateway.pause(id) }
    fun resume(id: Long) = mutate { gateway.resume(id) }
    fun delete(id: Long) = mutate { gateway.delete(id) }

    private fun mutate(action: suspend () -> Unit) {
        viewModelScope.launch {
            runCatching { action() }
                .onSuccess { refresh() }
                .onFailure { handle(it) }
        }
    }

    private fun handle(t: Throwable) {
        if (t is com.pantawin.app.data.SessionManager.SessionExpired) {
            onSessionExpired()
            return
        }
        _uiState.value = MonitorsUiState.Error(t.message ?: "Something went wrong")
    }
}
