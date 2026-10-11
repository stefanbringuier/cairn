import { spawn } from "node:child_process";
import { createInterface } from "node:readline";
import { fileURLToPath } from "node:url";
import path from "node:path";
import { chromium } from "playwright";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");
const server = spawn("python3", ["-u", "-c", `
from functools import partial
from http.server import SimpleHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
import signal, sys, tempfile
class Handler(SimpleHTTPRequestHandler):
    def log_message(self, *args): pass
signal.signal(signal.SIGTERM, lambda *_: sys.exit(0))
with tempfile.TemporaryDirectory(prefix="cairn-docs-preview-") as directory:
    Path(directory, "cairn").symlink_to(Path(sys.argv[1]))
    server = ThreadingHTTPServer(("127.0.0.1", 0), partial(Handler, directory=directory))
    print("http://127.0.0.1:%d/cairn/" % server.server_port, flush=True)
    server.serve_forever()
`, path.join(root, "site")], { stdio: ["ignore", "pipe", "inherit"] });
let browser;
try {
  const url = await new Promise((resolve, reject) => {
    server.on("error", reject);
    server.once("exit", () => reject(new Error("Docs server exited before starting")));
    createInterface({ input: server.stdout }).once("line", resolve);
  });
  browser = await chromium.launch({ executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH || undefined });
  const page = await browser.newPage({ viewport: { width: 1440, height: 1100 }, deviceScaleFactor: 1 });
  const errors = [];
  page.on("pageerror", (error) => errors.push(error.message));
  await page.goto(url, { waitUntil: "networkidle" });
  const checkWidth = async () => {
    if (await page.evaluate(() => document.documentElement.scrollWidth > innerWidth)) throw new Error("Docs overflow the viewport");
  };
  await checkWidth();
  if (await page.locator("img").evaluateAll((images) => images.some((image) => !image.complete || !image.naturalWidth))) {
    throw new Error("A documentation image failed to load");
  }
  await page.screenshot({ path: "/tmp/cairn-docs-desktop.png" });
  await page.locator("#search").fill("restore");
  await page.locator('#results a[href="usage.html"]').waitFor();
  await page.locator('#results a[href="usage.html"]').click();
  await page.waitForURL("**/cairn/usage.html");
  await page.locator('nav a[href="contributing.html"]').click();
  await page.locator("h2", { hasText: "Design belongs in the PR" }).waitFor();
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto(url, { waitUntil: "networkidle" });
  await checkWidth();
  await page.screenshot({ path: "/tmp/cairn-docs-mobile.png" });
  if (errors.length) throw new Error(errors.join("\n"));
  console.log("Docs passed desktop/mobile layout, search, and navigation under /cairn/.");
  console.log("Previews: /tmp/cairn-docs-desktop.png and /tmp/cairn-docs-mobile.png");
} finally {
  if (browser) await browser.close();
  server.kill("SIGTERM");
  await new Promise((resolve) => {
    if (server.exitCode !== null || server.signalCode !== null) resolve();
    else server.once("exit", resolve);
  });
}
