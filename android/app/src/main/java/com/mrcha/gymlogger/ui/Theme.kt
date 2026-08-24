package com.mrcha.gymlogger.ui

import android.os.Build
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.dynamicDarkColorScheme
import androidx.compose.material3.dynamicLightColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext

// A restrained palette. This app gets opened mid-set in a garage, so contrast
// matters more than personality.
private val Dark = darkColorScheme(
    primary = Color(0xFFB8C7FF),
    secondary = Color(0xFFC3C6CF),
    background = Color(0xFF111318),
    surface = Color(0xFF111318),
)

private val Light = lightColorScheme(
    primary = Color(0xFF3A5BC7),
    secondary = Color(0xFF585E71),
)

@Composable
fun GymLoggerTheme(
    darkTheme: Boolean = isSystemInDarkTheme(),
    content: @Composable () -> Unit,
) {
    val context = LocalContext.current
    val colors = when {
        Build.VERSION.SDK_INT >= Build.VERSION_CODES.S ->
            if (darkTheme) dynamicDarkColorScheme(context) else dynamicLightColorScheme(context)
        darkTheme -> Dark
        else -> Light
    }
    MaterialTheme(colorScheme = colors, content = content)
}
