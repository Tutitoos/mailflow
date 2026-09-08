const desktopSessionKey = "mailflow.desktop";

export function desktopEntryState(href: string, previouslyActive: boolean) {
  const url = new URL(href);
  const marked = url.searchParams.get("desktop") === "1";
  url.searchParams.delete("desktop");
  return {
    active: previouslyActive || marked,
    cleanPath: `${url.pathname}${url.search}${url.hash}`,
    marked,
  };
}

export function initializeDesktopRuntime() {
  try {
    const state = desktopEntryState(
      window.location.href,
      window.sessionStorage.getItem(desktopSessionKey) === "1",
    );
    if (state.active) window.sessionStorage.setItem(desktopSessionKey, "1");
    if (state.marked) window.history.replaceState(null, "", state.cleanPath);
  } catch {
    // A storage policy must not prevent the ordinary web application from loading.
  }
}

export function isDesktopRuntime() {
  try {
    return window.sessionStorage.getItem(desktopSessionKey) === "1";
  } catch {
    return false;
  }
}
