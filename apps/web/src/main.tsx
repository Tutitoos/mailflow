import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { lazy, StrictMode, Suspense } from "react";
import { createRoot } from "react-dom/client";
import { createBrowserRouter, RouterProvider } from "react-router";
import { AuthGate } from "./auth-gate";
import { registerDesktopOfflineShell } from "./desktop-cache";
import { initializeDesktopRuntime } from "./desktop-runtime";
import { type Locale, translate } from "./i18n";
import { AccountsPage, MailPage } from "./pages";
import { initializeTelemetry } from "./telemetry";
import "./styles.css";

const AdminPage = lazy(async () => ({ default: (await import("./admin-page")).AdminPage }));

function AdminRoute({ locale }: { locale: Locale }) {
  return (
    <Suspense
      fallback={
        <main className="mail-state" role="status">
          <span className="loading-spinner" />
          <strong>{translate(locale, "admin.loading")}</strong>
        </main>
      }
    >
      <AdminPage locale={locale} />
    </Suspense>
  );
}

const queryClient = new QueryClient({
  defaultOptions: {
    queries: { staleTime: 30_000, retry: 1 },
  },
});

initializeDesktopRuntime();
registerDesktopOfflineShell();
initializeTelemetry();

const router = createBrowserRouter([
  { path: "/", element: <AuthGate renderApp={(locale) => <MailPage initialLocale={locale} />} /> },
  { path: "/admin", element: <AuthGate renderApp={(locale) => <AdminRoute locale={locale} />} /> },
  {
    path: "/admin/:section",
    element: <AuthGate renderApp={(locale) => <AdminRoute locale={locale} />} />,
  },
  {
    path: "/settings/accounts",
    element: <AuthGate renderApp={(locale) => <AccountsPage locale={locale} />} />,
  },
]);

const root = document.getElementById("root");
if (!root) throw new Error("Missing root element");

createRoot(root).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  </StrictMode>,
);
