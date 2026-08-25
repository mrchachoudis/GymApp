package com.mrcha.gymlogger.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.RectangleShape
import androidx.compose.ui.unit.dp
import com.mrcha.gymlogger.net.LogResult
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale

/**
 * The session verdict, full screen.
 *
 * The mockup's second panel, and the reason it is a screen rather than a card:
 * after logging, the single most valuable thing the app has is a sentence about
 * what just happened. Rendered inline it competes with the rank, the muscle map
 * and the input box; rendered full-bleed it is simply the answer.
 *
 * The headline is derived, not typed by the model. The coach writes prose; what
 * kind of session it was is a fact the context builder already decided, and a
 * headline that disagreed with the badges below it would be worse than none.
 */
@Composable
fun VerdictScreen(
    result: LogResult,
    onConfirm: (Long) -> Unit,
    onClose: () -> Unit,
) {
    val lifts = result.context?.liftHistory.orEmpty()
    val weightPR = lifts.firstOrNull { it.isWeightPR }
    val repPR = lifts.firstOrNull { it.isRepPR }

    val headline = when {
        result.needsConfirmation.isNotBlank() -> "Needs\na check."
        weightPR != null -> "Weight PR\non ${weightPR.name}."
        repPR != null -> "Rep PR\non ${repPR.name}."
        lifts.any { it.isBaseline } -> "Baseline\nset."
        lifts.isNotEmpty() -> "Session\nlogged."
        else -> "Nothing\nto log."
    }

    val topSet = lifts.maxByOrNull { it.volumeTodayKg }
    val volumeKg = lifts.sumOf { it.volumeTodayKg }
    val workingSets = result.context?.liftHistory?.size ?: 0

    Column(
        Modifier
            .fillMaxSize()
            .background(Forge.Ground)
            .verticalScroll(rememberScrollState())
            .padding(horizontal = 20.dp),
    ) {
        Row(
            Modifier
                .fillMaxWidth()
                .padding(top = 18.dp),
            horizontalArrangement = Arrangement.SpaceBetween,
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(
                "SESSION LOGGED · " + SimpleDateFormat("HH:mm", Locale.US).format(Date()),
                style = MaterialTheme.typography.labelMedium,
                color = Forge.Ash,
            )
            TextButton(
                onClick = onClose,
                colors = ButtonDefaults.textButtonColors(contentColor = Forge.Ash),
            ) { Text("CLOSE", style = MaterialTheme.typography.labelMedium) }
        }

        Spacer(Modifier.height(18.dp))

        Text(
            headline,
            style = MaterialTheme.typography.headlineLarge,
            color = Forge.Parchment,
        )

        Spacer(Modifier.height(24.dp))

        // Three figures across, hairline-ruled above and below, as in the mockup.
        HairlineRule()
        Row(
            Modifier
                .fillMaxWidth()
                .padding(vertical = 14.dp),
            horizontalArrangement = Arrangement.SpaceBetween,
        ) {
            Stat("TOP SET", topSet?.topSetToday ?: "—")
            Stat("EIRM", topSet?.est1RMToday?.let { "%.1f".format(it) } ?: "—")
            Stat("VOLUME", if (volumeKg > 0) "%.1f t".format(volumeKg / 1000) else "—")
        }
        HairlineRule()

        Spacer(Modifier.height(20.dp))

        if (result.needsConfirmation.isNotBlank()) {
            Text(
                result.needsConfirmation,
                style = MaterialTheme.typography.bodyLarge,
                color = Forge.Parchment,
            )
            Spacer(Modifier.height(16.dp))
            if (result.pendingId != 0L) {
                Button(
                    onClick = { onConfirm(result.pendingId) },
                    shape = RectangleShape,
                    colors = ButtonDefaults.buttonColors(
                        containerColor = Forge.Blood,
                        contentColor = Forge.Parchment,
                    ),
                ) {
                    Text("LOG IT ANYWAY", style = MaterialTheme.typography.titleSmall)
                }
            }
        }

        if (workingSets > 0) {
            Text(
                "Session is in. $workingSets tracked ${if (workingSets == 1) "lift" else "lifts"}" +
                    if (volumeKg > 0) ", %.1f tonnes.".format(volumeKg / 1000) else ".",
                style = MaterialTheme.typography.bodyLarge,
                color = Forge.Parchment,
            )
            Spacer(Modifier.height(16.dp))
        }

        // The coach's prose, set as prose: parchment, generous leading, nothing
        // else on the screen competing with it.
        if (result.reply.isNotBlank()) {
            Text(
                result.reply,
                style = MaterialTheme.typography.bodyLarge,
                color = Forge.Parchment,
            )
        }

        if (result.repairs.isNotEmpty()) {
            Spacer(Modifier.height(20.dp))
            SectionLabel("Adjusted while saving", accent = Forge.Ash)
            Spacer(Modifier.height(6.dp))
            result.repairs.forEach { Muted(it) }
        }

        Spacer(Modifier.height(40.dp))
    }
}

@Composable
private fun Stat(label: String, value: String) {
    Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
        Text(label, style = MaterialTheme.typography.labelMedium, color = Forge.Ash)
        Text(
            value,
            style = MaterialTheme.typography.titleMedium,
            color = Forge.Parchment,
        )
    }
}
