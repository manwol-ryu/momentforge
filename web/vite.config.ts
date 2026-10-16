import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { defineConfig, type Plugin } from "vite";
import react from "@vitejs/plugin-react";

// The Go server listens here during `make dev`.
const goServer = process.env.MF_SERVER ?? "http://127.0.0.1:8765";
const dataDir = process.env.MF_DATA ?? resolve(import.meta.dirname, "..", ".momentforge");

// In dev, Vite serves index.html, so it injects the access key that the Go
// server wrote for this run. The production build keeps the placeholder,
// which the Go server replaces when it serves the page.
function devAccessKey(): Plugin {
  return {
    name: "momentforge-dev-access-key",
    apply: "serve",
    transformIndexHtml(html) {
      let key = "";
      try {
        key = readFileSync(resolve(dataDir, "runtime", "access-key"), "utf8").trim();
      } catch {
        console.warn("[momentforge] access key not found. Start the Go server first (make dev).");
      }
      return html.replace("__MF_ACCESS_KEY__", key);
    },
  };
}

export default defineConfig({
  plugins: [react(), devAccessKey()],
  build: { outDir: "dist", emptyOutDir: true, assetsInlineLimit: 0 },
  server: {
    host: "127.0.0.1",
    port: 5173,
    strictPort: true,
    proxy: {
      "/api": {
        target: goServer,
        changeOrigin: true, // Host header → 127.0.0.1:8765 (passes the Host check)
        configure(proxy) {
          // The Go server only trusts its own origin; present the request as same-origin.
          proxy.on("proxyReq", (req) => {
            req.setHeader("Origin", goServer);
            req.removeHeader("Sec-Fetch-Site");
          });
        },
      },
    },
  },
});
