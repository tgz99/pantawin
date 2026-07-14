package com.pantawin.app.navigation

import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.platform.LocalContext
import androidx.lifecycle.ViewModel
import androidx.lifecycle.ViewModelProvider
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.navigation.NavType
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.rememberNavController
import androidx.navigation.navArgument
import androidx.navigation.navDeepLink
import com.pantawin.app.PantawinApp
import com.pantawin.app.about.AboutScreen
import com.pantawin.app.data.SessionTeamGateway
import com.pantawin.app.data.TeamGateway
import com.pantawin.app.team.TeamMembersScreen
import com.pantawin.app.team.TeamMembersViewModel
import com.pantawin.app.team.TeamsListScreen
import com.pantawin.app.team.TeamsListViewModel
import com.pantawin.app.auth.AuthFlow
import com.pantawin.app.auth.AuthViewModel
import com.pantawin.app.auth.ChangePasswordScreen
import com.pantawin.app.auth.ChangePasswordViewModel
import com.pantawin.app.data.MonitorGateway
import com.pantawin.app.data.SessionManager
import com.pantawin.app.data.SessionMonitorGateway
import com.pantawin.app.monitors.AddMonitorScreen
import com.pantawin.app.monitors.AddMonitorViewModel
import com.pantawin.app.monitors.IncidentHistoryScreen
import com.pantawin.app.monitors.IncidentHistoryViewModel
import com.pantawin.app.monitors.MonitorDetailScreen
import com.pantawin.app.monitors.MonitorDetailViewModel
import com.pantawin.app.monitors.MonitorsScreen
import com.pantawin.app.monitors.MonitorsViewModel
import com.pantawin.app.push.rememberNotificationPermissionState
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.emitAll
import kotlinx.coroutines.flow.flow
import kotlinx.coroutines.launch

private object Routes {
    const val Monitors = "monitors"
    const val Add = "add"
    const val ChangePassword = "change-password"
    const val About = "about"
    const val Teams = "teams"
    const val TeamMembers = "teams/{id}/members/{name}"
    const val Detail = "monitor/{id}"
    const val Incidents = "monitor/{id}/incidents"

    fun detail(id: Long) = "monitor/$id"
    fun incidents(id: Long) = "monitor/$id/incidents"
    fun teamMembers(id: Long, name: String) =
        "teams/$id/members/${java.net.URLEncoder.encode(name, "UTF-8")}"
}

// savedStateHandle key: Add screen -> Monitors screen "a monitor was created".
private const val KeyMonitorAdded = "monitor_added"

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
        AuthFlow(authVm)
        return
    }

    val app = LocalContext.current.applicationContext as PantawinApp

    // Register this device's FCM token once per login (no-op while push is
    // dormant; onNewToken keeps it fresh afterwards).
    LaunchedEffect(Unit) { app.registerPushTokenIfEnabled() }

    // Ask for POST_NOTIFICATIONS only when push can actually deliver; if
    // denied, the dashboard shows the degraded banner (spec M3).
    val pushDegraded = if (app.pushEnabled) !rememberNotificationPermissionState().granted else false

    val gateway: MonitorGateway = SessionMonitorGateway(session)
    val teamGateway: TeamGateway = SessionTeamGateway(session)
    val navController = rememberNavController()

    // Live WebSocket feed (spec 6.4): cold flow that connects on collect and
    // reconnects while the dashboard ViewModel is alive. Stops when there is
    // no session (logout tears the screen down anyway).
    val realtimeEvents = remember {
        flow {
            while (true) {
                val token = session.currentAccessToken() ?: break
                runCatching { emitAll(app.realtimeClient.events(token)) }
                delay(5_000)
            }
        }
    }

    NavHost(navController = navController, startDestination = Routes.Monitors) {
        composable(Routes.Monitors) { entry ->
            val vm: MonitorsViewModel = viewModel(factory = factory { MonitorsViewModel(gateway, realtimeEvents) })
            // AddMonitorScreen signals a successful create through the saved
            // state of this back-stack entry; refetch so the new monitor
            // shows immediately on return.
            val monitorAdded by entry.savedStateHandle.getStateFlow(KeyMonitorAdded, false).collectAsState()
            LaunchedEffect(monitorAdded) {
                if (monitorAdded) {
                    vm.refresh()
                    entry.savedStateHandle[KeyMonitorAdded] = false
                }
            }
            MonitorsScreen(
                viewModel = vm,
                onAdd = { navController.navigate(Routes.Add) },
                onOpen = { id -> navController.navigate(Routes.detail(id)) },
                onChangePassword = { navController.navigate(Routes.ChangePassword) },
                onAbout = { navController.navigate(Routes.About) },
                onTeam = { navController.navigate(Routes.Teams) },
                onLogout = { vm.viewModelScope.launch { session.logout() } },
                showPushDegradedBanner = pushDegraded,
            )
        }
        composable(
            route = Routes.Detail,
            arguments = listOf(navArgument("id") { type = NavType.LongType }),
            // Notification taps land here: pantawin://monitor/{id}.
            deepLinks = listOf(navDeepLink { uriPattern = "pantawin://monitor/{id}" }),
        ) { entry ->
            val id = entry.arguments?.getLong("id") ?: return@composable
            val vm: MonitorDetailViewModel = viewModel(factory = factory { MonitorDetailViewModel(gateway, id) })
            MonitorDetailScreen(
                viewModel = vm,
                onBack = { navController.popBackStack() },
                onViewIncidents = { navController.navigate(Routes.incidents(id)) },
            )
        }
        composable(
            route = Routes.Incidents,
            arguments = listOf(navArgument("id") { type = NavType.LongType }),
        ) { entry ->
            val id = entry.arguments?.getLong("id") ?: return@composable
            val vm: IncidentHistoryViewModel = viewModel(factory = factory { IncidentHistoryViewModel(gateway, id) })
            IncidentHistoryScreen(
                viewModel = vm,
                onBack = { navController.popBackStack() },
            )
        }
        composable(Routes.About) {
            AboutScreen(onBack = { navController.popBackStack() })
        }
        composable(Routes.Teams) {
            val vm: TeamsListViewModel = viewModel(factory = factory { TeamsListViewModel(teamGateway) })
            TeamsListScreen(
                viewModel = vm,
                onBack = { navController.popBackStack() },
                onOpenTeam = { team -> navController.navigate(Routes.teamMembers(team.id, team.name)) },
            )
        }
        composable(
            route = Routes.TeamMembers,
            arguments = listOf(
                navArgument("id") { type = NavType.LongType },
                navArgument("name") { type = NavType.StringType },
            ),
        ) { entry ->
            val id = entry.arguments?.getLong("id") ?: return@composable
            val encodedName = entry.arguments?.getString("name").orEmpty()
            val name = java.net.URLDecoder.decode(encodedName, "UTF-8")
            val vm: TeamMembersViewModel = viewModel(factory = factory { TeamMembersViewModel(teamGateway, id) })
            TeamMembersScreen(viewModel = vm, teamName = name, onBack = { navController.popBackStack() })
        }
        composable(Routes.ChangePassword) {
            val vm: ChangePasswordViewModel = viewModel(factory = factory { ChangePasswordViewModel(session) })
            ChangePasswordScreen(
                viewModel = vm,
                onDone = { navController.popBackStack() },
                onBack = { navController.popBackStack() },
            )
        }
        composable(Routes.Add) {
            val vm: AddMonitorViewModel = viewModel(factory = factory { AddMonitorViewModel(gateway, teamGateway) })
            AddMonitorScreen(
                viewModel = vm,
                onDone = {
                    navController.previousBackStackEntry?.savedStateHandle?.set(KeyMonitorAdded, true)
                    navController.popBackStack()
                },
                onBack = { navController.popBackStack() },
            )
        }
    }
}
