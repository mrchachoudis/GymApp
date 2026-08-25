package com.mrcha.gymlogger.ui

import androidx.compose.animation.core.LinearEasing
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.keyframes
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Matrix
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.drawscope.DrawScope
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.drawscope.clipPath
import androidx.compose.ui.graphics.drawscope.scale
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.material3.Text

/** One trained muscle group and its weekly working-set count. */
data class MuscleVolume(val name: String, val sets: Int)

private const val VW = 120f
private const val VH = 260f
private const val CYCLE = 4200 // ms — one full tide, shared by figures and bars

// ── Body geometry ────────────────────────────────────────────────────────────
// Coordinates live in a 120x260 box and are scaled to the composable's size, so
// the figures stay identical at any height. Limbs are authored once and mirrored.

private class Muscle(val fill: Color, val mirrored: Boolean, val build: Path.() -> Unit)

private fun path(build: Path.() -> Unit) = Path().apply(build)

private val frontMuscles = listOf(
    Muscle(Forge.MuscleLow, false) { // neck
        moveTo(52f, 36f); lineTo(68f, 36f); lineTo(70f, 48f); lineTo(50f, 48f); close()
    },
    Muscle(Forge.MuscleWarm, false) { // clavicular shelf / traps front
        moveTo(50f, 46f); lineTo(70f, 46f); lineTo(84f, 56f); lineTo(36f, 56f); close()
    },
    Muscle(Forge.MuscleMid, false) { // abdominals
        moveTo(49f, 96f); lineTo(71f, 96f); lineTo(71f, 132f)
        cubicTo(71f, 141f, 66f, 147f, 60f, 147f)
        cubicTo(54f, 147f, 49f, 141f, 49f, 132f); close()
    },
    Muscle(Forge.MuscleWarm, true) { // deltoid cap
        moveTo(36f, 56f)
        cubicTo(25f, 59f, 21f, 72f, 26f, 84f)
        cubicTo(36f, 86f, 44f, 78f, 45f, 66f); close()
    },
    Muscle(Forge.MuscleHot, true) { // pectoral
        moveTo(58f, 56f)
        cubicTo(48f, 56f, 40f, 60f, 38f, 70f)
        cubicTo(36f, 82f, 45f, 92f, 58f, 94f); close()
    },
    Muscle(Forge.MuscleWarm, true) { // biceps
        moveTo(28f, 86f)
        cubicTo(21f, 94f, 21f, 112f, 26f, 124f)
        cubicTo(34f, 124f, 38f, 110f, 37f, 92f); close()
    },
    Muscle(Forge.MuscleLow, true) { // forearm
        moveTo(26f, 126f)
        cubicTo(20f, 138f, 20f, 156f, 24f, 168f)
        lineTo(34f, 166f)
        cubicTo(37f, 150f, 35f, 136f, 35f, 128f); close()
    },
    Muscle(Forge.MuscleMid, true) { // oblique
        moveTo(45f, 96f)
        cubicTo(40f, 106f, 42f, 124f, 48f, 136f)
        lineTo(48f, 96f); close()
    },
    Muscle(Forge.MuscleStrong, true) { // quadriceps
        moveTo(47f, 150f)
        cubicTo(40f, 164f, 40f, 190f, 47f, 208f)
        cubicTo(54f, 210f, 58f, 200f, 57f, 182f)
        lineTo(57f, 152f); close()
    },
    Muscle(Forge.MuscleDrained, true) { // calf
        moveTo(49f, 214f)
        cubicTo(43f, 224f, 43f, 240f, 49f, 250f)
        lineTo(56f, 248f)
        cubicTo(58f, 234f, 56f, 220f, 55f, 214f); close()
    },
)

private val backMuscles = listOf(
    Muscle(Forge.MuscleLow, false) { // nape
        moveTo(52f, 36f); lineTo(68f, 36f); lineTo(70f, 46f); lineTo(50f, 46f); close()
    },
    Muscle(Forge.MuscleMid, false) { // erectors
        moveTo(57f, 84f); lineTo(63f, 84f); lineTo(63f, 134f); lineTo(57f, 134f); close()
    },
    Muscle(Forge.MuscleWarm, true) { // rear deltoid
        moveTo(36f, 56f)
        cubicTo(25f, 59f, 21f, 72f, 26f, 84f)
        cubicTo(36f, 86f, 44f, 78f, 45f, 66f); close()
    },
    Muscle(Forge.MuscleWarm, true) { // trapezius
        moveTo(60f, 44f); lineTo(45f, 50f)
        cubicTo(41f, 62f, 47f, 74f, 57f, 82f)
        lineTo(60f, 84f); close()
    },
    Muscle(Forge.MuscleHot, true) { // latissimus
        moveTo(44f, 74f)
        cubicTo(33f, 84f, 34f, 110f, 45f, 130f)
        lineTo(57f, 112f); lineTo(57f, 84f); close()
    },
    Muscle(Forge.MuscleHot, true) { // triceps
        moveTo(28f, 86f)
        cubicTo(21f, 94f, 21f, 112f, 26f, 124f)
        cubicTo(34f, 124f, 38f, 110f, 37f, 92f); close()
    },
    Muscle(Forge.MuscleLow, true) { // forearm
        moveTo(26f, 126f)
        cubicTo(20f, 138f, 20f, 156f, 24f, 168f)
        lineTo(34f, 166f)
        cubicTo(37f, 150f, 35f, 136f, 35f, 128f); close()
    },
    Muscle(Forge.MuscleMid, true) { // gluteal
        moveTo(46f, 134f)
        cubicTo(37f, 137f, 33f, 152f, 41f, 163f)
        cubicTo(50f, 168f, 58f, 161f, 58f, 150f)
        lineTo(58f, 136f); close()
    },
    Muscle(Forge.MuscleMid, true) { // hamstring
        moveTo(47f, 168f)
        cubicTo(42f, 182f, 42f, 198f, 47f, 208f)
        cubicTo(54f, 210f, 57f, 200f, 56f, 184f)
        lineTo(56f, 170f); close()
    },
    Muscle(Forge.MuscleDrained, true) { // calf
        moveTo(49f, 214f)
        cubicTo(43f, 224f, 43f, 240f, 49f, 250f)
        lineTo(56f, 248f)
        cubicTo(58f, 234f, 56f, 220f, 55f, 214f); close()
    },
)

/** Flip a limb across the body's midline: x -> VW - x. */
private fun mirror(p: Path): Path {
    val m = Matrix()
    m.translate(VW, 0f)
    m.scale(-1f, 1f)
    return Path().apply { addPath(p); transform(m) }
}

/** A rising wave: the fill below a crest that sits [top] units down the box. */
private fun wave(top: Float): Path = path {
    moveTo(0f, top)
    var x = 0f
    var up = true
    while (x < VW) {
        val cx = x + 15f
        val cy = if (up) top - 7f else top + 7f
        quadraticTo(cx, cy, x + 30f, top)
        x += 30f
        up = !up
    }
    lineTo(VW, VH + 60f)
    lineTo(0f, VH + 60f)
    close()
}

private fun DrawScope.drawFigure(muscles: List<Muscle>, tide: Float, showAbs: Boolean) {
    scale(size.width / VW, size.height / VH, pivot = Offset.Zero) {
        val outline = Stroke(width = 0.9f)

        fun each(action: (Path, Color) -> Unit) {
            muscles.forEach { m ->
                val p = path(m.build)
                action(p, m.fill)
                if (m.mirrored) action(mirror(p), m.fill)
            }
        }

        // 1. Drained body — the ledger before the week is counted.
        drawOval(
            color = Forge.MuscleDrained,
            topLeft = Offset(47f, 6f),
            size = Size(26f, 32f),
        )
        each { p, _ ->
            drawPath(p, Forge.MuscleDrained)
            drawPath(p, Forge.MuscleOutlineDim, style = outline)
        }

        // 2. Blood tide — saturated muscle revealed only below the crest.
        val crest = VH + 36f - tide * (VH + 60f)
        clipPath(wave(crest)) {
            each { p, fill ->
                drawPath(p, fill)
                drawPath(p, Forge.MuscleOutline, style = outline)
            }
            if (showAbs) {
                drawLine(Forge.MuscleOutline, Offset(60f, 98f), Offset(60f, 144f), 0.8f)
                listOf(109f, 121f, 133f).forEach { y ->
                    drawLine(Forge.MuscleOutline, Offset(51f, y), Offset(69f, y), 0.8f)
                }
            }
        }

        // 3. Waterline — a bright ember band riding the crest, inside the body only.
        clipPath(wave(crest)) {
            clipPath(waveCut(crest + 7f)) {
                each { p, _ -> drawPath(p, Forge.Ember, alpha = 0.95f) }
            }
        }
    }
}

/** Everything ABOVE a crest — used to cut the tide down to a thin surface band. */
private fun waveCut(top: Float): Path = path {
    moveTo(0f, top)
    var x = 0f
    var up = true
    while (x < VW) {
        val cy = if (up) top - 7f else top + 7f
        quadraticTo(x + 15f, cy, x + 30f, top)
        x += 30f
        up = !up
    }
    lineTo(VW, -60f)
    lineTo(0f, -60f)
    close()
}

/**
 * Weekly muscle coverage: two schematic figures that flood blood-red from the
 * feet up on a loop, plus per-group set bars that fill on the same cycle.
 */
@Composable
fun MuscleMap(
    volumes: List<MuscleVolume>,
    totalSets: Int,
    neglected: String?,
    modifier: Modifier = Modifier,
    figureHeight: Int = 132,
) {
    val tide = rememberInfiniteTransition(label = "tide").animateFloat(
        initialValue = 0f,
        targetValue = 1f,
        animationSpec = infiniteRepeatable(
            animation = keyframes {
                durationMillis = CYCLE
                0f at 0 using LinearEasing
                1f at (CYCLE * 0.52f).toInt() using LinearEasing
                1f at (CYCLE * 0.88f).toInt() using LinearEasing
                0f at CYCLE
            },
            repeatMode = RepeatMode.Restart,
        ),
        label = "tideLevel",
    )

    Column(modifier, verticalArrangement = Arrangement.spacedBy(9.dp)) {
        Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.Bottom) {
            Text(
                "MUSCLE MAP · THIS WEEK",
                color = Forge.Slate,
                fontSize = 13.sp,
                letterSpacing = 2.8.sp,
                modifier = Modifier.weight(1f),
            )
            Text(
                "$totalSets SETS",
                color = Color(0xFF6E7278),
                fontSize = 12.sp,
                fontFamily = FontFamily.Monospace,
            )
        }

        Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
            Row(horizontalArrangement = Arrangement.spacedBy(4.dp)) {
                Canvas(
                    Modifier
                        .height(figureHeight.dp)
                        .width((figureHeight * VW / VH).dp),
                ) { drawFigure(frontMuscles, tide.value, showAbs = true) }
                Canvas(
                    Modifier
                        .height(figureHeight.dp)
                        .width((figureHeight * VW / VH).dp),
                ) { drawFigure(backMuscles, tide.value, showAbs = false) }
            }

            Column(
                Modifier.weight(1f),
                verticalArrangement = Arrangement.spacedBy(6.dp),
            ) {
                val peak = remember(volumes) { (volumes.maxOfOrNull { it.sets } ?: 1).coerceAtLeast(1) }
                volumes.forEachIndexed { i, v ->
                    SetBar(v, peak, tide.value, i)
                }
            }
        }

        if (neglected != null) {
            Spacer(Modifier.height(2.dp))
            Row(
                Modifier
                    .fillMaxWidth()
                    .background(Forge.Hairline)
                    .height(1.dp),
            ) {}
            Row(Modifier.fillMaxWidth().padding(top = 8.dp)) {
                Text(
                    "NEGLECTED",
                    color = Forge.Ash,
                    fontSize = 13.sp,
                    fontFamily = FontFamily.Monospace,
                    modifier = Modifier.weight(1f),
                )
                Text(
                    neglected,
                    color = Forge.BloodBright,
                    fontSize = 13.sp,
                    fontFamily = FontFamily.Monospace,
                    textAlign = TextAlign.End,
                )
            }
        }
    }
}

@Composable
private fun SetBar(v: MuscleVolume, peak: Int, tide: Float, index: Int) {
    val hot = v.sets >= 12
    val fraction = (v.sets.toFloat() / peak).coerceIn(0f, 1f)
    // Each bar lags the one above it, so the row reads as filling rather than snapping.
    val lag = (index * 0.022f)
    val fill = ((tide - lag) / 0.52f).coerceIn(0f, 1f) * fraction

    Column(verticalArrangement = Arrangement.spacedBy(3.dp)) {
        Row(Modifier.fillMaxWidth()) {
            Text(
                v.name,
                color = Forge.Ash,
                fontSize = 12.sp,
                fontFamily = FontFamily.Monospace,
                modifier = Modifier.weight(1f),
            )
            Text(
                v.sets.toString(),
                color = if (hot) Color(0xFFE8E2D4) else Forge.Ash,
                fontSize = 12.sp,
                fontWeight = if (hot) FontWeight.SemiBold else FontWeight.Normal,
                fontFamily = FontFamily.Monospace,
            )
        }
        Box(
            Modifier
                .fillMaxWidth()
                .height(3.dp)
                .background(Color(0xFF1A1416)),
        ) {
            Box(
                Modifier
                    .fillMaxWidth(fill)
                    .fillMaxSize()
                    .background(
                        Brush.horizontalGradient(
                            listOf(Forge.Blood, Forge.BloodBright, Forge.Ember),
                        ),
                    ),
            )
        }
    }
}
