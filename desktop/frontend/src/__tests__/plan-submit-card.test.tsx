// Run: tsx src/__tests__/plan-submit-card.test.tsx
//
// TC-07: ToolCard renders plan_submit through a dedicated structured card —
// plan_id subject, numbered steps, the acceptance script with real newlines
// (no pretty JSON, no literal \n escapes), write scope and change manifest —
// instead of the generic pretty-JSON fallback.
// TC-08: .approval-reason and .notice-line__body preserve newlines (pre-wrap)
// so review cards and multi-line plan notices stay readable in the desktop UI.

import { JSDOM } from "jsdom";
import React from "react";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { readFileSync } from "node:fs";
import gsap from "gsap";
import { ToolCard } from "../components/ToolCard";
import { LocaleProvider } from "../lib/i18n";
import { subjectOf } from "../lib/tools";
import type { Item } from "../lib/useController";

type ToolItem = Extract<Item, { kind: "tool" }>;

type GsapToOptions = { onComplete?: () => void };
const gsapForTests = gsap as unknown as {
  to: (target: unknown, vars: GsapToOptions) => unknown;
  fromTo: (target: unknown, from: unknown, vars: GsapToOptions) => unknown;
  set: (target: unknown, vars: unknown) => unknown;
  killTweensOf: (target: unknown) => void;
};
gsapForTests.to = (_t: unknown, vars: GsapToOptions) => {
  vars.onComplete?.();
  return {};
};
gsapForTests.fromTo = (_t: unknown, _f: unknown, vars: GsapToOptions) => {
  vars.onComplete?.();
  return {};
};
gsapForTests.set = () => ({});
gsapForTests.killTweensOf = () => {};

let passed = 0;
let failed = 0;

function ok(value: unknown, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

function flushTimers(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 0));
}

function installDom() {
  const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
    pretendToBeVisual: true,
    url: "http://localhost/",
  });
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  globalThis.window = dom.window as unknown as Window & typeof globalThis;
  globalThis.document = dom.window.document;
  Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
  globalThis.Node = dom.window.Node;
  globalThis.Element = dom.window.Element;
  globalThis.HTMLElement = dom.window.HTMLElement;
  globalThis.Event = dom.window.Event;
  globalThis.MouseEvent = dom.window.MouseEvent;
  globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
  globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
  dom.window.matchMedia = () => ({
    matches: true,
    media: "(prefers-reduced-motion: reduce)",
    onchange: null,
    addListener: () => undefined,
    removeListener: () => undefined,
    addEventListener: () => undefined,
    removeEventListener: () => undefined,
    dispatchEvent: () => false,
  });
  return dom;
}

const planArgs = JSON.stringify({
  plan_id: "plan-001",
  steps: ["step one: create src/main.go", "step two: implement the adder"],
  validate_script: "#!/usr/bin/env python3\nimport sys\n\nassert 'ok'\n",
  write_scope: [{ path: "src/", reason: "main code" }, { path: "README.md", reason: "documentation" }],
  change_manifest: [{ path: "src/main.go", action: "add" }, { path: "README.md", action: "modify" }],
});

console.log("\nplan_submit dedicated card + pre-wrap");

// TC-07a: subjectOf collapses plan_submit to the plan id.
{
  const subject = subjectOf("plan_submit", planArgs);
  ok(subject === "plan-001", "plan_submit subject is the plan id");
  ok(!subject.includes("{") && !subject.includes("plan_id"), "subject is not JSON");
}

// TC-07b: expanded card renders structured plan content, no pretty JSON.
{
  const dom = installDom();
  const rootEl = document.getElementById("root");
  if (!rootEl) throw new Error("missing root");
  const root = createRoot(rootEl);
  const item: ToolItem = {
    kind: "tool",
    id: "t-plan-submit",
    name: "plan_submit",
    args: planArgs,
    readOnly: false,
    status: "done",
  };
  await act(async () => {
    root.render(React.createElement(LocaleProvider, null, React.createElement(ToolCard, { item })));
    await flushTimers();
  });
  const head = document.querySelector(".tool__head") as HTMLButtonElement | null;
  ok(!!head, "card head renders");
  ok(head?.textContent?.includes("plan-001"), "collapsed row shows the plan id");
  await act(async () => {
    head?.click();
    await flushTimers();
  });
  const text = document.body.textContent ?? "";
  ok(text.includes("step one: create src/main.go"), "card shows step one");
  ok(text.includes("step two: implement the adder"), "card shows step two");
  ok(text.includes("assert 'ok'"), "card shows the script body");
  ok(text.includes("main code") && text.includes("documentation"), "card shows scope reasons");
  ok(text.includes("[add]") && text.includes("[modify]"), "card shows manifest actions");
  ok(!text.includes('"validate_script":'), "card does not render the pretty JSON payload");
  ok(!text.includes("\\n"), "card keeps real newlines, no literal backslash-n");
  const pre = document.querySelector(".code-block pre, .code-block code");
  ok(!!pre, "script renders inside a code block");
}

// TC-08: review reason + notice bodies preserve newlines via pre-wrap.
{
  const css = readFileSync(new URL("../styles.css", import.meta.url), "utf8");
  ok(/\.approval-reason\s*\{[^}]*white-space:\s*pre-wrap/.test(css), ".approval-reason keeps newlines (pre-wrap)");
  ok(/\.notice-line__body\s*\{[^}]*white-space:\s*pre-wrap/.test(css), ".notice-line__body keeps newlines (pre-wrap)");
}

const total = passed + failed;
process.stdout.write(`\nResults: ${passed}/${total} passed, ${failed}/${total} failed\n`);
if (failed > 0) process.exitCode = 1;
