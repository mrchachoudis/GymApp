# Firebase and the Android app

A server cannot make a phone buzz on its own. Google owns the persistent
connection to the device, so notifications go: `gymd` -> FCM -> phone. That is
what the service-account key is for.

## 1. Create the project

1. <https://console.firebase.google.com> -> Add project. Analytics is not needed.
2. Add app -> Android.
3. Package name: `com.mrcha.gymlogger`. It must match `applicationId` in
   `android/app/build.gradle.kts` exactly or the app will register a token that
   FCM refuses to deliver to.
4. Download `google-services.json` and put it at
   `android/app/google-services.json`. It is not a secret — it identifies the
   app, it does not authorise sending.

## 2. Server credentials

Project settings -> Service accounts -> Generate new private key. That JSON
**is** a secret: it authorises sending to every device on the project.

```bash
sudo cp firebase-adminsdk-xxxxx.json /etc/gymlogger/fcm.json
sudo chown gymlogger:gymlogger /etc/gymlogger/fcm.json
sudo chmod 600 /etc/gymlogger/fcm.json
sudo sed -i 's|# FCM_CREDENTIALS=.*|FCM_CREDENTIALS=/etc/gymlogger/fcm.json|' /etc/gymlogger/env
sudo systemctl restart gymd
```

The log line `fcm ready project=...` on startup means it loaded. Never put this
file in the repo or the APK.

## 3. Build the app

Open `android/` in Android Studio. It will sync Gradle and may offer to update
the plugin versions — accept.

To build before Firebase exists, comment out the `com.google.gms.google-services`
plugin line in `android/app/build.gradle.kts`. Everything except push works.

```bash
cd android
./gradlew assembleDebug
# app/build/outputs/apk/debug/app-debug.apk
```

Install over USB with `./gradlew installDebug`, or copy the APK to the phone and
allow install from unknown sources. No Play Store account needed for one phone.

For a release build you need a signing key:

```bash
keytool -genkey -v -keystore gym-release.jks -keyalg RSA \
  -keysize 2048 -validity 10000 -alias gym
```

Keep the keystore. Losing it means the next version cannot upgrade the installed
app, only replace it.

## 4. First run

1. Open the app, tap the gear.
2. Base URL: `https://gym.yourdomain.com` (no trailing slash).
3. Auth token: the value of `GYM_AUTH_TOKEN`.
4. Save. Grant the notification permission when asked — on Android 13+ push is
   silently dropped without it, and FCM still reports success, so it looks like
   the server is broken when it is not.

Verify the round trip:

```bash
curl -s -X POST https://gym.yourdomain.com/v1/test-push \
  -H "Authorization: Bearer $TOKEN"
```

`{"sent":1,"dropped":0}` and a notification on the phone means it is wired up.
`{"sent":0}` means no device registered — reopen the app while the server is
reachable, since the token is re-sent on launch whenever it has changed.

## 5. When notifications stop

In order of likelihood:

1. **Token rotated and never re-registered.** Opening the app fixes it. `sent:0`
   from `/v1/test-push` is the symptom.
2. **Battery optimisation.** Android may throttle FCM for apps it considers
   idle. Settings -> Apps -> Gym Logger -> Battery -> Unrestricted.
3. **Quiet hours doing their job.** The scheduler will not notify during a night
   shift, during the post-shift sleep window, or between `quiet_start_hour` and
   `quiet_end_hour`. Check with `gymd -next` on the box.
4. **Already sent today.** Reminders are deduplicated per day, so a restart does
   not re-nag. `SELECT * FROM notifications_sent ORDER BY sent_at DESC LIMIT 10;`
