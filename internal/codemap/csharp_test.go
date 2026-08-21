package codemap

import "testing"

const csharpSource = `using System;
using System.Threading.Tasks;

namespace Acme.Billing;

/// <summary>Charges a card.</summary>
[Serializable]
public class Charger : ICharger
{
    private readonly ILogger _log;
    public const int MaxRetries = 3;

    /// <summary>The currency in use.</summary>
    public string Currency { get; set; }

    public decimal Total => _items.Sum();

    public Charger(ILogger log) { _log = log; }

    ~Charger() { }

    /// <summary>Runs the charge.</summary>
    public async Task<Result> ChargeAsync(Card card, decimal amount)
    {
        return await Task.FromResult(Result.Ok);
    }

    public static Charger operator +(Charger a, Charger b) => a;

    public string this[int i] => _items[i];

    public event EventHandler Charged;

    public enum Mode { Fast, Slow }

    private struct Inner { public int X; }
}

public record Money(decimal Amount, string Currency);

public readonly record struct Point(int X, int Y);

public delegate void Handler(object sender);

public interface ICharger
{
    Task<Result> ChargeAsync(Card card, decimal amount);
}
`

func outlineCSharp(t *testing.T, name, src string) *FileMap {
	t.Helper()
	fm, err := Outline(write(t, name, src))
	if err != nil {
		t.Fatal(err)
	}
	if fm.Fallback {
		t.Fatalf("%s fell back to the heuristic scanner instead of using the C# grammar", name)
	}
	if fm.ParseError {
		t.Errorf("%s reported a parse error on valid source", name)
	}
	if fm.Lang != "c#" {
		t.Errorf("Lang = %q, want c#", fm.Lang)
	}
	return fm
}

func TestOutlineCSharpTopLevelDeclarations(t *testing.T) {
	fm := outlineCSharp(t, "Charger.cs", csharpSource)

	for _, tc := range []struct{ name, kind string }{
		{"Charger", "class"},
		{"Money", "record"},
		{"Point", "record"},
		{"Handler", "delegate"},
		{"ICharger", "interface"},
	} {
		findKind(t, fm, tc.name, tc.kind)
	}

	// `record struct` is a record_declaration like a plain record, and the
	// signature is where the difference shows.
	if sig := findKind(t, fm, "Point", "record").Signature; sig != "public readonly record struct Point(int X, int Y);" {
		t.Errorf("Point signature = %q", sig)
	}
}

// A file-scoped namespace declares the namespace and stops. Its types are
// siblings at the top of the compilation unit rather than nested inside it, so
// they must not be indented under it.
func TestOutlineCSharpFileScopedNamespace(t *testing.T) {
	fm := outlineCSharp(t, "Charger.cs", csharpSource)

	ns := atLine(t, fm, 4)
	if ns.Kind != "namespace" || ns.Signature != "namespace Acme.Billing;" {
		t.Errorf("namespace = %q / %q", ns.Kind, ns.Signature)
	}
	if ns.Depth != 0 {
		t.Errorf("namespace Depth = %d, want 0", ns.Depth)
	}
	if charger := findKind(t, fm, "Charger", "class"); charger.Depth != 0 {
		t.Errorf("Charger Depth = %d, want 0 — a file-scoped namespace encloses nothing", charger.Depth)
	}
}

// The block form does enclose its types. Both spellings are current C#, and the
// outline has to indent one and not the other.
func TestOutlineCSharpBlockNamespace(t *testing.T) {
	fm := outlineCSharp(t, "Old.cs", `using System;

namespace Acme.Legacy
{
    /// <summary>Old shape.</summary>
    public class Old
    {
        public string Name { get; set; }
        public void Go() { }
    }

    public enum Color { Red, Green }
}
`)

	ns := findKind(t, fm, "Acme.Legacy", "namespace")
	if ns.Depth != 0 {
		t.Errorf("namespace Depth = %d, want 0", ns.Depth)
	}
	old := findKind(t, fm, "Old", "class")
	if old.Depth != 1 {
		t.Errorf("Old Depth = %d, want 1 — a block namespace encloses its types", old.Depth)
	}
	if old.Doc != "Old shape." {
		t.Errorf("Old doc = %q", old.Doc)
	}
	if findKind(t, fm, "Go", "method").Depth != 2 {
		t.Error("a method inside a class inside a namespace should sit at depth 2")
	}
}

// The block-namespace patterns and the nested-type patterns both match on
// declaration_list. Anchoring a second copy under namespace_declaration would
// list every type in such a file twice.
func TestOutlineCSharpNoDuplicateSymbols(t *testing.T) {
	fm := outlineCSharp(t, "Old.cs", `namespace A
{
    public class One { }
    public class Two { }
}
`)

	seen := map[string]int{}
	for _, s := range fm.Symbols {
		seen[s.Name]++
	}
	for name, n := range seen {
		if n > 1 {
			t.Errorf("%s appears %d times in the outline, want once", name, n)
		}
	}
}

// C# replaced the public field with the property, so a type's properties are
// its public surface — the reason they are listed where Java's fields are not.
// Fields, events and enum members stay out.
func TestOutlineCSharpListsPropertiesNotFields(t *testing.T) {
	fm := outlineCSharp(t, "Charger.cs", csharpSource)

	currency := findKind(t, fm, "Currency", "property")
	if currency.Doc != "The currency in use." {
		t.Errorf("Currency doc = %q", currency.Doc)
	}
	// An expression-bodied property is listed on the same terms as an
	// auto-property; a rule a reader cannot see would be worse than either.
	findKind(t, fm, "Total", "property")

	for _, name := range []string{"_log", "MaxRetries", "Charged", "Fast", "Slow"} {
		for _, s := range fm.Symbols {
			if s.Name == name {
				t.Errorf("%s is a field, event or enum member and should stay out of the outline", name)
			}
		}
	}
}

// Everything called like a method reads like one here: constructors,
// destructors, operators and indexers included.
func TestOutlineCSharpMethodLikeMembers(t *testing.T) {
	fm := outlineCSharp(t, "Charger.cs", csharpSource)

	charge := findKind(t, fm, "ChargeAsync", "method")
	if charge.Signature != "public async Task<Result> ChargeAsync(Card card, decimal amount)" {
		t.Errorf("ChargeAsync signature = %q", charge.Signature)
	}
	if charge.Doc != "Runs the charge." {
		t.Errorf("ChargeAsync doc = %q", charge.Doc)
	}

	// The constructor, the destructor, the operator and the indexer each have to
	// have produced a symbol, on the source line they start on.
	for _, tc := range []struct {
		line int
		sig  string
	}{
		{18, "public Charger(ILogger log)"},
		{20, "~Charger()"},
		{28, "public static Charger operator +(Charger a, Charger b)"},
		{30, "public string this[int i] => _items[i];"},
	} {
		s := atLine(t, fm, tc.line)
		if s.Kind != "method" {
			t.Errorf("symbol at line %d = kind %q, want method", tc.line, s.Kind)
		}
		if s.Signature != tc.sig {
			t.Errorf("line %d signature = %q, want %q", tc.line, s.Signature, tc.sig)
		}
	}
}

// An operator and an indexer bind no identifier — C# names them by the symbol
// they overload. Java's static initialiser is excluded for exactly that reason,
// so the call to keep these needs its own justification: their signatures carry
// the whole declaration, so the line reads as `public string this[int i]`
// rather than as the blank entry the Java rule was avoiding.
func TestOutlineCSharpUnnamedMembersStillRender(t *testing.T) {
	fm := outlineCSharp(t, "Charger.cs", csharpSource)

	for _, line := range []int{28, 30} {
		s := atLine(t, fm, line)
		if s.Name != "" {
			t.Errorf("line %d Name = %q, expected the declaration to bind none", line, s.Name)
		}
		if s.Signature == "" {
			t.Errorf("line %d has neither a name nor a signature, so it renders as a blank line", line)
		}
	}
}

// C# parses [Serializable] into an attribute_list inside the declaration, so
// the attribute lands in the range and the signature without the walking that
// Python's decorators and Rust's attributes need.
func TestOutlineCSharpAttributesAreInsideTheDeclaration(t *testing.T) {
	fm := outlineCSharp(t, "Charger.cs", csharpSource)

	charger := findKind(t, fm, "Charger", "class")
	if charger.Signature != "[Serializable] public class Charger : ICharger" {
		t.Errorf("Charger signature = %q, want the attribute kept", charger.Signature)
	}
	// Line 6 is the doc comment, 7 the attribute, 8 the class.
	if charger.StartLine != 6 {
		t.Errorf("Charger StartLine = %d, want 6 (doc comment included)", charger.StartLine)
	}
	if charger.Doc != "Charges a card." {
		t.Errorf("Charger doc = %q", charger.Doc)
	}
}

// A modern Program.cs has no enclosing class at all. Without the top-level
// statement pattern such a file outlines to nothing but its using directives.
func TestOutlineCSharpTopLevelStatements(t *testing.T) {
	fm := outlineCSharp(t, "Program.cs", `using System;

Console.WriteLine("starting");

void Configure(WebApplicationBuilder b) { }

record Options(string Path);
`)

	fn := findKind(t, fm, "Configure", "func")
	if fn.Signature != "void Configure(WebApplicationBuilder b)" {
		t.Errorf("Configure signature = %q", fn.Signature)
	}
	findKind(t, fm, "Options", "record")
}

// A local type declared inside a method body is an implementation detail. It
// sits in a (block) rather than a declaration_list, which is what keeps it out.
func TestOutlineCSharpSkipsLocalDeclarations(t *testing.T) {
	fm := outlineCSharp(t, "Local.cs", `public class Outer
{
    public void Run()
    {
        var x = 1;
        void Helper() { }
    }
}
`)

	for _, s := range fm.Symbols {
		if s.Name == "Helper" || s.Name == "x" {
			t.Errorf("%s is local to a method body and should stay out of the outline", s.Name)
		}
	}
}

// The XML documentation convention would otherwise defeat the doc excerpt: the
// one-line form spends a quarter of the budget on its tags, and the block form
// leads with a bare <summary> that says nothing at all.
func TestOutlineCSharpXMLDocSummary(t *testing.T) {
	fm := outlineCSharp(t, "Payments.cs", `public class Payments
{
    /// <summary>
    /// Charges the <see cref="T:Acme.Card"/> for <paramref name="amount"/>.
    /// </summary>
    /// <param name="card">The card to charge.</param>
    /// <returns>A result.</returns>
    public Result Charge(Card card, decimal amount) => Result.Ok;

    /// <summary>Compares if a &lt; b.</summary>
    public bool Less(int a, int b) => a < b;

    // A plain comment, not XML at all.
    public void Plain() { }

    /// No summary element, just prose.
    public void Bare() { }
}
`)

	for _, tc := range []struct{ name, doc string }{
		// The summary is taken and the param and returns blocks dropped: they
		// document what the signature beside them already shows. An inline
		// element is unwrapped to the name it stood for, its cref kind prefix
		// removed, with no space stranded before the full stop.
		{"Charge", "Charges the Acme.Card for amount."},
		// An escape has to come back as the character it stands for, or the
		// markup this strips reappears as an entity.
		{"Less", "Compares if a < b."},
		// A comment that is not XML at all must pass through untouched.
		{"Plain", "A plain comment, not XML at all."},
		// So must a doc comment that simply has no summary element.
		{"Bare", "No summary element, just prose."},
	} {
		if got := findKind(t, fm, tc.name, "method").Doc; got != tc.doc {
			t.Errorf("%s doc = %q, want %q", tc.name, got, tc.doc)
		}
	}
}

// Prose is allowed to contain a bare "<" — a generic written out, a comparison
// left unescaped. It must not be mistaken for the start of a tag and swallow
// the rest of the line.
func TestSummaryFromXMLDocKeepsUnclosedAngleBrackets(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Returns a List<T of things", "Returns a List<T of things"},
		{"<summary>Wraps <c>Foo</c> nicely.</summary>", "Wraps Foo nicely."},
		{"<summary>Unclosed summary", "Unclosed summary"},
		{"No markup at all", "No markup at all"},
		{"<summary>Escaped &amp;lt; stays escaped.</summary>", "Escaped &lt; stays escaped."},
	} {
		if got := summaryFromXMLDoc(tc.in); got != tc.want {
			t.Errorf("summaryFromXMLDoc(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
