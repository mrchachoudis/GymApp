package com.mrcha.gymlogger.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.Search
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilterChip
import androidx.compose.material3.FilterChipDefaults
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.foundation.BorderStroke
import androidx.compose.ui.graphics.RectangleShape
import androidx.compose.material3.OutlinedTextFieldDefaults
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.unit.dp
import coil.ImageLoader
import coil.compose.AsyncImage
import coil.request.ImageRequest
import com.mrcha.gymlogger.net.Exercise
import com.mrcha.gymlogger.net.Facets

/**
 * The exercise library: 1,318 movements, searchable, with instructions and an
 * animated demo.
 *
 * Search runs on the server rather than over a downloaded copy. The library is
 * 837 KB of JSON and it would have to be shipped, stored and kept in step with
 * the service; a query round-trip is cheaper than all three, and the service
 * already owns the ranking that puts the plain barbell squat above forty
 * variants of it.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun LibraryScreen(
    state: LibraryState,
    imageLoader: ImageLoader,
    mediaUrl: (kind: String, file: String) -> String,
    onQueryChange: (String) -> Unit,
    onFilter: (equipment: String, target: String) -> Unit,
    onLoadMore: () -> Unit,
    onClose: () -> Unit,
) {
    var selected by remember { mutableStateOf<Exercise?>(null) }

    Scaffold(
        containerColor = Forge.Ground,
        topBar = {
            TopAppBar(
                colors = TopAppBarDefaults.topAppBarColors(
                    containerColor = Forge.Ground,
                    titleContentColor = Forge.Bone,
                    actionIconContentColor = Forge.Ash,
                ),
                title = {
                    Text("EXERCISES", style = MaterialTheme.typography.titleSmall)
                },
                actions = {
                    IconButton(onClick = onClose) {
                        Icon(Icons.Default.Close, contentDescription = "Close")
                    }
                },
            )
        },
    ) { padding ->
        Column(Modifier.padding(padding).fillMaxSize()) {
            OutlinedTextField(
                value = state.query,
                onValueChange = onQueryChange,
                modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp),
                placeholder = {
                    Text("incline press", color = Forge.Slate)
                },
                leadingIcon = {
                    Icon(Icons.Default.Search, contentDescription = null, tint = Forge.Ash)
                },
                singleLine = true,
                shape = RectangleShape,
                colors = OutlinedTextFieldDefaults.colors(
                    focusedBorderColor = Forge.Ember,
                    unfocusedBorderColor = Forge.Hairline,
                    focusedContainerColor = Forge.Panel,
                    unfocusedContainerColor = Forge.Panel,
                    cursorColor = Forge.Ember,
                    focusedTextColor = Forge.Parchment,
                    unfocusedTextColor = Forge.Parchment,
                ),
                keyboardOptions = KeyboardOptions(imeAction = ImeAction.Search),
            )

            FilterRow(state, onFilter)

            Text(
                if (state.total == 0 && !state.loading) "NOTHING MATCHES"
                else "SHOWING ${state.exercises.size} OF ${state.total}",
                style = MaterialTheme.typography.labelMedium,
                color = Forge.Slate,
                modifier = Modifier.padding(horizontal = 16.dp, vertical = 8.dp),
            )

            LazyColumn(Modifier.fillMaxSize()) {
                items(state.exercises, key = { it.id }) { ex ->
                    ExerciseRow(ex, imageLoader, mediaUrl) { selected = ex }
                    HairlineRule()
                }
                // Paging on reaching the end, rather than a button: the list is
                // long and the user is already scrolling.
                if (state.exercises.size < state.total) {
                    item {
                        LaunchedEffect(state.exercises.size) { onLoadMore() }
                        Box(Modifier.fillMaxWidth().padding(16.dp), Alignment.Center) {
                            CircularProgressIndicator(Modifier.size(20.dp), strokeWidth = 2.dp)
                        }
                    }
                }
            }
        }
    }

    selected?.let { ex ->
        ModalBottomSheet(
            onDismissRequest = { selected = null },
            containerColor = Forge.Panel,
            contentColor = Forge.Parchment,
            sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true),
        ) {
            ExerciseDetail(ex, imageLoader, mediaUrl)
        }
    }
}

/** Filter chips, built from the server's facets so they cannot rot. */
@Composable
private fun FilterRow(state: LibraryState, onFilter: (String, String) -> Unit) {
    val facets = state.facets ?: return
    LazyRow(
        Modifier.fillMaxWidth().padding(vertical = 4.dp),
        horizontalArrangement = Arrangement.spacedBy(8.dp),
        contentPadding = androidx.compose.foundation.layout.PaddingValues(horizontal = 16.dp),
    ) {
        // Only the eight most common pieces of equipment get a chip. All
        // twenty-eight would be a wall of scrolling, and the tail is things
        // like "wheel roller" with one exercise behind it.
        items(facets.equipment.take(8)) { f ->
            val active = state.equipment == f.value
            FilterChip(
                selected = active,
                onClick = { onFilter(if (active) "" else f.value, state.target) },
                label = {
                    Text("${f.value} ${f.count}", style = MaterialTheme.typography.bodySmall)
                },
                shape = RectangleShape,
                border = BorderStroke(1.dp, if (active) Forge.Ember else Forge.Hairline),
                colors = FilterChipDefaults.filterChipColors(
                    containerColor = Forge.Panel,
                    labelColor = Forge.Ash,
                    selectedContainerColor = Forge.Blood,
                    selectedLabelColor = Forge.Parchment,
                ),
            )
        }
    }
}

@Composable
private fun ExerciseRow(
    ex: Exercise,
    imageLoader: ImageLoader,
    mediaUrl: (String, String) -> String,
    onClick: () -> Unit,
) {
    Row(
        Modifier.fillMaxWidth().clickable(onClick = onClick).padding(16.dp),
        horizontalArrangement = Arrangement.spacedBy(12.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        // The still image, not the animation: a list of fifty running GIFs is
        // unreadable and costs 96 KB each. The animation is in the detail sheet.
        Demo(
            url = mediaUrl("img", ex.image),
            loader = imageLoader,
            modifier = Modifier.size(56.dp),
        )
        Column(Modifier.weight(1f)) {
            Text(ex.name, style = MaterialTheme.typography.bodyLarge, color = Forge.Parchment)
            Text(
                "${ex.target} · ${ex.equipment}",
                style = MaterialTheme.typography.bodySmall,
                color = Forge.Slate,
            )
        }
    }
}

@Composable
private fun ExerciseDetail(
    ex: Exercise,
    imageLoader: ImageLoader,
    mediaUrl: (String, String) -> String,
) {
    Column(
        Modifier
            .fillMaxWidth()
            .verticalScroll(rememberScrollState())
            .padding(horizontal = 20.dp)
            .padding(bottom = 32.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        Text(
            ex.name.uppercase(),
            style = MaterialTheme.typography.titleLarge,
            color = Forge.Ember,
        )
        Text(
            "${ex.bodyPart} · ${ex.target} · ${ex.equipment}",
            style = MaterialTheme.typography.bodySmall,
            color = Forge.Ash,
        )
        if (ex.secondary.isNotEmpty()) {
            Muted("also works: " + ex.secondary.joinToString(", "))
        }
        SectionLabel("Execution")

        Demo(
            url = mediaUrl("gif", ex.animation),
            loader = imageLoader,
            modifier = Modifier.fillMaxWidth().aspectRatio(1f),
        )

        ex.steps.forEachIndexed { i, step ->
            Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                Text("${i + 1}", style = Ledger, color = Forge.Ember)
                Text(step, style = MaterialTheme.typography.bodyMedium, color = Forge.Parchment)
            }
        }
    }
}

/**
 * A demo image. Renders nothing when the server has no media configured, which
 * is the default: the media is 139 MB and lives outside the repository, so a
 * fresh instance shows a library with instructions and no pictures rather than
 * a grid of broken-image icons.
 */
@Composable
private fun Demo(url: String, loader: ImageLoader, modifier: Modifier = Modifier) {
    var failed by remember(url) { mutableStateOf(false) }
    if (failed) {
        Spacer(modifier)
        return
    }
    AsyncImage(
        model = ImageRequest.Builder(androidx.compose.ui.platform.LocalContext.current)
            .data(url)
            .crossfade(true)
            .build(),
        imageLoader = loader,
        contentDescription = null,
        contentScale = ContentScale.Fit,
        onError = { failed = true },
        modifier = modifier,
    )
}

/** Everything the library screen renders, held by the view model. */
data class LibraryState(
    val query: String = "",
    val equipment: String = "",
    val target: String = "",
    val exercises: List<Exercise> = emptyList(),
    val total: Int = 0,
    val loading: Boolean = false,
    val facets: Facets? = null,
)
