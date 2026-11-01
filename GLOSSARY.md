# chare

A personal, LAN-only library for reading Markdown documents across devices (phone, tablet, jailbroken Kindle).

## Language

**Library**:
The directory tree configured as `DATA_DIR`; the only source of truth for content.
_Avoid_: Store, vault, database

**Document**:
A Markdown or plain-text file inside the Library.
_Avoid_: Note, plan, page, file

**Asset**:
A raster image (`.png`, `.jpg`, `.jpeg`, `.gif`, `.webp`) inside the Library, referenced by a Document.
_Avoid_: Attachment, media, image file

**Document Path**:
The slash-separated path relative to the Library root that identifies a Document or Asset.
_Avoid_: Key, ID, slug

**Listing**:
The view of one Library directory, showing its subdirectories, Documents, and Assets.
_Avoid_: Index, folder view

**Rich Reader**:
The full-featured HTML view of a Document, with breadcrumbs, table of contents, and typography controls.
_Avoid_: Web view, app reader

**Simple Reader**:
The minimal, JavaScript-free HTML view of a Document for old or constrained browsers such as the Kindle experimental browser.
_Avoid_: Plain view, kindle mode
