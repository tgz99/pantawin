package com.pantawin.app.monitors

import androidx.compose.animation.animateColorAsState
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.Logout
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.CheckCircle
import androidx.compose.material.icons.filled.MonitorHeart
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material.icons.outlined.Delete
import androidx.compose.material.icons.outlined.Groups
import androidx.compose.material.icons.outlined.Info
import androidx.compose.material.icons.outlined.Key
import androidx.compose.material.icons.outlined.Language
import androidx.compose.material.icons.outlined.Pause
import androidx.compose.material.icons.outlined.Person
import androidx.compose.material.icons.outlined.PlayArrow
import androidx.compose.material3.CenterAlignedTopAppBar
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ElevatedCard
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ExtendedFloatingActionButton
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import coil3.compose.AsyncImage
import com.pantawin.app.push.DegradedBanner
import com.pantawin.app.ui.EmptyState
import com.pantawin.app.ui.ErrorState
import com.pantawin.app.ui.LoadingState
import com.pantawin.app.ui.StatusVisual
import com.pantawin.app.ui.theme.StatusDown
import com.pantawin.app.ui.theme.StatusUp
import com.pantawin.app.ui.visual
import com.pantawin.shared.model.MonitorState
import com.pantawin.shared.model.MonitorStatus

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun MonitorsScreen(
    viewModel: MonitorsViewModel,
    onAdd: () -> Unit,
    onLogout: () -> Unit,
    onOpen: (Long) -> Unit = {},
    onChangePassword: () -> Unit = {},
    onAbout: () -> Unit = {},
    showPushDegradedBanner: Boolean = false,
) {
    val state by viewModel.uiState.collectAsState()
    var menuOpen by remember { mutableStateOf(false) }

    Scaffold(
        topBar = {
            CenterAlignedTopAppBar(
                title = {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Icon(
                            Icons.Filled.MonitorHeart,
                            contentDescription = null,
                            tint = MaterialTheme.colorScheme.primary,
                        )
                        Text(
                            "Pantawin",
                            fontWeight = FontWeight.SemiBold,
                            modifier = Modifier.padding(start = 8.dp),
                        )
                    }
                },
                actions = {
                    // Overflow menu: settings-ish actions live here so the
                    // bar stays clean as items accrue (M5 adds About).
                    IconButton(onClick = { menuOpen = true }) {
                        Icon(Icons.Default.MoreVert, contentDescription = "Menu")
                    }
                    DropdownMenu(expanded = menuOpen, onDismissRequest = { menuOpen = false }) {
                        DropdownMenuItem(
                            text = { Text("Change password") },
                            leadingIcon = { Icon(Icons.Outlined.Key, contentDescription = null) },
                            onClick = {
                                menuOpen = false
                                onChangePassword()
                            },
                        )
                        DropdownMenuItem(
                            text = { Text("About") },
                            leadingIcon = { Icon(Icons.Outlined.Info, contentDescription = null) },
                            onClick = {
                                menuOpen = false
                                onAbout()
                            },
                        )
                        DropdownMenuItem(
                            text = { Text("Log out") },
                            leadingIcon = { Icon(Icons.AutoMirrored.Filled.Logout, contentDescription = null) },
                            onClick = {
                                menuOpen = false
                                onLogout()
                            },
                        )
                    }
                },
            )
        },
        floatingActionButton = {
            ExtendedFloatingActionButton(
                onClick = onAdd,
                icon = { Icon(Icons.Filled.Add, contentDescription = null) },
                text = { Text("Add monitor") },
            )
        },
    ) { padding ->
        Column(Modifier.fillMaxSize().padding(padding)) {
            if (showPushDegradedBanner) DegradedBanner()
            when (val s = state) {
                is MonitorsUiState.Loading -> LoadingState()
                is MonitorsUiState.Error -> ErrorState(s.message, onRetry = viewModel::refresh)
                is MonitorsUiState.Loaded ->
                    if (s.monitors.isEmpty()) {
                        EmptyState(
                            title = "No monitors yet",
                            subtitle = "Tap “Add monitor” and Pantawin will start watching it around the clock.",
                            icon = Icons.Filled.MonitorHeart,
                        )
                    } else {
                        // M6: two sections — personal (owner-only) first, then
                        // team (shared with every user).
                        val personal = s.monitors.filter { it.scope != "team" }
                        val team = s.monitors.filter { it.scope == "team" }
                        LazyColumn(
                            modifier = Modifier.fillMaxSize(),
                            contentPadding = androidx.compose.foundation.layout.PaddingValues(
                                start = 16.dp, end = 16.dp, top = 8.dp, bottom = 96.dp,
                            ),
                            verticalArrangement = Arrangement.spacedBy(12.dp),
                        ) {
                            item(key = "summary") { StatusSummaryCard(s.monitors) }
                            item(key = "header-personal") {
                                SectionHeader("Personal", Icons.Outlined.Person)
                            }
                            if (personal.isEmpty()) {
                                item(key = "empty-personal") {
                                    SectionHint("No personal monitors yet — only you would see them.")
                                }
                            }
                            items(personal, key = { it.id }) { m ->
                                MonitorCard(
                                    monitor = m,
                                    onOpen = { onOpen(m.id) },
                                    onPause = { viewModel.pause(m.id) },
                                    onResume = { viewModel.resume(m.id) },
                                    onDelete = { viewModel.delete(m.id) },
                                )
                            }
                            item(key = "header-team") {
                                SectionHeader("Team", Icons.Outlined.Groups)
                            }
                            if (team.isEmpty()) {
                                item(key = "empty-team") {
                                    SectionHint("No team monitors yet — everyone sees these and gets their alerts.")
                                }
                            }
                            items(team, key = { it.id }) { m ->
                                MonitorCard(
                                    monitor = m,
                                    onOpen = { onOpen(m.id) },
                                    onPause = { viewModel.pause(m.id) },
                                    onResume = { viewModel.resume(m.id) },
                                    onDelete = { viewModel.delete(m.id) },
                                )
                            }
                        }
                    }
            }
        }
    }
}

// Fleet health at a glance: green when everything is up, red when anything
// is down. Color animates on transitions so a recovery visibly "heals".
@Composable
private fun StatusSummaryCard(monitors: List<MonitorStatus>) {
    val down = monitors.count { it.status == MonitorState.DOWN }
    val up = monitors.count { it.status == MonitorState.UP }
    val paused = monitors.count { it.status == MonitorState.PAUSED }
    val allGood = down == 0

    val container by animateColorAsState(
        targetValue = if (allGood) StatusUp.copy(alpha = 0.14f) else StatusDown.copy(alpha = 0.14f),
        label = "summaryContainer",
    )
    val accent = if (allGood) StatusUp else StatusDown

    Surface(
        shape = MaterialTheme.shapes.extraLarge,
        color = container,
        modifier = Modifier.fillMaxWidth(),
    ) {
        Row(
            modifier = Modifier.padding(horizontal = 20.dp, vertical = 18.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Icon(
                if (allGood) Icons.Filled.CheckCircle else Icons.Filled.MonitorHeart,
                contentDescription = null,
                tint = accent,
                modifier = Modifier.size(36.dp),
            )
            Column(Modifier.padding(start = 16.dp)) {
                Text(
                    if (allGood) "All systems operational" else "$down monitor${if (down > 1) "s" else ""} down",
                    style = MaterialTheme.typography.titleMedium,
                    fontWeight = FontWeight.SemiBold,
                    color = accent,
                )
                Text(
                    buildString {
                        append("$up up")
                        if (down > 0) append(" · $down down")
                        if (paused > 0) append(" · $paused paused")
                    },
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
    }
}

// M6 section headers: Personal (owner-only) vs Team (shared) monitors.
@Composable
private fun SectionHeader(title: String, icon: ImageVector) {
    Row(
        verticalAlignment = Alignment.CenterVertically,
        modifier = Modifier.padding(top = 8.dp),
    ) {
        Icon(
            icon,
            contentDescription = null,
            tint = MaterialTheme.colorScheme.primary,
            modifier = Modifier.size(20.dp),
        )
        Text(
            title,
            style = MaterialTheme.typography.titleSmall,
            fontWeight = FontWeight.SemiBold,
            color = MaterialTheme.colorScheme.primary,
            modifier = Modifier.padding(start = 8.dp),
        )
    }
}

@Composable
private fun SectionHint(text: String) {
    Text(
        text,
        style = MaterialTheme.typography.bodySmall,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
        modifier = Modifier.padding(horizontal = 4.dp),
    )
}

@Composable
private fun MonitorCard(
    monitor: MonitorStatus,
    onOpen: () -> Unit,
    onPause: () -> Unit,
    onResume: () -> Unit,
    onDelete: () -> Unit,
) {
    val visual = monitor.status.visual()

    ElevatedCard(
        onClick = onOpen,
        shape = MaterialTheme.shapes.large,
        modifier = Modifier.fillMaxWidth(),
    ) {
        Row(
            modifier = Modifier.fillMaxWidth().padding(start = 16.dp, end = 4.dp, top = 14.dp, bottom = 14.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Favicon(monitor.url)
            Column(Modifier.weight(1f).padding(start = 14.dp, end = 4.dp)) {
                Text(
                    monitor.name,
                    style = MaterialTheme.typography.titleMedium,
                    fontWeight = FontWeight.Medium,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
                Text(
                    monitor.url.removePrefix("https://").removePrefix("http://"),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
                Row(
                    verticalAlignment = Alignment.CenterVertically,
                    modifier = Modifier.padding(top = 8.dp),
                ) {
                    StatusPill(visual)
                    monitor.responseTimeMs?.let {
                        Text(
                            "$it ms",
                            style = MaterialTheme.typography.labelMedium,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            modifier = Modifier.padding(start = 10.dp),
                        )
                    }
                }
            }
            if (monitor.status == MonitorState.PAUSED) {
                CardAction(Icons.Outlined.PlayArrow, "Resume", onResume)
            } else {
                CardAction(Icons.Outlined.Pause, "Pause", onPause)
            }
            CardAction(Icons.Outlined.Delete, "Delete", onDelete)
        }
    }
}

// Real site favicon via Google's public favicon service, with a globe
// fallback while loading / for hosts without one.
@Composable
private fun Favicon(url: String) {
    val host = url.removePrefix("https://").removePrefix("http://").substringBefore('/')
    var loaded by remember { mutableStateOf(false) }
    Surface(
        shape = MaterialTheme.shapes.medium,
        color = MaterialTheme.colorScheme.surfaceContainerHighest,
        modifier = Modifier.size(48.dp),
    ) {
        Box(contentAlignment = Alignment.Center) {
            if (!loaded) {
                Icon(
                    Icons.Outlined.Language,
                    contentDescription = null,
                    tint = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.size(24.dp),
                )
            }
            AsyncImage(
                model = "https://www.google.com/s2/favicons?domain=$host&sz=64",
                contentDescription = null,
                onSuccess = { loaded = true },
                modifier = Modifier.size(28.dp).clip(MaterialTheme.shapes.extraSmall),
            )
        }
    }
}

@Composable
private fun StatusPill(visual: StatusVisual) {
    Surface(
        shape = MaterialTheme.shapes.small,
        color = visual.color.copy(alpha = 0.14f),
    ) {
        Row(
            verticalAlignment = Alignment.CenterVertically,
            modifier = Modifier.padding(horizontal = 8.dp, vertical = 3.dp),
        ) {
            Icon(
                visual.icon,
                contentDescription = null,
                tint = visual.color,
                modifier = Modifier.size(14.dp),
            )
            Text(
                visual.label,
                style = MaterialTheme.typography.labelMedium,
                color = visual.color,
                modifier = Modifier.padding(start = 4.dp),
            )
        }
    }
}

@Composable
private fun CardAction(icon: ImageVector, label: String, onClick: () -> Unit) {
    IconButton(onClick = onClick) {
        Icon(
            icon,
            contentDescription = label,
            tint = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}
