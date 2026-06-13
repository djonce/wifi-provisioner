const header = document.querySelector("[data-header]");
const navLinks = Array.from(document.querySelectorAll(".nav-links a"));
const tabButtons = Array.from(document.querySelectorAll("[data-tab]"));
const commandPanels = Array.from(document.querySelectorAll("[data-panel]"));
const copyButton = document.querySelector("[data-copy]");
const progress = document.querySelector("[data-progress]");
const parallaxLayer = document.querySelector("[data-parallax]");
const revealItems = Array.from(document.querySelectorAll("[data-reveal]"));

let lastScrollY = window.scrollY;
let ticking = false;

function renderIcons() {
  if (window.lucide) {
    window.lucide.createIcons();
  }
}

function setHeaderState() {
  if (!header) return;
  const currentY = window.scrollY;
  header.classList.toggle("is-scrolled", currentY > 24);
  header.classList.toggle("is-hidden", currentY > 180 && currentY > lastScrollY + 6);
  lastScrollY = currentY;
}

function updateProgress() {
  if (!progress) return;
  const maxScroll = document.documentElement.scrollHeight - window.innerHeight;
  const ratio = maxScroll > 0 ? window.scrollY / maxScroll : 0;
  progress.style.setProperty("--progress", String(Math.min(1, Math.max(0, ratio))));
}

function updateParallax() {
  if (!parallaxLayer) return;
  parallaxLayer.style.setProperty("--hero-shift", String(Math.min(window.scrollY, 900)));
}

function onScroll() {
  if (ticking) return;
  ticking = true;
  window.requestAnimationFrame(() => {
    setHeaderState();
    updateProgress();
    updateParallax();
    ticking = false;
  });
}

function setActiveTab(name) {
  tabButtons.forEach((button) => {
    const active = button.dataset.tab === name;
    button.classList.toggle("active", active);
    button.setAttribute("aria-selected", String(active));
  });

  commandPanels.forEach((panel) => {
    panel.classList.toggle("active", panel.dataset.panel === name);
  });
}

async function copyCurrentCommand() {
  const activePanel = document.querySelector(".command-block.active code");
  if (!activePanel || !copyButton) return;

  const text = activePanel.innerText.trim();
  try {
    await navigator.clipboard.writeText(text);
  } catch {
    const helper = document.createElement("textarea");
    helper.value = text;
    helper.setAttribute("readonly", "");
    helper.style.position = "fixed";
    helper.style.top = "-1000px";
    document.body.appendChild(helper);
    helper.select();
    document.execCommand("copy");
    helper.remove();
  }

  const icon = copyButton.querySelector("[data-lucide]");
  copyButton.setAttribute("aria-label", "已复制");
  if (icon) icon.setAttribute("data-lucide", "check");
  renderIcons();

  window.setTimeout(() => {
    const currentIcon = copyButton.querySelector("[data-lucide]");
    copyButton.setAttribute("aria-label", "复制当前命令");
    if (currentIcon) currentIcon.setAttribute("data-lucide", "copy");
    renderIcons();
  }, 1400);
}

function setupNavObserver() {
  const targets = navLinks
    .map((link) => document.querySelector(link.getAttribute("href")))
    .filter(Boolean);

  if (!targets.length || !("IntersectionObserver" in window)) return;

  const observer = new IntersectionObserver(
    (entries) => {
      const visible = entries
        .filter((entry) => entry.isIntersecting)
        .sort((a, b) => b.intersectionRatio - a.intersectionRatio)[0];

      if (!visible) return;
      navLinks.forEach((link) => {
        link.classList.toggle("is-active", link.getAttribute("href") === `#${visible.target.id}`);
      });
    },
    {
      rootMargin: "-35% 0px -55% 0px",
      threshold: [0.08, 0.2, 0.4],
    },
  );

  targets.forEach((target) => observer.observe(target));
}

function setupRevealObserver() {
  if (!revealItems.length) return;

  revealItems.forEach((item, index) => {
    item.style.setProperty("--reveal-delay", `${Math.min(index % 6, 5) * 55}ms`);
  });

  if (!("IntersectionObserver" in window)) {
    revealItems.forEach((item) => item.classList.add("is-visible"));
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
    {
      rootMargin: "0px 0px -12% 0px",
      threshold: 0.12,
    },
  );

  revealItems.forEach((item) => observer.observe(item));
}

tabButtons.forEach((button) => {
  button.addEventListener("click", () => setActiveTab(button.dataset.tab));
});

if (copyButton) {
  copyButton.addEventListener("click", copyCurrentCommand);
}

function init() {
  document.body.classList.add("reveal-ready");
  renderIcons();
  setHeaderState();
  updateProgress();
  updateParallax();
  setupNavObserver();
  setupRevealObserver();
}

window.addEventListener("scroll", onScroll, { passive: true });
if (document.readyState === "loading") {
  document.addEventListener("DOMContentLoaded", init, { once: true });
} else {
  init();
}
