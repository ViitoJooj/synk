import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Tauri runs the dev server on a fixed port and shows its own errors, so Vite
// must not pick another port or clear the terminal.
export default defineConfig({
  plugins: [react()],
  clearScreen: false,
  server: {
    port: 1420,
    strictPort: true,
    watch: { ignored: ["**/src-tauri/**"] },
  },
  envPrefix: ["VITE_", "TAURI_ENV_"],
  build: {
    target: "es2022",
    sourcemap: false,
  },
});
