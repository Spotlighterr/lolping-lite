//go:build darwin

#import <Cocoa/Cocoa.h>
#import <CoreGraphics/CoreGraphics.h>
#import <CoreText/CoreText.h>
#import <AVFoundation/AVFoundation.h>
#import <ApplicationServices/ApplicationServices.h>
#include "darwin_cocoa.h"
#include <stdlib.h>
#include <string.h>

static NSStatusItem *gStatusItem = nil;
static NSMenuItem *gEnabledItem = nil;
static NSMutableDictionary<NSNumber *, NSWindow *> *gOverlays = nil;
static int gNextOverlayId = 1;
static CFMachPortRef gEventTap = NULL;
static NSTimer *gFrameTimer = nil;
static NSMutableArray<AVAudioPlayer *> *gAudioPlayers = nil;
static BOOL gEnabled = YES;
static BOOL gDragging = NO;

static CGFloat primaryScreenHeight(void) {
    NSArray<NSScreen *> *screens = [NSScreen screens];
    if (screens.count > 0) {
        return screens[0].frame.size.height;
    }
    return 1080.0;
}

@interface LolPingMenuTarget : NSObject
@end

@implementation LolPingMenuTarget
- (void)onToggle:(id)sender {
    goOnTrayToggle();
}
- (void)onTest:(id)sender {
    goOnTrayTest();
}
- (void)onQuit:(id)sender {
    goOnTrayQuit();
}
- (void)onFrame:(NSTimer *)timer {
    goOnFrameTick();
}
@end

static LolPingMenuTarget *gMenuTarget = nil;

static CGEventRef eventTapCallback(CGEventTapProxy proxy, CGEventType type, CGEventRef event, void *refcon) {
    if (type == kCGEventTapDisabledByTimeout || type == kCGEventTapDisabledByUserInput) {
        if (gEventTap) {
            CGEventTapEnable(gEventTap, true);
        }
        return event;
    }
    if (!gEnabled) {
        return event;
    }
    CGPoint pt = CGEventGetLocation(event);
    int x = (int)pt.x;
    int y = (int)pt.y;

    if (type == kCGEventLeftMouseDown) {
        if (!gDragging) {
            CGEventFlags flags = CGEventGetFlags(event);
            BOOL optDown = (flags & kCGEventFlagMaskAlternate) != 0;
            BOOL ctrlDown = (flags & kCGEventFlagMaskControl) != 0;
            BOOL gDown = CGEventSourceKeyState(kCGEventSourceStateHIDSystemState, (CGKeyCode)5); // 'G'
            if (optDown || gDown || ctrlDown) {
                gDragging = YES;
                int emoteMode = (ctrlDown && !optDown && !gDown) ? 1 : 0;
                dispatch_async(dispatch_get_main_queue(), ^{
                    goOnMouseDown(x, y, emoteMode);
                });
                return NULL;
            }
        }
    } else if (type == kCGEventLeftMouseDragged || type == kCGEventMouseMoved) {
        if (gDragging) {
            dispatch_async(dispatch_get_main_queue(), ^{
                goOnMouseMove(x, y);
            });
        }
    } else if (type == kCGEventLeftMouseUp) {
        if (gDragging) {
            gDragging = NO;
            dispatch_async(dispatch_get_main_queue(), ^{
                goOnMouseUp(x, y);
            });
            return NULL;
        }
    } else if (type == kCGEventRightMouseDown) {
        if (gDragging) {
            gDragging = NO;
            dispatch_async(dispatch_get_main_queue(), ^{
                goOnRightCancel();
            });
            return NULL;
        }
    }
    return event;
}

void mac_init_app(const void *iconPng, int iconLen) {
    @autoreleasepool {
        [NSApplication sharedApplication];
        [NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];

        gOverlays = [[NSMutableDictionary alloc] init];
        gAudioPlayers = [[NSMutableArray alloc] init];
        gMenuTarget = [[LolPingMenuTarget alloc] init];

        // Prompt for Accessibility permission if not granted yet (required for global CGEventTap).
        NSDictionary *opts = @{(__bridge id)kAXTrustedCheckOptionPrompt: @YES};
        AXIsProcessTrustedWithOptions((__bridge CFDictionaryRef)opts);

        // Setup Menu Bar Status Item
        gStatusItem = [[NSStatusBar systemStatusBar] statusItemWithLength:NSSquareStatusItemLength];
        if (iconPng && iconLen > 0) {
            NSData *data = [NSData dataWithBytes:iconPng length:(NSUInteger)iconLen];
            NSImage *img = [[NSImage alloc] initWithData:data];
            if (img) {
                [img setSize:NSMakeSize(18, 18)];
                gStatusItem.button.image = img;
            }
        } else {
            gStatusItem.button.title = @"P";
        }
        gStatusItem.button.toolTip = @"lolPing Lite (Option/G+Click: Ping | Ctrl+Click: Emote)";

        NSMenu *menu = [[NSMenu alloc] init];
        gEnabledItem = [[NSMenuItem alloc] initWithTitle:@"Enabled (Option/G+Click: Ping | Ctrl+Click: Emote)"
                                                  action:@selector(onToggle:)
                                           keyEquivalent:@""];
        gEnabledItem.target = gMenuTarget;
        gEnabledItem.state = NSControlStateValueOn;
        [menu addItem:gEnabledItem];

        NSMenuItem *testItem = [[NSMenuItem alloc] initWithTitle:@"Test Ping Now"
                                                          action:@selector(onTest:)
                                                   keyEquivalent:@""];
        testItem.target = gMenuTarget;
        [menu addItem:testItem];

        [menu addItem:[NSMenuItem separatorItem]];

        NSMenuItem *quitItem = [[NSMenuItem alloc] initWithTitle:@"Quit lolPing Lite"
                                                          action:@selector(onQuit:)
                                                   keyEquivalent:@"q"];
        quitItem.target = gMenuTarget;
        [menu addItem:quitItem];
        gStatusItem.menu = menu;

        // Global mouse event tap
        CGEventMask mask = CGEventMaskBit(kCGEventLeftMouseDown) |
                           CGEventMaskBit(kCGEventLeftMouseDragged) |
                           CGEventMaskBit(kCGEventLeftMouseUp) |
                           CGEventMaskBit(kCGEventRightMouseDown);
        gEventTap = CGEventTapCreate(kCGSessionEventTap,
                                     kCGHeadInsertEventTap,
                                     kCGEventTapOptionDefault,
                                     mask,
                                     eventTapCallback,
                                     NULL);
        if (gEventTap) {
            CFRunLoopSourceRef src = CFMachPortCreateRunLoopSource(kCFAllocatorDefault, gEventTap, 0);
            CFRunLoopAddSource(CFRunLoopGetCurrent(), src, kCFRunLoopCommonModes);
            CGEventTapEnable(gEventTap, true);
            CFRelease(src);
        }
    }
}

void mac_run_loop(void) {
    @autoreleasepool {
        [NSApp run];
    }
}

void mac_quit_app(void) {
    @autoreleasepool {
        if (gFrameTimer) {
            [gFrameTimer invalidate];
            gFrameTimer = nil;
        }
        if (gEventTap) {
            CGEventTapEnable(gEventTap, false);
            CFRelease(gEventTap);
            gEventTap = NULL;
        }
        [NSApp terminate:nil];
    }
}

int mac_create_overlay(int w, int h) {
    @autoreleasepool {
        NSRect rect = NSMakeRect(0, 0, w, h);
        NSWindow *win = [[NSWindow alloc] initWithContentRect:rect
                                                    styleMask:NSWindowStyleMaskBorderless
                                                      backing:NSBackingStoreBuffered
                                                        defer:NO];
        [win setOpaque:NO];
        [win setBackgroundColor:[NSColor clearColor]];
        [win setHasShadow:NO];
        [win setIgnoresMouseEvents:YES];
        [win setLevel:NSStatusWindowLevel + 1];
        [win setCollectionBehavior:NSWindowCollectionBehaviorCanJoinAllSpaces |
                                   NSWindowCollectionBehaviorFullScreenAuxiliary |
                                   NSWindowCollectionBehaviorStationary];
        NSView *view = [[NSView alloc] initWithFrame:rect];
        [view setWantsLayer:YES];
        view.layer.backgroundColor = [[NSColor clearColor] CGColor];
        [win setContentView:view];

        int id = gNextOverlayId++;
        gOverlays[@(id)] = win;
        return id;
    }
}

void mac_update_overlay(int id, const void *bgraPixels, int w, int h, int screenX, int screenY, float opacity) {
    @autoreleasepool {
        NSWindow *win = gOverlays[@(id)];
        if (!win || !bgraPixels) return;

        CGColorSpaceRef cs = CGColorSpaceCreateDeviceRGB();
        CGDataProviderRef provider = CGDataProviderCreateWithData(NULL, bgraPixels, (size_t)w * (size_t)h * 4, NULL);
        CGImageRef cgImg = CGImageCreate((size_t)w, (size_t)h, 8, 32, (size_t)w * 4, cs,
                                         kCGBitmapByteOrder32Little | kCGImageAlphaPremultipliedFirst,
                                         provider, NULL, false, kCGRenderingIntentDefault);
        CGDataProviderRelease(provider);
        CGColorSpaceRelease(cs);

        if (cgImg) {
            CGFloat nsY = primaryScreenHeight() - (CGFloat)(screenY + h);
            [win setFrame:NSMakeRect((CGFloat)screenX, nsY, (CGFloat)w, (CGFloat)h) display:NO];
            [win setAlphaValue:(CGFloat)opacity];
            win.contentView.layer.contents = (__bridge id)cgImg;
            CGImageRelease(cgImg);
            [win orderFrontRegardless];
        }
    }
}

void mac_hide_overlay(int id) {
    @autoreleasepool {
        NSWindow *win = gOverlays[@(id)];
        if (win) {
            [win orderOut:nil];
        }
    }
}

void mac_destroy_overlay(int id) {
    @autoreleasepool {
        NSWindow *win = gOverlays[@(id)];
        if (win) {
            [win orderOut:nil];
            [win close];
            [gOverlays removeObjectForKey:@(id)];
        }
    }
}

unsigned char *mac_rasterize_text(const char *utf8, float fontSize, int *outW, int *outH) {
    @autoreleasepool {
        NSString *str = [NSString stringWithUTF8String:utf8];
        if (!str) str = @"";
        NSFont *font = [NSFont systemFontOfSize:(CGFloat)fontSize weight:NSFontWeightSemibold];
        NSDictionary *attrs = @{
            NSFontAttributeName: font,
            NSForegroundColorAttributeName: [NSColor whiteColor]
        };
        NSSize sz = [str sizeWithAttributes:attrs];
        int w = (int)ceil(sz.width) + 8;
        int h = (int)ceil(sz.height) + 6;
        if (w < 8) w = 8;
        if (h < 8) h = 8;

        unsigned char *buf = (unsigned char *)calloc((size_t)w * (size_t)h, 1);
        CGColorSpaceRef gray = CGColorSpaceCreateDeviceGray();
        CGContextRef ctx = CGBitmapContextCreate(buf, (size_t)w, (size_t)h, 8, (size_t)w, gray, kCGImageAlphaNone);
        CGColorSpaceRelease(gray);
        if (ctx) {
            NSGraphicsContext *nsCtx = [NSGraphicsContext graphicsContextWithCGContext:ctx flipped:NO];
            [NSGraphicsContext saveGraphicsState];
            [NSGraphicsContext setCurrentContext:nsCtx];
            [str drawAtPoint:NSMakePoint(4, 3) withAttributes:attrs];
            [NSGraphicsContext restoreGraphicsState];
            CGContextRelease(ctx);
        }
        *outW = w;
        *outH = h;
        return buf;
    }
}

void mac_free(void *ptr) {
    if (ptr) free(ptr);
}

void mac_play_wav(const void *wavData, int wavLen, float volume) {
    if (!wavData || wavLen <= 0 || volume <= 0.0f) return;
    @autoreleasepool {
        // Reap finished players
        NSMutableArray *stillPlaying = [[NSMutableArray alloc] init];
        for (AVAudioPlayer *p in gAudioPlayers) {
            if (p.isPlaying) [stillPlaying addObject:p];
        }
        gAudioPlayers = stillPlaying;
        if (gAudioPlayers.count >= 8) return;

        NSData *data = [NSData dataWithBytes:wavData length:(NSUInteger)wavLen];
        NSError *err = nil;
        AVAudioPlayer *player = [[AVAudioPlayer alloc] initWithData:data error:&err];
        if (player && !err) {
            player.volume = volume;
            [player prepareToPlay];
            [player play];
            [gAudioPlayers addObject:player];
        }
    }
}

void mac_set_animating(int active) {
    @autoreleasepool {
        if (active) {
            if (!gFrameTimer) {
                gFrameTimer = [NSTimer scheduledTimerWithTimeInterval:(1.0 / 120.0)
                                                               target:gMenuTarget
                                                             selector:@selector(onFrame:)
                                                             userInfo:nil
                                                              repeats:YES];
                [[NSRunLoop mainRunLoop] addTimer:gFrameTimer forMode:NSRunLoopCommonModes];
            }
        } else {
            if (gFrameTimer) {
                [gFrameTimer invalidate];
                gFrameTimer = nil;
            }
        }
    }
}

void mac_set_enabled_check(int enabled) {
    gEnabled = enabled ? YES : NO;
    if (gEnabledItem) {
        gEnabledItem.state = gEnabled ? NSControlStateValueOn : NSControlStateValueOff;
    }
}
