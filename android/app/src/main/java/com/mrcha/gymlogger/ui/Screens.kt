package com.mrcha.gymlogger.ui

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Mic
import androidx.compose.material.icons.filled.Send
import androidx.compose.material.icons.filled.Settings
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.unit.dp
import com.mrcha.gymlogger.MainViewModel
import com.mrcha.gymlogger.Prefs
import com.mrcha.gymlogger.net.LogResult
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
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
            Row(
                Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.SpaceBetween,
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Text(rank.tier, style = MaterialTheme.typography.headlineSmall)
                Text("%.1f".format(rank.score), style = MaterialTheme.typography.titleMedium)
            }

            // Progress within the current division, so the bar moves on a
            // weekly timescale rather than looking frozen for months.
            if (rank.nextTier.isNotBlank()) {
                val step = 100f / 21f
                val progress = ((step - rank.toNext.toFloat()) / step).coerceIn(0f, 1f)
                LinearProgressIndicator(
                    progress = { progress },
                    modifier = Modifier.fillMaxWidth(),
                )
                Text(
                    "%.1f to %s".format(rank.toNext, rank.nextTier),
                    style = MaterialTheme.typography.bodySmall,
                )
            }

            Text(
                "consistency %.0f  ·  strength %.0f".format(rank.consistency, rank.strength),
                style = MaterialTheme.typography.bodySmall,
            )
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
                    Icon(Icons.Default.Send, contentDescription = null)
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
