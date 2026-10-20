# Warp

Warp is a tiny, encrypted, multi-user **BBS-like file-sharing and chat tool**
written in Go. It offers the **file area and live chat of a miniature BBS-like
experience**, without claiming to be a full bulletin board system. The terminal
client uses [gotui v5](https://github.com/metaspartan/gotui). Warp is a
direct-connect alternative to Croc: no accounts, no environment-variable
configuration, and no third-party relay required.

**Warp is not a full BBS:** there are no message boards, user accounts, doors,
forums, persistent chat archives, or remote uploads. It concentrates on
encrypted downloads, browsing shared folders, and ephemeral group chat.

**Status: 0.5.0 experimental source release.** The protocol has not undergone
an independent security audit. Do not treat it as a hardened public file server.

## Build

Go **1.24 or newer** is required for gotui v5 and for the bounded filesystem
API (`os.OpenRoot`).

```sh
go mod tidy
go build -o warp ./cmd/warp
```

## Quickstart

On the host, in the directory you want to share:

```sh
cd ~/Documents
warp serve
```

Warp prints an invitation code beginning with `W1-`. Send this code privately.
Another user enters:

```sh
warp connect W1-PASTE_THE_FULL_CODE_HERE
```

In the BBS-like file area, use **Up/Down** or **j/k** to move through entries, or **PageUp**, **PageDown**, **Home** and **End** for large directories. The current directory appears in the file pane's title, and the list scrolls with the selected row. Mouse-wheel scrolling also works over the file area. Folders are listed before files and marked `[DIR]`, and even empty shared folders appear in the listing.

- **Enter** opens a selected **directory** or begins downloading the selected **file** immediately, without needing to flag it first. File downloads use the same resume and checksum checks as batch downloads.
- **`[..] Parent directory`** is at the top of every non-root folder. Highlight it and press **Enter** to go back, or use **Backspace** or **Escape** while browsing files. Leaving a folder selects it in its parent, so it's easy to browse nearby folders.
- **Space** tags/untags the selected **file or directory**. Tagged items show `[x]`. A tagged directory includes every file and subdirectory inside it, including empty directories; tagged descendants also show `[x]`. To exclude an item inherited from a tagged folder, untag its parent first. Existing independent file tags are not discarded when a folder is untagged. **F/f does not tag anything**.
- **D** (or **d**) downloads everything tagged: individual files **and entire directories**, recursively. The original relative folder structure is kept, including empty folders. Overlapping tags are deduplicated. Click **[ Download All ]** in the bottom action bar for the same action with a mouse.
- **Single left-click** selects a row; **double left-click** opens a directory (or `[..]` to go up), or starts the resumable download of a single file. **Right-click** toggles tags on files **and directories**. Mouse-wheel scrolling and hit testing follow the visible file viewport; the two clicks must be on the same row within 450 ms. Double-click never tags a file.
- **Tab** opens the **encrypted chat prompt**. **Enter** while composing sends the message; **Escape** leaves chat and returns to file navigation. You can also click **[ Chat ]**, **[ Files ]**, or the chat prompt area.
- **r** refreshes the file listing; flags survive a refresh when the exact file version remains available. **q** disconnects from file-navigation mode; **Ctrl+C** always quits.

### Warp's retro terminal palette

Warp's gotui interface uses **plasma violet borders**, **electric cyan file
panes**, **hot-pink chat and transfer outlines**, **gold selections and titles**,
and a **continuous midnight-blue background** (including text, list rows,
borders, empty spaces, and the bottom status strip). This neon, BBS-inspired palette is part of
the regular client UI (not a special terminal theme to install). Truecolor
terminals such as Alacritty show the full palette; other terminals may
approximate the colours. Warp paints every terminal cell and gives foreground
text an explicit dark background, so the terminal's own background should
not bleed through during scrolling or resizing.

The file pane and chat pane are side by side on wide terminals (92+ columns). On narrower terminals, Tab/chat switches the main pane between files and chat. A dedicated **TRANSFER STATUS** window is visible under the file/chat area while idle and during downloads. It shows the current filename, progress bar and percentage, overall batch progress, downloaded bytes, current transfer speed, retry attempt and transfer/reconnect status. Smaller terminals use a compact version of the window to keep the file list usable. The minimum terminal size is 40 columns by 16 rows. A terminal with mouse reporting enabled is needed for pointer actions (gotui enables this by default).

Downloads default to `./WarpDownloads`. You can select another path:

```sh
warp connect --nick Brit --downloads ~/Downloads W1-PASTE_CODE
```

### Sharing files

```sh
warp serve                    # shares the current directory recursively
warp serve ~/Documents         # shares that directory recursively
warp serve ~/Downloads/book.pdf # shares just book.pdf
```

Directory symlinks are not followed during listing, and files are opened
within an `os.Root` jail. **Everything regular under the shared directory is
visible to anyone with the code**, including hidden files. Use a dedicated
sharing directory if you don't want to expose unrelated files.

The server now includes directory entries in the initial listing and on
refresh, including empty directories. **Update both the serving and connecting
Warp installations to 0.4.0 or newer together.** Older clients do not understand the
new directory indicator and may attempt to download a folder.

### Over the internet

Warp's invitation code holds the reachable host/port, an access secret,
and a pinned TLS certificate fingerprint. It does **not** use a rendezvous
service or relay; for NAT traversal you need a forwarded TCP port or a VPN.

```sh
warp serve --listen :47477 --advertise your.public.ip
# Or: --advertise your-tailscale-ip
```

On one LAN, Warp automatically advertises a detected local IPv4 address.
If it selects the wrong interface, use `--advertise` explicitly. The default
listening port is **TCP 47477**.

## Privacy and encryption

- **TLS 1.3 is mandatory**, on both the control/chat stream and each file stream.
- Warp generates a temporary Ed25519 TLS certificate and a random 128-bit
  capability on each `warp serve` session. No environment variables or
  external accounts are needed.
- Clients pin the SHA-256 hash of the server certificate embedded in the
  invitation, rejecting mismatched hosts (no "accept unknown certificate").
- The code is a **bearer invitation**. Anyone with it can join and download;
  do not post it publicly. Generate a new code by restarting the server.
- Chat is encrypted **in transit between each client and the server**; the
  host can read messages in server memory. It is *not* peer-to-peer end-to-end
  encrypted chat. Chat history is not written to disk by Warp.
- Source files are never intentionally modified by Warp.

## Reliable transfers

Tagged downloads are queued sequentially, rather than started all at once.
Pressing Enter or double-clicking a file starts a single download using that same
machinery. Tagged folders expand into a batch of all nested files and folders;
empty folders are created locally too. Each transferred file goes through TLS
1.3 and SHA-256 verification. The status window remains visible during
transfers, including while chatting. If a batch fails partway through, the
unfinished entries are tagged individually so retrying won't try to overwrite
already completed downloads. Tags last for the current connection, but on-disk
partial files survive reconnects.

Warp downloads to `filename.warp-part` and writes a nearby JSON metadata file.
If the connection drops, Warp retries up to four times automatically; on
subsequent sessions, selecting that file continues from the on-disk offset.
An entire-file SHA-256 checksum is verified before the completed file is
renamed into place. Server file size and modification time must remain
unchanged while transferring. A changed file has a new ID and must start a
new download. Warp does not overwrite completed downloads.

For now, there are a few constraints: downloads are one at a time per client,
the file list is a snapshot until refreshed (and limited to 10,000 file and directory entries per share), all server clients need to be
able to reach the host over TCP, uploads are not supported, and the server
shows a terminal log rather than an interactive host chat pane.

## Development

```sh
go test ./...
go test -race ./...
go vet ./...
```

This project is distributed under the GNU General Public License v3.0 or
later. See `LICENSE`.
