// Mobile nav toggle.
(function () {
  const btn = document.getElementById("nav-toggle");
  const menu = document.getElementById("nav-menu");
  if (!btn || !menu) return;
  btn.addEventListener("click", () => {
    const open = menu.hidden;
    menu.hidden = !open;
    btn.setAttribute("aria-expanded", String(open));
  });
})();

// Hero chat demo: cycles through customer questions and streams an answer,
// highlighting the mentioned business — the product's premise in miniature.
(function () {
  const qEl = document.getElementById("q-text");
  const aBlock = document.getElementById("a-block");
  const aText = document.getElementById("a-text");
  const aCites = document.getElementById("a-cites");
  const data = document.getElementById("chat-scenarios");
  if (!qEl || !aBlock || !aText || !aCites || !data) return;

  const scenarios = JSON.parse(data.textContent);

  const citeClasses =
    "font-mono text-[0.72rem] text-moss border border-line rounded-full px-2.5 py-0.5 bg-paper";

  function render(s) {
    aText.innerHTML = s.a;
    aCites.innerHTML = "";
    for (const c of s.cites) {
      const chip = document.createElement("span");
      chip.className = citeClasses;
      chip.textContent = c;
      aCites.appendChild(chip);
    }
  }

  const reduceMotion = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
  if (reduceMotion) {
    qEl.textContent = scenarios[0].q;
    render(scenarios[0]);
    aBlock.classList.add("opacity-100");
    aBlock.querySelector(".mention").classList.add("lit");
    const caret = document.querySelector(".caret");
    if (caret) caret.remove();
    return;
  }

  let i = 0;
  const wait = (ms) => new Promise((r) => setTimeout(r, ms));

  async function type(text) {
    qEl.textContent = "";
    for (const ch of text) {
      qEl.textContent += ch;
      await wait(32);
    }
  }

  async function play() {
    const s = scenarios[i % scenarios.length];
    i += 1;

    aBlock.classList.remove("opacity-100");
    await wait(400);
    await type(s.q);
    await wait(500);

    render(s);
    aBlock.classList.add("opacity-100");
    await wait(700);
    aBlock.querySelector(".mention").classList.add("lit");
    await wait(4200);
    play();
  }

  play();
})();
