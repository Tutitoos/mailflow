import { afterEach, describe, expect, it } from "vitest";
import {
  builtinTranslations,
  currentTranslationRevision,
  installTranslationCatalog,
  resetTranslationCatalogForTests,
  translate,
} from "./i18n";

describe("translations", () => {
  afterEach(resetTranslationCatalogForTests);

  it("defaults to English and exposes Spanish", () => {
    resetTranslationCatalogForTests();
    expect(translate("en", "inbox")).toBe("Inbox");
    expect(translate("es", "inbox")).toBe("Recibidos");
    expect(translate("es", "accountMenu")).toBe("Menú de cuenta");
    expect(translate("es", "moreActions")).toBe("Más acciones");
    expect(translate("es", "trash")).toBe("Papelera");
    expect(translate("es", "formatting")).toBe("Formato");
    expect(Object.keys(builtinTranslations.es).sort()).toEqual(
      Object.keys(builtinTranslations.en).sort(),
    );
  });

  it("installs versioned runtime catalogs and rejects stale revisions", () => {
    resetTranslationCatalogForTests();
    expect(
      installTranslationCatalog({
        locale: "es",
        defaultLocale: "en",
        revision: 4,
        messages: { inbox: "Buzón personal" },
        missingKeys: [],
      }),
    ).toBe(true);
    expect(translate("es", "inbox")).toBe("Buzón personal");
    expect(translate("es", "starred")).toBe("Destacados");
    expect(currentTranslationRevision()).toBe(4);
    expect(
      installTranslationCatalog({
        locale: "es",
        defaultLocale: "en",
        revision: 3,
        messages: { inbox: "Antiguo" },
        missingKeys: [],
      }),
    ).toBe(false);
    expect(translate("es", "inbox")).toBe("Buzón personal");
  });
});
