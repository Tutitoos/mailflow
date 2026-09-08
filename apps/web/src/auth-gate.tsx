import { Check, Languages, LoaderCircle, LockKeyhole } from "lucide-react";
import { type FormEvent, type ReactNode, useCallback, useEffect, useState } from "react";
import {
  AuthRequestError,
  createOwner,
  getSessionLocale,
  getSetupStatus,
  signIn,
} from "./auth-client";
import { Button } from "./components/ui/button";
import { installTranslationCatalog, type Locale, type TranslationKey, translate } from "./i18n";
import { loadTranslationCatalog } from "./mailflow-api";
import { Brand } from "./pages";

type Phase = "loading" | "setup" | "sign-in" | "app" | "error";
type Translator = (key: TranslationKey) => string;

function LanguageButton({ locale, onChange }: { locale: Locale; onChange: () => void }) {
  return (
    <Button className="locale-button" size="sm" onClick={onChange} type="button">
      <Languages size={16} /> {locale.toUpperCase()}
    </Button>
  );
}

function AuthFrame({
  locale,
  onLocaleChange,
  children,
}: {
  locale: Locale;
  onLocaleChange: () => void;
  children: ReactNode;
}) {
  return (
    <main className="auth-shell">
      <header className="auth-header">
        <Brand />
        <LanguageButton locale={locale} onChange={onLocaleChange} />
      </header>
      {children}
      <footer>Mailflow · AGPL-3.0 · Self-hosted</footer>
    </main>
  );
}

function errorTranslation(error: unknown): TranslationKey {
  if (!(error instanceof AuthRequestError)) return "authUnavailable";
  if (error.code === "bootstrap_rejected") return "bootstrapRejected";
  if (error.code === "registration_closed") return "registrationClosed";
  if (error.code === "invalid_credentials") return "invalidCredentials";
  return "authUnavailable";
}

function SetupForm({ locale, onComplete }: { locale: Locale; onComplete: () => void }) {
  const t: Translator = (key) => translate(locale, key);
  const [name, setName] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [bootstrapToken, setBootstrapToken] = useState("");
  const [error, setError] = useState<TranslationKey | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setSubmitting(true);
    setError(null);
    try {
      await createOwner({ name, email, password, locale, bootstrapToken });
      setPassword("");
      setBootstrapToken("");
      onComplete();
    } catch (cause) {
      setPassword("");
      setBootstrapToken("");
      setError(errorTranslation(cause));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <section className="auth-card" aria-labelledby="setup-title">
      <div className="auth-icon" aria-hidden="true">
        <LockKeyhole size={21} />
      </div>
      <span className="auth-kicker">01 / 01</span>
      <h1 id="setup-title">{t("setupTitle")}</h1>
      <p>{t("setupDescription")}</p>
      <form onSubmit={submit}>
        <label>
          <span>{t("name")}</span>
          <input
            autoComplete="name"
            required
            value={name}
            onChange={(event) => setName(event.target.value)}
          />
        </label>
        <label>
          <span>{t("email")}</span>
          <input
            autoComplete="email"
            inputMode="email"
            required
            type="email"
            value={email}
            onChange={(event) => setEmail(event.target.value)}
          />
        </label>
        <label>
          <span>{t("password")}</span>
          <input
            autoComplete="new-password"
            minLength={12}
            required
            type="password"
            value={password}
            onChange={(event) => setPassword(event.target.value)}
          />
          <small>{t("passwordHint")}</small>
        </label>
        <label>
          <span>{t("bootstrapToken")}</span>
          <input
            autoComplete="off"
            required
            spellCheck={false}
            type="password"
            value={bootstrapToken}
            onChange={(event) => setBootstrapToken(event.target.value)}
          />
          <small>{t("bootstrapHint")}</small>
        </label>
        {error && (
          <p className="auth-error" role="alert">
            {t(error)}
          </p>
        )}
        <Button variant="primary" type="submit" disabled={submitting}>
          {submitting ? <LoaderCircle className="spin" size={16} /> : <Check size={16} />}
          {t("createOwner")}
        </Button>
      </form>
    </section>
  );
}

function SignInForm({ locale, onComplete }: { locale: Locale; onComplete: () => void }) {
  const t: Translator = (key) => translate(locale, key);
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<TranslationKey | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setSubmitting(true);
    setError(null);
    try {
      await signIn(email, password);
      setPassword("");
      onComplete();
    } catch (cause) {
      setPassword("");
      setError(errorTranslation(cause));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <section className="auth-card compact" aria-labelledby="sign-in-title">
      <div className="auth-icon" aria-hidden="true">
        <LockKeyhole size={21} />
      </div>
      <h1 id="sign-in-title">{t("signInTitle")}</h1>
      <p>{t("signInDescription")}</p>
      <form onSubmit={submit}>
        <label>
          <span>{t("email")}</span>
          <input
            autoComplete="email"
            required
            type="email"
            value={email}
            onChange={(event) => setEmail(event.target.value)}
          />
        </label>
        <label>
          <span>{t("password")}</span>
          <input
            autoComplete="current-password"
            required
            type="password"
            value={password}
            onChange={(event) => setPassword(event.target.value)}
          />
        </label>
        {error && (
          <p className="auth-error" role="alert">
            {t(error)}
          </p>
        )}
        <Button variant="primary" type="submit" disabled={submitting}>
          {submitting && <LoaderCircle className="spin" size={16} />}
          {t("signIn")}
        </Button>
      </form>
    </section>
  );
}

export function AuthGate({ renderApp }: { renderApp: (locale: Locale) => ReactNode }) {
  const [locale, setLocale] = useState<Locale>("en");
  const [phase, setPhase] = useState<Phase>("loading");
  const [, setCatalogRevision] = useState(0);
  const t: Translator = (key) => translate(locale, key);

  const load = useCallback(async (signal?: AbortSignal) => {
    setPhase("loading");
    try {
      const configured = await getSetupStatus(signal);
      if (!configured) {
        setPhase("setup");
        return;
      }
      const sessionLocale = await getSessionLocale(signal);
      if (sessionLocale) {
        setLocale(sessionLocale);
        setPhase("app");
      } else {
        setPhase("sign-in");
      }
    } catch {
      if (signal?.aborted) return;
      setPhase("error");
    }
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    void load(controller.signal);
    return () => controller.abort();
  }, [load]);

  useEffect(() => {
    const controller = new AbortController();
    void loadTranslationCatalog(locale, controller.signal)
      .then((catalog) => {
        if (installTranslationCatalog(catalog)) setCatalogRevision(catalog.revision);
      })
      .catch(() => undefined);
    return () => controller.abort();
  }, [locale]);

  if (phase === "app") return renderApp(locale);

  return (
    <AuthFrame
      locale={locale}
      onLocaleChange={() => setLocale((value) => (value === "en" ? "es" : "en"))}
    >
      {phase === "loading" && (
        <section className="auth-state" aria-live="polite">
          <LoaderCircle className="spin" size={22} />
          <span>{t("checkingSetup")}</span>
        </section>
      )}
      {phase === "error" && (
        <section className="auth-state" role="alert">
          <strong>{t("authUnavailable")}</strong>
          <Button variant="outline" onClick={() => void load()}>
            {t("tryAgain")}
          </Button>
        </section>
      )}
      {phase === "setup" && <SetupForm locale={locale} onComplete={() => setPhase("app")} />}
      {phase === "sign-in" && <SignInForm locale={locale} onComplete={() => setPhase("app")} />}
    </AuthFrame>
  );
}
