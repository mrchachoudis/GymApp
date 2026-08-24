// Root build file. Versions are declared here so the app module stays readable.
//
// If Android Studio offers to upgrade any of these on first open, let it: the
// versions below were chosen to be current and consistent with each other, not
// to be pinned forever.
plugins {
    id("com.android.application") version "8.7.2" apply false
    id("org.jetbrains.kotlin.android") version "2.0.21" apply false
    id("org.jetbrains.kotlin.plugin.compose") version "2.0.21" apply false
    id("org.jetbrains.kotlin.plugin.serialization") version "2.0.21" apply false
    id("com.google.gms.google-services") version "4.4.2" apply false
}
