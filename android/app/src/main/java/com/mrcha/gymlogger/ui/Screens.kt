package com.mrcha.gymlogger.ui

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.FitnessCenter
import androidx.compose.material.icons.filled.Mic
import androidx.compose.material.icons.filled.Person
import androidx.compose.material.icons.automirrored.filled.Send
import androidx.compose.material.icons.filled.Settings
import androidx.compose.material3.*
import androidx.compose.foundation.BorderStroke
import androidx.compose.ui.graphics.RectangleShape
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.OutlinedTextFieldDefaults
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.unit.dp
import com.mrcha.gymlogger.MainViewModel
import com.mrcha.gymlogger.Prefs
import com.mrcha.gymlogger.net.LogResult
import com.mrcha.gymlogger.net.MuscleReport
import com.mrcha.gymlogger.net.Attributes
import com.mrcha.gymlogger.net.PatternScore
import com.mrcha.gymlogger.net.Rank
import com.mrcha.gymlogger.net.Recommendation

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun GymApp(
    vm: MainViewModel,
    prefs: Prefs,
    onMic: () -> Unit,
    onSettingsSaved: () -> Unit,
) {
    // One loader for the whole app, carrying the bearer token and GIF decoding.
    // Rebuilt only when the token changes, so scrolling the library does not
    // throw away the memory cache.
    val context = LocalContext.current
    val token = vm.authHeader()
    val imageLoader = remember(token) { buildImageLoader(context, token) }

    val snackbar = remember { SnackbarHostState() }

    LaunchedEffect(vm.error) {
        vm.error?.let {
            snackbar.showSnackbar(it)
            vm.error = null
        }
    }

    Scaffold(
        containerColor = Forge.Ground,
        snackbarHost = { SnackbarHost(snackbar) },
    ) { padding ->
        Row(Modifier.padding(padding).fillMaxSize()) {
            SideRail(
                selected = vm.rail,
                onSelect = { tab ->
                    vm.rail = tab
                    when (tab) {
                        Rail.Lifts -> vm.openLibrary()
                        Rail.RankTab -> vm.openProfile()
                        else -> Unit
                    }
                },
            )
            RailDivider()

            Column(
                Modifier
                    .weight(1f)
                    .fillMaxSize()
                    .verticalScroll(rememberScrollState()),
            ) {
                // The wordmark sits above the content rather than in a title
                // bar, so the rank name below it keeps the full width.
                Row(
                    Modifier
                        .fillMaxWidth()
                        .padding(horizontal = 18.dp, vertical = 14.dp),
                    horizontalArrangement = Arrangement.SpaceBetween,
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    Text(
                        "Blood Ledger",
                        style = MaterialTheme.typography.headlineSmall,
                        color = Forge.Parchment,
                    )
                    IconButton(onClick = { vm.showSettings = true }) {
                        Icon(
                            Icons.Default.Settings,
                            contentDescription = "Settings",
                            tint = Forge.Slate,
                        )
                    }
                }

                when (vm.rail) {
                    Rail.Ledger -> LedgerTab(vm)
                    else -> LogScreen(
                        rank = vm.rank,
                        next = vm.next,
                        muscles = vm.muscles,
                        draft = vm.draft,
                        busy = vm.busy,
                        connection = vm.connection,
                        onDraftChange = { vm.draft = it },
                        onMic = onMic,
                        onSubmit = { vm.submit() },
                        onRetry = { vm.refresh() },
                    )
                }
            }
        }
    }

    // The session verdict takes the whole screen when a log lands, which is the
    // mockup's second panel: a result worth reading, not a card to scroll past.
    vm.lastResult?.let { result ->
        if (vm.showVerdict) {
            VerdictScreen(
                result = result,
                onConfirm = { id -> vm.confirmPending(id) },
                onClose = { vm.showVerdict = false },
            )
        }
    }

    if (vm.showProfile) {
        ProfileScreen(
            profile = vm.profile,
            busy = vm.busy,
            onSaveProfile = vm::saveProfile,
            onSaveBody = vm::saveBody,
            onSaveTape = vm::saveBodyTape,
            onSaveClaim = vm::saveClaim,
            onToggleSkill = vm::toggleSkill,
            onClose = {
                vm.showProfile = false
                vm.rail = Rail.Log
            },
        )
    }

    if (vm.showLibrary) {
        LibraryScreen(
            state = vm.library,
            imageLoader = imageLoader,
            mediaUrl = vm::mediaUrl,
            onQueryChange = vm::setLibraryQuery,
            onFilter = vm::setLibraryFilter,
            onLoadMore = vm::loadMoreExercises,
            onClose = {
                vm.showLibrary = false
                vm.rail = Rail.Log
            },
        )
    }

    if (vm.showSettings) {
        SettingsDialog(
            prefs = prefs,
            onDismiss = { vm.showSettings = false },
            onSaved = {
                vm.showSettings = false
                onSettingsSaved()
            },
        )
    }
}

/** LEDGER: the rank in full, with every attribute, pattern and gate. */
@Composable
private fun LedgerTab(vm: MainViewModel) {
    Column(
        Modifier.padding(horizontal = 14.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        vm.rank?.let { RankCard(it) }
        vm.lastResult?.let { ResultSection(it) { id -> vm.confirmPending(id) } }
        Spacer(Modifier.height(32.dp))
    }
}

@Composable
private fun RankCard(rank: Rank) {
    // The rank name is the loudest thing on screen and the only place the ember
    // accent runs at full strength, because it is the one number the whole app
    // exists to move.
    ForgePanel(accent = if (rank.berserk.qualified) Forge.Blood else Forge.Hairline) {
        Row(
            Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.SpaceBetween,
            verticalAlignment = Alignment.Bottom,
        ) {
            Text(
                rank.rank,
                style = MaterialTheme.typography.headlineSmall,
                color = Forge.Ember,
            )
            Text(
                "RS %.1f".format(rank.rs),
                style = MaterialTheme.typography.titleMedium,
                color = Forge.Bone,
            )
        }

        // Erratum 1: composite below the boundary, gates at it.
        if (rank.showGates) GateReadout(rank) else BandProgress(rank)

        SectionLabel("Attributes")
        AttributeRow(rank.attributes)

        SectionLabel("Patterns")
        PatternList(rank.patterns)

        if (rank.weakLink.isNotBlank()) {
            Text(
                rank.weakLink,
                style = MaterialTheme.typography.bodyMedium,
                color = Forge.Ember,
            )
        }

        if (rank.blood.total > 0) {
            SectionLabel("Blood", accent = Forge.BloodBright)
            LedgerRow(
                rank.blood.tierName,
                "%.0f".format(rank.blood.total),
                valueColor = Forge.BloodBright,
            )
            ForgeMeter(rank.blood.progress.toFloat(), color = Forge.BloodBright)
            Muted(
                "+%.0f last 30 days - %.0f to %s".format(
                    rank.blood.last30d, rank.blood.toNext, rank.blood.nextTier,
                ),
            )
        }

        LedgerRow(
            "threat %.0f".format(rank.threatLevel),
            "confidence %.0f%%".format(rank.confidence * 100),
            labelColor = if (rank.threatLevel < 100) Forge.Ember else Forge.Slate,
            valueColor = Forge.Slate,
        )

        rank.notes.forEach { Muted(it) }
    }
}

@Composable
private fun BandProgress(rank: Rank) {
    if (rank.nextRank.isBlank()) return
    Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
        ForgeMeter(rank.bandProgress.toFloat())
        LedgerRow(
            "RS %.0f / %.0f".format(rank.rs, rank.rs + rank.toNext),
            rank.nextRank,
            valueColor = Forge.Bone,
        )
    }
}

// GateReadout is the table Erratum 1 specifies: every gate, its value against
// its threshold, and a computed fix for whichever binds. No composite appears.
//
// A passing gate is bone, a failing one is blood. That is the entire colour
// system on this readout, which is why blood appears nowhere else in it.
@Composable
private fun GateReadout(rank: Rank) {
    Column(verticalArrangement = Arrangement.spacedBy(3.dp)) {
        SectionLabel("Berserk gates", accent = Forge.BloodBright)

        rank.berserk.gates.forEach { gate ->
            LedgerRow(
                gate.name.padEnd(11),
                "%5.1f / %-3.0f".format(gate.value, gate.threshold),
                labelColor = if (gate.pass) Forge.Bone else Forge.BloodBright,
                valueColor = if (gate.pass) Forge.Parchment else Forge.BloodBright,
            ) {
                Text(
                    if (gate.pass) "PASS" else "FAIL",
                    style = Ledger,
                    color = if (gate.pass) Forge.Ember else Forge.BloodBright,
                )
            }
        }

        LedgerRow(
            "PATTERNS".padEnd(11),
            "%d / 6 verified".format(rank.berserk.patternsVerified),
            labelColor = if (rank.berserk.patternsVerified == 6) Forge.Bone else Forge.BloodBright,
        )

        Gap(4)
        Text(
            rank.berserk.summary,
            style = MaterialTheme.typography.bodyMedium,
            color = Forge.Parchment,
        )

        rank.berserk.gates.firstOrNull { !it.pass && it.fix.isNotBlank() }?.let {
            Text(it.fix, style = MaterialTheme.typography.bodySmall, color = Forge.Ember)
        }

        // Erratum 6: without this, a passing floor beside a failing MIGHT reads
        // as a bug.
        if (rank.berserk.note.isNotBlank()) Muted(rank.berserk.note)
    }
}

@Composable
private fun AttributeRow(a: Attributes) {
    val rows = listOf(
        "MIGHT" to a.might, "DOMINION" to a.dominion, "FRAME" to a.frame,
        "VIGOR" to a.vigor, "DISCIPLINE" to a.discipline, "MASTERY" to a.mastery,
    )
    Column(verticalArrangement = Arrangement.spacedBy(5.dp)) {
        rows.forEach { (name, v) ->
            Column(verticalArrangement = Arrangement.spacedBy(2.dp)) {
                LedgerRow(name, "%.0f".format(v))
                // 100 is Berserk level in an attribute, so the meter is scaled
                // to that rather than to the 120 ceiling: a full bar should read
                // as "at the standard", not "near the cap".
                ForgeMeter((v / 100.0).toFloat(), color = Forge.MuscleHot)
            }
        }
    }
}

@Composable
private fun PatternList(patterns: List<PatternScore>) {
    Column(verticalArrangement = Arrangement.spacedBy(3.dp)) {
        patterns.forEach { p ->
            // An imputed score stands in for data the app has never seen, so it
            // is drawn drained - the same visual grammar the muscle map uses for
            // a group that got no work.
            val imputed = p.imputed
            LedgerRow(
                p.name,
                "%5.1f".format(p.score),
                labelColor = if (imputed) Forge.Slate else Forge.Bone,
                valueColor = if (imputed) Forge.Slate else Forge.Parchment,
            ) {
                Text(
                    if (imputed) "untested" else p.status.lowercase(),
                    style = MaterialTheme.typography.bodySmall,
                    color = if (imputed) Forge.Ember else Forge.Slate,
                )
            }
        }
    }
}

// MuscleCard adapts the server's per-group report onto the muscle map. The map
// takes only what it draws, so the wire type is flattened here rather than
// pushed into it.
@Composable
private fun MuscleCard(report: MuscleReport) {
    // Zero-volume groups are dropped from the bars: the figures already show a
    // drained muscle as unworked, and a row reading "CALVES 0" underneath says
    // the same thing twice. The neglected line is where absence gets named.
    val volumes = report.volumes
        .filter { it.sets > 0 }
        .sortedByDescending { it.sets }
        .map { MuscleVolume(it.name, kotlin.math.round(it.sets).toInt()) }

    if (volumes.isEmpty()) return

    val neglected = report.volumes
        .firstOrNull { it.name == report.neglected }
        ?.let { v ->
            if (v.lastTrained.isBlank()) report.neglected + " - NEVER LOGGED"
            else report.neglected + " - 0 SETS IN " + v.daysSince + " D"
        }
        ?: report.neglected.takeIf { it.isNotBlank() }?.let { it + " - NEVER LOGGED" }

    ForgePanel {
        SectionLabel("Muscle map")
        MuscleMap(
            volumes = volumes,
            totalSets = report.totalSets,
            neglected = neglected,
        )
        // Unmapped exercises are surfaced, not swallowed. Volume credited to
        // nothing is invisible in the figures above, and the only way a mapping
        // gap gets noticed is if the app admits to it.
        if (report.unmatched.isNotEmpty()) {
            Muted("not mapped to a muscle group: " + report.unmatched.joinToString(", "))
        }
    }
}

@Composable
private fun NextSessionCard(rec: Recommendation) {
    val title = when (rec.kind) {
        "train" -> rec.sessionName
        "rest" -> "Rest day"
        else -> "Nothing scheduled"
    }
    ForgePanel {
        SectionLabel("Up next")
        Text(
            title.uppercase(),
            style = MaterialTheme.typography.titleLarge,
            color = if (rec.kind == "rest") Forge.Ash else Forge.Parchment,
        )
        Muted(rec.reason)
    }
}

@Composable
private fun LogInput(
    value: String,
    busy: Boolean,
    onChange: (String) -> Unit,
    onMic: () -> Unit,
    onSubmit: () -> Unit,
) {
    Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
        SectionLabel("The work")
        OutlinedTextField(
            value = value,
            onValueChange = onChange,
            modifier = Modifier
                .fillMaxWidth()
                .heightIn(min = 110.dp),
            placeholder = {
                Text(
                    "bench 100 x 5, 5, 4; dips bw x 12, 10",
                    style = MaterialTheme.typography.bodyMedium,
                    color = Forge.Slate,
                )
            },
            enabled = !busy,
            shape = RectangleShape,
            textStyle = MaterialTheme.typography.bodyLarge,
            colors = OutlinedTextFieldDefaults.colors(
                focusedBorderColor = Forge.Ember,
                unfocusedBorderColor = Forge.Hairline,
                focusedContainerColor = Forge.Panel,
                unfocusedContainerColor = Forge.Panel,
                cursorColor = Forge.Ember,
                focusedTextColor = Forge.Parchment,
                unfocusedTextColor = Forge.Parchment,
            ),
            keyboardOptions = KeyboardOptions(imeAction = ImeAction.Default),
        )
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            OutlinedButton(
                onClick = onMic,
                enabled = !busy,
                shape = RectangleShape,
                border = BorderStroke(1.dp, Forge.Steel),
                colors = ButtonDefaults.outlinedButtonColors(contentColor = Forge.Bone),
            ) {
                Icon(Icons.Default.Mic, contentDescription = null)
                Spacer(Modifier.width(8.dp))
                Text("SPEAK", style = MaterialTheme.typography.labelMedium)
            }
            // The one blood-filled control in the app: logging is the act
            // everything else is downstream of.
            Button(
                onClick = onSubmit,
                enabled = !busy && value.isNotBlank(),
                modifier = Modifier.weight(1f),
                shape = RectangleShape,
                colors = ButtonDefaults.buttonColors(
                    containerColor = Forge.Blood,
                    contentColor = Forge.Parchment,
                    disabledContainerColor = Forge.PanelHi,
                    disabledContentColor = Forge.Slate,
                ),
            ) {
                if (busy) {
                    CircularProgressIndicator(
                        modifier = Modifier.size(18.dp),
                        strokeWidth = 2.dp,
                        color = Forge.Ember,
                    )
                } else {
                    Icon(Icons.AutoMirrored.Filled.Send, contentDescription = null)
                    Spacer(Modifier.width(8.dp))
                    Text("LOG IT", style = MaterialTheme.typography.labelMedium)
                }
            }
        }
    }
}

@Composable
private fun ResultSection(result: LogResult, onConfirm: (Long) -> Unit) {
    Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {

        // A parse that needs confirmation is the most important thing on
        // screen: nothing was written, and silently moving on would leave the
        // session unlogged. It is the one panel that gets a blood border.
        if (result.needsConfirmation.isNotBlank()) {
            ForgePanel(accent = Forge.Blood) {
                SectionLabel("Needs a check", accent = Forge.BloodBright)
                Text(
                    result.needsConfirmation,
                    style = MaterialTheme.typography.bodyMedium,
                    color = Forge.Parchment,
                )
                if (result.pendingId != 0L) {
                    Row(
                        horizontalArrangement = Arrangement.spacedBy(10.dp),
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        Button(
                            onClick = { onConfirm(result.pendingId) },
                            shape = RectangleShape,
                            colors = ButtonDefaults.buttonColors(
                                containerColor = Forge.Blood,
                                contentColor = Forge.Parchment,
                            ),
                        ) {
                            Text("LOG IT ANYWAY", style = MaterialTheme.typography.labelMedium)
                        }
                        Muted("or edit the text above and send again")
                    }
                }
            }
        }

        // The coach reply is prose, not data: parchment on panel, no ledger
        // face, no label above it. It should read like something written.
        if (result.reply.isNotBlank()) {
            ForgePanel {
                Text(
                    result.reply,
                    style = MaterialTheme.typography.bodyLarge,
                    color = Forge.Parchment,
                )
            }
        }

        result.repairs.takeIf { it.isNotEmpty() }?.let { repairs ->
            ForgePanel {
                SectionLabel("Adjusted while saving", accent = Forge.Ash)
                repairs.forEach { Muted(it) }
            }
        }

        result.context?.liftHistory?.takeIf { it.isNotEmpty() }?.let { lifts ->
            ForgePanel {
                SectionLabel("This session")
                lifts.forEach { lift ->
                    Row(
                        Modifier.fillMaxWidth(),
                        horizontalArrangement = Arrangement.SpaceBetween,
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        Column(Modifier.weight(1f)) {
                            Text(
                                lift.display,
                                style = MaterialTheme.typography.bodyMedium,
                                color = Forge.Parchment,
                            )
                            Text(lift.topSetToday, style = Ledger, color = Forge.Ash)
                        }
                        // A PR is the only thing in this list worth colour, and
                        // it gets the brightest one in the palette. A baseline
                        // is just a fact and stays ash.
                        val badge = when {
                            lift.isWeightPR -> "WEIGHT PR"
                            lift.isRepPR -> "REP PR"
                            lift.isBaseline -> "baseline"
                            else -> ""
                        }
                        if (badge.isNotEmpty()) {
                            val isPR = lift.isWeightPR || lift.isRepPR
                            Text(
                                badge,
                                style = MaterialTheme.typography.labelMedium,
                                color = if (isPR) Forge.BloodBright else Forge.Slate,
                            )
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun SettingsDialog(prefs: Prefs, onDismiss: () -> Unit, onSaved: () -> Unit) {
    var url by remember { mutableStateOf(prefs.baseUrl) }
    var token by remember { mutableStateOf(prefs.authToken) }

    AlertDialog(
        onDismissRequest = onDismiss,
        shape = RectangleShape,
        containerColor = Forge.Panel,
        titleContentColor = Forge.Bone,
        textContentColor = Forge.Parchment,
        title = { Text("SERVER", style = MaterialTheme.typography.titleSmall) },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
                OutlinedTextField(
                    value = url,
                    onValueChange = { url = it },
                    label = { Text("Base URL") },
                    placeholder = { Text("https://gym.yourdomain.com") },
                    singleLine = true,
                )
                OutlinedTextField(
                    value = token,
                    onValueChange = { token = it },
                    label = { Text("Auth token") },
                    singleLine = true,
                )
            }
        },
        confirmButton = {
            TextButton(onClick = {
                prefs.baseUrl = url
                prefs.authToken = token
                // Force re-registration so a server change does not leave the
                // phone pointed at the old one for push.
                prefs.registeredPushToken = ""
                onSaved()
            }) { Text("Save") }
        },
        dismissButton = {
            TextButton(onClick = onDismiss) { Text("Cancel") }
        },
    )
}

/**
 * Coil loader for the exercise demos.
 *
 * Two things it has to do that the default cannot: attach the bearer token,
 * since the media route is authenticated like everything else, and decode GIFs,
 * which needs the coil-gif decoder registered explicitly.
 */
private fun buildImageLoader(context: android.content.Context, token: String): coil.ImageLoader =
    coil.ImageLoader.Builder(context)
        .components {
            if (android.os.Build.VERSION.SDK_INT >= android.os.Build.VERSION_CODES.P) {
                add(coil.decode.ImageDecoderDecoder.Factory())
            } else {
                add(coil.decode.GifDecoder.Factory())
            }
        }
        .okHttpClient {
            okhttp3.OkHttpClient.Builder()
                .addInterceptor { chain ->
                    val req = chain.request().newBuilder()
                        .header("Authorization", "Bearer $token")
                        .build()
                    chain.proceed(req)
                }
                .build()
        }
        .build()
