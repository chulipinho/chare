# Go templates instead of an Angular SPA

The management UI is server-rendered Go templates with small vanilla JavaScript enhancements, embedded in the single binary, rather than an Angular SPA. The UI is light CRUD (list, upload, create, rename, delete), and a SPA would add a Node build step, a second front-end for the Kindle, and client-side rendering for a read path that must work on ancient browsers. The `/api/v1` surface exists independently for future native clients.
