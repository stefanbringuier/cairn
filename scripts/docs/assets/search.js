const field = document.querySelector("#search");
const results = document.querySelector("#results");
let pages;

field.addEventListener("input", async () => {
  const query = field.value.trim().toLocaleLowerCase();
  results.replaceChildren();
  results.hidden = !query;
  if (!query) return;
  try {
    pages ??= fetch("search.json").then((response) => {
      if (!response.ok) throw new Error("Search unavailable");
      return response.json();
    });
    const index = await pages;
    if (field.value.trim().toLocaleLowerCase() !== query) return;
    for (const page of index.filter((page) => `${page.title} ${page.text}`.toLocaleLowerCase().includes(query))) {
      const link = document.createElement("a");
      link.href = page.url;
      link.textContent = page.title;
      const snippet = document.createElement("small");
      const at = Math.max(0, page.text.toLocaleLowerCase().indexOf(query) - 30);
      snippet.textContent = `${page.text.slice(at, at + 100)}…`;
      link.append(snippet);
      results.append(link);
    }
    if (!results.childElementCount) results.textContent = "No matches. Try a command or destination.";
  } catch {
    pages = undefined;
    results.textContent = "Search is unavailable. Use the page links below.";
  }
});
