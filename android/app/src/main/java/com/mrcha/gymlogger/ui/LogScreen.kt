package com.mrcha.gymlogger.ui

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Mic
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.OutlinedTextFieldDefaults
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.RectangleShape
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.unit.dp
import com.mrcha.gymlogger.net.MuscleReport
import com.mrcha.gymlogger.net.Rank
import com.mrcha.gymlogger.net.Recommendation

/**
 * The LOG screen: the app's front page.
 *
 * Laid out to the mockup, which is built from full-bleed bands rather than
 * floating cards — each block runs edge to edge and is separated from the next
 * by a hairline. That is why nothing here uses ForgePanel: a panel has a border
 * on all four sides and an inset, and stacking them produces the boxed look the
 * mockup is deliberately not.
 */
@Composable
fun LogScreen(
    rank: Rank?,
    next: Recommendation?,
    muscles: MuscleReport?,
    draft: String,
    busy: Boolean,
    connection: ConnectionState,
    onDraftChange: (String) -> Unit,
    onMic: () -> Unit,
    onSubmit: () -> Unit,
    onRetry: () -> Unit,
) {
    Column(Modifier.fillMaxWidth()) {
        // A blank page is the worst possible answer to "is this working". If the
        // server cannot be reached, say so where the rank would have been.
        if (connection != ConnectionState.Ok && rank == null) {
            ConnectionBanner(connection, onRetry)
        }

        rank?.let {
            RankBand(it)
            HairlineRule()
        }

        next?.let {
            UpNextBand(it)
            HairlineRule()
        }

        InputBand(draft, busy, onDraftChange, onMic, onSubmit)

        muscles?.let {
            HairlineRule()
            MuscleBand(it)
        }

        Spacer(Modifier.height(32.dp))
    }
}

/**
 * RANK 61.4 / 100, the name at full bleed, the gradient bar, then the trailing
 * figures. The name is the largest thing in the app by a wide margin, which is
 * the whole point of the mockup: the rank is the product.
 */
@Composable
private fun RankBand(rank: Rank) {
    Column(
        Modifier
            .fillMaxWidth()
            .background(Forge.Panel)
            .padding(horizontal = 18.dp, vertical = 14.dp),
        verticalArrangement = Arrangement.spacedBy(10.dp),
    ) {
        Row(
            Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.SpaceBetween,
        ) {
            Text("RANK", style = MaterialTheme.typography.labelMedium, color = Forge.Ash)
            Text(
                "%.1f / 100".format(rank.rs),
                style = MaterialTheme.typography.labelMedium,
                color = Forge.Bone,
            )
        }

        Text(
            rank.rank,
            style = MaterialTheme.typography.headlineLarge,
            color = Forge.Parchment,
        )

        // Blood into ember, left to right, on a near-black channel.
        RankBar(rank.bandProgress.toFloat())

        Row(
            Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.SpaceBetween,
        ) {
            Text(
                if (rank.nextRank.isNotBlank())
                    "%.1f TO %s".format(rank.toNext, rank.nextRank)
                else "AT THE TOP",
                style = MaterialTheme.typography.labelMedium,
                color = Forge.Ash,
            )
            // The mockup shows two trailing figures. Ours are the two attributes
            // that move fastest and mean most day to day.
            Text(
                "MIGHT %.0f  ·  DISC %.0f".format(
                    rank.attributes.might, rank.attributes.discipline,
                ),
                style = MaterialTheme.typography.labelMedium,
                color = Forge.Ash,
            )
        }
    }
}

@Composable
private fun RankBar(progress: Float) {
    Box(
        Modifier
            .fillMaxWidth()
            .height(6.dp)
            .background(Forge.MuscleDrained),
    ) {
        Box(
            Modifier
                .fillMaxWidth(progress.coerceIn(0f, 1f))
                .height(6.dp)
                .background(Brush.horizontalGradient(listOf(Forge.Blood, Forge.Ember))),
        )
    }
}

@Composable
private fun UpNextBand(rec: Recommendation) {
    val title = when (rec.kind) {
        "train" -> rec.sessionName
        "rest" -> "Rest Day"
        else -> "Nothing Scheduled"
    }
    Column(
        Modifier
            .fillMaxWidth()
            .background(Forge.Panel)
            .padding(horizontal = 18.dp, vertical = 14.dp),
        verticalArrangement = Arrangement.spacedBy(6.dp),
    ) {
        Text("UP NEXT", style = MaterialTheme.typography.labelMedium, color = Forge.Ash)
        Text(
            title.replaceFirstChar { it.uppercase() },
            style = MaterialTheme.typography.headlineSmall,
            color = Forge.Parchment,
        )
        Text(
            rec.reason,
            style = MaterialTheme.typography.bodyMedium,
            color = Forge.Ash,
        )
    }
}

/**
 * The input block. Boxed rather than full-bleed, because it is the one thing on
 * the page you act on rather than read, and the mockup insets it accordingly.
 */
@Composable
private fun InputBand(
    draft: String,
    busy: Boolean,
    onChange: (String) -> Unit,
    onMic: () -> Unit,
    onSubmit: () -> Unit,
) {
    Column(
        Modifier
            .fillMaxWidth()
            .padding(14.dp),
    ) {
        Box(
            Modifier
                .fillMaxWidth()
                .background(Forge.PanelHi)
                .padding(12.dp),
        ) {
            Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
                Text(
                    "SPEAK OR TYPE",
                    style = MaterialTheme.typography.labelMedium,
                    color = Forge.BloodBright,
                )
                OutlinedTextField(
                    value = draft,
                    onValueChange = onChange,
                    modifier = Modifier
                        .fillMaxWidth()
                        .heightIn(min = 64.dp),
                    placeholder = {
                        Text(
                            "bench 100 x 5, 5, 4; dips bw x 12",
                            style = Ledger,
                            color = Forge.Slate,
                        )
                    },
                    enabled = !busy,
                    shape = RectangleShape,
                    textStyle = Ledger,
                    colors = OutlinedTextFieldDefaults.colors(
                        focusedBorderColor = Forge.Steel,
                        unfocusedBorderColor = Forge.Hairline,
                        focusedContainerColor = Forge.PanelHi,
                        unfocusedContainerColor = Forge.PanelHi,
                        cursorColor = Forge.Ember,
                        focusedTextColor = Forge.Parchment,
                        unfocusedTextColor = Forge.Parchment,
                    ),
                    keyboardOptions = KeyboardOptions(imeAction = ImeAction.Default),
                )

                // LOG IT takes the width; the mic is a small square beside it,
                // as in the mockup. Logging is the act, dictation is the input
                // method, and the sizes say which is which.
                Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                    Button(
                        onClick = onSubmit,
                        enabled = !busy && draft.isNotBlank(),
                        modifier = Modifier
                            .weight(1f)
                            .height(48.dp),
                        shape = RectangleShape,
                        colors = ButtonDefaults.buttonColors(
                            containerColor = Forge.Blood,
                            contentColor = Forge.Parchment,
                            disabledContainerColor = Forge.Panel,
                            disabledContentColor = Forge.Slate,
                        ),
                    ) {
                        if (busy) {
                            CircularProgressIndicator(
                                Modifier.size(18.dp),
                                strokeWidth = 2.dp,
                                color = Forge.Ember,
                            )
                        } else {
                            Text("LOG IT", style = MaterialTheme.typography.titleSmall)
                        }
                    }
                    Button(
                        onClick = onMic,
                        enabled = !busy,
                        modifier = Modifier.size(48.dp),
                        shape = RectangleShape,
                        contentPadding = androidx.compose.foundation.layout.PaddingValues(0.dp),
                        border = BorderStroke(1.dp, Forge.Steel),
                        colors = ButtonDefaults.buttonColors(
                            containerColor = Forge.Panel,
                            contentColor = Forge.Bone,
                        ),
                    ) {
                        Icon(Icons.Default.Mic, contentDescription = "Speak")
                    }
                }
            }
        }
    }
}

/** MUSCLE MAP · THIS WEEK, with the total on the right, then the figures. */
@Composable
private fun MuscleBand(report: MuscleReport) {
    val volumes = report.volumes
        .filter { it.sets > 0 }
        .sortedByDescending { it.sets }
        .map { MuscleVolume(it.name, kotlin.math.round(it.sets).toInt()) }

    if (volumes.isEmpty()) return

    val neglected = report.volumes
        .firstOrNull { it.name == report.neglected }
        ?.let { v ->
            if (v.lastTrained.isBlank()) report.neglected + " · NEVER LOGGED"
            else report.neglected + " · 0 SETS IN " + v.daysSince + " D"
        }
        ?: report.neglected.takeIf { it.isNotBlank() }?.let { "$it · NEVER LOGGED" }

    Column(
        Modifier
            .fillMaxWidth()
            .background(Forge.Panel)
            .padding(horizontal = 18.dp, vertical = 14.dp),
        verticalArrangement = Arrangement.spacedBy(10.dp),
    ) {
        Row(
            Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.SpaceBetween,
        ) {
            Text(
                "MUSCLE MAP · THIS WEEK",
                style = MaterialTheme.typography.labelMedium,
                color = Forge.Ash,
            )
            Text(
                "${report.totalSets} SETS",
                style = MaterialTheme.typography.labelMedium,
                color = Forge.Bone,
            )
        }

        MuscleMap(
            volumes = volumes,
            totalSets = report.totalSets,
            neglected = neglected,
        )

        if (report.unmatched.isNotEmpty()) {
            Muted("not mapped to a muscle group: " + report.unmatched.joinToString(", "))
        }
    }
}

/** Whether the phone can currently reach the service. */
enum class ConnectionState { Unknown, Ok, NotConfigured, Unreachable }

/**
 * Shown in place of the rank when there is nothing to show and a reason for it.
 *
 * The app previously rendered an empty page when the server was unreachable,
 * which is indistinguishable from "you have not trained yet" and from "the
 * build is broken".
 */
@Composable
private fun ConnectionBanner(state: ConnectionState, onRetry: () -> Unit) {
    val (title, detail) = when (state) {
        ConnectionState.NotConfigured ->
            "NO SERVER SET" to "Open settings and enter the base URL and auth token."
        ConnectionState.Unreachable ->
            "CANNOT REACH SERVER" to "The address answered nothing. Check the phone is on the same network, and that gymd is running."
        else -> "CONNECTING" to ""
    }
    Column(
        Modifier
            .fillMaxWidth()
            .background(Forge.Panel)
            .padding(18.dp),
        verticalArrangement = Arrangement.spacedBy(10.dp),
    ) {
        Text(title, style = MaterialTheme.typography.labelMedium, color = Forge.BloodBright)
        if (detail.isNotBlank()) {
            Text(detail, style = MaterialTheme.typography.bodyMedium, color = Forge.Ash)
        }
        Button(
            onClick = onRetry,
            shape = RectangleShape,
            colors = ButtonDefaults.buttonColors(
                containerColor = Forge.Blood,
                contentColor = Forge.Parchment,
            ),
        ) {
            Text("RETRY", style = MaterialTheme.typography.titleSmall)
        }
    }
}
