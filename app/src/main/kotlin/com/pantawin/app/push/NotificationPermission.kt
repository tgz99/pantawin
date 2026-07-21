package com.pantawin.app.push

import android.Manifest
import android.app.NotificationManager
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.os.PowerManager
import android.provider.Settings
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.core.content.ContextCompat
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner

/**
 * Requests POST_NOTIFICATIONS on Android 13+ (spec M3). Requests once on
 * first composition; if denied, [DegradedBanner] tells the user push is off
 * while email alerts still work. Pre-33 devices have the permission
 * implicitly, so this is a no-op there.
 */
@Composable
fun rememberNotificationPermissionState(): NotificationPermissionState {
    val context = LocalContext.current
    var granted by remember { mutableStateOf(hasNotificationPermission(context)) }
    var asked by remember { mutableStateOf(false) }

    val launcher = rememberLauncherForActivityResult(
        ActivityResultContracts.RequestPermission(),
    ) { isGranted -> granted = isGranted }

    LaunchedEffect(Unit) {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU && !granted && !asked) {
            asked = true
            launcher.launch(Manifest.permission.POST_NOTIFICATIONS)
        }
    }

    return NotificationPermissionState(granted = granted)
}

data class NotificationPermissionState(val granted: Boolean)

fun hasNotificationPermission(context: Context): Boolean {
    if (Build.VERSION.SDK_INT < Build.VERSION_CODES.TIRAMISU) return true
    return ContextCompat.checkSelfPermission(
        context, Manifest.permission.POST_NOTIFICATIONS,
    ) == PackageManager.PERMISSION_GRANTED
}

@Composable
fun DegradedBanner(modifier: Modifier = Modifier) {
    Surface(
        color = MaterialTheme.colorScheme.secondaryContainer,
        modifier = modifier.fillMaxWidth(),
    ) {
        Text(
            "Push alerts are off — email alerts still active. Enable notifications in system settings to get instant downtime pushes.",
            style = MaterialTheme.typography.bodySmall,
            modifier = Modifier.padding(12.dp),
        )
    }
}

/**
 * Several OEMs (Samsung, Xiaomi, Oppo/Vivo) suspend an app's background FCM
 * delivery under battery optimization — the notification permission can be
 * granted and the server can send, yet nothing ever shows up. Exempting the
 * app is the actual fix for that class of "alerts don't show" reports.
 */
fun isIgnoringBatteryOptimizations(context: Context): Boolean {
    val pm = context.getSystemService(PowerManager::class.java) ?: return true
    return pm.isIgnoringBatteryOptimizations(context.packageName)
}

fun batteryOptimizationExemptionIntent(context: Context): Intent =
    Intent(
        Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS,
        Uri.parse("package:${context.packageName}"),
    )

/** Re-checked on every resume since the system settings screen has no direct callback. */
@Composable
fun rememberBatteryOptimizationState(): BatteryOptimizationState {
    val context = LocalContext.current
    var ignoring by remember { mutableStateOf(isIgnoringBatteryOptimizations(context)) }
    val lifecycleOwner = LocalLifecycleOwner.current
    DisposableEffect(lifecycleOwner) {
        val observer = LifecycleEventObserver { _, event ->
            if (event == Lifecycle.Event.ON_RESUME) {
                ignoring = isIgnoringBatteryOptimizations(context)
            }
        }
        lifecycleOwner.lifecycle.addObserver(observer)
        onDispose { lifecycleOwner.lifecycle.removeObserver(observer) }
    }
    return BatteryOptimizationState(ignoring)
}

data class BatteryOptimizationState(val ignoring: Boolean)

/**
 * Android 14+ requires the user to separately grant full-screen-intent use
 * (Settings.ACTION_MANAGE_APP_USE_FULL_SCREEN_INTENT) — without it, DOWN
 * alerts fall back to a plain heads-up instead of waking/locking over the
 * screen. Pre-14 this permission is granted automatically at install.
 */
fun canUseFullScreenIntent(context: Context): Boolean {
    if (Build.VERSION.SDK_INT < Build.VERSION_CODES.UPSIDE_DOWN_CAKE) return true
    val mgr = context.getSystemService(NotificationManager::class.java) ?: return false
    return mgr.canUseFullScreenIntent()
}

fun fullScreenIntentSettingsIntent(context: Context): Intent =
    Intent(
        Settings.ACTION_MANAGE_APP_USE_FULL_SCREEN_INTENT,
        Uri.parse("package:${context.packageName}"),
    )

@Composable
fun rememberFullScreenIntentState(): FullScreenIntentState {
    val context = LocalContext.current
    var allowed by remember { mutableStateOf(canUseFullScreenIntent(context)) }
    val lifecycleOwner = LocalLifecycleOwner.current
    DisposableEffect(lifecycleOwner) {
        val observer = LifecycleEventObserver { _, event ->
            if (event == Lifecycle.Event.ON_RESUME) {
                allowed = canUseFullScreenIntent(context)
            }
        }
        lifecycleOwner.lifecycle.addObserver(observer)
        onDispose { lifecycleOwner.lifecycle.removeObserver(observer) }
    }
    return FullScreenIntentState(allowed)
}

data class FullScreenIntentState(val allowed: Boolean)

/**
 * Prompts the user to exempt the app from battery optimization so downtime
 * pushes keep arriving in the background. Only shown once push is otherwise
 * working (permission granted) and the OS hasn't already exempted the app.
 */
@Composable
fun BatteryOptimizationBanner(onRequestExemption: () -> Unit, modifier: Modifier = Modifier) {
    Surface(
        color = MaterialTheme.colorScheme.secondaryContainer,
        modifier = modifier.fillMaxWidth(),
    ) {
        Row(
            modifier = Modifier.fillMaxWidth().padding(start = 12.dp),
        ) {
            Text(
                "Alerts may be delayed or missed on this device unless background activity is allowed.",
                style = MaterialTheme.typography.bodySmall,
                modifier = Modifier.padding(vertical = 12.dp).weight(1f),
            )
            TextButton(onClick = onRequestExemption) { Text("Fix") }
        }
    }
}
