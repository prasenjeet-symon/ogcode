package tool

import "github.com/prasenjeet-symon/ogcode/internal/docindex"

// RegisterCoreTools registers the tools every entry point must offer, so the
// set cannot drift between them.
//
// The reason it is one function rather than a list repeated per entry point:
// an agent's system prompt is assembled at package init from its Tools list and
// names these tools by id — several of them, including "codebase_map" and
// "file_map", under a "Mandatory:" heading. Registry.ForAgent only offers the
// model what is registered, so an entry point that omits one turns a MUST into
// an instruction the model cannot follow. Nothing fails when that happens: no
// error is raised and no call is rejected, the agent simply explores worse, and
// the prompt's mandatory framing — which the map-before-read discipline and the
// shell-bypass rule both lean on — is taught to be negotiable.
//
// `ogcode run` shipped with eleven of these missing for exactly that reason.
//
// docIndex backs the four index-aware tools. It may be nil in tests that only
// check registration and resolution: ID and Description never touch it.
//
// What deliberately stays with the caller is anything whose dependencies are
// entry-point specific — the skill loader, MCP servers, the search backend, and
// the tools that close over the LoopRunner (task, deep_search, the recall
// pair). Those are wired where their dependencies are built.
func RegisterCoreTools(r *Registry, docIndex *docindex.Store) {
	r.Register(BashTool{})
	r.Register(ReadTool{})
	r.Register(FileMapTool{})
	r.Register(CheckSyntaxTool{})
	r.Register(WriteTool{})
	r.Register(EditTool{})
	r.Register(GlobTool{})
	r.Register(GrepTool{})
	r.Register(ViewImageTool{})
	r.Register(LatexToPdfTool{})
	r.Register(NewCompactContextTool())
	r.Register(ReadPdfPageTool{})
	r.Register(NewPdfIndexTool(docIndex))
	r.Register(ReadDocxPageTool{})
	r.Register(NewDocxIndexTool(docIndex))
	r.Register(NewProjectIndexTool(docIndex))
}
