#ifndef DARWIN_COCOA_H
#define DARWIN_COCOA_H

#include <stdint.h>

// Lifecycle & event loop
void mac_init_app(const void *iconPng, int iconLen);
void mac_run_loop(void);
void mac_quit_app(void);

// Overlay windows (returns opaque handle id >= 1)
int mac_create_overlay(int w, int h);
void mac_update_overlay(int id, const void *bgraPixels, int w, int h, int screenX, int screenY, float opacity);
void mac_hide_overlay(int id);
void mac_destroy_overlay(int id);

// Text rasterization (returns alpha mask w*h bytes allocated with malloc; caller frees via mac_free)
unsigned char *mac_rasterize_text(const char *utf8, float fontSize, int *outW, int *outH);
void mac_free(void *ptr);

// Audio playback (async from memory WAV bytes)
void mac_play_wav(const void *wavData, int wavLen, float volume);

// High-refresh frame pacing (120Hz / DisplayLink or 8ms timer)
void mac_set_animating(int active);

// Tray menu state
void mac_set_enabled_check(int enabled);

// Go callbacks invoked on main thread
extern void goOnMouseDown(int x, int y, int emoteMode);
extern void goOnMouseMove(int x, int y);
extern void goOnMouseUp(int x, int y);
extern void goOnRightCancel(void);
extern void goOnFrameTick(void);
extern void goOnTrayToggle(void);
extern void goOnTrayTest(void);
extern void goOnTrayQuit(void);

#endif
