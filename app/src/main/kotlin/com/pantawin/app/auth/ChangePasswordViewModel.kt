package com.pantawin.app.auth

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.pantawin.app.data.SessionManager
import com.pantawin.shared.api.PantawinApiException
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

data class ChangePasswordState(
    val submitting: Boolean = false,
    val error: String? = null,
    val done: Boolean = false,
)

class ChangePasswordViewModel(private val session: SessionManager) : ViewModel() {

    private val _state = MutableStateFlow(ChangePasswordState())
    val state: StateFlow<ChangePasswordState> = _state.asStateFlow()

    fun submit(current: String, new: String, confirm: String) {
        // Client-side mirror of the server policy for fast feedback; the
        // server is the authority.
        val policyError = validate(current, new, confirm)
        if (policyError != null) {
            _state.value = ChangePasswordState(error = policyError)
            return
        }
        _state.value = ChangePasswordState(submitting = true)
        viewModelScope.launch {
            runCatching { session.changePassword(current, new) }
                .onSuccess { _state.value = ChangePasswordState(done = true) }
                .onFailure { e ->
                    val msg = (e as? PantawinApiException)?.message
                        ?: e.message ?: "Failed to change password"
                    _state.value = ChangePasswordState(error = msg)
                }
        }
    }

    private fun validate(current: String, new: String, confirm: String): String? = when {
        current.isBlank() -> "Enter your current password"
        new.length < 8 -> "New password must be at least 8 characters"
        new.none { it.isUpperCase() } -> "New password needs at least one uppercase letter"
        new.none { it.isDigit() } -> "New password needs at least one number"
        new != confirm -> "New passwords don't match"
        else -> null
    }
}
