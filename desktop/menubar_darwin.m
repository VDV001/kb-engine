//go:build darwin && desktop

// Значок kb в меню-баре и глобальная ⌥Space — только Cocoa и Carbon, без
// сторонних библиотек: в Wails v2 этого нет (решение владельца 03.11.2026).
#import <Cocoa/Cocoa.h>
#import <Carbon/Carbon.h>

extern void kbToggle(int frontmost);
extern void kbQuit(void);
extern void kbHotkeyFailed(int status);

// kbFrontmost — окно kb видно и приложение активно: тогда ⌥Space прячет.
static BOOL kbFrontmost(void) {
    if (![NSApp isActive]) return NO;
    for (NSWindow *w in [NSApp windows]) {
        if ([w isVisible] && [w canBecomeKeyWindow]) return YES;
    }
    return NO;
}

@interface KBTray : NSObject
@end

@implementation KBTray
- (void)open:(id)sender { kbToggle(0); }
- (void)quit:(id)sender { kbQuit(); }
@end

static KBTray *tray;
static NSStatusItem *item;

static OSStatus kbOnHotKey(EventHandlerCallRef next, EventRef event, void *data) {
    kbToggle(kbFrontmost());
    return noErr;
}

void kbInstallMenuBar(void) {
    dispatch_async(dispatch_get_main_queue(), ^{
        tray = [KBTray new];
        item = [[NSStatusBar systemStatusBar] statusItemWithLength:NSSquareStatusItemLength];
        // Системный символ, а не своя картинка: шаблон сам темнеет и светлеет
        // вместе с меню-баром, и ассетов в пакете не прибавляется.
        NSImage *img = [NSImage imageWithSystemSymbolName:@"magnifyingglass" accessibilityDescription:@"kb"];
        [img setTemplate:YES];
        item.button.image = img;
        item.button.toolTip = @"kb — поиск по базе и файлам (⌥Space)";

        NSMenu *menu = [NSMenu new];
        NSMenuItem *open = [[NSMenuItem alloc] initWithTitle:@"Открыть поиск" action:@selector(open:) keyEquivalent:@" "];
        open.keyEquivalentModifierMask = NSEventModifierFlagOption;
        open.target = tray;
        [menu addItem:open];
        [menu addItem:[NSMenuItem separatorItem]];
        NSMenuItem *quit = [[NSMenuItem alloc] initWithTitle:@"Выйти из kb" action:@selector(quit:) keyEquivalent:@"q"];
        quit.target = tray;
        [menu addItem:quit];
        item.menu = menu;

        EventTypeSpec spec = { kEventClassKeyboard, kEventHotKeyPressed };
        InstallApplicationEventHandler(&kbOnHotKey, 1, &spec, NULL, NULL);
        EventHotKeyID hid = { 'kbsr', 1 };
        EventHotKeyRef ref = NULL;
        OSStatus st = RegisterEventHotKey(kVK_Space, optionKey, hid, GetApplicationEventTarget(), 0, &ref);
        if (st != noErr) kbHotkeyFailed((int)st);
    });
}

void kbActivate(void) {
    dispatch_async(dispatch_get_main_queue(), ^{ [NSApp activateIgnoringOtherApps:YES]; });
}
