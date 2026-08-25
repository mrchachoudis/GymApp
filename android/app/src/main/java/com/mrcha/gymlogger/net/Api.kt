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
    // Omitted by the server above 8 reps, where Epley stops meaning anything,
    // so this is genuinely nullable rather than defaulted to zero.
    @SerialName("est_1rm_today") val est1RMToday: Double? = null,
    @SerialName("est_1rm_previous") val est1RMPrevious: Double? = null,
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
data class MuscleVolumeDto(
    val group: String = "",
    val name: String = "",
    val sets: Double = 0.0,
    @SerialName("raw_sets") val rawSets: Int = 0,
    @SerialName("last_trained") val lastTrained: String = "",
    @SerialName("days_since") val daysSince: Int = 0,
)

@Serializable
data class MuscleReport(
    val from: String = "",
    val to: String = "",
    val volumes: List<MuscleVolumeDto> = emptyList(),
    @SerialName("total_sets") val totalSets: Int = 0,
    // Exercises the server could not map to a muscle group. Shown rather than
    // hidden: anything here is work the user did that no group was credited for.
    val unmatched: List<String> = emptyList(),
    val neglected: String = "",
)

@Serializable
data class Exercise(
    val id: String = "",
    val name: String = "",
    @SerialName("body_part") val bodyPart: String = "",
    val equipment: String = "",
    val target: String = "",
    val secondary: List<String> = emptyList(),
    val steps: List<String> = emptyList(),
    val image: String = "",
    val animation: String = "",
)

@Serializable
data class ExercisePage(
    val total: Int = 0,
    val offset: Int = 0,
    val exercises: List<Exercise> = emptyList(),
)

@Serializable
data class Facet(val value: String = "", val count: Int = 0)

@Serializable
data class Facets(
    @SerialName("body_parts") val bodyParts: List<Facet> = emptyList(),
    val equipment: List<Facet> = emptyList(),
    val targets: List<Facet> = emptyList(),
)

@Serializable
data class PatternRef(
    val pattern: String = "",
    val name: String = "",
    @SerialName("ref_kg") val refKg: Double = 0.0,
)

@Serializable
data class PatternClaim(
    val pattern: String = "",
    val name: String = "",
    @SerialName("e1rm_kg") val e1rmKg: Double = 0.0,
    val lift: String = "",
)

@Serializable
data class SkillState(val skill: String = "", val unlocked: Boolean = false)

@Serializable
data class Profile(
    @SerialName("height_cm") val heightCm: Double = 0.0,
    @SerialName("bodyweight_kg") val bodyweightKg: Double = 0.0,
    @SerialName("bodyfat_pct") val bodyfatPct: Double = 0.0,
    @SerialName("bf_source") val bfSource: String = "",
    val sex: String = "male",
    @SerialName("training_months") val trainingMonths: Double = 0.0,
    @SerialName("vo2max_est") val vo2maxEst: Double = 0.0,
    @SerialName("avg_session_minutes") val sessionMinutes: Double = 0.0,
    @SerialName("goal_profile") val goalProfile: String = "balanced",
    @SerialName("lbm_kg") val lbmKg: Double = 0.0,
    @SerialName("ffmi_adj") val ffmiAdj: Double = 0.0,
    val estimated: Boolean = false,
    val frozen: Boolean = false,
    // Inputs costing score right now purely because nothing was entered.
    val missing: List<String> = emptyList(),
    val claims: List<PatternClaim> = emptyList(),
    val skills: List<SkillState> = emptyList(),
    val references: List<PatternRef> = emptyList(),
)

@Serializable
data class BodyResult(
    val status: String = "",
    val date: String = "",
    @SerialName("bodyfat_pct") val bodyfatPct: Double = 0.0,
    @SerialName("bf_source") val bfSource: String = "",
)

@Serializable
private data class ProfileUpdate(
    @SerialName("height_cm") val heightCm: Double? = null,
    val sex: String? = null,
    @SerialName("training_months") val trainingMonths: Double? = null,
    @SerialName("vo2max_est") val vo2maxEst: Double? = null,
    @SerialName("avg_session_minutes") val sessionMinutes: Double? = null,
    @SerialName("goal_profile") val goalProfile: String? = null,
)

@Serializable
private data class BodyUpdate(
    @SerialName("bodyweight_kg") val bodyweightKg: Double,
    @SerialName("bodyfat_pct") val bodyfatPct: Double? = null,
    @SerialName("bf_source") val bfSource: String? = null,
    @SerialName("neck_cm") val neckCm: Double? = null,
    @SerialName("waist_cm") val waistCm: Double? = null,
    @SerialName("hip_cm") val hipCm: Double? = null,
)

@Serializable
private data class ClaimIn(
    val pattern: String,
    @SerialName("e1rm_kg") val e1rmKg: Double,
    val lift: String = "",
)

@Serializable
private data class ClaimsUpdate(val claims: List<ClaimIn>)

@Serializable
private data class SkillsUpdate(val skills: Map<String, Boolean>)

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

private fun String.encodeParam(): String =
    java.net.URLEncoder.encode(this, "UTF-8")

class ApiClient(
    private val baseUrl: String,
    private val authToken: String,
) {
    // explicitNulls = false matters for the profile: the server treats an absent
    // field as "leave this setting alone", so a partial save must omit the rest
    // rather than send nulls that would read as an instruction to clear them.
    private val json = Json {
        ignoreUnknownKeys = true
        encodeDefaults = true
        explicitNulls = false
    }

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

    suspend fun muscles(days: Int = 7): Result<MuscleReport> = get("/v1/muscles?days=$days")

    suspend fun exercises(
        query: String = "",
        equipment: String = "",
        target: String = "",
        limit: Int = 50,
        offset: Int = 0,
    ): Result<ExercisePage> {
        val params = buildList {
            if (query.isNotBlank()) add("q=" + query.encodeParam())
            if (equipment.isNotBlank()) add("equipment=" + equipment.encodeParam())
            if (target.isNotBlank()) add("target=" + target.encodeParam())
            add("limit=$limit")
            add("offset=$offset")
        }
        return get("/v1/exercises?" + params.joinToString("&"))
    }

    suspend fun facets(): Result<Facets> = get("/v1/exercises/facets")

    suspend fun profile(): Result<Profile> = get("/v1/profile")

    suspend fun saveProfile(
        heightCm: Double? = null,
        sex: String? = null,
        trainingMonths: Double? = null,
        vo2max: Double? = null,
        sessionMinutes: Double? = null,
        goalProfile: String? = null,
    ): Result<Unit> = post<ProfileUpdate, Unit>(
        "/v1/profile",
        ProfileUpdate(heightCm, sex, trainingMonths, vo2max, sessionMinutes, goalProfile),
    ).map { }

    /** Direct body-fat entry, or none at all when only the scale is known. */
    suspend fun saveBody(
        bodyweightKg: Double,
        bodyfatPct: Double? = null,
        source: String? = null,
    ): Result<BodyResult> = post("/v1/body", BodyUpdate(bodyweightKg, bodyfatPct, source))

    /**
     * Tape method. The server owns the formula and returns the body fat it
     * derived, so the phone never reimplements a calculation that takes a
     * logarithm of (waist - neck).
     */
    suspend fun saveBodyTape(
        bodyweightKg: Double,
        neckCm: Double,
        waistCm: Double,
        hipCm: Double? = null,
    ): Result<BodyResult> = post(
        "/v1/body",
        BodyUpdate(bodyweightKg, neckCm = neckCm, waistCm = waistCm, hipCm = hipCm),
    )

    suspend fun saveClaims(claims: List<Triple<String, Double, String>>): Result<Unit> =
        post<ClaimsUpdate, Unit>(
            "/v1/claims",
            ClaimsUpdate(claims.map { ClaimIn(it.first, it.second, it.third) }),
        ).map { }

    suspend fun saveSkill(skill: String, unlocked: Boolean): Result<Unit> =
        post<SkillsUpdate, Unit>("/v1/skills", SkillsUpdate(mapOf(skill to unlocked))).map { }

    /**
     * URL for a demo image or animation. Filenames come from the library, so
     * this is never built from user input.
     */
    fun mediaUrl(kind: String, file: String): String =
        baseUrl.trimEnd('/') + "/v1/media/" + kind + "/" + file

    /** The token, so the image loader can authenticate the same as every other call. */
    val bearer: String get() = authToken

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
