# Serve HTML documents inside a sandbox

HTML files (`.html`, `.htm`) are Documents, but they do not pass through the Markdown renderer. The original bytes are served with `Content-Type: text/html` and a sandbox CSP: `sandbox allow-same-origin`, no `allow-scripts`, no forms, frames, objects, or plugins. The Rich Reader embeds that page in a same-origin iframe with the same sandbox flags. `/raw/*.html` deliberately returns the source as `text/plain` so opening it cannot execute it.

Sanitizing HTML through bluemonday would break layout, embedded styles, and document semantics, which is the reason to keep an HTML file at all. Serving it unchanged on the trusted LAN without isolation would allow stored XSS under the chare origin, so isolation happens at the browser boundary instead of by rewriting the file. `allow-same-origin` is required for relative images and inline styles to resolve; it must never be paired with `allow-scripts`.

Consequences: scripts and forms in HTML documents do not run; sibling `.css`, `.js`, and font files are not supported Library types yet, so only inline styling and raster images resolve. Any future widening of the sandbox flags needs a new ADR.
