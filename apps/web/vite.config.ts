import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    port: 4310,
    strictPort: true,
  },
  preview: {
    port: 4311,
    strictPort: true,
  },
});
