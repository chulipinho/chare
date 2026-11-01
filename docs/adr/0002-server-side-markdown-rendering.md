# One server-side Markdown pipeline

All Markdown is rendered once on the server with goldmark (GFM) and sanitized with bluemonday, and clients either receive the rendered HTML or the raw source. Rendering in the browser would require a second renderer for the JavaScript-free Simple Reader and risk divergent output; server rendering also keeps old Kindle browsers working and gives one place to enforce XSS safety.
