package com.mrcha.gymlogger

import android.content.Context
import android.content.SharedPreferences

/**
 * Server address and auth token, stored on the device.
 *
 * The token is a shared secret for a single-user service behind a Cloudflare
 * Tunnel, not a per-user credential, so plain SharedPreferences is proportionate.
 * If this ever grows past one lifter, move it to EncryptedSharedPreferences.
 */
class Prefs(context: Context) {
    private val sp: SharedPreferences =
        context.getSharedPreferences("gymlogger", Context.MODE_PRIVATE)

    var baseUrl: String
        get() = sp.getString(KEY_URL, "") ?: ""
        set(v) = sp.edit().putString(KEY_URL, v.trim()).apply()

    var authToken: String
        get() = sp.getString(KEY_TOKEN, "") ?: ""
        set(v) = sp.edit().putString(KEY_TOKEN, v.trim()).apply()

    /** The last FCM token handed to the server, so registration is not re-sent every launch. */
    var registeredPushToken: String
        get() = sp.getString(KEY_PUSH, "") ?: ""
        set(v) = sp.edit().putString(KEY_PUSH, v).apply()

    val isConfigured: Boolean
        get() = baseUrl.isNotBlank() && authToken.isNotBlank()

    companion object {
        private const val KEY_URL = "base_url"
        private const val KEY_TOKEN = "auth_token"
        private const val KEY_PUSH = "push_token"
    }
}
