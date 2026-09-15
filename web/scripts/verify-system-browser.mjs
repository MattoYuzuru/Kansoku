#!/usr/bin/env node

import { spawn } from "node:child_process";
import { existsSync, mkdtempSync, rmSync, statSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const baseURL = process.env.KANSOKU_BROWSER_BASE_URL ?? "http://127.0.0.1:43100";
const candidates = [
  process.env.KANSOKU_CHROME,
  "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
  "/Applications/Chromium.app/Contents/MacOS/Chromium",
].filter(Boolean);

const chrome = candidates.find((candidate) => {
  try {
    return existsSync(candidate) && statSync(candidate).size > 0;
  } catch {
    return false;
  }
});

if (!chrome) {
  throw new Error("chrome_not_available");
}

const profile = mkdtempSync(join(tmpdir(), "kansoku-browser-"));
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
  const runtimeExceptions = [];
  const failedRequests = [];
  page.on("Runtime.exceptionThrown", (event) => {
    runtimeExceptions.push(event.exceptionDetails?.text ?? "runtime_exception");
  });
  page.on("Network.loadingFailed", (event) => {
    if (!event.canceled) failedRequests.push(event.errorText ?? "network_failure");
  });
  await page.call("Page.enable");
  await page.call("Runtime.enable");
  await page.call("Network.enable");
  await page.call("Network.setCacheDisabled", { cacheDisabled: true });
  await page.call("Emulation.setEmulatedMedia", {
    features: [{ name: "prefers-reduced-motion", value: "reduce" }],
  });

  const cases = [
    { name: "desktop", width: 1440, height: 900, scale: 1 },
    { name: "tablet", width: 1024, height: 768, scale: 1 },
    { name: "mobile_portrait", width: 390, height: 844, scale: 1, mobile: true },
    { name: "mobile_landscape", width: 844, height: 390, scale: 1, mobile: true },
    {
      name: "desktop_200_percent_reflow",
      width: 720,
      height: 900,
      scale: 1,
      zoomMode: "half_css_viewport",
    },
    {
      name: "desktop_page_scale_200_percent",
      width: 1440,
      height: 900,
      scale: 2,
      zoomMode: "cdp_page_scale",
    },
  ];
  const results = [];
  for (const testCase of cases) {
    await page.call("Emulation.setDeviceMetricsOverride", {
      width: testCase.width,
      height: testCase.height,
      deviceScaleFactor: 1,
      mobile: Boolean(testCase.mobile),
      screenOrientation: testCase.width > testCase.height
        ? { type: "landscapePrimary", angle: 90 }
        : { type: "portraitPrimary", angle: 0 },
    });
    await page.call("Page.navigate", { url: `${baseURL}/system` });
    await page.call("Emulation.setPageScaleFactor", { pageScaleFactor: testCase.scale });
    await waitFor(page, `
      document.readyState === "complete" &&
      document.body.textContent.includes("Durability and capacity") &&
      document.body.textContent.includes("Database budget used") &&
      document.body.textContent.includes("Projection repair pending") &&
      document.querySelector(".k-status[aria-label*='Runtime health is critical']")
    `, 15_000);
    if (testCase.scale > 1) {
      await waitFor(page, `visualViewport && visualViewport.scale >= 1.99`, 2_000);
    }
    const audit = await evaluate(page, browserAuditExpression(testCase.name));
    audit.zoom_mode = testCase.zoomMode ?? "none";
    assertAudit(audit);
    results.push(audit);
  }

  await page.call("Emulation.setDeviceMetricsOverride", {
    width: 1440, height: 900, deviceScaleFactor: 1, mobile: false,
  });
  await page.call("Emulation.setPageScaleFactor", { pageScaleFactor: 1 });
  await evaluate(page, `
    (() => {
      const button = document.querySelector(".k-iconbtn--theme");
      if (!(button instanceof HTMLButtonElement)) throw new Error("theme_button_missing");
      button.click();
      return true;
    })()
  `);
  await waitFor(page, `document.documentElement.getAttribute("data-theme") === "light"`, 2_000);
  const lightAudit = await evaluate(page, browserAuditExpression("desktop_light"));
  assertAudit(lightAudit);
  results.push(lightAudit);

  await evaluate(page, `document.activeElement?.blur(); true`);
  const focusOrder = [];
  for (let index = 0; index < 6; index += 1) {
    await page.call("Input.dispatchKeyEvent", {
      type: "keyDown", key: "Tab", code: "Tab", windowsVirtualKeyCode: 9,
    });
    await page.call("Input.dispatchKeyEvent", {
      type: "keyUp", key: "Tab", code: "Tab", windowsVirtualKeyCode: 9,
    });
    focusOrder.push(await evaluate(page, `
      (() => {
        const node = document.activeElement;
        return node?.getAttribute("aria-label") || node?.textContent?.trim() ||
          node?.getAttribute("title") || node?.tagName || "";
      })()
    `));
  }
  if (focusOrder.some((name) => !name)) {
    throw new Error("keyboard_focus_name_missing");
  }
  if (runtimeExceptions.length > 0) {
    throw new Error(`runtime_exceptions:${runtimeExceptions.length}`);
  }
  if (failedRequests.length > 0) {
    throw new Error(`network_failures:${failedRequests.length}`);
  }
  process.stdout.write(`${JSON.stringify({
    status: "pass",
    browser: version.product,
    base_url: baseURL,
    cases: results,
    keyboard_focus_names_present: focusOrder.length,
    runtime_exceptions: 0,
    network_failures: 0,
    profile_persisted: false,
  }, null, 2)}\n`);
} finally {
  pageSocket?.close();
  browserSocket?.close();
  child.kill("SIGTERM");
  await Promise.race([
    new Promise((resolve) => child.once("exit", resolve)),
    new Promise((resolve) => setTimeout(resolve, 2_000)),
  ]);
  if (child.exitCode === null) child.kill("SIGKILL");
  rmSync(profile, { recursive: true, force: true });
}

function devtoolsEndpoint(process) {
  return new Promise((resolve, reject) => {
    let buffer = "";
    const timeout = setTimeout(() => reject(new Error("chrome_start_timeout")), 10_000);
    process.once("exit", () => {
      clearTimeout(timeout);
      reject(new Error("chrome_exited_before_devtools"));
    });
    process.stderr.setEncoding("utf8");
    process.stderr.on("data", (chunk) => {
      buffer = (buffer + chunk).slice(-16_384);
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
    for (const listener of listeners.get(payload.method) ?? []) {
      listener(payload.params ?? {});
    }
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

async function evaluate(page, expression) {
  const response = await page.call("Runtime.evaluate", {
    expression,
    returnByValue: true,
    awaitPromise: true,
  });
  if (response.exceptionDetails) {
    throw new Error(response.exceptionDetails.text ?? "browser_evaluation_failed");
  }
  return response.result?.value;
}

async function waitFor(page, expression, timeoutMS) {
  const deadline = Date.now() + timeoutMS;
  while (Date.now() < deadline) {
    if (await evaluate(page, `Boolean(${expression})`)) return;
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  const diagnostic = await evaluate(page, `(() => ({
    ready_state: document.readyState,
    pathname: location.pathname,
    title: document.title,
    body_text_prefix: document.body.innerText.slice(0, 240),
    body_text_suffix: document.body.innerText.slice(-480),
    body_text_length: document.body.innerText.length,
    has_durability_heading: document.body.innerText.includes("Durability and capacity"),
    has_database_budget: document.body.innerText.includes("Database budget used"),
    has_loading: document.body.innerText.includes("Loading"),
    has_error: document.body.innerText.includes("Error"),
    status_labels: [...document.querySelectorAll(".k-status")]
      .map((node) => node.getAttribute("aria-label")).filter(Boolean),
    root_child_count: document.querySelector("#root")?.childElementCount ?? -1,
    module_script_count: document.querySelectorAll("script[type='module']").length
  }))()`);
  throw new Error(`browser_condition_timeout:${JSON.stringify(diagnostic)}`);
}

function browserAuditExpression(name) {
  return `(() => {
    const visible = (node) => {
      const style = getComputedStyle(node);
      const rect = node.getBoundingClientRect();
      return style.display !== "none" && style.visibility !== "hidden" &&
        rect.width > 0 && rect.height > 0;
    };
    const accessibleName = (node) =>
      node.getAttribute("aria-label") || node.getAttribute("title") ||
      node.textContent?.trim() || "";
    const controls = [...document.querySelectorAll(
      "a[href],button,input,select,textarea,[tabindex]:not([tabindex='-1'])"
    )].filter(visible);
    const unlabeled = controls.filter((node) => !accessibleName(node)).length;
    const undersized = controls.filter((node) => {
      const rect = node.getBoundingClientRect();
      return rect.width < 40 || rect.height < 40;
    }).length;
    const duplicateIDs = [...document.querySelectorAll("[id]")]
      .map((node) => node.id)
      .filter((id, index, all) => all.indexOf(id) !== index).length;
    const parseRGB = (value) => (value.match(/[\\d.]+/g) ?? []).slice(0, 3).map(Number);
    const luminance = (rgb) => {
      const values = rgb.map((channel) => {
        const scaled = channel / 255;
        return scaled <= 0.03928 ? scaled / 12.92 : ((scaled + 0.055) / 1.055) ** 2.4;
      });
      return 0.2126 * values[0] + 0.7152 * values[1] + 0.0722 * values[2];
    };
    const ratio = (foreground, background) => {
      const left = luminance(parseRGB(foreground));
      const right = luminance(parseRGB(background));
      return (Math.max(left, right) + 0.05) / (Math.min(left, right) + 0.05);
    };
    const statusContrast = [...document.querySelectorAll(".k-status")].filter(visible).map((node) => {
      const style = getComputedStyle(node);
      const panel = node.closest(".k-panel") ?? document.body;
      return ratio(style.color, getComputedStyle(panel).backgroundColor);
    });
    const body = document.body.textContent ?? "";
    const sidebar = document.querySelector(".k-sidebar");
    const transitionMS = sidebar
      ? Math.max(...getComputedStyle(sidebar).transitionDuration.split(",")
        .map((value) => parseFloat(value) * (value.includes("ms") ? 1 : 1000)))
      : Number.POSITIVE_INFINITY;
    return {
      name: ${JSON.stringify(name)},
      viewport: { width: innerWidth, height: innerHeight, scale: visualViewport?.scale ?? 1 },
      horizontal_overflow_px: Math.max(0, document.documentElement.scrollWidth -
        document.documentElement.clientWidth),
      unlabeled_controls: unlabeled,
      undersized_controls: undersized,
      duplicate_ids: duplicateIDs,
      h1_count: document.querySelectorAll("h1").length,
      panel_heading_count: document.querySelectorAll(".k-panel h2").length,
      main_present: Boolean(document.querySelector("main")),
      primary_navigation_named: Boolean(document.querySelector("aside[aria-label='Primary']")),
      capacity_fields_present: [
        "Database budget used", "Checkpoint budget used", "Docker filesystem free",
        "Emergency spool", "Backpressure rejections", "Durability unavailable",
        "WAL headroom", "Projection repair pending", "Completeness"
      ].every((label) => body.includes(label)),
      critical_state_named: [...document.querySelectorAll(".k-status")]
        .some((node) => (node.getAttribute("aria-label") ?? "").includes("Runtime health is critical")),
      not_observed_named: [...document.querySelectorAll(".k-status")]
        .some((node) => (node.getAttribute("aria-label") ?? "").startsWith("Not observed:")),
      minimum_status_contrast: statusContrast.length ? Math.min(...statusContrast) : 0,
      reduced_motion_transition_ms: transitionMS,
      title: document.title,
    };
  })()`;
}

function assertAudit(audit) {
  const failures = [];
  if (audit.horizontal_overflow_px > 1) failures.push("horizontal_overflow");
  if (audit.unlabeled_controls !== 0) failures.push("unlabeled_controls");
  if (audit.undersized_controls !== 0) failures.push("undersized_controls");
  if (audit.duplicate_ids !== 0) failures.push("duplicate_ids");
  if (audit.h1_count !== 1) failures.push("h1_count");
  if (audit.panel_heading_count < 3) failures.push("panel_headings");
  if (!audit.main_present) failures.push("main_landmark");
  if (!audit.primary_navigation_named) failures.push("primary_navigation");
  if (!audit.capacity_fields_present) failures.push("capacity_fields");
  if (!audit.critical_state_named) failures.push("critical_state_name");
  if (!audit.not_observed_named) failures.push("not_observed_name");
  if (audit.minimum_status_contrast < 4.5) failures.push("status_contrast");
  if (audit.reduced_motion_transition_ms > 0.1) failures.push("reduced_motion");
  if (!audit.title.includes("System")) failures.push("document_title");
  if (failures.length > 0) {
    throw new Error(
      `${audit.name}:${failures.join(",")}:${JSON.stringify(audit)}`,
    );
  }
}
