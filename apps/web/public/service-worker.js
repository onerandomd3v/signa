"use strict";

self.addEventListener("push", (event) => {
  event.waitUntil(
    self.registration.showNotification("Signa update", {
      body: "There’s an update. Open Signa to review it.",
      data: { path: "/incidents" },
    }),
  );
});

self.addEventListener("notificationclick", (event) => {
  event.notification.close();
  event.waitUntil(
    self.clients
      .matchAll({ type: "window", includeUncontrolled: true })
      .then((clients) => {
        const target = new URL("/incidents", self.location.origin).href;
        const existing = clients.find((client) => client.url === target);
        if (existing) return existing.focus();
        return self.clients.openWindow(target);
      }),
  );
});
