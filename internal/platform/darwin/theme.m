// The Objective-C side of P3.4: read the effective appearance, and watch it.
//
// Compiled WITHOUT ARC, like every other .m in this package: retain/release is manual and paired.
//
// Why KVO on NSApp.effectiveAppearance and not the AppleInterfaceThemeChangedNotification distributed
// notification: the distributed notification is the NSUserDefaults mechanism, and it fires only for
// the *system* Light/Dark setting. It misses a per-app appearance override (NSApp.appearance set by
// us or by a parent) and it hands back nothing but "something changed" anyway. effectiveAppearance is
// the resolved value the views actually draw in, it is documented KVO-compliant on NSApplication, and
// its change is delivered on the main thread -- which is where the contract says onChange must run.
#import <Cocoa/Cocoa.h>
#include "theme.h"
#include "panel.h" // gt_panel_effect_view

// Defined in Go (theme.go). Called only from the KVO callback, on the main thread, and it must return
// immediately -- see the callback body.
extern void goThemeChanged(void);

// ---------------------------------------------------------------------------
// Reading the effective appearance
// ---------------------------------------------------------------------------

static BOOL appearance_is_dark(NSAppearance *ap) {
    if (!ap) return NO;
    // bestMatch, not [ap.name isEqualToString:NSAppearanceNameDarkAqua]: under an accessibility
    // high-contrast appearance the raw name is NSAppearanceNameAccessibilityHighContrastDarkAqua and a
    // string compare would call it Light. bestMatch folds the variant onto its base.
    NSAppearanceName match = [ap bestMatchFromAppearancesWithNames:@[
        NSAppearanceNameAqua, NSAppearanceNameDarkAqua
    ]];
    return [match isEqualToString:NSAppearanceNameDarkAqua];
}

int32_t gt_theme_is_dark(void) {
    NSAppearance *ap = nil;

    // The panel's own effect view first: once the panel exists this is the appearance its tiles draw
    // in, and it reflects a per-window override that NSApp would not show.
    NSView *effect = (NSView *)gt_panel_effect_view();
    if (effect) ap = effect.effectiveAppearance;

    if (!ap) {
        NSApplication *app = NSApp ? NSApp : [NSApplication sharedApplication];
        ap = app.effectiveAppearance;
    }

    // No appearance to read at all (called absurdly early): match panel.h's built-in dark default
    // rather than flashing a Light palette onto a HUD.
    if (!ap) return 1;
    return appearance_is_dark(ap) ? 1 : 0;
}

int32_t gt_theme_material(void) {
    return (int32_t)NSVisualEffectMaterialHUDWindow;
}

// ---------------------------------------------------------------------------
// Watching it
// ---------------------------------------------------------------------------

@interface GTThemeObserver : NSObject
@end

@implementation GTThemeObserver
- (void)observeValueForKeyPath:(NSString *)keyPath
                      ofObject:(id)object
                        change:(NSDictionary<NSKeyValueChangeKey, id> *)change
                       context:(void *)context {
    // Nothing but the crossing. The new appearance is not read here and the panel is not restyled
    // here: that is ApplyTheme's job, on the event loop, reached through onChange
    // (docs/ARCHITECTURE.md#the-cgo-rule). This callback is on the main thread, in front of the pixels
    // the summon path is waiting for, so it must cost nothing.
    goThemeChanged();
}
@end

// Main-thread-only, so unguarded. NULL means "not watching".
static GTThemeObserver *g_theme_obs = nil;

// A private context pointer so -observeValueForKeyPath: could tell this registration from any other
// (there is only one today; this is the cost-free habit, not a live need).
static void *const kGTThemeCtx = (void *)&kGTThemeCtx;

static NSString *const kGTThemeKeyPath = @"effectiveAppearance";

gt_status gt_theme_watch_start(void) {
    if (g_theme_obs) return GT_OK; // already watching

    NSApplication *app = [NSApplication sharedApplication];
    if (!app) return GT_ERR_UNAVAILABLE;

    g_theme_obs = [[GTThemeObserver alloc] init];
    // options 0: the callback ignores the value, so there is no reason to pay for the change
    // dictionary to be populated.
    [app addObserver:g_theme_obs
          forKeyPath:kGTThemeKeyPath
             options:0
             context:kGTThemeCtx];
    return GT_OK;
}

void gt_theme_watch_stop(void) {
    if (!g_theme_obs) return;

    // removeObserver:forKeyPath:context: throws if the pair was never registered; it always was here,
    // because g_theme_obs is non-nil iff gt_theme_watch_start ran the addObserver: above.
    [[NSApplication sharedApplication] removeObserver:g_theme_obs
                                          forKeyPath:kGTThemeKeyPath
                                             context:kGTThemeCtx];
    [g_theme_obs release];
    g_theme_obs = nil;
}
