# lolPing Lite (Windows Native / Go)

A lightweight, native Windows rewrite of the desktop *League of Legends* Ping & Emote Wheel overlay written in pure Go (Win32 API, zero CGO, zero Chromium/Electron).

## Highlights

- **Ultra-low memory footprint:** ~40–50 MB RAM in a single native process (down from ~626 MB across 6 Electron/Chromium processes).
- **High-refresh-rate native VSync:** Driven by `dwmapi.dll` (`DwmFlush`) to match 120Hz / 144Hz / 165Hz / 240Hz monitors with 0% CPU usage when idle.
- **Fast software renderer:** Cached wheel base & per-sector wedge tables, fixed-point 16.16 bilinear scaling, pre-composited drop shadows, 1D radial LUT shockwaves, pooled overlay windows, and multi-core parallel effect rendering (~0.58 ms/frame wheel, ~0.80 ms/frame for 5 simultaneous pings).
- **Async WAV audio:** Pre-scaled PCM cache played asynchronously via `winmm.dll` (`waveOut`) without blocking the UI thread.
- **Local-only:** Focused purely on local desktop Ping Wheel & Emote Wheel (no multiplayer rooms).

## Controls

- **Hold `Alt` or `G` + Left Click (drag & release):** Open the 8-direction **Ping Wheel** and trigger a ping with sound & animation.
- **Hold `Ctrl` + Left Click (drag & release):** Open the **Emote Wheel**.
- **Right Click while dragging:** Cancel (`BACK`).
- **System Tray icon:** Right-click to toggle Enabled/Paused, trigger a Test Ping, or Quit.

## Build from Source (Windows)

Requires **Go 1.24+** on Windows (`amd64`).

```powershell
go test ./...
go vet ./...
go build -ldflags="-H windowsgui -s -w" -o lolPingLite.exe .
```

> Keep the `assets/emotes/*.frames` files beside `lolPingLite.exe` if you want animated emote playback.

## Credits & Attribution

- **Original Project & Design:** Ported from and inspired by **[darrenprx/lolPing](https://github.com/darrenprx/lolPing)** created by **Danrui Meng (`darrenprx`)**. Wheel geometry, visual styling, ping/emote timelines, sound mappings, and extracted desktop overlay assets originate from `darrenprx/lolPing`.
- **Game Assets:** *League of Legends* ping icons, emotes, and sound effects are the property of **Riot Games, Inc.** This is an unofficial, non-commercial fan desktop utility and is not affiliated with or endorsed by Riot Games.
