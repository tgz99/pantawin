package com.pantawin.app.push

import android.app.NotificationChannel
import android.app.NotificationManager
import android.content.Context
import android.content.Intent
import android.media.AudioAttributes
import android.net.Uri
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
    // v3: custom alarm-grade WAV (was the system alarm tone in v2). Channel
    // settings are immutable after creation, so upgrading the sound requires
    // a new channel id each time; legacy ids are deleted so they don't linger
    // in the system notification settings.
    const val CHANNEL_DOWN = "downtime_alerts_v3"
    private const val LEGACY_CHANNEL_DOWN_V1 = "downtime_alerts"
    private const val LEGACY_CHANNEL_DOWN_V2 = "downtime_alerts_v2"
    const val CHANNEL_RECOVERY = "recovery_v2"
    private const val LEGACY_CHANNEL_RECOVERY_V1 = "recovery"

    fun ensureChannels(context: Context) {
        val mgr = context.getSystemService(NotificationManager::class.java)
        mgr.deleteNotificationChannel(LEGACY_CHANNEL_DOWN_V1)
        mgr.deleteNotificationChannel(LEGACY_CHANNEL_DOWN_V2)
        mgr.deleteNotificationChannel(LEGACY_CHANNEL_RECOVERY_V1)
        // Played on the ALARM stream: loud, insistent, and (unlike the
        // notification stream) still audible in Vibrate/Silent ringer modes —
        // downtime must not be missable.
        val alarmAttributes = AudioAttributes.Builder()
            .setUsage(AudioAttributes.USAGE_ALARM)
            .setContentType(AudioAttributes.CONTENT_TYPE_SONIFICATION)
            .build()
        val downSound = Uri.parse("android.resource://${context.packageName}/${R.raw.down}")
        mgr.createNotificationChannel(
            NotificationChannel(CHANNEL_DOWN, "Downtime Alerts", NotificationManager.IMPORTANCE_HIGH).apply {
                description = "Critical alarm when a monitor goes down"
                enableVibration(true)
                vibrationPattern = longArrayOf(0, 400, 200, 400, 200, 600)
                setSound(downSound, alarmAttributes)
                // Honored only if the user grants Do-Not-Disturb access to the
                // app in system settings; a harmless no-op otherwise.
                setBypassDnd(true)
            },
        )
        // Played on the NOTIFICATION stream (default), so it follows Ringer
        // volume and stays silent under Vibrate/Silent/DND — recovery is
        // lower severity and doesn't need to bypass either.
        val notificationAttributes = AudioAttributes.Builder()
            .setUsage(AudioAttributes.USAGE_NOTIFICATION)
            .setContentType(AudioAttributes.CONTENT_TYPE_SONIFICATION)
            .build()
        val upSound = Uri.parse("android.resource://${context.packageName}/${R.raw.up}")
        mgr.createNotificationChannel(
            NotificationChannel(CHANNEL_RECOVERY, "Recovery", NotificationManager.IMPORTANCE_DEFAULT).apply {
                description = "Notifications when a monitor recovers"
                setSound(upSound, notificationAttributes)
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
        // Deep link into the monitor detail via pantawin://monitor/{id}. Only
        // fires when the user taps the notification (or the email CTA opens
        // this same route) — never launches the app on its own.
        val intent = monitorId?.let {
            Intent(Intent.ACTION_VIEW, Uri.parse("pantawin://monitor/$it")).apply {
                setPackage(context.packageName)
                addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
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

        val builder = NotificationCompat.Builder(context, channel)
            .setSmallIcon(R.drawable.ic_stat_alert)
            .setColor(accent)
            .setContentTitle(title)
            .setContentText(body)
            .setAutoCancel(true)
            .setPriority(if (isDown) NotificationCompat.PRIORITY_HIGH else NotificationCompat.PRIORITY_DEFAULT)
            .setCategory(if (isDown) NotificationCompat.CATEGORY_ALARM else NotificationCompat.CATEGORY_STATUS)
            .setVisibility(NotificationCompat.VISIBILITY_PUBLIC)
            .apply { pending?.let { setContentIntent(it) } }

        val notification = builder.build()

        // POST_NOTIFICATIONS is checked by the caller / gracefully handled;
        // NotificationManagerCompat.notify no-ops without permission on 13+.
        if (NotificationManagerCompat.from(context).areNotificationsEnabled()) {
            NotificationManagerCompat.from(context).notify(monitorId?.hashCode() ?: 0, notification)
        }
    }
}
