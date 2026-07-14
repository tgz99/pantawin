package com.pantawin.app.team

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.pantawin.app.data.TeamGateway
import com.pantawin.shared.api.PantawinApiException
import com.pantawin.shared.model.TeamMember
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

data class TeamUiState(
    val loading: Boolean = true,
    val members: List<TeamMember> = emptyList(),
    // 403 from the server: this account isn't the admin. The screen shows a
    // read-only explanation instead of the management UI.
    val notAdmin: Boolean = false,
    val submitting: Boolean = false,
    val error: String? = null,
)

class TeamViewModel(private val gateway: TeamGateway) : ViewModel() {

    private val _state = MutableStateFlow(TeamUiState())
    val state: StateFlow<TeamUiState> = _state.asStateFlow()

    init {
        refresh()
    }

    fun refresh() {
        viewModelScope.launch {
            _state.update { it.copy(loading = true, error = null) }
            runCatching { gateway.list() }
                .onSuccess { members ->
                    _state.update { it.copy(loading = false, members = members, notAdmin = false) }
                }
                .onFailure { e ->
                    if ((e as? PantawinApiException)?.status == 403) {
                        _state.update { it.copy(loading = false, notAdmin = true) }
                    } else {
                        _state.update { it.copy(loading = false, error = e.message ?: "Failed to load team") }
                    }
                }
        }
    }

    fun invite(email: String) {
        val trimmed = email.trim()
        if (!trimmed.contains('@') || trimmed.contains(' ')) {
            _state.update { it.copy(error = "Enter a valid email address") }
            return
        }
        viewModelScope.launch {
            _state.update { it.copy(submitting = true, error = null) }
            runCatching { gateway.add(trimmed) }
                .onSuccess {
                    _state.update { it.copy(submitting = false) }
                    refresh()
                }
                .onFailure { e ->
                    _state.update { it.copy(submitting = false, error = e.message ?: "Failed to invite") }
                }
        }
    }

    fun remove(email: String) {
        viewModelScope.launch {
            _state.update { it.copy(error = null) }
            runCatching { gateway.remove(email) }
                .onSuccess { refresh() }
                .onFailure { e ->
                    _state.update { it.copy(error = e.message ?: "Failed to remove") }
                }
        }
    }
}
