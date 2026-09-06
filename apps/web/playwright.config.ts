import { defineConfig } from "@playwright/test";

const viewports = [
  ["desktop-large", 1440, 900],
  ["desktop", 1280, 800],
  ["tablet-landscape", 1024, 768],
  ["tablet-portrait", 768, 1024],
  ["mobile-large", 430, 932],
  ["mobile", 390, 844],
] as const;

export default defineConfig({
  testDir: "./tests",
  fullyParallel: true,
  retries: process.env.CI ? 2 : 0,
  reporter: process.env.CI ? "github" : "list",
  use: {
    baseURL: "http://127.0.0.1:4310",
    channel: process.env.CI ? undefined : "chrome",
    colorScheme: "dark",
    locale: "en-US",
    reducedMotion: "reduce",
  },
  webServer: {
    command: "bun run dev",
    url: "http://127.0.0.1:4310",
    reuseExistingServer: !process.env.CI,
  },
  projects: viewports.map(([name, width, height]) => ({
    name,
    use: { viewport: { width, height } },
  })),
});
