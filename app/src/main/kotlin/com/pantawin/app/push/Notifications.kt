package com.pantawin.app.push

import android.app.NotificationChannel
import android.app.NotificationManager
import android.content.Context
import android.content.Intent
import android.media.AudioAttributes
import android.net.Uri
import android.provider.Settings
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import androidx.core.content.ContextCompat
import com.pantawin.app.R

/**
 * Notification channels + local notification building (spec 6.4, 6.6).
 * Two channels per the spec: "Downtime Alerts" (HIGH, distinct alert) and
 * "Recovery" (DEFAULT). The status-bar icon MUST be a white-on-transparent
 * vector (ic_stat_alert) — a colored icon renders as a blank square.
 */
object Notifications {
    // v2: alarm-grade sound. Channel settings are immutable after creation,
    // so upgrading the sound required a new channel id; the legacy id is
    // deleted so it doesn't linger in the system notification settings.
    const val CHANNEL_DOWN = "downtime_alerts_v2"
    private const val LEGACY_CHANNEL_DOWN = "downtime_alerts"
    const val CHANNEL_RECOVERY = "recovery"

    fun ensureChannels(context: Context) {
        val mgr = context.getSystemService(NotificationManager::class.java)
        mgr.deleteNotificationChannel(LEGACY_CHANNEL_DOWN)
        // The device's alarm tone played on the ALARM stream: loud, insistent,
        // and (unlike the notification stream) usually audible even when the
        // ringer is quiet — downtime must not be missable.
        val alarmAttributes = AudioAttributes.Builder()
            .setUsage(AudioAttributes.USAGE_ALARM)
            .setContentType(AudioAttributes.CONTENT_TYPE_SONIFICATION)
            .build()
        mgr.createNotificationChannel(
            NotificationChannel(CHANNEL_DOWN, "Downtime Alerts", NotificationManager.IMPORTANCE_HIGH).apply {
                description = "Critical alarm when a monitor goes down"
                enableVibration(true)
                vibrationPattern = longArrayOf(0, 400, 200, 400, 200, 600)
                setSound(Settings.System.DEFAULT_ALARM_ALERT_URI, alarmAttributes)
                // Honored only if the user grants Do-Not-Disturb access to the
                // app in system settings; a harmless no-op otherwise.
                setBypassDnd(true)
            },
        )
        mgr.createNotificationChannel(
            NotificationChannel(CHANNEL_RECOVERY, "Recovery", NotificationManager.IMPORTANCE_DEFAULT).apply {
                description = "Notifications when a monitor recovers"
            },
        )
    }

    fun show(
        context: Context,
        title: String,
        body: String,
        isDown: Boolean,
        monitorId: String?,
    ) {
        ensureChannels(context)

        val channel = if (isDown) CHANNEL_DOWN else CHANNEL_RECOVERY
        // Deep link into the monitor detail via pantawin://monitor/{id}.
        val intent = monitorId?.let {
            Intent(Intent.ACTION_VIEW, Uri.parse("pantawin://monitor/$it")).apply {
                setPackage(context.packageName)
            }
        }
        val pending = intent?.let {
            android.app.PendingIntent.getActivity(
                context, monitorId.hashCode(), it,
                android.app.PendingIntent.FLAG_IMMUTABLE or android.app.PendingIntent.FLAG_UPDATE_CURRENT,
            )
        }

        val accent = ContextCompat.getColor(
            context,
            if (isDown) R.color.status_down else R.color.status_up,
        )

        val notification = NotificationCompat.Builder(context, channel)
            .setSmallIcon(R.drawable.ic_stat_alert)
            .setColor(accent)
            .setContentTitle(title)
            .setContentText(body)
            .setAutoCancel(true)
            .setPriority(if (isDown) NotificationCompat.PRIORITY_HIGH else NotificationCompat.PRIORITY_DEFAULT)
            .setCategory(if (isDown) NotificationCompat.CATEGORY_ALARM else NotificationCompat.CATEGORY_STATUS)
            .apply { pending?.let { setContentIntent(it) } }
            .build()

        // POST_NOTIFICATIONS is checked by the caller / gracefully handled;
        // NotificationManagerCompat.notify no-ops without permission on 13+.
        if (NotificationManagerCompat.from(context).areNotificationsEnabled()) {
            NotificationManagerCompat.from(context).notify(monitorId?.hashCode() ?: 0, notification)
        }
    }
}
