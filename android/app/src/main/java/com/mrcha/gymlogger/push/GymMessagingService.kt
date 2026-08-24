package com.mrcha.gymlogger.push

import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import com.google.firebase.messaging.FirebaseMessagingService
import com.google.firebase.messaging.RemoteMessage
import com.mrcha.gymlogger.MainActivity
import com.mrcha.gymlogger.Prefs
import com.mrcha.gymlogger.R
import com.mrcha.gymlogger.net.ApiClient
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch

class GymMessagingService : FirebaseMessagingService() {

    /**
     * Firebase hands out a new token on install, on app data clear, and
     * occasionally on its own schedule. The server has to hear about every one
     * of those, otherwise reminders quietly stop arriving and nothing looks broken.
     */
    override fun onNewToken(token: String) {
        val prefs = Prefs(this)
        if (!prefs.isConfigured) return

        CoroutineScope(Dispatchers.IO).launch {
            val api = ApiClient(prefs.baseUrl, prefs.authToken)
            api.registerDevice(token).onSuccess {
                prefs.registeredPushToken = token
            }
        }
    }

    override fun onMessageReceived(message: RemoteMessage) {
        // A notification-payload message is drawn by the system when the app is
        // backgrounded, so only the foreground case needs handling here.
        val title = message.notification?.title ?: message.data["title"] ?: return
        val body = message.notification?.body ?: message.data["body"].orEmpty()
        showNotification(this, title, body)
    }

    companion object {
        const val CHANNEL_ID = "gym_reminders"

        fun ensureChannel(context: Context) {
            val channel = NotificationChannel(
                CHANNEL_ID,
                "Training reminders",
                NotificationManager.IMPORTANCE_DEFAULT,
            ).apply {
                description = "Session reminders, rest-day nudges and rank changes"
            }
            context.getSystemService(NotificationManager::class.java)
                .createNotificationChannel(channel)
        }

        fun showNotification(context: Context, title: String, body: String) {
            ensureChannel(context)
            // Without a content intent the reminder is a dead end: tapping it
            // does nothing, which trains you to swipe reminders away.
            val open = PendingIntent.getActivity(
                context,
                0,
                Intent(context, MainActivity::class.java)
                    .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TOP),
                PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
            )

            val notification = NotificationCompat.Builder(context, CHANNEL_ID)
                .setSmallIcon(R.drawable.ic_notification)
                .setContentIntent(open)
                .setContentTitle(title)
                .setContentText(body)
                .setStyle(NotificationCompat.BigTextStyle().bigText(body))
                .setAutoCancel(true)
                .build()

            runCatching {
                NotificationManagerCompat.from(context)
                    .notify(title.hashCode(), notification)
            }
        }
    }
}
