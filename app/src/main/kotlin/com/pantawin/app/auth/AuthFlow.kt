package com.pantawin.app.auth

import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue

/** Switches between the logged-out screens (M6.2). */
@Composable
fun AuthFlow(viewModel: AuthViewModel) {
    val state by viewModel.state.collectAsState()
    when (val screen = state.screen) {
        AuthScreen.Login -> LoginScreen(viewModel)
        AuthScreen.Register -> RegisterScreen(viewModel)
        is AuthScreen.VerifyOtp -> VerifyOtpScreen(viewModel, email = screen.email)
    }
}
