import { readFileSync, writeFileSync, mkdirSync } from "node:fs";
import { resolve } from "node:path";
import { pathToFileURL } from "node:url";
import { zipSync, strToU8 } from "fflate";
const files = [
  "manifest.json",
  "background.js",
  "bridge.js",
  "login-page.js",
  "newapi-login-page.js",
  "newapi-cookies.js",
  "webextension-api.js",
  "popup.html",
  "popup.js",
  "README.md",
];

export function buildBrowserHelperArchive(target = "universal") {
  if (!["universal", "chrome", "firefox"].includes(target))
    throw new Error("Unknown browser helper target: " + target);
  const archive = Object.fromEntries(
    files.map((name) => {
      let source = readFileSync(
        new URL("../browser-helper/" + name, import.meta.url),
        "utf8",
      );
      if (name === "manifest.json") {
        const manifest = JSON.parse(source);
        if (target === "chrome") {
          delete manifest.background.scripts;
          delete manifest.browser_specific_settings;
        } else if (target === "firefox") {
          delete manifest.background.service_worker;
          delete manifest.minimum_chrome_version;
        }
        source = JSON.stringify(manifest, null, 2) + "\n";
      }
      // A root manifest works with Firefox web-ext/AMO as well as Chrome's
      // unpacked loader. Only explicitly reviewed sources enter the archive.
      return [name, strToU8(source)];
    }),
  );
  return zipSync(archive);
}

export function packageBrowserHelper(
  outputDir = new URL("../public/", import.meta.url),
) {
  mkdirSync(outputDir, { recursive: true });
  for (const target of ["universal", "chrome", "firefox"]) {
    const suffix = target === "universal" ? "" : "-" + target;
    writeFileSync(
      new URL("uni-api-browser-helper" + suffix + ".zip", outputDir),
      buildBrowserHelperArchive(target),
    );
  }
}

if (
  process.argv[1] &&
  pathToFileURL(resolve(process.argv[1])).href === import.meta.url
)
  packageBrowserHelper();
