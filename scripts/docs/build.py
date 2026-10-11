"""Build a static docs site from the same Markdown people read in the repo."""

from html import escape
from html.parser import HTMLParser
import json
from pathlib import Path
import re
import shutil
from urllib.parse import quote, unquote, urlsplit, urlunsplit

import markdown

ROOT = Path(__file__).resolve().parents[2]
OUTPUT = ROOT / "site"
REPO = "https://github.com/stefanbringuier/cairn"
PAGES = {
    "README.md": ("index.html", "Start here"),
    "docs/usage.md": ("usage.html", "Use cairn"),
    "docs/design.md": ("design.html", "Design & extension"),
    "docs/spec.md": ("spec.html", "Specification"),
    "CONTRIBUTING.md": ("contributing.html", "Contribute"),
    "AGENTS.md": ("agents.html", "Agent directives"),
    "docs/agent-safety.md": ("agent-safety.html", "Agents & safety"),
    "docs/publishing.md": ("publishing.html", "Releases & publishing"),
    "SECURITY.md": ("security.html", "Security"),
}


class Document(HTMLParser):
    def __init__(self, text):
        super().__init__()
        self.ids, self.links, self.text = set(), [], []
        self.feed(text)

    def handle_starttag(self, tag, attrs):
        attrs = dict(attrs)
        if "id" in attrs:
            self.ids.add(attrs["id"])
        for attr in ("href", "src"):
            if attr in attrs:
                self.links.append(attrs[attr])

    def handle_data(self, text):
        self.text.append(text)


def rewrite(match, source):
    attr, link = match.groups()
    parts = urlsplit(link)
    if parts.scheme or parts.netloc or not parts.path:
        return match.group()
    target = (source.parent / unquote(parts.path)).resolve()
    relative = target.relative_to(ROOT).as_posix()
    if not target.exists():
        raise ValueError(f"{source.relative_to(ROOT)}: missing {link}")
    if relative in PAGES:
        path = PAGES[relative][0]
    elif relative.startswith("docs/images/"):
        path = relative.removeprefix("docs/")
    else:
        kind = "tree" if target.is_dir() else "blob"
        path = f"{REPO}/{kind}/main/{quote(relative)}"
    return f'{attr}="{escape(urlunsplit(("", "", path, parts.query, parts.fragment)), quote=True)}"'


def build():
    OUTPUT.mkdir(exist_ok=True)
    shutil.copytree(ROOT / "docs/images", OUTPUT / "images", dirs_exist_ok=True)
    shutil.copytree(ROOT / "scripts/docs/assets", OUTPUT / "assets", dirs_exist_ok=True)
    template = (ROOT / "scripts/docs/template.html").read_text()
    entries = []
    for source_name, (filename, title) in PAGES.items():
        source = ROOT / source_name
        text = source.read_text()
        if source_name == "README.md":
            # GitHub parses Markdown in this HTML block; Python-Markdown needs an opt-in.
            text = text.replace('<div align="center">', '<div class="hero" markdown="1">')
            text = text.replace("<details>", '<details markdown="1">')
            text = re.sub(r"<sub>.*?</sub>", "", text, flags=re.S)
            # Keep the website's assets local; live GitHub badges belong in the README.
            text = "\n".join(line for line in text.splitlines() if not line.startswith("[!["))
        renderer = markdown.Markdown(extensions=["extra", "toc", "sane_lists"])
        body = renderer.convert(text)
        body = re.sub(r'(href|src)="([^"]+)"', lambda match: rewrite(match, source), body)
        links = []
        for path, label in PAGES.values():
            current = 'aria-current="page"' if path == filename else ""
            links.append(f'<a href="{path}" {current}>{escape(label)}</a>')
        nav = "".join(links)
        page = template
        for key, value in {
            "title": escape(title), "nav": nav, "body": body,
            "toc": renderer.toc, "source": f"{REPO}/blob/main/{quote(source_name)}",
        }.items():
            page = page.replace("{{ " + key + " }}", value)
        OUTPUT.joinpath(filename).write_text(page)
        entries.append({"title": title, "url": filename, "text": " ".join(Document(body).text)})
    OUTPUT.joinpath("search.json").write_text(json.dumps(entries))
    OUTPUT.joinpath(".nojekyll").touch()
    validate()
    print(f"Built and validated {len(PAGES)} pages in {OUTPUT.relative_to(ROOT)}/")


def validate():
    documents = {path.name: Document(path.read_text()) for path in OUTPUT.glob("*.html")}
    for filename, doc in documents.items():
        for link in doc.links:
            parts = urlsplit(link)
            if parts.scheme or parts.netloc:
                continue
            target = OUTPUT / unquote(parts.path or filename)
            if not target.exists():
                raise ValueError(f"{filename}: broken site link {link}")
            if parts.fragment and target.name in documents:
                if unquote(parts.fragment) not in documents[target.name].ids:
                    raise ValueError(f"{filename}: missing anchor {link}")


if __name__ == "__main__":
    build()
