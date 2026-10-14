import { spawn } from "node:child_process";
import { readFile, mkdir } from "node:fs/promises";
import { createInterface } from "node:readline";
import { fileURLToPath } from "node:url";
import path from "node:path";
import { chromium } from "playwright";

const here = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(here, "../..");
const output = path.join(root, "docs/images");
await mkdir(output, { recursive: true });

const browser = await chromium.launch({
  executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH || undefined,
});
let fixture;
try {
  const page = await browser.newPage({ viewport: { width: 1320, height: 760 }, deviceScaleFactor: 2 });
  const css = await readFile(path.join(here, "node_modules/@xterm/xterm/css/xterm.css"), "utf8");
  await page.setContent(`<!doctype html><html><head><style>${css}
    * { box-sizing: border-box; }
    body { margin: 0; padding: 32px; background: #0e111a; color: #c0caf5; font-family: sans-serif; }
    .window { overflow: hidden; border: 1px solid #343b55; border-radius: 14px; background: #1a1b26; box-shadow: 0 24px 64px #0005; }
    .titlebar { height: 48px; display: flex; align-items: center; gap: 9px; padding: 0 20px; background: #202230; border-bottom: 1px solid #343b55; }
    .dot { width: 11px; height: 11px; border-radius: 50%; background: #f7768e; }
    .dot:nth-child(2) { background: #e0af68; } .dot:nth-child(3) { background: #9ece6a; }
    .label { margin-left: auto; margin-right: auto; font: 12px monospace; color: #7f8c98; }
    #terminal { padding: 24px; }
    .xterm-cursor-layer { opacity: 0; }
  </style></head><body><div class="window"><div class="titlebar"><i class="dot"></i><i class="dot"></i><i class="dot"></i><span class="label">cairn · demo-laptop</span></div><div id="terminal"></div></div></body></html>`);
  await page.addScriptTag({ path: path.join(here, "node_modules/@xterm/xterm/lib/xterm.js") });
  await page.evaluate(() => {
    window.terminal = new Terminal({
      cols: 110, rows: 32, fontSize: 17, lineHeight: 1.2,
      fontFamily: '"DejaVu Sans Mono", monospace', cursorBlink: false,
      theme: { background: "#1a1b26", foreground: "#c0caf5", cursor: "#1a1b26" },
      allowProposedApi: true,
    });
    terminal.open(document.getElementById("terminal"));
    window.screenText = () => Array.from({ length: terminal.rows }, (_, row) =>
      terminal.buffer.active.getLine(row)?.translateToString(true) ?? "").join("\n");
  });
  await page.exposeFunction("sendInput", (input) => {
    if (fixture && !fixture.stdin.destroyed) fixture.stdin.write(JSON.stringify({ input }) + "\n");
  });
  await page.evaluate(() => terminal.onData((input) => window.sendInput(input)));
  let writes = Promise.resolve();
  let writeError;
  const startFixture = (mode = "tui") => {
    fixture = spawn("python3", [path.join(here, "fixture.py"), path.join(root, "bin/cairn"), mode], {
      stdio: ["pipe", "pipe", "inherit"],
    });
    fixture.on("error", (error) => { writeError = error; });
    createInterface({ input: fixture.stdout }).on("line", (line) => {
      const { output } = JSON.parse(line);
      writes = writes.then(() => page.evaluate((encoded) => new Promise((resolve) => {
        terminal.write(Uint8Array.from(atob(encoded), (char) => char.charCodeAt(0)), resolve);
      }), output)).catch((error) => { writeError = error; });
    });
  };
  const stopFixture = async () => {
    fixture.stdin.end();
    fixture.kill("SIGTERM");
    await new Promise((resolve) => {
      if (fixture.exitCode !== null || fixture.signalCode !== null) resolve();
      else fixture.once("exit", resolve);
    });
    await writes;
  };
  startFixture();
  const ready = async (text) => {
    const deadline = Date.now() + 20000;
    while (Date.now() < deadline) {
      await writes;
      if (writeError) throw writeError;
      if (fixture.exitCode !== null || fixture.signalCode !== null) {
        throw new Error(`Demo process exited before displaying ${JSON.stringify(text)}`);
      }
      if (await page.evaluate((expected) => screenText().includes(expected), text)) return;
      await page.waitForTimeout(100);
    }
    throw new Error(`Waiting for ${JSON.stringify(text)}; screen:\n${await page.evaluate(() => screenText())}`);
  };
  const key = (input) => page.evaluate((value) => window.sendInput(value), input);
  const grab = async (name) => {
    // Crop unused rows without resizing the PTY or changing the application's view.
    await page.evaluate(() => {
      const lines = screenText().split("\n");
      const used = lines.findLastIndex((line) => line.trim()) + 2;
      const screen = document.querySelector(".xterm-screen");
      const rowHeight = screen.getBoundingClientRect().height / terminal.rows;
      const frame = document.getElementById("terminal");
      frame.style.height = `${Math.min(terminal.rows, used) * rowHeight + 48}px`;
      frame.style.overflow = "hidden";
    });
    await page.locator(".window").screenshot({ path: path.join(output, `${name}.png`) });
    await page.evaluate(() => { document.getElementById("terminal").style.height = ""; });
    console.log(`Captured docs/images/${name}.png`);
  };

  await ready("4 kept");
  await grab("dashboard");
  await key("r");
  await ready("snapshot");
  await ready("items");
  await grab("restore-browser");
  await key("\r");
  await ready("launch-plan.md");
  await key("r");
  await ready("will be saved first");
  await grab("restore-preview");
  await key("n");
  await ready("launch-plan.md");
  await key("\u001b");
  await ready("BACKUP TARGET");
  await key("t");
  await ready("Where can I save backups?");
  await ready("More cloud destinations");
  await grab("storage-guide");
  await key("\u001b[B\u001b[B");
  await key("\r");
  await ready("gdrive:backups");
  await grab("drive-setup");
  await key("?");
  await ready("A saved version. A way back.");
  await grab("help");
  await key("\u001b");
  await ready("gdrive:backups");
  await key("\u001b");
  await ready("Where can I save backups?");
  await key("\u001b");
  await ready("BACKUP TARGET");
  await key("e");
  await ready("Snapshot every");
  await grab("settings");
  await key("\u001b[C");
  await ready("6h");
  await key("\u001b");
  await ready("BACKUP TARGET");
  // Exercise terminal resizing after capture, including the compact dashboard.
  await page.evaluate(() => terminal.resize(60, 24));
  fixture.stdin.write(JSON.stringify({ resize: [24, 60] }) + "\n");
  await ready("FULL BACKUPS");
  await ready("quit");
  console.log("Verified settings navigation and the compact dashboard at 60×24.");
  await key("q");
  await stopFixture();
  await page.evaluate(() => {
    terminal.reset();
    terminal.resize(110, 32);
    document.querySelector(".label").textContent = "cairn --profile nas · demo-laptop";
  });
  startFixture("profile");
  await ready("BACKUP TARGET · nas");
  await ready("4 kept");
  await grab("profile-dashboard");
  await key("r");
  await ready("items");
  await key("/");
  await ready("Filter:");
  await key("snapshot");
  await ready("Filter: snapshot");
  await key("\r");
  await ready("Saved versions");
  await ready("filtered");
  await key("\u001b");
  const clearDeadline = Date.now() + 10000;
  while (Date.now() < clearDeadline) {
    await writes;
    if (await page.evaluate(() => !screenText().includes("filtered"))) break;
    await page.waitForTimeout(100);
  }
  if (await page.evaluate(() => screenText().includes("filtered"))) {
    throw new Error("Named-profile list filter did not clear with Escape");
  }
  await key("\u001b");
  await ready("BACKUP TARGET · nas");
  for (const cols of [40, 60, 110]) {
    await page.evaluate((width) => terminal.resize(width, 24), cols);
    fixture.stdin.write(JSON.stringify({ resize: [24, cols] }) + "\n");
    await ready("FULL BACKUPS");
    await ready("quit");
    if (cols === 40) await grab("profile-small");
    await key("?");
    await ready("A saved version.");
    await key("\u001b");
    await ready("BACKUP TARGET · nas");
  }
  console.log("Verified named-profile keyboard filtering, long target paths, help/back and 40/60/110×24 resizing.");
  await key("q");
  await stopFixture();
  await page.evaluate(() => {
    terminal.reset();
    terminal.resize(110, 32);
    document.querySelector(".label").textContent = "cairn restore launch-plan.md";
  });
  startFixture("restore");
  await ready("pre-restore");
  await ready("Cancel");
  await grab("cli-restore");
  await key("n\r");
} finally {
  if (fixture) {
    fixture.stdin.end();
    fixture.kill("SIGTERM");
    await new Promise((resolve) => {
      if (fixture.exitCode !== null || fixture.signalCode !== null) resolve();
      else fixture.once("exit", resolve);
    });
  }
  await browser.close();
}
