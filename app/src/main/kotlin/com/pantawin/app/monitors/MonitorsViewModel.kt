package com.pantawin.app.monitors

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.pantawin.app.data.MonitorGateway
import com.pantawin.shared.model.MonitorStatus
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

sealed interface MonitorsUiState {
    data object Loading : MonitorsUiState
    data class Loaded(val monitors: List<MonitorStatus>) : MonitorsUiState
    data class Error(val message: String) : MonitorsUiState
}

class MonitorsViewModel(
    private val gateway: MonitorGateway,
    private val onSessionExpired: () -> Unit = {},
) : ViewModel() {

    private val _uiState = MutableStateFlow<MonitorsUiState>(MonitorsUiState.Loading)
    val uiState: StateFlow<MonitorsUiState> = _uiState.asStateFlow()

    init {
        refresh()
    }

    fun refresh() {
        _uiState.value = MonitorsUiState.Loading
        viewModelScope.launch {
            runCatching { gateway.list() }
                .onSuccess { _uiState.value = MonitorsUiState.Loaded(it) }
                .onFailure { handle(it) }
        }
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
