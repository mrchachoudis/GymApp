package com.mrcha.gymlogger.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Typography
import androidx.compose.material3.darkColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.Font
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import com.mrcha.gymlogger.R
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.sp

/**
 * The Forge theme: a warrior's training ledger.
 *
 * Obsidian ground, worn-steel hairlines, parchment text, and blood used only
 * where it means something — a PR, a pain flag, the primary action. Everything
 * else is bone and ash.
 *
 * Three deliberate departures from the Material default:
 *
 *  1. **No dynamic colour.** Material You derives the palette from the user's
 *     wallpaper, which is the single thing most at odds with a designed look:
 *     the app would be lilac on one phone and mint on another. The palette is
 *     fixed.
 *  2. **No light scheme.** This is a dark design; a light variant would be a
 *     different one wearing the same name. `darkTheme` is accepted and ignored
 *     so callers do not have to care.
 *  3. **Monospace for numbers.** Loads, scores and gate readouts are columns of
 *     figures that have to line up. Proportional digits make a rank table look
 *     like prose.
 */
private val ForgeColors = darkColorScheme(
    primary = Forge.Ember,
    onPrimary = Forge.Ground,
    primaryContainer = Forge.Blood,
    onPrimaryContainer = Forge.Parchment,

    secondary = Forge.Bone,
    onSecondary = Forge.Ground,
    secondaryContainer = Forge.PanelHi,
    onSecondaryContainer = Forge.Bone,

    tertiary = Forge.Steel,
    onTertiary = Forge.Parchment,

    background = Forge.Ground,
    onBackground = Forge.Parchment,

    surface = Forge.Panel,
    onSurface = Forge.Parchment,
    surfaceVariant = Forge.PanelHi,
    onSurfaceVariant = Forge.Ash,

    // Error is blood-bright rather than the Material red, because a pain flag
    // and a PR should read as the same family of "this matters".
    error = Forge.BloodBright,
    onError = Forge.Parchment,
    errorContainer = Forge.Blood,
    onErrorContainer = Forge.Parchment,

    outline = Forge.Steel,
    outlineVariant = Forge.Hairline,
)

/**
 * The display face: Grenze Gotisch, SIL OFL (see assets/FONT_LICENSE.txt).
 *
 * It carries the whole identity of the design — the rank name, the session
 * verdict, a lift's title — and it is used ONLY for those. A blackletter set at
 * body size is unreadable, and one used everywhere stops meaning anything.
 */
val Display = FontFamily(Font(R.font.blackletter, FontWeight.Bold))

/** Numbers that have to line up in a column. */
val Ledger = TextStyle(
    fontFamily = FontFamily.Monospace,
    fontWeight = FontWeight.Normal,
    fontSize = 13.sp,
    lineHeight = 18.sp,
)

private val ForgeType = Typography(
    // Rank names. Wide letterspacing so "BLACK SWORDSMAN" reads as an inscription
    // rather than a label.
    // Rank names, session verdicts, lift titles. Blackletter, large, tight:
    // the mockup sets these as an inscription rather than a heading.
    headlineLarge = TextStyle(
        fontFamily = Display,
        fontWeight = FontWeight.Bold,
        fontSize = 46.sp,
        lineHeight = 48.sp,
        letterSpacing = 0.sp,
    ),
    headlineMedium = TextStyle(
        fontFamily = Display,
        fontWeight = FontWeight.Bold,
        fontSize = 34.sp,
        lineHeight = 36.sp,
    ),
    headlineSmall = TextStyle(
        fontFamily = Display,
        fontWeight = FontWeight.Bold,
        fontSize = 26.sp,
        lineHeight = 28.sp,
    ),
    titleLarge = TextStyle(
        fontWeight = FontWeight.SemiBold,
        fontSize = 19.sp,
        letterSpacing = 0.5.sp,
    ),
    titleMedium = TextStyle(
        fontFamily = FontFamily.Monospace,
        fontWeight = FontWeight.Bold,
        fontSize = 16.sp,
    ),
    // Section headings, set as small caps-ish labels.
    titleSmall = TextStyle(
        fontWeight = FontWeight.Bold,
        fontSize = 12.sp,
        letterSpacing = 1.8.sp,
    ),
    labelMedium = TextStyle(
        fontWeight = FontWeight.Medium,
        fontSize = 11.sp,
        letterSpacing = 1.4.sp,
    ),
    bodyLarge = TextStyle(fontSize = 15.sp, lineHeight = 21.sp),
    bodyMedium = TextStyle(fontSize = 14.sp, lineHeight = 20.sp),
    bodySmall = TextStyle(fontSize = 12.sp, lineHeight = 17.sp),
)

@Composable
fun GymLoggerTheme(
    @Suppress("UNUSED_PARAMETER") darkTheme: Boolean = true,
    content: @Composable () -> Unit,
) {
    MaterialTheme(
        colorScheme = ForgeColors,
        typography = ForgeType,
        content = content,
    )
}

/** Section heading, uppercased with a rule under it. Used across every screen. */
val SectionLabelStyle = TextStyle(
    fontWeight = FontWeight.Bold,
    fontSize = 11.sp,
    letterSpacing = 2.sp,
    textAlign = TextAlign.Start,
)
