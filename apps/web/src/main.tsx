import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { createBrowserRouter, RouterProvider } from "react-router";
import { AdminPage } from "./admin-page";
import { AuthGate } from "./auth-gate";
import { initializeDesktopRuntime } from "./desktop-runtime";
import { AccountsPage, MailPage } from "./pages";
import { initializeTelemetry } from "./telemetry";
import "./styles.css";

const queryClient = new QueryClient({
  defaultOptions: {
    queries: { staleTime: 30_000, retry: 1 },
  },
});

initializeDesktopRuntime();
initializeTelemetry();

const router = createBrowserRouter([
  { path: "/", element: <AuthGate renderApp={(locale) => <MailPage initialLocale={locale} />} /> },
  { path: "/admin", element: <AuthGate renderApp={(locale) => <AdminPage locale={locale} />} /> },
  {
    path: "/admin/:section",
    element: <AuthGate renderApp={(locale) => <AdminPage locale={locale} />} />,
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
