// Shared navigation behavior.
(function () {
  const header = document.getElementById("site-header");
  const button = document.getElementById("nav-toggle");
  const menu = document.getElementById("nav-menu");
  const openIcon = button?.querySelector(".nav-open-icon");
  const closeIcon = button?.querySelector(".nav-close-icon");

  function setMenu(open) {
    if (!button || !menu) return;
    menu.hidden = !open;
    button.setAttribute("aria-expanded", String(open));
    button.setAttribute("aria-label", open ? "Close menu" : "Open menu");
    openIcon?.classList.toggle("hidden", open);
    closeIcon?.classList.toggle("hidden", !open);
  }

  button?.addEventListener("click", () => {
    setMenu(button.getAttribute("aria-expanded") !== "true");
  });

  menu?.querySelectorAll("a").forEach((link) => {
    link.addEventListener("click", () => setMenu(false));
  });

  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape") setMenu(false);
  });

  function updateHeader() {
    const scrolled = window.scrollY > 8;
    header?.classList.toggle("is-scrolled", scrolled);
  }

  updateHeader();
  window.addEventListener("scroll", updateHeader, { passive: true });
})();

// Subtle entrance motion, disabled automatically when reduced motion is set.
(function () {
  const elements = document.querySelectorAll(".reveal");
  if (!elements.length) return;

  if (
    window.matchMedia("(prefers-reduced-motion: reduce)").matches ||
    !("IntersectionObserver" in window)
  ) {
    elements.forEach((element) => element.classList.add("is-visible"));
    return;
  }

  const observer = new IntersectionObserver(
    (entries) => {
      entries.forEach((entry) => {
        if (!entry.isIntersecting) return;
        entry.target.classList.add("is-visible");
        observer.unobserve(entry.target);
      });
    },
    { rootMargin: "0px 0px -8% 0px", threshold: 0.08 },
  );

  elements.forEach((element) => observer.observe(element));
})();

// Product premise in miniature: cycle through real customer-style questions,
// then reveal the recommendation and its cited sources.
(function () {
  const question = document.getElementById("q-text");
  const answerBlock = document.getElementById("a-block");
  const answer = document.getElementById("a-text");
  const citations = document.getElementById("a-cites");
  const data = document.getElementById("chat-scenarios");
  if (!question || !answerBlock || !answer || !citations || !data) return;

  const scenarios = JSON.parse(data.textContent);
  const citationClasses =
    "font-mono text-[0.62rem] text-moss border border-line rounded-full px-2.5 py-1 bg-paper";

  function render(scenario) {
    answer.innerHTML = scenario.a;
    citations.replaceChildren();
    scenario.cites.forEach((citation) => {
      const chip = document.createElement("span");
      chip.className = citationClasses;
      chip.textContent = citation;
      citations.appendChild(chip);
    });
  }

  const reduceMotion = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
  if (reduceMotion) {
    question.textContent = scenarios[0].q;
    render(scenarios[0]);
    answerBlock.classList.add("opacity-100");
    answerBlock.querySelector(".mention")?.classList.add("lit");
    document.querySelector(".caret")?.remove();
    return;
  }

  let index = 0;
  const wait = (milliseconds) =>
    new Promise((resolve) => window.setTimeout(resolve, milliseconds));

  async function type(text) {
    question.textContent = "";
    for (const character of text) {
      question.textContent += character;
      await wait(25);
    }
  }

  async function play() {
    const scenario = scenarios[index % scenarios.length];
    index += 1;
    answerBlock.classList.remove("opacity-100");
    await wait(350);
    await type(scenario.q);
    await wait(450);
    render(scenario);
    answerBlock.classList.add("opacity-100");
    await wait(550);
    answerBlock.querySelector(".mention")?.classList.add("lit");
    await wait(3800);
    play();
  }

  play();
})();
