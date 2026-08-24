package com.mrcha.gymlogger

import android.Manifest
import android.content.Intent
import android.os.Build
import android.os.Bundle
import android.speech.RecognizerIntent
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.result.contract.ActivityResultContracts
import androidx.activity.viewModels
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.google.firebase.FirebaseApp
import com.google.firebase.messaging.FirebaseMessaging
import com.mrcha.gymlogger.net.ApiClient
import com.mrcha.gymlogger.net.LogResult
import com.mrcha.gymlogger.net.Rank
import com.mrcha.gymlogger.net.Recommendation
import com.mrcha.gymlogger.push.GymMessagingService
import com.mrcha.gymlogger.ui.GymApp
import com.mrcha.gymlogger.ui.GymLoggerTheme
import kotlinx.coroutines.launch
import java.util.Locale

class MainViewModel : ViewModel() {
    var draft by mutableStateOf("")
    var busy by mutableStateOf(false)
    var lastResult by mutableStateOf<LogResult?>(null)
    var rank by mutableStateOf<Rank?>(null)
    var next by mutableStateOf<Recommendation?>(null)
    var error by mutableStateOf<String?>(null)
    var showSettings by mutableStateOf(false)

    private var api: ApiClient? = null

    fun configure(prefs: Prefs) {
        api = if (prefs.isConfigured) ApiClient(prefs.baseUrl, prefs.authToken) else null
    }

    fun refresh() {
        val client = api ?: return
        viewModelScope.launch {
            client.rank().onSuccess { rank = it }
            client.next().onSuccess { next = it }
        }
    }

    fun submit() {
        val client = api ?: run {
            error = "Set the server address and token first"
            return
        }
        val text = draft.trim()
        if (text.isEmpty() || busy) return

        busy = true
        error = null
        viewModelScope.launch {
            client.log(text)
                .onSuccess { result ->
                    lastResult = result
                    // Only clear the input once the session is actually stored.
                    // A parse that needs confirmation keeps the text so it can
                    // be corrected and resent rather than retyped.
                    if (result.sessionId != 0L) draft = ""
                    result.rank?.let { rank = it }
                    refresh()
                }
                .onFailure { error = it.message ?: "Could not reach the server" }
            busy = false
        }
    }

    fun confirmPending(pendingId: Long) {
        val client = api ?: return
        busy = true
        viewModelScope.launch {
            client.confirm(pendingId)
                .onSuccess {
                    lastResult = it
                    draft = ""
                    refresh()
                }
                .onFailure { error = it.message ?: "Could not confirm" }
            busy = false
        }
    }
}

class MainActivity : ComponentActivity() {

    // viewModels() rather than a plain instance: a rotation or a trip through
    // the recogniser recreates the activity, and a lost draft mid-session is
    // the one thing this app must never do.
    private val vm: MainViewModel by viewModels()
    private lateinit var prefs: Prefs

    private val requestNotifications =
        registerForActivityResult(ActivityResultContracts.RequestPermission()) { }

    // Speech input goes through the system recogniser rather than a bundled
    // model, which keeps the app small and means it works with whatever
    // keyboard and language pack are already installed.
    private val speech =
        registerForActivityResult(ActivityResultContracts.StartActivityForResult()) { result ->
            val spoken = result.data
                ?.getStringArrayListExtra(RecognizerIntent.EXTRA_RESULTS)
                ?.firstOrNull()
                ?: return@registerForActivityResult
            vm.draft = if (vm.draft.isBlank()) spoken else "${vm.draft.trimEnd()}; $spoken"
        }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        prefs = Prefs(this)
        vm.configure(prefs)

        GymMessagingService.ensureChannel(this)
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            requestNotifications.launch(Manifest.permission.POST_NOTIFICATIONS)
        }
        registerPushToken()

        setContent {
            GymLoggerTheme {
                GymApp(
                    vm = vm,
                    prefs = prefs,
                    onMic = { startSpeech() },
                    onSettingsSaved = {
                        vm.configure(prefs)
                        registerPushToken()
                        vm.refresh()
                    },
                )
            }
        }

        vm.refresh()
    }

    private fun startSpeech() {
        val intent = Intent(RecognizerIntent.ACTION_RECOGNIZE_SPEECH).apply {
            putExtra(
                RecognizerIntent.EXTRA_LANGUAGE_MODEL,
                RecognizerIntent.LANGUAGE_MODEL_FREE_FORM,
            )
            putExtra(RecognizerIntent.EXTRA_LANGUAGE, Locale.getDefault())
            putExtra(RecognizerIntent.EXTRA_PROMPT, "Say the set")
        }
        runCatching { speech.launch(intent) }
            .onFailure { vm.error = "No speech recogniser installed on this phone" }
    }

    /**
     * Push tokens are re-checked on every launch, not just on rotation. A token
     * that changed while the app was closed would otherwise never reach the
     * server, and reminders would stop with no visible symptom.
     */
    private fun registerPushToken() {
        if (!prefs.isConfigured) return
        // With no google-services.json there is no default FirebaseApp, and
        // getInstance() throws rather than returning null. Push is optional;
        // taking the launch down with it is not.
        if (FirebaseApp.getApps(this).isEmpty()) return
        FirebaseMessaging.getInstance().token.addOnSuccessListener { token ->
            if (token == prefs.registeredPushToken) return@addOnSuccessListener
            val api = ApiClient(prefs.baseUrl, prefs.authToken)
            vm.viewModelScope.launch {
                api.registerDevice(token).onSuccess { prefs.registeredPushToken = token }
            }
        }
    }
}
