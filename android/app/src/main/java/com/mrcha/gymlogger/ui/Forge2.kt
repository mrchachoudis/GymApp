package com.mrcha.gymlogger.ui

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.ui.graphics.RectangleShape
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.dp

/**
 * Shared Forge surfaces.
 *
 * The look is built from three things and nothing else: a near-black panel, a
 * one-pixel worn-steel hairline, and square corners. Material's default card is
 * a rounded, elevated, tinted rectangle — three decisions all pulling the other
 * way — so every panel in the app goes through here instead.
 */

/** A panel. Square, hairlined, unelevated. */
@Composable
fun ForgePanel(
    modifier: Modifier = Modifier,
    accent: Color? = null,
    content: @Composable ColumnScopeAlias.() -> Unit,
) {
    Card(
        modifier = modifier.fillMaxWidth(),
        shape = RectangleShape,
        colors = CardDefaults.cardColors(containerColor = Forge.Panel),
        elevation = CardDefaults.cardElevation(defaultElevation = 0.dp),
        border = BorderStroke(1.dp, accent ?: Forge.Hairline),
    ) {
        Column(
            Modifier.padding(16.dp),
            verticalArrangement = Arrangement.spacedBy(10.dp),
            content = content,
        )
    }
}

// Compose's ColumnScope is what the lambda above actually receives; aliasing it
// keeps the call sites free of an import that reads like noise.
typealias ColumnScopeAlias = androidx.compose.foundation.layout.ColumnScope

/**
 * A section heading: uppercase, letterspaced, with a rule that fades out to the
 * right. The fade is the one flourish in the whole system — a hard rule reads
 * as a table border, a fading one reads as an engraving.
 */
@Composable
fun SectionLabel(text: String, modifier: Modifier = Modifier, accent: Color = Forge.Bone) {
    Row(
        modifier.fillMaxWidth(),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(10.dp),
    ) {
        Text(
            text.uppercase(),
            style = SectionLabelStyle,
            color = accent,
        )
        Box(
            Modifier
                .weight(1f)
                .height(1.dp)
                .background(
                    Brush.horizontalGradient(
                        listOf(Forge.Hairline, Color.Transparent),
                    ),
                ),
        )
    }
}

/** A key/value row set in the ledger face, so columns of numbers line up. */
@Composable
fun LedgerRow(
    label: String,
    value: String,
    modifier: Modifier = Modifier,
    labelColor: Color = Forge.Ash,
    valueColor: Color = Forge.Parchment,
    trailing: (@Composable () -> Unit)? = null,
) {
    Row(
        modifier.fillMaxWidth(),
        horizontalArrangement = Arrangement.SpaceBetween,
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(label, style = Ledger, color = labelColor)
        Row(
            horizontalArrangement = Arrangement.spacedBy(8.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(value, style = Ledger, color = valueColor)
            trailing?.invoke()
        }
    }
}

/**
 * A thin horizontal meter.
 *
 * Material's LinearProgressIndicator is a rounded pill with a tinted track.
 * This is a 3dp bar on a near-black channel: it reads as a gauge cut into the
 * panel rather than a widget sitting on it.
 */
@Composable
fun ForgeMeter(
    progress: Float,
    modifier: Modifier = Modifier,
    color: Color = Forge.Ember,
) {
    Box(
        modifier
            .fillMaxWidth()
            .height(3.dp)
            .background(Forge.MuscleDrained),
    ) {
        Box(
            Modifier
                .fillMaxWidth(progress.coerceIn(0f, 1f))
                .height(3.dp)
                .background(
                    Brush.horizontalGradient(listOf(Forge.Blood, color)),
                ),
        )
    }
}

/** Muted body copy — notes, explanations, the small print under a control. */
@Composable
fun Muted(text: String, modifier: Modifier = Modifier) {
    Text(
        text,
        modifier = modifier,
        style = MaterialTheme.typography.bodySmall,
        color = Forge.Slate,
    )
}

/** A short vertical spacer used between stacked panels. */
@Composable
fun Gap(height: Int = 12) {
    Box(Modifier.height(height.dp).width(1.dp))
}

/**
 * A full-width hairline. Used between list rows, where Material's divider is
 * too bright against an obsidian ground and reads as a table grid.
 */
@Composable
fun HairlineRule(modifier: Modifier = Modifier) {
    Box(
        modifier
            .fillMaxWidth()
            .height(1.dp)
            .background(Forge.Hairline),
    )
}
