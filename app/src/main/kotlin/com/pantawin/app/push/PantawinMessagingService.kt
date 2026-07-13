package com.pantawin.app.push

import com.google.firebase.messaging.FirebaseMessagingService
import com.google.firebase.messaging.RemoteMessage
import com.pantawin.app.PantawinApp
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch

/**
 * Receives FCM data messages and renders local notifications with the right
 * channel/deep-link (spec 6.4). The server sends data-only high-priority
 * messages so the client controls channel + sound. Dormant until a Firebase
 * project is configured (see PantawinApp).
 */
class PantawinMessagingService : FirebaseMessagingService() {

    override fun onNewToken(token: String) {
        // Register the new token with the backend under the current session.
        val app = application as? PantawinApp ?: return
        CoroutineScope(Dispatchers.IO).launch {
            runCatching { app.sessionManager.registerPushToken(token) }
        }
    }

    override fun onMessageReceived(message: RemoteMessage) {
        val data = message.data
        val isDown = data["status"] == "DOWN"
        val title = data["title"] ?: (if (isDown) "Monitor down" else "Monitor recovered")
        val body = data["body"] ?: ""
        Notifications.show(
            context = this,
            title = title,
            body = body,
            isDown = isDown,
            monitorId = data["monitor_id"],
        )
    }
}
