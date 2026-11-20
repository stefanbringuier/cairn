import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { readFile, mkdir } from "node:fs/promises";
import { createInterface } from "node:readline";
import { fileURLToPath } from "node:url";
import path from "node:path";
import { chromium } from "playwright";

const here = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(here, "../..");
const requested = process.env.CAIRN_SCREENSHOT_ONLY?.split(",");
const profileCaptures = ["profiles-overview", "profiles-overview-small"];
const onlyProfiles = Boolean(requested?.length && requested.every((name) => profileCaptures.includes(name)));
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
  let receiptResolver;
  const startFixture = (mode = "tui") => {
    fixture = spawn("python3", [path.join(here, "fixture.py"), path.join(root, "bin/cairn"), mode], {
      stdio: ["pipe", "pipe", "inherit"],
    });
    fixture.on("error", (error) => { writeError = error; });
    createInterface({ input: fixture.stdout }).on("line", (line) => {
      const { output, receipt } = JSON.parse(line);
      if (receipt) {
        receiptResolver?.(receipt);
        return;
      }
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
  const receipt = (message = { receipt: true }) => new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error("No fixture receipt")), 20000);
    receiptResolver = (result) => {
      clearTimeout(timer);
      receiptResolver = undefined;
      resolve(result);
    };
    fixture.stdin.write(JSON.stringify(message) + "\n");
  });
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
    if (process.env.CAIRN_SCREENSHOT_ONLY && !process.env.CAIRN_SCREENSHOT_ONLY.split(",").includes(name)) return;
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

  const openProfiles = async (current) => {
    await key("p");
    await ready("Read-only");
    await ready("enter");
    await ready("current");
    assert.match(await page.evaluate(() => screenText()), new RegExp(`cairn · ${current}\\s+/\\s+profiles`));
  };
  const screen = () => page.evaluate(() => screenText());
  const requireAbsent = async (text) => {
    const deadline = Date.now() + 3000;
    do {
      await writes;
      if (!(await screen()).includes(text)) return;
      await page.waitForTimeout(50);
    } while (Date.now() < deadline);
    throw new Error(`Unexpected ${JSON.stringify(text)} in terminal:\n${await screen()}`);
  };
  const choose = async (filter, profile) => {
    await key("/");
    await key(filter);
    await ready(`Filter: ${filter}`);
    await key("\r");
    await key("\r");
    await ready(`BACKUP TARGET · ${profile}`);
    await ready("1 kept");
  };
  const profileRun = async (mode, cols, rows, capture, effects) => {
    const current = mode === "profiles-nas" ? "nas" : "default";
    await page.evaluate(([cols, rows, current]) => {
      terminal.reset();
      terminal.resize(cols, rows);
      document.querySelector(".window").style.width = cols === 40 ? "fit-content" : "100%";
      document.querySelector(".label").textContent = current === "nas" ? "cairn --profile nas · demo" : "cairn · demo-laptop";
    }, [cols, rows, current]);
    startFixture(mode);
    await ready(`BACKUP TARGET · ${current}`);
    await ready("1 kept");
    await ready("profiles");
    await key("?");
    await ready("A saved version.");
    await key("\u001b");
    await ready(`BACKUP TARGET · ${current}`);
    await openProfiles(current);
    await ready(`${current} · current`);
    const before = await receipt();
    if (cols === 110) {
      const view = await screen();
      assert.ok(view.indexOf("cloud ·") < view.indexOf("default ·") && view.indexOf("default ·") < view.indexOf("nas ·"));
      await ready("example.test/backups/cloud-projects");
      await ready("data-only");
      await ready("daily · disabled");
      await ready("daily · unknown");
    }
    for (const secret of ["fixture-username", "fixture-secret", "fixture-token", "fixture-fragment"]) await requireAbsent(secret);
    await grab(capture);
    await key("j");
    await key("k");
    await key("\u001b[B");
    await key("\u001b[A");
    await key("/");
    await key("not-a-profile");
    await ready("No matching profiles");
    await key("\u001b");
    await ready(`${current} · current`);
    await key("/");
    await key("office-archive");
    await ready("Filter: office-archive");
    await key("\r");
    await ready("nas ·");
    await requireAbsent("cloud ·");
    await key("\u001b");
    await requireAbsent("Filter:");
    await key("r");
    await ready("Read-only");
    for (const cols of [60, 110]) {
      await page.evaluate((width) => terminal.resize(width, 24), cols);
      fixture.stdin.write(JSON.stringify({ resize: [24, cols] }) + "\n");
      await ready("refresh");
    }
    const observed = await receipt();
    assert.deepEqual(observed.control, before.control);
    assert.deepEqual(observed.repos, before.repos);
    assert.deepEqual(observed.restic, before.restic);
    assert.deepEqual(observed.notes, before.notes);
    assert.ok(observed.systemctl.slice(before.systemctl.length).every((line) => /--user (show|is-enabled) /.test(line)));
    await key("\u001b");
    await ready(`BACKUP TARGET · ${current}`);
    await key("t");
    await ready("Where can I save backups?");
    await key("\u001b");
    await ready(`BACKUP TARGET · ${current}`);
    await openProfiles(current);
    await choose("cloud", "cloud");
    const switched = await receipt();
    assert.deepEqual(switched.control, before.control);
    assert.deepEqual(switched.repos, before.repos);
    assert.ok(switched.restic.some((line) => /repository-cloud\|.*profiles\/cloud\/password.* snapshots/.test(line)));
    assert.ok(switched.systemctl.slice(before.systemctl.length).every((line) => /--user (show|is-enabled) /.test(line)));
    if (effects) {
      await key("s");
      await ready("snapshot started");
      await key("b");
      await ready("backup started");
      await key("e");
      await ready("cairn · cloud  /  settings");
      await ready("Snapshot every");
      await key("\u001b[C");
      await ready("6h");
      for (let i = 0; i < 4; i++) await key("\r");
      await ready("settings saved");
      await ready("1 kept");
      const settings = await receipt();
      assert.notEqual(settings.control["cairn/profiles/cloud/config.json"], before.control["cairn/profiles/cloud/config.json"]);
      for (const [file, digest] of Object.entries(before.control)) {
        if (file !== "cairn/profiles/cloud/config.json") assert.equal(settings.control[file], digest, file);
      }
      const calls = settings.systemctl.slice(switched.systemctl.length);
      for (const kind of ["snapshot", "backup"]) {
        assert.ok(calls.includes(`--user start --no-block cairn-cloud-${kind}.service`));
        assert.ok(calls.includes(`--user enable --now cairn-cloud-${kind}.timer`));
      }
      for (const line of calls) {
        if (!line.startsWith("--user show ")) assert.match(line, /^--user (?:daemon-reload|(?:start|enable) .*cairn-cloud-)/);
      }
      assert.deepEqual(settings.repos, before.repos); // The service shim tests dispatch, not backup execution.
      await key("r");
      await ready("Saved versions");
      await ready("1 item");
      await key("\r");
      await ready("cloud-notes.md");
      await key("r");
      await ready("will be saved first");
      assert.match(await screen(), /cairn · cloud\s+\/\s+restore \/ preview/);
      await key("p");
      await ready("will be saved first");
      await key("y");
      await ready("undo: cairn --profile cloud restore --at");
      const restored = await receipt();
      const undo = restored.repos["repository-cloud"].filter((id) => !before.repos["repository-cloud"].includes(id));
      assert.equal(undo.length, 1);
      assert.ok((await screen()).replace(/\n\s*/g, "").includes(undo[0]), "Undo command did not display the full snapshot ID");
      assert.equal(restored.notes["cloud-notes.md"], "cloud saved content\n");
      assert.deepEqual(restored.repos["repository-default"], before.repos["repository-default"]);
      assert.deepEqual(restored.repos["repository-nas"], before.repos["repository-nas"]);
      for (const [note, text] of Object.entries(before.notes)) {
        if (note !== "cloud-notes.md") assert.equal(restored.notes[note], text);
      }
      const undone = await receipt({ undo: undo[0] });
      assert.deepEqual(undone.notes, before.notes);
      for (const line of undone.restic.slice(switched.restic.length)) {
        if (line.endsWith("|version")) continue;
        assert.match(line, /repository-cloud\|.*profiles\/cloud\/password/);
      }
      console.log("Verified cloud-local settings, timer dispatch, history, restore, displayed undo and executed undo.");
      await key("\u001b");
      await ready("BACKUP TARGET · cloud");
    }
    const inFlight = effects ? await receipt({ background: "start" }) : undefined;
    await openProfiles("cloud");
    await choose("office-archive", "nas");
    if (inFlight) {
      const afterSwitch = await receipt();
      assert.deepEqual(afterSwitch.repos, inFlight.repos);
      assert.ok(afterSwitch.systemctl.slice(inFlight.systemctl.length).every((line) => /--user (show|is-enabled) /.test(line)));
      const completed = await receipt({ background: "finish" });
      assert.deepEqual(completed.repos["repository-default"], inFlight.repos["repository-default"]);
      assert.deepEqual(completed.repos["repository-nas"], inFlight.repos["repository-nas"]);
      assert.ok(completed.repos["repository-cloud"].some((id) => !inFlight.repos["repository-cloud"].includes(id)));
      console.log("Verified the generated cloud snapshot service continues across the switch to NAS.");
    }
    await key("q");
    await stopFixture();
    console.log(`Verified ${mode}: ${cols}×${rows}, filtering, local-only inventory, cloud→nas, live resize to 60×24 and 110×24.`);
  };

  if (onlyProfiles) {
    await profileRun("profiles", 110, 32, "profiles-overview", true);
    await profileRun("profiles-nas", 40, 24, "profiles-overview-small", false);
  } else {
  startFixture();
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
    document.querySelector(".label").textContent = "cairn --profile cloud · data-only setup example";
  });
  startFixture("data-only");
  await ready("DATA BACKUPS");
  await ready("4 kept");
  await grab("data-only-dashboard");
  await page.evaluate(() => terminal.resize(60, 24));
  fixture.stdin.write(JSON.stringify({ resize: [24, 60] }) + "\n");
  await ready("DATA BACKUPS");
  await ready("quit");
  await key("q");
  await stopFixture();
  await page.evaluate(() => {
    terminal.reset();
    terminal.resize(110, 32);
    document.querySelector(".label").textContent = "cairn · choose a profile";
  });
  startFixture("chooser");
  await ready("Choose a profile");
  await ready("personal");
  await ready("phin");
  await grab("profile-chooser");
  for (const cols of [40, 60, 110]) {
    await page.evaluate((width) => terminal.resize(width, 24), cols);
    fixture.stdin.write(JSON.stringify({ resize: [24, cols] }) + "\n");
    await ready("Choose a profile");
    await ready("ctrl+c");
  }
  await key("/");
  await key("no-such-profile");
  await ready("Nothing matched");
  await key("\u001b");
  await ready("personal");
  await key("/");
  await key("phin");
  await ready("Filter: phin");
  await key("\r");
  await ready("filtered");
  await key("\r");
  await ready("BACKUP TARGET · phin");
  await ready("0 kept");
  await key("q");
  await stopFixture();
  console.log("Verified named-only startup, empty filtering, profile selection and 40/60/110×24 resizing.");
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
  await stopFixture();
  if (!requested || requested.some((name) => profileCaptures.includes(name))) {
    await profileRun("profiles", 110, 32, "profiles-overview", true);
    await profileRun("profiles-nas", 40, 24, "profiles-overview-small", false);
  }
  }
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
