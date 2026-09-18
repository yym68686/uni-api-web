import { readFileSync, writeFileSync, mkdirSync } from "node:fs";
import { zipSync, strToU8 } from "fflate";
const files = [
  "manifest.json",
  "background.js",
  "bridge.js",
  "login-page.js",
  "README.md",
];
const archive = Object.fromEntries(
  files.map((name) => [
    "uni-api-browser-helper/" + name,
    strToU8(
      readFileSync(
        new URL("../browser-helper/" + name, import.meta.url),
        "utf8",
      ),
    ),
  ]),
);
mkdirSync(new URL("../public/", import.meta.url), { recursive: true });
writeFileSync(
  new URL("../public/uni-api-browser-helper.zip", import.meta.url),
  zipSync(archive),
);
