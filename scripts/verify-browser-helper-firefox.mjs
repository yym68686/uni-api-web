// Opt-in, real Firefox smoke test. Uses an isolated WebDriver profile and local
// HTTPS fixtures behind a loopback-only proxy; never contacts production sites.
// npm run package:browser-helper
// node scripts/verify-browser-helper-firefox.mjs /path/to/firefox /path/to/geckodriver
import assert from "node:assert/strict";
import { spawn, execFileSync } from "node:child_process";
import { createServer as httpServer } from "node:http";
import { createServer as httpsServer } from "node:https";
import { connect, createServer as netServer } from "node:net";
import {
  mkdtempSync,
  readFileSync,
  writeFileSync,
  createWriteStream,
} from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

const [firefoxBinary, geckodriverBinary] = process.argv.slice(2);
if (!firefoxBinary || !geckodriverBinary) {
  throw new Error(
    "Usage: node scripts/verify-browser-helper-firefox.mjs FIREFOX GECKODRIVER",
  );
}
const output = mkdtempSync(join(tmpdir(), "uni-api-firefox-smoke-"));
const consoleOrigin = "https://uni-api-console.fugue.pro";
const siteOrigin = "https://site.test";
const channel = "uni-api-browser-login-v1";
const addonID = "uni-api-browser-helper@yym68686.github.io";
const fixtureEmail = "firefox-test@example.test";
const results = [];
const pause = (ms) => new Promise((done) => setTimeout(done, ms));
async function until(check, label, timeout = 20000) {
  const deadline = Date.now() + timeout;
  let lastError;
  while (Date.now() < deadline) {
    try {
      const value = await check();
      if (value) return value;
    } catch (error) {
      lastError = error;
    }
    await pause(150);
  }
  throw new Error(
    `Timed out: ${label}${lastError ? ` (${lastError.message})` : ""}`,
  );
}
async function listen(server) {
  await new Promise((done, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", done);
  });
  return server.address().port;
}
function json(response, value, status = 200, headers = {}) {
  response.writeHead(status, {
    "content-type": "application/json",
    ...headers,
  });
  response.end(JSON.stringify(value));
}
function html(response, body) {
  response.writeHead(200, { "content-type": "text/html; charset=utf-8" });
  response.end(
    `<!doctype html><meta charset="utf-8"><title>Local Firefox fixture</title>${body}`,
  );
}
execFileSync(
  "openssl",
  [
    "req",
    "-x509",
    "-newkey",
    "rsa:2048",
    "-nodes",
    "-days",
    "1",
    "-subj",
    "/CN=site.test",
    "-addext",
    "subjectAltName=DNS:site.test,DNS:uni-api-console.fugue.pro",
    "-keyout",
    join(output, "key.pem"),
    "-out",
    join(output, "cert.pem"),
  ],
  { stdio: "ignore" },
);
let loginCount = 0;
const fixture = httpsServer(
  {
    key: readFileSync(join(output, "key.pem")),
    cert: readFileSync(join(output, "cert.pem")),
  },
  (request, response) => {
    const url = new URL(request.url, `https://${request.headers.host}`);
    const cookie = request.headers.cookie || "";
    if (url.hostname === new URL(consoleOrigin).hostname) {
      return html(
        response,
        `<h1>Isolated console fixture</h1><script>
      window.helperMessages = [];
      addEventListener('message', event => {
        if (event.source === window && event.origin === location.origin &&
            event.data?.channel === '${channel}') window.helperMessages.push(event.data);
      });
    </script>`,
      );
    }
    if (url.pathname === "/seed") {
      response.setHeader(
        "set-cookie",
        `session=${url.searchParams.get("session")}; Path=/; Secure; HttpOnly; SameSite=Lax`,
      );
      return html(response, "Seeded isolated fixture cookie");
    }
    if (url.pathname === "/fixture/cookies") return json(response, { cookie });
    if (url.pathname === "/fixture/newapi-login") {
      loginCount++;
      return json(
        response,
        { id: 42, username: "firefox-test", email: fixtureEmail },
        200,
        {
          "set-cookie": [
            "session=container-login-session; Path=/; Secure; HttpOnly; SameSite=Lax",
            "cf_clearance=must-not-be-exported; Path=/; Secure; HttpOnly; SameSite=Lax",
          ],
        },
      );
    }
    if (url.pathname === "/api/user/self") {
      const matched = /(?:^|;\s*)session=container-login-session(?:;|$)/.test(
        cookie,
      );
      return json(
        response,
        {
          success: matched,
          data: matched
            ? {
                id: 42,
                username: "firefox-test",
                email: fixtureEmail,
              }
            : null,
        },
        matched ? 200 : 401,
      );
    }
    if (url.pathname.endsWith("/api/v1/auth/me")) {
      return json(
        response,
        { data: { email: fixtureEmail } },
        request.headers.authorization === "Bearer fixture-sub2api-access"
          ? 200
          : 401,
      );
    }
    if (url.pathname.endsWith("/sign-in")) {
      return html(
        response,
        `<form><input name="username" id="username"><input name="password" id="password" type="password">
      <button type="submit">Sign in</button></form><script>
      document.querySelector('form').addEventListener('submit', async event => {
        event.preventDefault();
        const response = await fetch('/fixture/newapi-login', {method:'POST'});
        localStorage.setItem('user', JSON.stringify(await response.json()));
        document.querySelector('button').disabled = true;
      });</script>`,
      );
    }
    if (url.pathname.endsWith("/login")) {
      return html(
        response,
        `<form><input id="email"><input id="password" type="password">
      <input type="checkbox" id="login-agreement-consent">
      <button type="submit" ${url.pathname.startsWith("/cancel/") ? "disabled" : ""}>Sign in</button></form>
      <script>document.querySelector('form').addEventListener('submit', event => {
        event.preventDefault();
        localStorage.setItem('auth_token', 'fixture-sub2api-access');
        localStorage.setItem('refresh_token', 'fixture-sub2api-refresh');
        localStorage.setItem('auth_user', JSON.stringify({email:'${fixtureEmail}'}));
      });</script>`,
      );
    }
    return html(response, "Local site fixture");
  },
);
const sockets = new Set();
const proxy = httpServer((_request, response) => response.writeHead(403).end());
proxy.on("connect", (request, client, head) => {
  // No request from this browser can tunnel to the real console or other hosts.
  if (
    !["uni-api-console.fugue.pro:443", "site.test:443"].includes(request.url)
  ) {
    client.end("HTTP/1.1 403 Forbidden\r\n\r\n");
    return;
  }
  const upstream = connect(fixture.address().port, "127.0.0.1", () => {
    client.write("HTTP/1.1 200 Connection Established\r\n\r\n");
    if (head.length) upstream.write(head);
    client.pipe(upstream);
    upstream.pipe(client);
  });
  for (const socket of [client, upstream]) {
    sockets.add(socket);
    socket.on("close", () => sockets.delete(socket));
    socket.on("error", () => {
      client.destroy();
      upstream.destroy();
    });
  }
});
let gecko, sessionID, driverOrigin;
async function webdriver(method, endpoint, value) {
  const response = await fetch(driverOrigin + endpoint, {
    method,
    ...(value === undefined
      ? {}
      : {
          headers: { "content-type": "application/json" },
          body: JSON.stringify(value),
        }),
    signal: AbortSignal.timeout(60000),
  });
  const payload = await response.json();
  // Script return values may legitimately contain an application's `error`
  // field (notably a canceled login). WebDriver errors use a non-2xx status.
  if (!response.ok) {
    throw new Error(
      `${method} ${endpoint}: ${payload.value?.message || JSON.stringify(payload)}`,
    );
  }
  return payload.value;
}
const command = (method, endpoint, value) =>
  webdriver(method, `/session/${sessionID}${endpoint}`, value);
const script = (source, ...args) =>
  command("POST", "/execute/sync", { script: source, args });
async function privileged(source, ...args) {
  await command("POST", "/moz/context", { context: "chrome" });
  try {
    return await script(source, ...args);
  } finally {
    await command("POST", "/moz/context", { context: "content" });
  }
}
const handles = () => command("GET", "/window/handles");
const focus = (handle) => command("POST", "/window", { handle });
async function openTab(url, userContextId = 0) {
  const before = await handles();
  await privileged(
    `gBrowser.selectedTab = gBrowser.addTab(arguments[0], {
    userContextId: arguments[1], triggeringPrincipal: Services.scriptSecurityManager.getSystemPrincipal()
  });`,
    url,
    userContextId,
  );
  const handle = await until(
    async () => (await handles()).find((id) => !before.includes(id)),
    "new browser tab",
  );
  await focus(handle);
  await until(
    () =>
      script(
        "return document.readyState === 'complete' && location.href === arguments[0]",
        url,
      ),
    "tab navigation",
  );
  return handle;
}
async function helperReady() {
  const id = `ping-${crypto.randomUUID()}`;
  await script("window.postMessage(arguments[0], location.origin)", {
    channel,
    id,
    type: "ping",
  });
  const ready = await until(
    () =>
      script(
        "return window.helperMessages.find(m => m.id === arguments[0] && m.type === 'ready')",
        id,
      ),
    "extension content bridge",
  );
  assert.ok(ready.capabilities.includes("newapi"));
}
async function startLogin(kind, base = siteOrigin) {
  const id = crypto.randomUUID();
  const before = await handles();
  await script("window.postMessage(arguments[0], location.origin)", {
    channel,
    id,
    type: "login",
    input: {
      base,
      email: fixtureEmail,
      password: "fixture-only-password",
      agreed: true,
      kind,
    },
  });
  const loginTab = await until(async () => {
    const result = await script(
      "return window.helperMessages.find(m => m.id === arguments[0] && m.type === 'result')",
      id,
    );
    if (result?.error) throw new Error(JSON.stringify(result));
    return (await handles()).find((id) => !before.includes(id));
  }, "helper login tab");
  return { id, loginTab };
}
async function resultFor(consoleTab, id) {
  await focus(consoleTab);
  return until(
    () =>
      script(
        "return window.helperMessages.find(m => m.id === arguments[0] && m.type === 'result')",
        id,
      ),
    "login result",
  );
}
function passed(name, detail = {}) {
  results.push({ name, passed: true, ...detail });
  console.log(`PASS ${name}`);
}
try {
  await listen(fixture);
  const proxyPort = await listen(proxy);
  const reservation = netServer();
  const driverPort = await listen(reservation);
  await new Promise((done) => reservation.close(done));
  driverOrigin = `http://127.0.0.1:${driverPort}`;
  const log = createWriteStream(join(output, "geckodriver.log"));
  gecko = spawn(
    resolve(geckodriverBinary),
    ["--port", String(driverPort), "--allow-system-access"],
    { stdio: ["ignore", "pipe", "pipe"] },
  );
  gecko.stdout.pipe(log);
  gecko.stderr.pipe(log);
  await until(
    async () => (await webdriver("GET", "/status")).ready,
    "geckodriver startup",
  );
  const session = await webdriver("POST", "/session", {
    capabilities: {
      alwaysMatch: {
        browserName: "firefox",
        acceptInsecureCerts: true,
        proxy: {
          proxyType: "manual",
          httpProxy: `127.0.0.1:${proxyPort}`,
          sslProxy: `127.0.0.1:${proxyPort}`,
          noProxy: ["localhost", "127.0.0.1"],
        },
        "moz:firefoxOptions": {
          binary: resolve(firefoxBinary),
          args: ["-headless"],
          prefs: {
            "privacy.userContext.enabled": true,
            "privacy.userContext.ui.enabled": true,
            "network.proxy.allow_hijacking_localhost": true,
            "network.trr.mode": 5,
            "datareporting.policy.dataSubmissionEnabled": false,
            "toolkit.telemetry.enabled": false,
            "extensions.update.enabled": false,
          },
        },
      },
    },
  });
  sessionID = session.sessionId;
  console.log(
    `Real Firefox ${session.capabilities.browserVersion}; artifacts: ${output}`,
  );
  await command("POST", "/moz/addon/install", {
    addon: readFileSync(
      new URL("../public/uni-api-browser-helper-firefox.zip", import.meta.url),
    ).toString("base64"),
    temporary: true,
  });
  // Set up the same optional permission that the popup requests after a click.
  // No production account, profile, certificate store or browser setting changes.
  await command("POST", "/moz/context", { context: "chrome" });
  try {
    const granted = await command("POST", "/execute/async", {
      script: `
      const done = arguments[arguments.length - 1];
      const {ExtensionPermissions} = ChromeUtils.importESModule('resource://gre/modules/ExtensionPermissions.sys.mjs');
      const extension = WebExtensionPolicy.getByID(arguments[0]).extension;
      ExtensionPermissions.add(arguments[0], {permissions:['cookies'], origins:[]}, extension).then(() => done(true), e => done({error:String(e)}));
    `,
      args: [addonID],
    });
    assert.equal(granted, true, JSON.stringify(granted));
  } finally {
    await command("POST", "/moz/context", { context: "content" });
  }
  const consoleTab = await openTab(consoleOrigin + "/firefox-fixture");
  await helperReady();
  const sub = await startLogin("sub2api");
  const subResult = await resultFor(consoleTab, sub.id);
  assert.equal(
    subResult.auth?.access_token,
    "fixture-sub2api-access",
    JSON.stringify(subResult),
  );
  assert.equal(subResult.auth.refresh_token, "fixture-sub2api-refresh");
  await until(
    async () => !(await handles()).includes(sub.loginTab),
    "successful login tab cleanup",
  );
  passed("sub2api login and return to console");

  await openTab(siteOrigin + "/inspect");
  await script("localStorage.clear()");
  await command("DELETE", "/window");
  await focus(consoleTab);

  const canceled = await startLogin("sub2api", siteOrigin + "/cancel");
  await script("window.postMessage(arguments[0], location.origin)", {
    channel,
    id: canceled.id,
    type: "cancel",
  });
  const cancelResult = await resultFor(consoleTab, canceled.id);
  assert.equal(cancelResult.auth, undefined);
  assert.match(cancelResult.error, /取消/);
  await until(
    async () => !(await handles()).includes(canceled.loginTab),
    "canceled login tab cleanup",
  );
  passed("cancel closes login tab and returns no session");

  const containers = await privileged(`
    let identities;
    try {
      identities = ChromeUtils.importESModule('resource://gre/modules/ContextualIdentityService.sys.mjs');
    } catch {
      identities = ChromeUtils.importESModule('moz-src:///toolkit/components/contextualidentity/ContextualIdentityService.sys.mjs');
    }
    const {ContextualIdentityService} = identities;
    return [ContextualIdentityService.create('Helper owner', 'fingerprint', 'blue').userContextId,
      ContextualIdentityService.create('Unrelated container', 'briefcase', 'red').userContextId];
  `);
  const [ownerContainer, otherContainer] = containers;
  for (const [container, session] of [
    [0, "default-sentinel"],
    [otherContainer, "other-container-sentinel"],
  ]) {
    await openTab(siteOrigin + "/seed?session=" + session, container);
    await command("DELETE", "/window");
  }
  const containerConsole = await openTab(
    consoleOrigin + "/container-fixture",
    ownerContainer,
  );
  await helperReady();
  const native = await startLogin("newapi");
  const nativeTab = await until(
    () =>
      privileged(
        `return [...gBrowser.tabs].find(t => t.linkedBrowser.currentURI.spec.startsWith(arguments[0]))?.userContextId`,
        siteOrigin + "/sign-in",
      ),
    "New API container navigation",
  );
  assert.equal(
    nativeTab,
    ownerContainer,
    "login tab must inherit the initiating Firefox container",
  );
  await focus(native.loginTab);
  await until(
    () =>
      script(
        "return document.querySelector('#username')?.value === arguments[0]",
        fixtureEmail,
      ),
    "New API form fill",
  );
  assert.equal(
    await script("return document.querySelector('#password').value"),
    "fixture-only-password",
  );
  assert.equal(loginCount, 0, "New API must wait for the user's submit");
  await script("document.querySelector('button[type=submit]').click()");
  const nativeResult = await resultFor(containerConsole, native.id);
  assert.equal(
    nativeResult.auth?.browser_kind,
    "newapi",
    JSON.stringify(nativeResult),
  );
  assert.equal(nativeResult.auth.browser_user_id, 42);
  assert.deepEqual(nativeResult.auth.browser_cookies, {
    session: "container-login-session",
  });
  await until(
    async () => !(await handles()).includes(native.loginTab),
    "New API login tab cleanup",
  );
  passed("New API login exports only the initiating container's session", {
    ownerContainer,
    otherContainer,
  });
  for (const [container, expected] of [
    [0, "default-sentinel"],
    [otherContainer, "other-container-sentinel"],
    [ownerContainer, "container-login-session"],
  ]) {
    await openTab(siteOrigin + "/inspect", container);
    const cookies = await command("POST", "/execute/async", {
      script: `
      const done = arguments[arguments.length - 1]; fetch('/fixture/cookies').then(r => r.json()).then(done);
    `,
      args: [],
    });
    assert.ok(
      cookies.cookie.includes(`session=${expected}`),
      JSON.stringify(cookies),
    );
    if (container !== ownerContainer)
      assert.ok(!cookies.cookie.includes("container-login-session"));
    await command("DELETE", "/window");
  }
  passed(
    "default and unrelated container cookies remain isolated and unchanged",
  );
  writeFileSync(
    join(output, "results.json"),
    JSON.stringify(
      { browser: session.capabilities.browserVersion, results },
      null,
      2,
    ) + "\n",
  );
} catch (error) {
  if (sessionID) {
    const screenshot = await command("GET", "/screenshot").catch(() => null);
    if (screenshot)
      writeFileSync(
        join(output, "failure.png"),
        Buffer.from(screenshot, "base64"),
      );
  }
  writeFileSync(
    join(output, "results.json"),
    JSON.stringify({ results, error: error.stack }, null, 2) + "\n",
  );
  console.error(`Artifacts: ${output}`);
  throw error;
} finally {
  if (sessionID)
    await webdriver("DELETE", `/session/${sessionID}`).catch(() => {});
  gecko?.kill();
  for (const socket of sockets) socket.destroy();
  proxy.close();
  fixture.close();
}
