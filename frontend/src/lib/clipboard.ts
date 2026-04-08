// copyToClipboard writes text to the system clipboard with a fallback
// for non-secure contexts. navigator.clipboard only works over HTTPS or
// localhost; iplayer-arr is typically reached over plain HTTP on a LAN
// IP, where the modern API silently rejects. Fall back to a hidden
// textarea + execCommand so copy buttons still work. See GitHub issue #21.
export async function copyToClipboard(text: string): Promise<boolean> {
  try {
    if (window.isSecureContext && navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text);
      return true;
    }
  } catch {
    // fall through to legacy path
  }
  try {
    const ta = document.createElement("textarea");
    ta.value = text;
    ta.style.position = "fixed";
    ta.style.top = "-1000px";
    ta.style.opacity = "0";
    document.body.appendChild(ta);
    ta.focus();
    ta.select();
    const ok = document.execCommand("copy");
    document.body.removeChild(ta);
    return ok;
  } catch {
    return false;
  }
}
