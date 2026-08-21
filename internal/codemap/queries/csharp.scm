; Outline queries for C#.
;
; Two departures from the grammar's own queries/tags.scm, matching the calls
; already made for Go, TypeScript, PHP, Python, Rust, Swift and Java:
;
;   1. tags.scm also emits @reference.call and @reference.type for every call
;      site and type mention. That exists for cross-file navigation; here it
;      would bury the declarations an outline is for.
;
;   2. Each pattern is anchored under its container, so a local class declared
;      inside a method body stays out — the same reason the Go queries anchor
;      under (source_file).
;
; Attributes need no handling of their own. C# parses `[Serializable] public`
; into an (attribute_list) inside the declaration, so the range and the
; signature both cover it already — as in Java and Swift, and unlike Python's
; decorators or Rust's attributes, which sit outside the node.
;
; A file's types can sit at either of two depths. The block form
; `namespace X { ... }` nests them in a declaration_list, while the file-scoped
; form `namespace X;` leaves them as siblings of the namespace at the top of the
; compilation unit. Both spellings are current, so both are matched throughout.

; --- top level -------------------------------------------------------------
(compilation_unit (using_directive) @def.import)

(compilation_unit (namespace_declaration) @def.namespace)
(compilation_unit (file_scoped_namespace_declaration) @def.namespace)

(compilation_unit (class_declaration) @def.class)
(compilation_unit (struct_declaration) @def.struct)
(compilation_unit (interface_declaration) @def.interface)
(compilation_unit (enum_declaration) @def.enum)
(compilation_unit (record_declaration) @def.record)
(compilation_unit (delegate_declaration) @def.delegate)

; Top-level statements are the shape of a modern Program.cs, where the entry
; point has no enclosing class at all. Without this a .NET 6+ console app or
; minimal API outlines to nothing but its using directives.
(compilation_unit (global_statement (local_function_statement) @def.func))

; --- types below the top level ---------------------------------------------
; A nested type is structure, not noise, and C# leans on it for options classes,
; builders and private state machines. Same call as the nested types kept for
; Java, Python and Swift.
;
; declaration_list is the body of a block namespace and of every type that can
; hold one, so this single set of patterns covers both depths at once: a class
; inside `namespace X { }` and a class inside another class match the same
; pattern. Adding namespace-anchored copies would double-capture — every type in
; a block-namespace file would be listed twice.
;
; A local type declared inside a method body sits in a (block), not a
; declaration_list, so it stays out without needing an anchor.
(declaration_list (namespace_declaration) @def.namespace)
(declaration_list (class_declaration) @def.class)
(declaration_list (struct_declaration) @def.struct)
(declaration_list (interface_declaration) @def.interface)
(declaration_list (enum_declaration) @def.enum)
(declaration_list (record_declaration) @def.record)
(declaration_list (delegate_declaration) @def.delegate)

; --- members ---------------------------------------------------------------
; Methods are where the code lives; a constructor is how the type is built; an
; operator, an indexer and a conversion are all called like methods and read
; like them here.
;
; A destructor is kept for the same reason a constructor is: in the rare type
; that declares one, it is doing resource work a reader needs to find.
(declaration_list (method_declaration) @def.method)
(declaration_list (constructor_declaration) @def.method)
(declaration_list (destructor_declaration) @def.method)
(declaration_list (operator_declaration) @def.method)
(declaration_list (conversion_operator_declaration) @def.method)
(declaration_list (indexer_declaration) @def.method)

; Properties are listed, where Java's fields and TypeScript's field_definitions
; are not. This is a real departure, and C# earns it: the language deliberately
; replaced the public field with the property, so a type's properties *are* its
; public surface. Dropping them would leave the outline of a typical class
; missing most of its API — the same loss as dropping its methods, not the
; noise-reduction that skipping Java's private fields buys.
;
; Auto-properties and bodied ones are treated alike. Splitting them would mean
; `Total => _items.Sum()` appears and `Name { get; set; }` does not, and a rule
; a reader cannot see is worse than either answer applied consistently.
(declaration_list (property_declaration) @def.property)

; Fields and events stay out, on the Java reasoning: a class with twenty
; one-line fields would bury its methods, and the type's own range already
; covers them. Enum members stay out for the same reason.
