package codemap

import (
	"strings"
	"testing"
)

const phpSource = `<?php

namespace App\Service;

use App\Contracts\Jsonable;
use App\Models\User;

const VERSION = '1.2.0';

/**
 * UserService resolves users and formats them for the API.
 */
class UserService extends BaseService implements Jsonable
{
    private const ROLE = 'admin';
    public readonly int $id;
    protected ?User $current = null;

    /** Builds a service bound to one tenant. */
    public function __construct(private string $tenant) {}

    public function getName(
        int $id,
        bool $full = false
    ): string {
        return 'x';
    }

    protected static function make(array $a): self {}
    private function reset(): void {}
    abstract public function persist(): bool;
}

interface Jsonable
{
    public function toJson(): string;
}

trait Timestamps
{
    public function touch(): void {}
}

enum Status: string
{
    case Active = 'active';
    case Banned = 'banned';

    public function label(): string { return $this->name; }
}

function helper_fn(int $x): int { return $x; }

abstract class AbstractThing {}
`

func outlinePHP(t *testing.T, name, src string) *FileMap {
	t.Helper()
	fm, err := Outline(write(t, name, src))
	if err != nil {
		t.Fatal(err)
	}
	if fm.Fallback {
		t.Fatalf("%s fell back to the heuristic scanner instead of using the PHP grammar", name)
	}
	if fm.ParseError {
		t.Errorf("%s reported a parse error on valid source", name)
	}
	if fm.Lang != "php" {
		t.Errorf("Lang = %q, want php", fm.Lang)
	}
	return fm
}

func TestOutlinePHPTopLevelDeclarations(t *testing.T) {
	fm := outlinePHP(t, "svc.php", phpSource)

	for name, wantKind := range map[string]string{
		"App\\Service":  "namespace",
		"VERSION":       "const",
		"UserService":   "class",
		"Jsonable":      "interface",
		"Timestamps":    "trait",
		"Status":        "enum",
		"helper_fn":     "func",
		"AbstractThing": "class",
	} {
		if got := find(t, fm, name).Kind; got != wantKind {
			t.Errorf("%s kind = %q, want %q", name, got, wantKind)
		}
	}
}

// Every method in real PHP carries a visibility modifier, so a scanner that
// only recognises a bare `function` keyword sees none of them. This is the
// single biggest thing the grammar buys over the fallback.
func TestOutlinePHPMethodsWithModifiers(t *testing.T) {
	fm := outlinePHP(t, "svc.php", phpSource)

	for _, tc := range []struct{ name, wantSig string }{
		{"__construct", "public function __construct(private string $tenant)"},
		{"make", "protected static function make(array $a): self"},
		{"reset", "private function reset(): void"},
		{"persist", "abstract public function persist(): bool;"},
		{"toJson", "public function toJson(): string;"},
		{"touch", "public function touch(): void"},
		{"label", "public function label(): string"},
	} {
		s := find(t, fm, tc.name)
		if s.Kind != "method" {
			t.Errorf("%s kind = %q, want method", tc.name, s.Kind)
		}
		if s.Signature != tc.wantSig {
			t.Errorf("%s signature = %q, want %q", tc.name, s.Signature, tc.wantSig)
		}
		if s.Depth != 1 {
			t.Errorf("%s depth = %d, want 1 (nested in its declaration)", tc.name, s.Depth)
		}
	}
}

// Properties, class constants and enum cases are deliberately absent: a class
// with twenty one-line properties would bury its methods, and the declaration's
// own range already covers them.
func TestOutlinePHPSkipsFieldsAndCases(t *testing.T) {
	fm := outlinePHP(t, "svc.php", phpSource)

	for _, unwanted := range []string{"ROLE", "id", "current", "Active", "Banned"} {
		for _, s := range fm.Symbols {
			if s.Name == unwanted {
				t.Errorf("outline lists %q (%s); properties, class constants and enum cases are not declarations worth a line", unwanted, s.Kind)
			}
		}
	}
}

// A docblock ends with a line holding only "*/". Stripping the leading star
// before the closing marker leaves a bare "/" that lands in the doc text, and
// PHP puts a docblock on nearly everything.
func TestOutlinePHPDocBlockDropsClosingMarker(t *testing.T) {
	fm := outlinePHP(t, "svc.php", phpSource)

	if got := find(t, fm, "UserService").Doc; got != "UserService resolves users and formats them for the API." {
		t.Errorf("UserService doc = %q", got)
	}
	if got := find(t, fm, "__construct").Doc; got != "Builds a service bound to one tenant." {
		t.Errorf("__construct doc = %q", got)
	}
	for _, s := range fm.Symbols {
		if strings.HasSuffix(s.Doc, "/") {
			t.Errorf("%s doc = %q, ends with a stray comment marker", s.Name, s.Doc)
		}
	}
}

// The doc comment is part of the declaration's range, so a reader jumping to it
// gets the explanation along with the code.
func TestOutlinePHPDocIncludedInRange(t *testing.T) {
	fm := outlinePHP(t, "svc.php", phpSource)

	// Line 10 opens the docblock, 13 is the class.
	if got := find(t, fm, "UserService").StartLine; got != 10 {
		t.Errorf("UserService StartLine = %d, want 10 (docblock included)", got)
	}
}

// PHP states each import as its own statement, so a file with twenty of them
// would otherwise spend twenty lines of the outline saying "import block".
func TestOutlinePHPMergesImports(t *testing.T) {
	fm := outlinePHP(t, "svc.php", phpSource)

	imports := 0
	for _, s := range fm.Symbols {
		if s.Kind == "import" {
			imports++
			if s.StartLine != 5 || s.EndLine != 6 {
				t.Errorf("import block = %d-%d, want 5-6", s.StartLine, s.EndLine)
			}
		}
	}
	if imports != 1 {
		t.Errorf("got %d import symbols, want 1 merged block", imports)
	}
}

// `namespace App { ... }` nests every declaration one level down. Without the
// wrapped patterns the file would outline to a single namespace line.
func TestOutlinePHPBracedNamespace(t *testing.T) {
	fm := outlinePHP(t, "braced.php", `<?php

namespace App\Http {
    use App\Kernel;

    const MODE = 'strict';

    class Controller
    {
        public function handle(): void {}
    }

    function boot(): void {}
}
`)

	for _, name := range []string{"Controller", "handle", "boot", "MODE"} {
		find(t, fm, name)
	}
	if got := find(t, fm, "Controller").Depth; got != 1 {
		t.Errorf("Controller depth = %d, want 1 (inside the namespace block)", got)
	}
	if got := find(t, fm, "handle").Depth; got != 2 {
		t.Errorf("handle depth = %d, want 2 (inside the class, inside the namespace)", got)
	}
}

// A template returns to HTML between blocks. The `php` grammar reads a file as
// text that opens into code at <?php, so this parses where `php_only` would not.
func TestOutlinePHPTemplateWithInlineHTML(t *testing.T) {
	fm := outlinePHP(t, "view.phtml", `<div class="row">
<?php

function render_row(array $cells): string
{
    return implode('', $cells);
}

?>
<span><?= render_row($cells) ?></span>
</div>
`)

	if got := find(t, fm, "render_row").Kind; got != "func" {
		t.Errorf("render_row kind = %q, want func", got)
	}
}

// A declaration inside a function body or a conditional is not part of the
// file's structure, and listing it is the noise an outline exists to avoid.
func TestOutlinePHPSkipsNestedDeclarations(t *testing.T) {
	fm := outlinePHP(t, "nested.php", `<?php

function outer(): void
{
    function inner(): void {}

    class Local {}
}
`)

	find(t, fm, "outer")
	for _, s := range fm.Symbols {
		if s.Name == "inner" || s.Name == "Local" {
			t.Errorf("outline lists %q, declared inside a function body", s.Name)
		}
	}
}

// A multi-line parameter list is collapsed rather than cut at the first break,
// so the whole signature stays readable on one line.
func TestOutlinePHPMultiLineSignature(t *testing.T) {
	fm := outlinePHP(t, "svc.php", phpSource)

	if got := find(t, fm, "getName").Signature; got != "public function getName( int $id, bool $full = false ): string" {
		t.Errorf("getName signature = %q", got)
	}
}
