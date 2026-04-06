const CLIPBOARD_ERROR_MESSAGE = "Clipboard copy failed";

function fallbackCopyText(value: string) {
  if (typeof document === "undefined" || !document.body) {
    throw new Error(CLIPBOARD_ERROR_MESSAGE);
  }

  const textarea = document.createElement("textarea");
  textarea.value = value;
  textarea.setAttribute("readonly", "");
  textarea.setAttribute("aria-hidden", "true");
  textarea.style.position = "fixed";
  textarea.style.top = "0";
  textarea.style.left = "-9999px";
  textarea.style.opacity = "0";
  textarea.style.pointerEvents = "none";

  const selection = document.getSelection();
  const previousRange = selection && selection.rangeCount > 0 ? selection.getRangeAt(0).cloneRange() : null;
  const activeElement = document.activeElement instanceof HTMLElement ? document.activeElement : null;

  document.body.append(textarea);
  textarea.focus();
  textarea.select();
  textarea.setSelectionRange(0, textarea.value.length);

  const didCopy = document.execCommand("copy");
  textarea.remove();

  if (selection) {
    selection.removeAllRanges();
    if (previousRange) {
      selection.addRange(previousRange);
    }
  }
  activeElement?.focus();

  if (!didCopy) {
    throw new Error(CLIPBOARD_ERROR_MESSAGE);
  }
}

export async function copyText(value: string) {
  if (typeof navigator !== "undefined" && navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(value);
      return;
    } catch {
      // Fall through to the legacy copy command for browsers with stricter clipboard policies.
    }
  }

  fallbackCopyText(value);
}

export async function copyTextFromAsyncSource(resolveText: () => Promise<string>) {
  // Safari requires the clipboard write to start inside the user gesture, even if the text resolves later.
  if (typeof navigator !== "undefined" && navigator.clipboard?.write && typeof ClipboardItem !== "undefined") {
    const textPromise = resolveText();

    try {
      await navigator.clipboard.write([
        new ClipboardItem({
          "text/plain": textPromise.then((text) => new Blob([text], { type: "text/plain" }))
        })
      ]);
      return;
    } catch {
      const text = await textPromise;
      await copyText(text);
      return;
    }
  }

  const text = await resolveText();
  await copyText(text);
}
