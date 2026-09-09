const shellCache = "mailflow-desktop-shell-v1";

self.addEventListener("install", (event) => {
  event.waitUntil(
    caches.open(shellCache).then(async (cache) => {
      const response = await fetch("/");
      if (!response.ok) throw new Error("desktop shell unavailable");
      await cache.put("/", response.clone());
      const html = await response.text();
      const assets = [...html.matchAll(/(?:src|href)="(\/assets\/[^"]+)"/g)].map(
        (match) => match[1],
      );
      await cache.addAll(assets);
      const current = new Set(["/", ...assets]);
      const previous = await cache.keys();
      await Promise.all(
        previous
          .filter((request) => !current.has(new URL(request.url).pathname))
          .map((request) => cache.delete(request)),
      );
      await self.skipWaiting();
    }),
  );
});

self.addEventListener("activate", (event) => {
  event.waitUntil(
    caches
      .keys()
      .then((keys) =>
        Promise.all(keys.filter((key) => key !== shellCache).map((key) => caches.delete(key))),
      ),
  );
  event.waitUntil(self.clients.claim());
});

self.addEventListener("fetch", (event) => {
  const request = event.request;
  const url = new URL(request.url);
  if (
    request.method !== "GET" ||
    url.origin !== self.location.origin ||
    url.pathname.startsWith("/api/")
  )
    return;

  const shellNavigation =
    url.pathname === "/" ||
    url.pathname === "/admin" ||
    url.pathname.startsWith("/admin/") ||
    url.pathname === "/settings/accounts";

  if (request.mode === "navigate" && shellNavigation) {
    event.respondWith(
      fetch(request)
        .then((response) => {
          if (response.ok)
            caches.open(shellCache).then((cache) => cache.put("/", response.clone()));
          return response;
        })
        .catch(() => caches.match("/", { ignoreVary: true })),
    );
    return;
  }

  if (!url.pathname.startsWith("/assets/")) return;

  event.respondWith(
    caches.match(request, { ignoreVary: true }).then(
      (cached) =>
        cached ||
        fetch(request).then((response) => {
          if (response.ok)
            caches.open(shellCache).then((cache) => cache.put(request, response.clone()));
          return response;
        }),
    ),
  );
});
