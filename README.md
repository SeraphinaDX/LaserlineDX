# Warp

Warp is a tiny, encrypted multi-user **file-sharing BBS** written in Go. The client
uses [gotui v5](https://github.com/metaspartan/gotui) to browse files and chat.
It is a direct-connect Croc alternative: no accounts, no environment-variable
configuration, and no third-party server required.

**Status: 0.2.0 experimental source release.** The protocol has not undergone
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

In the BBS, use **Up/Down** or **j/k** to move through files, or **PageUp**, **PageDown**, **Home** and **End** for large directories. The file list scrolls with the selected row, and displays the current row range. Mouse-wheel scrolling also works over the file area.

- **F** (or **f**, or **Space**) toggles a flag on the highlighted file. Flagged files are shown with `[x]`.
- **D** (or **d**) downloads **all flagged files** in file-list order, one at a time. Click **[ Download All ]** in the bottom action bar to do the same with the mouse.
- **Left-click** a file to select it; **right-click** the file row to flag/unflag it. The mouse hit target follows the scrolled list.
- **Enter** opens the **encrypted chat prompt**, not a file download. **Enter** while composing sends the message; **Escape** leaves chat and returns to file navigation. You can also click **[ Chat ]**, **[ Files ]**, or the chat prompt area.
- **r** refreshes the file listing; flags survive a refresh when the exact file version remains available. **q** disconnects from file-navigation mode; **Ctrl+C** always quits.

The file pane and chat pane are side by side on wide terminals (92+ columns). On narrower terminals, Enter/chat switches the main pane between files and chat. The minimum terminal size is 40 columns by 16 rows. A terminal with mouse reporting enabled is needed for pointer actions (gotui enables this by default).

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

Flagged downloads are queued sequentially, rather than started all at once. A successful file is removed from the flagged selection; if one fails, the batch pauses and that file and the unattempted files remain flagged so you can retry them. Your flag selections are only kept for the current connection, but the on-disk partial files survive reconnects.

Warp downloads to `filename.warp-part` and writes a nearby JSON metadata file.
If the connection drops, Warp retries up to four times automatically; on
subsequent sessions, selecting that file continues from the on-disk offset.
An entire-file SHA-256 checksum is verified before the completed file is
renamed into place. Server file size and modification time must remain
unchanged while transferring. A changed file has a new ID and must start a
new download. Warp does not overwrite completed downloads.

For now, there are a few constraints: downloads are one at a time per client,
the file list is a snapshot until refreshed (and limited to 10,000 files per share), all server clients need to be
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
