const forbiddenElements = [
  "script",
  "style",
  "form",
  "input",
  "button",
  "select",
  "textarea",
  "iframe",
  "frame",
  "object",
  "embed",
  "svg",
  "math",
  "meta",
  "link",
  "base",
];

const globalAttributes = new Set(["title", "dir", "lang"]);

function safeURL(value: string, schemes: string[]) {
  try {
    const parsed = new URL(value, "https://mailflow.invalid");
    return schemes.includes(parsed.protocol);
  } catch {
    return false;
  }
}

export function buildSafeMailDocument(markup: string, allowRemoteImages: boolean) {
  const parsed = new DOMParser().parseFromString(markup, "text/html");
  for (const element of parsed.querySelectorAll(forbiddenElements.join(","))) element.remove();

  for (const element of parsed.body.querySelectorAll("*")) {
    for (const attribute of [...element.attributes]) {
      const name = attribute.name.toLowerCase();
      const allowed =
        globalAttributes.has(name) ||
        (element instanceof HTMLAnchorElement && name === "href") ||
        (element instanceof HTMLImageElement &&
          ["src", "data-mailflow-src", "alt", "width", "height"].includes(name)) ||
        ((element.tagName === "TD" || element.tagName === "TH") &&
          (name === "colspan" || name === "rowspan"));
      if (!allowed) element.removeAttribute(attribute.name);
    }

    if (element instanceof HTMLAnchorElement) {
      if (!safeURL(element.getAttribute("href") ?? "", ["http:", "https:", "mailto:"])) {
        element.removeAttribute("href");
      }
      element.target = "_blank";
      element.rel = "noopener noreferrer";
    }

    if (element instanceof HTMLImageElement) {
      const source = element.dataset.mailflowSrc || element.getAttribute("src") || "";
      element.removeAttribute("src");
      element.removeAttribute("srcset");
      if (!safeURL(source, ["http:", "https:"])) {
        delete element.dataset.mailflowSrc;
        element.remove();
      } else if (allowRemoteImages) {
        delete element.dataset.mailflowSrc;
        element.src = source;
        element.setAttribute("referrerpolicy", "no-referrer");
        element.setAttribute("loading", "lazy");
      } else {
        element.dataset.mailflowSrc = source;
      }
    }
  }

  const imagePolicy = allowRemoteImages ? "https: http:" : "'none'";
  return `<!doctype html><html><head><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; img-src ${imagePolicy}; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'; frame-src 'none'"><meta name="referrer" content="no-referrer"><style>:root{color-scheme:dark}body{margin:0;padding:4px;color:#ededed;background:#0a0a0a;font:14px/1.55 system-ui,sans-serif;overflow-wrap:anywhere}a{color:#fafafa}img{max-width:100%;height:auto}img:not([src]){display:none}table{max-width:100%;border-collapse:collapse}pre{white-space:pre-wrap}</style></head><body>${parsed.body.innerHTML}</body></html>`;
}
