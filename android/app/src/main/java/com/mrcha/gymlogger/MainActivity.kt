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
import com.mrcha.gymlogger.net.LiftDetail
import com.mrcha.gymlogger.net.LiftSummary
import com.mrcha.gymlogger.net.LogResult
import com.mrcha.gymlogger.net.MuscleReport
import com.mrcha.gymlogger.net.Profile
import com.mrcha.gymlogger.net.Rank
import com.mrcha.gymlogger.net.Recommendation
import com.mrcha.gymlogger.push.GymMessagingService
import com.mrcha.gymlogger.ui.ConnectionState
import com.mrcha.gymlogger.ui.GymApp
import com.mrcha.gymlogger.ui.Rail
import com.mrcha.gymlogger.ui.LibraryState
import com.mrcha.gymlogger.ui.GymLoggerTheme
import kotlinx.coroutines.launch
import java.util.Locale

class MainViewModel : ViewModel() {
    var draft by mutableStateOf("")
    var busy by mutableStateOf(false)
    var lastResult by mutableStateOf<LogResult?>(null)
    var rank by mutableStateOf<Rank?>(null)
    var next by mutableStateOf<Recommendation?>(null)
    var muscles by mutableStateOf<MuscleReport?>(null)
    var error by mutableStateOf<String?>(null)
    var showSettings by mutableStateOf(false)
    var rail by mutableStateOf(Rail.Log)
    var showVerdict by mutableStateOf(false)
    var lifts by mutableStateOf<List<LiftSummary>>(emptyList())
    var liftDetail by mutableStateOf<LiftDetail?>(null)
    var showLiftDetail by mutableStateOf(false)

    /**
     * Whether the phone can reach the service.
     *
     * Tracked explicitly because the alternative is an empty page, which reads
     * identically to "you have not trained yet" and to "the build is broken".
     */
    var connection by mutableStateOf(ConnectionState.Unknown)
    var showLibrary by mutableStateOf(false)
    var showProfile by mutableStateOf(false)
    var profile by mutableStateOf<Profile?>(null)
    var library by mutableStateOf(LibraryState())

    /** Debounce token: only the newest search is allowed to publish results. */
    private var searchSeq = 0

    private var api: ApiClient? = null

    fun configure(prefs: Prefs) {
        api = if (prefs.isConfigured) ApiClient(prefs.baseUrl, prefs.authToken) else null
    }

    /**
     * Media lives behind the same bearer token as every other route, because
     * the service is on the public internet and an unauthenticated file route
     * is a file route anyone can walk.
     */
    fun mediaUrl(kind: String, file: String): String =
        api?.mediaUrl(kind, file) ?: ""

    fun authHeader(): String = api?.bearer ?: ""

    fun refresh() {
        val client = api ?: run {
            connection = ConnectionState.NotConfigured
            return
        }
        viewModelScope.launch {
            // Rank is the probe: if it fails, the other two will too, and one
            // banner is more useful than three silent empties.
            client.rank()
                .onSuccess {
                    rank = it
                    connection = ConnectionState.Ok
                }
                .onFailure {
                    connection = ConnectionState.Unreachable
                    error = it.message
                }
            if (connection != ConnectionState.Ok) return@launch
            client.next().onSuccess { next = it }
            client.muscles().onSuccess { muscles = it }
        }
    }

    // ---------- profile ----------

    fun openProfile() {
        showProfile = true
        loadProfile()
    }

    private fun loadProfile() {
        val client = api ?: return
        viewModelScope.launch {
            client.profile()
                .onSuccess { profile = it }
                .onFailure { error = it.message }
        }
    }

    /**
     * Every profile save reloads the profile AND the rank.
     *
     * Entering a real bodyweight changes lean mass, which changes every
     * strength reference, which changes MIGHT and therefore the rank. Leaving a
     * stale rank on screen after that would make the app look like it ignored
     * the input.
     */
    private fun afterProfileWrite() {
        loadProfile()
        refresh()
    }

    fun saveProfile(height: Double?, sex: String?, months: Double?, vo2: Double?, goal: String?) {
        val client = api ?: return
        busy = true
        viewModelScope.launch {
            client.saveProfile(height, sex, months, vo2, goalProfile = goal)
                .onSuccess { afterProfileWrite() }
                .onFailure { error = it.message }
            busy = false
        }
    }

    fun saveBody(weightKg: Double, bodyfat: Double?) {
        val client = api ?: return
        busy = true
        viewModelScope.launch {
            client.saveBody(weightKg, bodyfat, if (bodyfat != null) "caliper" else null)
                .onSuccess { afterProfileWrite() }
                .onFailure { error = it.message }
            busy = false
        }
    }

    fun saveBodyTape(weightKg: Double, neck: Double, waist: Double, hip: Double?) {
        val client = api ?: return
        busy = true
        viewModelScope.launch {
            client.saveBodyTape(weightKg, neck, waist, hip)
                .onSuccess {
                    // Report the derived number: a tape entry is the one case
                    // where the user cannot predict what they just saved.
                    error = "Body fat ${"%.1f".format(it.bodyfatPct)}% recorded"
                    afterProfileWrite()
                }
                .onFailure { error = it.message }
            busy = false
        }
    }

    fun saveClaim(pattern: String, e1rm: Double, lift: String) {
        val client = api ?: return
        viewModelScope.launch {
            client.saveClaims(listOf(Triple(pattern, e1rm, lift)))
                .onSuccess { afterProfileWrite() }
                .onFailure { error = it.message }
        }
    }

    fun toggleSkill(skill: String, unlocked: Boolean) {
        val client = api ?: return
        viewModelScope.launch {
            client.saveSkill(skill, unlocked)
                .onSuccess { afterProfileWrite() }
                .onFailure { error = it.message }
        }
    }

    // ---------- lifts ----------

    fun loadLifts() {
        val client = api ?: return
        viewModelScope.launch {
            client.lifts()
                .onSuccess { lifts = it.lifts }
                .onFailure { error = it.message }
        }
    }

    fun openLift(key: String) {
        val client = api ?: return
        // Cleared first so the previous lift's chart does not sit on screen
        // under the new title while the request is in flight.
        liftDetail = null
        showLiftDetail = true
        viewModelScope.launch {
            client.lift(key)
                .onSuccess { liftDetail = it }
                .onFailure {
                    error = it.message
                    showLiftDetail = false
                }
        }
    }

    // ---------- exercise library ----------

    fun openLibrary() {
        showLibrary = true
        if (library.facets == null) {
            val client = api ?: return
            viewModelScope.launch {
                client.facets().onSuccess { library = library.copy(facets = it) }
            }
        }
        if (library.exercises.isEmpty()) searchLibrary()
    }

    fun setLibraryQuery(q: String) {
        library = library.copy(query = q)
        searchLibrary()
    }

    fun setLibraryFilter(equipment: String, target: String) {
        library = library.copy(equipment = equipment, target = target)
        searchLibrary()
    }

    /**
     * Runs a search, replacing the current page.
     *
     * Typing fires this per keystroke, so a sequence number decides who wins:
     * a slow request for "inc" must not overwrite the results for "incline"
     * just because it landed later.
     */
    private fun searchLibrary() {
        val client = api ?: return
        val seq = ++searchSeq
        val q = library
        library = q.copy(loading = true)
        viewModelScope.launch {
            kotlinx.coroutines.delay(250) // debounce a burst of keystrokes
            if (seq != searchSeq) return@launch
            client.exercises(
                query = q.query, equipment = q.equipment, target = q.target, offset = 0,
            ).onSuccess {
                if (seq != searchSeq) return@onSuccess
                library = library.copy(
                    exercises = it.exercises, total = it.total, loading = false,
                )
            }.onFailure {
                if (seq == searchSeq) library = library.copy(loading = false)
                error = it.message
            }
        }
    }

    fun loadMoreExercises() {
        val client = api ?: return
        val cur = library
        if (cur.loading || cur.exercises.size >= cur.total) return
        val seq = searchSeq
        library = cur.copy(loading = true)
        viewModelScope.launch {
            client.exercises(
                query = cur.query, equipment = cur.equipment, target = cur.target,
                offset = cur.exercises.size,
            ).onSuccess {
                // A filter change while this was in flight invalidates the page.
                if (seq != searchSeq) return@onSuccess
                library = library.copy(
                    exercises = library.exercises + it.exercises,
                    total = it.total,
                    loading = false,
                )
            }.onFailure {
                library = library.copy(loading = false)
            }
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
                    // The verdict takes the screen. It is the answer to the
                    // thing the user just did, and burying it under the input
                    // box is what made it easy to miss.
                    showVerdict = true
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
                    showVerdict = true
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
