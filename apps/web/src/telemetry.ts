import * as Sentry from "@sentry/react";

const MASKED = "[Masked]";

export function initializeTelemetry() {
  const dsn = import.meta.env.VITE_SENTRY_DSN?.trim();
  if (!dsn) return;

  const replayEnabled = import.meta.env.VITE_SENTRY_REPLAY_ENABLED === "true";
  Sentry.init({
    dsn,
    tracesSampleRate: 0.1,
    replaysSessionSampleRate: replayEnabled ? 0.05 : 0,
    replaysOnErrorSampleRate: replayEnabled ? 0.1 : 0,
    integrations: replayEnabled
      ? [
          Sentry.replayIntegration({
            maskAllText: true,
            maskAllInputs: true,
            blockAllMedia: true,
            block: ["[data-sentry-block]", "[data-mail-content]"],
            unmask: [],
            unblock: [],
          }),
        ]
      : [],
    beforeSend(event) {
      event.user = undefined;
      event.request = undefined;
      event.extra = undefined;
      event.breadcrumbs = undefined;
      if (event.message) event.message = MASKED;
      for (const exception of event.exception?.values ?? []) {
        if (exception.value) exception.value = MASKED;
        for (const frame of exception.stacktrace?.frames ?? []) {
          frame.abs_path = undefined;
          frame.context_line = undefined;
          frame.pre_context = undefined;
          frame.post_context = undefined;
          frame.vars = undefined;
        }
      }
      return event;
    },
    beforeSendTransaction(event) {
      event.transaction = "mailflow.navigation";
      event.request = undefined;
      event.user = undefined;
      for (const span of event.spans ?? []) {
        span.description = undefined;
        span.data = {};
      }
      return event;
    },
  });
}
