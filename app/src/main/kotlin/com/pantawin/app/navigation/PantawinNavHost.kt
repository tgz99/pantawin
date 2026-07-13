package com.pantawin.app.navigation

import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.lifecycle.ViewModel
import androidx.lifecycle.ViewModelProvider
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.rememberNavController
import com.pantawin.app.auth.AuthViewModel
import com.pantawin.app.auth.LoginScreen
import com.pantawin.app.data.MonitorGateway
import com.pantawin.app.data.SessionManager
import com.pantawin.app.data.SessionMonitorGateway
import com.pantawin.app.monitors.AddMonitorScreen
import com.pantawin.app.monitors.AddMonitorViewModel
import com.pantawin.app.monitors.MonitorsScreen
import com.pantawin.app.monitors.MonitorsViewModel
import kotlinx.coroutines.launch

private object Routes {
    const val Monitors = "monitors"
    const val Add = "add"
}

// Minimal ViewModel factory for manual DI (M1). Each ViewModel gets exactly
// the dependency it needs.
private inline fun <VM : ViewModel> factory(crossinline create: () -> VM) =
    object : ViewModelProvider.Factory {
        @Suppress("UNCHECKED_CAST")
        override fun <T : ViewModel> create(modelClass: Class<T>): T = create() as T
    }

@Composable
fun PantawinNavHost(session: SessionManager) {
    val loggedIn by session.isLoggedIn.collectAsState(initial = false)

    if (!loggedIn) {
        val authVm: AuthViewModel = viewModel(factory = factory { AuthViewModel(session) })
        LoginScreen(authVm)
        return
    }

    val gateway: MonitorGateway = SessionMonitorGateway(session)
    val navController = rememberNavController()

    NavHost(navController = navController, startDestination = Routes.Monitors) {
        composable(Routes.Monitors) {
            val vm: MonitorsViewModel = viewModel(factory = factory { MonitorsViewModel(gateway) })
            MonitorsScreen(
                viewModel = vm,
                onAdd = { navController.navigate(Routes.Add) },
                onLogout = { vm.viewModelScope.launch { session.logout() } },
            )
        }
        composable(Routes.Add) {
            val vm: AddMonitorViewModel = viewModel(factory = factory { AddMonitorViewModel(gateway) })
            AddMonitorScreen(
                viewModel = vm,
                onDone = { navController.popBackStack() },
                onBack = { navController.popBackStack() },
            )
        }
    }
}
