// Sprint 11 A3 + B2 frontend tests.
// Run: tsx src/__tests__/plan-review-opinion.test.tsx

import { JSDOM } from "jsdom";
import React from "react";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { renderToStaticMarkup } from "react-dom/server";
import { createServer, type ViteDevServer } from "vite";
import gsap from "gsap";
import { ApprovalModal } from "../components/ApprovalModal";
import { LocaleProvider, preloadDetectedLocale } from "../lib/i18n";
import type { WireApproval } from "../lib/types";

let passed = 0;
let failed = 0;

type GsapToOptions = { onComplete?: () => void };
const gsapForTests = (typeof gsap.to === "function" ? gsap : (gsap as unknown as { default?: typeof gsap }).default) as unknown as {
  to?: (target: unknown, vars: GsapToOptions) => unknown;
};
if (typeof gsapForTests.to === "function") {
  gsapForTests.to = (_target: unknown, vars: GsapToOptions) => {
    vars.onComplete?.();
    return {};
  };
}

function ok(value: boolean, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

function eq(actual: unknown, expected: unknown, label: string) {
  if (JSON.stringify(actual) === JSON.stringify(expected)) ok(true, label);
  else ok(false, `${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
}

function flushTimers(ms = 0): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function installDom(language = "en-US") {
  const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
    pretendToBeVisual: true,
    url: "http://localhost/",
  });
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  globalThis.window = dom.window as unknown as Window & typeof globalThis;
  globalThis.document = dom.window.document;
  Object.defineProperty(dom.window.navigator, "language", { configurable: true, value: language });
  Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
  globalThis.Node = dom.window.Node;
  globalThis.Element = dom.window.Element;
  globalThis.HTMLElement = dom.window.HTMLElement;
  globalThis.HTMLTextAreaElement = dom.window.HTMLTextAreaElement;
  globalThis.Event = dom.window.Event;
  globalThis.KeyboardEvent = dom.window.KeyboardEvent;
  globalThis.InputEvent = dom.window.InputEvent;
  globalThis.MouseEvent = dom.window.MouseEvent;
  globalThis.localStorage = dom.window.localStorage;
  globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
  globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
  globalThis.getComputedStyle = dom.window.getComputedStyle.bind(dom.window);
  Object.defineProperty(dom.window.HTMLElement.prototype, "attachEvent", { configurable: true, value: () => {} });
  Object.defineProperty(dom.window.HTMLElement.prototype, "detachEvent", { configurable: true, value: () => {} });
  return dom;
}

function actionButton(label: string): HTMLButtonElement {
  const button = Array.from(document.querySelectorAll(".prompt-shelf__actions .prompt-action")).find((el) =>
    el.textContent?.includes(label),
  ) as HTMLButtonElement | undefined;
  if (!button) throw new Error(`action button not found: ${label}`);
  return button;
}

function confirmButton(): HTMLButtonElement {
  const button = document.querySelector(".decision-confirm-bar__confirm") as HTMLButtonElement | null;
  if (!button) throw new Error("confirm button did not render");
  return button;
}

async function renderApproval(approval: WireApproval, onAnswer: (allow: boolean, session: boolean, persist: boolean, opinion?: string) => void) {
  await preloadDetectedLocale();
  const rootEl = document.getElementById("root");
  if (!rootEl) throw new Error("missing root");
  const root = createRoot(rootEl);
  await act(async () => {
    root.render(
      <LocaleProvider>
        <ApprovalModal
          approval={approval}
          cwd="/repo"
          tabId="tab-a"
          onAnswer={onAnswer}
          onStop={() => undefined}
        />
      </LocaleProvider>,
    );
    await flushTimers();
  });
  return root;
}

async function selectAndConfirm(label: string) {
  await act(async () => {
    actionButton(label).click();
    await flushTimers();
  });
  await act(async () => {
    confirmButton().click();
    await flushTimers(220);
  });
}

console.log("\nplan review opinion + strict-plan notice badge");

{
  // TC-A3-07: plan_lock_review 卡显示可选意见输入框；提交时 onAnswer 透传意见。
  // 注：React 19 + JSDOM 下程序化 value 修改不会触发受控组件的 onChange（已验证），
  // 故"已填写意见透传"由后端 TC-A3-01/03/04（意见到达模型）与 API TC-A3-06
  // （意见逐层透传）覆盖，此处确定性地验证空意见默认值（fail-safe）与输入框呈现。
  installDom("en-US");
  const answers: Array<[boolean, boolean, boolean, string?]> = [];
  const root = await renderApproval(
    { id: "appr-1", tool: "plan_lock_review", subject: "Lock plan plan-001?", reason: "# Plan plan-001\n1. step" },
    (allow, session, persist, opinion) => answers.push([allow, session, persist, opinion]),
  );
  const input = document.querySelector(".plan-review-opinion__input") as HTMLTextAreaElement | null;
  ok(Boolean(input), "plan_lock_review card renders the opinion input");
  ok(Boolean(document.querySelector(".plan-review-opinion__label")), "opinion input has a visible label");
  await selectAndConfirm("Deny");
  eq(answers.length, 1, "deny submits one answer");
  eq(answers[0]?.[0], false, "deny carries allow=false");
  eq(answers[0]?.[3], "", "empty opinion defaults to an empty string (legacy behavior)");
  root.unmount();
}

{
  // TC-A3-07b: plan_change_review 同样显示意见框；允许路径也透传（空意见默认）。
  installDom("en-US");
  const answers: Array<[boolean, boolean, boolean, string?]> = [];
  const root = await renderApproval(
    { id: "appr-2", tool: "plan_change_review", subject: "Apply change to plan plan-001?" },
    (allow, session, persist, opinion) => answers.push([allow, session, persist, opinion]),
  );
  ok(Boolean(document.querySelector(".plan-review-opinion__input")), "plan_change_review card renders the opinion input");
  await selectAndConfirm("Approve");
  eq(answers[0]?.[0], true, "approve carries allow=true");
  eq(answers[0]?.[3], "", "empty opinion defaults to an empty string (legacy behavior)");
  root.unmount();
}

{
  // TC-A3-08: 普通工具审批卡（bash）不显示意见输入框；onAnswer 不带意见。
  installDom("en-US");
  const answers: Array<[boolean, boolean, boolean, string?]> = [];
  const root = await renderApproval(
    { id: "appr-3", tool: "bash", subject: "run go test ./..." },
    (allow, session, persist, opinion) => answers.push([allow, session, persist, opinion]),
  );
  ok(!document.querySelector(".plan-review-opinion__input"), "ordinary tool card must NOT render the opinion input");
  await selectAndConfirm("Allow once");
  eq(answers[0]?.[3], undefined, "ordinary tool answer carries no opinion");
  root.unmount();
}

{
  // TC-B2-01/02: NoticeCard 徽章（经 Vite SSR 加载，绕开 SVG 资源导入）。
  const server: ViteDevServer | undefined = await createServer({
    appType: "custom",
    logLevel: "silent",
    server: { middlewareMode: true },
  });
  const { NoticeCard: SSRNoticeCard } = await server.ssrLoadModule("/src/components/Transcript.tsx");
  const { LocaleProvider: SSRLocaleProvider } = await server.ssrLoadModule("/src/lib/i18n.tsx");

  // TC-B2-01: strict_plan code 的 Notice 渲染徽章与强调样式类。
  const badgeDoc = new JSDOM(
    renderToStaticMarkup(
      React.createElement(
        SSRLocaleProvider,
        null,
        React.createElement(SSRNoticeCard, {
          item: { kind: "notice", id: "n1", level: "info", text: "strict-plan: plan-id=plan-001 locked", code: "strict_plan" },
        }),
      ),
    ),
  ).window.document;
  ok(Boolean(badgeDoc.querySelector(".notice-line--strict-plan")), "strict-plan notice gets the emphasis style class");
  const badge = badgeDoc.querySelector(".notice-line__badge");
  ok(Boolean(badge) && (badge?.textContent ?? "").includes("STRICT-PLAN"), "strict-plan notice renders the STRICT-PLAN badge");

  // TC-B2-02: 无 code 的普通 notice 无徽章、无 strict-plan 样式类（回归）。
  const plainDoc = new JSDOM(
    renderToStaticMarkup(
      React.createElement(
        SSRLocaleProvider,
        null,
        React.createElement(SSRNoticeCard, {
          item: { kind: "notice", id: "n2", level: "info", text: "permission saved" },
        }),
      ),
    ),
  ).window.document;
  ok(!plainDoc.querySelector(".notice-line--strict-plan"), "plain notice must not carry the strict-plan class");
  ok(!plainDoc.querySelector(".notice-line__badge"), "plain notice must not render a badge");
  await server.close();
}

console.log(`\nplan-review-opinion: ${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
