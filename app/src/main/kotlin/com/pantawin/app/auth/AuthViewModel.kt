package com.pantawin.app.auth

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.pantawin.app.data.SessionManager
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

data class LoginState(
    val submitting: Boolean = false,
    val error: String? = null,
)

class AuthViewModel(private val session: SessionManager) : ViewModel() {

    private val _state = MutableStateFlow(LoginState())
    val state: StateFlow<LoginState> = _state.asStateFlow()

    fun login(email: String, password: String) {
        if (email.isBlank() || password.isBlank()) {
            _state.value = LoginState(error = "Enter your email and password")
            return
        }
        _state.value = LoginState(submitting = true)
        viewModelScope.launch {
            runCatching { session.login(email.trim(), password) }
                .onFailure { _state.value = LoginState(error = it.message ?: "Login failed") }
            // Success flips SessionManager.isLoggedIn, which the nav host observes.
        }
    }
}
