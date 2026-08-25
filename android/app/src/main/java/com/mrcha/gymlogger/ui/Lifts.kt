package com.mrcha.gymlogger.ui

import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.systemBarsPadding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.Search
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import com.mrcha.gymlogger.net.LiftDetail
import com.mrcha.gymlogger.net.LiftSummary

/**
 * LIFTS: what you actually train, most recent first.
 *
 * Scrolls itself, with a lazy list. Both matter: the shell is deliberately
 * rigid, because a scrolling parent measures its children with an infinite
 * maximum height and a lazy list measured that way throws rather than
 * degrading. That combination is what crashed this tab.
 */
@Composable
fun LiftsScreen(
    lifts: List<LiftSummary>,
    onOpen: (String) -> Unit,
    onBrowseLibrary: () -> Unit,
) {
    Column(Modifier.fillMaxSize()) {
        Row(
            Modifier
                .fillMaxWidth()
                .padding(horizontal = 18.dp),
            horizontalArrangement = Arrangement.SpaceBetween,
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text("YOUR LIFTS", style = MaterialTheme.typography.labelMedium, color = Forge.Ash)
            // The 1,318-movement library is a different thing from the handful
            // you actually train, so it lives behind a search rather than being
            // the first thing on this screen.
            IconButton(onClick = onBrowseLibrary) {
                Icon(Icons.Default.Search, contentDescription = "Browse library", tint = Forge.Ash)
            }
        }

        if (lifts.isEmpty()) {
            Text(
                "Nothing logged yet. Log a session and it will appear here.",
                style = MaterialTheme.typography.bodyMedium,
                color = Forge.Slate,
                modifier = Modifier.padding(18.dp),
            )
            return
        }

        LazyColumn(Modifier.fillMaxSize()) {
            items(lifts, key = { it.key }) { lift ->
                Row(
                    Modifier
                        .fillMaxWidth()
                        .clickable { onOpen(lift.key) }
                        .padding(horizontal = 18.dp, vertical = 14.dp),
                    horizontalArrangement = Arrangement.SpaceBetween,
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    Column(Modifier.weight(1f)) {
                        Text(
                            lift.display.uppercase(),
                            style = MaterialTheme.typography.titleLarge,
                            color = Forge.Parchment,
                        )
                        Text(
                            "${lift.equipment} · ${lift.sessions} sessions",
                            style = MaterialTheme.typography.bodySmall,
                            color = Forge.Slate,
                        )
                    }
                    // Bodyweight lifts trained only above eight reps have no
                    // honest estimated max, so the column is left blank rather
                    // than showing a zero that looks like a regression.
                    if (lift.bestE1RM > 0) {
                        Text(
                            "%.0f".format(lift.bestE1RM),
                            style = MaterialTheme.typography.titleMedium,
                            color = Forge.Bone,
                        )
                    }
                }
                HairlineRule()
            }
            item { Spacer(Modifier.height(24.dp)) }
        }
    }
}

/**
 * One lift in full: the mockup's third panel.
 *
 * Header, title, badges, the twelve-week estimated-max chart, the record book,
 * and the next load to attempt.
 */
@Composable
fun LiftDetailScreen(detail: LiftDetail?, onClose: () -> Unit) {
    if (detail == null) {
        Column(Modifier.fillMaxSize().background(Forge.Ground).systemBarsPadding()) {
            Text(
                "LOADING",
                Modifier.padding(20.dp),
                style = MaterialTheme.typography.labelMedium,
                color = Forge.Slate,
            )
        }
        return
    }

    Column(
        Modifier
            .fillMaxSize()
            .background(Forge.Ground)
            .systemBarsPadding()
            .verticalScroll(rememberScrollState()),
    ) {
        // Header band.
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
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Text(
                    listOf(detail.equipment, detail.loadBasis, "${detail.sessions} SESSIONS")
                        .filter { it.isNotBlank() }
                        .joinToString(" · ")
                        .uppercase(),
                    style = MaterialTheme.typography.labelMedium,
                    color = Forge.Ash,
                )
                IconButton(onClick = onClose) {
                    Icon(Icons.Default.Close, contentDescription = "Close", tint = Forge.Ash)
                }
            }

            Text(
                detail.display.uppercase(),
                style = MaterialTheme.typography.headlineMedium,
                color = Forge.Parchment,
            )

            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                if (detail.level.isNotBlank()) {
                    Badge(detail.level, filled = true)
                }
                if (detail.bwMultiple > 0) {
                    Badge("%.2f x BW".format(detail.bwMultiple), filled = false)
                }
            }
        }

        HairlineRule()

        SectionBand("ESTIMATED MAX · 12 WEEKS") {
            E1RMChart(detail)
        }

        HairlineRule()

        if (detail.records.isNotEmpty()) {
            SectionBand("RECORD BOOK") {
                detail.records.forEach { r ->
                    Row(
                        Modifier.fillMaxWidth().padding(vertical = 5.dp),
                        horizontalArrangement = Arrangement.SpaceBetween,
                    ) {
                        Text(r.label, style = Ledger, color = Forge.Ash)
                        Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                            Text(r.value, style = Ledger, color = Forge.Parchment)
                            Text(
                                r.date,
                                style = Ledger,
                                // A record set today is the reason to open this
                                // screen, so it is the only coloured thing here.
                                color = if (r.isToday) Forge.Ember else Forge.Slate,
                            )
                        }
                    }
                }
            }
            HairlineRule()
        }

        if (detail.nextStep.isNotBlank()) {
            Column(
                Modifier
                    .fillMaxWidth()
                    .padding(14.dp)
                    .background(Forge.Blood)
                    .padding(16.dp),
                verticalArrangement = Arrangement.spacedBy(6.dp),
            ) {
                Text(
                    "NEXT STEP",
                    style = MaterialTheme.typography.labelMedium,
                    color = Forge.Parchment,
                )
                Text(
                    detail.nextStep,
                    style = MaterialTheme.typography.titleLarge,
                    color = Forge.Parchment,
                )
                if (detail.nextStepWhy.isNotBlank()) {
                    Text(
                        detail.nextStepWhy,
                        style = MaterialTheme.typography.bodySmall,
                        color = Forge.Bone,
                    )
                }
            }
        }

        Spacer(Modifier.height(32.dp))
    }
}

/**
 * Twelve weekly bars, the most recent picked out in ember.
 *
 * Bars are scaled between a floor slightly below the minimum and the maximum,
 * not from zero. A bench that moved 100 to 105 over twelve weeks drawn from
 * zero is twelve bars of identical height, which shows nothing; the point of
 * the chart is the shape of the change.
 *
 * Weeks with no training draw as a 2dp stub in steel rather than nothing, so a
 * gap reads as "no session" instead of as a missing bar.
 */
@Composable
private fun E1RMChart(detail: LiftDetail) {
    val pts = detail.series
    if (pts.isEmpty()) return

    val vals = pts.filter { !it.empty }.map { it.e1rm }
    if (vals.isEmpty()) {
        Muted(
            detail.seriesNote.ifBlank {
                "No qualifying sets in the last twelve weeks."
            },
        )
        return
    }
    val max = vals.max()
    val min = vals.min()
    val floor = if (max == min) min * 0.9 else min - (max - min) * 0.35
    val span = (max - floor).coerceAtLeast(0.1)
    val lastFilled = pts.indexOfLast { !it.empty }

    Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
        Canvas(
            Modifier
                .fillMaxWidth()
                .height(150.dp),
        ) {
            val n = pts.size
            val gap = 6f
            val barW = (size.width - gap * (n - 1)) / n
            pts.forEachIndexed { i, p ->
                val x = i * (barW + gap)
                if (p.empty) {
                    drawRect(
                        color = Forge.Hairline,
                        topLeft = Offset(x, size.height - 2f),
                        size = Size(barW, 2f),
                    )
                    return@forEachIndexed
                }
                val h = (((p.e1rm - floor) / span).toFloat() * size.height)
                    .coerceIn(3f, size.height)
                drawRect(
                    brush = if (i == lastFilled) {
                        Brush.verticalGradient(listOf(Forge.Ember, Forge.Blood))
                    } else {
                        Brush.verticalGradient(listOf(Forge.Steel, Forge.MuscleMid))
                    },
                    topLeft = Offset(x, size.height - h),
                    size = Size(barW, h),
                )
            }
        }

        Row(Modifier.fillMaxWidth()) {
            pts.forEach { p ->
                Text(
                    p.week,
                    style = MaterialTheme.typography.bodySmall,
                    color = Forge.Slate,
                    textAlign = TextAlign.Center,
                    modifier = Modifier.weight(1f),
                )
            }
        }

        Row(
            Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.SpaceBetween,
        ) {
            Muted("low %.1f".format(min))
            Muted("best %.1f kg".format(max))
        }
    }
}

@Composable
private fun SectionBand(title: String, content: @Composable () -> Unit) {
    Column(
        Modifier
            .fillMaxWidth()
            .background(Forge.Panel)
            .padding(horizontal = 18.dp, vertical = 14.dp),
        verticalArrangement = Arrangement.spacedBy(10.dp),
    ) {
        Text(title, style = MaterialTheme.typography.labelMedium, color = Forge.Ash)
        content()
    }
}

@Composable
private fun Badge(text: String, filled: Boolean) {
    Box(
        Modifier
            .background(if (filled) Forge.Blood else Forge.PanelHi)
            .padding(horizontal = 10.dp, vertical = 5.dp),
    ) {
        Text(
            text,
            style = MaterialTheme.typography.labelMedium,
            color = if (filled) Forge.Parchment else Forge.Bone,
        )
    }
}
