// @vitest-environment jsdom

import { describe, expect, it } from "vitest";
import { buildSafeMailDocument } from "./safe-mail";

describe("isolated mail document", () => {
  it("removes active content and keeps remote images inert by default", () => {
    const document = buildSafeMailDocument(
      `<script>private()</script><form action="https://example.test"><input></form><a href="javascript:private()">link</a><img src="https://images.example.test/pixel" onerror="private()"><iframe src="https://example.test"></iframe>`,
      false,
    );
    const body = document.slice(document.indexOf("<body>"));
    expect(body).not.toMatch(/<script|<form|<input|<iframe|javascript:|onerror=|<img[^>]*\ssrc=/i);
    expect(document).toContain(`data-mailflow-src="https://images.example.test/pixel"`);
    expect(document).toContain(`img-src 'none'`);
  });

  it("authorizes only http images without referrer or active privileges", () => {
    const document = buildSafeMailDocument(
      `<img data-mailflow-src="https://images.example.test/pixel"><img src="data:text/html,private">`,
      true,
    );
    expect(document).toContain(`src="https://images.example.test/pixel"`);
    expect(document).toContain(`referrerpolicy="no-referrer"`);
    expect(document).not.toContain("data:text/html");
    expect(document).toContain("default-src 'none'");
  });
});
