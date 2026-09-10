import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

const root = resolve(import.meta.dir, "..");

describe("desktop mail frame CSP", () => {
  const config = JSON.parse(
    readFileSync(resolve(root, "apps/desktop/src-tauri/tauri.conf.json"), "utf8"),
  ) as { app: { security: { csp: string } } };
  const conversation = readFileSync(resolve(root, "apps/web/src/conversation.tsx"), "utf8");
  const safeMail = readFileSync(resolve(root, "apps/web/src/safe-mail.ts"), "utf8");
  const framePolicy = config.app.security.csp.match(/(?:^|;\s*)frame-src\s+([^;]+)/)?.[1];

  test("permits only revocable blob message frames", () => {
    expect(framePolicy).toBe("blob:");
    expect(conversation).toContain('sandbox="allow-popups"');
    expect(conversation).toContain("src={desktop ? desktopSource : undefined}");
    expect(conversation).toContain("srcDoc={desktop ? undefined : document}");
    expect(conversation).toContain("URL.createObjectURL");
    expect(conversation).toContain("URL.revokeObjectURL");
  });

  test("keeps active nested content denied inside the message document", () => {
    expect(safeMail).toContain("default-src 'none'");
    expect(safeMail).toContain("frame-src 'none'");
    expect(safeMail).toContain("form-action 'none'");
  });
});
