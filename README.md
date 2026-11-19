# lolPing Lite

A lightweight Windows-only rewrite of **lolPing** — drop *League of Legends* pings and emotes anywhere on your desktop while using only **~40–50 MB of RAM** instead of ~600 MB from Electron.

I built this in pure Go (Win32) because my 16 GB laptop is already full of Chrome tabs and VS Code windows, and I still wanted to spam pings on my desktop without eating RAM or dropping frames.

## How to use

Run `lolPingLite.exe` and it will sit quietly in your system tray:

- **Hold `Alt` (or `G`) + Left Click & drag:** Open the 8-direction Ping Wheel, release to ping with sound.
- **Hold `Ctrl` + Left Click & drag:** Open the Emote Wheel.
- **Right Click while dragging:** Cancel (`BACK`).
- **Right Click the tray icon:** Pause/resume, test a ping, or quit.

*(Local desktop only — no multiplayer rooms, just the simple ping & emote fun.)*

## Build it yourself

You just need **Go 1.24+** on Windows:

```powershell
go build -ldflags="-H windowsgui -s -w" -o lolPingLite.exe .
```

> Keep the `assets/emotes` folder next to `lolPingLite.exe` so animated emotes can play.

## Credits

- The original idea, wheel design, animations, and assets come from **[darrenprx/lolPing](https://github.com/darrenprx/lolPing)** by **Danrui Meng (`darrenprx`)** — huge thanks for making such a fun desktop toy!
- All *League of Legends* icons, emotes, and sound effects belong to **Riot Games, Inc.** This is an unofficial, non-commercial fan project.
