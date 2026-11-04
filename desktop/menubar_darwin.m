//go:build darwin && desktop

// Значок kb в меню-баре и глобальная ⌥Space — только Cocoa и Carbon, без
// сторонних библиотек: в Wails v2 этого нет (решение владельца 03.11.2026).
#import <Cocoa/Cocoa.h>
#import <Carbon/Carbon.h>
#import <objc/runtime.h>

extern void kbToggle(int frontmost);
extern void kbQuit(void);
extern void kbHotkeyFailed(int status);

// kbFrontmost — окно поиска видно и приложение активно: тогда ⌥Space прячет.
// Смотрится именно окно Wails: панель «О программе» и выбор папок окном поиска
// не считаются (ревью #380).
static BOOL kbFrontmost(void) {
    if (![NSApp isActive] || [NSApp isHidden]) return NO;
    Class main = NSClassFromString(@"WailsWindow");
    for (NSWindow *w in [NSApp windows]) {
        if ([w isKindOfClass:main] && [w isVisible]) return YES;
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

// kbReopen — щелчок по значку в Dock: окно спрятано, значит открыть поиск.
static BOOL kbReopen(id self, SEL cmd, NSApplication *app, BOOL visible) {
    if (!visible) kbToggle(0);
    return YES;
}

void kbInstallMenuBar(void) {
    dispatch_async(dispatch_get_main_queue(), ^{
        // kb в Dock и ⌘Tab (решение владельца 05.11.2026): Wails ставит Regular
        // сам. Щелчок по значку в Dock, когда окно спрятано красной кнопкой,
        // открывает поиск: своего обработчика reopen у Wails v2 нет, поэтому
        // метод добавляется делегату Wails. Если он появится у Wails, наш не
        // встанет (class_addMethod не заменяет), и останется его поведение.
        class_addMethod([[NSApp delegate] class], @selector(applicationShouldHandleReopen:hasVisibleWindows:),
                        (IMP)kbReopen, "B@:@B");
        tray = [KBTray new];
        item = [[NSStatusBar systemStatusBar] statusItemWithLength:NSSquareStatusItemLength];
        // Знак kb шаблоном (Resources/menubar.png, мозг с DVK одним цветом):
        // шаблон сам темнеет и светлеет вместе с меню-баром. Нет файла — лупа.
        NSImage *img = [[NSImage alloc] initWithContentsOfFile:[[NSBundle mainBundle] pathForImageResource:@"menubar"]];
        if (img) img.size = NSMakeSize(18, 18);
        else img = [NSImage imageWithSystemSymbolName:@"magnifyingglass" accessibilityDescription:@"kb"];
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
