// The menu-bar status item. Compiled WITHOUT ARC like the rest of this package; the status item, the
// menu, the menu items and the target are retained for the process's life and released in
// gt_menubar_remove.
#import <Cocoa/Cocoa.h>
#include "menubar.h"

// The action target. NSMenuItem holds its target weakly, so this is a static that outlives the menu.
@interface GTMenubarTarget : NSObject
- (void)openSettings:(id)sender;
- (void)quit:(id)sender;
@end

@implementation GTMenubarTarget
- (void)openSettings:(id)sender {
    (void)sender;
    goMenubarSettings();
}
- (void)quit:(id)sender {
    (void)sender;
    goMenubarQuit();
}
@end

static NSStatusItem *g_status = nil;
static GTMenubarTarget *g_target = nil;

// A template image so the icon tracks the menu bar's light/dark appearance. macOS 11+ has SF Symbols;
// the deployment target is 12.0 (D17) so imageWithSystemSymbolName: is always available, but the
// specific symbol might not be on an older 12.x — fall back to a short text title so the item is
// never invisible.
static void set_button_look(NSStatusBarButton *button) {
    NSImage *img = nil;
    if (@available(macOS 11.0, *)) {
        img = [NSImage imageWithSystemSymbolName:@"macwindow.on.rectangle"
                       accessibilityDescription:@"GoTab"];
        if (!img) {
            img = [NSImage imageWithSystemSymbolName:@"rectangle.on.rectangle"
                           accessibilityDescription:@"GoTab"];
        }
    }
    if (img) {
        img.template = YES;
        button.image = img;
    } else {
        button.title = @"⌥⇥";
    }
    button.toolTip = @"GoTab";
}

gt_status gt_menubar_install(const char *title) {
    if (g_status) return GT_OK;

    g_status = [[[NSStatusBar systemStatusBar]
                 statusItemWithLength:NSVariableStatusItemLength] retain];
    if (!g_status) return GT_ERR_INTERNAL;

    set_button_look(g_status.button);

    g_target = [[GTMenubarTarget alloc] init];

    NSMenu *menu = [[NSMenu alloc] initWithTitle:@"GoTab"];
    menu.autoenablesItems = NO;

    if (title && title[0] != '\0') {
        NSMenuItem *header = [[NSMenuItem alloc]
            initWithTitle:[NSString stringWithUTF8String:title] action:NULL keyEquivalent:@""];
        header.enabled = NO;
        [menu addItem:header];
        [header release];
        [menu addItem:[NSMenuItem separatorItem]];
    }

    NSMenuItem *settings = [[NSMenuItem alloc]
        initWithTitle:@"Settings…" action:@selector(openSettings:) keyEquivalent:@","];
    settings.target = g_target;
    [menu addItem:settings];
    [settings release];

    [menu addItem:[NSMenuItem separatorItem]];

    NSMenuItem *quit = [[NSMenuItem alloc]
        initWithTitle:@"Quit GoTab" action:@selector(quit:) keyEquivalent:@"q"];
    quit.target = g_target;
    [menu addItem:quit];
    [quit release];

    g_status.menu = menu;
    [menu release];
    return GT_OK;
}

void gt_menubar_remove(void) {
    if (g_status) {
        [[NSStatusBar systemStatusBar] removeStatusItem:g_status];
        [g_status release];
        g_status = nil;
    }
    if (g_target) {
        [g_target release];
        g_target = nil;
    }
}
