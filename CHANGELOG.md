# Changelog

## 0.5.0 — Mouse double-click, recursive directory tags, Warp neon palette

- Added double-click with the left mouse button: enter directories (and go up via the parent entry) or download a single file without tagging it. A single click still only selects.
- **Space** and right-click now tag directories as well as files. Tagged directories recursively include children, create empty directories locally, preserve relative paths, and deduplicate overlapping tags.
- If a batch fails, outstanding items remain tagged individually for safe retry without overwriting completed files.
- Added Warp's violet/cyan/hot-pink/gold retro terminal theme and clearer folder tag indicators.
- Reworded CLI help and README to describe Warp as **BBS-like**, explicitly distinguishing it from a complete BBS.
- Added deterministic mouse timing tests, recursive tagging tests, safe folder-creation tests, and a live encrypted tagged-folder integration test.


## 0.4.0 — Tab chat, Enter downloads, real directory browser

- Changed the chat shortcut from Enter to **Tab**; Enter still sends a message while chatting, and Escape leaves chat.
- **Enter** in file browsing now descends into a directory or starts a resumable, SHA-256-verified download of one selected file.
- The server now announces directory entries, including empty directories, alongside regular files.
- The client displays a real hierarchical browser, with folders before files, a breadcrumb-like current path in the title, and a `[..] Parent directory` row in subfolders.
- **Backspace** or **Escape** goes to the parent while file browsing. Folder navigation preserves selection and scrolls large directories correctly.
- At 0.4.0, flags were file-only and persisted across navigation and refresh; **D** downloaded flagged files from multiple directories.
- Added hierarchical navigation, empty-folder, scrolling/refresh and single-file Enter unit tests, plus server directory-listing tests.
- Upgrade both host and clients together; old clients cannot distinguish new folder entries from files.

## 0.3.0 — Download progress and dedicated transfer status

- Made **Space** the sole keyboard shortcut for flagging/unflagging files; removed F/f for that purpose. Right-click flagging remains available.
- Added a persistent **TRANSFER STATUS** pane beneath the files/chat area.
- Added an updating per-file progress bar, percentage, current filename, amount downloaded, and current transfer speed.
- Added overall batch progress and a separate batch progress bar, as well as the batch position and transfer attempt number.
- Status continues to show reconnect/retry messages and reports success or a paused batch after completion or failure.
- Kept file-list scrolling and mouse hit testing aligned with the resized file viewport at all supported terminal heights.
- Added tests for the new keyboard mapping, progress display, retry/transfer transitions, and responsive layout.

## 0.2.0 — Mouse controls and batch file selection

- Added mouse selection, right-click file flagging, and mouse-wheel scrolling in the file area.
- Added a visible **Download All** action, plus Chat and Files actions usable with the mouse.
- Added file flagging with F/f (and Space) and downloading all flagged files with D/d.
- Changed Enter to open chat; Enter sends while typing; Escape returns to the file area.
- Reworked the file list to use a persistent, bounded viewport. Large directory browsing and mouse row-to-file selection no longer depend on gotui's ephemeral widget scroll position.
- Added PageUp, PageDown, Home, and End navigation.
- Sequential batch transfers continue using TLS 1.3 and resumable partial downloads, with a per-file progress indicator and SHA-256 verification. Failed and remaining entries stay flagged.
- Flags survive file-list refreshes for unchanged file IDs, and stale entries are removed.
- Added UI navigation/flagging tests and a multi-file batch integration test.

## 0.1.0 — Initial experimental source release

- Encrypted mini BBS, ephemeral multi-user chat, shared-file browsing, TLS 1.3, and resumable file downloads.
