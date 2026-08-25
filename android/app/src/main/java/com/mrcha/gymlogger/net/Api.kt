package com.mrcha.gymlogger.net

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.serialization.SerialName
import kotlinx.serialization.encodeToString
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import java.util.concurrent.TimeUnit

// Wire types. Only the fields the UI actually renders are declared;
// ignoreUnknownKeys means the server can add more without breaking the app.

@Serializable
data class LiftEntry(
    val name: String = "",
    val display: String = "",
    @SerialName("top_set_today") val topSetToday: String = "",
    @SerialName("previous_top_set") val previousTopSet: String = "",
    @SerialName("is_baseline") val isBaseline: Boolean = false,
    @SerialName("is_weight_pr") val isWeightPR: Boolean = false,
    @SerialName("is_rep_pr") val isRepPR: Boolean = false,
    @SerialName("volume_today_kg") val volumeTodayKg: Double = 0.0,
)

@Serializable
data class Flag(
    val type: String = "",
    val detail: String = "",
    val priority: Int = 0,
)

@Serializable
data class SessionContext(
    val date: String = "",
    val flags: List<Flag> = emptyList(),
    @SerialName("lift_history") val liftHistory: List<LiftEntry> = emptyList(),
    @SerialName("rank_delta") val rankDelta: String = "",
)

@Serializable
data class Attributes(
    val might: Double = 0.0,
    val dominion: Double = 0.0,
    val frame: Double = 0.0,
    val vigor: Double = 0.0,
    val discipline: Double = 0.0,
    val mastery: Double = 0.0,
)

@Serializable
data class PatternScore(
    val pattern: String = "",
    val name: String = "",
    val score: Double = 0.0,
    val status: String = "",
    @SerialName("source_lift") val sourceLift: String = "",
    @SerialName("ref_kg") val refKg: Double = 0.0,
    val imputed: Boolean = false,
    val capped: Boolean = false,
)

@Serializable
data class Gate(
    val name: String = "",
    val value: Double = 0.0,
    val threshold: Double = 0.0,
    val pass: Boolean = false,
    val fix: String = "",
)

@Serializable
data class BerserkStatus(
    val qualified: Boolean = false,
    val gates: List<Gate> = emptyList(),
    @SerialName("patterns_verified") val patternsVerified: Int = 0,
    @SerialName("min_pattern") val minPattern: Double = 0.0,
    @SerialName("min_pattern_name") val minPatternName: String = "",
    val failing: Int = 0,
    val summary: String = "",
    val note: String = "",
)

@Serializable
data class Blood(
    val total: Double = 0.0,
    @SerialName("tier_name") val tierName: String = "",
    @SerialName("next_tier") val nextTier: String = "",
    @SerialName("to_next") val toNext: Double = 0.0,
    val progress: Double = 0.0,
    @SerialName("last_30d") val last30d: Double = 0.0,
)

@Serializable
data class Journey(
    val days: Int = 0,
    val sessions: Int = 0,
    @SerialName("rs_gain") val rsGain: Double = 0.0,
)

@Serializable
data class Rank(
    val rs: Double = 0.0,
    val rank: String = "",
    @SerialName("rank_index") val rankIndex: Int = 0,
    @SerialName("eligible_index") val eligibleIndex: Int = 0,
    val attributes: Attributes = Attributes(),
    val patterns: List<PatternScore> = emptyList(),
    val berserk: BerserkStatus = BerserkStatus(),
    val blood: Blood = Blood(),
    @SerialName("next_rank") val nextRank: String = "",
    @SerialName("to_next") val toNext: Double = 0.0,
    @SerialName("band_progress") val bandProgress: Double = 0.0,
    // Erratum 1: at the Berserk boundary a composite score is actively
    // misleading, so the server tells the client to render gates instead.
    @SerialName("show_gates") val showGates: Boolean = false,
    @SerialName("threat_level") val threatLevel: Double = 100.0,
    val confidence: Double = 0.0,
    val journey: Journey = Journey(),
    @SerialName("weak_link") val weakLink: String = "",
    val notes: List<String> = emptyList(),
)

@Serializable
data class LogResult(
    @SerialName("session_id") val sessionId: Long = 0,
    @SerialName("pending_id") val pendingId: Long = 0,
    @SerialName("needs_confirmation") val needsConfirmation: String = "",
    val context: SessionContext? = null,
    val rank: Rank? = null,
    val reply: String = "",
    val repairs: List<String> = emptyList(),
)

@Serializable
data class Recommendation(
    val kind: String = "",
    @SerialName("session_name") val sessionName: String = "",
    val groups: List<String> = emptyList(),
    val reason: String = "",
    @SerialName("days_since_last") val daysSinceLast: Int = 0,
)

@Serializable
private data class LogRequest(val text: String)

@Serializable
private data class DeviceRequest(val token: String, val platform: String = "android")

class ApiClient(
    private val baseUrl: String,
    private val authToken: String,
) {
    private val json = Json { ignoreUnknownKeys = true; encodeDefaults = true }

    // The log endpoint waits on two model calls, so the read timeout is
    // deliberately long. The connect timeout stays short so an unreachable
    // tunnel fails fast instead of looking like a slow session.
    private val http = OkHttpClient.Builder()
        .connectTimeout(10, TimeUnit.SECONDS)
        .readTimeout(120, TimeUnit.SECONDS)
        .build()

    private val jsonMedia = "application/json; charset=utf-8".toMediaType()

    suspend fun log(text: String): Result<LogResult> = post("/v1/log", LogRequest(text))

    suspend fun registerDevice(token: String): Result<Unit> =
        post<DeviceRequest, Unit>("/v1/device", DeviceRequest(token)).map { }

    suspend fun rank(): Result<Rank> = get("/v1/rank")

    suspend fun next(): Result<Recommendation> = get("/v1/next")

    suspend fun confirm(pendingId: Long): Result<LogResult> =
        rawPost("/v1/pending/$pendingId/confirm", "")

    private suspend inline fun <reified Req, reified Res> post(path: String, body: Req): Result<Res> =
        rawPost(path, json.encodeToString(body))

    private suspend inline fun <reified Res> rawPost(path: String, body: String): Result<Res> =
        withContext(Dispatchers.IO) {
            runCatching {
                val req = Request.Builder()
                    .url(baseUrl.trimEnd('/') + path)
                    .header("Authorization", "Bearer $authToken")
                    .post(body.toRequestBody(jsonMedia))
                    .build()
                http.newCall(req).execute().use { resp ->
                    val text = resp.body?.string().orEmpty()
                    if (!resp.isSuccessful) error(serverMessage(resp.code, text))
                    if (Res::class == Unit::class) Unit as Res
                    else json.decodeFromString<Res>(text)
                }
            }
        }

    private suspend inline fun <reified Res> get(path: String): Result<Res> =
        withContext(Dispatchers.IO) {
            runCatching {
                val req = Request.Builder()
                    .url(baseUrl.trimEnd('/') + path)
                    .header("Authorization", "Bearer $authToken")
                    .get()
                    .build()
                http.newCall(req).execute().use { resp ->
                    val text = resp.body?.string().orEmpty()
                    if (!resp.isSuccessful) error(serverMessage(resp.code, text))
                    json.decodeFromString<Res>(text)
                }
            }
        }

    // Surfaces the server's own error text rather than a bare status code,
    // which is the difference between "something broke" and "your token is wrong".
    fun serverMessage(code: Int, body: String): String {
        val detail = runCatching {
            json.decodeFromString<Map<String, String>>(body)["error"]
        }.getOrNull()
        return when {
            code == 401 -> "Unauthorized: check the auth token in settings"
            detail != null -> "Server error $code: $detail"
            else -> "Server error $code"
        }
    }
}
