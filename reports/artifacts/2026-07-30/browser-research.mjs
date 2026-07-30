#!/usr/bin/env node

import { spawn } from "node:child_process";
import {
  existsSync,
  mkdirSync,
  mkdtempSync,
  rmSync,
  statSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const baseURL = process.env.KANSOKU_BROWSER_BASE_URL ?? "http://127.0.0.1:43100";
const outputDir = dirname(fileURLToPath(import.meta.url));
const chrome = [
  process.env.KANSOKU_CHROME,
  "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
  "/Applications/Chromium.app/Contents/MacOS/Chromium",
].filter(Boolean).find((candidate) => {
  try {
    return existsSync(candidate) && statSync(candidate).size > 0;
  } catch {
    return false;
  }
});

if (!chrome) throw new Error("chrome_not_available");
mkdirSync(outputDir, { recursive: true });

const profile = mkdtempSync(join(tmpdir(), "kansoku-research-browser-"));
const child = spawn(chrome, [
  "--headless=new",
  "--remote-debugging-port=0",
  `--user-data-dir=${profile}`,
  "--no-first-run",
  "--no-default-browser-check",
  "--disable-background-networking",
  "--disable-component-update",
  "--disable-sync",
  "--metrics-recording-only",
  "--disable-features=Translate",
  "about:blank",
], { stdio: ["ignore", "ignore", "pipe"] });

let browserSocket;
let pageSocket;
try {
  const endpoint = await devtoolsEndpoint(child);
  browserSocket = await connect(endpoint);
  const browser = client(browserSocket);
  const version = await browser.call("Browser.getVersion");
  const port = new URL(endpoint).port;
  const targetResponse = await fetch(`http://127.0.0.1:${port}/json/new?about:blank`, {
    method: "PUT",
  });
  if (!targetResponse.ok) throw new Error("browser_target_create_failed");
  const target = await targetResponse.json();
  pageSocket = await connect(target.webSocketDebuggerUrl);
  const page = client(pageSocket);
  const exceptions = [];
  const failedRequests = [];
  const apiResponses = [];

  page.on("Runtime.exceptionThrown", (event) => {
    const details = event.exceptionDetails ?? {};
    exceptions.push({
      text: details.text ?? "runtime_exception",
      description: String(details.exception?.description ?? "").slice(0, 1200),
    });
  });
  page.on("Network.loadingFailed", (event) => {
    if (!event.canceled) failedRequests.push(String(event.errorText ?? "network_failure"));
  });
  page.on("Network.responseReceived", (event) => {
    const response = event.response ?? {};
    const url = String(response.url ?? "");
    if (!url.includes("/api/v1/")) return;
    const parsed = new URL(url);
    const path = parsed.pathname.replace(
      /^\/api\/v1\/agents\/[^/]+$/,
      "/api/v1/agents/{installation}",
    );
    apiResponses.push({ path, status: response.status });
  });
  await page.call("Page.enable");
  await page.call("Runtime.enable");
  await page.call("Network.enable");
  await page.call("Network.setCacheDisabled", { cacheDisabled: true });
  await page.call("Emulation.setEmulatedMedia", {
    features: [{ name: "prefers-reduced-motion", value: "no-preference" }],
  });
  await setViewport(page, 1440, 900);

  const evidence = {
    observed_at: new Date().toISOString(),
    browser: version.product,
    base_url: baseURL,
    source_state: "live_embedded_appliance",
    skill_profile: {},
    agent_profiles: [],
    range_persistence: {},
    reliability_navigation: {},
    overflow: {},
    theme_tokens: {},
    glossary_target: {},
    glossary_reduced_motion: {},
    runtime_exceptions: exceptions,
    failed_requests: failedRequests,
    api_responses: apiResponses,
  };

  await navigate(page, "/components/skills", `document.querySelector('a[href^="/components/skills/"]')`);
  const skillLink = await evaluate(page, `
    (() => {
      const node = document.querySelector('a[href^="/components/skills/"]');
      return node ? { href: node.getAttribute("href"), label: node.textContent?.trim() } : null;
    })()
  `);
  const exceptionStart = exceptions.length;
  await evaluate(page, `
    (() => {
      const node = document.querySelector('a[href^="/components/skills/"]');
      if (!(node instanceof HTMLElement)) throw new Error("skill_link_missing");
      node.click();
      return true;
    })()
  `);
  await delay(1800);
  evidence.skill_profile = {
    selected: skillLink,
    ...(await pageState(page)),
    new_exceptions: exceptions.slice(exceptionStart),
  };
  await screenshot(page, "skill-profile-after-spa-click.png");

  await navigate(page, "/agents", `document.querySelectorAll('a[href^="/agents/"]').length > 0`);
  const agentLinks = await evaluate(page, `
    [...document.querySelectorAll('a[href^="/agents/"]')]
      .slice(0, 8)
      .map((node) => ({ href: node.getAttribute("href"), label: node.textContent?.trim() }))
  `);
  for (const link of agentLinks) {
    const start = exceptions.length;
    await navigate(page, link.href, `document.querySelector("h1") || document.body.innerText.includes("Unknown agent")`);
    await delay(500);
    evidence.agent_profiles.push({
      list_label: link.label,
      href: link.href,
      ...(await pageState(page)),
      new_exceptions: exceptions.slice(start),
    });
  }
  await screenshot(page, "agent-profile.png");

  await navigate(page, "/activity", `document.querySelector(".k-dd__trigger")`);
  await evaluate(page, `
    (() => {
      const trigger = document.querySelector(".k-dd__trigger");
      if (!(trigger instanceof HTMLElement)) throw new Error("range_trigger_missing");
      trigger.click();
      return true;
    })()
  `);
  await waitFor(page, `document.querySelectorAll('[role="option"]').length > 0`, 3000);
  await evaluate(page, `
    (() => {
      const option = [...document.querySelectorAll('[role="option"]')]
        .find((node) => node.textContent?.includes("Last 7 days"));
      if (!(option instanceof HTMLElement)) throw new Error("range_option_missing");
      option.click();
      return true;
    })()
  `);
  await waitFor(page, `document.querySelector(".k-dd__value")?.textContent?.includes("Last 7 days")`, 3000);
  const activityBeforeLeave = await evaluate(page, `document.querySelector(".k-dd__value")?.textContent?.trim()`);
  await evaluate(page, `document.querySelector('.k-nav__row[href="/models"]')?.click(); true`);
  await waitFor(page, `location.pathname === "/models" && document.querySelector("h1")?.textContent?.trim() === "Models"`, 5000);
  await evaluate(page, `
    (() => {
      const trigger = document.querySelector(".k-dd__trigger");
      if (!(trigger instanceof HTMLElement)) throw new Error("models_range_trigger_missing");
      trigger.click();
      return true;
    })()
  `);
  await waitFor(page, `document.querySelectorAll('[role="option"]').length > 0`, 3000);
  await evaluate(page, `
    (() => {
      const option = [...document.querySelectorAll('[role="option"]')]
        .find((node) => node.textContent?.includes("Last 12 months"));
      if (!(option instanceof HTMLElement)) throw new Error("models_range_option_missing");
      option.click();
      return true;
    })()
  `);
  await waitFor(page, `document.querySelector(".k-dd__value")?.textContent?.includes("Last 12 months")`, 3000);
  const modelsBeforeLeave = await evaluate(page, `document.querySelector(".k-dd__value")?.textContent?.trim()`);
  await evaluate(page, `document.querySelector('.k-nav__row[href="/activity"]')?.click(); true`);
  await waitFor(page, `location.pathname === "/activity" && document.querySelector("h1")?.textContent?.trim() === "Activity"`, 5000);
  await delay(300);
  const activityAfterReturn = await evaluate(page, `document.querySelector(".k-dd__value")?.textContent?.trim()`);
  await evaluate(page, `document.querySelector('.k-nav__row[href="/models"]')?.click(); true`);
  await waitFor(page, `location.pathname === "/models" && document.querySelector("h1")?.textContent?.trim() === "Models"`, 5000);
  const modelsAfterReturn = await evaluate(page, `document.querySelector(".k-dd__value")?.textContent?.trim()`);
  await page.call("Page.reload", { ignoreCache: true });
  await waitFor(page, `document.readyState === "complete" && location.pathname === "/models"`, 10000);
  await waitFor(page, `document.querySelector(".k-dd__value")`, 5000);
  const modelsAfterReload = await evaluate(page, `document.querySelector(".k-dd__value")?.textContent?.trim()`);
  evidence.range_persistence = {
    activity_before_leave: activityBeforeLeave,
    models_before_leave: modelsBeforeLeave,
    activity_after_return: activityAfterReturn,
    models_after_return: modelsAfterReturn,
    models_after_reload: modelsAfterReload,
  };

  await navigate(page, "/reliability", `document.querySelector(".k-reliability-tabs")`);
  await evaluate(page, `window.__kansokuResearchSentinel = "present"; true`);
  await evaluate(page, `
    (() => {
      const link = document.querySelector('.k-reliability-tabs a[href*="tab=incidents"]');
      if (!(link instanceof HTMLElement)) throw new Error("incident_tab_missing");
      link.click();
      return true;
    })()
  `);
  await waitFor(page, `location.search.includes("tab=incidents")`, 5000);
  await delay(500);
  evidence.reliability_navigation = {
    sentinel_after_click: await evaluate(page, `window.__kansokuResearchSentinel ?? null`),
    native_select_count: await evaluate(page, `document.querySelectorAll("select").length`),
    next_page_link_count: await evaluate(page, `
      [...document.querySelectorAll("a")].filter((node) => node.textContent?.trim() === "Next page").length
    `),
    ...(await pageState(page)),
  };
  await screenshot(page, "reliability-incidents.png");

  for (const [name, width, height] of [
    ["desktop", 1440, 900],
    ["tablet", 1024, 768],
    ["mobile", 390, 844],
    ["zoom_200", 720, 450],
  ]) {
    await setViewport(page, width, height);
    await navigate(page, "/reliability", `document.querySelector(".k-kpi")`);
    await delay(800);
    evidence.overflow[`reliability_${name}`] = await overflowAudit(page);
    await navigate(page, "/system", `document.querySelector(".k-kpi")`);
    await delay(800);
    evidence.overflow[`system_${name}`] = await overflowAudit(page);
    if (name !== "desktop") await screenshot(page, `system-${name}.png`);
  }

  await setViewport(page, 1440, 900);
  await navigate(page, "/agents", `document.querySelector(".k-nav__row.is-active")`);
  evidence.theme_tokens.before = await themeAudit(page);
  await evaluate(page, `
    (() => {
      localStorage.setItem("kansoku.appearance.v1", JSON.stringify({
        version: 1,
        theme: "dark",
        sidebarCollapsed: false,
        accentPurple: "#00ff00",
        accentGold: "#00ffff",
        accentPurpleLight: "#006b00",
        accentGoldLight: "#006b6b",
        preset: "observatory"
      }));
      return true;
    })()
  `);
  await page.call("Page.reload", { ignoreCache: true });
  await waitFor(page, `document.readyState === "complete" && document.querySelector(".k-nav__row.is-active")`, 10000);
  evidence.theme_tokens.after_custom_accents = await themeAudit(page);
  await evaluate(page, `
    (() => {
      localStorage.setItem("kansoku.appearance.v1", JSON.stringify({
        version: 1,
        theme: "light",
        sidebarCollapsed: false,
        accentPurple: "#8B7FD6",
        accentGold: "#D9B45B",
        accentPurpleLight: "#2E7D5B",
        accentGoldLight: "#8A6D1F",
        preset: "moss-amber"
      }));
      return true;
    })()
  `);
  await page.call("Page.reload", { ignoreCache: true });
  await waitFor(page, `document.readyState === "complete" && document.querySelector(".k-nav__row.is-active")`, 10000);
  evidence.theme_tokens.after_light_preset = await themeAudit(page);

  await navigate(page, "/glossary#invoked", `document.getElementById("invoked")`);
  evidence.glossary_target = await evaluate(page, `
    (() => {
      const node = document.getElementById("invoked");
      const style = node ? getComputedStyle(node) : null;
      return {
        found: Boolean(node),
        animation_name: style?.animationName ?? null,
        animation_duration: style?.animationDuration ?? null,
        transition_duration: style?.transitionDuration ?? null,
        border_color: style?.borderColor ?? null,
        focused: document.activeElement === node,
      };
    })()
  `);
  await page.call("Emulation.setEmulatedMedia", {
    features: [{ name: "prefers-reduced-motion", value: "reduce" }],
  });
  await navigate(page, "/glossary#loaded", `document.getElementById("loaded")`);
  await waitFor(page, `document.getElementById("loaded")?.classList.contains("is-target-pulsing")`, 3000);
  await delay(100);
  evidence.glossary_reduced_motion = await evaluate(page, `
    (() => {
      const node = document.getElementById("loaded");
      const style = node ? getComputedStyle(node) : null;
      return {
        found: Boolean(node),
        animation_name: style?.animationName ?? null,
        background_color: style?.backgroundColor ?? null,
        border_color: style?.borderColor ?? null,
        pulse_class: node?.classList.contains("is-target-pulsing") ?? false,
        focused: document.activeElement === node,
      };
    })()
  `);

  writeFileSync(
    join(outputDir, "browser-evidence.json"),
    `${JSON.stringify(evidence, null, 2)}\n`,
    "utf8",
  );
  process.stdout.write(`${JSON.stringify({
    status: "captured",
    browser: evidence.browser,
    skill_profile: evidence.skill_profile,
    range_persistence: evidence.range_persistence,
    reliability_navigation: evidence.reliability_navigation,
    overflow_counts: Object.fromEntries(
      Object.entries(evidence.overflow).map(([key, value]) => [key, value.length]),
    ),
    runtime_exception_count: exceptions.length,
    failed_request_count: failedRequests.length,
    agent_profile_statuses: apiResponses
      .filter((response) => response.path === "/api/v1/agents/{installation}")
      .map((response) => response.status),
  }, null, 2)}\n`);
} finally {
  pageSocket?.close();
  browserSocket?.close();
  child.kill("SIGTERM");
  await Promise.race([
    new Promise((resolve) => child.once("exit", resolve)),
    delay(2000),
  ]);
  if (child.exitCode === null) child.kill("SIGKILL");
  rmSync(profile, { recursive: true, force: true });
}

async function pageState(page) {
  return evaluate(page, `
    (() => ({
      pathname: location.pathname,
      search: location.search,
      title: document.title,
      h1: document.querySelector("h1")?.textContent?.trim() ?? null,
      body_text_length: document.body.innerText.length,
      root_child_count: document.querySelector("#root")?.childElementCount ?? -1,
      body_has_unknown_agent: document.body.innerText.includes("Unknown agent"),
      body_has_error: document.body.innerText.includes("Error"),
      body_has_loading: document.body.innerText.includes("Loading"),
    }))()
  `);
}

async function overflowAudit(page) {
  return evaluate(page, `
    [...document.querySelectorAll(".k-kpi")].flatMap((card) => {
      const value = card.querySelector(".k-kpi__value-row");
      if (!(value instanceof HTMLElement)) return [];
      const cardRect = card.getBoundingClientRect();
      const valueRect = value.getBoundingClientRect();
      const overflow = value.scrollWidth > value.clientWidth + 1 ||
        valueRect.right > cardRect.right + 1 ||
        valueRect.left < cardRect.left - 1;
      return overflow ? [{
        label: card.querySelector(".k-kpi__label")?.textContent?.trim() ?? "unknown",
        text: value.textContent?.trim() ?? "",
        scroll_width: value.scrollWidth,
        client_width: value.clientWidth,
        right_overflow_px: Math.max(0, valueRect.right - cardRect.right),
      }] : [];
    })
  `);
}

async function themeAudit(page) {
  return evaluate(page, `
    (() => {
      const root = getComputedStyle(document.documentElement);
      const active = document.querySelector(".k-nav__row.is-active");
      const activeStyle = active ? getComputedStyle(active) : null;
      const markerStyle = active ? getComputedStyle(active, "::before") : null;
      return {
        accent_purple: root.getPropertyValue("--accent-purple").trim(),
        accent_gold: root.getPropertyValue("--accent-gold").trim(),
        row_hover_token: root.getPropertyValue("--row-hover").trim(),
        row_selected_token: root.getPropertyValue("--row-selected").trim(),
        active_background: activeStyle?.backgroundColor ?? null,
        active_marker: markerStyle?.backgroundColor ?? null,
      };
    })()
  `);
}

async function navigate(page, path, readyExpression) {
  const url = path.startsWith("http") ? path : `${baseURL}${path}`;
  await page.call("Page.navigate", { url });
  await waitFor(page, `document.readyState === "complete"`, 10000);
  if (readyExpression) await waitFor(page, readyExpression, 15000);
}

async function setViewport(page, width, height) {
  await page.call("Emulation.setDeviceMetricsOverride", {
    width,
    height,
    deviceScaleFactor: 1,
    mobile: width < 600,
  });
}

async function screenshot(page, name) {
  const result = await page.call("Page.captureScreenshot", {
    format: "png",
    captureBeyondViewport: false,
  });
  writeFileSync(join(outputDir, name), Buffer.from(result.data, "base64"));
}

async function evaluate(page, expression) {
  const response = await page.call("Runtime.evaluate", {
    expression,
    returnByValue: true,
    awaitPromise: true,
  });
  if (response.exceptionDetails) {
    throw new Error(response.exceptionDetails.exception?.description ?? response.exceptionDetails.text);
  }
  return response.result?.value;
}

async function waitFor(page, expression, timeoutMS) {
  const deadline = Date.now() + timeoutMS;
  while (Date.now() < deadline) {
    if (await evaluate(page, `Boolean(${expression})`)) return;
    await delay(100);
  }
  throw new Error(`browser_wait_timeout:${expression.slice(0, 80)}`);
}

function delay(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function devtoolsEndpoint(process) {
  return new Promise((resolve, reject) => {
    let buffer = "";
    const timeout = setTimeout(() => reject(new Error("chrome_start_timeout")), 10000);
    process.once("exit", () => {
      clearTimeout(timeout);
      reject(new Error("chrome_exited_before_devtools"));
    });
    process.stderr.setEncoding("utf8");
    process.stderr.on("data", (chunk) => {
      buffer = (buffer + chunk).slice(-16384);
      const match = buffer.match(/DevTools listening on (ws:\/\/[^\s]+)/);
      if (match) {
        clearTimeout(timeout);
        resolve(match[1]);
      }
    });
  });
}

function connect(url) {
  return new Promise((resolve, reject) => {
    const socket = new WebSocket(url);
    socket.addEventListener("open", () => resolve(socket), { once: true });
    socket.addEventListener("error", () => reject(new Error("devtools_connect_failed")), {
      once: true,
    });
  });
}

function client(socket) {
  let nextID = 1;
  const pending = new Map();
  const listeners = new Map();
  socket.addEventListener("message", (message) => {
    const payload = JSON.parse(String(message.data));
    if (payload.id) {
      const waiter = pending.get(payload.id);
      if (!waiter) return;
      pending.delete(payload.id);
      if (payload.error) waiter.reject(new Error(payload.error.message ?? "cdp_error"));
      else waiter.resolve(payload.result ?? {});
      return;
    }
    for (const listener of listeners.get(payload.method) ?? []) listener(payload.params ?? {});
  });
  return {
    call(method, params = {}) {
      const id = nextID++;
      socket.send(JSON.stringify({ id, method, params }));
      return new Promise((resolve, reject) => pending.set(id, { resolve, reject }));
    },
    on(method, listener) {
      listeners.set(method, [...(listeners.get(method) ?? []), listener]);
    },
  };
}
