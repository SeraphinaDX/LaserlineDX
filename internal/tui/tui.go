// Package tui provides Laserline's gotui v5 BBS-like file and chat terminal client.
package tui

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	ui "github.com/metaspartan/gotui/v5"
	"github.com/metaspartan/gotui/v5/widgets"

	"laserline/internal/transfer"
	"laserline/internal/wire"
)

const actionBar = " [ Download All ]  [ Chat ]  [ Files ]"

// Laserline's neon terminal palette: violet plasma, hot-pink accents, cyan signal
// lines and gold highlights on a dark-space background. RGB colors are
// downgraded by the terminal backend when truecolor is unavailable.
var (
	laserlineViolet = ui.NewRGBColor(161, 83, 255)
	laserlinePink   = ui.NewRGBColor(255, 72, 170)
	laserlineCyan   = ui.NewRGBColor(78, 232, 244)
	laserlineGold   = ui.NewRGBColor(255, 209, 99)
	laserlineInk    = ui.NewRGBColor(12, 14, 39)
	laserlineWhite  = ui.NewRGBColor(228, 235, 254)
)

const doubleClickInterval = 450 * time.Millisecond

type clickStamp struct {
	name string
	dir  string
	at   time.Time
}

type area struct{ left, top, right, bottom int }

func (r area) contains(x, y int) bool {
	return x >= r.left && x < r.right && y >= r.top && y < r.bottom
}

type layout struct {
	files, chat, prompt, transfer area
	buttons                       [3]area // Download All, Chat, Files
	fileRows                      int
	filesVisible                  bool
	wide                          bool
}

// The same geometry powers both rendering and mouse hit testing. Each file
// occupies exactly one cell row; list widgets do not own the scroll position.
func makeLayout(width, height int, composing bool) layout {
	wide := width >= 92
	fileRight := width
	if wide {
		fileRight = width * 45 / 100
	}
	barY := height - 3 // content row inside the three-cell footer block
	// Reserve a separate transfer-status panel without letting the file
	// viewport disappear on small terminals. All panes use the same bounds
	// for rendering and mouse hit testing.
	transferHeight := 3
	switch {
	case height >= 32:
		transferHeight = 7
	case height >= 25:
		transferHeight = 6
	case height >= 21:
		transferHeight = 5
	case height >= 18:
		transferHeight = 4
	}
	transferTop := height - 7 - transferHeight
	l := layout{
		files:        area{0, 3, fileRight, transferTop},
		chat:         area{fileRight, 3, width, transferTop},
		transfer:     area{0, transferTop, width, height - 7},
		prompt:       area{0, height - 7, width, height - 4},
		fileRows:     max(1, transferTop-5),
		filesVisible: wide || !composing,
		wide:         wide,
	}
	if !wide {
		l.chat = area{0, 3, width, transferTop}
	}
	labels := [...]string{"[ Download All ]", "[ Chat ]", "[ Files ]"}
	for i, label := range labels {
		start := strings.Index(actionBar, label) + 1 // left border takes cell 0
		l.buttons[i] = area{start, barY, start + len(label), barY + 1}
	}
	return l
}

type batchResult struct {
	succeeded  []string
	total      int
	lastName   string
	lastSize   int64
	failed     string
	remaining  []wire.File
	taggedDirs []string
	filesDone  int
	dirsDone   int
	err        error
}

// downloadEvent carries the batch position separately from the transfer
// message so UI progress does not depend on parsing human-readable strings.
type downloadEvent struct {
	transfer.Progress
	index          int // 1-based index in the batch
	completedBytes int64
}

// downloadStatus is owned exclusively by the TUI event loop. The transfer
// goroutine only sends events and never edits a widget or the status model.
type downloadStatus struct {
	active         bool
	name           string
	bytes, total   int64
	batchBytes     int64
	completedBytes int64
	index, count   int
	attempt        int
	message        string
	speed          float64 // bytes per second during this attempt
	lastBytes      int64
	lastSample     time.Time
}

// Directories can be tagged for a recursive batch download, but Enter on a
// directory always navigates into it. The parent row
// is synthetic; real directories (including empty ones) come from the server.
// Synthesizing intermediate folders also handles older servers whose listing
// only contained files, without changing the underlying download IDs.
type browserEntry struct {
	file   wire.File
	parent bool
}

func makeBrowserEntries(all []wire.File, dir string) []browserEntry {
	seen := make(map[string]wire.File)
	for _, f := range all {
		if f.Name == "" || strings.HasPrefix(f.Name, "/") {
			continue
		}
		rel := f.Name
		if dir != "" {
			if !strings.HasPrefix(f.Name, dir+"/") {
				continue
			}
			rel = strings.TrimPrefix(f.Name, dir+"/")
		}
		if rel == "" {
			continue
		}
		if slash := strings.IndexByte(rel, '/'); slash >= 0 {
			child := rel[:slash]
			name := child
			if dir != "" {
				name = dir + "/" + child
			}
			// A directory is displayed once regardless of how many files it has.
			if _, ok := seen[name]; !ok {
				seen[name] = wire.File{Name: name, IsDir: true}
			}
			continue
		}
		if f.IsDir || !strings.ContainsRune(rel, '/') {
			seen[f.Name] = f
		}
	}
	entries := make([]browserEntry, 0, len(seen)+1)
	if dir != "" {
		entries = append(entries, browserEntry{parent: true})
	}
	for _, f := range seen {
		entries = append(entries, browserEntry{file: f})
	}
	start := 0
	if dir != "" {
		start = 1 // leave .. at the top
	}
	sort.Slice(entries[start:], func(i, j int) bool {
		x, y := entries[i+start].file, entries[j+start].file
		if x.IsDir != y.IsDir {
			return x.IsDir
		}
		xName, yName := strings.ToLower(path.Base(x.Name)), strings.ToLower(path.Base(y.Name))
		if xName == yName {
			return x.Name < y.Name
		}
		return xName < yName
	})
	return entries
}

func (a *bbs) observeDownload(e downloadEvent, now time.Time) {
	d := &a.download
	newAttempt := e.index != d.index || e.Attempt != d.attempt
	if newAttempt {
		d.lastSample = time.Time{}
		d.speed = 0
	}
	if e.Name != "" {
		d.name = e.Name
	}
	d.index = e.index
	d.attempt = e.Attempt
	d.total = max64(0, e.Total)
	d.completedBytes = e.completedBytes
	if e.Message != "" {
		d.message = e.Message
	}
	// A retry/error notice has no byte offset. Keep the last known progress
	// instead of jumping the bar backwards to zero during reconnects.
	if strings.HasPrefix(e.Message, "Interrupted:") {
		d.speed = 0
		d.lastSample = time.Time{}
		return
	}
	d.bytes = min64(d.total, max64(0, e.Bytes))
	if !d.lastSample.IsZero() && d.bytes >= d.lastBytes {
		secs := now.Sub(d.lastSample).Seconds()
		if secs >= 0.1 {
			d.speed = float64(d.bytes-d.lastBytes) / secs
		}
	}
	d.lastBytes = d.bytes
	d.lastSample = now
}

type bbs struct {
	files       []wire.File
	entries     []browserEntry // only the rows in the current directory
	dir         string         // slash-separated path relative to the share root
	cursor      int
	scroll      int
	flagged     map[string]bool // stable file ID, not row number
	flaggedDirs map[string]bool // directory path, recursively includes descendants
	chat        []string
	composing   bool
	input       *widgets.Input
	nick        string
	people      int
	status      string
	progress    chan downloadEvent
	completed   chan batchResult
	busy        bool
	download    downloadStatus
	ctx         context.Context
	client      *transfer.Client
	downloads   string
	layout      layout
	heldMouse   string     // avoid repeated toggles from held/dragged right-clicks
	lastClick   clickStamp // double-click works on the same visible row only
}

// Run owns the terminal and all widget rendering. Network goroutines send
// events to channels; they never manipulate gotui state directly.
func Run(ctx context.Context, c *transfer.Client, welcome wire.Message, downloads string) error {
	if err := ui.Init(); err != nil {
		return err
	}
	defer ui.Close()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	a := &bbs{
		ctx: ctx, client: c, files: welcome.Files, nick: welcome.Name, people: welcome.Users,
		status:    "TLS 1.3 linked. Double-click/Enter: open or get. Space/right-click: tag. D: download all.",
		downloads: downloads, progress: make(chan downloadEvent, 32), completed: make(chan batchResult, 1),
		input: widgets.NewInput(), flagged: make(map[string]bool), flaggedDirs: make(map[string]bool),
	}
	a.chat = append(a.chat, "Welcome to Laserline DX's BBS-like file area and live chat. Chat is session-only.")
	a.rebuildEntries("")
	a.draw()
	events := ui.PollEventsWithContext(ctx)
	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-events:
			if !ok {
				return nil
			}
			if a.handle(ev) {
				return nil
			}
			a.draw()
		case m, ok := <-c.Updates:
			if !ok {
				a.status = "Connection closed. Reconnect with the code; partial downloads can resume."
				a.draw()
				return nil
			}
			a.message(m)
			a.draw()
		case err := <-c.Done:
			if ctx.Err() == nil {
				a.status = "Disconnected: " + err.Error()
			}
			a.draw()
			return nil
		case p := <-a.progress:
			a.observeDownload(p, time.Now())
			a.draw()
		case result := <-a.completed:
			// A buffered progress message from an earlier file must not
			// replace the batch's final success/failure summary.
			for len(a.progress) > 0 {
				<-a.progress
			}
			a.busy = false
			a.download.active = false
			a.download.speed = 0
			for _, id := range result.succeeded {
				delete(a.flagged, id)
			}
			// A tagged parent expands into concrete entries. Once any item
			// fails, convert the unfinished portion into individual tags so
			// retry won't attempt to overwrite files already downloaded.
			for _, dir := range result.taggedDirs {
				delete(a.flaggedDirs, dir)
			}
			for _, f := range result.remaining {
				if f.IsDir {
					a.ensureDirFlags()
					a.flaggedDirs[f.Name] = true
				} else {
					a.flagged[f.ID] = true
				}
			}
			if result.err != nil {
				a.download.message = "Paused: " + result.err.Error()
				a.status = fmt.Sprintf("Completed %d/%d items; paused at %s: %v. Remaining items stay tagged.",
					result.filesDone+result.dirsDone, result.total, result.failed, result.err)
			} else {
				a.download.name = result.lastName
				a.download.index = result.total
				a.download.total = result.lastSize
				a.download.bytes = result.lastSize
				a.download.completedBytes = a.download.batchBytes - result.lastSize
				a.download.message = "Finished: files SHA-256 verified; folders created"
				a.status = fmt.Sprintf("Downloaded %d file(s), prepared %d folder(s); SHA-256 verified.", result.filesDone, result.dirsDone)
			}
			a.draw()
		}
	}
}

func (a *bbs) message(m wire.Message) {
	switch m.Type {
	case "files":
		oldName := a.selectedName()
		a.files = m.Files
		live := make(map[string]bool, len(a.files))
		for _, f := range a.files {
			if !f.IsDir {
				live[f.ID] = true
			}
		}
		for id := range a.flagged {
			if !live[id] {
				delete(a.flagged, id)
			}
		}
		for dir := range a.flaggedDirs {
			if !directoryExists(a.files, dir) {
				delete(a.flaggedDirs, dir)
			}
		}
		// A removed directory is no longer a viable browsing location.
		for a.dir != "" && !directoryExists(a.files, a.dir) {
			a.dir = parentDir(a.dir)
		}
		a.rebuildEntries(oldName)
		a.status = "File area refreshed (existing flags preserved for unchanged files)"
	case "notice":
		a.addLine("* " + m.Text)
		a.people = m.Users
	case "chat":
		a.addLine(m.Name + ": " + m.Text)
	case "error":
		a.status = "Server: " + m.Error
	}
}

func directoryExists(files []wire.File, dir string) bool {
	for _, f := range files {
		if (f.IsDir && f.Name == dir) || strings.HasPrefix(f.Name, dir+"/") {
			return true
		}
	}
	return false
}

func parentDir(dir string) string {
	if dir == "" || !strings.ContainsRune(dir, '/') {
		return ""
	}
	return path.Dir(dir)
}

func (a *bbs) selectedName() string {
	if a.cursor >= 0 && a.cursor < len(a.entries) && !a.entries[a.cursor].parent {
		return a.entries[a.cursor].file.Name
	}
	return ""
}

func (a *bbs) rebuildEntries(selectName string) {
	a.entries = makeBrowserEntries(a.files, a.dir)
	a.cursor = 0
	for i, e := range a.entries {
		if !e.parent && selectName != "" && e.file.Name == selectName {
			a.cursor = i
			break
		}
	}
	a.keepVisible()
}

func (a *bbs) browse(dir, selectName string) {
	a.lastClick = clickStamp{}
	a.dir, a.scroll = dir, 0
	a.rebuildEntries(selectName)
	if dir != "" && selectName == "" && len(a.entries) > 1 {
		a.cursor = 1 // highlight first child rather than the parent row
	}
	a.keepVisible()
	a.status = "Browsing /" + dir + " — Enter opens a folder or downloads a file; .. goes back."
}

func (a *bbs) upDir() {
	if a.dir != "" {
		child := a.dir
		a.browse(parentDir(child), child)
	}
}

func (a *bbs) activate() {
	if a.cursor < 0 || a.cursor >= len(a.entries) {
		return
	}
	e := a.entries[a.cursor]
	switch {
	case e.parent:
		a.upDir()
	case e.file.IsDir:
		a.browse(e.file.Name, "")
	default:
		a.startDownloads([]wire.File{e.file}, false)
	}
}
func (a *bbs) addLine(s string) {
	// Prevent untrusted gotui formatting syntax from being interpreted.
	s = strings.NewReplacer("[", "(", "]", ")", "\x1b", "").Replace(s)
	a.chat = append(a.chat, s)
	if len(a.chat) > 200 {
		a.chat = a.chat[len(a.chat)-200:]
	}
}

func (a *bbs) handle(e ui.Event) bool {
	if e.ID == "<C-c>" {
		return true
	}
	if e.Type == ui.MouseEvent {
		if p, ok := e.Payload.(ui.Mouse); ok {
			a.mouse(e.ID, p.X, p.Y)
		}
		return false
	}
	return a.key(e.ID)
}

func (a *bbs) key(id string) bool {
	a.lastClick = clickStamp{} // keyboard navigation breaks a mouse double-click sequence
	if a.composing {
		switch id {
		case "<Escape>":
			a.leaveChat()
		case "<Enter>":
			body := strings.TrimSpace(a.input.Text)
			if body != "" && a.client != nil {
				if err := a.client.Send(wire.Message{Type: "chat", Text: body}); err != nil {
					a.status = err.Error()
				}
			}
			a.input.Text = ""
			a.input.Cursor = 0
		case "<Backspace>":
			a.input.Backspace()
		case "<Left>":
			a.input.MoveCursorLeft()
		case "<Right>":
			a.input.MoveCursorRight()
		case "<Space>":
			a.input.InsertRune(' ')
		default:
			if utf8.RuneCountInString(id) == 1 && !strings.HasPrefix(id, "<") && len(a.input.Text) < 1000 {
				r, _ := utf8.DecodeRuneInString(id)
				a.input.InsertRune(r)
			}
		}
		return false
	}
	switch id {
	case "q":
		return true
	case "<Tab>":
		a.composing = true
		a.status = "Chat active. Enter sends your message; Escape returns to the files."
	case "<Enter>":
		a.activate()
	case "<Backspace>", "<Escape>":
		a.upDir()
	case "<Space>", " ":
		a.toggleFlag(a.cursor)
	case "d", "D":
		a.downloadFlagged()
	case "j", "<Down>":
		a.move(1)
	case "k", "<Up>":
		a.move(-1)
	case "<PageDown>":
		a.move(max(1, a.layout.fileRows-1))
	case "<PageUp>":
		a.move(-max(1, a.layout.fileRows-1))
	case "<Home>":
		a.cursor = 0
		a.keepVisible()
	case "<End>":
		a.cursor = max(0, len(a.entries)-1)
		a.keepVisible()
	case "r":
		if a.client != nil {
			if err := a.client.Send(wire.Message{Type: "list"}); err != nil {
				a.status = err.Error()
			}
		}
	}
	return false
}

func (a *bbs) mouse(id string, x, y int) {
	a.mouseAt(id, x, y, time.Now())
}

// mouseAt is shared by the real event loop and deterministic click tests.
// Gotui reports down and release separately; only distinct completed clicks
// can activate a row. Clicking a different row or scrolling resets the pair.
func (a *bbs) mouseAt(id string, x, y int, now time.Time) {
	if id == "<MouseRelease>" {
		a.heldMouse = ""
		return
	}
	if id == "<MouseLeft>" || id == "<MouseRight>" {
		if a.heldMouse == id {
			return
		}
		a.heldMouse = id
	}
	if id != "<MouseLeft>" {
		a.lastClick = clickStamp{}
	}
	if id == "<MouseLeft>" {
		for i, button := range a.layout.buttons {
			if button.contains(x, y) {
				a.lastClick = clickStamp{}
				switch i {
				case 0:
					a.downloadFlagged()
				case 1:
					a.composing = true
					a.status = "Chat active. Enter sends; Escape returns to files."
				case 2:
					a.leaveChat()
				}
				return
			}
		}
		if a.layout.prompt.contains(x, y) {
			a.lastClick = clickStamp{}
			a.composing = true
			return
		}
	}
	if !a.layout.filesVisible || !a.layout.files.contains(x, y) {
		a.lastClick = clickStamp{}
		return
	}
	if id == "<MouseWheelUp>" {
		a.scrollBy(-3)
		return
	}
	if id == "<MouseWheelDown>" {
		a.scrollBy(3)
		return
	}
	if id != "<MouseLeft>" && id != "<MouseRight>" {
		return
	}
	if x <= a.layout.files.left || x >= a.layout.files.right-1 ||
		y <= a.layout.files.top || y >= a.layout.files.bottom-1 {
		a.lastClick = clickStamp{}
		return
	}
	index := a.scroll + y - (a.layout.files.top + 1)
	if index < 0 || index >= len(a.entries) {
		a.lastClick = clickStamp{}
		return
	}
	a.cursor = index
	e := a.entries[index]
	if id == "<MouseRight>" {
		a.toggleFlag(index)
		return
	}
	name := e.file.Name
	if e.parent {
		name = ".."
	}
	if a.lastClick.name == name && a.lastClick.dir == a.dir &&
		!a.lastClick.at.IsZero() && now.Sub(a.lastClick.at) >= 0 &&
		now.Sub(a.lastClick.at) <= doubleClickInterval {
		a.lastClick = clickStamp{}
		a.activate()
		return
	}
	a.lastClick = clickStamp{name: name, dir: a.dir, at: now}
}

func (a *bbs) leaveChat() {
	a.composing = false
	a.input.Text = ""
	a.input.Cursor = 0
}
func (a *bbs) move(amount int) {
	if len(a.entries) == 0 {
		return
	}
	a.cursor = min(len(a.entries)-1, max(0, a.cursor+amount))
	a.keepVisible()
}
func (a *bbs) keepVisible() {
	if len(a.entries) == 0 {
		a.cursor, a.scroll = 0, 0
		return
	}
	rows := max(1, a.layout.fileRows)
	a.cursor = min(len(a.entries)-1, max(0, a.cursor))
	if a.cursor < a.scroll {
		a.scroll = a.cursor
	} else if a.cursor >= a.scroll+rows {
		a.scroll = a.cursor - rows + 1
	}
	a.scroll = min(max(0, len(a.entries)-rows), max(0, a.scroll))
}
func (a *bbs) scrollBy(n int) {
	rows := max(1, a.layout.fileRows)
	maxStart := max(0, len(a.entries)-rows)
	a.scroll = min(maxStart, max(0, a.scroll+n))
	if len(a.entries) > 0 {
		a.cursor = min(len(a.entries)-1, max(a.scroll, min(a.cursor, a.scroll+rows-1)))
	}
}

// includedByDirectory reports an ancestor selection without counting the
// directory itself. Child rows visibly inherit a tagged parent's selection.
func (a *bbs) includedByDirectory(name string) bool {
	for dir := range a.flaggedDirs {
		if strings.HasPrefix(name, dir+"/") {
			return true
		}
	}
	return false
}

func (a *bbs) ensureDirFlags() {
	if a.flaggedDirs == nil {
		a.flaggedDirs = make(map[string]bool)
	}
}

func (a *bbs) tagCount() int {
	return len(a.flagged) + len(a.flaggedDirs)
}

func (a *bbs) tagged(e browserEntry) bool {
	if e.parent {
		return false
	}
	return a.includedByDirectory(e.file.Name) ||
		(e.file.IsDir && a.flaggedDirs[e.file.Name]) ||
		(!e.file.IsDir && a.flagged[e.file.ID])
}

func (a *bbs) toggleFlag(index int) {
	if a.busy {
		a.status = "Finish the current batch before changing tags"
		return
	}
	if index < 0 || index >= len(a.entries) || a.entries[index].parent {
		a.status = "Select a file or directory to tag"
		return
	}
	e := a.entries[index]
	if a.includedByDirectory(e.file.Name) {
		a.status = "Already included by a tagged parent directory; untag the parent first"
		return
	}
	if e.file.IsDir {
		a.ensureDirFlags()
		if a.flaggedDirs[e.file.Name] {
			delete(a.flaggedDirs, e.file.Name)
		} else {
			a.flaggedDirs[e.file.Name] = true
		}
	} else {
		if a.flagged == nil {
			a.flagged = make(map[string]bool)
		}
		if a.flagged[e.file.ID] {
			delete(a.flagged, e.file.ID)
		} else {
			a.flagged[e.file.ID] = true
		}
	}
	a.status = fmt.Sprintf("%d tag(s). D or [ Download All ] queues tagged files and whole folders.", a.tagCount())
}

// queued resolves selected directories against the current server snapshot,
// preserving empty directories and deduplicating overlapping folder/file tags.
// Parents are scheduled before their children; individual files retain their
// remote relative paths so the receiver recreates the tree.
func (a *bbs) queued() []wire.File {
	items := make(map[string]wire.File)
	for _, f := range a.files {
		if (f.IsDir && (a.flaggedDirs[f.Name] || a.includedByDirectory(f.Name))) ||
			(!f.IsDir && (a.flagged[f.ID] || a.includedByDirectory(f.Name))) {
			items[f.Name] = f
		}
	}
	for dir := range a.flaggedDirs {
		// Also support directories synthesized for older flat-listing peers.
		if _, ok := items[dir]; !ok {
			items[dir] = wire.File{Name: dir, IsDir: true}
		}
	}
	queue := make([]wire.File, 0, len(items))
	for _, f := range items {
		queue = append(queue, f)
	}
	sort.Slice(queue, func(i, j int) bool {
		left, right := queue[i].Name, queue[j].Name
		if strings.EqualFold(left, right) {
			return left < right
		}
		return strings.ToLower(left) < strings.ToLower(right)
	})
	return queue
}
func (a *bbs) fileCount() int {
	n := 0
	for _, f := range a.files {
		if !f.IsDir {
			n++
		}
	}
	return n
}
func (a *bbs) downloadFlagged() {
	queue := a.queued()
	if len(queue) == 0 {
		a.status = "Tag files or folders with Space or right-click first"
		return
	}
	a.startDownloads(queue, true)
}

// Both Enter on an individual file and D on flagged files use the same TLS
// transfer worker, progress reporting, SHA-256 checks and resume mechanism.
func (a *bbs) startDownloads(queue []wire.File, fromTags bool) {
	if a.busy {
		a.status = "A download is already in progress"
		return
	}
	if len(queue) == 0 {
		return
	}
	if a.client == nil {
		a.status = "No active connection"
		return
	}
	a.busy = true
	var batchBytes int64
	for _, item := range queue {
		if !item.IsDir {
			batchBytes += item.Size
		}
	}
	a.download = downloadStatus{
		active: true, name: queue[0].Name, total: queue[0].Size,
		batchBytes: batchBytes, index: 1, count: len(queue),
		message: "Connecting over TLS 1.3...",
	}
	a.status = fmt.Sprintf("Transferring %d item(s); interrupted files can resume.", len(queue))
	taggedDirs := []string{}
	if fromTags {
		for dir := range a.flaggedDirs {
			taggedDirs = append(taggedDirs, dir)
		}
	}
	go func() {
		result := batchResult{total: len(queue), taggedDirs: taggedDirs}
		var completedBytes int64
		for i, item := range queue {
			send := func(p transfer.Progress) {
				e := downloadEvent{Progress: p, index: i + 1, completedBytes: completedBytes}
				select {
				case a.progress <- e:
				default:
				}
			}
			var err error
			if item.IsDir {
				send(transfer.Progress{Name: item.Name, Message: "Creating directory..."})
				err = transfer.EnsureDirectory(a.downloads, item.Name)
				if err == nil {
					result.dirsDone++
				}
			} else {
				send(transfer.Progress{Name: item.Name, Total: item.Size, Message: "Connecting over TLS 1.3..."})
				err = transfer.Download(a.ctx, a.client.Invitation, item, a.downloads, send)
				if err == nil {
					completedBytes += item.Size
					result.filesDone++
					result.succeeded = append(result.succeeded, item.ID)
				}
			}
			if err != nil {
				result.failed, result.err = item.Name, err
				result.remaining = append([]wire.File(nil), queue[i:]...)
				break
			}
			result.lastName, result.lastSize = item.Name, item.Size
		}
		select {
		case a.completed <- result:
		case <-a.ctx.Done():
		}
	}()
}

func (a *bbs) draw() {
	width, height := ui.TerminalDimensions()
	if width < 40 || height < 16 {
		ui.Clear()
		p := widgets.NewParagraph()
		p.Text = "Laserline DX needs at least 40x16 terminal cells"
		styleLaserlinePanel(&p.Block, laserlineViolet)
		p.TextStyle = ui.NewStyle(laserlineWhite, laserlineInk)
		p.SetRect(0, 0, max(width, 1), max(height, 1))
		ui.Render(laserlineBackdrop(max(width, 1), max(height, 1)), p)
		return
	}
	a.layout = makeLayout(width, height, a.composing)
	a.keepVisible()
	l := a.layout
	header := widgets.NewParagraph()
	header.Title = " ✦ LASERLINE DX ✦ // ENCRYPTED FILE + CHAT LINK "
	header.Text = fmt.Sprintf("  ◆ TLS 1.3  //  USER: %s  //  ONLINE: %d  //  FILES: %d  //  TAGS: %d",
		safe(a.nick), a.people, a.fileCount(), a.tagCount())
	styleLaserlinePanel(&header.Block, laserlineViolet)
	header.TextStyle = ui.NewStyle(laserlineCyan, laserlineInk)
	header.SetRect(0, 0, width, 3)

	foot := widgets.NewParagraph()
	styleLaserlinePanel(&foot.Block, laserlineViolet)
	foot.Title = " SPACE: TAG  D: BATCH  ENTER/2-CLICK: OPEN/GET  TAB: CHAT  ESC: BACK "
	foot.Text = actionBar
	foot.TextStyle = ui.NewStyle(laserlineCyan, laserlineInk)
	foot.SetRect(0, height-4, width, height-1)
	// A one-cell status strip needs a dedicated drawable: gotui Paragraph
	// reserves an inner border even when Border is disabled.
	status := laserlineStatus(a.status, width, height)

	transferPanel := widgets.NewParagraph()
	transferPanel.Title = " TRANSFER STATUS "
	styleLaserlinePanel(&transferPanel.Block, laserlinePink)
	transferPanel.TextStyle = ui.NewStyle(laserlineCyan, laserlineInk)
	transferPanel.WrapText = false
	transferPanel.Text = downloadPanelText(a.download, l.transfer.right-l.transfer.left-2, l.transfer.bottom-l.transfer.top-2)
	transferPanel.SetRect(l.transfer.left, l.transfer.top, l.transfer.right, l.transfer.bottom)

	list := widgets.NewList()
	start := a.scroll
	end := min(len(a.entries), start+l.fileRows)
	where := "/" + a.dir
	list.Title = fmt.Sprintf(" FILE VAULT %s // %d-%d/%d | %d tags ", safe(where), visibleStart(start, end), end, len(a.entries), a.tagCount())
	styleLaserlinePanel(&list.Block, laserlineCyan)
	list.TextStyle = ui.NewStyle(laserlineWhite, laserlineInk)
	list.WrapText = false
	list.Rows = make([]string, 0, max(1, end-start))
	for i := start; i < end; i++ {
		e := a.entries[i]
		f := e.file
		marker := "  "
		if i == a.cursor {
			marker = "> "
		}
		if e.parent {
			list.Rows = append(list.Rows, marker+"    [..] Parent directory")
			continue
		}
		if f.IsDir {
			flag := "[ ]"
			if a.tagged(e) {
				flag = "[x]"
			}
			list.Rows = append(list.Rows, marker+flag+" ◆ [DIR] "+safe(path.Base(f.Name))+"/")
			continue
		}
		flag := "[ ]"
		if a.tagged(e) {
			flag = "[x]"
		}
		list.Rows = append(list.Rows, fmt.Sprintf("%s%s %s  (%s)", marker, flag, safe(path.Base(f.Name)), formatBytes(f.Size)))
	}
	if len(list.Rows) == 0 {
		list.Rows = []string{"  This directory is empty. Press r to refresh."}
	}
	list.SelectedRow = max(0, a.cursor-a.scroll)
	list.SelectedStyle = ui.NewStyle(laserlineGold, laserlineViolet, ui.ModifierBold)
	list.SetRect(l.files.left, l.files.top, l.files.right, l.files.bottom)

	chat := widgets.NewParagraph()
	chat.Title = " SIGNAL // LIVE ENCRYPTED CHAT "
	styleLaserlinePanel(&chat.Block, laserlinePink)
	chat.TextStyle = ui.NewStyle(laserlineWhite, laserlineInk)
	chat.WrapText = true
	chatText := a.chat
	maxRows := l.fileRows
	if len(chatText) > maxRows {
		chatText = chatText[len(chatText)-maxRows:]
	}
	chat.Text = strings.Join(chatText, "\n")
	chat.SetRect(l.chat.left, l.chat.top, l.chat.right, l.chat.bottom)

	var prompt ui.Drawable
	if a.composing {
		a.input.Title = " CHAT / Enter: send, Escape: files "
		styleLaserlinePanel(&a.input.Block, laserlinePink)
		a.input.TextStyle = ui.NewStyle(laserlineWhite, laserlineInk)
		a.input.CursorStyle = ui.NewStyle(laserlineInk, laserlineGold)
		a.input.SetRect(l.prompt.left, l.prompt.top, l.prompt.right, l.prompt.bottom)
		prompt = a.input
	} else {
		p := widgets.NewParagraph()
		p.Title = " CHAT PROMPT "
		styleLaserlinePanel(&p.Block, laserlinePink)
		p.TextStyle = ui.NewStyle(laserlineCyan, laserlineInk)
		p.Text = " Press Tab or click here to write a message."
		p.SetRect(l.prompt.left, l.prompt.top, l.prompt.right, l.prompt.bottom)
		prompt = p
	}
	ui.Clear()
	// Every cell begins with the Laserline background, including blank pane edges
	// and the bottom status row. Text and borders also use opaque backgrounds
	// so rendering over the canvas cannot reveal Alacritty's default colour.
	backdrop := laserlineBackdrop(width, height)
	if l.wide {
		ui.Render(backdrop, header, list, chat, transferPanel, prompt, foot, status)
	} else if a.composing {
		ui.Render(backdrop, header, chat, transferPanel, prompt, foot, status)
	} else {
		ui.Render(backdrop, header, list, transferPanel, prompt, foot, status)
	}
}
func visibleStart(start, end int) int {
	if end == 0 {
		return 0
	}
	return start + 1
}
func safe(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '[' {
			return '('
		}
		if r == ']' {
			return ')'
		}
		if r < 32 || r == 127 {
			return ' '
		}
		return r
	}, s)
}
func formatBytes(n int64) string {
	v := float64(n)
	for _, unit := range []string{"B", "KiB", "MiB", "GiB", "TiB"} {
		if v < 1024 || unit == "TiB" {
			return fmt.Sprintf("%.1f %s", v, unit)
		}
		v /= 1024
	}
	return "?"
}
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// downloadPanelText formats a fixed-height status window. At 40 columns or on
// short terminals it shortens intelligently instead of allowing wrapped lines
// to obscure the progress bar or overflow into the file/chat panes.
func downloadPanelText(d downloadStatus, width, rows int) string {
	if width < 1 || rows < 1 {
		return ""
	}
	if d.count == 0 {
		return fitLine(" Idle - flag files with Space (or right-click), then press D.", width)
	}
	filePct := percentage(d.bytes, d.total)
	batchDone := min64(d.batchBytes, max64(0, d.completedBytes+d.bytes))
	batchPct := percentage(batchDone, d.batchBytes)
	label := "Downloading"
	if !d.active {
		if strings.HasPrefix(d.message, "Paused:") {
			label = "Paused"
		} else {
			label = "Complete"
		}
	}
	lines := []string{
		fmt.Sprintf(" %s [%d/%d] %s", label, d.index, d.count, safe(d.name)),
		fmt.Sprintf(" File %3d%% %s", filePct, progressBar(d.bytes, d.total, max(8, min(42, width-14)))),
		fmt.Sprintf(" Batch %3d%% %s", batchPct, progressBar(batchDone, d.batchBytes, max(8, min(42, width-15)))),
		fmt.Sprintf(" %s / %s  |  %s/s  |  attempt %d", formatBytes(d.bytes), formatBytes(d.total), formatSpeed(d.speed), d.attempt),
		" " + safe(d.message),
	}
	// One-line and two-line formats keep a progress bar visible in the
	// smallest supported windows (16-19 rows tall).
	if rows == 1 {
		return fitLine(fmt.Sprintf(" %s %d/%d %3d%% %s", label, d.index, d.count, filePct,
			progressBar(d.bytes, d.total, max(6, min(16, width-25)))), width)
	}
	if rows == 2 {
		lines = []string{lines[0], lines[1]}
	} else if rows == 3 {
		lines = []string{lines[0], lines[1], lines[3]}
	} else if rows == 4 {
		lines = lines[:4]
	}
	for i, line := range lines {
		lines[i] = fitLine(line, width)
	}
	return strings.Join(lines, "\n")
}

func percentage(done, total int64) int {
	if total <= 0 {
		return 100
	}
	// Float conversion avoids overflow when multiplying very large file sizes.
	return int(100 * (float64(min64(total, max64(0, done))) / float64(total)))
}
func progressBar(done, total int64, width int) string {
	width = max(1, width)
	fill := percentage(done, total) * width / 100
	return "[" + strings.Repeat("#", fill) + strings.Repeat("-", width-fill) + "]"
}
func formatSpeed(bytesPerSecond float64) string {
	if bytesPerSecond <= 0 {
		return "0 B"
	}
	return formatBytes(int64(bytesPerSecond))
}
func fitLine(line string, width int) string {
	characters := []rune(line)
	if len(characters) <= width {
		return line
	}
	if width <= 1 {
		return string(characters[:width])
	}
	return string(characters[:width-1]) + "…"
}
func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
