// The settings window. Compiled without ARC like the rest of this package, so every alloc/init is
// balanced: a control is retained by its superview and by g_ctrls, so after adding it we release the
// alloc reference. The three statics (g_win, g_ctl, g_ctrls) live for the process.
//
// Layout is manual top-down frames in a non-flipped content view — the window is fixed size, so there
// is nothing for a stack view or Auto Layout to earn. Every control's action funnels through emit(),
// which sends "Key=Value" to Go; Go parses it with prefs.Set and persists.
#import <Cocoa/Cocoa.h>
#include "settings.h"

extern void goSettingsAssign(const char *kv);
extern void goSettingsClosed(void);

static const CGFloat WIN_W = 460.0;
static const CGFloat PAD = 20.0;
static const CGFloat ROW = 30.0;

static NSWindow *g_win = nil;
static NSMutableDictionary *g_ctrls = nil; // pref key (or "__hotkey") -> NSControl
static id g_ctl = nil;

static void emit(NSString *key, NSString *val) {
    goSettingsAssign([[NSString stringWithFormat:@"%@=%@", key, val] UTF8String]);
}

// ---------------------------------------------------------------------------
// The controller: one target for every action, plus the window delegate.
// ---------------------------------------------------------------------------

@interface GTSettingsCtl : NSObject <NSWindowDelegate>
@end

@implementation GTSettingsCtl
- (void)check:(NSButton *)b {
    emit(b.identifier, b.state == NSControlStateValueOn ? @"true" : @"false");
}
- (void)popup:(NSPopUpButton *)p {
    emit(p.identifier, [[p titleOfSelectedItem] lowercaseString]);
}
- (void)step:(NSStepper *)s {
    NSTextField *echo = g_ctrls[[s.identifier stringByAppendingString:@"#echo"]];
    echo.integerValue = s.integerValue;
    emit(s.identifier, [@(s.integerValue) stringValue]);
}
- (void)text:(NSTextField *)t {
    emit(t.identifier, t.stringValue);
}
- (void)windowWillClose:(NSNotification *)n {
    (void)n;
    goSettingsClosed();
}
@end

// ---------------------------------------------------------------------------
// The hotkey recorder: a button that captures the next modified keystroke.
// ---------------------------------------------------------------------------

@interface GTRecorder : NSButton
@end

@implementation GTRecorder
- (BOOL)acceptsFirstResponder {
    return YES;
}
- (void)mouseDown:(NSEvent *)e {
    (void)e;
    [self.window makeFirstResponder:self];
    [self setTitle:@"press a shortcut…"];
}
- (void)keyDown:(NSEvent *)e {
    NSUInteger any = NSEventModifierFlagControl | NSEventModifierFlagOption |
                     NSEventModifierFlagShift | NSEventModifierFlagCommand;
    NSUInteger m = e.modifierFlags & any;
    if (m == 0) {
        NSBeep(); // a bare key would be swallowed for every application
        return;
    }
    // Two assignments: prefs.Set understands each. Go recomputes the label and calls
    // gt_settings_set_hotkey, so this method does not format the chord itself.
    goSettingsAssign([[NSString stringWithFormat:@"HotkeyKeyCode=%u", (unsigned)e.keyCode] UTF8String]);
    goSettingsAssign([[NSString stringWithFormat:@"HotkeyModifiers=%lu", (unsigned long)m] UTF8String]);
    [self.window makeFirstResponder:nil];
}
- (void)flagsChanged:(NSEvent *)e {
    (void)e; // swallow: recording waits for the actual key
}
@end

// ---------------------------------------------------------------------------
// Builders. Each advances *y downward by one row.
// ---------------------------------------------------------------------------

static NSTextField *make_label(NSString *s, NSRect f) {
    NSTextField *l = [NSTextField labelWithString:s]; // autoreleased
    l.frame = f;
    return l;
}

static void add_section(NSView *v, CGFloat *y, NSString *title) {
    NSTextField *l = make_label(title, NSMakeRect(PAD, *y, WIN_W - 2 * PAD, 18));
    l.font = [NSFont boldSystemFontOfSize:12];
    [v addSubview:l];
    *y -= 24;
}

static void add_check(NSView *v, CGFloat *y, NSString *key, NSString *title) {
    NSButton *b = [NSButton checkboxWithTitle:title target:g_ctl action:@selector(check:)]; // autoreleased
    b.identifier = key;
    b.frame = NSMakeRect(PAD + 8, *y, WIN_W - 2 * PAD - 8, 20);
    [v addSubview:b];
    g_ctrls[key] = b;
    *y -= ROW - 4;
}

static void add_text(NSView *v, CGFloat *y, NSString *key, NSString *axLabel) {
    NSTextField *t = [NSTextField textFieldWithString:@""]; // autoreleased
    t.identifier = key;
    t.frame = NSMakeRect(PAD + 8, *y, WIN_W - 2 * PAD - 8, 22);
    t.target = g_ctl;
    t.action = @selector(text:);
    [[t cell] setSendsActionOnEndEditing:YES]; // fire on focus-loss, not just Return
    // The field's caption is a separate, unlinked label, so VoiceOver would announce it as an
    // unlabelled text field without this (P5.2).
    [t setAccessibilityLabel:axLabel];
    [v addSubview:t];
    g_ctrls[key] = t;
    *y -= ROW;
}

static void add_popup(NSView *v, CGFloat *y, NSString *key, NSString *caption, NSArray *titles) {
    [v addSubview:make_label(caption, NSMakeRect(PAD + 8, *y + 3, 150, 18))];
    NSPopUpButton *p = [[NSPopUpButton alloc] initWithFrame:NSMakeRect(PAD + 160, *y, 150, 26)
                                                  pullsDown:NO];
    [p addItemsWithTitles:titles];
    p.identifier = key;
    p.target = g_ctl;
    p.action = @selector(popup:);
    [p setAccessibilityLabel:caption]; // the caption label is not AX-linked to the control (P5.2)
    [v addSubview:p];
    g_ctrls[key] = p;
    [p release];
    *y -= ROW;
}

static void add_stepper(NSView *v, CGFloat *y, NSString *key, NSString *caption, int lo, int hi, int step) {
    [v addSubview:make_label(caption, NSMakeRect(PAD + 8, *y + 3, 220, 18))];
    NSTextField *echo = make_label(@"0", NSMakeRect(PAD + 240, *y + 3, 44, 18));
    echo.alignment = NSTextAlignmentRight;
    [v addSubview:echo];
    NSStepper *s = [[NSStepper alloc] initWithFrame:NSMakeRect(PAD + 300, *y - 2, 20, 26)];
    s.minValue = lo;
    s.maxValue = hi;
    s.increment = step;
    s.valueWraps = NO;
    s.identifier = key;
    s.target = g_ctl;
    s.action = @selector(step:);
    // A bare NSStepper speaks only "stepper"; the caption and the value echo are separate labels
    // (P5.2). The current value still reaches VoiceOver through the stepper's own integerValue.
    [s setAccessibilityLabel:caption];
    [v addSubview:s];
    g_ctrls[key] = s;
    g_ctrls[[key stringByAppendingString:@"#echo"]] = echo;
    [s release];
    *y -= ROW;
}

static void add_recorder(NSView *v, CGFloat *y, NSString *caption) {
    [v addSubview:make_label(caption, NSMakeRect(PAD + 8, *y + 4, 150, 18))];
    GTRecorder *r = [[GTRecorder alloc] initWithFrame:NSMakeRect(PAD + 160, *y, 160, 28)];
    r.bezelStyle = NSBezelStyleRounded;
    [r setButtonType:NSButtonTypeMomentaryPushIn];
    [r setTitle:@"—"];
    // The title is the chord ("⌥⇥") or a placeholder, not a name; give VoiceOver a stable label and
    // let the title ride as the value (P5.2).
    [r setAccessibilityLabel:@"Keyboard shortcut"];
    [v addSubview:r];
    g_ctrls[@"__hotkey"] = r;
    [r release];
    *y -= ROW;
}

// ---------------------------------------------------------------------------
// Public surface
// ---------------------------------------------------------------------------

gt_status gt_settings_open(void) {
    if (g_win) {
        [NSApp activateIgnoringOtherApps:YES];
        [g_win makeKeyAndOrderFront:nil];
        return GT_OK;
    }

    @autoreleasepool {
        [NSApplication sharedApplication];
        // Regular, so the settings window behaves like an ordinary app window while it is open.
        if ([NSApp activationPolicy] != NSApplicationActivationPolicyRegular) {
            [NSApp setActivationPolicy:NSApplicationActivationPolicyRegular];
        }

        g_ctl = [[GTSettingsCtl alloc] init];
        g_ctrls = [[NSMutableDictionary alloc] init];

        CGFloat h = 470;
        g_win = [[NSWindow alloc] initWithContentRect:NSMakeRect(0, 0, WIN_W, h)
                                           styleMask:(NSWindowStyleMaskTitled | NSWindowStyleMaskClosable)
                                             backing:NSBackingStoreBuffered
                                               defer:NO];
        g_win.title = @"GoTab Settings";
        g_win.delegate = g_ctl;
        g_win.releasedWhenClosed = NO;

        NSView *c = g_win.contentView;
        CGFloat y = h - PAD - 4;

        add_section(c, &y, @"Windows to show");
        add_check(c, &y, @"ShowMinimized", @"Minimized windows");
        add_check(c, &y, @"ShowHidden", @"Windows of hidden apps");
        add_check(c, &y, @"ShowOtherSpace", @"Windows on other Spaces");
        add_check(c, &y, @"ActiveAppOnly", @"Only the frontmost app’s windows");
        y -= 6;

        add_section(c, &y, @"Never show these apps");
        [c addSubview:make_label(@"comma-separated application names", NSMakeRect(PAD + 8, y + 2, 380, 16))];
        y -= 20;
        add_text(c, &y, @"BlockedApps", @"Never show these apps, comma-separated application names");
        y -= 6;

        add_section(c, &y, @"Appearance");
        add_popup(c, &y, @"Appearance", @"Panel appearance", @[ @"System", @"Light", @"Dark" ]);
        y -= 6;

        add_section(c, &y, @"Layout");
        add_stepper(c, &y, @"MaxColumns", @"Columns before wrapping", 1, 20, 1);
        add_stepper(c, &y, @"ThumbnailCacheSize", @"Thumbnail cache (images)", 8, 512, 8);
        y -= 6;

        add_section(c, &y, @"Shortcut");
        add_recorder(c, &y, @"Hold, then Tab to cycle");

        [g_win center];
        [NSApp activateIgnoringOtherApps:YES];
        [g_win makeKeyAndOrderFront:nil];
    }
    return GT_OK;
}

void gt_settings_close(void) {
    [g_win close];
}

static id ctrl(const char *key) {
    return g_ctrls ? g_ctrls[[NSString stringWithUTF8String:key]] : nil;
}

void gt_settings_set_bool(const char *key, int32_t on) {
    id b = ctrl(key);
    if ([b isKindOfClass:[NSButton class]]) {
        [(NSButton *)b setState:on ? NSControlStateValueOn : NSControlStateValueOff];
    }
}

void gt_settings_set_int(const char *key, int32_t v) {
    id s = ctrl(key);
    if ([s isKindOfClass:[NSStepper class]]) {
        [(NSStepper *)s setIntegerValue:v];
        NSTextField *echo = g_ctrls[[[NSString stringWithUTF8String:key] stringByAppendingString:@"#echo"]];
        echo.integerValue = v;
    }
}

void gt_settings_set_choice(const char *key, const char *value) {
    id p = ctrl(key);
    if ([p isKindOfClass:[NSPopUpButton class]]) {
        NSString *want = [[NSString stringWithUTF8String:value] capitalizedString];
        [(NSPopUpButton *)p selectItemWithTitle:want];
    }
}

void gt_settings_set_text(const char *key, const char *value) {
    id t = ctrl(key);
    if ([t isKindOfClass:[NSTextField class]]) {
        [(NSTextField *)t setStringValue:[NSString stringWithUTF8String:value]];
    }
}

void gt_settings_set_hotkey(const char *display) {
    id r = g_ctrls ? g_ctrls[@"__hotkey"] : nil;
    if (r) [(NSButton *)r setTitle:[NSString stringWithUTF8String:display]];
}
