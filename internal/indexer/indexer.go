package indexer

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/prasenjeet-symon/ogcode/internal/agent"
	"github.com/prasenjeet-symon/ogcode/internal/docindex"
	"github.com/prasenjeet-symon/ogcode/internal/gitignore"
	"github.com/prasenjeet-symon/ogcode/internal/session"
	"golang.org/x/sync/errgroup"
)

// Indexer scans a workspace directory for PDF and text/code files and runs the
// IndexAgent on batches of documents to produce semantic labels per page.
type Indexer struct {
	dir      string
	model    string // optional model override for the IndexAgent
	excludes []string
	// gitignore skips whatever the repository already declares as noise. It is
	// always consulted, not opt-in: a .gitignore is the one place a project has
	// already written down which files are generated, vendored or private, and
	// indexing them anyway spends real time and tokens producing labels for a
	// build directory nobody will search.
	gitignore        *gitignore.Matcher
	docStore         *docindex.Store
	loopRunner       *agent.LoopRunner
	maxConcurrent    int // number of parallel indexing sessions (Solution 2)
	maxKeywordsBatch int // max keyword count per LLM batch (Solution 1)
	progress         *ProgressTracker
}

// ProgressTracker tracks indexing progress and publishes events via the bus.
type ProgressTracker struct {
	Total     atomic.Int32
	Completed atomic.Int32
	Failed    atomic.Int32
	Current   atomic.Value // stores string
}

// New creates a new Indexer. Pass an empty model to use the runner's default.
func New(dir string, docStore *docindex.Store, lr *agent.LoopRunner) *Indexer {
	return &Indexer{
		dir:              dir,
		gitignore:        gitignore.New(dir),
		docStore:         docStore,
		loopRunner:       lr,
		maxConcurrent:    5,    // default: 5 parallel workers
		maxKeywordsBatch: 3000, // default: ~3000 keywords per batch
		progress:         &ProgressTracker{},
	}
}

// WithModel sets the model override for sessions created by the indexer.
func (idx *Indexer) WithModel(model string) *Indexer {
	idx.model = model
	return idx
}

// WithExcludes sets additional patterns to skip during the directory walk.
// Patterns are matched against directory names and file basenames using filepath.Match.
func (idx *Indexer) WithExcludes(patterns []string) *Indexer {
	idx.excludes = patterns
	return idx
}

// WithMaxConcurrent sets the number of parallel indexing sessions.
func (idx *Indexer) WithMaxConcurrent(n int) *Indexer {
	if n > 0 {
		idx.maxConcurrent = n
	}
	return idx
}

// WithMaxKeywordsBatch sets the maximum number of keywords per LLM batch.
func (idx *Indexer) WithMaxKeywordsBatch(n int) *Indexer {
	if n > 0 {
		idx.maxKeywordsBatch = n
	}
	return idx
}

// Progress returns the progress tracker for external monitoring.
func (idx *Indexer) Progress() *ProgressTracker {
	return idx.progress
}

// isExcluded reports whether a file or directory name matches any user-configured exclude pattern.
func (idx *Indexer) isExcluded(name string) bool {
	for _, pattern := range idx.excludes {
		if pattern == name {
			return true
		}
		if matched, _ := filepath.Match(pattern, name); matched {
			return true
		}
	}
	return false
}

// collectFiles walks the workspace and returns every file worth indexing.
//
// What is excluded is decided by the repository, not by this package. There is
// no built-in list of directories to skip: a hardcoded one is a second, unwritten
// exclusion policy that a project cannot see, cannot change, and does not agree
// with — it hides files a project chose to track and, being invisible, gives no
// hint about why they never turn up in a search. .gitignore is where a project
// has already written this down, so .gitignore is what decides, alongside the
// excludes the user configures directly.
//
// An ignored directory is pruned rather than walked into — which is faster, and
// is what git does, since a directory excluded from above cannot have its
// contents re-included from below.
func (idx *Indexer) collectFiles() ([]string, error) {
	var allFiles []string
	err := filepath.WalkDir(idx.dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries, keep walking
		}
		if d.IsDir() {
			if path != idx.dir {
				if idx.isExcluded(d.Name()) {
					return filepath.SkipDir
				}
				if idx.gitignore.Match(path, true) {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if idx.isExcluded(filepath.Base(path)) {
			return nil
		}
		if idx.gitignore.Match(path, false) {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext == ".pdf" || ext == ".docx" || IsTextFile(ext) {
			allFiles = append(allFiles, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return allFiles, nil
}

// docItem holds a file path and its extracted page corpora, ready for batching.
type docItem struct {
	path         string
	corpora      []PageCorpus
	isPDF        bool
	isDocx       bool
	keywordCount int // total keywords across all pages
}

// Run scans dir recursively for PDF and text/code files and indexes each one.
// It groups small text files into batches (Solution 1), runs batches in parallel
// (Solution 2), and publishes progress events (Solution 5).
func (idx *Indexer) Run(ctx context.Context) error {
	// Phase 1: Walk, filter, dedup, and extract text.
	allFiles, walkErr := idx.collectFiles()
	if walkErr != nil {
		return fmt.Errorf("walk files: %w", walkErr)
	}

	if len(allFiles) == 0 {
		slog.Info("no indexable files found", "dir", idx.dir)
		// Still purge stale entries — every previously-indexed file is now gone.
		if err := idx.purgeDeletedDocs(ctx, allFiles); err != nil {
			slog.Warn("purge deleted docs failed, continuing", "err", err)
		}
		return nil
	}

	slog.Info("found indexable files", "count", len(allFiles))

	// Log file type breakdown for debugging.
	var pdfCount, docxCount, textCount int
	for _, f := range allFiles {
		ext := strings.ToLower(filepath.Ext(f))
		switch ext {
		case ".pdf":
			pdfCount++
		case ".docx":
			docxCount++
		default:
			textCount++
		}
	}
	slog.Info("file type breakdown", "pdf", pdfCount, "docx", docxCount, "text", textCount)

	// Purge stale entries: remove index rows for files that no longer exist on disk.
	if err := idx.purgeDeletedDocs(ctx, allFiles); err != nil {
		slog.Warn("purge deleted docs failed, continuing", "err", err)
	}

	// Filter out already-indexed documents.
	var toIndex []string
	for _, filePath := range allFiles {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		indexed, err := idx.docStore.IsDocIndexed(filePath)
		if err != nil {
			slog.Warn("could not check index status, skipping", "path", filePath, "err", err)
			continue
		}
		if indexed {
			slog.Info("skipping already-indexed document", "path", filePath)
			continue
		}
		toIndex = append(toIndex, filePath)
	}

	if len(toIndex) == 0 {
		slog.Info("all documents already indexed", "dir", idx.dir)
		return nil
	}

	// Extract text and build corpora for all files to index.
	var items []docItem
	for _, filePath := range toIndex {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		isPDF := strings.ToLower(filepath.Ext(filePath)) == ".pdf"
		isDocx := strings.ToLower(filepath.Ext(filePath)) == ".docx"
		fileType := "text"
		if isPDF {
			fileType = "pdf"
		} else if isDocx {
			fileType = "docx"
		}
		var pages []PageText
		var err error
		if isPDF {
			pages, err = ExtractPages(filePath)
		} else if isDocx {
			pages, err = ExtractDocxPages(filePath)
		} else {
			pages, err = ExtractTextFile(filePath)
		}
		if err != nil {
			slog.Warn("failed to extract text, skipping", "path", filePath, "type", fileType, "err", err)
			continue
		}
		if len(pages) == 0 {
			slog.Info("no pages found, skipping", "path", filePath, "type", fileType)
			continue
		}
		slog.Info("extracted pages", "path", filePath, "type", fileType, "pages", len(pages))
		corpora := BuildCorpora(pages)
		kwCount := 0
		for _, c := range corpora {
			kwCount += len(c.Keywords)
		}
		items = append(items, docItem{
			path:         filePath,
			corpora:      corpora,
			isPDF:        isPDF,
			isDocx:       isDocx,
			keywordCount: kwCount,
		})
	}

	if len(items) == 0 {
		return nil
	}

	// Phase 2: Assemble batches and run in parallel.
	idx.progress.Total.Store(int32(len(items)))
	idx.publishProgress(ctx, "indexing")

	batches := idx.assembleBatches(items)
	slog.Info("assembled batches", "totalFiles", len(items), "batches", len(batches))

	g, gCtx := errgroup.WithContext(ctx)
	g.SetLimit(idx.maxConcurrent)

	for _, batch := range batches {
		batch := batch // capture
		g.Go(func() error {
			return idx.processBatch(gCtx, batch)
		})
	}

	if err := g.Wait(); err != nil {
		return fmt.Errorf("parallel indexing: %w", err)
	}

	idx.publishProgress(ctx, "done")
	slog.Info("indexing complete", "dir", idx.dir, "total", len(items))
	return nil
}

// purgeDeletedDocs removes index entries for documents that were previously
// indexed but no longer exist on disk. It compares the set of indexed doc
// paths (under idx.dir) against the current filesystem walk (allFiles) and
// deletes any indexed path that is not present in the walk. This keeps the
// index in sync when files are deleted between runs.
func (idx *Indexer) purgeDeletedDocs(ctx context.Context, allFiles []string) error {
	if idx.docStore == nil {
		return nil
	}

	// Build a set of current file paths for O(1) lookups.
	currentFiles := make(map[string]struct{}, len(allFiles))
	for _, p := range allFiles {
		currentFiles[p] = struct{}{}
	}

	// Retrieve every indexed doc path under idx.dir.
	indexedPaths, err := idx.docStore.ListDocPaths(idx.dir)
	if err != nil {
		return fmt.Errorf("list indexed doc paths: %w", err)
	}

	var deleted int
	for _, docPath := range indexedPaths {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if _, exists := currentFiles[docPath]; exists {
			continue
		}
		if err := idx.docStore.DeleteByDoc(docPath); err != nil {
			slog.Warn("failed to purge stale index entry", "path", docPath, "err", err)
			continue
		}
		deleted++
		slog.Info("purged stale index entry for deleted file", "path", docPath)
	}
	if deleted > 0 {
		slog.Info("purged stale index entries", "count", deleted)
	}
	return nil
}

// batch represents a group of documents to be sent to a single LLM session,
// or a single document (PDF or DOCX) that needs its own session due to size.
type batch struct {
	// For multi-file batches (text/code files): multiple docItems
	items []docItem
	// For single-document batches (PDF or DOCX): one document
	isPDF  bool
	isDocx bool
}

// assembleBatches groups documents into batches. PDFs and DOCX files always get
// their own batch (they can be very large). Small text/code files are packed
// together based on total keyword count to minimize LLM sessions while staying
// within context limits.
func (idx *Indexer) assembleBatches(items []docItem) []*batch {
	var batches []*batch

	// Separate multi-page documents (PDF, DOCX) from text files.
	var multiPageDocs []docItem
	var textFiles []docItem
	for _, item := range items {
		if item.isPDF || item.isDocx {
			multiPageDocs = append(multiPageDocs, item)
		} else {
			textFiles = append(textFiles, item)
		}
	}

	// Each multi-page document gets its own batch (it may have hundreds of pages).
	for _, doc := range multiPageDocs {
		// Split large documents into page-range batches using the existing batchSize constant.
		for start := 0; start < len(doc.corpora); start += batchSize {
			end := start + batchSize
			if end > len(doc.corpora) {
				end = len(doc.corpora)
			}
			batches = append(batches, &batch{
				items: []docItem{{
					path:         doc.path,
					corpora:      doc.corpora[start:end],
					isPDF:        doc.isPDF,
					isDocx:       doc.isDocx,
					keywordCount: doc.keywordCount,
				}},
				isPDF:  doc.isPDF,
				isDocx: doc.isDocx,
			})
		}
	}

	// Group text files into keyword-count-bounded batches (Solution 1).
	var currentBatch []docItem
	var currentKW int
	for _, item := range textFiles {
		if len(currentBatch) > 0 && currentKW+item.keywordCount > idx.maxKeywordsBatch {
			// Flush current batch
			batches = append(batches, &batch{items: currentBatch})
			currentBatch = nil
			currentKW = 0
		}
		currentBatch = append(currentBatch, item)
		currentKW += item.keywordCount
	}
	if len(currentBatch) > 0 {
		batches = append(batches, &batch{items: currentBatch})
	}

	return batches
}

// processBatch handles a single batch — either a multi-file text batch or
// a single-PDF batch. It builds the prompt, creates a session, and runs the
// IndexAgent loop.
func (idx *Indexer) processBatch(ctx context.Context, b *batch) error {
	// Build the user prompt text from all documents in the batch.
	var userText string
	var title string

	if b.isPDF || b.isDocx {
		// Single multi-page document (PDF or DOCX): use the original per-file format.
		item := b.items[0]
		var sb strings.Builder
		for _, c := range item.corpora {
			kw := c.Keywords
			if len(kw) == 0 {
				kw = []string{"(empty)"}
			}
			fmt.Fprintf(&sb, "%d: %s\n", c.PageNum, strings.Join(kw, ","))
		}
		userText = fmt.Sprintf("Index this document: %s\n\nPages (page_num: keywords):\n%s",
			item.path, sb.String())
		title = fmt.Sprintf("Index: %s (p%d-%d)",
			filepath.Base(item.path),
			b.items[0].corpora[0].PageNum,
			b.items[0].corpora[len(b.items[0].corpora)-1].PageNum)
	} else if len(b.items) == 1 {
		// Single text file: simple format.
		item := b.items[0]
		var sb strings.Builder
		for _, c := range item.corpora {
			kw := c.Keywords
			if len(kw) == 0 {
				kw = []string{"(empty)"}
			}
			fmt.Fprintf(&sb, "%d: %s\n", c.PageNum, strings.Join(kw, ","))
		}
		userText = fmt.Sprintf("Index this document: %s\n\nPages (page_num: keywords):\n%s",
			item.path, sb.String())
		title = fmt.Sprintf("Index: %s", filepath.Base(item.path))
	} else {
		// Multi-file batch: format all documents together.
		var sb strings.Builder
		sb.WriteString("Index the following documents. Call submit_doc_index once per document.\n\n")
		for _, item := range b.items {
			sb.WriteString(fmt.Sprintf("Document: %s\n", item.path))
			for _, c := range item.corpora {
				kw := c.Keywords
				if len(kw) == 0 {
					kw = []string{"(empty)"}
				}
				fmt.Fprintf(&sb, "%d: %s\n", c.PageNum, strings.Join(kw, ","))
			}
			sb.WriteString("\n")
		}
		userText = sb.String()
		title = fmt.Sprintf("Index batch: %d files", len(b.items))
	}

	slog.Info("processing batch", "title", title, "files", len(b.items), "isPDF", b.isPDF, "isDocx", b.isDocx)

	sess := &session.Session{
		ID:          session.NewSessionID(),
		ProjectID:   idx.dir,
		Directory:   idx.dir,
		Title:       title,
		Model:       idx.model,
		SessionType: "index",
		CreatedAt:   session.Now(),
		UpdatedAt:   session.Now(),
	}
	if err := idx.loopRunner.Store.Create(sess); err != nil {
		return fmt.Errorf("create session: %w", err)
	}

	userMsg := &session.MessageInfo{
		ID:        session.NewMessageID(),
		SessionID: sess.ID,
		Role:      session.RoleUser,
		Agent:     "index",
		CreatedAt: session.Now(),
	}
	if err := idx.loopRunner.Store.CreateMessage(userMsg); err != nil {
		return fmt.Errorf("create user message: %w", err)
	}
	textData, _ := json.Marshal(session.TextPartData{Text: userText})
	userPart := &session.Part{
		ID:        session.NewPartID(),
		MessageID: userMsg.ID,
		SessionID: sess.ID,
		Type:      session.PartText,
		Data:      textData,
		CreatedAt: session.Now(),
		UpdatedAt: session.Now(),
	}
	if err := idx.loopRunner.Store.CreatePart(userPart); err != nil {
		return fmt.Errorf("create user part: %w", err)
	}

	if err := idx.loopRunner.RunLoop(ctx, sess.ID, "index", 0, 0); err != nil {
		// Log but don't fail the entire indexing run — other batches can still succeed.
		slog.Warn("index agent loop failed", "title", title, "err", err)
		for range b.items {
			idx.progress.Failed.Add(1)
		}
	} else {
		completed := idx.progress.Completed.Add(int32(len(b.items)))
		slog.Info("batch completed", "title", title, "files", len(b.items),
			"totalCompleted", completed)
	}

	idx.publishProgress(ctx, "indexing")
	return nil
}

// publishProgress emits a progress event on the bus if available.
func (idx *Indexer) publishProgress(ctx context.Context, phase string) {
	if idx.loopRunner.Bus == nil {
		return
	}
	idx.loopRunner.Bus.Publish("docindex.progress", map[string]any{
		"directory": idx.dir,
		"phase":     phase,
		"total":     idx.progress.Total.Load(),
		"completed": idx.progress.Completed.Load(),
		"failed":    idx.progress.Failed.Load(),
	})
}

// batchSize is the number of pages sent to the IndexAgent per session for PDFs.
// Compact keyword format keeps each batch well under 50KB.
const batchSize = 100

// IndexDocument extracts text from a PDF, DOCX, or text/code file, builds keyword
// corpora, then runs the IndexAgent in batches to produce labels.
// This is the single-file entry point — it delegates to the batch infrastructure.
func (idx *Indexer) IndexDocument(ctx context.Context, filePath string) error {
	slog.Info("indexing document", "path", filePath)

	var pages []PageText
	var err error
	isPDF := strings.ToLower(filepath.Ext(filePath)) == ".pdf"
	isDocx := strings.ToLower(filepath.Ext(filePath)) == ".docx"
	if isPDF {
		pages, err = ExtractPages(filePath)
	} else if isDocx {
		pages, err = ExtractDocxPages(filePath)
	} else {
		pages, err = ExtractTextFile(filePath)
	}
	if err != nil {
		return fmt.Errorf("extract pages: %w", err)
	}
	if len(pages) == 0 {
		slog.Info("no pages found", "path", filePath)
		return nil
	}

	corpora := BuildCorpora(pages)
	kwCount := 0
	for _, c := range corpora {
		kwCount += len(c.Keywords)
	}

	item := docItem{
		path:         filePath,
		corpora:      corpora,
		isPDF:        isPDF,
		isDocx:       isDocx,
		keywordCount: kwCount,
	}

	batches := idx.assembleBatches([]docItem{item})
	for _, b := range batches {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := idx.processBatch(ctx, b); err != nil {
			slog.Warn("batch failed", "doc", filePath, "err", err)
		}
	}

	slog.Info("document indexed", "path", filePath)
	return nil
}

// Plan is what a run would do, worked out without doing any of it.
type Plan struct {
	Total   int `json:"total"`   // indexable files currently on disk
	Pending int `json:"pending"` // not yet indexed — what an incremental run would work through
	Indexed int `json:"indexed"` // already in the index, skipped by an incremental run
	Stale   int `json:"stale"`   // indexed but gone from disk — purged by the next run

	// Two breakdowns, because the two runs work on different sets: a rebuild
	// reads every file, an incremental run only the pending ones. A single
	// breakdown would be shown next to whichever count the dialog is leading
	// with and describe the other one.
	PDF  int `json:"pdf"`
	Docx int `json:"docx"`
	Text int `json:"text"`

	PendingPDF  int `json:"pendingPdf"`
	PendingDocx int `json:"pendingDocx"`
	PendingText int `json:"pendingText"`
}

// Preview reports what Run would do, without extracting a page or calling a model.
//
// Indexing costs money and minutes, and a button that cannot say how much of
// either before it starts is one people press blindly or avoid entirely. The
// walk here is the same walk Run does, filtered by the same excludes and the
// same .gitignore, so these are the run's own numbers rather than a guess at
// them — including the count of entries it will drop for files that no longer
// exist, which is otherwise invisible until it has already happened.
func (idx *Indexer) Preview() (*Plan, error) {
	files, err := idx.collectFiles()
	if err != nil {
		return nil, fmt.Errorf("walk files: %w", err)
	}

	plan := &Plan{Total: len(files)}
	onDisk := make(map[string]struct{}, len(files))

	for _, path := range files {
		onDisk[path] = struct{}{}
		switch strings.ToLower(filepath.Ext(path)) {
		case ".pdf":
			plan.PDF++
		case ".docx":
			plan.Docx++
		default:
			plan.Text++
		}
	}

	if idx.docStore == nil {
		plan.Pending = plan.Total
		plan.PendingPDF, plan.PendingDocx, plan.PendingText = plan.PDF, plan.Docx, plan.Text
		return plan, nil
	}

	indexed, err := idx.docStore.ListDocPaths(idx.dir)
	if err != nil {
		return nil, fmt.Errorf("list indexed doc paths: %w", err)
	}

	// One pass over what the index holds answers both questions: an indexed
	// path still on disk is one the run skips, and one that is not is an entry
	// the run purges.
	inIndex := make(map[string]struct{}, len(indexed))
	for _, path := range indexed {
		inIndex[path] = struct{}{}
		if _, exists := onDisk[path]; !exists {
			plan.Stale++
		}
	}
	for path := range onDisk {
		if _, exists := inIndex[path]; exists {
			plan.Indexed++
			continue
		}
		switch strings.ToLower(filepath.Ext(path)) {
		case ".pdf":
			plan.PendingPDF++
		case ".docx":
			plan.PendingDocx++
		default:
			plan.PendingText++
		}
	}
	plan.Pending = plan.Total - plan.Indexed
	return plan, nil
}

// FileEntry is one file in the workspace tree, with its index status.
type FileEntry struct {
	Path      string `json:"path"`
	Indexed   bool   `json:"indexed"`
	PageCount int    `json:"pageCount"`
	IndexedAt int64  `json:"indexedAt"`
}

// FileList reports every indexable file in the workspace alongside which of
// them the index already holds. The walk is the same one Run uses — the same
// excludes, the same .gitignore — so the list matches what a run would touch.
// Indexed entries that no longer exist on disk are not included; they are
// purged by the next run, not shown as files.
func (idx *Indexer) FileList() ([]FileEntry, error) {
	files, err := idx.collectFiles()
	if err != nil {
		return nil, fmt.Errorf("walk files: %w", err)
	}

	// Build a lookup of indexed doc paths → summary so each on-disk file can be
	// annotated in one pass. ListDocsSummary already deduplicates by doc_path.
	indexed := make(map[string]*docindex.DocSummary)
	if idx.docStore != nil {
		docs, err := idx.docStore.ListDocsSummary(idx.dir)
		if err != nil {
			return nil, fmt.Errorf("list indexed docs: %w", err)
		}
		for _, d := range docs {
			indexed[d.DocPath] = d
		}
	}

	out := make([]FileEntry, 0, len(files))
	for _, path := range files {
		entry := FileEntry{Path: path}
		if d, ok := indexed[path]; ok {
			entry.Indexed = true
			entry.PageCount = d.PageCount
			entry.IndexedAt = d.IndexedAt
		}
		out = append(out, entry)
	}
	return out, nil
}
