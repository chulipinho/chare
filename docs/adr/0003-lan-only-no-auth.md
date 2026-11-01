# LAN-only, no authentication

The server binds to the local network and serves the Library without authentication, trusting the home network. Authentication would add friction on the Kindle and phone for a personal single-user tool. The threat model still requires path-traversal prevention, symlink rejection, upload type allow-listing, and HTML sanitization. If exposure ever changes beyond the LAN, authentication must be added in front of the mux before the server is exposed.
