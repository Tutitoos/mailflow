import { describe, expect, it } from "vitest";
import { translate } from "./i18n";

describe("translations", () => {
  it("defaults to English and exposes Spanish", () => {
    expect(translate("en", "inbox")).toBe("Inbox");
    expect(translate("es", "inbox")).toBe("Recibidos");
  });
});
