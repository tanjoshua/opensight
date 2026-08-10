// Injects partials/header.html and partials/footer.html into every page,
// between the markers each page carries. Pages stay directly servable static
// files; run this (via `npm run build`) after editing a partial.
import { readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = dirname(fileURLToPath(import.meta.url));

// Route each page answers on, used to mark the current nav entry.
const pages = {
  "index.html": "/",
  "monitor/index.html": "/monitor/",
  "improve/index.html": "/improve/",
  "pricing/index.html": "/pricing/",
  "faq/index.html": "/faq/",
};

const partial = (name) => readFileSync(join(root, "partials", `${name}.html`), "utf8").trim();
const header = partial("header");
const footer = partial("footer");

// The nav marks the page it sits on, which the shared partial cannot know.
function markCurrent(markup, route) {
  if (route === "/monitor/" || route === "/improve/") {
    markup = markup.replace(
      'data-menu-trigger class="nav-link ',
      'data-menu-trigger class="nav-link-current ',
    );
  }
  return markup
    .replace(
      `<a href="${route}" class="nav-link">`,
      `<a href="${route}" class="nav-link-current" aria-current="page">`,
    )
    .replace(
      `<a href="${route}" class="nav-panel-item">`,
      `<a href="${route}" class="nav-panel-item bg-paper" aria-current="page">`,
    )
    .replace(
      `<a href="${route}" class="rounded-xl px-3 py-3 font-medium no-underline hover:bg-paper">`,
      `<a href="${route}" class="bg-paper rounded-xl px-3 py-3 font-medium no-underline" aria-current="page">`,
    );
}

function inject(page, name, markup) {
  const marker = new RegExp(`<!-- partial:${name} -->[\\s\\S]*?<!-- /partial:${name} -->`);
  if (!marker.test(page)) throw new Error(`missing ${name} marker`);
  return page.replace(marker, () => `<!-- partial:${name} -->\n${markup}\n<!-- /partial:${name} -->`);
}

for (const [file, route] of Object.entries(pages)) {
  const path = join(root, file);
  let page = readFileSync(path, "utf8");
  page = inject(page, "header", markCurrent(header, route));
  page = inject(page, "footer", footer);
  writeFileSync(path, page);
}

console.log(`partials injected into ${Object.keys(pages).length} pages`);
