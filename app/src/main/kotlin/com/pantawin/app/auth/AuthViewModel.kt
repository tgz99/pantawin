package com.pantawin.app.auth

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.pantawin.app.data.SessionManager
import com.pantawin.shared.api.PantawinApiException
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

/** Which logged-out screen is showing (M6.2: email/password signup gained a
 * registration + OTP-verification step; Google sign-in still skips both). */
sealed interface AuthScreen {
    data object Login : AuthScreen
    data object Register : AuthScreen
    data class VerifyOtp(val email: String) : AuthScreen
}

data class AuthUiState(
    val screen: AuthScreen = AuthScreen.Login,
    val submitting: Boolean = false,
    val error: String? = null,
    val info: String? = null,
)

class AuthViewModel(private val session: SessionManager) : ViewModel() {

    private val _state = MutableStateFlow(AuthUiState())
    val state: StateFlow<AuthUiState> = _state.asStateFlow()

    fun showRegister() {
        _state.value = AuthUiState(screen = AuthScreen.Register)
    }

    fun showLogin() {
        _state.value = AuthUiState(screen = AuthScreen.Login)
    }

    fun login(email: String, password: String) {
        if (email.isBlank() || password.isBlank()) {
            _state.update { it.copy(error = "Enter your email and password") }
            return
        }
        val trimmedEmail = email.trim()
        _state.update { it.copy(submitting = true, error = null, info = null) }
        viewModelScope.launch {
            runCatching { session.login(trimmedEmail, password) }
                .onFailure { e ->
                    // 428: registered but never finished OTP verification —
                    // route straight to the code screen instead of a bare
                    // "login failed" the user can't act on.
                    if ((e as? PantawinApiException)?.status == 428) {
                        _state.value = AuthUiState(
                            screen = AuthScreen.VerifyOtp(trimmedEmail),
                            info = "Verify your email to finish signing in. Enter the code we sent, or resend it below.",
                        )
                    } else {
                        _state.update { s -> s.copy(submitting = false, error = e.message ?: "Login failed") }
                    }
                }
            // Success flips SessionManager.isLoggedIn, which the nav host observes.
        }
    }

    /** Completes Google sign-in with the ID token Credential Manager returned.
     * No OTP step regardless of whether this creates a new account. */
    fun loginWithGoogle(idToken: String) {
        _state.update { it.copy(submitting = true, error = null, info = null) }
        viewModelScope.launch {
            runCatching { session.loginWithGoogle(idToken) }
                .onFailure { e -> _state.update { s -> s.copy(submitting = false, error = e.message ?: "Google sign-in failed") } }
        }
    }

    fun googleSignInFailed(message: String?) {
        // User cancellation passes null — not an error worth showing.
        _state.update { it.copy(submitting = false, error = message) }
    }

    /** Starts email/password signup — success moves to the OTP screen, it
     * does not log the user in yet. */
    fun register(email: String, password: String) {
        val trimmedEmail = email.trim()
        if (trimmedEmail.isBlank() || password.isBlank()) {
            _state.update { it.copy(error = "Enter your email and password") }
            return
        }
        _state.update { it.copy(submitting = true, error = null, info = null) }
        viewModelScope.launch {
            runCatching { session.register(trimmedEmail, password) }
                .onSuccess {
                    _state.value = AuthUiState(
                        screen = AuthScreen.VerifyOtp(trimmedEmail),
                        info = "We sent a 6-digit code to $trimmedEmail.",
                    )
                }
                .onFailure { e -> _state.update { s -> s.copy(submitting = false, error = e.message ?: "Registration failed") } }
        }
    }

    fun verifyOtp(email: String, code: String) {
        val trimmedCode = code.trim()
        if (trimmedCode.isBlank()) {
            _state.update { it.copy(error = "Enter the code") }
            return
        }
        _state.update { it.copy(submitting = true, error = null, info = null) }
        viewModelScope.launch {
            runCatching { session.verifyOtp(email, trimmedCode) }
                .onFailure { e -> _state.update { s -> s.copy(submitting = false, error = e.message ?: "Verification failed") } }
            // Success flips SessionManager.isLoggedIn, which the nav host observes.
        }
    }

    fun resendOtp(email: String) {
        _state.update { it.copy(submitting = true, error = null, info = null) }
        viewModelScope.launch {
            runCatching { session.resendOtp(email) }
                .onSuccess { _state.update { s -> s.copy(submitting = false, info = "Code resent — check your email.") } }
                .onFailure { e -> _state.update { s -> s.copy(submitting = false, error = e.message ?: "Failed to resend code") } }
        }
    }
}
