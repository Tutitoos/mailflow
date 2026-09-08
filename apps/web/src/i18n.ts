import catalogs from "../../../services/api/internal/modules/translations/catalogs.json";

export type Locale = "en" | "es";

export const builtinTranslations = catalogs;
export type TranslationKey = keyof (typeof builtinTranslations)["en"];

type RuntimeCatalog = Partial<Record<Locale, Partial<Record<TranslationKey, string>>>>;

let runtimeCatalog: RuntimeCatalog = {};
let runtimeRevision = 0;

export type TranslationCatalogPayload = {
  locale: Locale;
  defaultLocale: "en";
  revision: number;
  messages: Record<string, string>;
  missingKeys: string[];
};

export function installTranslationCatalog(payload: TranslationCatalogPayload) {
  if (
    (payload.locale !== "en" && payload.locale !== "es") ||
    payload.defaultLocale !== "en" ||
    !Number.isSafeInteger(payload.revision) ||
    payload.revision < runtimeRevision
  ) {
    return false;
  }
  const messages: Partial<Record<TranslationKey, string>> = {};
  for (const [key, value] of Object.entries(payload.messages)) {
    if (
      Object.hasOwn(builtinTranslations.en, key) &&
      typeof value === "string" &&
      value.length > 0 &&
      value.length <= 4096
    ) {
      messages[key as TranslationKey] = value;
    }
  }
  runtimeCatalog = { ...runtimeCatalog, [payload.locale]: messages };
  runtimeRevision = payload.revision;
  return true;
}

export function currentTranslationRevision() {
  return runtimeRevision;
}

export function resetTranslationCatalogForTests() {
  runtimeCatalog = {};
  runtimeRevision = 0;
}

export function translate(locale: Locale, key: TranslationKey) {
  return (
    runtimeCatalog[locale]?.[key] ??
    runtimeCatalog.en?.[key] ??
    builtinTranslations[locale][key] ??
    builtinTranslations.en[key]
  );
}
