package com.mrcha.gymlogger.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Close
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.Checkbox
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilterChip
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.foundation.BorderStroke
import androidx.compose.ui.graphics.RectangleShape
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.CheckboxDefaults
import androidx.compose.material3.FilterChipDefaults
import androidx.compose.material3.OutlinedTextFieldDefaults
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import com.mrcha.gymlogger.net.Profile

/**
 * The profile screen, which is where the rank system gets the inputs it cannot
 * derive from logged sets.
 *
 * This exists because the engine was running against a placeholder body. Lean
 * mass sets every strength reference in the system and MIGHT is the largest
 * single weight in the score, so with no height or weight entered the most
 * important attribute was computed from a number nobody supplied. Two more
 * terms -- training age and VO2max -- read zero purely because there was
 * nowhere to type them.
 *
 * So the screen leads with what is missing, and shows the derived numbers next
 * to the inputs: seeing the bench reference move when you enter your real
 * weight is the thing that makes the LBM split legible.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ProfileScreen(
    profile: Profile?,
    busy: Boolean,
    onSaveProfile: (height: Double?, sex: String?, months: Double?, vo2: Double?, goal: String?) -> Unit,
    onSaveBody: (weight: Double, bodyfat: Double?) -> Unit,
    onSaveTape: (weight: Double, neck: Double, waist: Double, hip: Double?) -> Unit,
    onSaveClaim: (pattern: String, e1rm: Double, lift: String) -> Unit,
    onToggleSkill: (skill: String, unlocked: Boolean) -> Unit,
    onClose: () -> Unit,
) {
    Scaffold(
        containerColor = Forge.Ground,
        topBar = {
            TopAppBar(
                colors = TopAppBarDefaults.topAppBarColors(
                    containerColor = Forge.Ground,
                    titleContentColor = Forge.Bone,
                    actionIconContentColor = Forge.Ash,
                ),
                title = { Text("PROFILE", style = MaterialTheme.typography.titleSmall) },
                actions = {
                    IconButton(onClick = onClose) {
                        Icon(Icons.Default.Close, contentDescription = "Close")
                    }
                },
            )
        },
    ) { padding ->
        if (profile == null) {
            Text(
                "LOADING",
                Modifier.padding(padding).padding(24.dp),
                style = MaterialTheme.typography.labelMedium,
                color = Forge.Slate,
            )
            return@Scaffold
        }

        Column(
            Modifier
                .padding(padding)
                .fillMaxSize()
                .verticalScroll(rememberScrollState())
                .padding(horizontal = 16.dp),
            verticalArrangement = Arrangement.spacedBy(16.dp),
        ) {
            MissingCard(profile)
            DerivedCard(profile)
            BodySection(profile, busy, onSaveBody, onSaveTape)
            TrainingSection(profile, onSaveProfile)
            ClaimsSection(profile, onSaveClaim)
            SkillsSection(profile, onToggleSkill)
            Spacer(Modifier.height(32.dp))
        }
    }
}

/** What is costing score right now, and only that. */
@Composable
private fun MissingCard(p: Profile) {
    if (p.missing.isEmpty()) return
    val labels = mapOf(
        "bodyweight_kg" to "Bodyweight — every strength reference is scaled from it",
        "bodyfat_pct" to "Body fat — currently a BMI estimate, so lean mass is a guess",
        "height_cm" to "Height — sets the leverage correction",
        "training_months" to "Training age — worth 25% of DISCIPLINE, reads zero without it",
        "vo2max_est" to "VO₂max — worth 30% of VIGOR, reads zero without it",
    )
    ForgePanel(accent = Forge.Blood) {
        SectionLabel("Missing inputs", accent = Forge.BloodBright)
        p.missing.forEach { key ->
            Text(
                labels[key] ?: key,
                style = MaterialTheme.typography.bodySmall,
                color = Forge.Parchment,
            )
        }
    }
}

/** The numbers the engine actually computes with. */
@Composable
private fun DerivedCard(p: Profile) {
    ForgePanel {
        SectionLabel("Derived")
        LedgerRow("lean mass", "%.1f kg".format(p.lbmKg))
        LedgerRow("FFMI", "%.1f".format(p.ffmiAdj))
        LedgerRow(
            "body fat",
            "%.1f%% (%s)".format(p.bodyfatPct, p.bfSource),
            valueColor = if (p.estimated) Forge.Ember else Forge.Parchment,
        )
        if (p.estimated) {
            Muted("Estimated from BMI. A tape or caliper measurement tightens every reference below.")
        }
        if (p.frozen) {
            Text(
                "References frozen: body fat moved more than 4 points in 30 days. Re-measure to unfreeze.",
                style = MaterialTheme.typography.bodySmall,
                color = Forge.BloodBright,
            )
        }

        SectionLabel("Loads that score 100")
        p.references.forEach { r ->
            LedgerRow(r.name, "%.1f kg".format(r.refKg))
        }
    }
}

@Composable
private fun BodySection(
    p: Profile,
    busy: Boolean,
    onSaveBody: (Double, Double?) -> Unit,
    onSaveTape: (Double, Double, Double, Double?) -> Unit,
) {
    var weight by remember(p.bodyweightKg) { mutableStateOf(fmt(p.bodyweightKg)) }
    var bodyfat by remember(p.bodyfatPct) {
        mutableStateOf(if (p.estimated) "" else fmt(p.bodyfatPct))
    }
    var tape by remember { mutableStateOf(false) }
    var neck by remember { mutableStateOf("") }
    var waist by remember { mutableStateOf("") }
    var hip by remember { mutableStateOf("") }

    Section("Body") {
        NumField("Bodyweight (kg)", weight) { weight = it }

        Row(
            horizontalArrangement = Arrangement.spacedBy(8.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            ForgeChip("Enter body fat", !tape) { tape = false }
            ForgeChip("Tape method", tape) { tape = true }
        }

        if (tape) {
            // The formula needs height, which the profile already holds, so it
            // is not asked for twice.
            Muted(
                "Measured at the narrowest point of the neck and at the navel." +
                    if (p.sex == "female") " Hip at the widest point." else "",
            )
            NumField("Neck (cm)", neck) { neck = it }
            NumField("Waist (cm)", waist) { waist = it }
            if (p.sex == "female") NumField("Hip (cm)", hip) { hip = it }
            Button(
                onClick = {
                    val w = weight.toDoubleOrNull() ?: return@Button
                    val n = neck.toDoubleOrNull() ?: return@Button
                    val wa = waist.toDoubleOrNull() ?: return@Button
                    onSaveTape(w, n, wa, hip.toDoubleOrNull())
                },
                enabled = !busy && weight.isNotBlank() && neck.isNotBlank() && waist.isNotBlank(),
                shape = RectangleShape,
                colors = forgeButtonColors(),
            ) { Text("CALCULATE AND SAVE", style = MaterialTheme.typography.labelMedium) }
        } else {
            NumField("Body fat (%) — leave blank if unknown", bodyfat) { bodyfat = it }
            Button(
                onClick = {
                    val w = weight.toDoubleOrNull() ?: return@Button
                    onSaveBody(w, bodyfat.toDoubleOrNull())
                },
                enabled = !busy && weight.isNotBlank(),
                shape = RectangleShape,
                colors = forgeButtonColors(),
            ) { Text("SAVE", style = MaterialTheme.typography.labelMedium) }
        }
    }
}

@Composable
private fun TrainingSection(
    p: Profile,
    onSave: (Double?, String?, Double?, Double?, String?) -> Unit,
) {
    var height by remember(p.heightCm) { mutableStateOf(fmt(p.heightCm)) }
    var months by remember(p.trainingMonths) { mutableStateOf(fmt(p.trainingMonths)) }
    var vo2 by remember(p.vo2maxEst) { mutableStateOf(fmt(p.vo2maxEst)) }
    var sex by remember(p.sex) { mutableStateOf(p.sex) }
    var goal by remember(p.goalProfile) { mutableStateOf(p.goalProfile) }

    Section("Training") {
        NumField("Height (cm)", height) { height = it }
        NumField("Training age (months)", months) { months = it }
        NumField("VO₂max estimate", vo2) { vo2 = it }
        Muted(
            "VIGOR needs about 34 to clear its gate — roughly a brisk four-flight " +
                "stair climb without stopping. Leave blank only if you truly do not know.",
        )

        LabelledChips("Sex", listOf("male", "female"), sex) { sex = it }
        LabelledChips("Goal", listOf("balanced", "power", "physique"), goal) { goal = it }
        Muted(
            "A goal profile shifts at most 0.06 of weight between MIGHT, FRAME and " +
                "VIGOR. The Berserk gates never move.",
        )

        Button(
            onClick = {
                onSave(
                    height.toDoubleOrNull(), sex,
                    months.toDoubleOrNull(), vo2.toDoubleOrNull(), goal,
                )
            },
            shape = RectangleShape,
            colors = forgeButtonColors(),
        ) { Text("SAVE", style = MaterialTheme.typography.labelMedium) }
    }
}

/**
 * Onboarding self-reports. These score at 0.93 confidence and carry a lifter to
 * DREADBORN and no further, which is what stops a text field from minting a
 * day-one BERSERK — so the screen says as much rather than letting someone
 * wonder why their numbers stopped counting.
 */
@Composable
private fun ClaimsSection(p: Profile, onSave: (String, Double, String) -> Unit) {
    Section("Known bests") {
        Muted(
            "Estimated one-rep max per pattern, if you already know it. Self-reported " +
                "numbers score at 93% and carry you to DREADBORN at most; the top two " +
                "ranks need the app to have seen the lift.",
        )
        p.claims.forEach { c ->
            var v by remember(c.pattern, c.e1rmKg) {
                mutableStateOf(if (c.e1rmKg > 0) fmt(c.e1rmKg) else "")
            }
            Row(
                Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.spacedBy(8.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                OutlinedTextField(
                    value = v,
                    onValueChange = { v = it },
                    label = { Text(c.name, style = MaterialTheme.typography.bodySmall) },
                    modifier = Modifier.weight(1f),
                    singleLine = true,
                    shape = RectangleShape,
                    textStyle = Ledger,
                    colors = forgeFieldColors(),
                    keyboardOptions = KeyboardOptions(
                        keyboardType = KeyboardType.Decimal,
                        imeAction = ImeAction.Next,
                    ),
                )
                TextButton(
                    onClick = { v.toDoubleOrNull()?.let { onSave(c.pattern, it, c.lift) } },
                    enabled = v.toDoubleOrNull() != null,
                    colors = ButtonDefaults.textButtonColors(contentColor = Forge.Ember),
                ) { Text("SAVE", style = MaterialTheme.typography.labelMedium) }
            }
        }
    }
}

@Composable
private fun SkillsSection(p: Profile, onToggle: (String, Boolean) -> Unit) {
    Section("Skills") {
        Muted("Binary unlocks, no load required. Twelve of them make up 20% of MASTERY.")
        p.skills.forEach { s ->
            Row(
                Modifier.fillMaxWidth(),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Checkbox(
                    checked = s.unlocked,
                    onCheckedChange = { onToggle(s.skill, it) },
                    colors = CheckboxDefaults.colors(
                        checkedColor = Forge.Blood,
                        uncheckedColor = Forge.Steel,
                        checkmarkColor = Forge.Parchment,
                    ),
                )
                Text(
                    s.skill,
                    style = MaterialTheme.typography.bodyMedium,
                    color = if (s.unlocked) Forge.Parchment else Forge.Ash,
                )
            }
        }
    }
}

// ---------- small shared pieces ----------

@Composable
private fun Section(title: String, content: @Composable () -> Unit) {
    ForgePanel {
        SectionLabel(title)
        content()
    }
}

@Composable
private fun NumField(label: String, value: String, onChange: (String) -> Unit) {
    OutlinedTextField(
        value = value,
        onValueChange = onChange,
        label = { Text(label, style = MaterialTheme.typography.bodySmall) },
        modifier = Modifier.fillMaxWidth(),
        singleLine = true,
        shape = RectangleShape,
        textStyle = Ledger,
        colors = forgeFieldColors(),
        keyboardOptions = KeyboardOptions(
            keyboardType = KeyboardType.Decimal,
            imeAction = ImeAction.Next,
        ),
    )
}

@Composable
private fun LabelledChips(
    label: String,
    options: List<String>,
    selected: String,
    onSelect: (String) -> Unit,
) {
    Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
        Text(label.uppercase(), style = MaterialTheme.typography.labelMedium, color = Forge.Ash)
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            options.forEach { o ->
                ForgeChip(o, selected == o) { onSelect(o) }
            }
        }
    }
}

/** Blank rather than "0" for an unset number, so the field reads as empty. */
private fun fmt(v: Double): String = when {
    v <= 0 -> ""
    v == v.toLong().toDouble() -> v.toLong().toString()
    else -> "%.1f".format(v)
}

// ---------- Forge control styling ----------

@Composable
private fun forgeFieldColors() = OutlinedTextFieldDefaults.colors(
    focusedBorderColor = Forge.Ember,
    unfocusedBorderColor = Forge.Hairline,
    focusedContainerColor = Forge.PanelHi,
    unfocusedContainerColor = Forge.PanelHi,
    cursorColor = Forge.Ember,
    focusedTextColor = Forge.Parchment,
    unfocusedTextColor = Forge.Parchment,
    focusedLabelColor = Forge.Ember,
    unfocusedLabelColor = Forge.Slate,
)

@Composable
private fun forgeButtonColors() = ButtonDefaults.buttonColors(
    containerColor = Forge.Blood,
    contentColor = Forge.Parchment,
    disabledContainerColor = Forge.PanelHi,
    disabledContentColor = Forge.Slate,
)

/** Square chip, hairlined when idle and blood-filled when chosen. */
@Composable
private fun ForgeChip(label: String, selected: Boolean, onClick: () -> Unit) {
    FilterChip(
        selected = selected,
        onClick = onClick,
        label = { Text(label, style = MaterialTheme.typography.bodySmall) },
        shape = RectangleShape,
        border = BorderStroke(1.dp, if (selected) Forge.Ember else Forge.Hairline),
        colors = FilterChipDefaults.filterChipColors(
            containerColor = Forge.Panel,
            labelColor = Forge.Ash,
            selectedContainerColor = Forge.Blood,
            selectedLabelColor = Forge.Parchment,
        ),
    )
}
