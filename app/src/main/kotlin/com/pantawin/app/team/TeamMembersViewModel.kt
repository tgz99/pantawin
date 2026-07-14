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

data class TeamMembersUiState(
    val loading: Boolean = true,
    val members: List<TeamMember> = emptyList(),
    // 403 from the server: no longer a member of this team (e.g. removed
    // elsewhere mid-session). The screen shows a read-only explanation.
    val notMember: Boolean = false,
    val submitting: Boolean = false,
    val error: String? = null,
)

/** Member management for one specific team (M6.3 — any current member,
 * not just its creator, may invite others). */
class TeamMembersViewModel(private val gateway: TeamGateway, private val teamId: Long) : ViewModel() {

    private val _state = MutableStateFlow(TeamMembersUiState())
    val state: StateFlow<TeamMembersUiState> = _state.asStateFlow()

    init {
        refresh()
    }

    fun refresh() {
        viewModelScope.launch {
            _state.update { it.copy(loading = true, error = null) }
            runCatching { gateway.listMembers(teamId) }
                .onSuccess { members ->
                    _state.update { it.copy(loading = false, members = members, notMember = false) }
                }
                .onFailure { e ->
                    if ((e as? PantawinApiException)?.status == 403) {
                        _state.update { it.copy(loading = false, notMember = true) }
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
            runCatching { gateway.invite(teamId, trimmed) }
                .onSuccess {
                    _state.update { it.copy(submitting = false) }
                    refresh()
                }
                .onFailure { e -> _state.update { it.copy(submitting = false, error = e.message ?: "Failed to invite") } }
        }
    }

    fun removeInvite(email: String) {
        viewModelScope.launch {
            _state.update { it.copy(error = null) }
            runCatching { gateway.removeInvite(teamId, email) }
                .onSuccess { refresh() }
                .onFailure { e -> _state.update { it.copy(error = e.message ?: "Failed to remove") } }
        }
    }
}
