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
        snackbarHost = { SnackbarHost(snackbar) },
        topBar = {
            TopAppBar(
                title = { Text("Gym Logger") },
                actions = {
                    IconButton(onClick = { vm.openProfile() }) {
                        Icon(Icons.Default.Person, contentDescription = "Profile")
                    }
                    IconButton(onClick = { vm.openLibrary() }) {
                        Icon(Icons.Default.FitnessCenter, contentDescription = "Exercises")
                    }
                    IconButton(onClick = { vm.showSettings = true }) {
                        Icon(Icons.Default.Settings, contentDescription = "Settings")
                    }
                },
            )
        },
    ) { padding ->
        Column(
            modifier = Modifier
                .padding(padding)
                .padding(horizontal = 16.dp)
                .fillMaxSize()
                .verticalScroll(rememberScrollState()),
            verticalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            vm.rank?.let { RankCard(it) }
            vm.next?.let { NextSessionCard(it) }
            vm.muscles?.let { MuscleCard(it) }

            LogInput(
                value = vm.draft,
                busy = vm.busy,
                onChange = { vm.draft = it },
                onMic = onMic,
                onSubmit = { vm.submit() },
            )

            vm.lastResult?.let { ResultSection(it) { id -> vm.confirmPending(id) } }

            Spacer(Modifier.height(24.dp))
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
            onClose = { vm.showProfile = false },
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
            onClose = { vm.showLibrary = false },
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

@Composable
private fun RankCard(rank: Rank) {
    Card(Modifier.fillMaxWidth()) {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Row(
                Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.SpaceBetween,
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Text(rank.rank, style = MaterialTheme.typography.headlineSmall)
                Text("RS %.1f".format(rank.rs), style = MaterialTheme.typography.titleMedium)
            }

            // Erratum 1, and this is the whole point of the switch: below the
            // Berserk boundary a composite is the honest readout, and at the
            // boundary it is misleading, because a lifter can sit above the old
            // RS 80 threshold and still be one point of MASTERY short. So the
            // top two ranks get the binding gate instead of a progress bar.
            if (rank.showGates) {
                GateReadout(rank)
            } else {
                BandProgress(rank)
            }

            AttributeRow(rank.attributes)
            PatternList(rank.patterns)

            if (rank.weakLink.isNotBlank()) {
                Text(
                    rank.weakLink,
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.primary,
                )
            }

            if (rank.blood.total > 0) {
                Text(
                    "%s  ·  %.0f Blood (+%.0f last 30d)  ·  %.0f to %s".format(
                        rank.blood.tierName, rank.blood.total,
                        rank.blood.last30d, rank.blood.toNext, rank.blood.nextTier,
                    ),
                    style = MaterialTheme.typography.bodySmall,
                )
            }

            Text(
                "threat %.0f  ·  confidence %.0f%%  ·  journey %d sessions".format(
                    rank.threatLevel, rank.confidence * 100, rank.journey.sessions,
                ),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )

            rank.notes.forEach {
                Text(
                    it,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
    }
}

@Composable
private fun BandProgress(rank: Rank) {
    if (rank.nextRank.isBlank()) return
    LinearProgressIndicator(
        progress = { rank.bandProgress.toFloat().coerceIn(0f, 1f) },
        modifier = Modifier.fillMaxWidth(),
    )
    Text(
        "RS %.0f / %.0f → %s".format(rank.rs, rank.rs + rank.toNext, rank.nextRank),
        style = MaterialTheme.typography.bodySmall,
    )
}

// GateReadout is the table Erratum 1 specifies: every gate, its value against
// its threshold, and the computed fix for whichever one is binding. No
// composite number appears here at all.
@Composable
private fun GateReadout(rank: Rank) {
    Column(verticalArrangement = Arrangement.spacedBy(2.dp)) {
        rank.berserk.gates.forEach { gate ->
            Row(
                Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.SpaceBetween,
            ) {
                Text(
                    "%-11s %5.1f / %.0f".format(gate.name, gate.value, gate.threshold),
                    style = MaterialTheme.typography.bodySmall,
                    fontFamily = FontFamily.Monospace,
                )
                Text(
                    if (gate.pass) "✓" else "✗",
                    color = if (gate.pass) MaterialTheme.colorScheme.primary
                    else MaterialTheme.colorScheme.error,
                    style = MaterialTheme.typography.bodySmall,
                )
            }
        }

        Text(
            "%-11s %d / 6 verified".format("PATTERNS", rank.berserk.patternsVerified),
            style = MaterialTheme.typography.bodySmall,
            fontFamily = FontFamily.Monospace,
        )

        Spacer(Modifier.height(4.dp))
        Text(rank.berserk.summary, style = MaterialTheme.typography.bodyMedium)

        // The first failing gate carries a computed instruction rather than
        // encouragement: what number has to move, and to what.
        rank.berserk.gates.firstOrNull { !it.pass && it.fix.isNotBlank() }?.let {
            Text(
                it.fix,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.primary,
            )
        }

        // Erratum 6: a user looking at a passing pattern floor and a failing
        // MIGHT gate will otherwise assume a bug, so the reason is stated.
        if (rank.berserk.note.isNotBlank()) {
            Text(
                rank.berserk.note,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}

@Composable
private fun AttributeRow(a: Attributes) {
    Text(
        "MIGHT %.0f  DOM %.0f  FRAME %.0f  VIGOR %.0f  DISC %.0f  MAST %.0f".format(
            a.might, a.dominion, a.frame, a.vigor, a.discipline, a.mastery,
        ),
        style = MaterialTheme.typography.bodySmall,
        fontFamily = FontFamily.Monospace,
    )
}

@Composable
private fun PatternList(patterns: List<PatternScore>) {
    Column {
        patterns.forEach { p ->
            Row(
                Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.SpaceBetween,
            ) {
                Text(
                    "%-18s %5.1f".format(p.name, p.score),
                    style = MaterialTheme.typography.bodySmall,
                    fontFamily = FontFamily.Monospace,
                )
                // An imputed score is an estimate standing in for data the app
                // has never seen, and saying so is what makes the nudge work.
                Text(
                    if (p.imputed) "untested" else p.status.lowercase(),
                    style = MaterialTheme.typography.bodySmall,
                    color = if (p.imputed) MaterialTheme.colorScheme.error
                    else MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
    }
}

// MuscleCard adapts the server's per-group report onto the muscle map. The map
// itself takes only what it draws -- name, set count, total, and the neglected
// line -- so the wire type is flattened here rather than pushed into it.
@Composable
private fun MuscleCard(report: MuscleReport) {
    // Zero-volume groups are dropped from the bars: the figures already show a
    // drained muscle as unworked, and a row reading "CALVES 0" under it is the
    // same fact twice. The neglected line is where absence gets named.
    val volumes = report.volumes
        .filter { it.sets > 0 }
        .sortedByDescending { it.sets }
        .map { MuscleVolume(it.name, kotlin.math.round(it.sets).toInt()) }

    if (volumes.isEmpty()) return

    val neglected = report.volumes
        .firstOrNull { it.name == report.neglected }
        ?.let { v ->
            if (v.lastTrained.isBlank()) "${report.neglected} · NEVER LOGGED"
            else "${report.neglected} · 0 SETS IN ${v.daysSince} D"
        }
        ?: report.neglected.takeIf { it.isNotBlank() }?.let { "$it · NEVER LOGGED" }

    Card(Modifier.fillMaxWidth()) {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            MuscleMap(
                volumes = volumes,
                totalSets = report.totalSets,
                neglected = neglected,
            )
            // Unmapped exercises are surfaced, not swallowed. Volume credited to
            // nothing is invisible in the figures above, and the only way a
            // mapping gap gets noticed is if the app admits to it.
            if (report.unmatched.isNotEmpty()) {
                Text(
                    "not mapped to a muscle group: " + report.unmatched.joinToString(", "),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
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
    Card(Modifier.fillMaxWidth()) {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
            Text("Up next", style = MaterialTheme.typography.labelMedium)
            Text(title, style = MaterialTheme.typography.titleLarge)
            Text(rec.reason, style = MaterialTheme.typography.bodySmall)
        }
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
    Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
        OutlinedTextField(
            value = value,
            onValueChange = onChange,
            modifier = Modifier
                .fillMaxWidth()
                .heightIn(min = 120.dp),
            label = { Text("What did you do") },
            placeholder = { Text("bench 100 x 5, 5, 4; dips bw x 12, 10") },
            enabled = !busy,
            keyboardOptions = KeyboardOptions(imeAction = ImeAction.Default),
        )
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            OutlinedButton(onClick = onMic, enabled = !busy) {
                Icon(Icons.Default.Mic, contentDescription = null)
                Spacer(Modifier.width(8.dp))
                Text("Speak")
            }
            Button(
                onClick = onSubmit,
                enabled = !busy && value.isNotBlank(),
                modifier = Modifier.weight(1f),
            ) {
                if (busy) {
                    CircularProgressIndicator(
                        modifier = Modifier.size(18.dp),
                        strokeWidth = 2.dp,
                    )
                } else {
                    Icon(Icons.AutoMirrored.Filled.Send, contentDescription = null)
                    Spacer(Modifier.width(8.dp))
                    Text("Log it")
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
        // session unlogged.
        if (result.needsConfirmation.isNotBlank()) {
            Card(
                colors = CardDefaults.cardColors(
                    containerColor = MaterialTheme.colorScheme.errorContainer,
                ),
            ) {
                Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    Text("Needs a check", style = MaterialTheme.typography.titleMedium)
                    Text(result.needsConfirmation, style = MaterialTheme.typography.bodyMedium)
                    if (result.pendingId != 0L) {
                        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                            Button(onClick = { onConfirm(result.pendingId) }) {
                                Text("Log it anyway")
                            }
                            Text(
                                "or edit the text above and send again",
                                style = MaterialTheme.typography.bodySmall,
                                modifier = Modifier.align(Alignment.CenterVertically),
                            )
                        }
                    }
                }
            }
        }

        if (result.reply.isNotBlank()) {
            Card(Modifier.fillMaxWidth()) {
                Text(
                    result.reply,
                    modifier = Modifier.padding(16.dp),
                    style = MaterialTheme.typography.bodyLarge,
                )
            }
        }

        result.repairs.takeIf { it.isNotEmpty() }?.let { repairs ->
            Card(Modifier.fillMaxWidth()) {
                Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                    Text("Adjusted while saving", style = MaterialTheme.typography.labelMedium)
                    repairs.forEach {
                        Text(it, style = MaterialTheme.typography.bodySmall)
                    }
                }
            }
        }

        result.context?.liftHistory?.takeIf { it.isNotEmpty() }?.let { lifts ->
            Card(Modifier.fillMaxWidth()) {
                Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    Text("This session", style = MaterialTheme.typography.labelMedium)
                    lifts.forEach { lift ->
                        Row(
                            Modifier.fillMaxWidth(),
                            horizontalArrangement = Arrangement.SpaceBetween,
                        ) {
                            Column(Modifier.weight(1f)) {
                                Text(lift.display, style = MaterialTheme.typography.bodyMedium)
                                Text(
                                    lift.topSetToday,
                                    style = MaterialTheme.typography.bodySmall,
                                    fontFamily = FontFamily.Monospace,
                                )
                            }
                            val badge = when {
                                lift.isWeightPR -> "weight PR"
                                lift.isRepPR -> "rep PR"
                                lift.isBaseline -> "baseline"
                                else -> ""
                            }
                            if (badge.isNotEmpty()) {
                                Text(
                                    badge,
                                    style = MaterialTheme.typography.labelSmall,
                                    fontWeight = FontWeight.Bold,
                                    modifier = Modifier.align(Alignment.CenterVertically),
                                )
                            }
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
        title = { Text("Server") },
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
