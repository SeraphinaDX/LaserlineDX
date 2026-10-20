# Changelog

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
