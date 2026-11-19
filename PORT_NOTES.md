Native upstream visual port
==========================

`lolPingLite.exe` uses Go, Win32 layered DIBs, GDI text, and WinMM PCM
playback. There is no CGO, Chromium, WebView, or runtime FFmpeg dependency.

Keep `assets/emotes/*.frames` beside the executable in that directory layout.
Bundled animations stream one independently compressed frame at a time; sounds
and still icons are embedded. Copying the EXE alone loses the bundled animations.

Implemented from the supplied upstream sources:

- 600px wheel: radii 86/188/288, icon radius 137, 48px icons; proportional drawing.
- Metallic and silver radial gradients, cardinal flares, gold dividers, teal
  core, hub, mouse/BACK mark, and Segoe UI center labels (14-character limit).
- Ping icon filter chain, 1.18 hover scale, 80/100ms hover transitions, and
  120ms ease-out opening/closing. Center labels identify the hovered slice as
  explicitly requested; the upstream default ping items do not supply labels.
- Default ping and emote slice orders, including the Facepalm emote center.
- Three 650ms ping rings at 0/220/1400ms, ground ellipse, 3.2s life, pop/bob,
  and All In's opposing sword layers, shake, and flash.
- The nine selected bundled emotes' original 3s WebP frame timelines and OGG
  audio, converted at build time; 60-ping/8-audio-voice ceilings.
- Right-button cancellation consumes the pending left release. Dragging back
  into the center cancels; a stationary click still places the center action.
- `glowFx.ts` is a teammate field outline, not a ping beam. Its 2px/4px/900ms
  drawing primitive is ported; this application has no teammate/room event source.

Fidelity limits
---------------

The source geometry, colors, stops, keyframe times, and cubic-bezier functions
are ported. This is not a claim of pixel-identical browser output: GDI font
antialiasing, procedural edge coverage, and software shadow blur differ from
Chromium/Skia. Polar coordinates are cached at 1/64px precision. Bundled emote
frames are resampled to 188px for their 187.5px display box.

The static Danger-hover wheel was compared against the real upstream Wheel
class/CSS rendered in Chrome at 600px. Mean absolute channel errors were
0.509/0.411/0.365 on the 0-255 RGB scale. Captures and their difference image
are in `_archive/upstream-port-verification/`.

Build and verification
----------------------

```powershell
$env:CGO_ENABLED = '0'
go test ./...
go vet ./...
go build -ldflags="-H windowsgui -s -w" -o lolPingLite.exe .
.\lolPingLite.exe --smoke-test
```

The 30-second smoke run uses a separate mutex and exercises the actual native
message loop, window presentation, audio, cancellation callback, All In, and
eight emote animations. It writes `.tmp/smoke.json` and exits. It does not inject
physical mouse input into the user's desktop. Tests also verify premultiplied
alpha, native UpdateLayeredWindow, sound order, dead-zone boundaries, the third
ring, animation frame changes, and that removing an effect does not quit the app.

Measured on this machine: initial idle 15.8 MiB working set / 19.3 MiB private;
wheel 27.5 / 24.7; brief overlapping ping/emote peak 30.1 / 25.7; settled after
cycling all emotes 25.7 / 20.2. GDI objects returned from 13 to 11. These are
sampled values, not a guaranteed maximum; a 60-ping flood uses more memory.

The Explorer tray-registration check is skipped in this sandbox because even a
stock Windows icon cannot register with the user's Explorer session. Native
layered presentation passed. Existing user-owned PID 14364 could not be stopped
(access denied); quit the old copy from its tray and reopen the rebuilt EXE.

All 684 files in `.upstream` were checked against their pre-port SHA-256 hashes:
zero changes. The original sources and executable are preserved under
`_archive/before-upstream-port/`.

Asset regeneration is optional: `tools/prepare_emotes.py` needs Pillow;
`tools/prepare_sounds.py [path-to-ffmpeg-8.dll]` uses an installed shared FFmpeg
library through Python ctypes at build time only. Generated assets already ship.
