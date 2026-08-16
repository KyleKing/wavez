# Browser and iOS Simulator Automation for a Go Coding Agent

Research date: 2026-08-12. Current month referenced throughout is August 2026. Library maintenance and version claims come from direct GitHub/docs fetches on this date unless flagged `[unverified]`.

## TL;DR

For a Go-based coding agent that needs to verify web UI changes, **chromedp is the right default**: it's mature (13.2k stars, last commit mid-July 2026), needs no external runtime beyond a Chrome/Chromium binary, and gives full CDP access to screenshots, console, and network. Skip Firefox support unless a specific requirement demands it — WebDriver BiDi is real but the Go tooling for it doesn't exist yet, and Playwright's Firefox path only helps if the whole stack moves to playwright-go, which trades away chromedp's simplicity for a Node.js driver dependency.

For iOS Simulator, `xcrun simctl` covers screenshot and app lifecycle with zero extra dependencies and no macOS permission prompts, but it cannot tap or type — for interaction you need XCUITest through Appium's `appium-xcuitest-driver` (actively released, backed by Apple's own framework via WebDriverAgent) or `facebook/idb` (still alive as of August 2026, not archived, though the Appium ecosystem itself has drifted away from it).

For giving the agent "sight," pull the accessibility tree (Playwright's ARIA snapshot, or the CDP `Accessibility` domain directly) as the default read path — it's near-free in tokens and exact where the DOM already carries semantic structure. Reserve actual screenshot + vision-model calls for the moments text can't settle the question: layout, color, spacing, does-it-look-right checks. A local text-only model can drive the whole agent loop and call out to a hosted vision model (Claude, GPT) just for those screenshot judgments — that's the same shape Anthropic's own computer-use reference loop uses, just with the perception step split off instead of baked into the acting model.

## 1. Go-native browser automation libraries

### chromedp

- Repo: [github.com/chromedp/chromedp](https://github.com/chromedp/chromedp) — 13.2k stars, 167 open issues, last updated **July 14, 2026** per search-indexed activity (confirm exact commit SHA before depending on this for a build pin) [unverified: exact commit date not re-confirmed via direct repo fetch, only search-result summary]
- CDP coverage: generated from the Chrome DevTools Protocol spec, so coverage tracks upstream CDP closely; this is the library's main selling point over go-rod (which hand-writes a higher-level API on top)
- Screenshot: full-page, viewport, and per-element screenshot via `chromedp.Screenshot` / `chromedp.FullScreenshot`, backed by the CDP `Page.captureScreenshot` command
- Console capture: via `chromedp.ListenTarget` subscribing to CDP `Runtime.consoleAPICalled` / `Runtime.exceptionThrown` events
- Network capture: via `chromedp.ListenTarget` on `Network.requestWillBeSent` / `Network.responseReceived`, or the higher-level `network.Enable()` action
- Ergonomics: functional-options / action-chain style (`chromedp.Run(ctx, chromedp.Navigate(url), chromedp.Click(sel), chromedp.Screenshot(sel, &buf))`) — idiomatic Go, but verbose for anything beyond linear scripts; steeper learning curve than go-rod's chained API
- Runtime dependency: needs a Chrome/Chromium binary on the host, or the `chromedp/headless-shell` Docker image; does not bundle a browser
- Gotchas: context cancellation and allocator lifecycle management are common sources of subtle bugs (leaked processes, contexts not properly nested) per the library's own docs

### go-rod

- Repo: [github.com/go-rod/rod](https://github.com/go-rod/rod) — 7.1k stars, 181 open issues, actively committed (1,300+ commits, CI green)
- Screenshot/console/network: all supported through CDP, plus a distinctive `HijackRequests` API for intercepting and rewriting network traffic mid-flight (chromedp requires more manual CDP wiring for the equivalent)
- Ergonomics: chained, auto-waiting API (`page.MustNavigate(url).MustWaitStable()`) — generally considered more approachable than chromedp for scripting, at the cost of more "magic" (implicit waits, panics-by-default `Must*` variants alongside error-returning ones)
- Runtime dependency: can auto-download and manage its own browser binary, which chromedp does not do — a meaningful ergonomic win for a self-contained agent tool that shouldn't assume a pre-installed Chrome
- Maintenance: active as of 2026, 100% test coverage claimed in CI

### playwright-go

- The original `mxschmitt/playwright-go` is effectively inactive; the community fork [github.com/playwright-community/playwright-go](https://github.com/playwright-community/playwright-go) is the maintained one, latest release **June 26, 2026**, tracking Playwright core v1.51.1 as of that release ([Snyk advisor](https://snyk.io/advisor/golang/github.com/mxschmitt/playwright-go), maintenance score 35/100 as of Feb 23, 2026 — middling, not abandoned)
- Mechanism: playwright-go does **not** reimplement Playwright — it shells out to the Node.js-based Playwright driver process and talks to it over a private protocol. This means the Go binary needs the Playwright driver downloaded (`playwright install`) and, transitively, whatever the driver needs to run — a real deployment complication for a tool meant to be a single Go binary
- Cross-browser: this is Playwright's actual differentiator — Chromium, Firefox, and WebKit all through one API, using browser builds Playwright itself patches and ships (see Firefox section below)
- Screenshot/console/network: full parity with the JS API surface since it's a thin RPC wrapper, not a reimplementation
- Verdict: only worth the driver-process complexity if genuine multi-browser (especially WebKit/Safari) coverage is a hard requirement

### Comparison

| Library | Maturity | CDP coverage | Ergonomics | Runtime footprint | Best for |
|---|---|---|---|---|---|
| chromedp | High, most stars, longest track record | Full, spec-generated | Verbose but idiomatic Go | Needs external Chrome binary | Chrome-only agent that wants direct CDP control and no external process |
| go-rod | High, actively maintained | Full via CDP | More ergonomic, auto-waiting | Can self-manage browser binary | Same use case, values ergonomics over chromedp's directness |
| playwright-go | Moderate (community fork), driver-dependent | Full (via Node driver) | Familiar Playwright API | Requires bundled/downloaded Node driver process | Only if Firefox/WebKit parity is required |

For a single Go binary with no Node.js dependency, chromedp or go-rod are the realistic choices; go-rod's self-managed browser download is the more agent-friendly default since it removes an install-time assumption.

## 2. Firefox automation

- **CDP in Firefox**: Firefox shipped partial/experimental CDP support for years to support Puppeteer, but Mozilla has been moving away from it. Mozilla's own developer blog frames this explicitly: ["Deprecating CDP Support in Firefox: Embracing the Future with WebDriver BiDi"](https://fxdx.dev/deprecating-cdp-support-in-firefox-embracing-the-future-with-webdriver-bidi/) — CDP in Firefox is being sunset in favor of BiDi, not actively invested in.
- **WebDriver BiDi**: the W3C's cross-browser successor protocol. As of August 2026 it's in active, incremental development — Mozilla's newsletter cites Milestone 20 completed June 28, 2026 and Milestone 19 completed March 29, 2026 ([fxdx.dev](https://fxdx.dev/firefox-webdriver-newsletter-151/)). Firefox reached production-ready BiDi support starting with Firefox 129, and [Chrome for Developers' own blog](https://developer.chrome.com/blog/firefox-support-in-puppeteer-with-webdriver-bidi) confirms Puppeteer 23+ uses BiDi for stable cross-browser (including Firefox) support. It's a real, standards-track protocol implemented natively by Chrome, Edge, and Firefox — not vaporware.
- **Go library support for BiDi**: no mature, widely-adopted Go client for WebDriver BiDi was found in this pass. chromedp and go-rod are both CDP-specific; neither has announced BiDi support. This is the practical blocker — the protocol is production-ready on the browser side, the Go ecosystem hasn't caught up.
- **geckodriver**: still the way to drive Firefox over classic WebDriver (Selenium-style), separate from BiDi. No evidence found of a direct Go wrapper purpose-built for geckodriver outside generic Selenium Go bindings, which are a heavier dependency than a CDP-native library.
- **Playwright's Firefox support**: confirmed via [Playwright's own browser docs](https://playwright.dev/docs/browsers) — "Playwright's Firefox version matches the recent Firefox Stable build. Playwright doesn't work with the branded version of Firefox since it relies on patches." Playwright maintains its own patched Firefox build and updates it every release; playwright-go inherits this because it's a thin wrapper over the same driver.
- **Verdict**: for a Go agent whose job is verifying web UI changes, Chrome-only via chromedp/go-rod is the pragmatic choice as of August 2026. The only path to real Firefox support without adopting playwright-go's Node-driver dependency is hand-rolling a BiDi client, which doesn't exist yet in Go and would be a meaningful build. Firefox-specific rendering bugs are rare enough in most agent-verification workflows (checking that a UI change rendered/behaved correctly) that Chrome-only coverage is a reasonable, explicit tradeoff — not a shortcut being taken silently.

## 3. iOS Simulator automation

### xcrun simctl (Apple, built-in)

- Screenshot: `xcrun simctl io <device> screenshot <path>` — no extra install, no macOS permission prompt (talks to the simulator process directly, not the screen)
- Video/IO recording: `xcrun simctl io <device> recordVideo <path>`
- App lifecycle: `simctl install`, `simctl launch`, `simctl terminate`, `simctl uninstall`
- **Interaction**: simctl has no tap/swipe/type primitives. It is screenshot-and-lifecycle only — confirmed by the absence of any interaction subcommand in Apple's simctl surface and corroborated by every third-party tool (idb, WebDriverAgent, Appium) existing specifically to fill that gap. [unverified: could not load Apple's official simctl doc page directly, 404 on the URL tried — this conclusion is drawn from cross-referencing the tools built to compensate for simctl's gap, not from Apple's primary documentation]

### XCTest / XCUITest (Apple, official)

- Fundamentally a test-runner workflow: requires a compiled XCTest target and `xcodebuild test` invocation, not something designed for ad hoc one-off actions from a CLI
- WebDriverAgent (below) exists specifically to expose XCUITest's capabilities as an always-on HTTP server instead of a per-run test bundle, which is the practical way anything drives XCUITest programmatically without owning a full test target

### facebook/idb

- **Status as of August 2026: not archived, actively maintained.** [github.com/facebook/idb](https://github.com/facebook/idb) — 5.3k stars, 499 forks, MIT license, repo last updated **August 7, 2026**, 172 open issues / 10 open PRs. This directly contradicts any assumption of abandonment — idb itself is alive.
- Important nuance: `appium-idb` (the Appium *wrapper* around idb) is deprecated — Appium's own docs state they're moving off idb because "the upstream fb-idb stack is not maintained well" [as reported by the appium-idb project]. That's Appium's integration losing confidence in idb's release cadence, not idb being archived. Treat this as a maintenance-risk signal worth tracking, not a hard stop.
- Architecture: a macOS "companion" process plus a Python client that can run remotely — capable of screenshot, tap, app install/launch, and using private frameworks for functionality Xcode doesn't expose officially
- Requires Xcode 14.0+ to build from source, Python 3.11+ for the client; installable via Homebrew and pip

### WebDriverAgent

- [github.com/appium/WebDriverAgent](https://github.com/appium/WebDriverAgent) — 1.8k stars, not archived, actively maintained under the Appium org (originally Facebook-authored, now Appium-owned)
- Mechanism: links `XCTest.framework` directly and runs as an HTTP WebDriver server in the simulator/device context — this is what actually gives Appium's iOS driver interaction (tap, gesture, element lookup) via Apple's own official UI-testing API, rather than private APIs
- Appium's XCUITest driver manages WebDriverAgent as a subprocess and proxies WebDriver commands to it

### Appium (appium-xcuitest-driver)

- [github.com/appium/appium-xcuitest-driver](https://github.com/appium/appium-xcuitest-driver) — latest version **11.17.7**, published within days of this research date per npm; since major version 10.0.0 it requires Appium 3
- Backed by Apple's own XCUITest via WebDriverAgent — this is the most officially-grounded interaction path of the options surveyed
- Can create/manage its own simulator instance for a test run, or attach to an already-running one
- Overhead: running the full Appium server for a "screenshot + tap" workflow is heavier than needed; Appium is built for test-suite orchestration, not lightweight ad hoc control. No evidence found of a documented lightweight mode that skips the server for one-off commands.

### Go libraries for driving the simulator directly

No mature, actively maintained Go-native wrapper around simctl, idb, or Appium was found. The realistic pattern is a thin Go `exec.Command` wrapper around `xcrun simctl` for screenshot/lifecycle, calling out to idb's CLI (or the WebDriverAgent HTTP server directly) for interaction — not a native Go client library.

### Practical recommendation

Use `xcrun simctl` directly (via `os/exec`) for screenshot and app lifecycle — zero dependencies, zero permission prompts, Apple-official. For interaction (tap/type), `facebook/idb` is the lower-overhead option since it's alive and doesn't require standing up a full Appium server; treat its maintenance drift within the Appium ecosystem as a reason to keep the interaction layer thin and swappable, not a reason to avoid it outright. If richer element-targeting (find-by-accessibility-id, wait-for-element) becomes a real requirement, that's the point to move to Appium's `appium-xcuitest-driver` for its direct grounding in Apple's own WebDriverAgent/XCUITest, accepting the server overhead.

## 4. How an LLM agent "sees" a screen

### Screenshot + vision model

- Pattern: capture a screenshot, base64-encode it (or reference by URL), pass as an image content block alongside a text instruction, let the model reason over pixels and return either a text judgment or coordinate-based actions
- Cost/latency: images consume meaningfully more input tokens than equivalent text, and resolution matters — a full desktop screenshot at native resolution can be expensive per call. Anthropic's computer-use tool documentation (see section 5) explicitly ships a `zoom` action specifically because full-resolution small text (file names, tab titles) isn't reliably legible at a screenshot's default downscaled resolution — evidence that naive full-frame screenshots have real fidelity limits for a vision model.

### Accessibility tree extraction as the cheaper alternative

- Playwright ships this natively: [ARIA snapshots](https://playwright.dev/docs/aria-snapshots) capture the accessibility tree as YAML — role, accessible name, and relevant ARIA state (`checked`, `disabled`, `expanded`) per element, via `page.ariaSnapshot()`
- This is a genuinely different, cheaper channel: pure text, orders of magnitude fewer tokens than an image, and semantically precise for anything the DOM already exposes correctly (buttons, form fields, headings, links) — but blind to pure visual state (does this look misaligned, is the color wrong, did an image fail to load visibly)
- The same data is available lower-level via CDP's `Accessibility.getFullAXTree`, reachable directly from chromedp/go-rod without needing Playwright at all
- Practical pattern: use the accessibility tree as the default read path for "is X present / clickable / in the expected state" checks, and fall back to a screenshot + vision call only for genuinely visual judgments

### Model landscape (August 2026)

- Claude vision: computer-use tool documentation lists current supported models as `claude-opus-5`, `claude-sonnet-5`, `claude-opus-4-8`, `claude-opus-4-7`, `claude-opus-4-6`, `claude-sonnet-4-6`, `claude-opus-4-5-20251101` (per [platform.claude.com computer-use-tool docs](https://platform.claude.com/docs/en/agents-and-tools/tool-use/computer-use-tool), fetched 2026-08-12) — vision/computer-use capability is current across the live Claude model lineup, not a legacy-only feature
- GPT and Gemini vision capability in their current 2026 model lines: [unverified] — not independently checked in this pass; treat as needing a direct check against OpenAI/Google docs before depending on a specific claim
- Local/open-weight vision models (Qwen-VL family, Llama vision variants, etc.) as of August 2026: [unverified] — not independently checked in this pass

### Local agent loop + hosted vision call, or fully local vision model?

The Anthropic computer-use docs are themselves informative here: the reference loop assumes a model with native vision and coordinate-grounded action support built in — the model *is* the perception-plus-action step, not a separate module the agent loop calls out to. That said, nothing about a self-built Go agent requires adopting that exact shape. A cheaper, more decoupled pattern — driving the overall agent loop with a local or cheaper text model, and invoking a frontier vision API only for "look at this screenshot and answer X" — is architecturally sound and matches how the accessibility-tree-first pattern above is meant to compose: text/DOM reasoning handles the bulk of verification, and a vision call is reserved for the minority of checks that are genuinely visual. Whether a fully local vision-capable model is "good enough" to replace that hosted call for a given accuracy bar is a claim this pass didn't verify — flag as an open question requiring a direct model-quality comparison before committing to local-only vision.

## 5. Anthropic's computer use and Claude in Chrome as architecture references

### Computer use tool (API)

Source: [platform.claude.com/docs/en/agents-and-tools/tool-use/computer-use-tool](https://platform.claude.com/docs/en/agents-and-tools/tool-use/computer-use-tool), fetched 2026-08-12. Status: beta, current header `computer-use-2025-11-24`.

- **Actions**: `screenshot`, `left_click` (and other click variants), `type`, `key`, `mouse_move`, `scroll`, `left_click_drag`, `hold_key`, `wait`, and — new in `computer_20251124` — `zoom`, which re-renders a specific screen region at full resolution on request (`region: [x1, y1, x2, y2]`), explicitly built because the model can't reliably read small text at a screenshot's default resolution
- **Coordinate system**: actions like `left_click` take absolute `[x, y]` pixel coordinates against a fixed `display_width_px` / `display_height_px` declared in the tool definition; out-of-bounds coordinates are a defined error case the calling application must handle
- **Agent loop shape**: the application, not Claude, owns the loop. Claude returns a `tool_use` block; the calling application executes the actual action against a real (sandboxed) environment, captures the result (screenshot bytes, command output), and returns it as a `tool_result` in the next turn. This repeats until Claude stops requesting tools. Claude never has a direct connection to the target environment — every action is mediated by the caller's own implementation.
- **Reference environment**: Anthropic's own [quickstart](https://github.com/anthropics/anthropic-quickstarts/tree/main/computer-use-demo) runs a virtual X11 display (Xvfb) with a lightweight window manager (Mutter) and panel (Tint2) inside a Docker container, with the whole thing exposed for viewing/interaction over mapped ports
- **Perception model**: pure screenshot-based, with the caller responsible for producing valid images on each `screenshot` action — there is no DOM or accessibility-tree channel in this tool; it's a general desktop-control primitive, not browser-specific
- **Cost/effort tuning**: docs recommend `high` thinking effort by default on Opus 4.7, `medium` as the best accuracy-to-cost point on Sonnet 4.6/Opus 4.6, explicitly warning that `max` effort adds token cost without improving UI-task accuracy — directly relevant if a self-built loop wants to tune reasoning effort against a similar screenshot-judgment task

### Claude for Chrome extension

Source: [claude.com/blog/claude-for-chrome](https://claude.com/blog/claude-for-chrome). Timeline: research preview announced **August 25, 2025**, expanded to Max plan **November 24, 2025**, reached Pro/Team/Enterprise **December 18, 2025**.

- The public blog post doesn't fully disclose the perception mechanism (screenshot vs. DOM vs. hybrid) — it references DOM only in the context of a prompt-injection attack vector (hidden form fields invisible to a human but present in the DOM), which at minimum confirms the extension has *some* DOM-level visibility, not purely a screenshot loop. The exact split between screenshot and DOM/accessibility-tree perception is [unverified] from public sources found in this pass.
- **Permission model** worth borrowing directly for a self-built version: per-site allow/revoke controls in settings, explicit user confirmation required before "high-risk" actions (publishing, purchasing, sharing personal data), a blocklist of high-risk site categories (financial services, adult content, pirated content), and a classifier layer specifically watching for prompt-injection patterns in what the model reads off a page
- Anthropic's own red-team numbers are a useful calibration point for anyone building something similar: 23.6% attack success rate before mitigations, 11.2% after — i.e., even Anthropic's mitigated version isn't near zero, which argues for keeping a human-confirmation gate on state-changing actions in any self-built equivalent, not just informational ones.

### What's genuinely reusable as inspiration

The caller-owns-the-loop shape from the computer-use API (Claude proposes an action via `tool_use`, your code executes it against the real environment and returns a `tool_result`, repeat) maps directly onto a Go agent driving chromedp/go-rod: define a small action vocabulary (screenshot, click, type, navigate, read-console), let the model request one per turn, execute it against the real browser/simulator, and feed the result back. The permission/confirmation layering from Claude for Chrome (site allowlists, human confirmation on state-changing actions) is worth adopting wholesale for any variant that can, e.g., submit forms or make purchases during verification — not just for user-facing safety, but because a coding agent given accidental write access to a real environment is exactly this failure mode.

## 6. Practical gotchas

### Headless vs. headed

- Headless is the right default for CDP-driven verification: chromedp/go-rod screenshots come from `Page.captureScreenshot`, which works identically whether or not a window is actually drawn on screen, so there's no functional reason to run headed for automated checks
- Headed is worth keeping as an option specifically for human-supervised debugging sessions — watching the agent interact live is a different use case than capturing a verification screenshot, and some rendering paths (certain GPU-accelerated compositing, specific font-rendering edge cases) have historically differed between headless and headed Chrome, though modern Chrome's "new headless" mode (the default since Chrome 112, using the same rendering path as headed) narrows this considerably. Confirm this hasn't changed in the current Chrome release before treating headless output as visually authoritative for pixel-perfect comparisons — flag as [unverified] for current-version specifics.

### macOS permission prompts

- **CDP-only browser automation (chromedp/go-rod) does not trigger macOS Screen Recording or Accessibility prompts.** These libraries talk to the browser process directly over the CDP WebSocket, not through OS-level screen capture or synthetic input APIs — this is a meaningful practical advantage over any approach based on system-level screenshotting or AppleScript/`osascript`-driven input.
- **Screen Recording permission** is required only when capturing pixels *outside* an application's own rendering (e.g., a `screencapture`-style grab of an arbitrary window or the whole screen) — not needed for chromedp/go-rod's browser-internal screenshot, and not needed for `xcrun simctl io screenshot` (which reads from the simulator process, not the physical screen).
- **Accessibility permission** is required for synthetic system-wide keyboard/mouse input — relevant only if driving the iOS Simulator's window via AppleScript/System Events or a tool like `cliclick`, since the Simulator app itself doesn't expose a CDP-equivalent internal input channel. idb and WebDriverAgent avoid this because they inject input through XCTest/private frameworks talking to the simulator process directly, not through OS-level synthetic events.
- `xcrun simctl` operations (screenshot, launch, install) do not trigger these prompts — confirmed by absence of any permission-prompt documentation across Apple's or third-party sources found in this pass, and consistent with simctl talking to `simctld`/CoreSimulator directly rather than the OS input/screen stack.

### Code signing and notarization

- A CLI tool distributed outside the Mac App Store to other Macs needs to be signed and notarized to avoid Gatekeeper blocking first-run — this applies regardless of what the tool does, purely a function of distributing an unsigned binary from an unidentified developer
- Tools that request Accessibility or Screen Recording permission specifically need to be a properly signed, identifiable binary for the permission dialog and System Settings entry to behave correctly and persist across relaunches — an unsigned or ad-hoc-signed binary can produce permission grants that silently stop working after a rebuild (a new binary hash looks like a "new app" to TCC, macOS's permission database)
- Hardened runtime entitlements matter here: if the tool ever needs Accessibility-driven input simulation, it should ship with the hardened runtime enabled and only the entitlements it actually uses, since notarization requires the hardened runtime and Apple's notary service does static/dynamic analysis against declared entitlements
- No specific 2026 Apple policy tightening around automation/accessibility tools was found or verified in this pass — flagged as an area to check against current Apple Developer documentation before shipping, since Apple has a track record of incrementally tightening synthetic-input and private-API restrictions across macOS releases. [unverified]

## Recommended vision/automation stack

For a Go-based coding agent on macOS, verifying both web UI and iOS Simulator app changes:

- **Browser automation**: go-rod as the primary driver — CDP-native, no external Node.js dependency, self-manages its own Chrome binary (removing an install-time assumption chromedp doesn't handle), full screenshot/console/network access. chromedp is an equally valid choice if the team prefers a lower-abstraction, closer-to-CDP API; either is a reasonable default and the choice mostly comes down to ergonomic preference, not a capability gap.
- **Firefox**: skip it. No mature Go BiDi client exists yet, and Playwright's Firefox path only pays off if the whole browser layer moves to playwright-go's Node-driver dependency. Revisit only if a concrete requirement (a bug that reproduces only in Firefox, a customer requirement) forces the question.
- **iOS Simulator screenshot/lifecycle**: shell out to `xcrun simctl` directly — zero dependencies, zero permission prompts, Apple-official.
- **iOS Simulator interaction**: `facebook/idb`'s CLI for tap/type/install, kept behind a thin, swappable interface given its ecosystem maintenance drift; escalate to Appium's `appium-xcuitest-driver` (backed by WebDriverAgent/XCUITest) only if idb's interaction model proves insufficient for real test scenarios.
- **Default perception channel**: accessibility tree, not screenshots — CDP's `Accessibility.getFullAXTree` (or Playwright-style ARIA snapshot semantics reimplemented on top of it) for "is this element present/correct/in the right state" checks, at near-zero token cost.
- **Vision channel**: reserved for genuinely visual judgments (layout, alignment, color, does-it-look-right). Route these specific calls to a hosted Claude model with vision (current lineup per the computer-use docs: Claude Opus 5, Claude Sonnet 5, and the Claude 4.x Opus/Sonnet line) rather than requiring the whole agent loop to run on a vision-capable model. This mirrors the accessibility-tree-first, screenshot-as-fallback split already recommended above, and avoids the cost/latency penalty of running every verification step through image tokens.
- **Agent loop shape**: borrow the caller-owns-the-loop pattern directly from Anthropic's computer-use tool — the model requests one action per turn (navigate, click, screenshot, read-console, tap, etc.), the Go agent executes it against the real chromedp/go-rod session or simctl/idb target, and returns the result. Layer in Claude-for-Chrome-style guardrails (explicit confirmation before any state-changing or destructive action, a narrow allowlist of what the agent can touch) given this is a coding agent that could otherwise be pointed at a real, non-sandboxed dev environment.
- **Packaging**: if this ships beyond a personal dev script, budget for code signing and notarization from the start, especially once Accessibility-permission-based Simulator interaction (AppleScript/System Events, if idb's own input path proves insufficient) enters the picture — retrofitting signing after Accessibility permission grants have already gone stale on unsigned builds is more painful than starting signed.

## Open items flagged [unverified] in this pass

- Exact current GPT and Gemini frontier model vision capability as of August 2026 (not independently checked)
- Local/open-weight vision model quality (Qwen-VL, Llama vision variants) as of August 2026 (not independently checked)
- Whether headless vs. headed Chrome still has meaningful rendering differences in the current Chrome release (search budget was exhausted before this could be directly confirmed against Chrome's current release notes)
- Precise perception mechanism (screenshot vs. DOM vs. hybrid, and the split between them) inside the Claude for Chrome extension — Anthropic's public blog post doesn't fully specify this
- Any 2026-specific Apple policy tightening on synthetic input / accessibility APIs beyond what's already documented in existing entitlement/notarization requirements
