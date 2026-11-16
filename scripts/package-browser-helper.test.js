import { readFileSync } from "node:fs";
import { expect, it } from "vitest";
import { strFromU8, unzipSync } from "fflate";
import { buildBrowserHelperArchive } from "./package-browser-helper.mjs";

it.each(["universal", "chrome", "firefox"])(
  "packages a complete, credential-free %s extension with a root manifest",
  (target) => {
    const archive = unzipSync(buildBrowserHelperArchive(target));
    expect(Object.keys(archive).sort()).toEqual(
      [
        "README.md",
        "background.js",
        "bridge.js",
        "login-page.js",
        "manifest.json",
        "newapi-cookies.js",
        "newapi-login-page.js",
        "popup.html",
        "popup.js",
        "webextension-api.js",
      ].sort(),
    );
    const manifest = JSON.parse(strFromU8(archive["manifest.json"]));
    expect(manifest.manifest_version).toBe(3);
    expect(manifest.version).toBe("1.3.0");
    expect(manifest.background.type).toBe("module");
    expect(manifest.permissions).toEqual(["scripting"]);
    expect(manifest.optional_permissions).toEqual(["cookies"]);
    expect(manifest.content_scripts[0].matches).toEqual([
      "https://uni-api-console.fugue.pro/*",
    ]);
    expect(manifest.background.service_worker).toBe(
      target === "firefox" ? undefined : "background.js",
    );
    expect(manifest.background.scripts).toEqual(
      target === "chrome" ? undefined : ["background.js"],
    );
    expect(manifest.minimum_chrome_version).toBe(
      target === "firefox" ? undefined : "121",
    );
    if (target === "chrome")
      expect(manifest.browser_specific_settings).toBeUndefined();
    else {
      expect(manifest.browser_specific_settings.gecko.strict_min_version).toBe(
        "140.0",
      );
      expect(manifest.browser_specific_settings.gecko.id).toBeTruthy();
      expect(
        manifest.browser_specific_settings.gecko.data_collection_permissions
          .required,
      ).toContain("authenticationInfo");
    }
    for (const name of Object.keys(archive).filter(
      (name) => name !== "manifest.json",
    )) {
      expect(strFromU8(archive[name])).toBe(
        readFileSync(
          new URL("../browser-helper/" + name, import.meta.url),
          "utf8",
        ),
      );
    }
    for (const name of ["background.js", "newapi-cookies.js", "popup.js"]) {
      const imports = [
        ...strFromU8(archive[name]).matchAll(/from "\.\/(.+?)"/g),
      ];
      for (const [, dependency] of imports)
        expect(archive[dependency]).toBeDefined();
    }
    expect(strFromU8(archive["popup.html"])).toContain(
      'type="module" src="popup.js"',
    );
  },
);

it("rejects unknown packaging targets", () => {
  expect(() => buildBrowserHelperArchive("unknown")).toThrow("Unknown");
});
