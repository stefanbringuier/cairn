for (const code of document.querySelectorAll("pre > code.language-sh")) {
  const pre = code.parentElement;
  const command = code.textContent.trimEnd();
  const installer = command.startsWith("curl -fsSL") && command.includes("/scripts/install.sh | sh");
  const firstSteps = command.startsWith("export PATH=") && command.includes("cairn init");
  const label = installer ? "Install cairn" : firstSteps ? "Your first steps" : "Terminal";
  const card = document.createElement("div");
  card.className = `command-card${installer ? " install-command" : ""}`;
  const toolbar = document.createElement("div");
  toolbar.className = "command-toolbar";
  const title = document.createElement("span");
  title.className = "command-title";
  title.textContent = label;
  const button = document.createElement("button");
  button.type = "button";
  button.className = "copy-command";
  button.textContent = "Copy";
  button.setAttribute("aria-label", `Copy ${label.toLowerCase()} commands`);
  const status = document.createElement("span");
  status.className = "sr-only";
  status.setAttribute("role", "status");
  let reset;
  button.addEventListener("click", async () => {
    clearTimeout(reset);
    try {
      await navigator.clipboard.writeText(command);
      button.textContent = "Copied ✓";
      status.textContent = "Commands copied to clipboard.";
    } catch {
      const selection = window.getSelection();
      const range = document.createRange();
      range.selectNodeContents(code);
      selection.removeAllRanges();
      selection.addRange(range);
      button.textContent = "Press Ctrl+C";
      status.textContent = "Commands selected. Press Ctrl+C to copy.";
    }
    reset = setTimeout(() => { button.textContent = "Copy"; status.textContent = ""; }, 2500);
  });
  pre.before(card);
  toolbar.append(title, button);
  card.append(toolbar, pre, status);
}
