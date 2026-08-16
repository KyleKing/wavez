# Remote and mobile access for a locally-running coding agent

Researched 2026-08-12. Scope: architecture patterns for reaching a coding agent process running on a personal Mac from a phone, over the public internet, as a solo developer. Facts are dated where they were confirmed; anything not pinned to a primary source is flagged `[unverified]`.

The single most useful finding came from Anthropic's own docs: Claude Code already ships a feature (Remote Control) that solves this exact problem, and its architecture is worth treating as a reference design throughout this doc rather than a separate case study.

## 1. Tunneling and relay options

| Option | Setup effort (solo dev) | Cost | Latency | Reliability / always-on fit |
|---|---|---|---|---|
| Tailscale + Funnel | Low, roughly 1-2 hours | Free on the Personal plan (up to 6 users, unlimited devices), $0 confirmed via [tailscale.com/pricing](https://tailscale.com/pricing) | Low, WireGuard-based mesh, direct peer connection when NAT traversal succeeds, relayed through Tailscale's DERP servers otherwise | Good. Funnel is available on all plans per [tailscale.com/kb/1223/funnel](https://tailscale.com/kb/1223/funnel). Bandwidth is described only as subject to "non-configurable bandwidth limits" with no published number [unverified numeric limit] |
| ngrok | Low, under an hour to a public URL | Free tier exists but is not viable for always-on use: $5 one-time usage credit, then metered at roughly $0.02/hour per active endpoint plus data and request overage, working out to about $14.40/month just in endpoint-hours on continuous use per [ngrok.com/pricing](https://ngrok.com/pricing) | Not independently confirmed; ngrok markets itself for quick expose-a-port use, not persistent personal infrastructure | Workable but the only option here with a real ongoing bill for 24/7 use |
| Cloudflare Tunnel (cloudflared) | Low-medium, requires a Cloudflare account and a domain in Cloudflare's DNS | Free for the tunnel itself; a domain adds its own (small) annual cost. Could not confirm a dedicated pricing page for Cloudflare Tunnel specifically [unverified, the pricing subpage 404'd during this research] | Not independently confirmed here | Outbound-only connections from `cloudflared` to Cloudflare's edge, so no inbound firewall exposure, per [developers.cloudflare.com/cloudflare-one](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/) |
| Self-hosted WireGuard | Medium-high: you own key management, a public relay/VPS if you want NAT traversal, and renewal of your own infra | A cheap always-on VPS (~$5/month) if you're not on someone else's tailnet | Best case, direct WireGuard, but only if you've solved NAT traversal yourself | Requires you to be your own ops team: no managed reconnect, no device-approval UI, no magic-DNS |
| Headscale (self-hosted Tailscale control server) | Medium: the project explicitly scopes itself to "self-hosters and hobbyists," and its maintainers say they don't support running it behind a reverse proxy or in a container, which pushes setup effort up for a from-scratch deploy, per [github.com/juanfont/headscale](https://github.com/juanfont/headscale) | Free software, but you supply the always-on host | Same underlying WireGuard mesh as Tailscale | Removes the dependency on Tailscale's company continuing to exist, at the cost of owning the control-plane operations yourself |

**Recommendation:** Tailscale Funnel is the clear fit for a solo dev. It is free on the Personal plan, has no bandwidth metering that turns into a bill, requires no owned domain, and gives you a mesh network (the tailnet) as a side effect that's useful for other personal-infra needs beyond this one project. Cloudflare Tunnel is the credible second choice if you already run a domain through Cloudflare and want a public HTTPS endpoint rather than exposure scoped to your own devices. ngrok is fine for development/testing but its metered pricing makes it the wrong choice for something meant to run continuously. Self-hosting WireGuard or Headscale only pays off if you specifically want zero dependency on Tailscale Inc., which is a real but separate tradeoff from "minimal setup effort," which is what this project needs.

One clarification worth carrying into the architecture decision (see section 6): Anthropic's own Remote Control feature for Claude Code doesn't use any of the above. It opens **outbound-only HTTPS from the local machine to the Anthropic API**, registers, and polls/streams, with no inbound port, no tunnel, and no VPN at all. That's a materially different (and simpler) pattern than "expose a local port," worth considering directly rather than defaulting to a tunnel. See section 5 and 6.

## 2. Authentication approaches

For a single legitimate user reaching their own machine, the auth question splits into "what protects the network path" and "what protects the app on top of it."

**If you're inside a Tailscale tailnet (not Funnel):** Tailscale already gives every device a WireGuard-derived cryptographic identity and ACL-gated access, described in [tailscale.com/kb/1099/device-approval](https://tailscale.com/kb/1099/device-approval). New devices require explicit approval before they can send or receive traffic on the tailnet unless you've configured auto-approval. This is functionally equivalent to what mTLS buys you (per-device identity, not just a shared secret) without you managing certificates yourself. In this mode, app-level auth is defense-in-depth, not the primary control, so a lightweight bearer token is enough.

**If you enable Funnel (public internet, not just the tailnet):** the network is no longer the security boundary, since anyone with the URL can reach the port. App-level auth becomes load-bearing, not just defense-in-depth. A long-lived API key stored in the phone's Keychain, sent as a bearer token, is proportionate here: rotate it if the phone is lost, and don't log it. Full OAuth 2.0 Device Authorization Grant (RFC 8628) is real, well-specified infrastructure (device code, user code, polling token endpoint, per [datatracker.ietf.org/doc/html/rfc8628](https://datatracker.ietf.org/doc/html/rfc8628)), but it's built for a multi-user consumer product signing into someone else's authorization server. Standing it up yourself (device endpoint, token endpoint, polling loop, rate limiting) is meaningfully more code than a solo project with one user needs; treat it as overkill unless you're already running an OAuth server for another reason.

**Device-pairing / QR-code flow** is the better-fitting middle ground, and it's exactly what Claude Code's own Remote Control does: `claude remote-control` shows a session URL and, on demand, a QR code; scanning it from the Claude mobile app authenticates that device to that session, all mediated through Anthropic's own backend rather than any certificate you manage, per [code.claude.com/docs/en/remote-control](https://code.claude.com/docs/en/remote-control). For a self-hosted equivalent: generate a short-lived, single-use pairing code shown as a QR code on the Mac, have the phone exchange it once for a long-lived bearer token, and store that token in Keychain. This gets you most of the usability of OAuth device flow (no typing a token in by hand) without standing up an authorization server.

**mTLS** is the most secure option but the least proportionate to burden for one user: you'd be managing a private CA, issuing and rotating a client cert to your phone, and dealing with iOS's client-certificate UX (which is not seamless). Skip it unless Funnel-style public exposure combined with a high-value target makes you want certificate-level assurance specifically.

**Recommendation:** if reachable only over the tailnet, a shared bearer token is secure enough given Tailscale's own device-level auth underneath it. If exposed via Funnel or any other public tunnel, add a QR-pairing flow that mints a device-scoped long-lived token, stored in Keychain/Keystore, with a manual revoke/rotate path. Skip building your own OAuth device-flow server and skip mTLS for a single-user personal setup.

## 3. Mobile app delivery options

Full comparison and effort estimates were researched separately and are captured in [`mobile-delivery.md`](mobile-delivery.md) in this directory; the summary:

| Path | Basic app effort | Key constraint |
|---|---|---|
| PWA (Add to Home Screen) | 1-2 dev-days | No App Store friction at all; iOS Safari has supported Web Push for Home Screen PWAs since iOS 16.4 (March 2023), refined by Safari 18.4's Declarative Web Push. Whether iOS Web Push supports actionable approve/deny buttons directly on the notification is not confirmed and should be smoke-tested before committing to this path for the approve/deny flow specifically [unverified] |
| Native iOS (Swift/SwiftUI) | 3-5 dev-days | Apple Developer Program is $99/year (confirmed current 2026 pricing); free Xcode provisioning works but expires every 7 days and restricts push entitlements. TestFlight internal testing (paid membership) skips App Store review and ships builds within minutes, the right fallback if the PWA's notification-action richness turns out insufficient |
| Cross-platform (React Native / Flutter / Expo) | 4-7 dev-days (2-4 if already fluent) | Inherits the exact same Apple provisioning and push constraints as native, with framework overhead on top and no offsetting advantage unless Android is also a real target |

Background execution limits are a non-issue across all three paths for this use case: none of them can reliably poll in the background on iOS regardless of framework, so every viable design is push-triggered rather than background-polling-driven anyway.

**Recommendation:** build the PWA first. Verify the notification-action-button question early; if it's a hard blocker, fall back to native SwiftUI with TestFlight rather than a cross-platform framework, which buys nothing here.

## 4. Push notification architecture

| Option | Setup effort | Cost | Dependency chain |
|---|---|---|---|
| Direct APNs integration | Moderate: register push capability in the Developer Portal, generate a `.p8` token-based key (simpler than the older certificate-based flow), implement HTTP/2 calls to Apple's APNs endpoint from your backend | Requires the $99/year Apple Developer Program membership | None beyond Apple itself, this is the root of the tree every other option ultimately depends on |
| Pushover | Very low: POST to `api.pushover.net/1/messages.json` with a token, user key, and message | One-time per-platform purchase after a 30-day trial, not a subscription, per [pushover.net](https://pushover.net/) | Pushover's own app still delivers via APNs (iOS) / FCM (Android) under the hood, so it's a convenience wrapper around those, not an alternative to them |
| ntfy.sh (hosted) | Very low: POST to `ntfy.sh/<topic>` | Free without registration; paid tiers ($6-25/month) add reserved topics, higher daily message caps, and larger attachments | Same as Pushover: the ntfy iOS/Android app still ultimately rides platform push (APNs/FCM); this research could not pin the exact mechanism for iOS specifically [unverified, ntfy's iOS delivery architecture doc wasn't found] |
| Self-hosted ntfy | Low-medium: run the ntfy server yourself (a single Go binary or container) | Free software, cost of your own always-on host | Self-hosting the message broker does not remove the APNs dependency for iOS delivery. You still need Apple's push infrastructure in the loop somewhere for a phone to receive anything while the app isn't open, since iOS doesn't allow arbitrary apps to hold a persistent background connection |

**The point worth stating plainly:** every one of these, including a fully self-hosted setup, ultimately depends on Apple's APNs to get a notification onto a locked iPhone screen. "Self-hosted" only removes your dependency on someone else's *server* (Pushover's servers, the public ntfy.sh instance). It does not and cannot remove Apple's role as the final delivery hop on iOS. The only way to skip that dependency entirely is to keep the app open and foreground, which defeats the purpose of a notification.

**Recommendation:** for "agent needs input" / "agent finished," which is low-frequency and high-importance, ntfy.sh's free hosted tier is the fastest path (a single unauthenticated-by-default HTTP POST, though pick a non-guessable topic name since topics function as the access control on the free tier). Pushover is a close second and slightly more polished if you're willing to pay the one-time per-platform fee. Skip standing up your own ntfy instance unless you're already running always-on infra for another reason (see section 6), since it buys you nothing on the iOS delivery question specifically.

## 5. Session and state sync model

Three sync patterns fit a remote phone client talking to a long-running local agent:

- **WebSocket streaming**: best for near-real-time token/tool-call streaming and bidirectional input (approve/deny, follow-up prompts), at the cost of needing reconnect/backoff logic and doing nothing useful for a backgrounded iOS app, since iOS won't hold the socket open once the app is backgrounded.
- **SSE**: simpler than WebSocket for the server-to-client direction (agent output streaming to the phone), still one-directional, still killed the moment iOS backgrounds the app.
- **Polling REST**: the least elegant but the only one that degrades gracefully to "check status when the app happens to be open," and it's trivial to implement and debug.

Given that iOS reliably kills persistent connections in the background, the pattern that actually matters for this use case is **push-to-wake plus a lightweight status fetch**: a push notification tells the phone something changed, the app opens (or a notification-service extension fires) and does a single REST fetch or opens a short-lived WebSocket/SSE connection to catch up, then lets the connection drop again once idle. Don't design around a connection staying open while the phone is asleep in your pocket, since no coding-agent mobile companion researched here does that, because iOS won't allow it.

**How Claude Code's own Remote Control does it** (the most directly relevant existing example, confirmed from [code.claude.com/docs/en/remote-control](https://code.claude.com/docs/en/remote-control), Aug 2026): the local `claude` process makes outbound HTTPS requests only, and it never opens an inbound port. It registers with the Anthropic API and polls for work; when a phone or browser connects, the Anthropic backend routes messages between the client and the local session over what the docs call a "streaming connection." The full session transcript is stored on Anthropic's servers to keep devices in sync and to survive reconnects, while code execution and filesystem access stay on the local machine. If the Mac sleeps, Claude Code reconnects automatically when it wakes; if the network is down for more than roughly 10 minutes while the Mac stays awake, the session times out and has to be restarted manually. Push notifications are sent by Claude's own backend, not by the local process directly, when a task finishes or needs a decision. This sidesteps the entire "how do I run my own APNs integration" question in section 4, because Anthropic already operates that infrastructure and the local process never talks to APNs at all.

**Cursor's equivalent** (Cloud Agents, formerly Background Agents, per [cursor.com/docs/background-agent](https://cursor.com/docs/background-agent), Aug 2026): agents run in cloud VMs, not on the developer's own machine, reachable from a native iOS app and a PWA on Android at `cursor.com/agents`, plus Slack (`@cursor`) and GitHub/GitLab PR comments. Notably Cursor's mobile story assumes the agent itself runs in the cloud, sidestepping the "is my Mac reachable" question entirely. See section 6.

**Recommendation for a self-built version:** don't try to reinvent an always-open tunnel connection into the phone. Build push-to-wake (section 4) as the trigger, and have the woken app make one REST call to fetch current state. A WebSocket is worth adding later for the in-app "watch it stream live while I'm holding the phone open" case, but it should never be the only path to get status.

## 6. Handling a sleeping or offline Mac

**Why Wake-on-LAN doesn't help here:** WoL magic packets are Ethernet broadcast frames, so they don't cross routers or survive NAT translation by design, confirmed in [en.wikipedia.org/wiki/Wake-on-LAN](https://en.wikipedia.org/wiki/Wake-on-LAN). The workarounds (subnet-directed broadcast, a router configured to relay a magic packet from the internet, tunneling in via VPN first) are all fragile, router-dependent, and generally require the target machine to already be reachable on the network in some form. That begs the question, since a fully-asleep Mac with no network stack active can't be reached at all regardless of packet type.

**What Tailscale/Remote Control actually give you in practice:** Claude Code's docs describe the behavior directly ("if your laptop sleeps or your network drops, Claude Code reconnects automatically when your machine comes back online"), but a Mac that's fully asleep (not just idle) with the process suspended can't respond until something wakes it. macOS's "Wake for network access" setting (Power Nap on supported Macs) can periodically check network reachability and respond to some requests without a full wake, but this research could not confirm current, specific 2026 behavior for how reliably an Apple Silicon Mac stays reachable over Tailscale while fully asleep [unverified, no primary Apple or Tailscale doc found addressing this directly]. Practically: expect "the Mac needs to be at least awake, if not necessarily in active use" as the baseline requirement for anything that depends on the local machine actually running.

**The always-on relay/queue pattern:** for the case where you want to trigger something on the Mac even when it might be asleep, the standard shape is a small always-on intermediary that accepts the request the instant it arrives and holds it (or notifies you it can't be delivered yet) until the Mac is next reachable. Given this project is called "my-pi," a Raspberry Pi on the home network is a genuinely reasonable choice for that intermediary: negligible electricity cost, no recurring cloud bill, and it's already on the same LAN so it can attempt to wake the Mac locally (real WoL works fine on a LAN segment) the moment a request comes in over Tailscale from outside. The tradeoffs are real, though: the Pi itself needs to stay patched and online, and if your home internet or the Pi goes down, you've just moved the single point of failure rather than removed it. A cheap always-on cloud VM does the same job with different tradeoffs (a small recurring bill, no dependency on your home network staying up, but yet another thing to patch and secure).

**The alternative architecture, running the agent in the cloud instead:** this is what both Claude Code on the web and Cursor's Cloud Agents do, and it sidesteps the entire sleeping-Mac problem by design, since the agent process itself never depends on a personal machine being on. Reference costs confirmed here: GitHub Codespaces gives every personal GitHub account a free monthly compute/storage quota before metered billing kicks in, per [docs.github.com/en/codespaces](https://docs.github.com/en/codespaces/overview); Devin has a Free tier and a $20/month Pro tier for individuals, per [devin.ai/pricing](https://devin.ai/pricing) (Aug 2026). The real cost of this path isn't dollars, though. It's losing direct access to your own filesystem, locally-installed tools, and any local-only resources (e.g. locally-run model inference) unless the cloud environment is specifically configured to reach them.

**Practical recommendation:** for "check on my agent and approve things while out and about," which is squarely what this project needs, keeping the Mac awake and reachable (via Tailscale, with macOS's normal sleep-prevention settings, or literally leaving it plugged in and set to never sleep since it's a desk-bound personal machine) is enough, and it's dramatically simpler than standing up a relay or moving to the cloud. Reach for the Raspberry Pi relay pattern specifically if there's a recurring need to *wake* the Mac from full sleep on demand rather than just staying connected while it's already awake, since that's a materially different requirement than what's described in the brief. Move to a cloud-hosted agent only if the actual goal shifts from "reach my own machine" to "never depend on my own machine being on," which is a different project with a different cost profile (see the effort estimates below).

## Recommended minimal remote+mobile stack for a solo dev

**Basic version, covering viewing agent status, approving/denying actions, and getting push notifications:**

- Tailscale (mesh) with Funnel enabled only if reachability without both devices on the tailnet is required; otherwise stay on the tailnet and skip Funnel's public exposure entirely
- A QR-pairing flow that mints a device-scoped bearer token stored in the phone's Keychain
- A PWA (Add to Home Screen) hitting a small local HTTP API on the Mac for status and approve/deny actions
- ntfy.sh (free hosted tier, non-guessable topic name) for "agent needs input" / "agent finished" pushes, called from the local agent process when those events fire
- Mac left awake/reachable rather than building any sleep/wake infrastructure

Estimated effort: **4-6 developer-days**, roughly 1-2 days for the PWA itself, 1 day for the local HTTP API and its Tailscale exposure, 1 day for the pairing/auth flow, 0.5-1 day for the ntfy integration, and slack for testing across a real network drop and phone lock-screen behavior.

**Full version, covering a rich native UI, voice input, an offline-capable relay, and so on:**

- Native SwiftUI app (with TestFlight internal distribution, Apple Developer Program) for genuinely native notification actions, better background-refresh behavior, haptics, and voice-input capture
- Direct APNs integration or Pushover instead of ntfy, if richer notification categories/actions are wanted
- A Raspberry Pi (or small always-on cloud VM) relay/queue if on-demand wake-from-sleep becomes an actual requirement rather than "stay reachable while already on"
- WebSocket live-streaming for the in-app "watch the agent work in real time" experience, layered on top of the push-to-wake baseline rather than replacing it
- Voice input (on-device dictation via SFSpeechRecognizer, or piping audio to a transcription API) wired into the prompt-submission flow

Estimated effort: **15-25 developer-days**, dominated by the native app build-out and its App Store/TestFlight distribution ceremony, plus real integration testing of the relay's failure modes (Pi offline, home internet down, Mac genuinely asleep vs. reachable).

## Sources

- [Tailscale Funnel documentation](https://tailscale.com/kb/1223/funnel)
- [Tailscale pricing](https://tailscale.com/pricing)
- [Tailscale device approval](https://tailscale.com/kb/1099/device-approval)
- [ngrok pricing](https://ngrok.com/pricing)
- [Cloudflare Tunnel / cloudflared overview](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/)
- [Headscale (self-hosted Tailscale control server)](https://github.com/juanfont/headscale)
- [RFC 8628: OAuth 2.0 Device Authorization Grant](https://datatracker.ietf.org/doc/html/rfc8628)
- [Apple: setting up a remote notification server (APNs)](https://developer.apple.com/documentation/usernotifications/setting-up-a-remote-notification-server)
- [Pushover](https://pushover.net/)
- [ntfy.sh](https://ntfy.sh/)
- [Expo push notifications overview](https://docs.expo.dev/push-notifications/overview/)
- [Claude Code: Remote Control](https://code.claude.com/docs/en/remote-control)
- [Claude Code on mobile](https://code.claude.com/docs/en/mobile)
- [Claude Code overview / platform comparison](https://code.claude.com/docs/en/overview)
- [Cursor: Cloud Agents / background agent docs](https://cursor.com/docs/background-agent)
- [GitHub Codespaces overview](https://docs.github.com/en/codespaces/overview)
- [Devin pricing](https://devin.ai/pricing)
- [Wake-on-LAN, Wikipedia](https://en.wikipedia.org/wiki/Wake-on-LAN)
- [Mobile delivery deep-dive (this directory)](mobile-delivery.md)
