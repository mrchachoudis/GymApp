package com.mrcha.gymlogger.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.rotate
import androidx.compose.ui.layout.layout
import androidx.compose.ui.unit.dp

/** The four places the app goes. */
enum class Rail(val label: String) {
    Log("LOG"),
    Ledger("LEDGER"),
    Lifts("LIFTS"),
    RankTab("RANK"),
}

/**
 * The vertical navigation rail down the left edge.
 *
 * It replaces the top-bar icons, and the reason is not decoration: the mockup
 * gives the rank name the full width of the screen, and a title bar with three
 * action icons in it steals that width back. Rotated labels cost 44dp and read
 * as part of the binding of a ledger rather than as chrome bolted on top.
 *
 * The red block at the top is the only saturated thing in the rail — it marks
 * the app rather than any one tab, so the tabs themselves can stay quiet.
 */
@Composable
fun SideRail(
    selected: Rail,
    onSelect: (Rail) -> Unit,
    modifier: Modifier = Modifier,
) {
    Column(
        modifier
            .fillMaxHeight()
            .width(44.dp)
            .background(Forge.Panel),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Box(
            Modifier
                .padding(top = 10.dp)
                .size(22.dp)
                .background(Forge.Blood),
        )
        Column(
            Modifier.padding(top = 18.dp),
            verticalArrangement = Arrangement.spacedBy(10.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            Rail.entries.forEach { tab ->
                RailTab(tab, tab == selected) { onSelect(tab) }
            }
        }
    }
}

@Composable
private fun RailTab(tab: Rail, active: Boolean, onClick: () -> Unit) {
    Box(
        Modifier
            .clickable(onClick = onClick)
            .padding(vertical = 8.dp),
        contentAlignment = Alignment.Center,
    ) {
        Text(
            tab.label,
            style = MaterialTheme.typography.labelMedium,
            color = if (active) Forge.Ember else Forge.Slate,
            modifier = Modifier
                .rotateVertically()
                .padding(vertical = 4.dp),
        )
    }
}

/**
 * Rotates a composable a quarter turn and swaps its measured width and height.
 *
 * `Modifier.rotate` alone rotates the drawing but leaves the layout box the
 * original shape, so a rotated label reserves a wide, short slot and overlaps
 * its neighbours. Measuring with swapped constraints is what makes the rail
 * actually stack.
 */
private fun Modifier.rotateVertically(): Modifier =
    layout { measurable, constraints ->
        val placeable = measurable.measure(
            constraints.copy(
                minWidth = constraints.minHeight,
                maxWidth = constraints.maxHeight,
                minHeight = constraints.minWidth,
                maxHeight = constraints.maxWidth,
            ),
        )
        layout(placeable.height, placeable.width) {
            placeable.place(
                x = -(placeable.width / 2 - placeable.height / 2),
                y = -(placeable.height / 2 - placeable.width / 2),
            )
        }
    }.rotate(-90f)

/** A hairline that runs the full height, separating the rail from content. */
@Composable
fun RailDivider() {
    Box(
        Modifier
            .fillMaxHeight()
            .width(1.dp)
            .background(Forge.Hairline),
    )
}

/** Spacer used where the mockup leaves air between stacked blocks. */
@Composable
fun VGap(h: Int) = Box(Modifier.height(h.dp))
