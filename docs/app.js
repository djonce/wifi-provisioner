const header = document.querySelector("[data-header]");
const navLinks = Array.from(document.querySelectorAll(".nav-links a"));
const tabButtons = Array.from(document.querySelectorAll("[data-tab]"));
const commandPanels = Array.from(document.querySelectorAll("[data-panel]"));
const copyButton = document.querySelector("[data-copy]");

function renderIcons() {
  if (window.lucide) {
    window.lucide.createIcons();
  }
}

function setHeaderState() {
  if (!header) return;
  header.classList.toggle("is-scrolled", window.scrollY > 24);
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

tabButtons.forEach((button) => {
  button.addEventListener("click", () => setActiveTab(button.dataset.tab));
});

if (copyButton) {
  copyButton.addEventListener("click", copyCurrentCommand);
}

window.addEventListener("scroll", setHeaderState, { passive: true });
window.addEventListener("load", () => {
  renderIcons();
  setHeaderState();
  setupNavObserver();
});
