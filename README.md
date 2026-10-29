# Hearth

Hearth is a household ledger for utility bills. People record a bill, split it, and track who has paid the person who covered the provider.

Pages are Go templates updated with HTMX. Money is stored in cents. Each bill keeps the split it was given, so inviting someone later does not rewrite history.

## Which program you are running

The server and the desktop app are not the same executable. You choose when you build. Double-clicking a file runs whatever that file was built as. There is no switch at launch.

| You want | Build | File | What happens when you open it |
| --- | --- | --- | --- |
| Headless server | `make server` | `bin/hearth` | A console. Open http://localhost:8080 in a browser. The database is `data/hearth.db`. |
| Desktop window | `make desktop` | `bin/hearth-desktop` | A window. Closing it quits Hearth. The database is in your user config directory. |

On Windows those files are `bin\hearth.exe` and `bin\hearth-desktop.exe`. Double-clicking `hearth.exe` opens a console, not a window. Double-click `hearth-desktop.exe` to open the app.

`go run ./cmd/hearth` and Docker are the headless server. The phone apps are separate projects under `mobile/`.

You need GNU make. On Windows that is the `make` from MinGW or Git, not `nmake`.

```bash
make server
make desktop
make test
```

The headless server can be built for another operating system. The desktop window cannot: it links the webview on the machine you are building on.

```bash
make server GOOS=linux GOARCH=amd64
make server GOOS=linux GOARCH=arm64
make server GOOS=darwin GOARCH=arm64
make server GOOS=windows GOARCH=amd64
make servers
```

`make servers` builds all six of those. A cross-compiled server is named for its target, such as `bin/hearth-linux-amd64` or `bin/hearth-windows-amd64.exe`. `make server` with no `GOOS` still writes `bin/hearth` for this machine.

## Headless server

`make server` writes `bin/hearth` for the machine you are on. Run that file, then open http://localhost:8080. Stop it by closing the console or pressing Ctrl+C.

```bash
go run ./cmd/hearth
```

## Desktop window

`make desktop` writes `bin/hearth-desktop`. On Windows, double-click `bin\hearth-desktop.exe`. A second launch exits while the first is open.

The database and the log live under the user config directory: `%AppData%\hearth` on Windows, `~/Library/Application Support/hearth` on macOS, `~/.config/hearth` on Linux. `HEARTH_DATA` still overrides the SQLite path.

This build needs CGO and a C++ compiler. The Makefile turns CGO on for this target only, and on Windows it passes `-H windowsgui` so the window has no console.

- Windows: the WebView2 runtime, already present on current Windows 10 and 11, and MinGW-w64 so that `gcc` and `g++` are on `PATH`.
- macOS: Xcode command-line tools. Build on a Mac.
- Linux: GTK 3 and WebKitGTK 4.0 (`libgtk-3-dev` and `libwebkit2gtk-4.0-dev`, or the distro equivalents).

`HEARTH_URL` empty starts Hearth inside the window at `http://127.0.0.1`. Set it to an `https://` origin and the window only opens that server. It does not open a local database.

## Phone

Each phone install is its own household. Nothing is synced to the desktop. The preference key `remote_url` is the phone's `HEARTH_URL`. Leave it empty.

Build the Go library first, from the repo root:

```bash
go install golang.org/x/mobile/cmd/gomobile@latest
gomobile init
gomobile bind -target=android -androidapi 26 -o mobile/android/app/libs/hearth.aar ./mobile
gomobile bind -target=ios -o mobile/ios/Mobile.xcframework ./mobile
```

Android, on a machine with the Android SDK:

```bash
cd mobile/android
./gradlew assembleDebug
```

In `cmd` on Windows, run `gradlew assembleDebug` from that directory instead of `./gradlew`.

The debug APK is `mobile\android\app\build\outputs\apk\debug\app-debug.apk`. Cleartext HTTP is allowed only for `127.0.0.1`.

iOS requires a Mac. Open `mobile/ios/Hearth.xcodeproj` after the xcframework exists, select your team, and run it on a simulator or a phone. A personal Apple ID is enough for a short-lived install on your own device.

## Docker

Docker builds the headless server only.

```bash
docker compose up --build
```

The SQLite database lives in the `hearth-data` volume. Copy that file to back it up.

The compose file turns secure cookies off so the app works over plain HTTP on localhost. In production, terminate TLS in front of Hearth, set `HEARTH_BASE_URL` to the public `https://` origin, and set `HEARTH_SECURE_COOKIES=true`.

## Configuration

| Variable | Used by | Default | Purpose |
| --- | --- | --- | --- |
| `HEARTH_ADDR` | Server | `:8080` | Listen address |
| `HEARTH_DATA` | Server and desktop | `data/hearth.db` on the server, config directory on the desktop | SQLite file |
| `HEARTH_ENV` | Both | `development` | `production` turns secure cookies on |
| `HEARTH_BASE_URL` | Both | request host | Public origin for invite links |
| `HEARTH_SECURE_COOKIES` | Both | on in production | `true` or `false` |
| `HEARTH_TRUST_PROXY` | Server | off | Trust `X-Forwarded-For` and `X-Forwarded-Proto` |
| `HEARTH_URL` | Desktop and phone | empty | Open this URL instead of starting a local server |

## How a bill is split

- **Equal** divides the total across the people included on that bill. Everyone starts included. Leftover cents go to those people in a stable order.
- **Weight** uses whole-number weights. A weight of 0 gives that person nothing.
- **Custom amounts** must add up to the bill exactly.

Until someone is marked as having paid the provider, the bill is a plan and nobody owes anyone. After that, each other person's share is either paid or not. Marking it paid settles that whole share. Pairwise balances on the ledger are netted across bills.

A utility can remember people who should start unchecked. Checking them on a single bill does not change that default.

Someone can be added to a household with only a name. They show up on splits and cannot sign in. An invite to the same email links that person to the new account.

Invite links are single-use, expire after 14 days, and can only be accepted by an account with the invited email. Share the link directly. There is no mailer.

## Sign-in

Hearth is its own OAuth 2.0 authorization server. The browser signs in with email and password, receives an authorization code, and exchanges it for a JWT access token and a refresh token. Both are `HttpOnly` cookies. The access token lasts 15 minutes. The refresh token lasts 14 days and rotates on use.

The signing key is `oauth-ed25519.key` and the first-party client secret is `oauth-client.txt`, both in the same directory as the SQLite file. Public keys are at `/.well-known/jwks.json`. The token endpoint is `POST /oauth/token`.
