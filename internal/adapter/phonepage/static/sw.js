// Service worker страницы телефона DVK (#622): только push. Кэша и работы без
// сети нет — страница всегда свежая с сервера (Cache-Control: no-cache).
//
// Сервер шлет только вид события ({"kind":"answer"}), подпись рисует телефон:
// переписку сервер не видит. Уведомление показывается на каждый push — Safari
// отзывает подписку у страницы, которая получает push молча.
self.addEventListener("install", () => self.skipWaiting());
self.addEventListener("activate", (e) => e.waitUntil(self.clients.claim()));

self.addEventListener("push", (e) => {
  e.waitUntil(self.registration.showNotification("DVK", {
    body: "Компьютер ответил",
    tag: "dvk-answer", // новое заменяет прежнее, а не копится стопкой
    renotify: true,
    icon: "icon-192.png",
  }));
});

// Нажатие открывает уже открытую страницу, а если ее нет — новую.
self.addEventListener("notificationclick", (e) => {
  e.notification.close();
  e.waitUntil((async () => {
    const scope = new URL(self.registration.scope).pathname;
    for (const c of await self.clients.matchAll({ type: "window", includeUncontrolled: true })) {
      if (new URL(c.url).pathname.startsWith(scope)) return c.focus();
    }
    return self.clients.openWindow(self.registration.scope);
  })());
});
